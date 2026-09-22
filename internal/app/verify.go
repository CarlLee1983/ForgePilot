package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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

// ErrExecutionBindingDrift reports that a Runner-bound plan or authorization
// changed at the verification boundary. It is separate from the verdict guard:
// a verification may have honestly produced Candidate Evidence before this
// later governance change was observed.
var ErrExecutionBindingDrift = errors.New("the execution plan or authorization changed during verification")

// VerifyOptions carries what one verification needs beyond the Work Item.
// Timeout of zero means no deadline, which is the CLI's existing behaviour:
// ADR-0004 decided a Verification Run has no built-in time limit.
type VerifyOptions struct {
	Snapshot bool
	Timeout  time.Duration
	Now      func() time.Time
	// ExecutionGoalID and ExecutionAuthorizationDigest are an optional Runner
	// boundary guard. Standalone verification leaves them empty and retains its
	// existing contract; a Runner supplies both so a revision is rejected before
	// a check starts and reported after an already-started check's Evidence is
	// committed.
	ExecutionGoalID              string
	ExecutionAuthorizationDigest string
}

type FanoutSkip = work.FanoutSkip

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
	Reclaimed         *work.Evidence
	ReclaimedSet      []work.Evidence
	Evidence          work.Evidence
	EvidenceSet       []work.Evidence
	HasEvidence       bool
	VerificationRunID string
	Skipped           []FanoutSkip
	Status            work.Status
	Candidate         work.Candidate
	LogPath           string
	RuntimeSummary    string
	// RefreshWarning records that Evidence was preserved but dependency
	// readiness could not be refreshed from repository facts afterwards.
	RefreshWarning error
	// Interrupted marks a run that ended without producing a result.
	Interrupted bool
	// Cleanup records that a managed process group could not be confirmed
	// stopped. It is not an engineering result: any Evidence this call
	// produced still stands, and the caller must nonetheless stop rather than
	// start a step that could overlap whatever is still running.
	Cleanup error
	// Unresolved describes those groups in the terms a caller needs to persist a
	// recovery record: which stage started them, which group id was observed, and
	// where it was working. A sentence in Cleanup cannot be checked by the next
	// process; this can. See docs/adr/0022-pending-cleanup-outlives-the-process.md.
	Unresolved []Unresolved
}

// Unresolved is one managed process group a verification could not confirm
// stopped. It is the typed boundary between the layer that observes the fact
// and the layer that persists it: internal/app never learns what a run record
// looks like, and internal/runner never re-derives the fact from a message.
type Unresolved struct {
	// Kind names the stage that started the group.
	Kind string
	// PGID is the group id observed at launch, or zero when none was recorded —
	// which is itself a fail-closed answer, not an absence of work to do.
	PGID int
	// Location is the checkout or worktree the group was working in.
	Location string
	// Detail is the original report, kept for a person rather than for a rule.
	Detail string
}

// The stages that can leave a group behind. They are named rather than inferred
// so a recovery record says which external process was running.
const (
	UnresolvedRuntimePreflight   = "RUNTIME_PREFLIGHT"
	UnresolvedCanonicalPreflight = "CANONICAL_PREFLIGHT"
	UnresolvedCanonicalCheck     = "CANONICAL_CHECK"
	UnresolvedGit                = "GIT"
)

// note records an unconfirmed group on the result, keeping both the typed
// description and the joined error a caller may simply test.
func (result *VerifyResult) note(kind, location string, err error) {
	if err == nil {
		return
	}
	pgid, _ := process.UnsettledGroup(err)
	result.Unresolved = append(result.Unresolved,
		Unresolved{Kind: kind, PGID: pgid, Location: location, Detail: err.Error()})
	result.Cleanup = errors.Join(result.Cleanup, err)
}

// cleanupWindow is the one bounded allowance a verification has for the tidying
// that must still happen after its own context has ended. It is deliberately
// neither the caller's context — which is cancelled, and would skip necessary
// cleanup entirely — nor context.Background(), which would put no limit on it
// at all. It is drawn once per verification and shared, so a path made of
// several cleanup helpers still has one total somebody can state. It is per
// cleanup stage, not per command: a stage that never begins spends nothing, and
// a stage that does spends one allowance however many helpers it runs.
// See docs/adr/0021-execution-limits-are-bounded-and-named.md.
type cleanupWindow struct {
	parent context.Context
	ctx    context.Context
	cancel context.CancelFunc
}

