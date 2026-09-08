package repository

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CanonicalCommand is the only verification ForgePilot runs. The managed project
// owns what it means; ForgePilot never accepts an arbitrary command template.
const CanonicalCommand = "make verify"

// EnsureClean rejects a worktree whose contents are not fully described by HEAD.
// Untracked files count as dirty: a new but uncommitted implementation file is
// exactly the content a verification must not silently skip. Ignored files do not.
//
// action names what the caller is doing right now ("verifying", "reviewing")
// so the rejection reads as the command the user actually ran. It plays no
// part in the judgement itself: the check above is the only thing either
// caller may lean on.
func EnsureClean(root, action string) error {
	output, err := git(root, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return err
	}
	if strings.TrimSpace(output) != "" {
		return fmt.Errorf("worktree is not clean; commit or stash the following before %s:\n%s", action, strings.TrimRight(output, "\n"))
	}
	return nil
}

// Head resolves the full commit SHA that a Verification Run will be bound to.
func Head(root string) (string, error) {
	output, err := git(root, "rev-parse", "HEAD")
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
	_, err := git(root, "worktree", "prune")
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
	_, _ = git(root, "worktree", "remove", "--force", path)
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	if _, err := git(root, "worktree", "prune"); err != nil {
		return err
	}
	if _, err := git(root, "worktree", "add", "--detach", path, revision); err != nil {
		return fmt.Errorf("create isolated worktree: %w", err)
	}
	return nil
}

// RemoveWorktree always forces: a verification run leaves build output behind,
// and git refuses to remove a worktree that has untracked files.
func RemoveWorktree(root, path string) error {
	if _, err := git(root, "worktree", "remove", "--force", path); err != nil {
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
	command := exec.Command("make", "verify")
	command.Dir = directory
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

func git(root string, arguments ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("git %s: %w: %s", strings.Join(arguments, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}
