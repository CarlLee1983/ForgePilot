package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/CarlLee1983/ForgePilot/internal/work"
)

const stateDirectory = ".forgepilot"

func Init(root string) error {
	var err error
	root, err = canonicalRoot(root)
	if err != nil {
		return err
	}
	if !isRepositoryRoot(root) {
		return errors.New("init must run at a Git repository root")
	}
	directory := filepath.Join(root, stateDirectory)
	if err := os.MkdirAll(filepath.Join(directory, "locks"), 0755); err != nil {
		return err
	}
	return withLock(directory, func() error {
		if _, err := os.Stat(statePath(directory)); err == nil {
			return addIgnore(root)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := save(directory, work.NewState()); err != nil {
			return err
		}
		return addIgnore(root)
	})
}

func FindRoot(start string) (string, error) {
	directory, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if info, err := os.Stat(filepath.Join(directory, stateDirectory)); err == nil && info.IsDir() {
			return canonicalRoot(directory)
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", errors.New("no .forgepilot state found; run forgepilot init first")
		}
		directory = parent
	}
}

func Load(root string) (work.State, error) {
	root, err := canonicalRoot(root)
	if err != nil {
		return work.State{}, err
	}
	state, err := load(filepath.Join(root, stateDirectory))
	if err != nil {
		return work.State{}, err
	}
	return state, validateRepository(state, root)
}

func Update(root string, operation func(*work.State) error) error {
	var err error
	root, err = canonicalRoot(root)
	if err != nil {
		return err
	}
	directory := filepath.Join(root, stateDirectory)
	return withLock(directory, func() error {
		state, err := load(directory)
		if err != nil {
			return err
		}
		if err := validateRepository(state, root); err != nil {
			return err
		}
		if err := operation(&state); err != nil {
			return err
		}
		if err := state.Validate(); err != nil {
			return err
		}
		return save(directory, state)
	})
}

func load(directory string) (work.State, error) {
	file, err := os.Open(statePath(directory))
	if err != nil {
		return work.State{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var state work.State
	if err := decoder.Decode(&state); err != nil {
		return work.State{}, fmt.Errorf("read state: %w", err)
	}
	if err := ensureEOF(decoder); err != nil {
		return work.State{}, fmt.Errorf("read state: %w", err)
	}
	if err := state.Validate(); err != nil {
		return work.State{}, fmt.Errorf("invalid state: %w", err)
	}
	return state, nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("state contains extra JSON values")
	}
	return nil
}

func save(directory string, state work.State) error {
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return writeFileAtomically(directory, statePath(directory), encoded)
}

// writeFileAtomically leaves either the previous contents or the complete new
// contents at the destination, never a truncated file.
func writeFileAtomically(directory, destination string, encoded []byte) error {
	temporary, err := os.CreateTemp(directory, ".state-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, destination); err != nil {
		return err
	}
	if directoryHandle, err := os.Open(directory); err == nil {
		defer directoryHandle.Close()
		if err := directoryHandle.Sync(); err != nil {
			return err
		}
	}
	return nil
}

func withLock(directory string, operation func() error) error {
	if err := os.MkdirAll(filepath.Join(directory, "locks"), 0755); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(directory, "locks", "state.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock state: %w", err)
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return operation()
}

func addIgnore(root string) error {
	path := filepath.Join(root, ".gitignore")
	contents, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, line := range strings.Split(string(contents), "\n") {
		if strings.TrimSpace(line) == ".forgepilot/" {
			return nil
		}
	}
	if len(contents) > 0 && !bytes.HasSuffix(contents, []byte("\n")) {
		contents = append(contents, '\n')
	}
	contents = append(contents, []byte(".forgepilot/\n")...)
	return os.WriteFile(path, contents, 0644)
}

func statePath(directory string) string { return filepath.Join(directory, "state.json") }

func canonicalRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

func validateRepository(state work.State, root string) error {
	for _, goal := range state.Goals {
		if goal.Repository != root {
			return fmt.Errorf("state belongs to repository %q, not %q", goal.Repository, root)
		}
	}
	return nil
}

func isRepositoryRoot(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".git"))
	return err == nil
}
