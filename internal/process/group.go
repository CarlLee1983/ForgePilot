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

// NotSettled names the group that could not be confirmed, so a caller can carry
// the fact forward as a recovery record rather than only as a sentence. The
// pgid is the thing whoever has to sort this out will look for, and a message
// is not something the next process can check.
// See docs/adr/0022-pending-cleanup-outlives-the-process.md.
type NotSettled struct {
	PGID   int
	Reason string
}

func (unsettled *NotSettled) Error() string {
	return fmt.Sprintf("%s: %s", ErrNotSettled, unsettled.Reason)
}

func (unsettled *NotSettled) Unwrap() error { return ErrNotSettled }

// UnsettledGroup reports the process group an error says was left unconfirmed,
// and whether the error said so at all.
func UnsettledGroup(err error) (int, bool) {
	var unsettled *NotSettled
	if errors.As(err, &unsettled) {
		return unsettled.PGID, true
	}
	return 0, errors.Is(err, ErrNotSettled)
}

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
	// after it has been told to: the leader's two rounds, then the group's two
	// rounds, then output collection. It is deliberately named and bounded —
	// "the deadline has passed" and "everything has stopped" are different
	// instants, and callers document the gap rather than pretend it is zero.
	//
	// It is a total, not a per-stage allowance. One Budget is drawn for a whole
	// stopped execution and every stage below shares it, so the sum above is a
	// ceiling on the path rather than a description of its first step. Before
	// that, StopLeader waited on its wait channel without a bound after SIGKILL,
	// which made this constant describe a path that could not honour it.
	// See docs/adr/0021-execution-limits-are-bounded-and-named.md and
	// docs/adr/0022-pending-cleanup-outlives-the-process.md.
	CleanupGrace = TerminationGrace + KillGrace + TerminationGrace + KillGrace + OutputDrainGrace
	// pollInterval is how often a signalled group is re-checked.
	pollInterval = 20 * time.Millisecond
)

// Budget is the single allowance one stopped execution has for cleaning up. It
// is drawn once and handed down, because a budget re-granted in full at every
// helper is not a bound on the path — it is a bound on one step of a path that
// can have any number of steps. A nil Budget means an unbudgeted caller and
// each stage then gets its own nominal grace, which is what a standalone
// recovery action wants.
type Budget struct{ deadline time.Time }

// NewBudget starts one execution's cleanup allowance.
func NewBudget() *Budget { return &Budget{deadline: time.Now().Add(CleanupGrace)} }

// budgetKey carries a shared Budget down a call chain. A context is used rather
// than a parameter because the chain runs through internal/repository's Git
// helpers, which have no business knowing what a cleanup budget is — they only
// have to not silently start a new one.
type budgetKey struct{}

// WithBudget attaches a cleanup allowance to ctx, so every managed process
// started under it draws on that one allowance instead of opening a fresh one.
// This is what keeps a cleanup path made of several commands — remove a
// worktree, prune, close a runtime — to a single stated total rather than a
// full grace per command.
func WithBudget(ctx context.Context, budget *Budget) context.Context {
	return context.WithValue(ctx, budgetKey{}, budget)
}

// BudgetFrom returns the allowance ctx carries, or nil when it carries none.
func BudgetFrom(ctx context.Context) *Budget {
	if ctx == nil {
		return nil
	}
	budget, _ := ctx.Value(budgetKey{}).(*Budget)
	return budget
}

// commandKey carries the argv of the process being settled, so the internal
// failure seam can say which managed command it is standing in for. It travels
// on the context for the same reason the budget does: the chain runs through
// helpers that have no business knowing about it.
type commandKey struct{}

func withCommand(ctx context.Context, argv []string) context.Context {
	return context.WithValue(ctx, commandKey{}, argv)
}

func commandFrom(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	argv, _ := ctx.Value(commandKey{}).([]string)
	return argv
}

// Remaining is how much of the allowance is left. A nil Budget is an unbudgeted
// caller, which still has a full nominal grace ahead of it.
func (budget *Budget) Remaining() time.Duration {
	if budget == nil {
		return CleanupGrace
	}
	return time.Until(budget.deadline)
}

// share is what remains of the budget, never more than the stage asked for. A
// budget that is spent returns zero, which every stage below reads as "there is
// no time left to confirm this", not as "wait forever".
func (budget *Budget) share(want time.Duration) time.Duration {
	if budget == nil {
		return want
	}
	remaining := time.Until(budget.deadline)
	if remaining <= 0 {
		return 0
	}
	if remaining < want {
		return remaining
	}
	return want
}

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
	ctx = withCommand(ctx, command.Args)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		drain(nil)
		return Run{}, err
	}
	// Setpgid makes the child its own group leader, so its pid is its pgid.
	pgid := command.Process.Pid
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()

	waitErr, completed, cleanup := Settle(ctx, pgid, finished, drain)
	run := Run{Cleanup: cleanup}
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

