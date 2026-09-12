package repository

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// CanonicalCommand is the only verification ForgePilot runs. The managed project
// owns what it means; ForgePilot never accepts an arbitrary command template.
const CanonicalCommand = "make verify"

// Snapshot is an immutable view of a repository's current working contents.
// BaseRevision identifies the committed history it was taken from. Revision and
// Ref are populated only by CaptureSnapshot, whose snapshot commit is retained
// under a local ForgePilot ref.
type Snapshot struct {
	BaseRevision string
	Revision     string
	Digest       string
	Ref          string
}

// CaptureSnapshot records the current working contents in a commit parented by
// HEAD. It uses a private index, leaving the user's staged state untouched.
func CaptureSnapshot(root, workID string, createdAt time.Time) (Snapshot, error) {
	base, tree, digest, environment, cleanup, err := prepareSnapshot(root, false)
	if err != nil {
		return Snapshot{}, err
	}
	defer cleanup()

	commitEnvironment := append(environment,
		"GIT_AUTHOR_NAME=ForgePilot", "GIT_AUTHOR_EMAIL=forgepilot@local",
		"GIT_COMMITTER_NAME=ForgePilot", "GIT_COMMITTER_EMAIL=forgepilot@local",
		"GIT_AUTHOR_DATE="+createdAt.UTC().Format(time.RFC3339Nano),
		"GIT_COMMITTER_DATE="+createdAt.UTC().Format(time.RFC3339Nano),
	)
	current, err := Head(root)
	if err != nil {
		return Snapshot{}, err
	}
	if current != base {
		return Snapshot{}, fmt.Errorf("HEAD moved while capturing snapshot: started at %s, now at %s", base, current)
	}
	output, err := git(root, commitEnvironment, "commit-tree", tree, "-p", base)
	if err != nil {
		return Snapshot{}, fmt.Errorf("create snapshot commit: %w", err)
	}
	revision := strings.TrimSpace(output)
	ref := snapshotRef(workID, createdAt, revision)
	if err := retainSnapshot(root, ref, revision); err != nil {
		return Snapshot{}, fmt.Errorf("retain snapshot: %w", err)
	}
	return Snapshot{BaseRevision: base, Revision: revision, Digest: digest, Ref: ref}, nil
}

// InspectSnapshot computes the same content digest CaptureSnapshot would use,
// but keeps every object it creates in a temporary object database and creates
// no ref. It therefore leaves both Git's visible state and object store alone.
func InspectSnapshot(root string) (Snapshot, error) {
	base, _, digest, _, cleanup, err := prepareSnapshot(root, true)
	if err != nil {
		return Snapshot{}, err
	}
	defer cleanup()
	return Snapshot{BaseRevision: base, Digest: digest}, nil
}

