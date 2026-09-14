package process

import (
	"sync/atomic"
	"time"
)

// This file exists for one reason: the failure this package is built around —
// a process group that cannot be confirmed gone — is not a failure real
// hardware can be asked for on demand. SIGKILL cannot be ignored, so producing
// a genuinely unkillable group would mean contriving an uninterruptible wait,
// which is neither reproducible nor something a test should leave behind.
//
// The seam is deliberately here rather than anywhere a user can reach: it is an
// internal package, it takes a function rather than reading an environment
// variable, and there is no flag, setting or bypass in the CLI that can turn it
// on. Nothing in the production path consults it except the single check at the
// top of a stop, which in every real run is one atomic load that finds nil —
// preceded by reading the remaining budget and the context's command, both of
// which are cheap and neither of which does anything when the pointer is nil.
var injectedCleanupFailure atomic.Pointer[func(int, []string, time.Duration) error]

// InjectCleanupFailure makes confirmations report the given failure until the
// returned function is called. Tests use it to drive the real app, runner and
// storage paths through a cleanup that could not be confirmed; it must never be
// called from production code.
//
// The command being settled is passed as well as its group id, because a
// verification settles several managed processes in turn — a snapshot's Git
// calls, a runtime probe, `make -n verify`, the check itself, then the removal
// of the checkout — and a test that cannot say which of them it means is a test
// that asserts about whichever one happened to run first. It is empty whenever
// the stop did not come through process.Start: a recovery settling a group it
// did not launch here, and also internal/agent's long-lived Session, which
// holds its own handle and calls Settle directly. A test that needs to name an
// agent session cannot do it by argv. The cleanup allowance left at that moment is passed too: a
// stage that opened its window before the work rather than before the cleanup
// reaches its own tidying with nothing left, and that is a difference only a
// test that can see the number will notice.
func InjectCleanupFailure(fail func(pgid int, command []string, remaining time.Duration) error) func() {
	injectedCleanupFailure.Store(&fail)
	return func() { injectedCleanupFailure.Store(nil) }
}

func injectedFailure(pgid int, command []string, remaining time.Duration) error {
	if fail := injectedCleanupFailure.Load(); fail != nil && *fail != nil {
		return (*fail)(pgid, command, remaining)
	}
	return nil
}