// Settle ends one managed execution and reports the three things that are
// genuinely separate: what the wait said, whether the command reached its own
// end, and whether its process group could be confirmed empty afterwards.
//
// It is exported because there are two shapes of caller and only one correct
// sequence. Start blocks on a command and wants a Run; internal/agent holds a
// long-lived Session and wants to keep the handle. Before this they each spelt
// the sequence out, which meant the invariant they both depend on — one budget
// for the whole stop, the leader reaped before the group is confirmed, the
// leader's own verdict kept rather than discarded — had two implementations and
// only one of them would be updated next time.
//
// drain is called last and within the same budget: collecting output is the end
// of one stop, not a fresh allowance after it. It may be nil.
func Settle(ctx context.Context, pgid int, finished <-chan error, drain func(*Budget)) (waitErr error, completed bool, cleanup error) {
	var unsettled error
	var budget *Budget
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
			// One budget for this whole stop, drawn at the moment the execution was
			// told to end and shared by every stage below it — or inherited, when
			// the caller is already spending one.
			budget = inheritedBudget(ctx)
			// An unconfirmed leader is kept, not discarded: it is the difference
			// between "the process is gone" and "we stopped asking", and only the
			// first of those lets the next step start.
			if err := StopLeader(pgid, finished, budget); errors.Is(err, ErrNotSettled) {
				unsettled = err
			}
		}
	}
	if budget == nil {
		budget = inheritedBudget(ctx)
	}
	// The leader has been reaped by the time this runs. That order matters: an
	// unreaped zombie is still a member of its own group, so confirming before
	// reaping would always report a group that is still alive.
	cleanup = errors.Join(unsettled, stopCommand(pgid, budget, commandFrom(ctx)))
	if drain != nil {
		drain(budget)
	}
	return waitErr, completed, cleanup
}

// inheritedBudget is the caller's allowance when it has one, and a fresh one
// otherwise. An execution running inside somebody else's cleanup must not open
// a second full grace: that is how a path with several stops stops having a
// total anyone can state.
func inheritedBudget(ctx context.Context) *Budget {
	if budget := BudgetFrom(ctx); budget != nil {
		return budget
	}
	return NewBudget()
}

// StopLeader asks a group to stop and waits for its leader to be reaped,
// insisting with SIGKILL once the grace period is over. Callers that started a
// process themselves use it before Stop: an unreaped zombie is still a member
// of its own group, so confirming before reaping would always say "still alive".
//
// Both waits are bounded and both draw on the same budget. A wait that never
// comes back is reported as ErrNotSettled rather than waited for: "we sent
// SIGKILL" and "the process is gone" are different statements, and a caller
// that cannot tell them apart is the one that starts a second writer. The wait
// channel is left to its own consumer — a result that arrives afterwards lands
// in a buffered channel nobody reads again, so it can neither be lost into a
// second Wait nor overwrite the reason this execution ended.
func StopLeader(pgid int, finished <-chan error, budget *Budget) error {
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	if err, arrived := awaitWait(finished, budget.share(TerminationGrace)); arrived {
		return err
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	if err, arrived := awaitWait(finished, budget.share(KillGrace)); arrived {
		return err
	}
	return &NotSettled{PGID: pgid, Reason: fmt.Sprintf("no wait result for process group %d arrived after SIGTERM and SIGKILL", pgid)}
}

// awaitWait reports the wait result if it arrives within the bound, and whether
// it did. A zero bound is a budget that is already spent, which is answered
// without blocking rather than by waiting forever.
func awaitWait(finished <-chan error, within time.Duration) (error, bool) {
	if within <= 0 {
		select {
		case err := <-finished:
			return err, true
		default:
			return nil, false
		}
	}
	timer := time.NewTimer(within)
	defer timer.Stop()
	select {
	case err := <-finished:
		return err, true
	case <-timer.C:
		return nil, false
	}
}

// Stop asks a process group to stop, then insists, then confirms. It returns
// nil only when the group is observably gone; an unconfirmed group is reported
// rather than assumed away. Its two rounds share the caller's budget, so a
// cleanup path made of several stops still has one explainable total.
func Stop(pgid int, budget *Budget) error { return stopCommand(pgid, budget, nil) }

// stopCommand is Stop, told which managed command it is settling. Only the
// internal failure seam reads that; the stop itself is identical.
func stopCommand(pgid int, budget *Budget, argv []string) error {
	if err := injectedFailure(pgid, argv, budget.Remaining()); err != nil {
		return err
	}
	if pgid <= 0 {
		return &NotSettled{Reason: "no process group was recorded"}
	}
	if Gone(pgid) {
		return nil
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	if awaitGone(pgid, budget.share(TerminationGrace)) {
		return nil
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	if awaitGone(pgid, budget.share(KillGrace)) {
		return nil
	}
	return &NotSettled{PGID: pgid, Reason: fmt.Sprintf("process group %d is still alive after SIGTERM and SIGKILL", pgid)}
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

// awaitGone polls until the group is empty or the bound is up. A spent budget
// still buys one check: the question "is it gone" has an answer that costs
// nothing, and refusing to ask it would report a settled group as unconfirmed.
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
// once the group has been settled and bounds collection of what was produced,
// drawing on the same cleanup budget as the stop that preceded it: output
// collection is the last stage of one stop, not a fresh allowance after it.
func Attach(command *exec.Cmd, log io.Writer) (func(*Budget), error) {
	if file, ok := log.(*os.File); ok && file != nil {
		command.Stdout, command.Stderr = file, file
		return func(*Budget) {}, nil
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
	return func(budget *Budget) {
		_ = writer.Close()
		// A spent budget still gets a read deadline rather than none: the deadline
		// is what makes this return at all when a background grandchild is holding
		// the write end open.
		share := budget.share(OutputDrainGrace)
		if share <= 0 {
			share = time.Millisecond
		}
		_ = reader.SetReadDeadline(time.Now().Add(share))
		<-copied
		_ = reader.Close()
	}, nil
}