func newCleanupWindow(parent context.Context) *cleanupWindow {
	return &cleanupWindow{parent: parent}
}

// context opens the window on first use. Opening it earlier would start the
// clock during the work rather than during the cleanup, so by the time cleanup
// began there would be nothing left of it.
//
// The window carries one process.Budget as well as one deadline. Without it,
// every managed process started inside the window — each `git worktree remove`,
// each prune — opened a fresh full grace of its own, and the window's own
// timeout only governed starting them, not the confirmation waits underneath.
// A cleanup path made of several commands would then have had no total anyone
// could state, which is precisely what ADR-0022 says must not happen.
func (window *cleanupWindow) context() context.Context {
	if window.ctx == nil {
		ctx, cancel := context.WithTimeout(
			context.WithoutCancel(window.parent), process.CleanupGrace)
		window.ctx, window.cancel = process.WithBudget(ctx, process.NewBudget()), cancel
	}
	return window.ctx
}

func (window *cleanupWindow) release() {
	if window.cancel != nil {
		window.cancel()
	}
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

func runVerification(ctx context.Context, root, id string, output io.Writer, options VerifyOptions) (result VerifyResult, _ error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Two allowances, not one. Closing out an abandoned run and tidying up after
	// this one are different cleanup stages separated by the whole of the work in
	// between, and a single window would have the second stage drawing on a
	// deadline that started before the first. Each opens when its own stage runs.
	reclaim := newCleanupWindow(ctx)
	defer reclaim.release()
	cleanup := newCleanupWindow(ctx)
	defer cleanup.release()
	// An abandoned run is a fact that already happened, so it is recorded before
	// anything is allowed to refuse the command: a block stops new work, not the
	// recording of what is already over. Reclaiming is never quiet — it is
	// reported even when the command then refuses to start a new run. See
	// docs/adr/0009-reclaim-before-refusing.md.
	reclaimed, err := reclaimOrphan(reclaim, &result, id, root, output, options.now())
	if err != nil {
		return result, err
	}
	result.Reclaimed = reclaimed
	if reclaimed != nil {
		result.ReclaimedSet = []work.Evidence{*reclaimed}
	}
	// Reclaiming runs external Git. A removal that could not confirm what it
	// stopped is the same refusal here as anywhere else below: nothing new may
	// start, and the fact travels to the caller rather than into a warning.
	if result.Cleanup != nil {
		return result, fmt.Errorf("the abandoned run's checkout could not be cleared safely: %w", result.Cleanup)
	}
	// Reclaim is unconditional: a stop prevents new work, but cannot erase an
	// abandoned run that already happened. Only after reclaim may cancellation
	// refuse the new Candidate, checkout, log, and subprocess.
	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("%w before it began: %w", ErrVerificationInterrupted, err)
	}
	var operationErr error
	lockErr := storage.WithCanonicalVerificationLock(root, func() error {
		result, operationErr = runVerificationLocked(ctx, root, id, output, options, result, cleanup)
		return operationErr
	})
	if errors.Is(lockErr, storage.ErrCanonicalVerificationInFlight) {
		return result, refuse(lockErr)
	}
	return result, lockErr
}

