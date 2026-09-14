package forgepilot_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These cover the third gap ticket 07 names: a cleanup nobody could confirm
// used to live only in Stop.Reason, which a resume clears and a new run never
// reads, and the workspace scan skipped any record whose Worker was nil — which
// is every canonical check, runtime probe and Git child there has ever been.
// See docs/adr/0022-pending-cleanup-outlives-the-process.md.

// liveGroup starts a process in its own group and returns its pgid. It stands
// in for whatever a previous Runner left behind: the recovery judgement asks the
// operating system, so the test has to give it something real to find.
func liveGroup(t *testing.T) int {
	t.Helper()
	command := exec.Command("/bin/sh", "-c", "sleep 300")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	pgid := command.Process.Pid
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()
	t.Cleanup(func() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		select {
		case <-finished:
		case <-time.After(30 * time.Second):
		}
	})
	return pgid
}

// stopGroup ends a group the test started and waits for it to be gone, so a
// later assertion about "the blocker is cleared" is about a fact rather than
// about a signal having been sent.
func stopGroup(t *testing.T, pgid int) {
	t.Helper()
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(-pgid, 0) != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process group %d did not go away", pgid)
}

// leaveUnconfirmedCleanup seeds one finished run and rewrites its record as a
// run whose canonical check left a process group nobody could confirm empty.
// Nothing about it mentions a worker: that is the point.
func leaveUnconfirmedCleanup(t *testing.T, fixture runnerFixture, agent string, pgid int) string {
	t.Helper()
	if _, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot", "--max-steps", "1"); code != 3 {
		t.Fatalf("seed run did not stop on its step budget")
	}
	runID := lastRun(t, fixture.root)
	record := loadRunRecord(t, fixture.root, runID)
	record["pending"] = []any{map[string]any{
		"id": "pe-1", "kind": "CANONICAL_CHECK", "phase": "CLEANUP_UNCONFIRMED",
		"work_item_id":   "WI-001",
		"location":       filepath.Join(fixture.root, ".forgepilot", "worktrees", "WI-001-abcdef"),
		"identity":       map[string]any{"pid": 0, "pgid": pgid},
		"unresolved":     true,
		"stop_reason":    "on a signal",
		"cleanup_detail": "process group is still alive after SIGTERM and SIGKILL",
		"observed_at":    time.Now().UTC().Format(time.RFC3339Nano),
	}}
	writeRunRecord(t, fixture.root, runID, record)
	return runID
}

