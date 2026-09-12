package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/repository"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func verify(args []string, root string, output io.Writer) error {
	if len(args) < 1 || len(args) > 2 || (len(args) == 2 && args[1] != "--snapshot") {
		return errors.New("usage: forgepilot verify <work-id> [--snapshot]")
	}
	id := args[0]
	snapshot := len(args) == 2
	// Holding the Work Item's verification lock for the whole command is what
	// makes reclaiming an orphan safe: while it is held, no other live runner can
	// exist, so a run still recorded in state must be abandoned.
	return storage.WithVerifyLock(root, id, func() error {
		return runVerification(id, root, output, snapshot)
	})
}

func runVerification(id, root string, output io.Writer, snapshot bool) error {
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
	startedAt := now()
	candidate := work.Candidate{Kind: work.CommitCandidate}
	if snapshot {
		captured, err := repository.CaptureSnapshot(root, id, startedAt)
		if err != nil {
			return err
		}
		candidate = work.Candidate{Kind: work.SnapshotCandidate, Revision: captured.Revision,
			BaseRevision: captured.BaseRevision, Digest: captured.Digest}
	} else {
		if err := repository.EnsureClean(root, "verifying"); err != nil {
			return err
		}
		revision, err := repository.Head(root)
		if err != nil {
			return err
		}
		candidate.Revision = revision
	}

	// The canonical check is looked for in the isolated checkout, not the user's
	// worktree: those are different file trees, and only the checkout holds what
	// the recorded revision actually contains.
	worktree := worktreePath(root, id, candidate.Revision)
	if err := repository.PruneWorktrees(root); err != nil {
		return err
	}
	if err := repository.AddWorktree(root, worktree, candidate.Revision); err != nil {
		return err
	}
	runtime, err := repository.ResolveRuntime(worktree)
	if err != nil {
		_ = repository.RemoveWorktree(root, worktree)
		return err
	}
	defer func() {
		if closeErr := runtime.Close(); closeErr != nil {
			fmt.Fprintf(output, "warning: could not remove resolved runtime environment: %v\n", closeErr)
		}
	}()
	if err := repository.EnsureCanonicalCheckWithRuntime(worktree, runtime); err != nil {
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

	// The log is opened, and its path printed, before anything about the run is
	// recorded: a log that cannot be created must abort the command before any
	// state is written or Evidence appended, not degrade into a run with no log.
	logFile := logPath(root, id, candidate.Revision, startedAt)
	log, err := repository.OpenLog(logFile)
	if err != nil {
		return err
	}
	defer log.Close()
	if snapshot {
		if _, err := fmt.Fprintf(output, "Candidate: SNAPSHOT\nRevision: %s\nBase: %s\n", candidate.Revision, candidate.BaseRevision); err != nil {
			return err
		}
	}
	if summary := runtime.Summary(); summary != "" {
		if _, err := fmt.Fprintf(output, "Runtime: %s\n", summary); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(output, "Log: %s\n", logFile); err != nil {
		return err
	}

	if err := beginRun(id, root, candidate, worktree, logFile, runtime.Versions(), startedAt); err != nil {
		return err
	}
	exitCode, runErr := repository.RunCanonicalCheckWithRuntime(worktree, runtime, log)
	if runErr != nil {
		return runErr
	}

	var evidence work.Evidence
	var status work.Status
	if err := storage.Update(root, func(state *work.State) error {
		var recordErr error
		evidence, recordErr = state.RecordVerification(id, candidate.Revision, repository.CanonicalCommand, exitCode, now())
		if recordErr == nil {
			status = state.WorkItemStatus(id)
		}
		return recordErr
	}); err != nil {
		return err
	}
	// Non-PASS output no longer floods stdout: the log just printed above is
	// where it lives now. See docs/adr/0012-verification-log-outside-state.md.
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
	var abandoned, logFile string
	var found bool
	if err := storage.Update(root, func(state *work.State) error {
		var err error
		reclaimed, abandoned, logFile, found, err = state.ReclaimRun(id, repository.CanonicalCommand, now())
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
	// logFile is the same value beginRun wrote to current_run, not a path
	// re-derived from today's naming scheme: the streamed output an
	// interrupted run leaves behind (see docs/adr/0012-verification-log-outside-state.md)
	// is otherwise unreachable without it.
	_, err := fmt.Fprintf(output, "%s %s at %s (previous run did not finish, log: %s)\n", reclaimed.ID, reclaimed.Result, reclaimed.Revision, logFile)
	return err
}

// beginRun marks a new Verification Run in flight. Any abandoned run has already
// been closed out by reclaimOrphan, so a Work Item is never left with neither an
// outcome for its old run nor a record of its new one.
func beginRun(id, root string, candidate work.Candidate, worktree, logFile string, runtime map[string]string, startedAt time.Time) error {
	return storage.Update(root, func(state *work.State) error {
		return state.BeginCandidateVerificationWithRuntime(id, candidate, worktree, logFile, runtime, startedAt)
	})
}

func worktreePath(root, id, revision string) string {
	return filepath.Join(root, ".forgepilot", "worktrees", fmt.Sprintf("%s-%s", id, shortRevision(revision)))
}

// logPath names a Verification Run's output file by the run itself, not by the
// Evidence it will eventually produce: the Evidence ID is only assigned in the
// transaction that closes the run out, so it does not exist yet when the log
// must be opened. started-at only keeps repeated runs against the same
// revision from overwriting each other. See docs/adr/0012-verification-log-outside-state.md.
func logPath(root, id, revision string, startedAt time.Time) string {
	return filepath.Join(root, ".forgepilot", "logs", fmt.Sprintf("%s-%s-%s.log", id, shortRevision(revision), startedAt.Format("20060102T150405.000000000Z")))
}
