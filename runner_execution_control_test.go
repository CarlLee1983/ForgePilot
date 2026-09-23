package forgepilot_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// These cover the three execution-control holes the Runner MVP left open: a
// canonical check that never heard the stop signal, a canonical check whose
// background children outlived it, and a total deadline that only applied
// between steps. See docs/specs/runner-mvp/issues/06-execution-control.md.

// checkControl are the absolute paths a canonical-check fixture uses to say
// what it is doing. They live outside the repository on purpose: the check runs
// in a detached worktree, so anything it writes relative to itself is thrown
// away with that checkout.
type checkControl struct {
	started    string
	background string
	finished   string
}

func newCheckControl(t *testing.T) checkControl {
	t.Helper()
	directory := t.TempDir()
	return checkControl{
		started:    filepath.Join(directory, "check-started"),
		background: filepath.Join(directory, "check-background.pid"),
		finished:   filepath.Join(directory, "check-finished"),
	}
}

// replaceCanonicalCheck swaps the fixture's canonical check for one the test
// controls and commits it, so the committed revision a Candidate is built from
// really does define this check.
func (fixture runnerFixture) replaceCanonicalCheck(t *testing.T, script string) {
	t.Helper()
	write(t, filepath.Join(fixture.root, "verify.sh"), script)
	commitAll(t, fixture.root, "replace the canonical check")
}

// blockingCheck is a canonical check that starts a background child, announces
// that it is running, and then blocks. It is how a test gets a Runner that is
// genuinely inside a verification rather than one that is about to be.
func blockingCheck(control checkControl) string {
	return `( sleep 300 ) &
echo "$!" > ` + control.background + `
: > ` + control.started + `
sleep 300
`
}

// backgroundCheck is a canonical check that leaves a child behind and then
// exits with the given code. Nothing about it is unusual: `make verify` forking
// a watcher, a dev server or a test daemon is ordinary.
func backgroundCheck(control checkControl, exitCode string) string {
	return `( sleep 300 ) &
echo "$!" > ` + control.background + `
: > ` + control.finished + `
exit ` + exitCode + `
`
}

// startRun launches the CLI without waiting for it, so the test can signal it.
func (fixture runnerFixture) startRun(t *testing.T, agentPath string, arguments ...string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	command := exec.Command(fixture.binary, fixture.runtimeArguments(arguments)...)
	command.Dir = fixture.root
	command.Env = append(os.Environ(), "FORGEPILOT_FAKE_AGENT="+agentPath)
	output := new(bytes.Buffer)
	command.Stdout, command.Stderr = output, output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	// Even a failing assertion must not leave a Runner — or the check it is
	// inside — running after the test returns.
	t.Cleanup(func() {
		if command.Process != nil {
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			_ = command.Process.Kill()
		}
	})
	return command, output
}

// waitForExit collects the exit code within a bound, so a Runner that ignores
// its stop signal fails the test rather than hanging it.
func waitForExit(t *testing.T, command *exec.Cmd, output *bytes.Buffer, within time.Duration) int {
	t.Helper()
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()
	select {
	case err := <-finished:
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		if err != nil {
			t.Fatalf("run: %v\n%s", err, output.String())
		}
		return 0
	case <-time.After(within):
		t.Fatalf("the run did not end within %s\n%s", within, output.String())
		return -1
	}
}

// assertProcessGone waits, within a bound, for a recorded pid to disappear.
func assertProcessGone(t *testing.T, pidFile, what string) {
	t.Helper()
	pid := strings.TrimSpace(readTestFile(t, pidFile))
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if exec.Command("kill", "-0", pid).Run() != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = exec.Command("kill", "-9", pid).Run()
	t.Fatalf("%s (pid %s) survived", what, pid)
}

func evidenceResults(t *testing.T, root string) []work.Result {
	t.Helper()
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	var results []work.Result
	for _, evidence := range state.Evidence {
		results = append(results, evidence.Result)
	}
	return results
}

func countResult(results []work.Result, want work.Result) int {
	count := 0
	for _, result := range results {
		if result == want {
			count++
		}
	}
	return count
}

func stopReasonOf(t *testing.T, root, runID string) string {
	t.Helper()
	record := loadRunRecord(t, root, runID)
	stop, ok := record["stop"].(map[string]any)
	if !ok {
		t.Fatalf("run %s recorded no stop: %v", runID, record)
	}
	return stop["reason"].(string)
}

// A: the canonical check is on the same stop signal as everything else.

