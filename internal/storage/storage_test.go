package storage

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/carl/forgepilot/internal/work"
)

func TestInitRetryAndFailedWritePreserveState(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	if err := Update(root, func(state *work.State) error { return state.AddGoal("g", "Goal", "", root, time.Now().UTC()) }); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, stateDirectory, "state.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, stateDirectory)
	if err := os.Chmod(directory, 0555); err != nil {
		t.Fatal(err)
	}
	err = Update(root, func(state *work.State) error { return state.AddGoal("other", "Other", "", root, time.Now().UTC()) })
	if restoreErr := os.Chmod(directory, 0755); restoreErr != nil {
		t.Fatal(restoreErr)
	}
	if err == nil {
		t.Fatal("write succeeded in read-only state directory")
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("failed write changed state")
	}
}

func TestLoadRejectsCorruptAndFutureState(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, stateDirectory, "state.json")
	for _, contents := range []string{"{", `{"schema_version":2,"next_work_id":1,"goals":[],"work_items":[]}`} {
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(root); err == nil {
			t.Fatalf("accepted %q", contents)
		}
	}
}

func TestLockReleasedAfterProcessExit(t *testing.T) {
	if directory := os.Getenv("FORGEPILOT_LOCK_HOLDER"); directory != "" {
		_ = withLock(directory, func() error { os.Exit(0); return nil })
		return
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, stateDirectory)
	command := exec.Command(os.Args[0], "-test.run=TestLockReleasedAfterProcessExit")
	command.Env = append(os.Environ(), "FORGEPILOT_LOCK_HOLDER="+directory)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("holder: %v: %s", err, output)
	}
	if err := withLock(directory, func() error { return nil }); err != nil {
		t.Fatalf("reacquire lock: %v", err)
	}
}

func TestMovedRepositoryIsRejected(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "original")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	if err := Update(root, func(state *work.State) error { return state.AddGoal("g", "Goal", "", root, time.Now().UTC()) }); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(base, "moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(moved); err == nil {
		t.Fatal("accepted state after repository move")
	}
}
