package storage

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/carl/forgepilot/internal/work"
)

func TestInitRetryAndFailedWritePreserveState(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	if err := Update(root, func(state *work.State) error { return state.AddGoal("g", "Goal", "", root, time.Now().UTC()) }); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, stateDirectory, "state.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, stateDirectory)
	if err := os.Chmod(directory, 0555); err != nil {
		t.Fatal(err)
	}
	err = Update(root, func(state *work.State) error { return state.AddGoal("other", "Other", "", root, time.Now().UTC()) })
	if restoreErr := os.Chmod(directory, 0755); restoreErr != nil {
		t.Fatal(restoreErr)
	}
	if err == nil {
		t.Fatal("write succeeded in read-only state directory")
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("failed write changed state")
	}
}

func TestLoadRejectsCorruptAndFutureState(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, stateDirectory, "state.json")
	for _, contents := range []string{"{", `{"schema_version":3,"next_work_id":1,"next_evidence_id":1,"goals":[],"work_items":[],"evidence":[]}`} {
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(root); err == nil {
			t.Fatalf("accepted %q", contents)
		}
	}
}

func TestLockReleasedAfterProcessExit(t *testing.T) {
	if directory := os.Getenv("FORGEPILOT_LOCK_HOLDER"); directory != "" {
		_ = withLock(directory, func() error { os.Exit(0); return nil })
		return
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, stateDirectory)
	command := exec.Command(os.Args[0], "-test.run=TestLockReleasedAfterProcessExit")
	command.Env = append(os.Environ(), "FORGEPILOT_LOCK_HOLDER="+directory)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("holder: %v: %s", err, output)
	}
	if err := withLock(directory, func() error { return nil }); err != nil {
		t.Fatalf("reacquire lock: %v", err)
	}
}

func TestMovedRepositoryIsRejected(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "original")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	if err := Update(root, func(state *work.State) error { return state.AddGoal("g", "Goal", "", root, time.Now().UTC()) }); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(base, "moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(moved); err == nil {
		t.Fatal("accepted state after repository move")
	}
}

// legacyState writes a schema v1 snapshot, the shape M1 left behind.
func legacyState(t *testing.T, root string) string {
	t.Helper()
	canonical, err := canonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	contents := `{"schema_version":1,"next_work_id":3,"goals":[{"id":"g","title":"Goal","description":"","repository":"` +
		canonical + `","status":"ACTIVE","created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"}],` +
		`"work_items":[{"id":"WI-001","goal_id":"g","story_ref":"specs/stories/a","status":"RUNNING","depends_on":null,` +
		`"created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"},` +
		`{"id":"WI-002","goal_id":"g","story_ref":"specs/stories/b","status":"PENDING","depends_on":["WI-001"],` +
		`"created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(root, stateDirectory, "state.json"), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return contents
}

func TestMigrateUpgradesLegacyStateAndKeepsBackup(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	original := legacyState(t, root)

	_, err := Load(root)
	if err == nil {
		t.Fatal("read a v1 state without migrating")
	}
	if !strings.Contains(err.Error(), "migrate") {
		t.Fatalf("error %q does not tell the user to migrate", err)
	}

	migrated, err := Migrate(root)
	if err != nil || !migrated {
		t.Fatalf("Migrate = %v, %v", migrated, err)
	}
	state, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Goals) != 1 || len(state.WorkItems) != 2 || state.NextWorkID != 3 {
		t.Fatalf("migration lost data: %#v", state)
	}
	if state.WorkItems[1].DependsOn[0] != "WI-001" || state.WorkItems[0].Status != work.Running {
		t.Fatalf("migration changed work items: %#v", state.WorkItems)
	}
	if state.NextEvidenceID != 1 || len(state.Evidence) != 0 || state.WorkItems[0].CurrentRun != nil {
		t.Fatalf("migration did not initialise v2 fields: %#v", state)
	}

	backup, err := os.ReadFile(filepath.Join(root, stateDirectory, "state.json.v1.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != original {
		t.Fatal("backup does not hold the original snapshot")
	}

	// Already current: report no upgrade, succeed, change nothing.
	before, err := os.ReadFile(filepath.Join(root, stateDirectory, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	migrated, err = Migrate(root)
	if err != nil || migrated {
		t.Fatalf("second Migrate = %v, %v", migrated, err)
	}
	after, err := os.ReadFile(filepath.Join(root, stateDirectory, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("migrating an up-to-date state rewrote it")
	}

	// An existing backup must never be overwritten.
	legacyState(t, root)
	if _, err := Migrate(root); err == nil {
		t.Fatal("overwrote an existing backup")
	}
	current, err := os.ReadFile(filepath.Join(root, stateDirectory, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != original {
		t.Fatal("refused migration still modified the state")
	}
}
