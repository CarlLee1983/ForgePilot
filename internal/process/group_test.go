package process

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// script writes an executable shell script and returns its path.
func script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func run(t *testing.T, path string) *exec.Cmd {
	t.Helper()
	return exec.Command("/bin/sh", path)
}

// awaitFile blocks until a marker appears, within a bound. Tests say what stage
// execution has reached by handshake rather than by guessing with a sleep.
func awaitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
}

func recordedPID(t *testing.T, path string) int {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(contents)))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return pid
}

func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// A command that exits on its own having forked a child has not finished owning
// anything. Before this, cleanup lived only on the cancellation path, so the
// ordinary path — the common one — handed the child to whatever ran next.
func TestACleanExitStillEmptiesItsProcessGroup(t *testing.T) {
	directory := t.TempDir()
	child := filepath.Join(directory, "child.pid")
	command := run(t, script(t, `( sleep 300 ) &
echo "$!" > `+child+`
exit 0
`))
	log := new(bytes.Buffer)

	result, err := Start(context.Background(), command, log)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Completed || result.ExitCode != 0 {
		t.Fatalf("result = %+v, want a completed exit 0", result)
	}
	if result.Cleanup != nil {
		t.Fatalf("cleanup = %v", result.Cleanup)
	}
	if pid := recordedPID(t, child); alive(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("child %d outlived the command that forked it", pid)
	}
}

// A non-zero exit is an engineering result, and it changes nothing about
// ownership: the group is emptied just the same, and the exit code survives.
func TestAFailingExitStillEmptiesItsProcessGroup(t *testing.T) {
	directory := t.TempDir()
	child := filepath.Join(directory, "child.pid")
	command := run(t, script(t, `( sleep 300 ) &
echo "$!" > `+child+`
exit 7
`))

	result, err := Start(context.Background(), command, new(bytes.Buffer))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Completed || result.ExitCode != 7 {
		t.Fatalf("result = %+v, want a completed exit 7", result)
	}
	if pid := recordedPID(t, child); alive(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("child %d outlived the failing command that forked it", pid)
	}
}

