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
	// makes the reclaim below safe: while it is held, no other live runner can
	// exist, so any run still recorded in state must be an orphan.
	return storage.WithVerifyLock(root, id, func() error {
		return runVerification(id, root, output)
	})
}

func runVerification(id, root string, output io.Writer) error {
	if err := reclaimOrphanedRun(id, root, output); err != nil {
		return err
	}

	// Refuse before creating anything. A project with no canonical check, or a
	// worktree HEAD does not describe, cannot be verified at all — neither is a
	// verification failure, so neither may leave Evidence behind.
	state, err := storage.Load(root)
	if err != nil {
		return err
	}
	if err := state.Verifiable(id); err != nil {
		return err
	}
	if err := repository.EnsureCanonicalCheck(root); err != nil {
		return err
	}
	if err := repository.EnsureClean(root); err != nil {
		return err
	}
	revision, err := repository.Head(root)
	if err != nil {
		return err
	}

	worktree := worktreePath(root, id, revision)
	if err := storage.Update(root, func(state *work.State) error {
		return state.BeginVerification(id, revision, worktree, now())
	}); err != nil {
		return err
	}

	if err := repository.PruneWorktrees(root); err != nil {
		return err
	}
	if err := repository.AddWorktree(root, worktree, revision); err != nil {
		return err
	}
	exitCode, runOutput, runErr := repository.RunCanonicalCheck(worktree)
	if removeErr := repository.RemoveWorktree(root, worktree); removeErr != nil && runErr == nil {
		runErr = removeErr
	}
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

// reclaimOrphanedRun closes out a Verification Run whose process died. The caller
// holds the verification lock, so nothing is still running; the outcome is
// unknown, and recording it as anything other than INTERRUPTED would be a
// fabricated result.
func reclaimOrphanedRun(id, root string, output io.Writer) error {
	var reclaimed work.Evidence
	var found bool
	if err := storage.Update(root, func(state *work.State) error {
		var err error
		reclaimed, found, err = state.ReclaimRun(id, now())
		return err
	}); err != nil {
		return err
	}
	if !found {
		return nil
	}
	if reclaimed.Revision != "" {
		if path := worktreePath(root, id, reclaimed.Revision); path != "" {
			_ = repository.RemoveWorktree(root, path)
		}
	}
	_ = repository.PruneWorktrees(root)
	_, err := fmt.Fprintf(output, "%s %s at %s (previous run did not finish)\n", reclaimed.ID, reclaimed.Result, reclaimed.Revision)
	return err
}

func worktreePath(root, id, revision string) string {
	short := revision
	if len(short) > 12 {
		short = short[:12]
	}
	return filepath.Join(root, ".forgepilot", "worktrees", fmt.Sprintf("%s-%s", id, short))
}
