package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
)

const stateDirectory = ".forgepilot"

var ErrControlInFlight = errors.New("an execution-control operation is already running in this repository")

// Read returns the empty v1 sidecar when no control file exists. This keeps
// repositories created before execution control readable without treating an
// absent pause as an inferred execution permission.
func Read(root string) (State, error) {
	root, err := canonicalRoot(root)
	if err != nil {
		return State{}, err
	}
	return read(controlPath(root))
}

// ReadLocked is for callers already inside WithLock. It does not acquire a
// second lock and performs the same strict, fail-closed decode as Read.
func ReadLocked(root string) (State, error) {
	root, err := canonicalRoot(root)
	if err != nil {
		return State{}, err
	}
	return read(controlPath(root))
}

// ReadExactV2Locked is the engine-compatibility read. Legacy sidecars remain
// readable for ordinary commands, but their in-memory upgrade cannot prove
// that a candidate understands the durable v2 pause and ownership facts.
func ReadExactV2Locked(root string) (State, error) {
	root, err := canonicalRoot(root)
	if err != nil {
		return State{}, err
	}
	path := controlPath(root)
	info, err := os.Lstat(path)
	if err != nil {
		return State{}, err
	}
	if !info.Mode().IsRegular() {
		return State{}, errors.New("engine compatibility requires a regular v2 execution-control sidecar")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	if err := checkUniqueControlJSON(json.NewDecoder(bytes.NewReader(body)), 0); err != nil {
		return State{}, fmt.Errorf("ambiguous execution-control sidecar: %w", err)
	}
	var header struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(body, &header); err != nil || header.SchemaVersion != SchemaVersion {
		return State{}, errors.New("engine compatibility requires a durable v2 execution-control sidecar")
	}
	return read(path)
}

func checkUniqueControlJSON(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("JSON nesting exceeds compatibility limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delim != '{' && delim != '[' {
		return errors.New("invalid JSON container")
	}
	seen := make(map[string]bool)
	for decoder.More() {
		if delim == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return errors.New("duplicate or invalid JSON object key")
			}
			seen[name] = true
		}
		if err := checkUniqueControlJSON(decoder, depth+1); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

// Update serializes a read-modify-write transaction with the distinct
// execution-control lock.
func Update(root string, operation func(*State) error) error {
	return WithLock(root, func() error { return UpdateLocked(root, operation) })
}

// UpdateLocked performs a transaction while the caller holds WithLock. It is
// intentionally separate so one control decision can encompass several reads
// and one final atomic write without lock re-entry.
func UpdateLocked(root string, operation func(*State) error) error {
	if operation == nil {
		return errors.New("nil execution-control update")
	}
	root, err := canonicalRoot(root)
	if err != nil {
		return err
	}
	before, err := read(controlPath(root))
	if err != nil {
		return err
	}
	next := before
	if err := operation(&next); err != nil {
		return err
	}
	if err := validateTransition(before, next); err != nil {
		return err
	}
	if reflect.DeepEqual(before, next) {
		return nil
	}
	next.Revision = before.Revision + 1
	if next.Pause != nil {
		// Revision names the enclosing sidecar commit, not only the commit that
		// first created the pause. Appending a wait or declaration while paused
		// must keep the pause internally consistent with the new state revision.
		next.Pause.Revision = next.Revision
	}
	if err := next.Validate(); err != nil {
		return fmt.Errorf("invalid execution control: %w", err)
	}
	return write(root, next)
}

// WithLock holds the repository-local execution-control flock. It is distinct
// from state, runner, and verification locks because pause intent must become
// durable before process cancellation and must not deadlock lifecycle writes.
func WithLock(root string, operation func() error) error {
	if operation == nil {
		return errors.New("nil execution-control operation")
	}
	root, err := canonicalRoot(root)
	if err != nil {
		return err
	}
	locks, err := controlLocksDirectory(root)
	if err != nil {
		return err
	}
	file, err := openRegularLock(filepath.Join(locks, "execution-control.lock"))
	if err != nil {
		return err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock execution control: %w", err)
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return operation()
}

func read(path string) (State, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewState(), nil
	}
	if err != nil {
		return State{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return State{}, err
	}
	if !info.Mode().IsRegular() {
		return State{}, fmt.Errorf("execution-control sidecar is not a regular file")
	}
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var state State
	if err := decoder.Decode(&state); err != nil {
		return State{}, fmt.Errorf("read execution control: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return State{}, errors.New("execution-control sidecar contains extra JSON values")
	}
	if err := state.Validate(); err == nil {
		return state, nil
	} else if state.SchemaVersion != 1 {
		return State{}, fmt.Errorf("invalid execution control: %w", err)
	}
	if state.Pause != nil && state.Pause.EngineRevision != nil {
		return State{}, errors.New("v1 execution-control sidecar cannot carry engine revision intent")
	}
	// v1 had no engine-revision intent. Upgrade only in memory: reads must not
	// silently rewrite the sidecar, and the next material Update writes v2 only
	// after the legacy facts validate under the new schema.
	state.SchemaVersion = SchemaVersion
	if err := state.Validate(); err != nil {
		return State{}, fmt.Errorf("invalid execution control: %w", err)
	}
	return state, nil
}

func write(root string, state State) error {
	directory, err := controlDirectory(root)
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	temporary, err := os.CreateTemp(directory, ".execution-control-*.tmp")
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
	if err := os.Rename(temporaryName, controlPath(root)); err != nil {
		return err
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer directoryHandle.Close()
	return directoryHandle.Sync()
}

func validateTransition(before, next State) error {
	if next.SchemaVersion != before.SchemaVersion {
		return errors.New("execution-control schema version is immutable")
	}
	if next.Revision != before.Revision {
		return errors.New("execution-control revision is managed by Update")
	}
	if len(next.ExternalDeclarations) < len(before.ExternalDeclarations) {
		return errors.New("external declarations are append-only")
	}
	for index := range before.ExternalDeclarations {
		if !reflect.DeepEqual(before.ExternalDeclarations[index], next.ExternalDeclarations[index]) {
			return errors.New("external declarations are append-only")
		}
	}
	if len(next.Waits) < len(before.Waits) {
		return errors.New("waits are append-only")
	}
	for index := range before.Waits {
		if !reflect.DeepEqual(before.Waits[index], next.Waits[index]) {
			return errors.New("waits are append-only")
		}
	}
	return nil
}

func canonicalRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

func controlPath(root string) string {
	return filepath.Join(root, stateDirectory, "execution-control.json")
}

func controlDirectory(root string) (string, error) {
	return safeDirectory(filepath.Join(root, stateDirectory))
}

func controlLocksDirectory(root string) (string, error) {
	state, err := controlDirectory(root)
	if err != nil {
		return "", err
	}
	return safeDirectory(filepath.Join(state, "locks"))
}

func safeDirectory(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("unsafe execution-control directory %q", path)
	}
	return path, nil
}

func openRegularLock(path string) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return nil, fmt.Errorf("unsafe execution-control lock %q", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, fmt.Errorf("unsafe execution-control lock %q", path)
	}
	return file, nil
}
