package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/process"
	"github.com/CarlLee1983/ForgePilot/internal/repository"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// Refusal marks a verification that was declined before any run began: an open
// Gate, a stopped Goal, a dirty worktree, an unsatisfiable Runtime Contract, a
// revision with no canonical check. No Evidence exists for it and none should:
// a refusal is not an engineering failure, and anything that turned one into a
// FAIL would be inventing a result the repository never produced.
type Refusal struct{ Err error }

func (refusal *Refusal) Error() string { return refusal.Err.Error() }
func (refusal *Refusal) Unwrap() error { return refusal.Err }

func refuse(err error) error {
	if err == nil {
		return nil
	}
	return &Refusal{Err: err}
}

// IsRefusal reports whether a verification was declined before it started.
func IsRefusal(err error) bool {
	var refusal *Refusal
	return errors.As(err, &refusal)
}

// ErrVerificationTimedOut reports a canonical check stopped by its deadline. The
// run produced no result, so it closes out as INTERRUPTED — never as a FAIL.
var ErrVerificationTimedOut = errors.New("canonical check exceeded its timeout")

// ErrVerificationInterrupted reports a canonical check that was stopped by its
// caller — a signal, or a deadline the caller owns — rather than by the timeout
// this command was given. The run produced no result either way, so it closes
// out as INTERRUPTED; the two are kept apart because only the caller knows
// which of its own limits ended the step, and guessing from context.Err() would
// report every expiry as a verification timeout.
var ErrVerificationInterrupted = errors.New("canonical check was interrupted")

// ErrStateWrittenDuringCheck reports that .forgepilot/state.json changed while
// the canonical check was running. The check is the repository's own script, so
// an agent session that rewrote it runs code here after its own session — and
// after the digest taken around that session — has ended. A verification whose
// governance state moved underneath it proves nothing about anything.
var ErrStateWrittenDuringCheck = errors.New("the canonical check changed .forgepilot/state.json")

// VerifyOptions carries what one verification needs beyond the Work Item.
// Timeout of zero means no deadline, which is the CLI's existing behaviour:
// ADR-0004 decided a Verification Run has no built-in time limit.
type VerifyOptions struct {
	Snapshot bool
	Timeout  time.Duration
	Now      func() time.Time
}

func (options VerifyOptions) now() time.Time {
	if options.Now != nil {
		return options.Now().UTC()
	}
	return time.Now().UTC()
}

// VerifyResult is the typed answer a caller needs in order to tell an
// engineering outcome apart from everything else that can happen. Evidence is
// present exactly when a run produced one; Reclaimed describes an earlier
// abandoned run this call closed out on the way in.
type VerifyResult struct {
	Reclaimed      *work.Evidence
	Evidence       work.Evidence
	HasEvidence    bool
	Status         work.Status
	Candidate      work.Candidate
	LogPath        string
	RuntimeSummary string
	// RefreshWarning records that Evidence was preserved but dependency
	// readiness could not be refreshed from repository facts afterwards.
	RefreshWarning error
	// Interrupted marks a run that ended without producing a result.
	Interrupted bool
	// Cleanup records that the canonical check's process group could not be
	// confirmed stopped. It is not an engineering result: any Evidence this call
	// produced still stands, and the caller must nonetheless stop rather than
	// start a step that could overlap whatever is still running.
	Cleanup error
}

// Verify runs the canonical check against one Work Item's Candidate, holding
// that Work Item's verification lock for the whole command. Holding it is what
// makes reclaiming an orphan safe: while it is held, no other live runner can
// exist, so a run still recorded in state must be abandoned.
//
// A returned error is either a *Refusal — declined before the run began — or an
// operational failure of Git, storage or the filesystem. Neither is a FAIL.
func Verify(ctx context.Context, root, id string, output io.Writer, options VerifyOptions) (VerifyResult, error) {
	var result VerifyResult
	err := storage.WithVerifyLock(root, id, func() error {
		var runErr error
		result, runErr = runVerification(ctx, root, id, output, options)
		return runErr
	})
	return result, err
}

