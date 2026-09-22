package forgepilot_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// A converging dependency makes the deferred re-verifications visible: each new
// Candidate makes earlier PASSes stale, and the work that joins two branches
// cannot start until its own prerequisites are current again. Deferring those
// re-verifications is scheduling, not forgiveness — every one of them is still
// owed before the Goal can complete.
func TestConvergingDependenciesPayTheirDeferredReverifications(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md", "b.md", "c.md", "d.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "diamond",
		[]string{"specs/stories/a.md"},
		[]string{"specs/stories/b.md", "WI-001"},
		[]string{"specs/stories/c.md", "WI-001"},
		[]string{"specs/stories/d.md", "WI-002", "WI-003"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	output, code := fixture.runForge(t, agent, "run", "--goal", "diamond", "--runtime", "fake", "--snapshot", "--max-steps", "60")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, output)
	}
	if sessions := fixture.sessions(t); len(sessions) != 4 {
		t.Fatalf("sessions = %v, want exactly one per work item", sessions)
	}

	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	verifications := 0
	for _, evidence := range state.Evidence {
		if evidence.Type == work.VerificationEvidence {
			verifications++
			if evidence.Result != work.Pass {
				t.Fatalf("evidence %s is %s", evidence.ID, evidence.Result)
			}
		}
	}
	// Four implementations plus the re-verifications the moving Candidate owed.
	if verifications <= 4 {
		t.Fatalf("%d verifications: stale prerequisites were never re-verified", verifications)
	}
	for _, item := range state.WorkItems {
		if item.Status != work.Verified {
			t.Fatalf("%s is %s", item.ID, item.Status)
		}
	}
	// Every PASS names the current Candidate: that is what Goal completion checks,
	// and reaching it is what exit 0 claimed.
	if !strings.Contains(output, "GOAL_COMPLETED") {
		t.Fatalf("output:\n%s", output)
	}
}

// Readiness withdrawn by a Gate does not come back on its own (ADR-0017). The
// Runner must recover it through the existing reconcile command rather than by
// deciding for itself that the queue looks fine now.
func TestRunnerRecoversWithheldReadinessThroughReconcile(t *testing.T) {
	fixture := newRunnerFixture(t)
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue",
		[]string{"specs/stories/a.md"},
		[]string{"specs/stories/b.md", "WI-001"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	// Drive WI-001 to VERIFIED, then stop before WI-002 starts.
	if _, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot", "--max-steps", "3"); code != 3 {
		t.Fatalf("seed run exit = %d", code)
	}
	// A Gate withdraws the downstream readiness. The Candidate then moves, so
	// answering the Gate cannot restore it: the prerequisite's PASS no longer
	// names the current workspace. Moving the workspace back makes the PASS
	// current again — and the persisted PENDING still does not correct itself,
	// which is the condition reconcile exists for (ADR-0017).
	output := mustOutput(t, fixture, "gate", "open", "--work", "WI-001", "--question", "which?", "--option", "one", "--option", "two")
	gateID := gateIDFrom(t, output)
	write(t, filepath.Join(fixture.root, "moved.txt"), "moves the snapshot candidate\n")
	mustRun(t, fixture.binary, fixture.root, "gate", "resolve", gateID, "--option", "one", "--by", fixtureIdentity)
	if status := statusOf(t, fixture, "WI-002"); status != work.Pending {
		t.Fatalf("WI-002 at a stale prerequisite = %s, want PENDING", status)
	}
	if err := os.Remove(filepath.Join(fixture.root, "moved.txt")); err != nil {
		t.Fatal(err)
	}
	if status := statusOf(t, fixture, "WI-002"); status != work.Pending {
		t.Fatalf("WI-002 = %s; persisted readiness corrected itself", status)
	}

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, output)
	}
	if !strings.Contains(output, "RECONCILE queue") {
		t.Fatalf("readiness was not recovered through reconcile:\n%s", output)
	}
	if status := statusOf(t, fixture, "WI-002"); status != work.Verified {
		t.Fatalf("WI-002 = %s", status)
	}
}