func prepareSnapshot(root string, isolateObjects bool) (string, string, string, []string, func(), error) {
	base, err := Head(root)
	if err != nil {
		return "", "", "", nil, nil, err
	}
	directory, err := os.MkdirTemp("", "forgepilot-snapshot-")
	if err != nil {
		return "", "", "", nil, nil, fmt.Errorf("create snapshot workspace: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	environment := []string{"GIT_INDEX_FILE=" + filepath.Join(directory, "index")}
	if isolateObjects {
		gitDir, err := git(root, nil, "rev-parse", "--absolute-git-dir")
		if err != nil {
			cleanup()
			return "", "", "", nil, nil, err
		}
		objectDirectory := filepath.Join(directory, "objects")
		if err := os.Mkdir(objectDirectory, 0700); err != nil {
			cleanup()
			return "", "", "", nil, nil, fmt.Errorf("create temporary object directory: %w", err)
		}
		environment = append(environment,
			"GIT_OBJECT_DIRECTORY="+objectDirectory,
			"GIT_ALTERNATE_OBJECT_DIRECTORIES="+filepath.Join(strings.TrimSpace(gitDir), "objects"),
		)
	}
	if _, err := git(root, environment, "read-tree", base); err != nil {
		cleanup()
		return "", "", "", nil, nil, fmt.Errorf("seed snapshot index: %w", err)
	}
	if _, err := git(root, environment, "add", "-A", "--", "."); err != nil {
		cleanup()
		return "", "", "", nil, nil, fmt.Errorf("stage snapshot contents: %w", err)
	}
	output, err := git(root, environment, "write-tree")
	if err != nil {
		cleanup()
		return "", "", "", nil, nil, fmt.Errorf("write snapshot tree: %w", err)
	}
	tree := strings.TrimSpace(output)
	digest, err := snapshotDigest(root, environment, base, tree)
	if err != nil {
		cleanup()
		return "", "", "", nil, nil, err
	}
	return base, tree, digest, environment, cleanup, nil
}

func snapshotRef(workID string, createdAt time.Time, revision string) string {
	return "refs/forgepilot/snapshots/" + workID + "/" + createdAt.UTC().Format("20060102T150405.000000000Z") + "-" + revision
}

func retainSnapshot(root, ref, revision string) error {
	if _, err := git(root, nil, "update-ref", ref, revision, ""); err == nil {
		return nil
	} else {
		output, resolveErr := git(root, nil, "rev-parse", "--verify", ref)
		if resolveErr == nil && strings.TrimSpace(output) == revision {
			return nil
		}
		return fmt.Errorf("create snapshot ref %s: %w", ref, err)
	}
}

type snapshotEntry struct {
	mode, kind, object, path string
}

func snapshotDigest(root string, environment []string, base, tree string) (string, error) {
	output, err := git(root, environment, "ls-tree", "-r", "-z", tree)
	if err != nil {
		return "", fmt.Errorf("list snapshot tree: %w", err)
	}
	var entries []snapshotEntry
	for _, record := range bytes.Split([]byte(output), []byte{0}) {
		if len(record) == 0 {
			continue
		}
		header, path, ok := bytes.Cut(record, []byte{'\t'})
		if !ok {
			return "", errors.New("read malformed snapshot tree entry")
		}
		fields := bytes.Fields(header)
		if len(fields) != 3 {
			return "", errors.New("read malformed snapshot tree entry")
		}
		entries = append(entries, snapshotEntry{string(fields[0]), string(fields[1]), string(fields[2]), string(path)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	hash := sha256.New()
	writeSnapshotDigestField(hash, "forgepilot-snapshot-digest-v1")
	writeSnapshotDigestField(hash, base)
	for _, entry := range entries {
		for _, field := range []string{entry.path, entry.mode, entry.kind, entry.object} {
			writeSnapshotDigestField(hash, field)
		}
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil)), nil
}

func writeSnapshotDigestField(hash io.Writer, field string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(field)))
	_, _ = hash.Write(length[:])
	_, _ = hash.Write([]byte(field))
}

// EnsureClean rejects a worktree whose contents are not fully described by HEAD.
// Untracked files count as dirty: a new but uncommitted implementation file is
// exactly the content a verification must not silently skip. Ignored files do not.
//
// action names what the caller is doing right now ("verifying", "reviewing")
// so the rejection reads as the command the user actually ran. It plays no
// part in the judgement itself: the check above is the only thing either
// caller may lean on.
func EnsureClean(root, action string) error {
	output, err := git(root, nil, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return err
	}
	if strings.TrimSpace(output) != "" {
		return fmt.Errorf("worktree is not clean; commit or stash the following before %s:\n%s", action, strings.TrimRight(output, "\n"))
	}
	return nil
}

// Uncommitted reports whether the given repository-relative path holds content
// that HEAD does not describe: untracked, or tracked with changes not yet
// committed. Unlike EnsureClean, which asks about the whole worktree, this asks
// about exactly one path, so a dirty file elsewhere in the worktree cannot make
// this report a false positive for a path that is itself clean.
func Uncommitted(root, path string) (bool, error) {
	output, err := git(root, nil, "status", "--porcelain", "--untracked-files=normal", "--", path)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) != "", nil
}

// Head resolves the full commit SHA that a Verification Run will be bound to.
func Head(root string) (string, error) {
	output, err := git(root, nil, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve HEAD: %w", err)
	}
	revision := strings.TrimSpace(output)
	if revision == "" {
		return "", errors.New("repository has no commits to verify")
	}
	return revision, nil
}

// EnsureCanonicalCheck reports whether a checkout defines the canonical check at
// all. A revision without one cannot be verified, which is not the same as
// failing verification, so this is refused rather than recorded as Evidence.
//
// It must be given the isolated checkout, never the user's worktree: a Makefile
// that is present but gitignored would make the main worktree look verifiable
// while the committed revision has no canonical check at all.
func EnsureCanonicalCheck(checkout string) error {
	return EnsureCanonicalCheckWithRuntime(checkout, RuntimeEnvironment{})
}

// EnsureCanonicalCheckWithRuntime inspects the same checkout with the same
// resolved environment that the guarded verification transaction will use.
func EnsureCanonicalCheckWithRuntime(checkout string, runtime RuntimeEnvironment) error {
	found := false
	for _, name := range []string{"GNUmakefile", "makefile", "Makefile"} {
		if _, err := os.Stat(filepath.Join(checkout, name)); err == nil {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("this revision has no makefile, so `%s` cannot be run", CanonicalCommand)
	}
	command := exec.Command("make", "-n", "verify")
	command.Dir = checkout
	command.Env = mergedEnvironment(runtime.environment)
	output, err := command.CombinedOutput()
	if err == nil {
		return nil
	}
	if strings.Contains(string(output), "No rule to make target") {
		return fmt.Errorf("this revision does not define `%s`", CanonicalCommand)
	}
	return fmt.Errorf("`%s` cannot be run against this revision: %s", CanonicalCommand, strings.TrimSpace(string(output)))
}

// PruneWorktrees clears registrations left behind by runs that were killed. Git
// keeps the metadata until asked to prune, so this runs before every new run.
func PruneWorktrees(root string) error {
	_, err := git(root, nil, "worktree", "prune")
	return err
}

// AddWorktree checks the exact revision out in isolation. Always detached and by
// full SHA: naming a branch fails because the main worktree already holds it.
func AddWorktree(root, path, revision string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	// Clear both halves of a leftover run before pruning: git keeps a
	// registration whose directory still exists, and removing the directory first
	// without pruning turns it into a "missing but already registered" failure.
	_, _ = git(root, nil, "worktree", "remove", "--force", path)
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	if _, err := git(root, nil, "worktree", "prune"); err != nil {
		return err
	}
	if _, err := git(root, nil, "worktree", "add", "--detach", path, revision); err != nil {
		return fmt.Errorf("create isolated worktree: %w", err)
	}
	return nil
}

// RemoveWorktree always forces: a verification run leaves build output behind,
// and git refuses to remove a worktree that has untracked files.
func RemoveWorktree(root, path string) error {
	if _, err := git(root, nil, "worktree", "remove", "--force", path); err != nil {
		return os.RemoveAll(path)
	}
	return nil
}

// OpenLog creates the file a Verification Run's output will stream into,
// creating its directory if needed. It is called before anything about the run
// is recorded, so a log that cannot be created aborts the command before any
// state is written or Evidence appended.
func OpenLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("create log file: %w", err)
	}
	return file, nil
}