func runVerification(ctx context.Context, root, id string, output io.Writer, options VerifyOptions) (VerifyResult, error) {
	var result VerifyResult
	if ctx == nil {
		ctx = context.Background()
	}
	// Every external process below is preceded by this. A step that has already
	// been told to stop must not be the one that starts something new, and the
	// preflight — snapshot capture, worktree checkout, runtime probes,
	// `make -n verify` — is where a cancellation path used to go blind.
	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("%w before it began: %w", ErrVerificationInterrupted, err)
	}
	// An abandoned run is a fact that already happened, so it is recorded before
	// anything is allowed to refuse the command: a block stops new work, not the
	// recording of what is already over. Reclaiming is never quiet — it is
	// reported even when the command then refuses to start a new run. See
	// docs/adr/0009-reclaim-before-refusing.md.
	reclaimed, err := reclaimOrphan(id, root, output, options.now())
	if err != nil {
		return result, err
	}
	result.Reclaimed = reclaimed
	// From here nothing else is written until every refusal has been passed.
	state, err := storage.Load(root)
	if err != nil {
		return result, err
	}
	if err := state.CanBeginVerification(id); err != nil {
		return result, refuse(err)
	}
	startedAt := options.now()
	candidate := work.Candidate{Kind: work.CommitCandidate}
	if options.Snapshot {
		captured, err := repository.CaptureSnapshot(root, id, startedAt)
		if err != nil {
			return result, err
		}
		candidate = work.Candidate{Kind: work.SnapshotCandidate, Revision: captured.Revision,
			BaseRevision: captured.BaseRevision, Digest: captured.Digest}
	} else {
		if err := repository.EnsureClean(root, "verifying"); err != nil {
			return result, refuse(err)
		}
		revision, err := repository.Head(root)
		if err != nil {
			return result, err
		}
		candidate.Revision = revision
	}
	result.Candidate = candidate

	// The canonical check is looked for in the isolated checkout, not the user's
	// worktree: those are different file trees, and only the checkout holds what
	// the recorded revision actually contains.
	worktree := WorktreePath(root, id, candidate.Revision)
	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("%w before its checkout was made: %w", ErrVerificationInterrupted, err)
	}
	if err := repository.PruneWorktrees(root); err != nil {
		return result, err
	}
	if err := repository.AddWorktree(root, worktree, candidate.Revision); err != nil {
		return result, err
	}
	runtime, err := repository.ResolveRuntimeInContext(ctx, worktree)
	if err != nil {
		_ = repository.RemoveWorktree(root, worktree)
		// A cancelled resolution is not the repository failing to declare a usable
		// runtime, so it must not be reported as a refusal: that would record a
		// condition outside the code where there is only a stop signal.
		if ctx.Err() != nil {
			return result, fmt.Errorf("%w while resolving its runtime: %w", ErrVerificationInterrupted, err)
		}
		// Nor is a process group nobody could confirm empty. The preflight runs
		// external processes too, and a refusal there would record "this revision
		// has no usable runtime" for something that is only a stop that did not
		// complete.
		if errors.Is(err, process.ErrNotSettled) {
			result.Cleanup = err
			return result, err
		}
		return result, refuse(err)
	}
	defer func() {
		if closeErr := runtime.Close(); closeErr != nil {
			fmt.Fprintf(output, "warning: could not remove resolved runtime environment: %v\n", closeErr)
		}
	}()
	if err := repository.EnsureCanonicalCheckInContext(ctx, worktree, runtime); err != nil {
		_ = repository.RemoveWorktree(root, worktree)
		if ctx.Err() != nil {
			return result, fmt.Errorf("%w during its preflight: %w", ErrVerificationInterrupted, err)
		}
		if errors.Is(err, process.ErrNotSettled) {
			result.Cleanup = err
			return result, err
		}
		return result, refuse(err)
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
	logFile := LogPath(root, id, candidate.Revision, startedAt)
	log, err := repository.OpenLog(logFile)
	if err != nil {
		return result, err
	}
	defer log.Close()
	result.LogPath = logFile
	result.RuntimeSummary = runtime.Summary()
	if options.Snapshot {
		if _, err := fmt.Fprintf(output, "Candidate: SNAPSHOT\nRevision: %s\nBase: %s\n", candidate.Revision, candidate.BaseRevision); err != nil {
			return result, err
		}
	}
	if summary := runtime.Summary(); summary != "" {
		if _, err := fmt.Fprintf(output, "Runtime: %s\n", summary); err != nil {
			return result, err
		}
	}
	if _, err := fmt.Fprintf(output, "Log: %s\n", logFile); err != nil {
		return result, err
	}

	if err := beginRun(id, root, candidate, worktree, logFile, runtime.Versions(), startedAt); err != nil {
		return result, err
	}
	// Taken after beginRun, this command's own last write before the check
	// starts, so only what happens while the check runs is in the window.
	verdictBefore, err := verdictFingerprint(root, id)
	if err != nil {
		return result, err
	}
	exitCode, cleanupErr, runErr := runCanonicalCheck(ctx, worktree, runtime, log, options.Timeout)
	result.Cleanup = cleanupErr
	// A state that no longer loads is the loudest version of the same signal:
	// it parsed on the way in, so whatever happened to it happened here.
	verdictAfter, fingerprintErr := verdictFingerprint(root, id)
	if fingerprintErr != nil || verdictAfter != verdictBefore {
		// Checked before the exit code is even looked at: something moved this
		// Work Item's verdict while the check that decides it was still running,
		// so the check's own account of what it did is worthless. Nothing is
		// written in response — not even the reclaim that closes out a timeout —
		// because a state this command no longer recognises is not a state to
		// transition. The verification stays live, which is what makes the next
		// command say so rather than quietly carry on.
		return result, verificationVerdictChanged(id)
	}
	if errors.Is(runErr, ErrVerificationTimedOut) || errors.Is(runErr, ErrVerificationInterrupted) {
		// The check produced no result, so the run is closed out the same way any
		// other interruption is: INTERRUPTED Evidence, never an inferred FAIL. A
		// run that can still be settled legally is settled, so nobody is left with
		// a VERIFYING that has no live runner behind it.
		interrupted, _, _, _, reclaimErr := reclaimRun(id, root, options.now(), verdictBefore)
		if reclaimErr != nil {
			return result, reclaimErr
		}
		result.Interrupted, result.Evidence, result.HasEvidence = true, interrupted, true
		result.Status = statusOf(root, id)
		if _, err := fmt.Fprintf(output, "%s %s at %s\n%s %s\n", interrupted.ID, interrupted.Result, interrupted.Revision, id, result.Status); err != nil {
			return result, err
		}
		return result, runErr
	}
	if runErr != nil {
		return result, runErr
	}

	var evidence work.Evidence
	var status work.Status
	var factsErr error
	if err := storage.Update(root, func(state *work.State) error {
		if err := ensureVerificationVerdictUnchanged(state, id, verdictBefore); err != nil {
			return err
		}
		var recordErr error
		repositoryState, err := CandidateFacts(state, root)
		if err != nil {
			factsErr = err
			evidence, recordErr = state.RecordVerification(id, candidate.Revision, repository.CanonicalCommand, exitCode, options.now())
		} else {
			evidence, recordErr = state.RecordVerificationWithRepository(id, candidate.Revision, repository.CanonicalCommand, exitCode, repositoryState, options.now())
		}
		if recordErr == nil {
			status = state.WorkItemStatus(id)
		}
		return recordErr
	}); err != nil {
		return result, err
	}
	result.Evidence, result.HasEvidence, result.Status = evidence, true, status
	result.RefreshWarning = factsErr
	// Non-PASS output no longer floods stdout: the log just printed above is
	// where it lives now. See docs/adr/0012-verification-log-outside-state.md.
	if _, err = fmt.Fprintf(output, "%s %s at %s\n%s %s\n", evidence.ID, evidence.Result, evidence.Revision, id, status); err != nil {
		return result, err
	}
	if factsErr != nil {
		_, err = fmt.Fprintf(output, "warning: dependency readiness was not refreshed: %v; Evidence was preserved; repair repository access and rerun %s\n", factsErr, VerificationRetryCommand(id, candidate))
	}
	return result, err
}

