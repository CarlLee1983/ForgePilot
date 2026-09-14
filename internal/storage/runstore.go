package storage

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// runDirectory holds Runner execution history. It sits under the existing
// .forgepilot/ directory, so it is already inside the ignore entry `init`
// writes and therefore cannot change a Candidate digest.
const runDirectory = "runs"

// ErrRunnerInFlight reports that another live Runner already owns this
// workspace.
var ErrRunnerInFlight = errors.New("another runner already owns this workspace")

// ErrCapacityExceeded reports that writing a Runner artifact would exceed a
// configured limit. It is a stop condition, never something to make room for by
// deleting: the artifacts a Goal's final review needs must survive.
var ErrCapacityExceeded = errors.New("runner artifact capacity exceeded")

// ArtifactLimits bounds what one Runner may write. Every limit is finite and
// every limit is injectable, so a test can reach the boundary in milliseconds
// instead of filling a disk.
type ArtifactLimits struct {
	MaxWriteBytes int64 `json:"max_write_bytes"`
	MaxRunBytes   int64 `json:"max_run_bytes"`
	MaxTotalBytes int64 `json:"max_total_bytes"`
}

// Validate refuses a limit that cancels a bound. Zero reads as "unchecked"
// everywhere below, so accepting it from a flag would let a caller switch off
// the ceilings a long unattended run depends on.
func (limits ArtifactLimits) Validate() error {
	for name, value := range map[string]int64{
		"--max-agent-output-bytes": limits.MaxWriteBytes,
		"--max-run-bytes":          limits.MaxRunBytes,
		"--max-runs-bytes":         limits.MaxTotalBytes,
	} {
		if value <= 0 {
			return fmt.Errorf("%s must be positive; this version does not accept 0 as unlimited", name)
		}
	}
	if limits.MaxWriteBytes > limits.MaxRunBytes || limits.MaxRunBytes > limits.MaxTotalBytes {
		return fmt.Errorf("artifact limits must widen outwards: one write (%d) ≤ one run (%d) ≤ all runs (%d)",
			limits.MaxWriteBytes, limits.MaxRunBytes, limits.MaxTotalBytes)
	}
	return nil
}

// WithWorkspaceLock runs an operation while holding this workspace's Runner
// lock. The path is canonicalised first, so a symlinked alias of the same
// repository is the same workspace and cannot start a second Runner.
//
// It coordinates Runners and nothing else. It is not a Git workspace lock, it
// does not stop an editor or another agent from writing files, and it is
// deliberately not the state transaction lock: a model or a canonical check may
// run for many minutes, and `forgepilot status` must stay answerable
// throughout.
func WithWorkspaceLock(root string, operation func() error) error {
	root, err := canonicalRoot(root)
	if err != nil {
		return err
	}
	directory := filepath.Join(root, stateDirectory, "locks")
	if err := os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(directory, "runner.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("%w: %s", ErrRunnerInFlight, root)
		}
		return fmt.Errorf("lock workspace: %w", err)
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return operation()
}

// RunnerRunning reports whether a live process owns this workspace's Runner
// lock. It never creates the lock file, so queries stay side-effect free.
func RunnerRunning(root string) bool {
	root, err := canonicalRoot(root)
	if err != nil {
		return false
	}
	file, err := os.Open(filepath.Join(root, stateDirectory, "locks", "runner.lock"))
	if err != nil {
		return false
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK)
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return false
}

// StateDigest fingerprints the ForgePilot state file. A Runner takes it before
// and after an agent session: the coding CLI is given write access to the
// repository, and the repository contains `.forgepilot/`. The prohibition in
// the briefing is a request made to an untrusted model, not a mechanism, so
// this is the mechanism. A change here means the session wrote governance state
// it was told not to touch, and nothing it produced can be trusted afterwards.
func StateDigest(root string) (string, error) {
	root, err := canonicalRoot(root)
	if err != nil {
		return "", err
	}
	contents, err := os.ReadFile(statePath(filepath.Join(root, stateDirectory)))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(contents)), nil
}

// RunDirectory names where one run's artifacts live.
func RunDirectory(root, runID string) (string, error) {
	root, err := canonicalRoot(root)
	if err != nil {
		return "", err
	}
	if err := validRunID(runID); err != nil {
		return "", err
	}
	return filepath.Join(root, stateDirectory, runDirectory, runID), nil
}

// WriteRunArtifact replaces one artifact atomically: a reader sees either the
// previous contents or the complete new ones. A run record written by halves is
// worse than none, because recovery would trust it.
func WriteRunArtifact(root, runID, name string, contents []byte, limits ArtifactLimits) error {
	directory, path, err := artifactPath(root, runID, name)
	if err != nil {
		return err
	}
	if err := checkCapacity(root, runID, directory, path, int64(len(contents)), limits, false); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return writeFileAtomically(filepath.Dir(path), path, contents)
}

