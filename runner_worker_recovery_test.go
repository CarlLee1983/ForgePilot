package forgepilot_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Worker and Pending answer the same question — may a new writer start on this
// workspace — and they used to answer it differently. Pending refused a leader
// that had exited leaving its group populated; Worker read the same fact as
// "the worker is gone" and cleared the entry. A record with a Worker and no
// Pending is not a legacy shape either: the agent launch path saves the worker
// identity first and only writes a Pending once a cleanup has failed, so a
// crash in between leaves exactly this.
// See docs/adr/0020-worker-ownership-is-fail-closed.md.

// orphanedLeader starts a process group whose leader exits immediately while a
// child it forked keeps the group — and the workspace — alive. It returns the
// leader's pid and the group id, which are the same number because the leader
// was made its own group leader.
func orphanedLeader(t *testing.T) (int, int) {
	t.Helper()
	// The leader execs sleep in the background and exits; the sleep inherits its
	// process group, which is precisely the shape ADR-0020 was narrowed for.
	command := exec.Command("/bin/sh", "-c", "sleep 300 & exit 0")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	pgid := command.Process.Pid
	t.Cleanup(func() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	})
	// Reaped here, so the leader is genuinely gone rather than a zombie that is
	// still a member of its own group.
	if err := command.Wait(); err != nil {
		t.Fatalf("the leader did not exit cleanly: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if syscall.Kill(-pgid, 0) == nil {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("the orphaned child never joined its parent's process group")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return pgid, pgid
}

// leaveWorkerOnly seeds a finished run and rewrites its record as one that
// crashed between saving the worker identity and recording any pending
// execution. Nothing about it mentions a pending cleanup: that is the point.
func leaveWorkerOnly(t *testing.T, fixture runnerFixture, agent string, pid, pgid int) string {
	t.Helper()
	if _, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot", "--max-steps", "1"); code != 3 {
		t.Fatal("seed run did not stop on its step budget")
	}
	runID := lastRun(t, fixture.root)
	record := loadRunRecord(t, fixture.root, runID)
	delete(record, "pending")
	record["worker"] = map[string]any{
		"work_item_id": "WI-001",
		"attempt":      float64(1),
		"session_dir":  filepath.Join(fixture.root, ".forgepilot", "runs", runID, "sessions", "WI-001-1"),
		"started_at":   time.Now().UTC().Format(time.RFC3339Nano),
		"identity": map[string]any{
			"pid": float64(pid), "pgid": float64(pgid),
			"executable": "/bin/sh",
			// What the operating system reported at launch. It no longer holds that
			// pid, which is the whole case under test: the leader is gone and the
			// group it left behind is not.
			"observed_start":   "Mon Sep 14 00:00:00 2026",
			"observed_command": "/bin/sh -c sleep 300 & exit 0",
			"recorded_at":      time.Now().UTC().Format(time.RFC3339Nano),
		},
	}
	writeRunRecord(t, fixture.root, runID, record)
	return runID
}

// A worker whose leader has exited but whose process group still has members
// must block every door back into the workspace, exactly as a pending cleanup
// does. Before this it blocked none of them.
func TestAWorkerWhoseGroupOutlivedItBlocksEveryWayBackIntoTheWorkspace(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md", "b.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	fixture.seedGoal(t, "second", []string{"specs/stories/b.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	pid, pgid := orphanedLeader(t)
	runID := leaveWorkerOnly(t, fixture, agent, pid, pgid)
	before := loadRunRecord(t, fixture.root, runID)
	sessionsBefore := len(fixture.sessions(t))

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

	// A blocked attempt is not work: it spends nothing and moves nothing.
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
	if after["worker"] == nil {
		t.Fatal("the worker entry was cleared without the group being confirmed gone")
	}

	// Once the group really is gone the block lifts, in the same invocation that
	// establishes it rather than on a second attempt by hand.
	stopGroup(t, pgid)
	output, code := fixture.runForge(t, agent, "run", "resume", runID)
	if strings.Contains(output, "RECOVERY_BLOCKED") {
		t.Fatalf("a confirmed-empty group still blocked recovery: exit = %d\n%s", code, output)
	}
	cleared := loadRunRecord(t, fixture.root, runID)
	if cleared["worker"] != nil {
		t.Fatalf("a confirmed worker was not cleared: %v", cleared["worker"])
	}
}

// A pid that has been reused belongs to somebody else. Recovery may neither
// signal it nor read its liveness as its own worker still running, so the only
// safe answer is to refuse while leaving the process untouched.
func TestRecoveryNeitherSignalsNorTrustsAReusedPid(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	// A live group standing in for whatever took the pid over. Its identity does
	// not match what was recorded, so it is Unrelated.
	pgid := liveGroup(t)
	runID := leaveWorkerOnly(t, fixture, agent, pgid, pgid)
	// The recorded command is deliberately not what this process is running.
	record := loadRunRecord(t, fixture.root, runID)
	worker := record["worker"].(map[string]any)
	worker["identity"].(map[string]any)["observed_command"] = "/usr/bin/some-other-program --not-ours"
	writeRunRecord(t, fixture.root, runID, record)

	output, code := fixture.runForge(t, agent, "run", "resume", runID)
	if code != 2 || !strings.Contains(output, "RECOVERY_BLOCKED") {
		t.Fatalf("a reused pid did not block recovery: exit = %d\n%s", code, output)
	}
	if syscall.Kill(-pgid, 0) != nil {
		t.Fatal("recovery signalled a process group whose ownership it could not confirm")
	}
}

// A recovery that succeeds must not be refused by the stop the previous process
// wrote. RECOVERY_BLOCKED in Stop is why that run ended; it is not this pass's
// verdict, and reading it as one made a settled workspace need a second attempt
// by hand before anything could start.
func TestAnOldRecoveryBlockedStopDoesNotBlockASettledWorkspace(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md", "b.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	fixture.seedGoal(t, "second", []string{"specs/stories/b.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	pgid := liveGroup(t)
	runID := leaveUnconfirmedCleanup(t, fixture, agent, pgid)
	// The refusal the previous process recorded, durable exactly as it would be.
	if _, code := fixture.runForge(t, agent, "run", "--goal", "second", "--runtime", "fake", "--snapshot"); code != 2 {
		t.Fatal("the seeded workspace did not block a second goal")
	}
	blocked := loadRunRecord(t, fixture.root, runID)
	stop, ok := blocked["stop"].(map[string]any)
	if !ok || stop["reason"] != "RECOVERY_BLOCKED" {
		t.Fatalf("the blocked run did not record its refusal: %v", blocked["stop"])
	}

	stopGroup(t, pgid)
	output, code := fixture.runForge(t, agent, "run", "--goal", "second", "--runtime", "fake", "--snapshot")
	if strings.Contains(output, "RECOVERY_BLOCKED") {
		t.Fatalf("a settled workspace was refused by an old stop: exit = %d\n%s", code, output)
	}
	settled := loadRunRecord(t, fixture.root, runID)
	if pending, present := settled["pending"]; present && pending != nil {
		if entries, ok := pending.([]any); ok && len(entries) != 0 {
			t.Fatalf("a confirmed pending execution was not cleared: %v", pending)
		}
	}
}