// VerificationRetryCommand names the command that would rerun this exact
// verification, so a warning can tell the user what to do rather than only what
// went wrong.
func VerificationRetryCommand(id string, candidate work.Candidate) string {
	command := fmt.Sprintf("forgepilot verify %s", id)
	if candidate.Kind == work.SnapshotCandidate {
		command += " --snapshot"
	}
	return command
}

// verdictFingerprint captures the facts this verification is about to decide:
// the Work Item's own status and its latest Verification Evidence. Nothing else belongs in
// it. A digest of the whole state file would be simpler and wrong — a person
// running `goal block` or `gate resolve` in another terminal writes state
// legitimately while a check runs, and ADR-0010's transaction lock is
// deliberately not held across it, so a byte comparison would refuse a run that
// nobody tampered with and lose Evidence that was honestly earned.
func verdictFingerprint(root, id string) (string, error) {
	state, err := storage.Load(root)
	if err != nil {
		return "", err
	}
	return verificationVerdictFingerprint(&state, id)
}

// verificationVerdictFingerprint is the complete state that can affect the
// target verification's recorded result: its Work Item (including CurrentRun),
// owning Goal's repository and review policy, and latest Verification Evidence.
// It deliberately excludes mutable Goal lifecycle fields, sibling Work Items,
// other Goals and Gates so a legitimate governance write does not discard an
// honestly earned verification result.
func verificationVerdictFingerprint(state *work.State, id string) (string, error) {
	latest, ok := state.LatestVerification(id)
	type goalInputs struct {
		ID           string            `json:"id"`
		Repository   string            `json:"repository"`
		ReviewPolicy work.ReviewPolicy `json:"review_policy"`
	}
	verdict := struct {
		Item     *work.Item     `json:"item,omitempty"`
		Goal     *goalInputs    `json:"goal,omitempty"`
		Evidence *work.Evidence `json:"evidence,omitempty"`
	}{}
	for index := range state.WorkItems {
		if state.WorkItems[index].ID != id {
			continue
		}
		item := state.WorkItems[index]
		verdict.Item = &item
		for goalIndex := range state.Goals {
			if state.Goals[goalIndex].ID == item.GoalID {
				goal := state.Goals[goalIndex]
				verdict.Goal = &goalInputs{ID: goal.ID, Repository: goal.Repository, ReviewPolicy: goal.ReviewPolicy}
				break
			}
		}
		break
	}
	if ok {
		verdict.Evidence = &latest
	}
	encoded, err := json.Marshal(verdict)
	if err != nil {
		return "", fmt.Errorf("encode verification verdict: %w", err)
	}
	return string(encoded), nil
}