// An open Gate stops the run. The Runner must not start the gated work and must
// not resolve the Gate to get past it.
func TestAnOpenGateStopsTheRunWithoutBeingResolved(t *testing.T) {
	fixture := newRunnerFixture(t)
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	mustRun(t, fixture.binary, fixture.root, "gate", "open", "--work", "WI-001",
		"--question", "which store?", "--option", "postgres", "--option", "sqlite")
	agent := fixture.fakeAgent(t, implementsCleanly)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	if code != 2 {
		t.Fatalf("exit = %d\n%s", code, output)
	}
	if !strings.Contains(output, "WAIT_GATE") {
		t.Fatalf("output:\n%s", output)
	}
	if fixture.sessions(t) != nil {
		t.Fatal("the runner started gated work")
	}
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if count := state.OpenGateCount("WI-001"); count != 1 {
		t.Fatalf("open gates = %d; the runner resolved one", count)
	}
}

// A Goal blocked while nothing else can move must stop the run rather than be
// worked around, and the Runner must never unblock it.
func TestABlockedGoalStopsTheRun(t *testing.T) {
	fixture := newRunnerFixture(t)
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	mustRun(t, fixture.binary, fixture.root, "goal", "block", "queue", "--reason", "direction under review")
	agent := fixture.fakeAgent(t, implementsCleanly)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	if code != 1 {
		t.Fatalf("exit = %d\n%s", code, output)
	}
	if !strings.Contains(output, "not ACTIVE") {
		t.Fatalf("output:\n%s", output)
	}
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if state.Goals[0].Status != work.GoalBlocked {
		t.Fatalf("goal = %s; the runner unblocked it", state.Goals[0].Status)
	}
}

// A live Verification Run belonging to another process is not a completion and
// not something to barge past: the run stops and says what it is waiting on.
func TestALiveVerificationElsewhereStopsTheRunWithoutClaimingCompletion(t *testing.T) {
	fixture := newRunnerFixture(t)
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	mustRun(t, fixture.binary, fixture.root, "start", "WI-001")
	orphan(t, fixture.root, "WI-001")
	agent := fixture.fakeAgent(t, implementsCleanly)

	// Hold the Work Item's verification lock, which is exactly what a live run
	// holds, so the recorded VERIFYING reads as live rather than abandoned.
	holding := make(chan struct{})
	released := make(chan struct{})
	go func() {
		_ = storage.WithVerifyLock(fixture.root, "WI-001", func() error {
			close(holding)
			<-released
			return nil
		})
	}()
	<-holding
	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	close(released)

	if code != 2 {
		t.Fatalf("exit = %d\n%s", code, output)
	}
	if !strings.Contains(output, "VERIFICATION_IN_FLIGHT") {
		t.Fatalf("output:\n%s", output)
	}
	if strings.Contains(output, "GOAL_COMPLETED") {
		t.Fatal("a live verification was reported as Goal completion")
	}
}

// A run that changes nothing semantic must stop on its own. Fresh Evidence IDs
// and rising attempt numbers are not progress, which is why the check compares
// work, evidence results, freshness, gates and the Goal instead.
func TestARunThatChangesNothingStopsForNoProgress(t *testing.T) {
	fixture := newRunnerFixture(t)
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	// The session claims to have finished without changing anything, so every
	// verification reaches the same result on the same Candidate.
	// Every attempt leaves the workspace in the same incomplete state, so every
	// verification reaches the same FAIL on the same Candidate.
	agent := fixture.fakeAgent(t, `printf 'broken\n' > "$workspace/$lower.txt"
printf '{"outcome":"implementation_finished","summary":"claimed to finish"}' > "$result"
`)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot",
		"--max-attempts-per-work", "20", "--max-steps", "60")
	if code != 3 {
		t.Fatalf("exit = %d\n%s", code, output)
	}
	if !strings.Contains(output, "NO_PROGRESS") {
		t.Fatalf("output:\n%s", output)
	}
	if sessions := fixture.sessions(t); len(sessions) > 6 {
		t.Fatalf("%d sessions before the loop was detected", len(sessions))
	}
}

