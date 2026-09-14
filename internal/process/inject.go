package process

import "sync/atomic"

// This file exists for one reason: the failure this package is built around —
// a process group that cannot be confirmed gone — is not a failure real
// hardware can be asked for on demand. SIGKILL cannot be ignored, so producing
// a genuinely unkillable group would mean contriving an uninterruptible wait,
// which is neither reproducible nor something a test should leave behind.
//
// The seam is deliberately here rather than anywhere a user can reach: it is an
// internal package, it takes a function rather than reading an environment
// variable, and there is no flag, setting or bypass in the CLI that can turn it
// on. Nothing in the production path consults it except the single check in
// Stop, which is a pointer load on a nil pointer in every real run.
var injectedCleanupFailure atomic.Pointer[func(int) error]

// InjectCleanupFailure makes every confirmation report the given failure until
// the returned function is called. Tests use it to drive the real app, runner
// and storage paths through a cleanup that could not be confirmed; it must
// never be called from production code.
func InjectCleanupFailure(fail func(int) error) func() {
	injectedCleanupFailure.Store(&fail)
	return func() { injectedCleanupFailure.Store(nil) }
}

func injectedFailure(pgid int) error {
	if fail := injectedCleanupFailure.Load(); fail != nil && *fail != nil {
		return (*fail)(pgid)
	}
	return nil
}