// An unconfirmed cleanup has to outlive the process that observed it. All three
// doors into this workspace are the same door, and none of them may open while
// something may still be writing behind it.
func TestAnUnconfirmedCleanupBlocksEveryWayBackIntoTheWorkspace(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md", "b.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	fixture.seedGoal(t, "second", []string{"specs/stories/b.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	pgid := liveGroup(t)
	runID := leaveUnconfirmedCleanup(t, fixture, agent, pgid)
	before := loadRunRecord(t, fixture.root, runID)
	sessionsBefore := len(fixture.sessions(t))

	// Each of these is a new operating-system process reading the record back.
	for _, attempt := range []struct {
		name      string
		arguments []string
	}{
		{name: "resume the same run", arguments: []string{"run", "resume", runID}},
		{name: "start a new run", arguments: []string{"run", "--goal", "queue", "--runtime", "fake", "--snapshot"}},
		{name: "another goal in the same workspace", arguments: []string{"run", "--goal", "second", "--runtime", "fake", "--snapshot"}},
	} {
		output, code := fixture.runForge(t, agent, attempt.arguments...)
		if code != 2 || !strings.Contains(output, "RECOVERY_BLOCKED") {
			t.Fatalf("%s: exit = %d, want 2 with RECOVERY_BLOCKED\n%s", attempt.name, code, output)
		}
		if sessions := fixture.sessions(t); len(sessions) != sessionsBefore {
			t.Fatalf("%s started a session behind a blocked recovery: %v", attempt.name, sessions)
		}
	}

	// A blocked attempt is not work. It must not spend the run's budget or move
	// the deadline the original run was given.
	after := loadRunRecord(t, fixture.root, runID)
	if after["deadline"] != before["deadline"] {
		t.Fatalf("a blocked attempt moved the deadline: %v -> %v", before["deadline"], after["deadline"])
	}
	if after["steps"] != before["steps"] {
		t.Fatalf("a blocked attempt spent steps: %v -> %v", before["steps"], after["steps"])
	}
	if !sameAttempts(after["attempts"], before["attempts"]) {
		t.Fatalf("a blocked attempt spent attempts: %v -> %v", before["attempts"], after["attempts"])
	}
	// And the record it was judged on is still there to judge again.
	if pending, ok := after["pending"].([]any); !ok || len(pending) != 1 {
		t.Fatalf("the pending execution was cleared without being confirmed: %v", after["pending"])
	}
}

// Clearing the last stop reason is not the same as clearing a recovery block,
// and a resume does the first. Before this they were the same field.
func TestResumeClearsTheStopButNotThePendingCleanup(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	pgid := liveGroup(t)
	runID := leaveUnconfirmedCleanup(t, fixture, agent, pgid)

	output, code := fixture.runForge(t, agent, "run", "resume", runID)
	if code != 2 || !strings.Contains(output, "RECOVERY_BLOCKED") {
		t.Fatalf("resume exit = %d\n%s", code, output)
	}
	record := loadRunRecord(t, fixture.root, runID)
	stop, ok := record["stop"].(map[string]any)
	if !ok || stop["reason"] != "RECOVERY_BLOCKED" {
		t.Fatalf("stop = %v, want a RECOVERY_BLOCKED stop", record["stop"])
	}
	// The block names the group, so a person has something to check.
	if detail, _ := stop["detail"].(string); !strings.Contains(detail, "process group") {
		t.Fatalf("the block did not name what is still running: %q", detail)
	}
}

// Once the group really is gone, the same record recovers. The block is a
// refusal to guess, not a dead end — and it lifts because the fact changed, not
// because a flag was passed.
func TestRecoveryResumesOnceTheGroupIsConfirmedGone(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	pgid := liveGroup(t)
	runID := leaveUnconfirmedCleanup(t, fixture, agent, pgid)
	if _, code := fixture.runForge(t, agent, "run", "resume", runID); code != 2 {
		t.Fatal("the seeded block did not stop a resume")
	}
	stopGroup(t, pgid)

	// The run was seeded against a one-step budget, so it stops on that budget
	// rather than on the block. Which reason it stops for is the whole assertion:
	// RECOVERY_BLOCKED would mean the group was still unaccounted for.
	output, code := fixture.runForge(t, agent, "run", "resume", runID)
	if code == 2 || strings.Contains(output, "RECOVERY_BLOCKED") {
		t.Fatalf("resume after the group ended was still blocked: exit = %d\n%s", code, output)
	}
	if !strings.Contains(output, "confirmed stopped") {
		t.Fatalf("the resume did not report confirming the group\n%s", output)
	}
	// Cleared only now, and only because it was confirmed.
	if record := loadRunRecord(t, fixture.root, runID); record["pending"] != nil {
		t.Fatalf("pending = %v, want it resolved", record["pending"])
	}
}

// A pending execution recorded before its process could be identified is the
// shape a crash in the launch window leaves. It cannot be shown to have
// stopped, so it must not be read as nothing to do.
func TestAPendingExecutionWithNoObservedIdentityIsRefused(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	if _, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot", "--max-steps", "1"); code != 3 {
		t.Fatal("seed run did not stop on its step budget")
	}
	runID := lastRun(t, fixture.root)
	record := loadRunRecord(t, fixture.root, runID)
	record["pending"] = []any{map[string]any{
		"id": "pe-1", "kind": "VERIFICATION", "phase": "PENDING_START",
		"work_item_id": "WI-001", "location": fixture.root,
		"identity": map[string]any{"pid": 0, "pgid": 0}, "unresolved": true,
		"observed_at": time.Now().UTC().Format(time.RFC3339Nano),
	}}
	writeRunRecord(t, fixture.root, runID, record)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	if code != 2 || !strings.Contains(output, "RECOVERY_BLOCKED") {
		t.Fatalf("exit = %d, want 2 with RECOVERY_BLOCKED\n%s", code, output)
	}
}

// Compatibility. A record written before Pending existed has none, and that is
// read as "this run recorded nothing beyond its worker" — not as a claim that
// the workspace is clear. A record that does not parse is not an empty one.
func TestOlderRunRecordsStillRecoverTheWayTheyDid(t *testing.T) {
	t.Run("a normal finished record still reads", func(t *testing.T) {
		fixture := newRunnerFixture(t, "a.md")
		mustRun(t, fixture.binary, fixture.root, "init")
		fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
		agent := fixture.fakeAgent(t, implementsCleanly)
		if _, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot"); code != 0 {
			t.Fatal("the seed run did not reach the review boundary")
		}
		runID := lastRun(t, fixture.root)
		record := loadRunRecord(t, fixture.root, runID)
		delete(record, "pending")
		writeRunRecord(t, fixture.root, runID, record)
		// Nothing pending, no worker: a second run reads it and carries on.
		if _, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot"); code != 0 {
			t.Fatal("a record with no pending field blocked a new run")
		}
	})

	t.Run("an unreadable record blocks", func(t *testing.T) {
		fixture := newRunnerFixture(t, "a.md")
		mustRun(t, fixture.binary, fixture.root, "init")
		fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
		agent := fixture.fakeAgent(t, implementsCleanly)
		if _, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot", "--max-steps", "1"); code != 3 {
			t.Fatal("seed run did not stop on its step budget")
		}
		runID := lastRun(t, fixture.root)
		path := filepath.Join(fixture.root, ".forgepilot", "runs", runID, "run.json")
		if err := os.WriteFile(path, []byte("{not json"), 0644); err != nil {
			t.Fatal(err)
		}
		output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
		if code != 2 || !strings.Contains(output, "RECOVERY_BLOCKED") {
			t.Fatalf("a corrupt record did not block: exit = %d\n%s", code, output)
		}
	})

	t.Run("a legacy worker record still recovers", func(t *testing.T) {
		fixture := newRunnerFixture(t, "a.md")
		mustRun(t, fixture.binary, fixture.root, "init")
		fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
		agent := fixture.fakeAgent(t, implementsCleanly)
		if _, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot", "--max-steps", "1"); code != 3 {
			t.Fatal("seed run did not stop on its step budget")
		}
		runID := lastRun(t, fixture.root)
		record := loadRunRecord(t, fixture.root, runID)
		delete(record, "stop")
		delete(record, "pending")
		// A worker whose pid is long gone: the pre-Pending recovery path answers
		// this, and must go on answering it.
		record["worker"] = map[string]any{
			"work_item_id": "WI-001", "attempt": 1,
			"session_dir": filepath.Join(fixture.root, ".forgepilot", "runs", runID, "wi-001-attempt-1"),
			"started_at":  time.Now().UTC().Format(time.RFC3339Nano),
			"identity": map[string]any{"pid": 999999, "pgid": 999999, "executable": "/bin/sh",
				"observed_start": "Mon Jan  1 00:00:00 2001", "observed_command": "/bin/sh gone"},
		}
		writeRunRecord(t, fixture.root, runID, record)
		output, code := fixture.runForge(t, agent, "run", "resume", runID)
		if code == 2 || strings.Contains(output, "RECOVERY_BLOCKED") {
			t.Fatalf("a legacy worker record that is plainly gone did not recover: exit = %d\n%s", code, output)
		}
		if !strings.Contains(output, "is gone") {
			t.Fatalf("the legacy recovery path did not report on the worker\n%s", output)
		}
	})
}

