package forgepilot_test

import (
	"bytes"
	"encoding/json"
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

// Two Runners must never write the same workspace at once, and reaching it
// through a symlinked path must not defeat the check: an alias is the same
// repository, and a lock keyed on the spelling of the path would not know it.
func TestASecondRunnerIsRefusedThroughAnAliasToo(t *testing.T) {
	fixture := newRunnerFixture(t)
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})

	// The first agent blocks until released, so a second Runner meets a live one.
	gate := filepath.Join(t.TempDir(), "release")
	blocking := fixture.fakeAgent(t, `while [ ! -f `+gate+` ]; do sleep 0.05; done
printf 'done\n' > "$workspace/$lower.txt"
printf '{"outcome":"implementation_finished","summary":"released"}' > "$result"
`)
	first := exec.Command(fixture.binary, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	first.Dir = fixture.root
	first.Env = append(os.Environ(), "FORGEPILOT_FAKE_AGENT="+blocking)
	var firstOutput bytes.Buffer
	first.Stdout, first.Stderr = &firstOutput, &firstOutput
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.WriteFile(gate, nil, 0644)
		_ = first.Wait()
	}()
	waitForSession(t, fixture, &firstOutput)

	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(fixture.root, alias); err != nil {
		t.Fatal(err)
	}
	for name, directory := range map[string]string{"same path": fixture.root, "symlinked alias": alias} {
		command := exec.Command(fixture.binary, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
		command.Dir = directory
		command.Env = append(os.Environ(), "FORGEPILOT_FAKE_AGENT="+blocking)
		output, err := command.CombinedOutput()
		if err == nil {
			t.Fatalf("%s: a second runner was allowed:\n%s", name, output)
		}
		if !strings.Contains(string(output), "another runner already owns this workspace") {
			t.Fatalf("%s: refusal = %s", name, output)
		}
	}

	// Queries stay answerable while a Runner holds the workspace: the Runner lock
	// is not the state transaction lock.
	if output, err := command(fixture.binary, fixture.root, "status"); err != nil {
		t.Fatalf("status while a runner holds the workspace: %v\n%s", err, output)
	}
}

// A run interrupted after its budget was charged must not replay that budget on
// resume, and must not re-implement work whose Evidence already exists.
func TestResumeKeepsBudgetAndDoesNotReimplementVerifiedWork(t *testing.T) {
	fixture := newRunnerFixture(t)
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue",
		[]string{"specs/stories/a.md"},
		[]string{"specs/stories/b.md", "WI-001"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	// Three steps is enough to start, implement and verify WI-001 and no more.
	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot", "--max-steps", "3")
	if code != 3 || !strings.Contains(output, "MAX_STEPS") {
		t.Fatalf("first run exit = %d\n%s", code, output)
	}
	runID := lastRun(t, fixture.root)
	before := loadRunRecord(t, fixture.root, runID)
	if before["steps"].(float64) != 3 {
		t.Fatalf("steps = %v", before["steps"])
	}
	deadline := before["deadline"].(string)
	if len(fixture.sessions(t)) != 1 {
		t.Fatalf("sessions after the first run = %v", fixture.sessions(t))
	}

	// Resuming must fail on the same budget rather than start over.
	output, code = fixture.runForge(t, agent, "run", "resume", runID)
	if code != 3 || !strings.Contains(output, "MAX_STEPS") {
		t.Fatalf("resume exit = %d\n%s", code, output)
	}
	after := loadRunRecord(t, fixture.root, runID)
	if before["stop"].(map[string]any)["at"] == after["stop"].(map[string]any)["at"] {
		t.Fatal("resume reported the previous stop instead of re-deciding")
	}
	if after["deadline"].(string) != deadline {
		t.Fatalf("resume moved the deadline from %s to %s", deadline, after["deadline"])
	}
	if after["steps"].(float64) < before["steps"].(float64) {
		t.Fatal("resume reset the consumed step budget")
	}
	if sessions := fixture.sessions(t); len(sessions) != 1 {
		t.Fatalf("resume re-implemented already verified work: %v", sessions)
	}

	// With room to continue, the resume picks up at WI-002 — from ForgePilot's
	// state, not from anything the run record remembered about progress.
	output, code = fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	if code != 0 {
		t.Fatalf("continuation exit = %d\n%s", code, output)
	}
	if sessions := fixture.sessions(t); len(sessions) != 2 || sessions[1] != "WI-002" {
		t.Fatalf("sessions = %v", sessions)
	}
}

// A worker whose identity cannot be confirmed must block recovery rather than
// be assumed dead: assuming would start a second writer on the same workspace.
func TestResumeRefusesWhenAWorkerCannotBeConfirmed(t *testing.T) {
	fixture := newRunnerFixture(t)
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	if _, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot", "--max-steps", "1"); code != 3 {
		t.Fatalf("seed run exit = %d", code)
	}
	runID := lastRun(t, fixture.root)
	record := loadRunRecord(t, fixture.root, runID)
	// A crash between launching a process and recording its identity leaves
	// exactly this: a worker with a pid and nothing to recognise it by.
	delete(record, "stop")
	record["worker"] = map[string]any{
		"work_item_id": "WI-001",
		"attempt":      1,
		"session_dir":  filepath.Join(fixture.root, ".forgepilot", "runs", runID, "wi-001-attempt-1"),
		"started_at":   time.Now().UTC().Format(time.RFC3339Nano),
		"identity":     map[string]any{"pid": os.Getpid(), "pgid": 0, "executable": "/bin/sh"},
	}
	writeRunRecord(t, fixture.root, runID, record)

	output, code := fixture.runForge(t, agent, "run", "resume", runID)
	if code != 2 {
		t.Fatalf("resume exit = %d\n%s", code, output)
	}
	if !strings.Contains(output, "RECOVERY_BLOCKED") {
		t.Fatalf("resume output = %s", output)
	}
	if sessions := fixture.sessions(t); len(sessions) != 0 {
		t.Fatalf("a blocked recovery started a worker: %v", sessions)
	}
	// The process it could not identify is still alive: refusing must not kill.
	if os.Getpid() == 0 {
		t.Fatal("impossible")
	}
}

