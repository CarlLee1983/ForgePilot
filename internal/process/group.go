// Package process runs and stops the process groups ForgePilot owns. It exists
// because two callers need exactly the same three guarantees and neither may
// have its own version of them: a child is started in its own process group, a
// group is stopped within a bounded time, and the stop is confirmed rather than
// assumed. `make verify` forks and a coding CLI forks, so signalling only the
// process ForgePilot launched leaves the real work running.
//
// See docs/adr/0020-worker-ownership-is-fail-closed.md. Nothing here decides
// ownership: callers establish that a group is theirs before asking for it to
// be stopped.
package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// ErrNotSettled reports that a process group was signalled but could not be
// confirmed gone. It is not an engineering result and never a verification
// failure: it says the caller may not safely continue, which is a different
// statement from anything about the code under test.
var ErrNotSettled = errors.New("a managed process group could not be confirmed stopped")

const (
	// TerminationGrace is how long a signalled group has to exit on its own. It
	// lets a canonical check flush what it has produced into its log.
	TerminationGrace = 5 * time.Second
	// KillGrace bounds how long the group is watched after SIGKILL before the
	// cleanup is reported as unconfirmed. Without it "we sent SIGKILL" would be
	// mistaken for "nothing is running", which is the assumption this package
	// exists to remove.
	KillGrace = 5 * time.Second
	// OutputDrainGrace bounds how long already-produced output is collected once
	// the group has been settled. A background child that inherited the write end
	// of the pipe would otherwise hold collection open for as long as it lives.
	OutputDrainGrace = 2 * time.Second
	// CleanupGrace is the worst case one managed execution may spend stopping
	// after it has been told to: the leader's own grace, then the group's two
	// rounds, then output collection. It is deliberately named and bounded —
	// "the deadline has passed" and "everything has stopped" are different
	// instants, and callers document the gap rather than pretend it is zero.
	// See docs/adr/0021-execution-limits-are-bounded-and-named.md.
	CleanupGrace = TerminationGrace + TerminationGrace + KillGrace + OutputDrainGrace
	// pollInterval is how often a signalled group is re-checked.
	pollInterval = 20 * time.Millisecond
)

// Run is one managed execution: the command is started in its own process
// group, waited for under ctx, and its group is settled before Run returns —
// on every path, including a clean exit. A command that exits 0 having forked a
// watcher has not finished owning anything until that watcher is gone.
type Run struct {
	// ExitCode is meaningful only when Completed is true.
	ExitCode int
	// Completed reports that the command reached its own end *and* handed back a
	// usable exit code. It is decided at the moment that happened, not by
	// inspecting ctx afterwards: a cancellation that arrives beside a result must
	// not discard the result, and a result that arrives after a cancellation must
	// not overwrite the reason the execution ended.
	//
	// A wait that failed for its own reasons — ECHILD, an I/O failure — is not a
	// completion, because "we never learned how it ended" and "it ended with code
	// zero" are different statements and only one of them may become Evidence.
	Completed bool
	// Cleanup is non-nil when the process group could not be confirmed empty.
	// The command's own result, if it produced one, still stands.
	Cleanup error
}

// Start runs command to completion or to ctx's end, whichever happens first,
// streaming its combined output to log. command.SysProcAttr and its output
// fields are set here and must not be set by the caller.
func Start(ctx context.Context, command *exec.Cmd, log io.Writer) (Run, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Checked before anything is launched: a step that is already over must not
	// be the one that starts a new external process.
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}
	drain, err := Attach(command, log)
	if err != nil {
		return Run{}, err
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		drain()
		return Run{}, err
	}
	// Setpgid makes the child its own group leader, so its pid is its pgid.
	pgid := command.Process.Pid
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()

	var waitErr error
	completed := false
	select {
	case waitErr = <-finished:
		completed = true
	case <-ctx.Done():
		// The command may have reached its own end in the same instant. Nothing is
		// waited for here: a result that had already arrived by the time the
		// cancellation was observed is kept, and anything still running is
		// stopped. A cancellation and a result that arrive together therefore
		// resolve in favour of the result, which is as fine a distinction as two
		// independent clocks can make.
		select {
		case waitErr = <-finished:
			completed = true
		default:
			waitErr = StopLeader(pgid, finished)
		}
	}
	// The leader has been reaped by the time this runs. That order matters: an
	// unreaped zombie is still a member of its own group, so confirming before
	// reaping would always report a group that is still alive.
	run := Run{Cleanup: Stop(pgid)}
	drain()
	if !completed {
		return run, nil
	}
	var exit *exec.ExitError
	switch {
	case errors.As(waitErr, &exit):
		// A non-zero exit is the command's result, not a failure to run it.
		run.Completed, run.ExitCode = true, exit.ExitCode()
		return run, nil
	case waitErr == nil:
		run.Completed = true
		return run, nil
	}
	return run, waitErr
}

// StopLeader asks a group to stop and waits for its leader to be reaped,
// insisting with SIGKILL once the grace period is over. Callers that started a
// process themselves use it before Stop: an unreaped zombie is still a member
// of its own group, so confirming before reaping would always say "still alive".
func StopLeader(pgid int, finished <-chan error) error {
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	select {
	case err := <-finished:
		return err
	case <-time.After(TerminationGrace):
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	return <-finished
}

// Stop asks a process group to stop, then insists, then confirms. It returns
// nil only when the group is observably gone; an unconfirmed group is reported
// rather than assumed away.
func Stop(pgid int) error {
	if pgid <= 0 {
		return fmt.Errorf("%w: no process group was recorded", ErrNotSettled)
	}
	if Gone(pgid) {
		return nil
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	if awaitGone(pgid, TerminationGrace) {
		return nil
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	if awaitGone(pgid, KillGrace) {
		return nil
	}
	return fmt.Errorf("%w: process group %d is still alive after SIGTERM and SIGKILL", ErrNotSettled, pgid)
}

// Gone reports whether a process group has no members left. Only ESRCH counts:
// a permission error means something is there that we may not signal, which is
// the opposite of an empty group.
func Gone(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	return errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH)
}

func awaitGone(pgid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		if Gone(pgid) {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(pollInterval)
	}
}

// Attach gives a child a real file descriptor rather than an io.Writer.
// os/exec would otherwise attach a copy goroutine whose completion depends on
// every inherited descriptor being closed, and a background grandchild holding
// that pipe open makes Wait block for as long as it lives — which is exactly
// how cleanup code comes to be unreachable. The returned function is called
// once the group has been settled and bounds collection of what was produced.
func Attach(command *exec.Cmd, log io.Writer) (func(), error) {
	if file, ok := log.(*os.File); ok && file != nil {
		command.Stdout, command.Stderr = file, file
		return func() {}, nil
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create output pipe: %w", err)
	}
	command.Stdout, command.Stderr = writer, writer
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		_, _ = io.Copy(log, reader)
	}()
	return func() {
		_ = writer.Close()
		_ = reader.SetReadDeadline(time.Now().Add(OutputDrainGrace))
		<-copied
		_ = reader.Close()
	}, nil
}
