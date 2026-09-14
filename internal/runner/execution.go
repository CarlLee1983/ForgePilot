package runner

import (
	"context"
	"errors"
	"sync"
	"time"
)

// These are the causes a cancelled context carries, so a layer holding only the
// context can still say something better than "context canceled".
var (
	errStoppedOnSignal   = errors.New("stopped on signal")
	errRunDeadlinePassed = errors.New("the run passed its deadline")
	errStepTimedOut      = errors.New("the step passed its own timeout")
)

// stopCause is why one blocking step was cut short. It is recorded at the
// moment the thing happened rather than inferred afterwards from
// context.Err(): a context only remembers that it ended, and "cancelled" is
// the same word for a Ctrl-C, an expired run and a step that used up its own
// timeout — three answers a person reading the record needs to tell apart.
type stopCause int

const (
	// causeNone means the step ran to its own end.
	causeNone stopCause = iota
	// causeSignal is SIGINT or SIGTERM reaching the Runner.
	causeSignal
	// causeRunDeadline is the whole run passing the deadline it was given at
	// startup. It is never extended, by a resume or by anything else.
	causeRunDeadline
	// causeStepTimeout is this one step outliving --agent-timeout or
	// --verify-timeout while the run still had duration left.
	causeStepTimeout
)

// execution bounds one blocking step — an agent session, a verification — and
// remembers why it ended.
//
// Its deadline is the earlier of two: the run's own deadline, and this step's
// timeout counted from now. A step never buys more time than the run has left,
// which is the whole difference between a total limit and a limit that is
// re-granted in full at every step.
//
// When two reasons arrive together the order is fixed: a signal outranks the
// run deadline, which outranks the step's own timeout. A person who asked the
// process to stop is not told it ran out of time, and a step that expires in
// the same instant as the run it belongs to is reported against the run.
type execution struct {
	ctx    context.Context
	cancel context.CancelCauseFunc

	// signalled and expired answer the two higher-precedence questions without
	// waiting, so a tie can be broken by rule rather than by whichever timer the
	// scheduler happened to deliver first.
	signalled func() bool
	expired   func() bool

	timers []*time.Timer

	mutex sync.Mutex
	cause stopCause
}

// newExecution starts watching for the three ways a step can be cut short.
// Timers are real even when the clock is injected: the duration they are armed
// with is measured with the run's own clock, so a test that moves that clock
// moves the deadline it is testing, and production and test compare the same
// two quantities rather than two different notions of "now".
func (runner *Runner) newExecution(timeout time.Duration) *execution {
	ctx, cancel := context.WithCancelCause(context.Background())
	deadline := runner.record.Deadline
	stop := runner.options.Stop
	execution := &execution{
		ctx:    ctx,
		cancel: cancel,
		signalled: func() bool {
			select {
			case <-stop:
				return true
			default:
				return false
			}
		},
		expired: func() bool { return !runner.now().Before(deadline) },
	}

	// This subtraction is the effective deadline:
	// min(run deadline, now + step timeout), expressed as two timers racing.
	remaining := deadline.Sub(runner.now())
	go execution.watch(stop, execution.arm(remaining), execution.arm(timeout))
	return execution
}

// arm returns the channel that fires after the given duration, or one that has
// already fired when the duration is gone. Both limits are validated positive
// before a run starts, so the second case means the run's deadline is behind us.
func (execution *execution) arm(after time.Duration) <-chan time.Time {
	if after <= 0 {
		fired := make(chan time.Time, 1)
		fired <- time.Now()
		return fired
	}
	timer := time.NewTimer(after)
	execution.timers = append(execution.timers, timer)
	return timer.C
}

func (execution *execution) watch(stop <-chan struct{}, runDeadline, stepTimeout <-chan time.Time) {
	select {
	case <-stop:
		execution.fire(causeSignal)
	case <-runDeadline:
		execution.fire(causeRunDeadline)
	case <-stepTimeout:
		execution.fire(causeStepTimeout)
	case <-execution.ctx.Done():
	}
}

// fire records the reason once and cancels the step. The recorded reason is
// upgraded to a higher-precedence one that is already true, so two limits
// reaching the watcher in the same instant classify by rule rather than by
// which channel the runtime picked.
func (execution *execution) fire(cause stopCause) {
	execution.mutex.Lock()
	if execution.cause == causeNone {
		if cause != causeSignal && execution.signalled() {
			cause = causeSignal
		} else if cause == causeStepTimeout && execution.expired() {
			cause = causeRunDeadline
		}
		execution.cause = cause
	}
	recorded := execution.cause
	execution.mutex.Unlock()
	execution.cancel(causeError(recorded))
}

// Cause reports why the step was cut short, or causeNone if it was not.
func (execution *execution) Cause() stopCause {
	execution.mutex.Lock()
	defer execution.mutex.Unlock()
	return execution.cause
}

// release stops the watcher and its timers. It is deferred by every caller: an
// execution that outlived its step would keep a timer alive for as long as the
// run does.
func (execution *execution) release() {
	for _, timer := range execution.timers {
		timer.Stop()
	}
	execution.cancel(context.Canceled)
}

// causeError is the value handed to context.Cause, so a layer that only has the
// context can still report something better than "context canceled".
func causeError(cause stopCause) error {
	switch cause {
	case causeSignal:
		return errStoppedOnSignal
	case causeRunDeadline:
		return errRunDeadlinePassed
	case causeStepTimeout:
		return errStepTimedOut
	default:
		return context.Canceled
	}
}

// describe names a cause for a stop detail. A cleanup that could not be
// confirmed is reported under RECOVERY_BLOCKED, which would otherwise lose the
// answer to "was this a Ctrl-C or an expired run" — the first thing whoever has
// to sort it out will want to know.
func describe(cause stopCause) string {
	switch cause {
	case causeSignal:
		return "on a signal"
	case causeRunDeadline:
		return "at the run's deadline"
	case causeStepTimeout:
		return "at its own timeout"
	default:
		return "on its own"
	}
}

// stopReason maps a cut-short step onto the reason the run records. onTimeout
// is the step's own reason — AGENT_TIMEOUT or VERIFY_TIMEOUT — which only the
// caller knows.
func (runner *Runner) stopReasonFor(cause stopCause, onTimeout StopReason) (StopReason, bool) {
	switch cause {
	case causeSignal:
		return runner.stopSignal(), true
	case causeRunDeadline:
		return StopMaxDuration, true
	case causeStepTimeout:
		return onTimeout, true
	default:
		return "", false
	}
}

// newFactsExecution bounds the short Git reads the loop does between steps —
// resolving HEAD, digesting the working tree for a Candidate comparison. They
// are not steps and get no step timeout of their own, but they are external
// processes running this repository's hooks and filters, so they are bound by
// the run's deadline and by the same stop signal as everything else. A read
// that cannot be interrupted is a blind spot between two things that can be.
func (runner *Runner) newFactsExecution() *execution {
	return runner.newExecution(runner.record.Deadline.Sub(runner.now()))
}