// Ctrl-C during a verification must stop the check, not wait it out. Before
// this, the Runner called app.Verify with context.Background(), so the signal
// reached the loop and nothing else: `make verify` ran to completion, and a
// long one held the terminal for as long as it wanted.
func TestSignalDuringVerificationStopsTheCheckAndItsProcessGroup(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	control := newCheckControl(t)
	fixture.replaceCanonicalCheck(t, blockingCheck(control))
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	command, output := fixture.startRun(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot",
		"--verify-timeout", "10m", "--max-duration", "10m")
	waitForFile(t, control.started)

	if err := command.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if code := waitForExit(t, command, output, 60*time.Second); code != 130 {
		t.Fatalf("exit = %d, want 130\n%s", code, output.String())
	}
	assertProcessGone(t, control.background, "a child of the canonical check")

	// The interrupted Verification Run is closed out the legal way: INTERRUPTED
	// Evidence, never an inferred FAIL, and never a VERIFYING nobody will settle.
	results := evidenceResults(t, fixture.root)
	if countResult(results, work.Interrupted) != 1 {
		t.Fatalf("evidence = %v, want exactly one INTERRUPTED", results)
	}
	if countResult(results, work.Fail) != 0 {
		t.Fatalf("evidence = %v; an interruption was recorded as an engineering failure", results)
	}
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if status := state.WorkItemStatus("WI-001"); status == work.Verifying {
		t.Fatal("the work item was left VERIFYING with no live runner")
	}
	runID := lastRun(t, fixture.root)
	if reason := stopReasonOf(t, fixture.root, runID); reason != "INTERRUPTED" {
		t.Fatalf("stop reason = %s, want INTERRUPTED", reason)
	}
}

// SIGTERM ends the same verification the same way; only the exit code differs,
// and that difference is the whole reason a supervisor can tell them apart.
func TestTerminationDuringVerificationExitsWithItsOwnCode(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	control := newCheckControl(t)
	fixture.replaceCanonicalCheck(t, blockingCheck(control))
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	command, output := fixture.startRun(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot",
		"--verify-timeout", "10m", "--max-duration", "10m")
	waitForFile(t, control.started)

	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if code := waitForExit(t, command, output, 60*time.Second); code != 143 {
		t.Fatalf("exit = %d, want 143\n%s", code, output.String())
	}
	assertProcessGone(t, control.background, "a child of the canonical check")
	runID := lastRun(t, fixture.root)
	if reason := stopReasonOf(t, fixture.root, runID); reason != "TERMINATED" {
		t.Fatalf("stop reason = %s, want TERMINATED", reason)
	}
}

// A signal stops the run where it stands. It must not be the moment a new
// session or a new verification is started, and the Evidence already earned by
// an earlier Work Item must survive it untouched: a saved verdict is a fact,
// and a later interruption is not a reason to rewrite one.
func TestASignalNeitherStartsTheNextWorkNorRewritesSavedEvidence(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md", "b.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"}, []string{"specs/stories/b.md"})
	// The first Work Item is implemented and verified normally; the second
	// blocks, which is where the signal lands.
	agent := fixture.fakeAgent(t, `if [ "$item" = "WI-002" ]; then
  sleep 300
fi
printf 'done\n' > "$workspace/$lower.txt"
printf '{"outcome":"implementation_finished","summary":"implemented %s"}' "$item" > "$result"
`)

	command, output := fixture.startRun(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot",
		"--max-duration", "10m", "--agent-timeout", "10m")
	// Wait until the second session has started, which means the first Work Item
	// already has its PASS Evidence.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) && len(fixture.sessions(t)) < 2 {
		time.Sleep(20 * time.Millisecond)
	}
	if len(fixture.sessions(t)) < 2 {
		t.Fatalf("the runner never reached the second work item:\n%s", output.String())
	}

	if err := command.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if code := waitForExit(t, command, output, 60*time.Second); code != 130 {
		t.Fatalf("exit = %d, want 130\n%s", code, output.String())
	}
	// Exactly the two sessions that were legitimately started: no third one was
	// launched after the signal arrived.
	if sessions := fixture.sessions(t); len(sessions) != 2 {
		t.Fatalf("sessions = %v; a signal started more work", sessions)
	}
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	evidence, ok := state.LatestVerification("WI-001")
	if !ok {
		t.Fatal("the first work item lost the evidence it had earned")
	}
	if evidence.Result != work.Pass {
		t.Fatalf("WI-001 evidence = %s; a later signal rewrote a saved verdict", evidence.Result)
	}
}

// B: a check that exits on its own still hands back an empty process group.