// An abandoned Verification Run is closed out through the existing reclaim
// path, and the run continues from the state that produced rather than from
// anything the run record believed.
func TestRunnerReclaimsAnAbandonedVerificationRun(t *testing.T) {
	fixture := newRunnerFixture(t)
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	// Leave WI-001 in VERIFYING with no live runner, exactly as a killed
	// verification does.
	mustRun(t, fixture.binary, fixture.root, "start", "WI-001")
	orphan(t, fixture.root, "WI-001")

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, output)
	}
	if !strings.Contains(output, "INTERRUPTED") {
		t.Fatalf("the abandoned run was not reported as interrupted:\n%s", output)
	}
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	interrupted := 0
	for _, evidence := range state.Evidence {
		if evidence.Result == work.Interrupted {
			interrupted++
			if evidence.ExitCode != nil {
				t.Fatalf("interrupted evidence carries an exit code: %#v", evidence)
			}
		}
	}
	if interrupted != 1 {
		t.Fatalf("interrupted evidence count = %d", interrupted)
	}
}

// waitForSession blocks until the fake agent has been launched at least once,
// so a test that needs a live Runner does not race its startup.
func waitForSession(t *testing.T, fixture runnerFixture, output *bytes.Buffer) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if len(fixture.sessions(t)) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the runner never started a session:\n%s", output.String())
}

func writeRunRecord(t *testing.T, root, runID string, record map[string]any) {
	t.Helper()
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteRunArtifact(root, runID, "run.json", append(encoded, '\n'), storage.ArtifactLimits{}); err != nil {
		t.Fatal(err)
	}
}

// orphan puts a Work Item into VERIFYING with no live runner, which is what a
// killed verification leaves behind.
func orphan(t *testing.T, root, id string) {
	t.Helper()
	if err := storage.Update(root, func(state *work.State) error {
		return state.BeginCandidateVerification(id,
			work.Candidate{Kind: work.CommitCandidate, Revision: headOf(t, root)},
			filepath.Join(root, ".forgepilot", "worktrees", "abandoned"),
			filepath.Join(root, ".forgepilot", "logs", "abandoned.log"), time.Now().UTC())
	}); err != nil {
		t.Fatal(err)
	}
}

