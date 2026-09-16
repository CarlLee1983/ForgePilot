package storage

import (
	"errors"
	"testing"
)

func TestWithCanonicalVerificationLockLosesBeforeCallback(t *testing.T) {
	root := t.TempDir()
	holding := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- WithCanonicalVerificationLock(root, func() error {
			close(holding)
			<-release
			return nil
		})
	}()
	<-holding

	called := false
	err := WithCanonicalVerificationLock(root, func() error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrCanonicalVerificationInFlight) {
		t.Fatalf("second canonical verification error = %v, want ErrCanonicalVerificationInFlight", err)
	}
	if called {
		t.Fatal("losing canonical verification ran its callback")
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("holding canonical verification: %v", err)
	}
}