// ensureVerificationVerdictUnchanged is called inside the transaction that
// writes an outcome, closing the gap between the post-check observation and
// RecordVerification. It intentionally projects only the target Work Item's
// verdict, so unrelated human governance writes remain legitimate.
func ensureVerificationVerdictUnchanged(state *work.State, id, expected string) error {
	actual, err := verificationVerdictFingerprint(state, id)
	if err != nil || actual != expected {
		return verificationVerdictChanged(id)
	}
	return nil
}

func verificationVerdictChanged(id string) error {
	return fmt.Errorf("%w while verifying %s; nothing it reported can be trusted. Inspect the state before running anything else",
		ErrStateWrittenDuringCheck, id)
}

func statusOf(root, id string) work.Status {
	state, err := storage.Load(root)
	if err != nil {
		return ""
	}
	return state.WorkItemStatus(id)
}

// runCanonicalCheck executes the canonical check under the caller's context and
// this command's own optional deadline. It reports three things separately,
// because they are three different questions: the engineering exit code, whether
// the managed process group could be confirmed stopped, and why the step ended.
//
// The child is given its own process group so the whole tree is stopped rather
// than only its outermost process, and the group is settled on every path — a
// check that exits 0 having forked a watcher has not finished owning the
// worktree. See docs/adr/0020-worker-ownership-is-fail-closed.md.
func runCanonicalCheck(ctx context.Context, directory string, runtime repository.RuntimeEnvironment, log io.Writer, timeout time.Duration) (int, error, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	checkCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		checkCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	run, err := repository.RunCanonicalCheckInContext(checkCtx, directory, runtime, log)
	if run.Completed {
		// "It reached its own end" is not the same as "it produced a result": a
		// wait that failed for its own reasons — ECHILD, an I/O failure — leaves
		// exit code zero, and returning that as an outcome would write a PASS for
		// a check nobody watched finish.
		if err != nil {
			return 0, run.Cleanup, err
		}
		// Decided when it happened, not by asking the context afterwards: a result
		// this check genuinely produced is not discarded because a signal arrived
		// beside it, and it is the last thing to reach a durable Evidence write.
		return run.ExitCode, run.Cleanup, nil
	}
	// The caller's own end is asked about first. Classifying by the combined
	// deadline would report a run that ran out of total duration as a
	// verification timeout, sending whoever reads the record to the wrong flag.
	if ctx.Err() != nil {
		return 0, run.Cleanup, fmt.Errorf("%w: %w", ErrVerificationInterrupted, ctx.Err())
	}
	if timeout > 0 && errors.Is(checkCtx.Err(), context.DeadlineExceeded) {
		return 0, run.Cleanup, fmt.Errorf("%w after %s", ErrVerificationTimedOut, timeout)
	}
	if err != nil {
		return 0, run.Cleanup, err
	}
	return 0, run.Cleanup, fmt.Errorf("%w: the canonical check ended without a result", ErrVerificationInterrupted)
}