// A background child inherits whatever descriptors the command was given. When
// those are a pipe os/exec owns, Wait does not return until every writer has
// closed it — so a child that lives for five minutes makes the cleanup code
// after Wait unreachable for five minutes. Giving the child a descriptor we own
// and bounding collection ourselves is what keeps that from happening.
func TestAnInheritedOutputDescriptorDoesNotHoldTheCommandOpen(t *testing.T) {
	directory := t.TempDir()
	child := filepath.Join(directory, "child.pid")
	// The child keeps stdout open for far longer than this test may take.
	command := run(t, script(t, `( sleep 300; echo late ) &
echo "$!" > `+child+`
echo done
exit 0
`))
	log := new(bytes.Buffer)

	finished := make(chan Run, 1)
	go func() {
		result, err := Start(context.Background(), command, log)
		if err != nil {
			t.Error(err)
		}
		finished <- result
	}()
	select {
	case result := <-finished:
		if !result.Completed || result.ExitCode != 0 {
			t.Fatalf("result = %+v", result)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the command never returned; an inherited descriptor held it open")
	}
	if !strings.Contains(log.String(), "done") {
		t.Fatalf("the output produced before the child was left behind was lost: %q", log.String())
	}
	if pid := recordedPID(t, child); alive(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("child %d survived", pid)
	}
}

// A process that ignores SIGTERM is stopped anyway, within a bounded time.
// "We sent a signal" is not the same statement as "nothing is running", which
// is why the stop is confirmed and only then reported as clean.
func TestAProcessThatIgnoresTerminationIsStillStoppedWithinBounds(t *testing.T) {
	directory := t.TempDir()
	started := filepath.Join(directory, "started")
	command := run(t, script(t, `trap '' TERM
: > `+started+`
while true; do sleep 0.1; done
`))
	ctx, cancel := context.WithCancel(context.Background())

	finished := make(chan Run, 1)
	go func() {
		result, err := Start(ctx, command, new(bytes.Buffer))
		if err != nil {
			t.Error(err)
		}
		finished <- result
	}()
	awaitFile(t, started)
	cancel()

	select {
	case result := <-finished:
		if result.Completed {
			t.Fatal("a process that was killed was reported as having completed")
		}
		if result.Cleanup != nil {
			t.Fatalf("cleanup = %v; the group was not confirmed gone", result.Cleanup)
		}
	case <-time.After(TerminationGrace + KillGrace + 30*time.Second):
		t.Fatal("an unresponsive process was not stopped within the documented grace")
	}
}

// A result that genuinely arrived is not thrown away by a cancellation that
// arrives after it. The handshake here is exact: the cancellation is issued
// only once the whole process group is observably gone.
func TestACompletedCommandIsNotDiscardedByALaterCancellation(t *testing.T) {
	directory := t.TempDir()
	leader := filepath.Join(directory, "leader.pid")
	command := run(t, script(t, `echo "$$" > `+leader+`
exit 3
`))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Cancelled only once the whole group is observably gone, so "the command
	// finished first" is an observation rather than a hope about scheduling.
	go func() {
		defer cancel()
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			contents, err := os.ReadFile(leader)
			if err != nil {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			pgid, err := strconv.Atoi(strings.TrimSpace(string(contents)))
			if err != nil || !Gone(pgid) {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			return
		}
	}()

	result, err := Start(ctx, command, new(bytes.Buffer))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Completed || result.ExitCode != 3 {
		t.Fatalf("result = %+v; a finished command was reported as cancelled", result)
	}
}

// A step that has already been told to stop must not be the one that starts a
// new external process. This is the preflight blind spot in concrete form.
func TestAnAlreadyCancelledContextStartsNothing(t *testing.T) {
	directory := t.TempDir()
	marker := filepath.Join(directory, "ran")
	command := run(t, script(t, `: > `+marker+`
`))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := Start(ctx, command, new(bytes.Buffer)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Start err = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a process was started after the context had already ended")
	}
}

// Stop refuses to call an unrecorded group settled. Reporting "nothing to do"
// for a group nobody can name is how a live writer stops being visible.
func TestStopRefusesAnUnrecordedGroup(t *testing.T) {
	if err := Stop(0); !errors.Is(err, ErrNotSettled) {
		t.Fatalf("Stop(0) = %v, want ErrNotSettled", err)
	}
}

// "It reached its own end" and "it handed back a usable exit code" are the same
// statement only when the wait itself succeeded. A wait that failed for its own
// reasons leaves exit code zero, and reporting that as a completion is how an
// execution error becomes a PASS.
func TestAnUnusableWaitResultIsNotACompletion(t *testing.T) {
	for _, expectation := range []struct {
		body     string
		exitCode int
	}{
		{"exit 0\n", 0},
		{"exit 9\n", 9},
	} {
		result, err := Start(context.Background(), run(t, script(t, expectation.body)), new(bytes.Buffer))
		if err != nil {
			t.Fatalf("%q: %v", expectation.body, err)
		}
		// A non-zero exit is the command's own result, so it is a completion and
		// carries no error: an engineering FAIL must reach Evidence as a FAIL.
		if !result.Completed || result.ExitCode != expectation.exitCode {
			t.Fatalf("%q: result = %+v", expectation.body, result)
		}
	}
	// And a command that could not be launched at all is not a completion with
	// exit code zero, which is the shape the caller must never read as a result.
	missing, err := Start(context.Background(), exec.Command(filepath.Join(t.TempDir(), "not-here")), new(bytes.Buffer))
	if err == nil {
		t.Fatal("a command that does not exist was started")
	}
	if missing.Completed {
		t.Fatalf("result = %+v; a command that never ran read as completed", missing)
	}
}