// SIGINT must stop the worker's whole process group, record enough to resume,
// and exit by the signal convention — not look like a runtime failure.
func TestSignalStopsTheWorkerAndLeavesAResumableRun(t *testing.T) {
	fixture := newRunnerFixture(t)
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	marker := filepath.Join(t.TempDir(), "grandchild.pid")
	blocking := fixture.fakeAgent(t, `( sleep 120 ) &
echo "$!" > `+marker+`
sleep 120
`)

	command := exec.Command(fixture.binary, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	command.Dir = fixture.root
	command.Env = append(os.Environ(), "FORGEPILOT_FAKE_AGENT="+blocking)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waitForSession(t, fixture, &output)
	waitForFile(t, marker)
	grandchild := strings.TrimSpace(readTestFile(t, marker))

	if err := command.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	err := command.Wait()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	if code != 130 {
		t.Fatalf("exit = %d\n%s", code, output.String())
	}
	if !strings.Contains(output.String(), "INTERRUPTED") {
		t.Fatalf("output:\n%s", output.String())
	}

	// The whole tree the session started is gone, not just its outermost process.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if exec.Command("kill", "-0", grandchild).Run() != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if exec.Command("kill", "-0", grandchild).Run() == nil {
		t.Fatalf("grandchild %s survived the signal", grandchild)
	}

	// The run is resumable, and its record no longer claims a live worker.
	runID := lastRun(t, fixture.root)
	record := loadRunRecord(t, fixture.root, runID)
	if _, present := record["worker"]; present {
		t.Fatalf("the interrupted run still claims a worker: %v", record["worker"])
	}
	if record["stop"].(map[string]any)["reason"] != "INTERRUPTED" {
		t.Fatalf("stop = %v", record["stop"])
	}
	statusOutput, statusCode := fixture.runForge(t, blocking, "run", "status", runID)
	if statusCode != 0 || !strings.Contains(statusOutput, "INTERRUPTED") {
		t.Fatalf("run status = %d\n%s", statusCode, statusOutput)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

// A run stopped for a Gate continues after the Gate is answered, on the budget
// and deadline it already had, in a new session.
func TestResumeContinuesAfterTheBlockerIsCleared(t *testing.T) {
	fixture := newRunnerFixture(t)
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	mustRun(t, fixture.binary, fixture.root, "gate", "open", "--work", "WI-001",
		"--question", "which store?", "--option", "postgres", "--option", "sqlite")
	agent := fixture.fakeAgent(t, implementsCleanly)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	if code != 2 || !strings.Contains(output, "WAIT_GATE") {
		t.Fatalf("first run exit = %d\n%s", code, output)
	}
	runID := lastRun(t, fixture.root)
	before := loadRunRecord(t, fixture.root, runID)

	mustRun(t, fixture.binary, fixture.root, "gate", "resolve", "GATE-001", "--option", "postgres", "--by", fixtureIdentity)
	output, code = fixture.runForge(t, agent, "run", "resume", runID)
	if code != 0 {
		t.Fatalf("resume exit = %d\n%s", code, output)
	}
	if !strings.Contains(output, "GOAL_COMPLETED") {
		t.Fatalf("resume output:\n%s", output)
	}
	if sessions := fixture.sessions(t); len(sessions) != 1 {
		t.Fatalf("sessions = %v", sessions)
	}
	after := loadRunRecord(t, fixture.root, runID)
	if after["deadline"].(string) != before["deadline"].(string) {
		t.Fatal("resume moved the deadline")
	}
	if after["steps"].(float64) <= before["steps"].(float64) {
		t.Fatalf("steps went from %v to %v; the resume did no work or reset the count",
			before["steps"], after["steps"])
	}
	if after["run_id"] != before["run_id"] {
		t.Fatal("resume started a different run")
	}
}

// A fresh `run` must settle the workers an earlier run left behind. The
// workspace lock proves no Runner is live; it proves nothing about a coding CLI
// that outlived one, and starting a second writer beside it is exactly what
// ownership is supposed to prevent.
func TestAFreshRunRefusesWhileAnEarlierWorkerCannotBeConfirmed(t *testing.T) {
	fixture := newRunnerFixture(t)
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	if _, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot", "--max-steps", "1"); code != 3 {
		t.Fatalf("seed run exit = %d", code)
	}
	runID := lastRun(t, fixture.root)
	record := loadRunRecord(t, fixture.root, runID)
	// What a SIGKILLed Runner leaves: a worker recorded without the identity that
	// would let anyone recognise it.
	delete(record, "stop")
	record["worker"] = map[string]any{
		"work_item_id": "WI-001", "attempt": 1,
		"session_dir": filepath.Join(fixture.root, ".forgepilot", "runs", runID, "wi-001-attempt-1"),
		"started_at":  time.Now().UTC().Format(time.RFC3339Nano),
		"identity":    map[string]any{"pid": os.Getpid(), "pgid": 0, "executable": "/bin/sh"},
	}
	writeRunRecord(t, fixture.root, runID, record)

	// A brand new run, naming no run id at all, must still refuse.
	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	if code != 2 || !strings.Contains(output, "RECOVERY_BLOCKED") {
		t.Fatalf("exit = %d\n%s", code, output)
	}
	if fixture.sessions(t) != nil {
		t.Fatal("a second writer was started beside an unconfirmable worker")
	}
	if !strings.Contains(output, runID) {
		t.Fatalf("the refusal does not name the run that is stuck:\n%s", output)
	}
	if runs, err := storage.ListRuns(fixture.root); err != nil || len(runs) != 1 {
		t.Fatalf("a blocked start created a new run: %v, %v", runs, err)
	}

	// Once the worker record is cleared by hand, the run proceeds.
	record = loadRunRecord(t, fixture.root, runID)
	delete(record, "worker")
	writeRunRecord(t, fixture.root, runID, record)
	if output, code = fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot"); code != 0 {
		t.Fatalf("exit after clearing the worker = %d\n%s", code, output)
	}
}

