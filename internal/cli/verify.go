package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/CarlLee1983/ForgePilot/internal/repository"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
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
	// An abandoned run is a fact that already happened, so it is recorded before
	// anything is allowed to refuse the command: a block stops new work, not the
	// recording of what is already over. Reclaiming is never quiet — it is
	// reported even when the command then refuses to start a new run. See
	// docs/adr/0009-reclaim-before-refusing.md.
	if err := reclaimOrphan(id, root, output); err != nil {
		return err
	}
	// From here nothing else is written until every refusal has been passed.
	state, err := storage.Load(root)
	if err != nil {
		return err
	}
	if err := state.CanBeginVerification(id); err != nil {
		return err
	}
	if err := repository.EnsureClean(root, "verifying"); err != nil {
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

// reclaimOrphan closes out a run that was abandoned, recording it as INTERRUPTED
// and returning the Work Item to RUNNING. The caller holds the Work Item's
// verification lock, so a run still marked in flight can only be an orphan: no
// live runner can exist.
//
// It asks nothing about Gates or the Goal. Whether a new run may start is a
// separate question, decided after this and by different rules.
func reclaimOrphan(id, root string, output io.Writer) error {
	var reclaimed work.Evidence
	var abandoned string
	var found bool
	if err := storage.Update(root, func(state *work.State) error {
		var err error
		reclaimed, abandoned, found, err = state.ReclaimRun(id, repository.CanonicalCommand, now())
		return err
	}); err != nil {
		return err
	}
	if !found {
		return nil
	}
	// The abandoned run's own worktree, taken from state rather than recomputed:
	// state holds where that run actually ran, which survives changes to the
	// naming scheme or the layout.
	if abandoned != "" {
		_ = repository.RemoveWorktree(root, abandoned)
	}
	_, err := fmt.Fprintf(output, "%s %s at %s (previous run did not finish)\n", reclaimed.ID, reclaimed.Result, reclaimed.Revision)
	return err
}

// beginRun marks a new Verification Run in flight. Any abandoned run has already
// been closed out by reclaimOrphan, so a Work Item is never left with neither an
// outcome for its old run nor a record of its new one.
func beginRun(id, root, revision, worktree string, output io.Writer) error {
	return storage.Update(root, func(state *work.State) error {
		return state.BeginVerification(id, revision, worktree, now())
	})
}

func worktreePath(root, id, revision string) string {
	return filepath.Join(root, ".forgepilot", "worktrees", fmt.Sprintf("%s-%s", id, shortRevision(revision)))
}
