package repository

import (
	"bytes"
	"context"
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

	"github.com/CarlLee1983/ForgePilot/internal/process"
)

// CanonicalCommand is the only verification ForgePilot runs. The managed project
// owns what it means; ForgePilot never accepts an arbitrary command template.
const CanonicalCommand = "make verify"

// ErrVerificationLogCollision reports that a proposed log path already
// belongs to another attempt. Callers must choose a new attempt suffix rather
// than overwrite it.
var ErrVerificationLogCollision = errors.New("verification log path already exists")

// ErrVerificationLogNotFound reports that no durable log belongs to a
// Verification Run ID.
var ErrVerificationLogNotFound = errors.New("verification log not found")

// ErrVerificationLogAmbiguous reports that more than one durable log claims a
// Verification Run ID. Recovery must fail closed rather than guess.
var ErrVerificationLogAmbiguous = errors.New("verification log lookup is ambiguous")

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
func CaptureSnapshot(ctx context.Context, root, workID string, createdAt time.Time) (Snapshot, error) {
	base, tree, digest, environment, cleanup, err := prepareSnapshot(ctx, root, false)
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
	current, err := Head(ctx, root)
	if err != nil {
		return Snapshot{}, err
	}
	if current != base {
		return Snapshot{}, fmt.Errorf("HEAD moved while capturing snapshot: started at %s, now at %s", base, current)
	}
	output, err := git(ctx, root, commitEnvironment, "commit-tree", tree, "-p", base)
	if err != nil {
		return Snapshot{}, fmt.Errorf("create snapshot commit: %w", err)
	}
	revision := strings.TrimSpace(output)
	ref := snapshotRef(workID, createdAt, revision)
	if err := retainSnapshot(ctx, root, ref, revision); err != nil {
		return Snapshot{}, fmt.Errorf("retain snapshot: %w", err)
	}
	return Snapshot{BaseRevision: base, Revision: revision, Digest: digest, Ref: ref}, nil
}

// InspectSnapshot computes the same content digest CaptureSnapshot would use,
// but keeps every object it creates in a temporary object database and creates
// no ref. It therefore leaves both Git's visible state and object store alone.
func InspectSnapshot(ctx context.Context, root string) (Snapshot, error) {
	base, _, digest, _, cleanup, err := prepareSnapshot(ctx, root, true)
	if err != nil {
		return Snapshot{}, err
	}
	defer cleanup()
	return Snapshot{BaseRevision: base, Digest: digest}, nil
}

func prepareSnapshot(ctx context.Context, root string, isolateObjects bool) (string, string, string, []string, func(), error) {
	base, err := Head(ctx, root)
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
		gitDir, err := git(ctx, root, nil, "rev-parse", "--absolute-git-dir")
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
	if _, err := git(ctx, root, environment, "read-tree", base); err != nil {
		cleanup()
		return "", "", "", nil, nil, fmt.Errorf("seed snapshot index: %w", err)
	}
	if _, err := git(ctx, root, environment, "add", "-A", "--", "."); err != nil {
		cleanup()
		return "", "", "", nil, nil, fmt.Errorf("stage snapshot contents: %w", err)
	}
	output, err := git(ctx, root, environment, "write-tree")
	if err != nil {
		cleanup()
		return "", "", "", nil, nil, fmt.Errorf("write snapshot tree: %w", err)
	}
	tree := strings.TrimSpace(output)
	digest, err := snapshotDigest(ctx, root, environment, base, tree)
	if err != nil {
		cleanup()
		return "", "", "", nil, nil, err
	}
	return base, tree, digest, environment, cleanup, nil
}

func snapshotRef(workID string, createdAt time.Time, revision string) string {
	return "refs/forgepilot/snapshots/" + workID + "/" + createdAt.UTC().Format("20060102T150405.000000000Z") + "-" + revision
}

func retainSnapshot(ctx context.Context, root, ref, revision string) error {
	if _, err := git(ctx, root, nil, "update-ref", ref, revision, ""); err == nil {
		return nil
	} else {
		output, resolveErr := git(ctx, root, nil, "rev-parse", "--verify", ref)
		if resolveErr == nil && strings.TrimSpace(output) == revision {
			return nil
		}
		return fmt.Errorf("create snapshot ref %s: %w", ref, err)
	}
}

type snapshotEntry struct {
	mode, kind, object, path string
}

