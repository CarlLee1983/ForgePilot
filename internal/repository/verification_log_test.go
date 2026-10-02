package repository

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenExclusiveLogDoesNotTruncateExistingWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "VR-100-candidate-attempt.log")
	first, err := OpenExclusiveLog(path)
	if err != nil {
		t.Fatalf("open first exclusive log: %v", err)
	}
	if _, err := first.WriteString("winner contents"); err != nil {
		t.Fatalf("write winner: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close winner: %v", err)
	}

	second, err := OpenExclusiveLog(path)
	if second != nil {
		_ = second.Close()
		t.Fatal("collision returned a log handle")
	}
	if !errors.Is(err, ErrVerificationLogCollision) || !errors.Is(err, os.ErrExist) {
		t.Fatalf("second exclusive log error = %v, want typed existing-file collision", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read winning log: %v", err)
	}
	if got, want := string(contents), "winner contents"; got != want {
		t.Fatalf("winner contents = %q, want %q", got, want)
	}
}