func runVerificationLocked(ctx context.Context, root, id string, output io.Writer, options VerifyOptions, initial VerifyResult, cleanup *cleanupWindow) (result VerifyResult, returnErr error) {
	result = initial
	// From here nothing else is written until every refusal has been passed.
	state, err := storage.Load(root)
	if err != nil {
		return result, err
	}
	if err := state.CanBeginVerification(id); err != nil {
		return result, refuse(err)
	}
	// A Runner-bound verification must recheck its plan and authorization before
	// any Candidate capture, checkout, or runtime preflight can begin. The later
	// check immediately before beginRun closes the preparation window as well.
	if err := verifyExecutionBinding(root, options); err != nil {
		return result, err
	}
	verificationRunID := state.NextVerificationRun()
	startedAt := options.now()
	candidate := work.Candidate{Kind: work.CommitCandidate}
	if options.Snapshot {
		// Under ctx like everything else: capture runs `read-tree`, `add -A` and
		// `write-tree`, each of which runs this repository's own hooks and filters
		// and can therefore block for as long as they like.
		captured, err := repository.CaptureSnapshot(ctx, root, id, startedAt)
		if err != nil {
			return result, result.gitStage(ctx, UnresolvedGit, root, "while capturing its Candidate", err, false)
		}
		candidate = work.Candidate{Kind: work.SnapshotCandidate, Revision: captured.Revision,
			BaseRevision: captured.BaseRevision, Digest: captured.Digest}
	} else {
		if err := repository.EnsureClean(ctx, root, "verifying"); err != nil {
			return result, result.gitStage(ctx, UnresolvedGit, root, "before its Candidate was resolved", err, true)
		}
		revision, err := repository.Head(ctx, root)
		if err != nil {
			return result, result.gitStage(ctx, UnresolvedGit, root, "before its Candidate was resolved", err, false)
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
	if err := repository.PruneWorktrees(ctx, root); err != nil {
		return result, result.gitStage(ctx, UnresolvedGit, root, "before its checkout was made", err, false)
	}
	if err := repository.AddWorktree(ctx, root, worktree, candidate.Revision); err != nil {
		// The checkout is where a blocking post-checkout hook lives, so this is
		// the call most likely to be the one still running when a stop arrives.
		// The worktree is deliberately left in place: removing a checkout whose
		// processes have not been confirmed gone removes it out from under them,
		// and it is also what a recovery record points at.
		return result, result.gitStage(ctx, UnresolvedGit, worktree, "while making its checkout", err, false)
	}
	runtime, err := repository.ResolveRuntimeInContext(ctx, worktree)
	if err != nil {
		// The unconfirmed group is asked about before the cancellation is. Both
		// can be true at once, and the ordering used to be the other way round, so
		// a stop that coincided with a group nobody could confirm empty reported
		// only the stop — and the group vanished from the record.
		if unsettledPart(err) == nil {
			// Nothing of ours is still running in there, so the checkout can go —
			// unless removing it turns out to leave something behind, which is a
			// fact of its own and not a tidy-up detail to drop on the floor.
			// Only the unconfirmed part is carried back. An ordinary removal
			// failure is deliberately dropped here: recording it would give the
			// Runner a pending with no group id, and ADR-0022 has no way to resolve
			// one of those short of a person editing the record.
			result.note(UnresolvedGit, worktree, unsettledPart(repository.RemoveWorktree(cleanup.context(), root, worktree)))
		}
		// A cancelled resolution is not the repository failing to declare a usable
		// runtime, so it must not be reported as a refusal: that would record a
		// condition outside the code where there is only a stop signal. Nor is a
		// group nobody could confirm empty, which is why that is asked first.
		return result, result.gitStage(ctx, UnresolvedRuntimePreflight, worktree, "while resolving its runtime", err, true)
	}
	defer func() {
		// Held back for the same reason the worktree below is: the shim directory
		// is first on the PATH of the check that was running, so removing it while
		// that process may still be alive pulls the runtime out from under it.
		// Deferred functions run last-in-first-out, so without this the worktree
		// guard would decline to delete the checkout and this would then delete
		// the runtime inside it.
		if result.Cleanup != nil {
			fmt.Fprintf(output, "warning: the resolved runtime environment was left in place: its processes could not be confirmed stopped\n")
			return
		}
		if closeErr := runtime.Close(); closeErr != nil {
			fmt.Fprintf(output, "warning: could not remove resolved runtime environment: %v\n", closeErr)
		}
	}()
	if err := repository.EnsureCanonicalCheckInContext(ctx, worktree, runtime); err != nil {
		if unsettledPart(err) == nil {
			// Only the unconfirmed part, for the reason given above the same call
			// in the runtime-preflight branch.
			result.note(UnresolvedGit, worktree, unsettledPart(repository.RemoveWorktree(cleanup.context(), root, worktree)))
		}
		return result, result.gitStage(ctx, UnresolvedCanonicalPreflight, worktree, "during its preflight", err, true)
	}

	// Cleanup runs last and cannot veto a result: once the canonical check has
	// produced an outcome, failing to tidy up must not discard it.
	defer func() {
		// Not removed while something may still be running in it. A worktree is
		// also the location a recovery record points at, so deleting it before the
		// group is confirmed gone destroys the information needed to recover.
		if result.Cleanup != nil {
			fmt.Fprintf(output, "warning: %s was left in place: its processes could not be confirmed stopped\n", worktree)
			return
		}
		removeErr := repository.RemoveWorktree(cleanup.context(), root, worktree)
		if removeErr == nil {
			return
		}
		// An ordinary tidy-up failure stays a warning. One that could not confirm
		// the processes it stopped does not: this is the last thing that runs, so
		// a warning here is the whole of what the caller would ever learn, and the
		// caller is the layer that has to refuse to start the next step.
		if unsettled := unsettledPart(removeErr); unsettled != nil {
			result.note(UnresolvedGit, worktree, unsettled)
			fmt.Fprintf(output, "warning: %s was left as it is: removing it could not confirm the processes it stopped: %v\n", worktree, removeErr)
			return
		}
		fmt.Fprintf(output, "warning: could not remove %s: %v\n", worktree, removeErr)
	}()

	// The log is opened, and its path printed, before anything about the run is
	// recorded: a log that cannot be created must abort the command before any
	// state is written or Evidence appended, not degrade into a run with no log.
	log, logFile, err := openVerificationLog(root, verificationRunID, candidate.Revision, startedAt)
	if err != nil {
		return result, err
	}
	keepLog := false
	defer func() {
		if !keepLog {
			returnErr = errors.Join(returnErr, log.Close(), os.Remove(logFile))
			return
		}
		_ = log.Close()
	}()
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
	if err := verifyExecutionBinding(root, options); err != nil {
		return result, err
	}

	plan, err := beginRun(id, root, candidate, worktree, logFile, runtime.Versions(), verificationRunID, startedAt)
	if err != nil {
		return result, err
	}
	keepLog = true
	result.VerificationRunID = verificationRunID
	// Taken after beginRun, this command's own last write before the check
	// starts, so only what happens while the check runs is in the window.
	verdictBefore, err := verdictFingerprint(root, id)
	if err != nil {
		return result, err
	}
	exitCode, cleanupErr, runErr := runCanonicalCheck(ctx, worktree, runtime, log, options.Timeout)
	result.note(UnresolvedCanonicalCheck, worktree, cleanupErr)
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
		result.EvidenceSet = []work.Evidence{interrupted}
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
	var evidenceSet []work.Evidence
	var skipped []work.FanoutSkip
	var status work.Status
	var factsErr error
	updateErr := storage.Update(root, func(state *work.State) error {
		if err := ensureVerificationVerdictUnchanged(state, id, verdictBefore); err != nil {
			return err
		}
		var recordErr error
		repositoryState, err := CandidateFacts(ctx, state, root)
		factsErr = err
		if exitCode == 0 {
			if err != nil {
				evidenceSet, skipped, recordErr = state.RecordVerificationFanoutPass(plan, repository.CanonicalCommand, nil, options.now())
			} else {
				evidenceSet, skipped, recordErr = state.RecordVerificationFanoutPass(plan, repository.CanonicalCommand, &repositoryState, options.now())
			}
			if recordErr == nil {
				evidence = evidenceSet[0]
			}
		} else if err != nil {
			evidence, recordErr = state.RecordVerification(id, candidate.Revision, repository.CanonicalCommand, exitCode, options.now())
		} else {
			evidence, recordErr = state.RecordVerificationWithRepository(id, candidate.Revision, repository.CanonicalCommand, exitCode, repositoryState, options.now())
		}
		if exitCode != 0 && recordErr == nil {
			evidenceSet = []work.Evidence{evidence}
		}
		if recordErr == nil {
			status = state.WorkItemStatus(id)
		}
		return recordErr
	})
	// Asked before the transaction's own verdict, and on both of its paths.
	// Whether Evidence was saved and whether something is still running in this
	// workspace are two different facts, and only one of them is about storage:
	// the group was observed while the callback read repository facts, so it
	// exists whether or not the write that followed landed. Returning the
	// transaction error first is how it used to disappear — the Runner then saw
	// a result with no Cleanup, cleared the pending it had recorded before the
	// verification started, and the next process found a workspace that looked
	// clear. Recorded exactly once, here, at the same location CandidateFacts
	// actually ran in. See docs/adr/0022-pending-cleanup-outlives-the-process.md.
	if unsettled := unsettledPart(factsErr); unsettled != nil {
		result.note(UnresolvedGit, root, unsettled)
	}
	if updateErr != nil {
		// Nothing below may run: the Evidence the callback built exists only in
		// memory, and claiming a transaction that failed produced a result would
		// invent exactly the kind of outcome a refusal is careful not to. The
		// operational error travels unwrapped so errors.Is and errors.As still
		// recognise it; what was added above travels with it on the result, where
		// the caller looks for it — and where the deferred cleanup above reads it,
		// so the checkout is kept rather than tidied away.
		return result, updateErr
	}
	result.Evidence, result.EvidenceSet, result.Skipped, result.HasEvidence, result.Status = evidence, evidenceSet, skipped, true, status
	if err := verifyExecutionBinding(root, options); err != nil {
		return result, err
	}
	// Failing to refresh dependency readiness is a warning: the Evidence stands
	// and a rerun repairs it. Failing to confirm a process group started while
	// reading those facts is not — the next step must not begin, whatever the
	// Evidence says, which is what the note above already recorded.
	result.RefreshWarning = factsErr
	// Non-PASS output no longer floods stdout: the log just printed above is
	// where it lives now. See docs/adr/0012-verification-log-outside-state.md.
	if _, err = fmt.Fprintf(output, "%s %s at %s\n%s %s\n", evidence.ID, evidence.Result, evidence.Revision, id, status); err != nil {
		return result, err
	}
	for _, peer := range evidenceSet[1:] {
		if _, err = fmt.Fprintf(output, "%s %s at %s\n%s %s (shared %s)\n", peer.ID, peer.Result, peer.Revision, peer.WorkItemID, statusOf(root, peer.WorkItemID), verificationRunID); err != nil {
			return result, err
		}
	}
	for _, skip := range skipped {
		if _, err = fmt.Fprintf(output, "%s skipped from %s: %s\n", skip.WorkItemID, verificationRunID, skip.Reason); err != nil {
			return result, err
		}
	}
	if factsErr != nil {
		_, err = fmt.Fprintf(output, "warning: dependency readiness was not refreshed: %v; Evidence was preserved; repair repository access and rerun %s\n", factsErr, VerificationRetryCommand(id, candidate))
	}
	return result, err
}

func verifyExecutionBinding(root string, options VerifyOptions) error {
	if options.ExecutionGoalID == "" && options.ExecutionAuthorizationDigest == "" {
		return nil
	}
	if options.ExecutionGoalID == "" || options.ExecutionAuthorizationDigest == "" {
		return fmt.Errorf("%w: incomplete Runner execution binding", ErrExecutionBindingDrift)
	}
	if err := ValidateCurrentExecutionBindings(root, options.ExecutionGoalID, options.ExecutionAuthorizationDigest); err != nil {
		return fmt.Errorf("%w: %v", ErrExecutionBindingDrift, err)
	}
	return nil
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
		ID               string                `json:"id"`
		Repository       string                `json:"repository"`
		ReviewPolicy     work.ReviewPolicy     `json:"review_policy"`
		CompletionPolicy work.CompletionPolicy `json:"completion_policy"`
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
				verdict.Goal = &goalInputs{ID: goal.ID, Repository: goal.Repository, ReviewPolicy: goal.ReviewPolicy, CompletionPolicy: goal.CompletionPolicy}
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
func reclaimOrphan(cleanup *cleanupWindow, result *VerifyResult, id, root string, output io.Writer, now time.Time) (*work.Evidence, error) {
	reclaimed, abandoned, logFile, found, err := reclaimRun(id, root, now, "")
	if err != nil || !found {
		return nil, err
	}
	// The abandoned run's own worktree, taken from state rather than recomputed:
	// state holds where that run actually ran, which survives changes to the
	// naming scheme or the layout.
	if abandoned != "" {
		// Known boundary, and deliberately unchanged. An orphan is reached with
		// this Work Item's verification lock held, so no live runner can exist —
		// but a Runner that was SIGKILLed mid-check can have left a process group
		// in this very checkout, and that group is recorded in a run record, which
		// internal/app may not read: the Runner is the layer that owns execution
		// history. The Runner therefore refuses to reach here at all while an
		// unresolved pending execution exists (see settleWorkspace), so the
		// exposure is a standalone `forgepilot verify` run by hand while a Runner's
		// pending cleanup is outstanding. Closing that would mean making the
		// standalone command consult run records and refuse, which is a change to
		// its contract that ADR-0004 and ticket 07 both rule out.
		// The cleanup window opens here rather than at the top of the command: an
		// orphan is the only thing above that needs it, and drawing the allowance
		// when there is no orphan spends the whole of it on the verification that
		// follows.
		// Only the unconfirmed part: an ordinary failure to clear an abandoned
		// checkout is not a reason to block this workspace until a person edits a
		// record, which a pending with no group id would be.
		result.note(UnresolvedGit, abandoned, unsettledPart(repository.RemoveWorktree(cleanup.context(), root, abandoned)))
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
func beginRun(id, root string, candidate work.Candidate, worktree, logFile string, runtime map[string]string, verificationRunID string, startedAt time.Time) (work.FanoutPlan, error) {
	var plan work.FanoutPlan
	err := storage.Update(root, func(state *work.State) error {
		var err error
		plan, err = state.BeginVerificationFanout(id, candidate, worktree, logFile, runtime, verificationRunID, startedAt)
		return err
	})
	return plan, err
}

// WorktreePath names the isolated checkout one Verification Run uses.
func WorktreePath(root, id, revision string) string {
	return filepath.Join(root, ".forgepilot", "worktrees", fmt.Sprintf("%s-%s", id, shortRevision(revision)))
}

// logPath names a Verification Run's output file by the run itself, not by the
// Evidence it will eventually produce: the Evidence ID is only assigned in the
// transaction that closes the run out, so it does not exist yet when the log
// must be opened. The unpredictable attempt token plus exclusive creation keeps
// collisions from truncating another execution's output. See ADR-0027.
func logPath(root, verificationRunID, revision string, startedAt time.Time, attempt string) string {
	return filepath.Join(root, ".forgepilot", "logs", fmt.Sprintf("%s-%s-%s-%s.log", verificationRunID, shortRevision(revision), startedAt.Format("20060102T150405.000000000Z"), attempt))
}

func openVerificationLog(root, verificationRunID, revision string, startedAt time.Time) (*os.File, string, error) {
	for attempt := 0; attempt < 1000; attempt++ {
		var token [8]byte
		if _, err := rand.Read(token[:]); err != nil {
			return nil, "", fmt.Errorf("generate verification log suffix: %w", err)
		}
		path := logPath(root, verificationRunID, revision, startedAt, fmt.Sprintf("%x", token[:]))
		log, err := repository.OpenExclusiveLog(path)
		if errors.Is(err, repository.ErrVerificationLogCollision) {
			continue
		}
		return log, path, err
	}
	return nil, "", fmt.Errorf("could not allocate a unique log for %s", verificationRunID)
}

// gitStage classifies a failed Git stage in the one order this command must
// always use: the process group nobody could confirm empty first, then the
// cancellation, then the plain failure. Both facts can be true at once, and
// asking about the cancellation first is how the group used to disappear from
// the result. `during` names the stage for the message; `refusable` says
// whether an ordinary failure here is a condition outside the code — a dirty
// worktree is, a Git command that could not run is not.
// See docs/adr/0022-pending-cleanup-outlives-the-process.md.
func (result *VerifyResult) gitStage(ctx context.Context, kind, location, during string, err error, refusable bool) error {
	// Classified from this stage's own error, never from the accumulated
	// result.Cleanup. A tidy-up that ran between the failure and here can have
	// added an unconfirmed group of its own, and reading that as "this stage was
	// unconfirmed" turned an honest refusal — an unsatisfiable Runtime Contract,
	// a dirty worktree — into a bare operational error. Whether the caller may
	// continue is a separate question it asks result.Cleanup itself.
	unsettled := unsettledPart(err)
	result.note(kind, location, unsettled)
	if unsettled != nil {
		return err
	}
	if ctx.Err() != nil {
		return fmt.Errorf("%w %s: %w", ErrVerificationInterrupted, during, err)
	}
	if refusable {
		return refuse(err)
	}
	return err
}

// unsettledPart pulls the unconfirmed-group report out of an error that may be
// carrying several things at once. A cancelled Git command whose child could not
// be confirmed gone reports both, and only one of them is a reason to refuse to
// continue.
func unsettledPart(err error) error {
	if err == nil {
		return nil
	}
	var unsettled *process.NotSettled
	if errors.As(err, &unsettled) {
		return unsettled
	}
	if errors.Is(err, process.ErrNotSettled) {
		return err
	}
	return nil
}