// The briefing tells a session not to touch .forgepilot/, but the sandbox it
// runs in can reach it. A session that writes governance state has invalidated
// everything downstream of itself, including its own account of what it did.
func TestASessionThatWritesForgePilotStateStopsTheRun(t *testing.T) {
	fixture := newRunnerFixture(t)
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	// The session forges a PASS for itself and claims to be finished.
	tamper := fixture.fakeAgent(t, `printf 'done\n' > "$workspace/$lower.txt"
sed 's/"status": "RUNNING"/"status": "VERIFIED"/' "$workspace/.forgepilot/state.json" > "$workspace/.forgepilot/state.tampered"
mv "$workspace/.forgepilot/state.tampered" "$workspace/.forgepilot/state.json"
printf '{"outcome":"implementation_finished","summary":"all done, verified"}' > "$result"
`)

	output, code := fixture.runForge(t, tamper, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	if code != 2 {
		t.Fatalf("exit = %d\n%s", code, output)
	}
	if !strings.Contains(output, "AGENT_WROTE_FORGEPILOT_STATE") {
		t.Fatalf("a session's write to state was not detected:\n%s", output)
	}
	if strings.Contains(output, "GOAL_COMPLETED") {
		t.Fatal("a forged VERIFIED was reported as Goal completion")
	}
}

// A fresh `run` settles the workers an earlier Runner left behind. `run resume`
// launches sessions on the same workspace and must clear the same bar: the
// worker that cannot be confirmed belongs to another run, but it writes this
// tree.
func TestResumeRefusesWhileAnotherRunsWorkerCannotBeConfirmed(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	// A Gate stops both runs with their budgets untouched, so the resume below
	// has the room to start a session — which is the thing it must not do.
	mustRun(t, fixture.binary, fixture.root, "gate", "open", "--work", "WI-001",
		"--question", "which store?", "--option", "postgres", "--option", "sqlite")
	agent := fixture.fakeAgent(t, implementsCleanly)

	first, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	if code != 2 {
		t.Fatalf("the first run did not stop on the Gate: %d\n%s", code, first)
	}
	second, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	if code != 2 {
		t.Fatalf("the second run did not stop on the Gate: %d\n%s", code, second)
	}
	// Two runs started in the same second are ordered by a random suffix, so the
	// ids are read from the runs themselves rather than from the directory order.
	abandoned, resumable := startedRun(t, first), startedRun(t, second)
	if abandoned == resumable {
		t.Fatal("the fixture produced one run where it needs two")
	}
	mustRun(t, fixture.binary, fixture.root, "gate", "resolve", "GATE-001", "--option", "postgres", "--by", fixtureIdentity)

	// What a SIGKILLed Runner leaves behind in the run it was driving: a worker
	// recorded without the identity that would let anyone recognise it.
	record := loadRunRecord(t, fixture.root, abandoned)
	delete(record, "stop")
	record["worker"] = map[string]any{
		"work_item_id": "WI-001", "attempt": 1,
		"session_dir": filepath.Join(fixture.root, ".forgepilot", "runs", abandoned, "wi-001-attempt-1"),
		"started_at":  time.Now().UTC().Format(time.RFC3339Nano),
		"identity":    map[string]any{"pid": os.Getpid(), "pgid": 0, "executable": "/bin/sh"},
	}
	writeRunRecord(t, fixture.root, abandoned, record)

	output, code := fixture.runForge(t, agent, "run", "resume", resumable)
	if code != 2 || !strings.Contains(output, "RECOVERY_BLOCKED") {
		t.Fatalf("exit = %d\n%s", code, output)
	}
	if sessions := fixture.sessions(t); sessions != nil {
		t.Fatalf("a second writer was started beside an unconfirmable worker: %v", sessions)
	}
	if !strings.Contains(output, abandoned) {
		t.Fatalf("the refusal does not name the run that is stuck:\n%s", output)
	}
}

// The digest around a session catches a session that edits governance state
// directly. It is not the only place untrusted code runs: the canonical check
// is the repository's own script, and this run's session just rewrote it.
func TestACanonicalCheckThatWritesForgePilotStateStopsTheRun(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	// The session itself never touches .forgepilot/. It leaves the forgery to
	// the verification it knows will run afterwards.
	tamper := fixture.fakeAgent(t, `printf 'done\n' > "$workspace/$lower.txt"
cat > "$workspace/verify.sh" <<SH
set -e
sed 's/"status": "VERIFYING"/"status": "VERIFIED"/' "$workspace/.forgepilot/state.json" > "$workspace/.forgepilot/state.tampered"
mv "$workspace/.forgepilot/state.tampered" "$workspace/.forgepilot/state.json"
echo "canonical check passed"
SH
printf '{"outcome":"implementation_finished","summary":"implemented %s"}' "$item" > "$result"
`)

	output, code := fixture.runForge(t, tamper, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	if code != 2 {
		t.Fatalf("exit = %d\n%s", code, output)
	}
	if !strings.Contains(output, "AGENT_WROTE_FORGEPILOT_STATE") {
		t.Fatalf("a write to state from inside the canonical check was not detected:\n%s", output)
	}
	if strings.Contains(output, "GOAL_COMPLETED") {
		t.Fatal("a forged VERIFIED was reported as Goal completion")
	}
}

// startedRun reads the run id a `run` command announced.
func startedRun(t *testing.T, output string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if fields := strings.Fields(line); len(fields) > 1 && fields[0] == "Run" {
			return fields[1]
		}
	}
	t.Fatalf("no run id in:\n%s", output)
	return ""
}

// SIGINT and SIGTERM both stop the run, and the exit code says which: 130 and
// 143 are what a shell, a supervisor and a CI runner read to tell a Ctrl-C
// apart from a termination they asked for.
func TestTerminationExitsWithItsOwnCode(t *testing.T) {
	fixture := newRunnerFixture(t)
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	blocking := fixture.fakeAgent(t, "sleep 120\n")

	command := exec.Command(fixture.binary, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	command.Dir = fixture.root
	command.Env = append(os.Environ(), "FORGEPILOT_FAKE_AGENT="+blocking)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waitForSession(t, fixture, &output)

	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	err := command.Wait()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	if code != 143 {
		t.Fatalf("exit = %d, want 143\n%s", code, output.String())
	}

	// The record says which signal ended it, so `run status` does not report a
	// termination as a Ctrl-C long after the terminal has gone.
	runID := lastRun(t, fixture.root)
	statusOutput, statusCode := fixture.runForge(t, blocking, "run", "status", runID)
	if statusCode != 0 {
		t.Fatalf("run status = %d\n%s", statusCode, statusOutput)
	}
	if !strings.Contains(statusOutput, "Exit code: 143") {
		t.Fatalf("run status:\n%s", statusOutput)
	}
}