func snapshotDigest(ctx context.Context, root string, environment []string, base, tree string) (string, error) {
	output, err := git(ctx, root, environment, "ls-tree", "-r", "-z", tree)
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
func EnsureClean(ctx context.Context, root, action string) error {
	output, err := git(ctx, root, nil, "status", "--porcelain", "--untracked-files=normal")
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
func Uncommitted(ctx context.Context, root, path string) (bool, error) {
	output, err := git(ctx, root, nil, "status", "--porcelain", "--untracked-files=normal", "--", path)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) != "", nil
}

// Head resolves the full commit SHA that a Verification Run will be bound to.
func Head(ctx context.Context, root string) (string, error) {
	output, err := git(ctx, root, nil, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve HEAD: %w", err)
	}
	revision := strings.TrimSpace(output)
	if revision == "" {
		return "", errors.New("repository has no commits to verify")
	}
	return revision, nil
}

// EnsureCanonicalCheckInContext reports whether a checkout defines the canonical
// check at all. A revision without one cannot be verified, which is not the same
// as failing verification, so this is refused rather than recorded as Evidence.
//
// It must be given the isolated checkout, never the user's worktree: a Makefile
// that is present but gitignored would make the main worktree look verifiable
// while the committed revision has no canonical check at all.
//
// `make -n verify` is a real external process — it reads the makefile the
// project wrote and can do whatever that makefile does — so it is bounded by the
// same stop signal and deadline as the check it precedes. A preflight that
// cannot be interrupted is a blind spot in the middle of a cancellation path,
// not a cheap probe.
func EnsureCanonicalCheckInContext(ctx context.Context, checkout string, runtime RuntimeEnvironment) error {
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
	var collected strings.Builder
	run, err := process.Start(ctx, command, &collected)
	if err != nil {
		return err
	}
	if !run.Completed {
		return errors.Join(ctx.Err(), run.Cleanup)
	}
	if run.Cleanup != nil {
		return run.Cleanup
	}
	if run.ExitCode == 0 {
		return nil
	}
	output := collected.String()
	if strings.Contains(output, "No rule to make target") {
		return fmt.Errorf("this revision does not define `%s`", CanonicalCommand)
	}
	return fmt.Errorf("`%s` cannot be run against this revision: %s", CanonicalCommand, strings.TrimSpace(output))
}

// PruneWorktrees clears registrations left behind by runs that were killed. Git
// keeps the metadata until asked to prune, so this runs before every new run.
func PruneWorktrees(ctx context.Context, root string) error {
	_, err := git(ctx, root, nil, "worktree", "prune")
	return err
}

// AddWorktree checks the exact revision out in isolation. Always detached and by
// full SHA: naming a branch fails because the main worktree already holds it.
func AddWorktree(ctx context.Context, root, path, revision string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	// Clear both halves of a leftover run before pruning: git keeps a
	// registration whose directory still exists, and removing the directory first
	// without pruning turns it into a "missing but already registered" failure.
	// An ordinary failure here is expected and ignored — there is usually nothing
	// to remove — but a removal whose own children could not be confirmed gone is
	// not an ordinary failure, and deleting the directory anyway would take it
	// out from under them.
	if _, err := git(ctx, root, nil, "worktree", "remove", "--force", path); unconfirmedGroup(err) {
		return err
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	if _, err := git(ctx, root, nil, "worktree", "prune"); err != nil {
		return err
	}
	if _, err := git(ctx, root, nil, "worktree", "add", "--detach", path, revision); err != nil {
		return fmt.Errorf("create isolated worktree: %w", err)
	}
	return nil
}

// RemoveWorktree always forces: a verification run leaves build output behind,
// and git refuses to remove a worktree that has untracked files.
//
// The filesystem fallback is for the ordinary failure it was written for — a
// directory Git has no registration for — and only that. A removal whose own
// process group could not be confirmed gone is an execution-safety report, and
// os.RemoveAll would answer it with a success while deleting the checkout a
// recovery record points at, out from under whatever is still writing it.
// See docs/adr/0022-pending-cleanup-outlives-the-process.md.
func RemoveWorktree(ctx context.Context, root, path string) error {
	_, err := git(ctx, root, nil, "worktree", "remove", "--force", path)
	if err == nil {
		return nil
	}
	if unconfirmedGroup(err) {
		return err
	}
	return os.RemoveAll(path)
}

// unconfirmedGroup reports whether an error — which may be carrying several
// things at once, since a cancelled Git command whose child could not be
// confirmed gone reports both — says a process group was left unconfirmed.
func unconfirmedGroup(err error) bool {
	if err == nil {
		return false
	}
	_, unsettled := process.UnsettledGroup(err)
	return unsettled
}

// OpenExclusiveLog creates a Verification Run log without ever truncating an
// existing file. The caller owns selection of a unique attempt suffix; an
// existing path is reported as a typed collision so it can retry safely.
func OpenExclusiveLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%w: %w", ErrVerificationLogCollision, os.ErrExist)
		}
		return nil, fmt.Errorf("create exclusive log file: %w", err)
	}
	return file, nil
}