// sameAttempts compares the decoded attempt map, which is what "a blocked
// attempt spent no budget" means concretely.
func sameAttempts(first, second any) bool {
	return reflect.DeepEqual(first, second)
}

// A record can carry all three at once: a worker, a pending execution, and the
// RECOVERY_BLOCKED stop that was written when the session's cleanup could not
// be confirmed. That is exactly what internal/runner writes on that path.
//
// Recovery must then judge both halves. Reading "this record already has a
// stop" as "recovery already refused" makes the worker half unreachable, so the
// worker is never confirmed, never cleared, and the workspace stays blocked for
// ever — including after the pending half becomes confirmable. `run resume` of
// that exact run would be the only way out, because resume is the one path that
// clears the stop first; a new run, or another Goal, could never get past it.
func TestARecordWithAStopStillJudgesItsWorker(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md", "b.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	fixture.seedGoal(t, "second", []string{"specs/stories/b.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	if _, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot", "--max-steps", "1"); code != 3 {
		t.Fatal("seed run did not stop on its step budget")
	}
	runID := lastRun(t, fixture.root)
	record := loadRunRecord(t, fixture.root, runID)
	// The shape the session-cleanup path leaves behind, with both halves already
	// resolvable: the worker's pid is long gone and the pending group is empty.
	record["stop"] = map[string]any{
		"reason": "RECOVERY_BLOCKED", "detail": "the session's process group could not be confirmed stopped",
		"at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	record["worker"] = map[string]any{
		"work_item_id": "WI-001", "attempt": 1,
		"session_dir": filepath.Join(fixture.root, ".forgepilot", "runs", runID, "wi-001-attempt-1"),
		"started_at":  time.Now().UTC().Format(time.RFC3339Nano),
		"identity": map[string]any{"pid": 999999, "pgid": 999999, "executable": "/bin/sh",
			"observed_start": "Mon Jan  1 00:00:00 2001", "observed_command": "/bin/sh gone"},
	}
	delete(record, "pending")
	writeRunRecord(t, fixture.root, runID, record)

	// Twice: the first pass must settle the worker, the second must find nothing
	// left to settle. A recovery that never clears the worker blocks for ever.
	for round := 1; round <= 2; round++ {
		output, code := fixture.runForge(t, agent, "run", "--goal", "second", "--runtime", "fake", "--snapshot")
		if round == 2 && (code == 2 || strings.Contains(output, "RECOVERY_BLOCKED")) {
			t.Fatalf("round %d: the workspace is still blocked although both halves were confirmable\n%s", round, output)
		}
		if record := loadRunRecord(t, fixture.root, runID); round == 2 && record["worker"] != nil {
			t.Fatalf("round %d: the worker was never judged: %v", round, record["worker"])
		}
	}
}