// RunCanonicalCheck executes the managed project's canonical check, streaming
// its combined output to log as it runs, and reports its exit code. A non-zero
// code is a verification result, not an error here; err is reserved for being
// unable to run the check at all.
//
// Streaming rather than collecting the output and writing it once means an
// interrupted run still leaves behind whatever it produced before it was
// killed, and a long run is not silent for its whole duration.
func RunCanonicalCheck(directory string, log io.Writer) (int, error) {
	return RunCanonicalCheckWithRuntime(directory, RuntimeEnvironment{}, log)
}

// RunCanonicalCheckWithRuntime executes the canonical check in the runtime
// environment already resolved and validated for this candidate checkout.
func RunCanonicalCheckWithRuntime(directory string, runtime RuntimeEnvironment, log io.Writer) (int, error) {
	command := exec.Command("make", "verify")
	command.Dir = directory
	command.Env = mergedEnvironment(runtime.environment)
	command.Stdout = log
	command.Stderr = log
	err := command.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return 0, fmt.Errorf("run `%s`: %w", CanonicalCommand, err)
	}
	return 0, nil
}

func mergedEnvironment(overrides []string) []string {
	if len(overrides) == 0 {
		return nil
	}
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	keys := make(map[string]struct{}, len(overrides))
	for _, entry := range overrides {
		key, _, _ := strings.Cut(entry, "=")
		keys[key] = struct{}{}
	}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, overridden := keys[key]; !overridden {
			environment = append(environment, entry)
		}
	}
	return append(environment, overrides...)
}

func git(root string, environment []string, arguments ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	command.Env = mergedEnvironment(environment)
	output, err := command.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("git %s: %w: %s", strings.Join(arguments, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}