// FindVerificationLog returns the sole log whose filename begins with the
// complete Verification Run ID token. The hyphen boundary prevents VR-100 from
// matching VR-1000. It intentionally has no legacy Work Item/revision policy:
// callers must select that legacy lookup explicitly.
func FindVerificationLog(root, verificationRunID string) (string, error) {
	if verificationRunID == "" || verificationRunID != filepath.Base(verificationRunID) || verificationRunID == "." || verificationRunID == ".." {
		return "", fmt.Errorf("invalid verification run ID %q", verificationRunID)
	}
	directory := filepath.Join(root, ".forgepilot", "logs")
	entries, err := os.ReadDir(directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: %s", ErrVerificationLogNotFound, verificationRunID)
		}
		return "", fmt.Errorf("read verification logs: %w", err)
	}
	prefix := verificationRunID + "-"
	match := ""
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		if match != "" {
			return "", fmt.Errorf("%w: %s", ErrVerificationLogAmbiguous, verificationRunID)
		}
		match = entry.Name()
	}
	if match == "" {
		return "", fmt.Errorf("%w: %s", ErrVerificationLogNotFound, verificationRunID)
	}
	return filepath.Join(directory, match), nil
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

// git runs one Git command under the caller's context, through the same managed
// process path as every other external process ForgePilot starts. Before this it
// used exec.Command and CombinedOutput with no context at all, so a cancellation
// reached the Runner's `make verify` and its runtime probes but stopped dead at
// the repository boundary: a post-checkout hook or a clean/smudge filter that
// blocks held a Ctrl-C for as long as it liked, and the `ctx.Err()` checks
// placed before each call are start gates, which say nothing to a process that
// is already running.
//
// Git's own semantics are unchanged and are the reason this is not simply
// exec.CommandContext: stdout and stderr stay merged into the single string
// callers parse, the environment override still wins over the inherited one so
// GIT_INDEX_FILE keeps the user's real index out of it, and a non-zero exit is
// still an error carrying the output and the exit code rather than a result.
func git(ctx context.Context, root string, environment []string, arguments ...string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	command.Env = mergedEnvironment(environment)
	var collected strings.Builder
	run, err := process.Start(ctx, command, &collected)
	output := collected.String()
	if err != nil {
		return output, fmt.Errorf("git %s: %w", strings.Join(arguments, " "), errors.Join(err, run.Cleanup))
	}
	if !run.Completed {
		// Stopped rather than finished. The cleanup verdict travels with it: a Git
		// child that could not be confirmed gone is still holding this repository,
		// and losing that here is exactly how a worktree gets removed underneath a
		// process that is still writing it.
		return output, fmt.Errorf("git %s: %w", strings.Join(arguments, " "),
			errors.Join(context.Cause(ctx), run.Cleanup))
	}
	if run.Cleanup != nil {
		return output, fmt.Errorf("git %s: %w", strings.Join(arguments, " "), run.Cleanup)
	}
	if run.ExitCode != 0 {
		return output, fmt.Errorf("git %s: exit status %d: %s", strings.Join(arguments, " "), run.ExitCode, strings.TrimSpace(output))
	}
	return output, nil
}

// RunCanonicalCheckInContext executes the managed project's canonical check
// under a context, streaming its combined output to log as it runs, and reports
// how it ended. A non-zero exit code is a verification result, not an error
// here; err is reserved for being unable to run the check at all.
//
// Streaming rather than collecting the output and writing it once means an
// interrupted run still leaves behind whatever it produced before it was
// killed, and a long run is not silent for its whole duration. The child gets its own process group, the whole group
// is stopped when the context ends, and — the part that used to be missing —
// the group is settled on the ordinary path too: a check that exits 0 having
// forked a watcher has not stopped owning this worktree, and leaving that
// watcher running would hand it to the next step.
// See docs/adr/0020-worker-ownership-is-fail-closed.md.
func RunCanonicalCheckInContext(ctx context.Context, directory string, runtime RuntimeEnvironment, log io.Writer) (process.Run, error) {
	command := exec.Command("make", "verify")
	command.Dir = directory
	command.Env = mergedEnvironment(runtime.environment)
	run, err := process.Start(ctx, command, log)
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return run, fmt.Errorf("run `%s`: %w", CanonicalCommand, err)
	}
	return run, err
}
