package forgepilot_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/process"
	"github.com/CarlLee1983/ForgePilot/internal/runner"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
)

// Tidying up after a verification happens once the Evidence is already durable.
// A tidy-up nobody could confirm therefore has to travel as itself: the
// engineering result stands, and the Runner stops because something may still
// be writing this workspace. Before this the removal answered an unconfirmed
// Git child with os.RemoveAll — deleting the checkout a recovery record points
// at and reporting success — and the deferred path only printed a warning, so
// nothing durable was left for the next process to find.
// See docs/adr/0022-pending-cleanup-outlives-the-process.md.
//
// This run is driven in-process because the seam that makes a group
// unconfirmable is internal and deliberately unreachable from the CLI. The
// blocking it leaves behind is then asserted through real CLI processes, which
// is where it has to hold.

func runnerOptions(root, goalID string, output *bytes.Buffer) runner.Options {
	return runner.Options{
		Root: root, GoalID: goalID, RuntimeName: "fake", Snapshot: true, Output: output,
		Budget: runner.Budget{MaxSteps: 100, MaxAttemptsPerWork: 3, MaxDuration: 5 * time.Minute,
			AgentTimeout: 2 * time.Minute, VerifyTimeout: 2 * time.Minute, MaxHandoffBytes: 64 * 1024},
		Limits: storage.ArtifactLimits{MaxWriteBytes: 1 << 20, MaxRunBytes: 16 << 20, MaxTotalBytes: 128 << 20},
	}
}

func TestAnUnconfirmedTidyUpKeepsTheEvidenceAndStopsTheRun(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md", "b.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"}, []string{"specs/stories/b.md"})
	fixture.seedGoal(t, "second", []string{"specs/stories/b.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)
	t.Setenv("FORGEPILOT_FAKE_AGENT", agent)

	// The check announces that it has run, so only the removals that follow it —
	// the tidying up — are made unconfirmable. The pre-clean AddWorktree does on
	// the way in is a different call at a different moment.
	marker := filepath.Join(t.TempDir(), "check-ran")
	fixture.replaceCanonicalCheck(t, ": > "+marker+"\nexit 0\n")

	// The group the failure names is one this test started and still owns. An
	// injected report that named the Git process's own group would name a group
	// that really is empty, and the next recovery would clear it correctly — so
	// there would be nothing left to assert about.
	stillRunning := liveGroup(t)
	var mutex sync.Mutex
	var fired int
	restore := process.InjectCleanupFailure(func(_ int, argv []string, _ time.Duration) error {
		if !strings.Contains(strings.Join(argv, " "), "worktree remove") {
			return nil
		}
		if _, err := os.Stat(marker); err != nil {
			return nil
		}
		mutex.Lock()
		fired++
		mutex.Unlock()
		return &process.NotSettled{PGID: stillRunning, Reason: "injected: the group could not be confirmed"}
	})
	var output bytes.Buffer
	record, err := runner.Start(runnerOptions(fixture.root, "queue", &output))
	restore()
	if err != nil {
		t.Fatalf("the run failed operationally: %v\n%s", err, output.String())
	}
	if record.Stop == nil || record.Stop.Reason != runner.StopRecoveryBlocked {
		t.Fatalf("stop = %#v, want RECOVERY_BLOCKED\n%s", record.Stop, output.String())
	}
	mutex.Lock()
	ranTidyUp := fired
	mutex.Unlock()
	if ranTidyUp == 0 {
		t.Fatalf("the tidy-up never ran, so nothing was made unconfirmable\n%s", output.String())
	}

	// The Evidence the check honestly earned is untouched.
	evidence := evidenceResults(t, fixture.root)
	if len(evidence) == 0 {
		t.Fatalf("a cleanup failure discarded the verification result\n%s", output.String())
	}
	// The fact is durable, in the field a resume does not clear.
	stored := loadRunRecord(t, fixture.root, record.RunID)
	pending, ok := stored["pending"].([]any)
	if !ok || len(pending) == 0 {
		t.Fatalf("no pending execution was persisted: %v\n%s", stored["pending"], output.String())
	}
	entry := pending[0].(map[string]any)
	if entry["unresolved"] != true {
		t.Fatalf("the pending execution was recorded as resolved: %v", entry)
	}
	if entry["location"] == "" {
		t.Fatalf("the pending execution names no checkout: %v", entry)
	}
	identity, _ := entry["identity"].(map[string]any)
	if identity == nil || identity["pgid"] == float64(0) {
		t.Fatalf("the pending execution names no process group: %v", entry)
	}
	evidenceBefore := fmt.Sprint(evidence)
	sessionsBefore := len(fixture.sessions(t))

	// And now the part that has to hold in a new operating-system process: every
	// door back into this workspace is shut until somebody confirms the group.
	for _, attempt := range []struct {
		name      string
		arguments []string
	}{
		{name: "resume the same run", arguments: []string{"run", "resume", record.RunID}},
		{name: "start a new run", arguments: []string{"run", "--goal", "queue", "--runtime", "fake", "--snapshot"}},
		{name: "another goal in the same workspace", arguments: []string{"run", "--goal", "second", "--runtime", "fake", "--snapshot"}},
	} {
		out, code := fixture.runForge(t, agent, attempt.arguments...)
		if code != 2 || !strings.Contains(out, "RECOVERY_BLOCKED") {
			t.Fatalf("%s: exit = %d, want 2 with RECOVERY_BLOCKED\n%s", attempt.name, code, out)
		}
		if sessions := fixture.sessions(t); len(sessions) != sessionsBefore {
			t.Fatalf("%s started a session behind a blocked recovery: %v", attempt.name, sessions)
		}
	}
	// The stored result is still the one the check produced.
	if after := fmt.Sprint(evidenceResults(t, fixture.root)); after != evidenceBefore {
		t.Fatalf("the evidence changed while the workspace was blocked: %s -> %s", evidenceBefore, after)
	}

	// Once the group is confirmed gone the workspace opens again, without anyone
	// editing a record by hand.
	stopGroup(t, stillRunning)
	out, _ := fixture.runForge(t, agent, "run", "resume", record.RunID)
	if strings.Contains(out, "RECOVERY_BLOCKED") {
		t.Fatalf("a confirmed-empty group still blocked the workspace\n%s", out)
	}
}
