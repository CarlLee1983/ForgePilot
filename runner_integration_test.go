package forgepilot_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// The headline behaviour: three dependent Work Items are implemented and
// verified in order, each attempt in its own session, and the run stops at the
// Goal final-review boundary without completing anything.
func TestRunnerDrivesDependentWorkToTheGoalReviewBoundary(t *testing.T) {
	fixture := newRunnerFixture(t)
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue",
		[]string{"specs/stories/a.md"},
		[]string{"specs/stories/b.md", "WI-001"},
		[]string{"specs/stories/c.md", "WI-002"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, output)
	}
	if !strings.Contains(output, "AWAITING_GOAL_REVIEW") {
		t.Fatalf("output does not report the review boundary:\n%s", output)
	}
	if !strings.Contains(output, "not completion") {
		t.Fatalf("output does not say this is not completion:\n%s", output)
	}

	if sessions := fixture.sessions(t); len(sessions) != 3 ||
		sessions[0] != "WI-001" || sessions[1] != "WI-002" || sessions[2] != "WI-003" {
		t.Fatalf("sessions = %v, want one per work item in dependency order", sessions)
	}

	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	evidence := map[string]string{}
	for _, item := range state.WorkItems {
		if item.Status == work.Done {
			t.Fatalf("%s reached DONE; the runner must never complete work", item.ID)
		}
		if item.Status != work.Verified {
			t.Fatalf("%s is %s, want VERIFIED", item.ID, item.Status)
		}
		latest, ok := state.LatestVerification(item.ID)
		if !ok || latest.Result != work.Pass {
			t.Fatalf("%s latest verification = %#v", item.ID, latest)
		}
		if previous, seen := evidence[latest.ID]; seen {
			t.Fatalf("%s reuses Evidence %s from %s", item.ID, latest.ID, previous)
		}
		evidence[latest.ID] = item.ID
	}
	if state.Goals[0].Status != work.GoalActive {
		t.Fatalf("goal = %s; the runner must not complete a Goal", state.Goals[0].Status)
	}

	// The run record names the exact Evidence the boundary was judged on.
	runID := lastRun(t, fixture.root)
	record := loadRunRecord(t, fixture.root, runID)
	stop := record["stop"].(map[string]any)
	if stop["reason"] != "AWAITING_GOAL_REVIEW" {
		t.Fatalf("stop = %v", stop)
	}
	if len(stop["evidence_ids"].([]any)) != 3 {
		t.Fatalf("evidence_ids = %v", stop["evidence_ids"])
	}
	// Execution history retains every Evidence produced by each shared run; the
	// stop record above separately names only the three latest final-review IDs.
	history := record["evidence_ids"].([]any)
	if len(history) != 6 {
		t.Fatalf("run Evidence history = %v, want all six Evidence records from three executions", history)
	}
	seen := map[string]bool{}
	for _, raw := range history {
		id := raw.(string)
		if seen[id] {
			t.Fatalf("run Evidence history repeats %s: %v", id, history)
		}
		seen[id] = true
	}
}

func TestRunRefusesWithoutSnapshotAndNeverCommits(t *testing.T) {
	fixture := newRunnerFixture(t)
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)
	before := headOf(t, fixture.root)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake")
	if code != 1 || !strings.Contains(output, "--snapshot is required") {
		t.Fatalf("exit = %d, output = %s", code, output)
	}
	if headOf(t, fixture.root) != before {
		t.Fatal("a refused run moved HEAD")
	}
	if fixture.sessions(t) != nil {
		t.Fatal("a refused run started a session")
	}
}

func TestDryRunInspectsWithoutChangingAnything(t *testing.T) {
	fixture := newRunnerFixture(t)
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"}, []string{"specs/stories/b.md", "WI-001"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	statePath := filepath.Join(fixture.root, ".forgepilot", "state.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	head := headOf(t, fixture.root)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot", "--dry-run")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, output)
	}
	for _, want := range []string{"Dry run for goal queue", "Work items in scope: 2", "First action: START WI-001", "Nothing was started"} {
		if !strings.Contains(output, want) {
			t.Fatalf("dry run output missing %q:\n%s", want, output)
		}
	}

	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("dry run changed state.json")
	}
	if headOf(t, fixture.root) != head {
		t.Fatal("dry run moved HEAD")
	}
	if fixture.sessions(t) != nil {
		t.Fatal("dry run started a session")
	}
	if entries, err := os.ReadDir(filepath.Join(fixture.root, ".forgepilot", "runs")); err == nil && len(entries) > 0 {
		t.Fatalf("dry run wrote run records: %v", entries)
	}
	if refs := gitOutput(t, fixture.root, "for-each-ref", "refs/forgepilot/snapshots"); strings.TrimSpace(refs) != "" {
		t.Fatalf("dry run created snapshot refs: %s", refs)
	}
}

// A second Goal in the same repository must neither be driven nor be able to
// make the named Goal look stalled.
func TestRunnerDrivesOnlyTheNamedGoal(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md", "b.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	// The other Goal is created first, so it owns the earliest Work Item and
	// would be the global `next` answer.
	fixture.seedGoal(t, "other", []string{"specs/stories/a.md"})
	fixture.seedGoal(t, "mine", []string{"specs/stories/b.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	output, code := fixture.runForge(t, agent, "run", "--goal", "mine", "--runtime", "fake", "--snapshot")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, output)
	}
	if sessions := fixture.sessions(t); len(sessions) != 1 || sessions[0] != "WI-002" {
		t.Fatalf("sessions = %v, want only the named Goal's work", sessions)
	}
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range state.WorkItems {
		if item.ID == "WI-001" && item.Status != work.Ready {
			t.Fatalf("the other Goal's work is %s; the runner touched it", item.Status)
		}
	}
}

func lastRun(t *testing.T, root string) string {
	t.Helper()
	runs, err := storage.ListRuns(root)
	if err != nil || len(runs) == 0 {
		t.Fatalf("ListRuns = %v, %v", runs, err)
	}
	return runs[len(runs)-1]
}

func loadRunRecord(t *testing.T, root, runID string) map[string]any {
	t.Helper()
	contents, err := storage.ReadRunArtifact(root, runID, "run.json")
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(contents, &record); err != nil {
		t.Fatal(err)
	}
	return record
}

func headOf(t *testing.T, root string) string {
	t.Helper()
	return strings.TrimSpace(gitOutput(t, root, "rev-parse", "HEAD"))
}
