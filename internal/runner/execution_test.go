package runner

import (
	"context"
	"errors"
	"testing"
	"time"
)

// testRunner is a Runner with just enough of a record to bound a step. Nothing
// here is launched: what is under test is which of three limits a step is
// given, and which one it says ended it.
func testRunner(deadline time.Time, stop <-chan struct{}) *Runner {
	now := time.Now().UTC()
	return &Runner{
		options: Options{Stop: stop, Now: func() time.Time { return now }},
		record:  &Record{Deadline: deadline},
	}
}

func awaitCause(t *testing.T, execution *execution, within time.Duration) stopCause {
	t.Helper()
	select {
	case <-execution.ctx.Done():
	case <-time.After(within):
		t.Fatalf("the step was not cut short within %s", within)
	}
	return execution.Cause()
}

// A step never buys more time than the run has left. Granting each step its own
// full timeout is what let a run with an eight-hour ceiling spend a session's
// worth of time past it, then a verification's worth on top of that.
func TestAStepIsBoundedByWhicheverLimitComesFirst(t *testing.T) {
	// The run has almost no time left; the step's own timeout is enormous.
	runner := testRunner(time.Now().UTC().Add(30*time.Millisecond), nil)
	execution := runner.newExecution(time.Hour)
	defer execution.release()

	if cause := awaitCause(t, execution, 10*time.Second); cause != causeRunDeadline {
		t.Fatalf("cause = %v, want the run deadline", cause)
	}
	if cause := context.Cause(execution.ctx); !errors.Is(cause, errRunDeadlinePassed) {
		t.Fatalf("context cause = %v", cause)
	}
}

// The other way round: a run with hours left and a step that outlives its own
// timeout is the step's problem, and must be reported against the step's flag.
func TestAStepThatOutlivesItsOwnTimeoutSaysSo(t *testing.T) {
	runner := testRunner(time.Now().UTC().Add(time.Hour), nil)
	execution := runner.newExecution(30 * time.Millisecond)
	defer execution.release()

	if cause := awaitCause(t, execution, 10*time.Second); cause != causeStepTimeout {
		t.Fatalf("cause = %v, want the step timeout", cause)
	}
}

// A person who asked the process to stop is not told it ran out of time. The
// signal is already true when either timer fires, so the tie is broken by rule
// rather than by whichever channel the scheduler delivered first.
func TestASignalOutranksEveryExpiry(t *testing.T) {
	stop := make(chan struct{})
	close(stop)
	// Both other limits are already gone too, so every source is ready at once.
	runner := testRunner(time.Now().UTC().Add(-time.Hour), stop)
	execution := runner.newExecution(time.Nanosecond)
	defer execution.release()

	if cause := awaitCause(t, execution, 10*time.Second); cause != causeSignal {
		t.Fatalf("cause = %v, want the signal", cause)
	}
}

// When a step's timeout and the run's deadline arrive together, the run wins:
// the run is over either way, and reporting the step's flag would send whoever
// reads the record to a limit that was not the binding one.
func TestTheRunDeadlineOutranksACoincidentStepTimeout(t *testing.T) {
	runner := testRunner(time.Now().UTC().Add(-time.Hour), nil)
	execution := runner.newExecution(time.Hour)
	defer execution.release()
	// Delivered as a step timeout; the run deadline is already behind us, so the
	// recorded reason is upgraded to the one that actually binds.
	execution.fire(causeStepTimeout)

	if cause := execution.Cause(); cause != causeRunDeadline {
		t.Fatalf("cause = %v, want the run deadline", cause)
	}
}

// The first reason to arrive is the one that stands. A later limit expiring
// during the bounded cleanup must not rewrite why the step ended.
func TestTheFirstReasonToArriveIsTheOneRecorded(t *testing.T) {
	runner := testRunner(time.Now().UTC().Add(time.Hour), nil)
	execution := runner.newExecution(time.Hour)
	defer execution.release()

	execution.fire(causeStepTimeout)
	execution.fire(causeRunDeadline)
	if cause := execution.Cause(); cause != causeStepTimeout {
		t.Fatalf("cause = %v; a later reason overwrote the one that ended the step", cause)
	}
}

// The mapping the run record is written from. AGENT_TIMEOUT and VERIFY_TIMEOUT
// differ only in what the caller passes, which is the whole point: the step's
// own reason is the only part this layer cannot know.
func TestEachCauseMapsOntoItsOwnStopReason(t *testing.T) {
	stop := make(chan struct{})
	runner := testRunner(time.Now().UTC().Add(time.Hour), stop)
	runner.options.Signalled = func() StopReason { return StopTerminated }

	for _, expectation := range []struct {
		cause     stopCause
		onTimeout StopReason
		want      StopReason
		ok        bool
	}{
		{causeSignal, StopVerifyTimeout, StopTerminated, true},
		{causeRunDeadline, StopVerifyTimeout, StopMaxDuration, true},
		{causeStepTimeout, StopVerifyTimeout, StopVerifyTimeout, true},
		{causeStepTimeout, StopAgentTimeout, StopAgentTimeout, true},
		{causeNone, StopAgentTimeout, "", false},
	} {
		reason, ok := runner.stopReasonFor(expectation.cause, expectation.onTimeout)
		if ok != expectation.ok || reason != expectation.want {
			t.Fatalf("cause %v with %s = %s, %t; want %s, %t",
				expectation.cause, expectation.onTimeout, reason, ok, expectation.want, expectation.ok)
		}
	}
}

// A step that ran to its own end reports no reason at all, so nothing
// classifies an ordinary completion as a limit being hit.
func TestAStepThatWasNotCutShortReportsNoCause(t *testing.T) {
	runner := testRunner(time.Now().UTC().Add(time.Hour), nil)
	execution := runner.newExecution(time.Hour)
	defer execution.release()

	if cause := execution.Cause(); cause != causeNone {
		t.Fatalf("cause = %v, want none", cause)
	}
}
