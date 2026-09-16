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

func TestFindVerificationLogUsesRunIDTokenBoundary(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, ".forgepilot", "logs")
	if err := os.MkdirAll(logs, 0755); err != nil {
		t.Fatal(err)
	}
	wrong := filepath.Join(logs, "VR-1000-abcdef-attempt.log")
	if err := os.WriteFile(wrong, []byte("wrong run"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := FindVerificationLog(root, "VR-100"); !errors.Is(err, ErrVerificationLogNotFound) {
		t.Fatalf("lookup VR-100 error = %v, want ErrVerificationLogNotFound", err)
	}
	right := filepath.Join(logs, "VR-100-abcdef-attempt.log")
	if err := os.WriteFile(right, []byte("right run"), 0644); err != nil {
		t.Fatal(err)
	}
	path, err := FindVerificationLog(root, "VR-100")
	if err != nil {
		t.Fatalf("lookup VR-100: %v", err)
	}
	if path != right {
		t.Fatalf("lookup path = %q, want %q", path, right)
	}
}

func TestFindVerificationLogRejectsAmbiguousMatches(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, ".forgepilot", "logs")
	if err := os.MkdirAll(logs, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"VR-100-first.log", "VR-100-second.log"} {
		if err := os.WriteFile(filepath.Join(logs, name), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := FindVerificationLog(root, "VR-100"); !errors.Is(err, ErrVerificationLogAmbiguous) {
		t.Fatalf("ambiguous lookup error = %v, want ErrVerificationLogAmbiguous", err)
	}
}
