package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/process"
)

// Removing a checkout is the one cleanup that can destroy the evidence a
// recovery needs. Both entry points used to answer a Git call that could not
// confirm its own children with `os.RemoveAll`, which succeeds — so an
// execution-safety report became a nil error and the directory it pointed at
// was deleted anyway.
//
// Git may already have finished deleting before it reports an unconfirmed
// child, and that is not pretended away: the promise is that ForgePilot adds no
// destructive step of its own and never reports success.
// See docs/adr/0022-pending-cleanup-outlives-the-process.md.

// unconfirmedWorktreeRemoval fails the confirmation of exactly the Git call
// that removes a worktree, leaving every other managed process alone.
func unconfirmedWorktreeRemoval() func() {
	return process.InjectCleanupFailure(func(pgid int, argv []string, _ time.Duration) error {
		if !strings.Contains(strings.Join(argv, " "), "worktree remove") {
			return nil
		}
		return &process.NotSettled{PGID: pgid, Reason: "injected: the group could not be confirmed"}
	})
}

func seededRepository(t *testing.T) (string, string) {
	t.Helper()
	root := newSnapshotRepository(t)
	writeSnapshotFile(t, root, "a.txt", "base\n")
	gitSnapshot(t, root, "add", "-A")
	gitSnapshot(t, root, "commit", "-m", "base")
	return root, strings.TrimSpace(gitSnapshot(t, root, "rev-parse", "HEAD"))
}

// leftoverDirectory is a checkout Git has no registration for. It is the case
// the filesystem fallback was written for, and the one where the directory is
// still there afterwards to assert about: Git refuses, so it deletes nothing.
func leftoverDirectory(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, ".forgepilot", "worktrees", "WI-001")
	writeSnapshotFile(t, path, "leftover.txt", "leftover\n")
	return path
}

func TestRemoveWorktreeAddsNoRecursiveDeleteToAnUnconfirmedGroup(t *testing.T) {
	root, _ := seededRepository(t)
	path := leftoverDirectory(t, root)
	restore := unconfirmedWorktreeRemoval()
	defer restore()

	err := RemoveWorktree(context.Background(), root, path)
	if err == nil {
		t.Fatal("an unconfirmed removal reported success")
	}
	if !errors.Is(err, process.ErrNotSettled) {
		t.Fatalf("err = %v, want an unsettled report", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("the checkout a recovery record points at was deleted anyway: %v", statErr)
	}
}

// AddWorktree clears a leftover checkout before making its own. That removal is
// the same destructive act under another name, and it used to discard the Git
// result entirely before calling os.RemoveAll.
func TestAddWorktreeRefusesToClearACheckoutItCannotConfirmIsIdle(t *testing.T) {
	root, revision := seededRepository(t)
	path := leftoverDirectory(t, root)
	restore := unconfirmedWorktreeRemoval()
	defer restore()

	err := AddWorktree(context.Background(), root, path, revision)
	if err == nil {
		t.Fatal("an unconfirmed pre-clean reported success")
	}
	if !errors.Is(err, process.ErrNotSettled) {
		t.Fatalf("err = %v, want an unsettled report", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("the leftover checkout was deleted despite an unconfirmed group: %v", statErr)
	}
}

// A registered worktree Git does remove before reporting an unconfirmed child
// is the honest half of the same promise: the deletion cannot be taken back,
// but the report must not become a success.
func TestARemovalThatSucceededStillReportsItsUnconfirmedGroup(t *testing.T) {
	root, revision := seededRepository(t)
	path := filepath.Join(root, ".forgepilot", "worktrees", "WI-002")
	if err := AddWorktree(context.Background(), root, path, revision); err != nil {
		t.Fatal(err)
	}
	restore := unconfirmedWorktreeRemoval()
	defer restore()

	err := RemoveWorktree(context.Background(), root, path)
	if !errors.Is(err, process.ErrNotSettled) {
		t.Fatalf("err = %v, want an unsettled report", err)
	}
}

// The ordinary failure this fallback was written for is untouched: a worktree
// Git has no registration for is still cleared from disk.
func TestRemoveWorktreeStillClearsADirectoryGitDoesNotKnowAbout(t *testing.T) {
	root, _ := seededRepository(t)
	path := leftoverDirectory(t, root)

	if err := RemoveWorktree(context.Background(), root, path); err != nil {
		t.Fatalf("an ordinary stale directory was not cleared: %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("the stale directory is still there: %v", statErr)
	}
}
