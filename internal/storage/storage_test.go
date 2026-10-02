package storage

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// importGoal adds a Goal with one node the only way state can now gain one: a
// Goal Plan import.
func importGoal(state *work.State, root, id string) error {
	plan := work.GoalPlan{
		Goal:  work.PlanGoal{ID: id, Title: "Goal " + id},
		Nodes: []work.PlanNode{{ID: id + "-node", Story: "specs/stories/" + id}},
	}
	_, err := state.ImportGoalPlan(plan, root, time.Now().UTC())
	return err
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestInitRetryAndFailedWritePreserveState(t *testing.T) {
	root := repositoryRoot(t)
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	if err := Update(root, func(state *work.State) error { return importGoal(state, root, "g") }); err != nil {
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
	err = Update(root, func(state *work.State) error { return importGoal(state, root, "other") })
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

// currentShape is a complete, valid schema-19 document. Each rejection fixture
// below is this document with only the version changed, and the test first
// loads it unchanged: a fixture that fails for any other reason (an unknown
// field, a missing counter) would "reject" without testing the version at all.
const currentShape = `{"schema_version":%d,"next_evidence_id":1,"next_gate_id":1,"next_verification_run_id":1,"goals":[],"work_items":[],"evidence":[],"gates":[]}`

func stateWithVersion(version int) string { return fmt.Sprintf(currentShape, version) }

func TestLoadRejectsCorruptOlderAndNewerState(t *testing.T) {
	root := repositoryRoot(t)
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, stateDirectory, "state.json")
	write := func(contents string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}

	write(stateWithVersion(work.SchemaVersion))
	if _, err := Load(root); err != nil {
		t.Fatalf("the current-shape document does not load, so the fixtures below prove nothing: %v", err)
	}

	write("{")
	if _, err := Load(root); err == nil {
		t.Fatal("accepted corrupt state")
	}

	write(stateWithVersion(work.SchemaVersion + 1))
	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "newer") || strings.Contains(err.Error(), "export-plan") {
		t.Fatalf("newer schema: %v; want a refusal that says newer and does not suggest exporting", err)
	}

	for _, version := range []int{work.SchemaVersion - 1, 1} {
		write(stateWithVersion(version))
		_, err = Load(root)
		if err == nil || !strings.Contains(err.Error(), "export-plan") {
			t.Fatalf("schema %d: %v; want a refusal naming the export tool", version, err)
		}
	}

	// A real v18 document carries fields this shape does not know. The refusal
	// must still be about the version, not "unknown field".
	write(`{"schema_version":18,"next_work_id":2,"next_evidence_id":1,"next_gate_id":1,"next_verification_run_id":1,"goals":[{"id":"g","execution":{}}],"work_items":[{"id":"WI-001","external_ref":"x"}],"evidence":[],"gates":[]}`)
	_, err = Load(root)
	if err == nil || !strings.Contains(err.Error(), "schema version 18") || strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("v18 document: %v; want a schema-version refusal", err)
	}

	// Update takes the same path and must leave the file alone.
	before, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if err := Update(root, func(*work.State) error { return nil }); err == nil {
		t.Fatal("Update accepted a v18 state")
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("a refused Update rewrote the old state")
	}
}

func TestInitWritesTheCurrentSchema(t *testing.T) {
	root := repositoryRoot(t)
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	state, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if state.SchemaVersion != work.SchemaVersion || work.SchemaVersion != 19 {
		t.Fatalf("init wrote schema %d, SchemaVersion is %d; want 19", state.SchemaVersion, work.SchemaVersion)
	}
}

func TestLockReleasedAfterProcessExit(t *testing.T) {
	if directory := os.Getenv("FORGEPILOT_LOCK_HOLDER"); directory != "" {
		_ = withLock(directory, func() error { os.Exit(0); return nil })
		return
	}
	root := repositoryRoot(t)
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
	if err := Update(root, func(state *work.State) error { return importGoal(state, root, "g") }); err != nil {
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