// reclaimOrphan closes out a run that was abandoned, recording it as INTERRUPTED
// and returning the Work Item to RUNNING. The caller holds the Work Item's
// verification lock, so a run still marked in flight can only be an orphan: no
// live runner can exist.
//
// It asks nothing about Gates or the Goal. Whether a new run may start is a
// separate question, decided after this and by different rules.
func reclaimOrphan(id, root string, output io.Writer, now time.Time) (*work.Evidence, error) {
	reclaimed, abandoned, logFile, found, err := reclaimRun(id, root, now, "")
	if err != nil || !found {
		return nil, err
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
	if _, err := fmt.Fprintf(output, "%s %s at %s (previous run did not finish, log: %s)\n", reclaimed.ID, reclaimed.Result, reclaimed.Revision, logFile); err != nil {
		return nil, err
	}
	return &reclaimed, nil
}

func reclaimRun(id, root string, now time.Time, expectedVerdict string) (work.Evidence, string, string, bool, error) {
	var reclaimed work.Evidence
	var abandoned, logFile string
	var found bool
	err := storage.Update(root, func(state *work.State) error {
		if expectedVerdict != "" {
			if err := ensureVerificationVerdictUnchanged(state, id, expectedVerdict); err != nil {
				return err
			}
		}
		var updateErr error
		reclaimed, abandoned, logFile, found, updateErr = state.ReclaimRun(id, repository.CanonicalCommand, now)
		return updateErr
	})
	return reclaimed, abandoned, logFile, found, err
}

// beginRun marks a new Verification Run in flight. Any abandoned run has already
// been closed out by reclaimOrphan, so a Work Item is never left with neither an
// outcome for its old run nor a record of its new one.
func beginRun(id, root string, candidate work.Candidate, worktree, logFile string, runtime map[string]string, startedAt time.Time) error {
	return storage.Update(root, func(state *work.State) error {
		return state.BeginCandidateVerificationWithRuntime(id, candidate, worktree, logFile, runtime, startedAt)
	})
}

// WorktreePath names the isolated checkout one Verification Run uses.
func WorktreePath(root, id, revision string) string {
	return filepath.Join(root, ".forgepilot", "worktrees", fmt.Sprintf("%s-%s", id, shortRevision(revision)))
}

// LogPath names a Verification Run's output file by the run itself, not by the
// Evidence it will eventually produce: the Evidence ID is only assigned in the
// transaction that closes the run out, so it does not exist yet when the log
// must be opened. started-at only keeps repeated runs against the same
// revision from overwriting each other. See docs/adr/0012-verification-log-outside-state.md.
func LogPath(root, id, revision string, startedAt time.Time) string {
	return filepath.Join(root, ".forgepilot", "logs", fmt.Sprintf("%s-%s-%s.log", id, shortRevision(revision), startedAt.Format("20060102T150405.000000000Z")))
}