// `make verify` forking a watcher and exiting 0 is ordinary. What is not
// ordinary is that watcher still writing the workspace while the next agent
// session runs, and still being there when the digest around that session is
// taken.
func TestACanonicalCheckLeavesNoBackgroundChildBehindOnAPass(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	control := newCheckControl(t)
	fixture.replaceCanonicalCheck(t, backgroundCheck(control, "0"))
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	if code != 0 || !strings.Contains(output, "GOAL_COMPLETED") {
		t.Fatalf("exit = %d\n%s", code, output)
	}
	if countResult(evidenceResults(t, fixture.root), work.Pass) == 0 {
		t.Fatal("the passing check did not produce PASS evidence")
	}
	assertProcessGone(t, control.background, "a child of the canonical check")
}

// The same is true of a check that fails. The engineering result stands — it is
// a real FAIL — and the process group is still emptied before anything else is
// allowed to start.
func TestACanonicalCheckLeavesNoBackgroundChildBehindOnAFailure(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	control := newCheckControl(t)
	fixture.replaceCanonicalCheck(t, backgroundCheck(control, "1"))
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot",
		"--max-attempts-per-work", "1")
	if code != 3 || !strings.Contains(output, "MAX_ATTEMPTS_PER_WORK") {
		t.Fatalf("exit = %d\n%s", code, output)
	}
	if countResult(evidenceResults(t, fixture.root), work.Fail) == 0 {
		t.Fatal("the failing check did not produce FAIL evidence")
	}
	assertProcessGone(t, control.background, "a child of the canonical check")
}

// C: --max-duration bounds the whole run, not the gaps between steps.

// A deadline that only applies at the top of the loop is not a deadline: an
// agent session started just before it passes runs its full --agent-timeout on
// top of it.
func TestTheRunDeadlineStopsAnAgentSessionInFlight(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	blocking := fixture.fakeAgent(t, "sleep 300\n")

	started := time.Now()
	output, code := fixture.runForge(t, blocking, "run", "--goal", "queue", "--runtime", "fake", "--snapshot",
		"--max-duration", "5s", "--agent-timeout", "5m", "--verify-timeout", "5m")
	elapsed := time.Since(started)
	if code != 3 || !strings.Contains(output, "MAX_DURATION") {
		t.Fatalf("exit = %d after %s\n%s", code, elapsed, output)
	}
	if elapsed > 90*time.Second {
		t.Fatalf("the run outlived its 5s deadline by %s", elapsed)
	}
}

// The same hole on the other side of the loop: a verification started just
// before the deadline passes runs its full --verify-timeout on top of it.
func TestTheRunDeadlineStopsAVerificationInFlight(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	control := newCheckControl(t)
	fixture.replaceCanonicalCheck(t, blockingCheck(control))
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	started := time.Now()
	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot",
		"--max-duration", "20s", "--agent-timeout", "5m", "--verify-timeout", "5m")
	elapsed := time.Since(started)
	if code != 3 || !strings.Contains(output, "MAX_DURATION") {
		t.Fatalf("exit = %d after %s\n%s", code, elapsed, output)
	}
	if elapsed > 120*time.Second {
		t.Fatalf("the run outlived its 20s deadline by %s", elapsed)
	}
	assertProcessGone(t, control.background, "a child of the canonical check")
	// Interrupted, not failed: the check produced no result, and inventing one
	// would be the same mistake as forging an exit code.
	results := evidenceResults(t, fixture.root)
	if countResult(results, work.Fail) != 0 {
		t.Fatalf("evidence = %v; an expired deadline was recorded as an engineering failure", results)
	}
}

// The boundary the old loop never checked: an agent session that finishes after
// the deadline has already passed must not be followed by a verification.
func TestNoVerificationStartsAfterTheRunDeadlineHasPassed(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	control := newCheckControl(t)
	// A check that records the moment it starts, so "it never started" is an
	// observation rather than an inference.
	fixture.replaceCanonicalCheck(t, `: > `+control.started+`
echo "canonical check passed"
`)
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	slow := fixture.fakeAgent(t, `sleep 6
printf 'done\n' > "$workspace/$lower.txt"
printf '{"outcome":"implementation_finished","summary":"implemented %s"}' "$item" > "$result"
`)

	output, code := fixture.runForge(t, slow, "run", "--goal", "queue", "--runtime", "fake", "--snapshot",
		"--max-duration", "3s", "--agent-timeout", "5m", "--verify-timeout", "5m")
	if code != 3 || !strings.Contains(output, "MAX_DURATION") {
		t.Fatalf("exit = %d\n%s", code, output)
	}
	if _, err := os.Stat(control.started); err == nil {
		t.Fatal("a verification was started after the run deadline had already passed")
	}
}

