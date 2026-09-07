package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/carl/forgepilot/internal/repository"
	"github.com/carl/forgepilot/internal/storage"
	"github.com/carl/forgepilot/internal/work"
)

func verify(args []string, root string, output io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: forgepilot verify <work-id>")
	}
	id := args[0]
	// Holding the Work Item's verification lock for the whole command is what
	// makes reclaiming an orphan safe: while it is held, no other live runner can
	// exist, so a run still recorded in state must be abandoned.
	return storage.WithVerifyLock(root, id, func() error {
		return runVerification(id, root, output)
	})
}

func runVerification(id, root string, output io.Writer) error {
	// Everything that can refuse the command happens before anything is written.
	// A user who sees this command fail must be able to trust that it changed
	// nothing — including that it did not quietly reclaim an abandoned run.
	state, err := storage.Load(root)
	if err != nil {
		return err
	}
	if err := state.CanBeginVerification(id); err != nil {
		return err
	}
	if err := repository.EnsureClean(root); err != nil {
		return err
	}
	revision, err := repository.Head(root)
	if err != nil {
		return err
	}

	// The canonical check is looked for in the isolated checkout, not the user's
	// worktree: those are different file trees, and only the checkout holds what
	// the recorded revision actually contains.
	worktree := worktreePath(root, id, revision)
	if err := repository.PruneWorktrees(root); err != nil {
		return err
	}
	if err := repository.AddWorktree(root, worktree, revision); err != nil {
		return err
	}
	if err := repository.EnsureCanonicalCheck(worktree); err != nil {
		_ = repository.RemoveWorktree(root, worktree)
		return err
	}

	// Cleanup runs last and cannot veto a result: once the canonical check has
	// produced an outcome, failing to tidy up must not discard it.
	defer func() {
		if removeErr := repository.RemoveWorktree(root, worktree); removeErr != nil {
			fmt.Fprintf(output, "warning: could not remove %s: %v\n", worktree, removeErr)
		}
	}()

	if err := beginRun(id, root, revision, worktree, output); err != nil {
		return err
	}
	exitCode, runOutput, runErr := repository.RunCanonicalCheck(worktree)
	if runErr != nil {
		return runErr
	}

	var evidence work.Evidence
	var status work.Status
	if err := storage.Update(root, func(state *work.State) error {
		var recordErr error
		evidence, recordErr = state.RecordVerification(id, revision, repository.CanonicalCommand, exitCode, now())
		if recordErr == nil {
			status = state.WorkItemStatus(id)
		}
		return recordErr
	}); err != nil {
		return err
	}
	if evidence.Result != work.Pass {
		fmt.Fprint(output, runOutput)
	}
	_, err = fmt.Fprintf(output, "%s %s at %s\n%s %s\n", evidence.ID, evidence.Result, evidence.Revision, id, status)
	return err
}

// beginRun closes out any abandoned run and marks the new one in a single
// transaction, as the state machine requires: a Work Item is never briefly left
// with neither an outcome for its old run nor a record of its new one.
func beginRun(id, root, revision, worktree string, output io.Writer) error {
	var reclaimed work.Evidence
	var abandoned string
	var found bool
	if err := storage.Update(root, func(state *work.State) error {
		var err error
		if reclaimed, abandoned, found, err = state.ReclaimRun(id, repository.CanonicalCommand, now()); err != nil {
			return err
		}
		return state.BeginVerification(id, revision, worktree, now())
	}); err != nil {
		return err
	}
	if !found {
		return nil
	}
	// The abandoned run's own worktree, taken from state rather than recomputed.
	if abandoned != "" && abandoned != worktree {
		_ = repository.RemoveWorktree(root, abandoned)
	}
	_, err := fmt.Fprintf(output, "%s %s at %s (previous run did not finish)\n", reclaimed.ID, reclaimed.Result, reclaimed.Revision)
	return err
}

func worktreePath(root, id, revision string) string {
	return filepath.Join(root, ".forgepilot", "worktrees", fmt.Sprintf("%s-%s", id, shortRevision(revision)))
}