// Capacity is a stop, not a cleanup trigger: nothing a final review might need
// is deleted to make room.
func TestExceedingTheArtifactBudgetStopsSafely(t *testing.T) {
	fixture := newRunnerFixture(t)
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"}, []string{"specs/stories/b.md", "WI-001"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	// Leave room for one profiled session and its Evidence, but not the next
	// session reservation; the assertion is about preserving existing artifacts.
	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot",
		"--max-agent-output-bytes", "2048", "--max-run-bytes", "82000")
	if code != 3 {
		t.Fatalf("exit = %d\n%s", code, output)
	}
	if !strings.Contains(output, "CAPACITY_EXCEEDED") {
		t.Fatalf("output:\n%s", output)
	}
	if !strings.Contains(output, "nothing is deleted automatically") && !strings.Contains(output, "free space under") {
		t.Fatalf("the refusal does not say how to recover:\n%s", output)
	}
	// Whatever was produced before the limit was reached is still there: a
	// capacity stop is a refusal to write more, never a licence to delete what a
	// final review might need.
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Evidence) == 0 {
		t.Fatal("the capacity stop discarded evidence")
	}
	if runs, listErr := storage.ListRuns(fixture.root); listErr != nil || len(runs) != 1 {
		t.Fatalf("runs after a capacity stop = %v, %v", runs, listErr)
	}
	if refs := gitOutput(t, fixture.root, "for-each-ref", "refs/forgepilot/snapshots"); strings.TrimSpace(refs) == "" {
		t.Fatal("the capacity stop removed the snapshot refs a final review needs")
	}
}

// A deterministic soak over many Work Items: no sleeps, no real model, and the
// assertion is about scheduling across sessions rather than about elapsed time.
func TestSoakSchedulesManyWorkItemsAcrossSessions(t *testing.T) {
	if testing.Short() {
		t.Skip("soak test")
	}
	const count = 8
	var stories []string
	for index := 1; index <= count; index++ {
		stories = append(stories, fmt.Sprintf("soak-%02d.md", index))
	}
	fixture := newRunnerFixture(t, stories...)
	mustRun(t, fixture.binary, fixture.root, "init")

	items := make([][]string, 0, count)
	for index, story := range stories {
		entry := []string{"specs/stories/" + story}
		if index > 0 {
			entry = append(entry, fmt.Sprintf("WI-%03d", index))
		}
		items = append(items, entry)
	}
	fixture.seedGoal(t, "soak", items...)
	agent := fixture.fakeAgent(t, implementsCleanly)

	started := time.Now()
	output, code := fixture.runForge(t, agent, "run", "--goal", "soak", "--runtime", "fake", "--snapshot",
		"--max-steps", "400", "--max-duration", "30m")
	if code != 0 {
		t.Fatalf("exit = %d after %s\n%s", code, time.Since(started), output)
	}
	if sessions := fixture.sessions(t); len(sessions) != count {
		t.Fatalf("sessions = %d, want exactly one per work item", len(sessions))
	}
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range state.WorkItems {
		if item.Status != work.Verified {
			t.Fatalf("%s is %s", item.ID, item.Status)
		}
	}
	record := loadRunRecord(t, fixture.root, lastRun(t, fixture.root))
	attempts := record["attempts"].(map[string]any)
	for id, used := range attempts {
		if used.(float64) != 1 {
			t.Fatalf("%s used %v attempts", id, used)
		}
	}
}

func mustOutput(t *testing.T, fixture runnerFixture, arguments ...string) string {
	t.Helper()
	output, err := command(fixture.binary, fixture.root, arguments...)
	if err != nil {
		t.Fatalf("%v: %v\n%s", arguments, err, output)
	}
	return output
}

func gateIDFrom(t *testing.T, output string) string {
	t.Helper()
	for _, field := range strings.Fields(output) {
		if strings.HasPrefix(field, "GATE-") {
			return field
		}
	}
	t.Fatalf("no gate ID in %q", output)
	return ""
}

func statusOf(t *testing.T, fixture runnerFixture, id string) work.Status {
	t.Helper()
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	return state.WorkItemStatus(id)
}