// An agent that outlives its own timeout, inside a run with plenty of duration
// left, is an AGENT_TIMEOUT. Reporting it as MAX_DURATION would send whoever
// reads the record to the wrong flag.
func TestAnAgentTimeoutInsideTheRunDeadlineKeepsItsOwnReason(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	blocking := fixture.fakeAgent(t, "sleep 300\n")

	output, code := fixture.runForge(t, blocking, "run", "--goal", "queue", "--runtime", "fake", "--snapshot",
		"--max-duration", "10m", "--agent-timeout", "3s", "--verify-timeout", "5m")
	if code != 3 || !strings.Contains(output, "AGENT_TIMEOUT") {
		t.Fatalf("exit = %d\n%s", code, output)
	}
}

// And a verification that outlives its own timeout, inside a run with plenty of
// duration left, is a VERIFY_TIMEOUT.
func TestAVerifyTimeoutInsideTheRunDeadlineKeepsItsOwnReason(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	control := newCheckControl(t)
	fixture.replaceCanonicalCheck(t, blockingCheck(control))
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot",
		"--max-duration", "10m", "--agent-timeout", "5m", "--verify-timeout", "5s")
	if code != 3 || !strings.Contains(output, "VERIFY_TIMEOUT") {
		t.Fatalf("exit = %d\n%s", code, output)
	}
	assertProcessGone(t, control.background, "a child of the canonical check")
}

// A resume inherits the deadline it was given. An expired run may be resumed —
// recovery still has to happen — but it must not buy a single new session with
// it.
func TestResumingAnExpiredRunStartsNoNewSession(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	blocking := fixture.fakeAgent(t, "sleep 300\n")

	output, code := fixture.runForge(t, blocking, "run", "--goal", "queue", "--runtime", "fake", "--snapshot",
		"--max-duration", "5s", "--agent-timeout", "5m", "--verify-timeout", "5m")
	if code != 3 || !strings.Contains(output, "MAX_DURATION") {
		t.Fatalf("exit = %d\n%s", code, output)
	}
	runID := lastRun(t, fixture.root)
	before := loadRunRecord(t, fixture.root, runID)
	sessionsBefore := len(fixture.sessions(t))

	output, code = fixture.runForge(t, blocking, "run", "resume", runID)
	if code != 3 || !strings.Contains(output, "MAX_DURATION") {
		t.Fatalf("resume exit = %d\n%s", code, output)
	}
	if sessions := fixture.sessions(t); len(sessions) != sessionsBefore {
		t.Fatalf("sessions went from %d to %d; an expired run bought new work", sessionsBefore, len(sessions))
	}
	after := loadRunRecord(t, fixture.root, runID)
	if after["deadline"] != before["deadline"] {
		t.Fatalf("deadline moved from %v to %v", before["deadline"], after["deadline"])
	}
	if after["steps"] != before["steps"] {
		t.Fatalf("steps moved from %v to %v", before["steps"], after["steps"])
	}
	if !equalAttempts(after["attempts"], before["attempts"]) {
		t.Fatalf("attempts moved from %v to %v", before["attempts"], after["attempts"])
	}
}

func equalAttempts(after, before any) bool {
	left, leftOK := after.(map[string]any)
	right, rightOK := before.(map[string]any)
	if !leftOK || !rightOK || len(left) != len(right) {
		return leftOK == rightOK && left == nil && right == nil
	}
	for key, value := range right {
		if left[key] != value {
			return false
		}
	}
	return true
}

// The standalone command is not the Runner and must not inherit its ceilings.
// `forgepilot verify` has no built-in time limit (ADR-0004), and a shared
// service is not a reason to give it one.
func TestStandaloneVerifyKeepsItsOwnContract(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	control := newCheckControl(t)
	fixture.replaceCanonicalCheck(t, backgroundCheck(control, "0"))
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	mustRun(t, fixture.binary, fixture.root, "start", "WI-001")

	output, code := fixture.runForge(t, "", "verify", "WI-001", "--snapshot")
	if code != 0 {
		t.Fatalf("verify exit = %d\n%s", code, output)
	}
	if !strings.Contains(output, "PASS") {
		t.Fatalf("verify output:\n%s", output)
	}
	// It still cleans up after itself, which is not a change of contract: a
	// command that has returned is a command that owns nothing.
	assertProcessGone(t, control.background, "a child of the canonical check")
}
