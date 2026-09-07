package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ErrVerificationInFlight reports that another live process already holds this
// Work Item's Verification Run.
var ErrVerificationInFlight = errors.New("a verification of this work item is already running")

// WithVerifyLock runs an operation while holding the Work Item's verification
// lock. The lock is per Work Item, so unrelated work verifies concurrently, and
// the operating system releases it when the process dies — which is what makes a
// leftover VERIFYING status reliably distinguishable from a live run. A PID
// recorded in state could not do this: PIDs are reused.
func WithVerifyLock(root, workID string, operation func() error) error {
	root, err := canonicalRoot(root)
	if err != nil {
		return err
	}
	path, err := verifyLockPath(root, workID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	// A live run holds this lock for its whole duration, while a status probe
	// holds a shared lock only momentarily. Retrying briefly therefore separates
	// the two without ever waiting on a real run.
	var lockErr error
	for attempt := 0; attempt < 5; attempt++ {
		if lockErr = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); lockErr == nil {
			break
		}
		if !errors.Is(lockErr, syscall.EWOULDBLOCK) {
			return fmt.Errorf("lock verification of %s: %w", workID, lockErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if lockErr != nil {
		return fmt.Errorf("%w: %s", ErrVerificationInFlight, workID)
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return operation()
}

// VerificationRunning reports whether a live process holds the Work Item's
// verification lock. It never creates the lock file, so read-only commands stay
// free of side effects: a missing file simply means no run was ever started.
func VerificationRunning(root, workID string) bool {
	root, err := canonicalRoot(root)
	if err != nil {
		return false
	}
	path, err := verifyLockPath(root, workID)
	if err != nil {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	// Probe with a shared lock: it fails exactly when a run holds the exclusive
	// one, and unlike an exclusive probe it does not make one read-only command
	// look like a conflict to another.
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK)
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return false
}

func verifyLockPath(root, workID string) (string, error) {
	if workID == "" || workID != filepath.Base(workID) || workID == "." || workID == ".." {
		return "", fmt.Errorf("invalid work item ID %q", workID)
	}
	return filepath.Join(root, stateDirectory, "locks", "verify-"+workID), nil
}
