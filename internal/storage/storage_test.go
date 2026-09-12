package storage

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/work"
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
	// The second fixture must name a schema version this binary does not yet
	// support. It has to be raised with every bump: left behind, it silently
	// stops testing rejection and starts testing that a valid state loads.
	for _, contents := range []string{"{", `{"schema_version":8,"next_work_id":1,"next_evidence_id":1,"next_gate_id":1,"goals":[],"work_items":[],"evidence":[],"gates":[]}`} {
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(root); err == nil {
			t.Fatalf("accepted %q", contents)
		}
	}
}

func TestRuntimeMetadataIsDefensivelyCopiedBeforeSaving(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	runtime := map[string]string{"go": "1.25.5"}
	if err := Update(root, func(state *work.State) error {
		if err := state.AddGoal("g", "Goal", "", canonicalRoot, now); err != nil {
			return err
		}
		item, err := state.AddWork("g", "specs/stories/a", nil, now)
		if err != nil {
			return err
		}
		if err := state.Start(item.ID, now); err != nil {
			return err
		}
		if err := state.BeginCandidateVerificationWithRuntime(item.ID, work.Candidate{Kind: work.CommitCandidate, Revision: "abc123"}, "/tmp/wt", "", runtime, now); err != nil {
			return err
		}
		runtime["go"] = "mutated-before-save"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	state, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItems[0].CurrentRun.Runtime["go"]; got != "1.25.5" {
		t.Fatalf("saved runtime = %q, want defensive copy", got)
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

// v2State writes a schema v2 snapshot carrying accumulated Evidence, the shape
// M2 left behind.
func v2State(t *testing.T, root string) string {
	t.Helper()
	canonical, err := canonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	contents := `{"schema_version":2,"next_work_id":3,"next_evidence_id":2,` +
		`"goals":[{"id":"g","title":"Goal","description":"","repository":"` + canonical +
		`","status":"ACTIVE","created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"}],` +
		`"work_items":[{"id":"WI-001","goal_id":"g","story_ref":"specs/stories/a","status":"REVIEW","depends_on":null,` +
		`"current_run":null,"created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"},` +
		`{"id":"WI-002","goal_id":"g","story_ref":"specs/stories/b","status":"PENDING","depends_on":["WI-001"],` +
		`"current_run":null,"created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"}],` +
		`"evidence":[{"id":"EV-001","type":"verification","repository":"` + canonical +
		`","work_item_id":"WI-001","story_ref":"specs/stories/a","revision":"abc123","command":"make verify",` +
		`"exit_code":0,"result":"PASS","created_at":"2026-09-07T00:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(root, stateDirectory, "state.json"), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return contents
}

func TestMigrateUpgradesV2StateAndKeepsEvidence(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	original := v2State(t, root)

	if _, err := Load(root); err == nil {
		t.Fatal("read a v2 state without migrating")
	} else if !strings.Contains(err.Error(), "migrate") {
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
	if state.SchemaVersion != work.SchemaVersion || state.NextWorkID != 3 || state.NextEvidenceID != 2 {
		t.Fatalf("migration lost counters: %#v", state)
	}
	if len(state.Goals) != 1 || len(state.WorkItems) != 2 || len(state.Evidence) != 1 {
		t.Fatalf("migration lost data: %#v", state)
	}
	if got := state.Evidence[0]; got.ID != "EV-001" || got.Result != work.Pass || got.Revision != "abc123" || got.ExitCode == nil || *got.ExitCode != 0 {
		t.Fatalf("migration changed evidence: %#v", got)
	}
	if state.WorkItems[1].DependsOn[0] != "WI-001" || state.WorkItems[0].Status != work.Review {
		t.Fatalf("migration changed work items: %#v", state.WorkItems)
	}
	// The v3 containers exist and start empty: nothing writes to them yet.
	if state.NextGateID != 1 || len(state.Gates) != 0 {
		t.Fatalf("migration did not initialise v3 fields: %#v", state)
	}

	backup, err := os.ReadFile(filepath.Join(root, stateDirectory, "state.json.v2.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != original {
		t.Fatal("backup does not hold the original snapshot")
	}
}

// TestMigrateWalksEveryVersionInOneStep proves a snapshot left behind by M1
// still reaches the current schema: the user who skipped a release migrates
// once, not once per version they missed.
func TestMigrateWalksEveryVersionInOneStep(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	legacyState(t, root)
	if migrated, err := Migrate(root); err != nil || !migrated {
		t.Fatalf("Migrate = %v, %v", migrated, err)
	}
	state, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if state.SchemaVersion != work.SchemaVersion || state.NextEvidenceID != 1 || state.NextGateID != 1 {
		t.Fatalf("v1 snapshot did not reach the current schema: %#v", state)
	}
	if len(state.WorkItems) != 2 {
		t.Fatalf("migration lost work items: %#v", state.WorkItems)
	}
}

// TestMigrateRefusesToDiscardWhatAStepWouldCreate guards the one way a version
// header can lie: it is a claim about the file, and a hand-edited header on a
// newer snapshot would otherwise send the migration through a step that empties
// containers the file already fills.
func TestMigrateRefusesToDiscardWhatAStepWouldCreate(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	// A current snapshot carrying evidence, with its version header rewound.
	v2State(t, root)
	if migrated, err := Migrate(root); err != nil || !migrated {
		t.Fatalf("Migrate = %v, %v", migrated, err)
	}
	path := filepath.Join(root, stateDirectory, "state.json")
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rewound := strings.Replace(string(current), `"schema_version": 7`, `"schema_version": 1`, 1)
	if rewound == string(current) {
		t.Fatalf("failed to rewind the version header of %s", current)
	}
	if err := os.WriteFile(path, []byte(rewound), 0600); err != nil {
		t.Fatal(err)
	}
	// The v1 backup from the first migration is gone, so nothing blocks on that.
	if err := os.Remove(filepath.Join(root, stateDirectory, "state.json.v2.bak")); err != nil {
		t.Fatal(err)
	}

	if _, err := Migrate(root); err == nil {
		t.Fatal("migrated a snapshot whose header understated what it carries")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != rewound {
		t.Fatal("a refused migration still modified the state")
	}
}

// v3State writes an M3-era snapshot: evidence and gates both populated, so a
// migration that dropped either would be caught rather than merely suspected.
func v3State(t *testing.T, root string) string {
	t.Helper()
	canonical, err := canonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	contents := `{"schema_version":3,"next_work_id":3,"next_evidence_id":3,"next_gate_id":2,` +
		`"goals":[{"id":"g","title":"Goal","description":"","repository":"` + canonical +
		`","status":"ACTIVE","reason":"","created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"}],` +
		`"work_items":[{"id":"WI-001","goal_id":"g","story_ref":"specs/stories/a","status":"REVIEW","depends_on":null,` +
		`"current_run":null,"created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"},` +
		`{"id":"WI-002","goal_id":"g","story_ref":"specs/stories/b","status":"PENDING","depends_on":["WI-001"],` +
		`"current_run":null,"created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"}],` +
		`"evidence":[{"id":"EV-001","type":"verification","repository":"` + canonical +
		`","work_item_id":"WI-001","story_ref":"specs/stories/a","revision":"abc123","command":"make verify",` +
		`"exit_code":0,"result":"PASS","reviewer":"","note":"","created_at":"2026-09-07T00:00:00Z"},` +
		`{"id":"EV-002","type":"review","repository":"` + canonical +
		`","work_item_id":"WI-001","story_ref":"specs/stories/a","revision":"abc123","command":"",` +
		`"exit_code":null,"result":"REJECTED","reviewer":"carl@example.com","note":"not yet",` +
		`"created_at":"2026-09-07T00:00:00Z"}],` +
		`"gates":[{"id":"GATE-001","work_item_id":"WI-001","question":"which?","rationale":"",` +
		`"options":["a","b"],"status":"RESOLVED","choice":"a","note":"","reason":"",` +
		`"decided_by":"carl@example.com","opened_at":"2026-09-07T00:00:00Z",` +
		`"decided_at":"2026-09-07T00:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(root, stateDirectory, "state.json"), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return contents
}

// TestMigrateUpgradesV3StateAndKeepsEverything covers the step that changes no
// data at all. The ceremony still has to happen: the contract the user works to
// is "upgrading means a backup and a version bump", not "sometimes there is a
// backup".
func TestMigrateUpgradesV3StateAndKeepsEverything(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	original := v3State(t, root)

	if _, err := Load(root); err == nil {
		t.Fatal("read a v3 state without migrating")
	} else if !strings.Contains(err.Error(), "migrate") {
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
	if state.SchemaVersion != work.SchemaVersion {
		t.Fatalf("migration did not reach the current schema: %#v", state)
	}
	if state.NextWorkID != 3 || state.NextEvidenceID != 3 || state.NextGateID != 2 {
		t.Fatalf("migration lost counters: %#v", state)
	}
	if len(state.Goals) != 1 || len(state.WorkItems) != 2 || len(state.Evidence) != 2 || len(state.Gates) != 1 {
		t.Fatalf("migration lost data: %#v", state)
	}
	if got := state.Evidence[1]; got.Result != work.Rejected || got.Reviewer != "carl@example.com" || got.PR != "" {
		t.Fatalf("migration changed review evidence: %#v", got)
	}
	if got := state.Gates[0]; got.ID != "GATE-001" || got.Choice != "a" {
		t.Fatalf("migration changed gates: %#v", got)
	}

	backup, err := os.ReadFile(filepath.Join(root, stateDirectory, "state.json.v3.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != original {
		t.Fatal("backup does not hold the original snapshot")
	}

	// Running it again is safe and says so, so nobody has to remember whether
	// they already upgraded.
	if migrated, err := Migrate(root); err != nil || migrated {
		t.Fatalf("second Migrate = %v, %v", migrated, err)
	}
}

// v4State writes an M4-era snapshot: a resolved review carries a PR reference,
// and one Work Item has a Verification Run in flight without a log path, so a
// migration that dropped either the PR or the in-flight run would be caught.
func v4State(t *testing.T, root string) string {
	t.Helper()
	canonical, err := canonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	contents := `{"schema_version":4,"next_work_id":3,"next_evidence_id":3,"next_gate_id":2,` +
		`"goals":[{"id":"g","title":"Goal","description":"","repository":"` + canonical +
		`","status":"ACTIVE","reason":"","created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"}],` +
		`"work_items":[{"id":"WI-001","goal_id":"g","story_ref":"specs/stories/a","status":"REVIEW","depends_on":null,` +
		`"current_run":null,"created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"},` +
		`{"id":"WI-002","goal_id":"g","story_ref":"specs/stories/b","status":"VERIFYING","depends_on":["WI-001"],` +
		`"current_run":{"revision":"def456","worktree_path":"` + filepath.Join(root, ".forgepilot", "worktrees", "WI-002-def456") +
		`","started_at":"2026-09-07T00:00:00Z"},"created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"}],` +
		`"evidence":[{"id":"EV-001","type":"verification","repository":"` + canonical +
		`","work_item_id":"WI-001","story_ref":"specs/stories/a","revision":"abc123","command":"make verify",` +
		`"exit_code":0,"result":"PASS","reviewer":"","note":"","pr":"","created_at":"2026-09-07T00:00:00Z"},` +
		`{"id":"EV-002","type":"review","repository":"` + canonical +
		`","work_item_id":"WI-001","story_ref":"specs/stories/a","revision":"abc123","command":"",` +
		`"exit_code":null,"result":"APPROVED","reviewer":"carl@example.com","note":"looks good",` +
		`"pr":"CarlLee1983/ForgePilot#42","created_at":"2026-09-07T00:00:00Z"}],` +
		`"gates":[{"id":"GATE-001","work_item_id":"WI-001","question":"which?","rationale":"",` +
		`"options":["a","b"],"status":"RESOLVED","choice":"a","note":"","reason":"",` +
		`"decided_by":"carl@example.com","opened_at":"2026-09-07T00:00:00Z",` +
		`"decided_at":"2026-09-07T00:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(root, stateDirectory, "state.json"), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return contents
}

// TestMigrateUpgradesV4StateAndKeepsEverything covers the step this ticket adds:
// current_run gains a log path, but nothing existing is discarded, including a
// run that was already in flight before the log path field existed.
func TestMigrateUpgradesV4StateAndKeepsEverything(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	original := v4State(t, root)

	if _, err := Load(root); err == nil {
		t.Fatal("read a v4 state without migrating")
	} else if !strings.Contains(err.Error(), "migrate") {
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
	if state.SchemaVersion != work.SchemaVersion {
		t.Fatalf("migration did not reach the current schema: %#v", state)
	}
	if state.NextWorkID != 3 || state.NextEvidenceID != 3 || state.NextGateID != 2 {
		t.Fatalf("migration lost counters: %#v", state)
	}
	if len(state.Goals) != 1 || len(state.WorkItems) != 2 || len(state.Evidence) != 2 || len(state.Gates) != 1 {
		t.Fatalf("migration lost data: %#v", state)
	}
	if got := state.Evidence[1]; got.Result != work.Approved || got.PR != "CarlLee1983/ForgePilot#42" {
		t.Fatalf("migration changed review evidence: %#v", got)
	}
	// A run already in flight before v5 existed has no log path recorded, and
	// migration must not invent one: an empty LogPath is the honest fact that
	// this run's output was never streamed anywhere.
	run := state.WorkItems[1].CurrentRun
	if run == nil || run.Revision != "def456" || run.LogPath != "" {
		t.Fatalf("migration changed the in-flight run: %#v", run)
	}
	if got := state.Gates[0]; got.ID != "GATE-001" || got.Choice != "a" {
		t.Fatalf("migration changed gates: %#v", got)
	}

	backup, err := os.ReadFile(filepath.Join(root, stateDirectory, "state.json.v4.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != original {
		t.Fatal("backup does not hold the original snapshot")
	}

	// Running it again is safe and says so, so nobody has to remember whether
	// they already upgraded.
	if migrated, err := Migrate(root); err != nil || migrated {
		t.Fatalf("second Migrate = %v, %v", migrated, err)
	}
}

// v5State writes the immediately previous schema. Candidate identity did not
// exist yet, so migration must explicitly preserve every historical revision as
// a COMMIT candidate rather than leaving its meaning implicit.
func v5State(t *testing.T, root string) string {
	t.Helper()
	canonical, err := canonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	contents := `{"schema_version":5,"next_work_id":3,"next_evidence_id":3,"next_gate_id":1,` +
		`"goals":[{"id":"g","title":"Goal","description":"","repository":"` + canonical +
		`","status":"ACTIVE","reason":"","created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"}],` +
		`"work_items":[{"id":"WI-001","goal_id":"g","story_ref":"specs/stories/a","status":"REVIEW","depends_on":null,` +
		`"current_run":null,"created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"},` +
		`{"id":"WI-002","goal_id":"g","story_ref":"specs/stories/b","status":"VERIFYING","depends_on":null,` +
		`"current_run":{"revision":"def456","worktree_path":"` + filepath.Join(root, ".forgepilot", "worktrees", "WI-002-def456") +
		`","log_path":"` + filepath.Join(root, ".forgepilot", "logs", "WI-002-def456.log") +
		`","started_at":"2026-09-07T00:00:00Z"},"created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"}],` +
		`"evidence":[{"id":"EV-001","type":"verification","repository":"` + canonical +
		`","work_item_id":"WI-001","story_ref":"specs/stories/a","revision":"abc123","command":"make verify",` +
		`"exit_code":0,"result":"PASS","reviewer":"","note":"","pr":"","created_at":"2026-09-07T00:00:00Z"},` +
		`{"id":"EV-002","type":"review","repository":"` + canonical +
		`","work_item_id":"WI-001","story_ref":"specs/stories/a","revision":"abc123","command":"",` +
		`"exit_code":null,"result":"APPROVED","reviewer":"carl@example.com","note":"looks good",` +
		`"pr":"","created_at":"2026-09-07T00:00:00Z"}],"gates":[]}`
	if err := os.WriteFile(filepath.Join(root, stateDirectory, "state.json"), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return contents
}

func TestMigrateUpgradesV5RevisionsToExplicitCommitCandidates(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	original := v5State(t, root)

	migrated, err := Migrate(root)
	if err != nil || !migrated {
		t.Fatalf("Migrate = %v, %v", migrated, err)
	}
	state, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, evidence := range state.Evidence {
		if evidence.CandidateKind != work.CommitCandidate || evidence.BaseRevision != "" || evidence.CandidateDigest != "" {
			t.Fatalf("migration did not make evidence candidate explicit: %#v", evidence)
		}
	}
	run := state.WorkItems[1].CurrentRun
	if run == nil || run.CandidateKind != work.CommitCandidate || run.Revision != "def456" || run.BaseRevision != "" || run.CandidateDigest != "" {
		t.Fatalf("migration did not make current run candidate explicit: %#v", run)
	}
	backup, err := os.ReadFile(filepath.Join(root, stateDirectory, "state.json.v5.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != original {
		t.Fatal("backup does not hold the original v5 snapshot")
	}
}

// v6State is the immediately previous schema. Runtime metadata did not exist,
// so its absent runtime fields must remain a valid, honest historical record.
func v6State(t *testing.T, root string) string {
	t.Helper()
	contents := v5State(t, root)
	contents = strings.Replace(contents, `"schema_version":5`, `"schema_version":6`, 1)
	contents = strings.ReplaceAll(contents, `"revision":"abc123","command"`, `"revision":"abc123","candidate_kind":"COMMIT","base_revision":"","candidate_digest":"","command"`)
	contents = strings.Replace(contents, `"revision":"def456","worktree_path"`, `"revision":"def456","candidate_kind":"COMMIT","base_revision":"","candidate_digest":"","worktree_path"`, 1)
	if err := os.WriteFile(filepath.Join(root, stateDirectory, "state.json"), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return contents
}

func TestMigrateUpgradesV6WithoutInventingRuntimeMetadata(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	original := v6State(t, root)
	if _, err := Load(root); err == nil {
		t.Fatal("read a v6 state without migrating")
	}
	migrated, err := Migrate(root)
	if err != nil || !migrated {
		t.Fatalf("Migrate = %v, %v", migrated, err)
	}
	state, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if state.SchemaVersion != work.SchemaVersion {
		t.Fatalf("schema version = %d, want %d", state.SchemaVersion, work.SchemaVersion)
	}
	for _, evidence := range state.Evidence {
		if evidence.Runtime != nil {
			t.Fatalf("migration invented evidence runtime: %#v", evidence)
		}
	}
	run := state.WorkItems[1].CurrentRun
	if run == nil || run.Runtime != nil {
		t.Fatalf("migration changed in-flight run runtime: %#v", run)
	}
	backup, err := os.ReadFile(filepath.Join(root, stateDirectory, "state.json.v6.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != original {
		t.Fatal("backup does not hold the original v6 snapshot")
	}
}