// AppendRunArtifact adds one record to an artifact's tail. The capacity check
// happens before the write, so a limit is reached by refusing rather than by
// leaving a half-written trail behind.
func AppendRunArtifact(root, runID, name string, contents []byte, limits ArtifactLimits) error {
	directory, path, err := artifactPath(root, runID, name)
	if err != nil {
		return err
	}
	if err := checkCapacity(root, runID, directory, path, int64(len(contents)), limits, true); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(contents); err != nil {
		return err
	}
	return file.Sync()
}

// ReadRunArtifact reads one artifact back.
func ReadRunArtifact(root, runID, name string) ([]byte, error) {
	_, path, err := artifactPath(root, runID, name)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// ListRuns names the runs recorded in this workspace, most recent identifier
// last. Nothing here deletes: a Goal's final review may need any of them.
func ListRuns(root string) ([]string, error) {
	root, err := canonicalRoot(root)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(root, stateDirectory, runDirectory))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var runs []string
	for _, entry := range entries {
		if entry.IsDir() && validRunID(entry.Name()) == nil {
			runs = append(runs, entry.Name())
		}
	}
	sort.Strings(runs)
	return runs, nil
}

// CheckRunCapacity reports whether a run may write another `size` bytes. The
// Runner calls it before launching a session, because a session writes its own
// files directly — a briefing and a console log — and a limit only checked on
// the way out would be discovered after the disk was already full.
func CheckRunCapacity(root, runID string, size int64, limits ArtifactLimits) error {
	directory, err := RunDirectory(root, runID)
	if err != nil {
		return err
	}
	// The per-write bound is deliberately not applied here: this is a
	// reservation for several files a session is about to write, not one write.
	reservation := limits
	reservation.MaxWriteBytes = 0
	return checkCapacity(root, runID, directory, "", size, reservation, true)
}

func artifactPath(root, runID, name string) (string, string, error) {
	directory, err := RunDirectory(root, runID)
	if err != nil {
		return "", "", err
	}
	if err := validArtifactName(name); err != nil {
		return "", "", err
	}
	return directory, filepath.Join(directory, filepath.FromSlash(name)), nil
}

// checkCapacity refuses before writing. All three bounds are checked because
// they fail differently: one runaway session, one runaway run, and a workspace
// slowly filling across many runs.
func checkCapacity(root, runID, directory, path string, size int64, limits ArtifactLimits, appending bool) error {
	if limits.MaxWriteBytes > 0 && size > limits.MaxWriteBytes {
		return fmt.Errorf("%w: a single artifact write of %d bytes exceeds the %d byte limit; nothing is deleted automatically — raise the limit or start a new workspace",
			ErrCapacityExceeded, size, limits.MaxWriteBytes)
	}
	existing := fileSize(path)
	if limits.MaxRunBytes > 0 {
		used, err := directorySize(directory)
		if err != nil {
			return err
		}
		projected := used + size
		if !appending {
			projected -= existing
		}
		if projected > limits.MaxRunBytes {
			return fmt.Errorf("%w: run %s would hold %d bytes, over the %d byte limit; free space under %s or start a new workspace",
				ErrCapacityExceeded, runID, projected, limits.MaxRunBytes, directory)
		}
	}
	if limits.MaxTotalBytes > 0 {
		root, err := canonicalRoot(root)
		if err != nil {
			return err
		}
		runsRoot := filepath.Join(root, stateDirectory, runDirectory)
		used, err := directorySize(runsRoot)
		if err != nil {
			return err
		}
		projected := used + size
		if !appending {
			projected -= existing
		}
		if projected > limits.MaxTotalBytes {
			return fmt.Errorf("%w: runner artifacts would hold %d bytes, over the %d byte limit; nothing is deleted automatically — review and clear %s yourself",
				ErrCapacityExceeded, projected, limits.MaxTotalBytes, runsRoot)
		}
	}
	return nil
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func directorySize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		total += info.Size()
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	return total, err
}

func validRunID(runID string) error {
	if runID == "" || runID != filepath.Base(runID) || runID == "." || runID == ".." || strings.HasPrefix(runID, ".") {
		return fmt.Errorf("invalid run ID %q", runID)
	}
	for _, character := range runID {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' {
			continue
		}
		return fmt.Errorf("invalid run ID %q", runID)
	}
	return nil
}

// validArtifactName allows one level of nesting so a session's files can sit
// together, and nothing that could escape the run directory.
func validArtifactName(name string) error {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
		return fmt.Errorf("invalid artifact name %q", name)
	}
	parts := strings.Split(name, "/")
	if len(parts) > 2 {
		return fmt.Errorf("invalid artifact name %q", name)
	}
	for _, part := range parts {
		if part == "" || part == "." || strings.HasPrefix(part, ".") {
			return fmt.Errorf("invalid artifact name %q", name)
		}
	}
	return nil
}
