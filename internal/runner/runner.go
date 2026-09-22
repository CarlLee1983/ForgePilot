package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/agent"
	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/process"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// Options configure one run. Root is the ForgePilot state root, which is also
// the workspace: this version drives one workspace, one Runner, one Goal.
type Options struct {
	Root           string
	GoalID         string
	RuntimeName    string
	RuntimeCommand string
	Snapshot       bool
	Budget         Budget
	Limits         storage.ArtifactLimits
	// Output receives progress, and the verification command's own presentation
	// passes through to it as well.
	Output io.Writer
	// Now is the clock. Tests supply one so a deadline is reachable without
	// waiting for it.
	Now func() time.Time
	// Stop is closed on SIGINT or SIGTERM.
	Stop <-chan struct{}
	// Signalled reports which of the two closed Stop. Both end the run the same
	// way; the difference is the exit code a shell or supervisor reads, so the
	// record has to carry it too. Nil means an interrupt.
	Signalled func() StopReason
	// Excerpt bounds how much of a failed verification log is quoted into the
	// next handoff.
	Excerpt int
	// The following instance-scoped seams are only used by package tests to
	// exercise charged Runner paths before FP-58 supplies runtime identity and
	// to terminate a helper process at a durable crash boundary.
	testExecutionIdentity *app.RunnerIdentity
	testCrashAt           func(string)
	testNewRunner         func(Options) (*Runner, error)
}

// DefaultExcerpt bounds the failure quotation in a handoff.
const DefaultExcerpt = 8 * 1024

// noProgressStrikes is how many consecutive iterations may leave every semantic
// fact unchanged before the run is declared stuck. One is too eager — a single
// re-read can legitimately change nothing — and an unbounded count is the loop
// this guard exists to stop.
const noProgressStrikes = 3

// Runner executes one Goal. It owns no rules: it asks, acts through existing
// transitions, and writes down what it did.
type Runner struct {
	options Options
	runtime agent.Runtime
	record  *Record
	// saveRecord is an instance-scoped fault-injection seam for crash-boundary
	// recovery tests. Production runners always persist through Record.save.
	saveRecord func() error
}

func (runner *Runner) now() time.Time {
	if runner.options.Now != nil {
		return runner.options.Now().UTC()
	}
	return time.Now().UTC()
}

// stopSignal names the signal that closed the Stop channel.
func (runner *Runner) stopSignal() StopReason {
	if runner.options.Signalled != nil {
		if reason := runner.options.Signalled(); reason != "" {
			return reason
		}
	}
	return StopInterrupted
}

func (runner *Runner) print(format string, arguments ...any) {
	if runner.options.Output != nil {
		fmt.Fprintf(runner.options.Output, format, arguments...)
	}
}

// Plan is what --dry-run reports: everything checked, nothing done.
type Plan struct {
	Goal              work.Goal
	WorkItemCount     int
	RuntimeName       string
	RuntimeExecutable string
	RuntimeVersion    string
	Budget            Budget
	Decision          app.Decision
	Scope             []string
}

// DryRun validates the request and reports the first action it would take. It
// reads, resolves the runtime, and stops: no start, no reconcile, no
// verification, no snapshot, no session, no run record. Its whole value is that
// it exercises the real decision path without writing anything.
func DryRun(options Options) (Plan, error) {
	if err := validateNonArtifactBudget(options.Budget); err != nil {
		return Plan{}, err
	}
	if !options.Snapshot {
		return Plan{}, errSnapshotRequired
	}
	goal, err := app.RunnableGoal(options.Root, options.GoalID)
	if err != nil {
		return Plan{}, err
	}
	options, err = effectiveNewRunOptions(goal, options)
	if err != nil {
		return Plan{}, err
	}
	if report, err := app.ReviewGoalStoryReadiness(context.Background(), options.Root, options.GoalID); err != nil {
		return Plan{}, err
	} else if len(report.Defects) > 0 {
		return Plan{}, errors.New(report.String())
	}
	runtime, err := agent.Resolve(options.RuntimeName, options.RuntimeCommand)
	if err != nil {
		return Plan{}, err
	}
	executable, err := runtime.Executable()
	if err != nil {
		return Plan{}, err
	}
	version, err := runtime.Version()
	if err != nil {
		return Plan{}, err
	}
	decision, err := app.GoalDecision(context.Background(), options.Root, options.GoalID)
	if err != nil {
		return Plan{}, err
	}
	scope, err := app.CurrentGoalScope(options.Root, options.GoalID)
	if err != nil {
		return Plan{}, err
	}
	workItemCount, err := app.GoalWorkItemCount(options.Root, options.GoalID)
	if err != nil {
		return Plan{}, err
	}
	return Plan{Goal: goal, RuntimeName: runtime.Name(), RuntimeExecutable: executable,
		RuntimeVersion: version, WorkItemCount: workItemCount, Budget: options.Budget, Decision: decision, Scope: scope}, nil
}

var errSnapshotRequired = errors.New("--snapshot is required: this version verifies a working-tree snapshot and never commits for you")

func validateNonArtifactBudget(budget Budget) error {
	budget.MaxHandoffBytes = 1
	return budget.Validate()
}

// effectiveNewRunOptions derives a fresh charged run's artifact ceilings from
// the authorization. Legacy runs retain the caller-supplied artifact contract.
func effectiveNewRunOptions(goal work.Goal, options Options) (Options, error) {
	if goal.Execution == nil {
		if err := options.Budget.Validate(); err != nil {
			return options, err
		}
		if err := options.Limits.Validate(); err != nil {
			return options, err
		}
		return options, nil
	}
	if len(goal.Execution.Authorizations) == 0 {
		return options, fmt.Errorf("goal %q has inconsistent execution authorization state", goal.ID)
	}
	artifacts := goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1].Caps.Artifacts
	options.Budget.MaxHandoffBytes = artifacts.MaxHandoffBytes
	options.Limits = storage.ArtifactLimits{
		MaxWriteBytes: int64(artifacts.MaxWriteBytes),
		MaxRunBytes:   int64(artifacts.MaxRunBytes),
		MaxTotalBytes: int64(artifacts.MaxTotalBytes),
	}
	if err := options.Budget.Validate(); err != nil {
		return options, fmt.Errorf("execution authorization has invalid artifact caps: %w", err)
	}
	if err := options.Limits.Validate(); err != nil {
		return options, fmt.Errorf("execution authorization has invalid artifact caps: %w", err)
	}
	return options, nil
}

// Start begins a new run. It takes the workspace Runner lock for the whole
// execution, so a second Runner — including one reaching the same repository
// through a symlinked path — is refused rather than allowed to overlap.
func Start(options Options) (Record, error) {
	return startWithWorkspaceLock(options, false)
}

// startWithWorkspaceLock runs Start's admission and execution body either with
// the workspace lock acquired by this helper or while a caller-owned lock is
// already held. The latter is used by authorization-level continuation so its
// exact-versus-successor decision and the resulting admission share one
// linearization point.
func startWithWorkspaceLock(options Options, lockHeld bool) (Record, error) {
	if err := validateNonArtifactBudget(options.Budget); err != nil {
		return Record{}, err
	}
	if !options.Snapshot {
		return Record{}, errSnapshotRequired
	}
	var record Record
	makeRunner := newRunner
	if options.testNewRunner != nil {
		makeRunner = options.testNewRunner
	}
	operation := func() error {
		// Validate the effective artifact contract before recovery can inspect or
		// rewrite another Run Record. newRunner repeats this immediately before
		// persistence so a concurrent authorization revision cannot be bypassed.
		goal, err := app.RunnableGoal(options.Root, options.GoalID)
		if err != nil {
			return err
		}
		effectiveOptions, err := effectiveNewRunOptions(goal, options)
		if err != nil {
			return err
		}
		if pause, err := pauseFor(options.Root, options.GoalID, ""); err != nil {
			return err
		} else if pause != nil {
			at := time.Now().UTC()
			if effectiveOptions.Now != nil {
				at = effectiveOptions.Now().UTC()
			}
			record = Record{GoalID: options.GoalID, Stop: &Stop{Reason: StopUserPaused,
				Detail: fmt.Sprintf("paused by %s: %s", pause.RequestedBy, pause.Reason), At: at}}
			return nil
		}
		// The workspace lock proves no Runner is live. It proves nothing about the
		// workers a dead Runner launched: a SIGKILLed Runner releases its lock
		// while its coding CLI keeps writing this tree. Settling those before
		// anything new starts is what keeps `run` from doing the one thing
		// ADR-0020 promises it will not — create overlapping writers.
		blocked, err := settleWorkspace(effectiveOptions, "")
		if err != nil {
			return err
		}
		if blocked != nil {
			record = *blocked
			return nil
		}
		if report, err := app.ReviewGoalStoryReadiness(context.Background(), options.Root, options.GoalID); err != nil {
			return err
		} else if len(report.Defects) > 0 {
			return errors.New(report.String())
		}
		runner, err := makeRunner(effectiveOptions)
		if err != nil {
			return err
		}
		defer func() { record = *runner.record }()
		for {
			if err := runner.loop(); err != nil {
				return err
			}
			eligible, err := runner.automaticRolloverEligible()
			if err != nil {
				return err
			}
			if !eligible {
				return nil
			}
			nextRunner, err := makeRunner(effectiveOptions)
			if err != nil {
				return err
			}
			runner = nextRunner
		}
	}
	var err error
	if lockHeld {
		err = operation()
	} else {
		err = storage.WithWorkspaceLock(options.Root, operation)
	}
	return record, err
}

func (runner *Runner) automaticRolloverEligible() (bool, error) {
	if runner.record.Stop == nil || (runner.record.Stop.Reason != StopMaxSteps && runner.record.Stop.Reason != StopMaxDuration) ||
		runner.record.Worker != nil || len(runner.record.UnresolvedPending()) != 0 {
		return false, nil
	}
	return app.AutomaticRolloverAllowed(runner.options.Root, runner.record.GoalID,
		runner.record.ExecutionAuthorizationDigest, runner.now())
}

// settleWorkspace applies the recovery judgement to every earlier run on this
// workspace. It returns a record carrying a RECOVERY_BLOCKED stop when any of
// them cannot be settled, because starting a second writer on a workspace that
// may still have one is not a risk worth taking for the convenience of not
// having to name a run id. `except` names the run the caller settles itself: a
// resume recovers its own record through the Runner that is about to drive it,
// and settling it twice would judge it without one.
//
// It is the one criterion Start and Resume share, and it reads the workspace
// rather than the command line: a different Goal, a new run id or a restarted
// CLI are all the same workspace, and none of them may walk past an execution
// nobody could confirm stopped. It also no longer skips a record whose Worker
// is nil — a canonical check, a runtime probe or a Git child leaves no worker,
// which is exactly how those used to pass unnoticed.
// See docs/adr/0022-pending-cleanup-outlives-the-process.md.
func settleWorkspace(options Options, except string) (*Record, error) {
	runs, err := storage.ListRuns(options.Root)
	if err != nil {
		return nil, err
	}
	for _, runID := range runs {
		if runID == except {
			continue
		}
		existing, err := LoadRecord(options.Root, runID)
		if err != nil {
			// An unreadable record may describe a live worker. Refusing is the only
			// answer that cannot be wrong. A record that does not parse is not a
			// record with nothing pending in it.
			return blockedRecord(options, runID, fmt.Sprintf("run %s has an unreadable record: %v", runID, err)), nil
		}
		if existing.Worker == nil && len(existing.UnresolvedPending()) == 0 {
			continue
		}
		runner := &Runner{options: options, record: &existing}
		// The verdict is this pass's own, not whatever stop the record was
		// carrying: a run that ended RECOVERY_BLOCKED and has since become
		// confirmable is settled here, in this invocation, rather than refused
		// once more and settled only on a second attempt by hand.
		blocked, err := runner.recover()
		if err != nil {
			return nil, err
		}
		if blocked {
			return &existing, nil
		}
	}
	return nil, nil
}

func blockedRecord(options Options, runID, detail string) *Record {
	at := time.Now().UTC()
	if options.Now != nil {
		at = options.Now().UTC()
	}
	if options.Output != nil {
		fmt.Fprintf(options.Output, "Stopped: %s — %s\n", StopRecoveryBlocked, detail)
	}
	// No GoalID: this record stands for someone else's run, and the goal this
	// run wanted is not the goal that one was driving. Naming the wrong one sends
	// whoever has to sort it out to the wrong place; the detail names the run.
	return &Record{RunID: runID,
		Stop: &Stop{Reason: StopRecoveryBlocked, Detail: detail, At: at}}
}

// Resume continues a stopped run. It keeps the original workspace, Goal,
// runtime and consumed budget: a resume that reset either would turn every
// limit into a suggestion.
func Resume(options Options, runID string) (Record, error) {
	return resumeWithWorkspaceLock(options, runID, false)
}

// resumeWithWorkspaceLock is the lock-aware body of Resume. Authorization
// continuation uses it while holding the same workspace lock in which it
// rechecked the anchor, preventing a stale exact-resume decision.
func resumeWithWorkspaceLock(options Options, runID string, lockHeld bool) (Record, error) {
	var record Record
	operation := func() error {
		existing, err := LoadRecord(options.Root, runID)
		if err != nil {
			return err
		}
		root, err := filepath.EvalSymlinks(options.Root)
		if err != nil {
			return err
		}
		if existing.Workspace != root {
			return fmt.Errorf("run %s belongs to workspace %s, not %s", runID, existing.Workspace, root)
		}
		// A record is a file on disk, so its budget is an input like any other.
		// An agent timeout of zero read back from a hand-edited record would mean
		// a session that never times out.
		if err := existing.Budget.Validate(); err != nil {
			return fmt.Errorf("run %s has an invalid budget: %w", runID, err)
		}
		if err := existing.Limits.Validate(); err != nil {
			return fmt.Errorf("run %s has invalid artifact limits: %w", runID, err)
		}
		// A resume launches sessions on this workspace exactly as a fresh run
		// does, so it clears the same bar: a worker another run left behind is
		// still a writer here, and the workspace lock says nothing about it.
		// This run's own worker is left to recover() below.
		runner := &Runner{options: resumeOptions(options, existing), record: &existing}
		defer func() { record = *runner.record }()
		if stopped, err := runner.honorPause(); stopped || err != nil {
			return err
		}
		blocked, err := settleWorkspace(runner.options, runID)
		if err != nil {
			return err
		}
		if blocked != nil {
			record = *blocked
			return nil
		}
		if existing.RunPreparationState != "" {
			if err := validatePendingRunPreparationRecord(existing); err != nil {
				return err
			}
			currentState, err := storage.Load(options.Root)
			if err != nil {
				return err
			}
			currentGoal, ok := currentState.GoalByID(existing.GoalID)
			if !ok {
				return fmt.Errorf("run %s refers to an unknown Goal %q", runID, existing.GoalID)
			}
			if existing.RunPreparationState == RunPreparationPendingClassification && currentGoal.Execution != nil {
				return fmt.Errorf("run %s execution authorization changed after the Run Record intent was saved; refuse stale legacy recovery", runID)
			}
			authorizationChanged := existing.ExecutionAuthorizationDigest != "" && currentGoal.Execution != nil &&
				len(currentGoal.Execution.Authorizations) > 0 && currentGoal.Execution.Authorizations[len(currentGoal.Execution.Authorizations)-1].Digest != existing.ExecutionAuthorizationDigest
			if !authorizationChanged && existing.ExecutionAuthorizationDigest != "" {
				if err := app.ValidateRunnerArtifactLimits(options.Root, existing.GoalID, existing.ExecutionAuthorizationDigest,
					work.ExecutionArtifactLimits{MaxHandoffBytes: existing.Budget.MaxHandoffBytes,
						MaxWriteBytes: int(existing.Limits.MaxWriteBytes), MaxRunBytes: int(existing.Limits.MaxRunBytes),
						MaxTotalBytes: int(existing.Limits.MaxTotalBytes)}); err != nil {
					return err
				}
			}
			runner.runtime, err = agent.Resolve(existing.RuntimeName, existing.RuntimeCommand)
			if err != nil {
				return err
			}
			executable, err := runner.runtime.Executable()
			if err != nil {
				return err
			}
			version, err := runner.runtime.Version()
			if err != nil {
				return err
			}
			if runner.runtime.Name() != existing.RuntimeName || executable != existing.RuntimeExecutable || version != existing.RuntimeVersion {
				return fmt.Errorf("run %s preparation runtime no longer matches its durable intent", runID)
			}
			// The intent's authorization digest is the authority observed when
			// this exact Run was written. An empty digest records that it was
			// classified as legacy; recovery must not adopt an authorization added
			// after the intent was persisted.
			expectedDigest := existing.ExecutionAuthorizationDigest
			identity := runner.identityWithTestFacts(app.RunnerIdentity{Runtime: runner.runtime.Name(), ExecutablePath: executable,
				Version: version, Sandbox: string(runner.runtime.SessionEnvironment().Sandbox)})
			if err := runner.finishPendingRunPreparation(identity, expectedDigest); err != nil {
				return err
			}
			existing = *runner.record
			if authorizationChanged {
				runner.record.Stop = &Stop{Reason: StopScopeChanged, Detail: "execution authorization changed while completing prior run preparation", At: runner.now()}
				return runner.save()
			}
		}
		var reservationSnapshot app.RunnerReservationSnapshot
		if existing.ExecutionAuthorizationDigest != "" {
			if err := reconcileRecordedNeedsHumanDispositions(options.Root, &existing); err != nil {
				return err
			}
			reservationSnapshot, err = app.ReconcileRunnerReservationReceipts(options.Root, existing.GoalID,
				existing.RunID, existing.RunReservationID, existing.ReservationReceipts)
			if err != nil {
				return err
			}
			changed := !reflect.DeepEqual(existing.ReservationReceipts, reservationSnapshot.Receipts)
			existing.ReservationReceipts = reservationSnapshot.Receipts
			ownershipChanged, err := reconcileChargedWorkerReceipts(&existing, reservationSnapshot)
			if err != nil {
				return err
			}
			humanWaitsChanged, err := reconcileHumanWaitReservations(&existing, reservationSnapshot)
			if err != nil {
				return err
			}
			if changed || ownershipChanged || humanWaitsChanged {
				if err := runner.save(); err != nil {
					return err
				}
			}
		}
		// Keep the previous stop while cleanup and admission are checked. Recovery
		// may need to save process ownership changes before a charged resume can be
		// admitted; that save must not silently clear the run's durable stop.
		if blocked, err := runner.recover(); blocked || err != nil {
			return err
		}
		// A completion transaction may have committed before the previous process
		// could save its terminal run record. State is authoritative in that case:
		// repair the record before readiness preflight, runtime resolution, or any
		// budget check that assumes the Goal is still active.
		if completed, err := runner.repairCompletedGoal(); completed || err != nil {
			return err
		}
		if existing.ExecutionAuthorizationDigest == "" || existing.RunReservationID != existing.RunID+":run" {
			return fmt.Errorf("run %s has no current execution authorization", existing.RunID)
		}
		// Exact resume is not a new run and cannot spend a replacement run unit.
		// Recovery and already-committed completion are deliberately handled first:
		// neither launches a worker, and both must stay recoverable even if an old
		// runtime executable has since disappeared.
		if runner.runtime == nil {
			runner.runtime, err = agent.Resolve(existing.RuntimeName, existing.RuntimeCommand)
			if err != nil {
				return err
			}
		}
		executable, err := runner.runtime.Executable()
		if err != nil {
			return err
		}
		version, err := runner.runtime.Version()
		if err != nil {
			return err
		}
		_, err = app.ValidateRunnerResume(options.Root, existing.GoalID, existing.RunID,
			existing.ExecutionAuthorizationDigest, existing.RunReservationID, existing.ReservationReceipts,
			work.ExecutionArtifactLimits{MaxHandoffBytes: existing.Budget.MaxHandoffBytes,
				MaxWriteBytes: int(existing.Limits.MaxWriteBytes), MaxRunBytes: int(existing.Limits.MaxRunBytes),
				MaxTotalBytes: int(existing.Limits.MaxTotalBytes)},
			runner.identityWithTestFacts(app.RunnerIdentity{Runtime: runner.runtime.Name(), ExecutablePath: executable, Version: version,
				Sandbox: string(runner.runtime.SessionEnvironment().Sandbox)}), runner.now())
		if err != nil {
			return err
		}
		// Only an admitted exact resume consumes the prior stop. Keep it durable
		// if authorization or accounting validation above rejects the request.
		if runner.record.Stop != nil {
			runner.record.Stop = nil
			if err := runner.save(); err != nil {
				return err
			}
		}
		if stopped, err := runner.checkReadiness(); stopped || err != nil {
			return err
		}
		return runner.loop()
	}
	var err error
	if lockHeld {
		err = operation()
	} else {
		err = storage.WithWorkspaceLock(options.Root, operation)
	}
	return record, err
}

// ResumeAuthorization continues from an anchor run under the Goal's current
// authorization. It preserves exact-run semantics while the anchor still has
// its original authorization and deadline. A revised authorization or an
// expired run creates a separately charged successor; the anchor's immutable
// budget, deadline, and stop history remain in its Run Record.
func ResumeAuthorization(options Options, anchorRunID string) (Record, error) {
	return resumeAuthorizationWithWorkspaceLock(options, anchorRunID, false)
}

func resumeAuthorizationWithWorkspaceLock(options Options, anchorRunID string, lockHeld bool) (Record, error) {
	var record Record
	operation := func() error {
		// Load the anchor and current authorization while holding the same
		// workspace lock that will admit the exact resume or successor. A read
		// before this lock can become stale when another Runner finishes and
		// records a bounded stop between the decision and admission.
		anchor, err := LoadRecord(options.Root, anchorRunID)
		if err != nil {
			return err
		}
		root, err := filepath.EvalSymlinks(options.Root)
		if err != nil {
			return err
		}
		if anchor.Workspace != root {
			return fmt.Errorf("run %s belongs to workspace %s, not %s", anchorRunID, anchor.Workspace, root)
		}
		if anchor.ExecutionAuthorizationDigest == "" {
			return fmt.Errorf("run %s is not bound to an execution authorization; resume that exact run", anchorRunID)
		}
		state, err := storage.Load(options.Root)
		if err != nil {
			return err
		}
		goal, ok := state.GoalByID(anchor.GoalID)
		if !ok || goal.Execution == nil || len(goal.Execution.Authorizations) == 0 {
			return fmt.Errorf("run %s has no current execution authorization", anchorRunID)
		}
		current := goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1]
		now := time.Now().UTC()
		if options.Now != nil {
			now = options.Now().UTC()
		}
		if exactAuthorizationResumeReusable(anchor, current.Digest, now) {
			record, err = resumeWithWorkspaceLock(options, anchorRunID, true)
			return err
		}
		if anchor.Stop == nil {
			return fmt.Errorf("run %s is not stopped; resume that exact run before authorizing a successor", anchorRunID)
		}

		record, err = startAuthorizationSuccessorWithLock(options, anchor, current, true)
		return err
	}
	var err error
	if lockHeld {
		err = operation()
	} else {
		err = storage.WithWorkspaceLock(options.Root, operation)
	}
	return record, err
}

// exactAuthorizationResumeReusable keeps the explicit authorization entrypoint
// distinct from exact-run resume. A run that exhausted its own bounded
// contract cannot make progress by reusing that contract, even when the Goal
// authorization digest is unchanged; authorization-level continuation must
// create a separately charged successor instead.
func exactAuthorizationResumeReusable(anchor Record, currentAuthorizationDigest string, now time.Time) bool {
	if currentAuthorizationDigest == "" || currentAuthorizationDigest != anchor.ExecutionAuthorizationDigest {
		return false
	}
	if anchor.Stop == nil {
		return now.Before(anchor.Deadline)
	}
	switch anchor.Stop.Reason {
	case StopMaxSteps, StopMaxAttempts, StopMaxDuration:
		return false
	default:
		return now.Before(anchor.Deadline)
	}
}

func startAuthorizationSuccessorWithLock(options Options, anchor Record, authorization work.ExecutionAuthorization, lockHeld bool) (Record, error) {
	successor := resumeOptions(options, anchor)
	successor.RuntimeName = authorization.WorkerProfile.Runtime
	successor.RuntimeCommand = authorization.WorkerProfile.ExecutablePath
	return startWithWorkspaceLock(successor, lockHeld)
}

// ResumeAuthorizationGoal is the public Goal-scoped continuation entrypoint.
// It selects the most recently updated charged run for this Goal, then
// delegates the exact-versus-successor decision to ResumeAuthorization. The
// latter rechecks recovery, bindings, identity, expiry, and cumulative caps at
// the real admission boundary; this selection never authorizes a new contract.
func ResumeAuthorizationGoal(options Options, goalID string) (Record, error) {
	if goalID == "" {
		return Record{}, errors.New("execution authorization resume requires a Goal ID")
	}
	var record Record
	operation := func() error {
		// Selection shares the continuation lock with the anchor decision. A
		// successor created by another Runner cannot appear after this list is
		// read and leave this call continuing an older anchor.
		root, err := filepath.EvalSymlinks(options.Root)
		if err != nil {
			return err
		}
		state, err := storage.Load(options.Root)
		if err != nil {
			return err
		}
		goal, ok := state.GoalByID(goalID)
		if !ok || goal.Execution == nil || len(goal.Execution.Authorizations) == 0 {
			return fmt.Errorf("goal %q has no current execution authorization", goalID)
		}
		runIDs, err := storage.ListRuns(options.Root)
		if err != nil {
			return err
		}
		var selected *Record
		for _, runID := range runIDs {
			candidate, err := LoadRecord(options.Root, runID)
			if err != nil {
				return err
			}
			if candidate.Workspace != root || candidate.GoalID != goalID || candidate.ExecutionAuthorizationDigest == "" {
				continue
			}
			if selected == nil || candidate.UpdatedAt.After(selected.UpdatedAt) ||
				(candidate.UpdatedAt.Equal(selected.UpdatedAt) && candidate.RunID > selected.RunID) {
				copy := candidate
				selected = &copy
			}
		}
		if selected == nil {
			return fmt.Errorf("goal %q has no authorization-bound run to continue", goalID)
		}
		record, err = resumeAuthorizationWithWorkspaceLock(options, selected.RunID, true)
		return err
	}
	err := storage.WithWorkspaceLock(options.Root, operation)
	return record, err
}

// resumeOptions takes the execution settings from the record rather than from
// the command line: a resume must not quietly switch Goal, runtime or budget.
func resumeOptions(options Options, record Record) Options {
	options.GoalID = record.GoalID
	options.RuntimeName = record.RuntimeName
	options.RuntimeCommand = record.RuntimeCommand
	options.Snapshot = record.Snapshot
	options.Budget = record.Budget
	// Limits travel with the run for the same reason the budget does: a resume
	// that silently tightened them would stop a healthy run for capacity it was
	// never actually short of.
	options.Limits = record.Limits
	return options
}

func newRunner(options Options) (*Runner, error) {
	goal, err := app.RunnableGoal(options.Root, options.GoalID)
	if err != nil {
		return nil, err
	}
	options, err = effectiveNewRunOptions(goal, options)
	if err != nil {
		return nil, err
	}
	if goal.Execution == nil || len(goal.Execution.Authorizations) == 0 {
		return nil, fmt.Errorf("goal %q has no current execution authorization", goal.ID)
	}
	runtime, err := agent.Resolve(options.RuntimeName, options.RuntimeCommand)
	if err != nil {
		return nil, err
	}
	executable, err := runtime.Executable()
	if err != nil {
		return nil, err
	}
	version, err := runtime.Version()
	if err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(options.Root)
	if err != nil {
		return nil, err
	}
	scope, err := app.CurrentGoalScope(options.Root, options.GoalID)
	if err != nil {
		return nil, err
	}
	runner := &Runner{options: options, runtime: runtime}
	identity := runner.identityWithTestFacts(app.RunnerIdentity{Runtime: runtime.Name(), ExecutablePath: executable, Version: version,
		Sandbox: string(runtime.SessionEnvironment().Sandbox)})
	authorizations := goal.Execution.Authorizations
	currentAuthorization := authorizations[len(authorizations)-1]
	if !runner.now().Before(currentAuthorization.ExpiresAt) {
		return nil, fmt.Errorf("execution authorization for goal %q has expired", goal.ID)
	}
	if err := app.ValidateCurrentExecutionBindings(options.Root, goal.ID, currentAuthorization.Digest); err != nil {
		return nil, err
	}
	if pending, err := findPendingRunPreparation(options.Root, options.GoalID); err != nil {
		return nil, err
	} else if pending != nil {
		if err := validatePendingRunPreparationRecord(*pending); err != nil {
			return nil, err
		}
		if err := validatePendingStartRequest(*pending, options, root, goal, runtime, executable, version, scope); err != nil {
			return nil, err
		}
		if pending.RunPreparationState == RunPreparationPendingClassification && goal.Execution != nil {
			return nil, fmt.Errorf("run %s execution authorization changed after the Run Record intent was saved; refuse stale legacy recovery", pending.RunID)
		}
		runner.record = pending
		expectedDigest := ""
		expectedDigest = pending.ExecutionAuthorizationDigest
		if err := runner.finishPendingRunPreparation(identity, expectedDigest); err != nil {
			return nil, err
		}
		runner.crashAt("after-run-record")
		runner.print("Run %s preparation recovered for goal %s (%s)\nRuntime: %s %s\nDeadline: %s\n",
			runner.record.RunID, goal.ID, goal.Title, runtime.Name(), version, runner.record.Deadline.Format(time.RFC3339))
		return runner, nil
	}
	if err := app.CheckRunnerRunAdmissionIntegrity(options.Root, options.GoalID); err != nil {
		return nil, err
	}
	startedAt := runner.now()
	runID, err := NewRunID(startedAt)
	if err != nil {
		return nil, err
	}
	authorizationDigest := goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1].Digest
	if authorizationDigest == "" {
		return nil, fmt.Errorf("goal %q has an execution authorization without a durable digest", goal.ID)
	}
	runReservationID := runID + ":run"
	preparationState := RunPreparationPendingCharge
	runner.record = &Record{
		RunID: runID, Workspace: root, GoalID: goal.ID, GoalTitle: goal.Title, CompletionPolicy: goal.CompletionPolicy, Scope: scope,
		RuntimeName: runtime.Name(), RuntimeExecutable: executable, RuntimeVersion: version,
		RuntimeCommand: options.RuntimeCommand, Snapshot: options.Snapshot,
		ExecutionAuthorizationDigest: authorizationDigest, RunReservationID: runReservationID,
		RunPreparationState: preparationState,
		Budget:              options.Budget, Limits: options.Limits,
		StartedAt: startedAt, Deadline: startedAt.Add(options.Budget.MaxDuration),
		Attempts: map[string]int{}, HumanWaits: map[string]int{},
	}
	if err := runner.record.save(options.Root, options.Limits, startedAt); err != nil {
		return nil, err
	}
	runner.crashAt("after-run-intent")
	if err := runner.finishPendingRunPreparation(identity, authorizationDigest); err != nil {
		return nil, err
	}
	runner.crashAt("after-run-record")
	runner.print("Run %s started for goal %s (%s)\nRuntime: %s %s\nDeadline: %s\n",
		runID, goal.ID, goal.Title, runtime.Name(), version, runner.record.Deadline.Format(time.RFC3339))
	return runner, nil
}

func findPendingRunPreparation(root, goalID string) (*Record, error) {
	runIDs, err := storage.ListRuns(root)
	if err != nil {
		return nil, err
	}
	var pending *Record
	for _, runID := range runIDs {
		record, err := LoadRecord(root, runID)
		if err != nil {
			return nil, err
		}
		if record.RunPreparationState == "" {
			continue
		}
		if err := validatePendingRunPreparationRecord(record); err != nil {
			return nil, err
		}
		if record.GoalID != goalID {
			return nil, fmt.Errorf("run %s has an unfinished preparation for Goal %s; resume that exact run first", runID, record.GoalID)
		}
		if pending != nil {
			return nil, errors.New("multiple unfinished Run preparations exist; reconcile them before starting another run")
		}
		pending = &record
	}
	return pending, nil
}

func validatePendingRunPreparationRecord(record Record) error {
	if record.RunPreparationState != RunPreparationPendingClassification && record.RunPreparationState != RunPreparationPendingCharge {
		return fmt.Errorf("run %s has an unknown Run preparation state", record.RunID)
	}
	if record.RunID == "" || record.GoalID == "" || record.Workspace == "" || record.StartedAt.IsZero() ||
		record.Deadline.IsZero() || !record.Deadline.Equal(record.StartedAt.Add(record.Budget.MaxDuration)) ||
		record.Steps != 0 || record.Worker != nil || len(record.Pending) != 0 || len(record.History) != 0 ||
		record.Stop != nil || len(record.Attempts) != 0 || len(record.HumanWaits) != 0 ||
		len(record.HumanWaitReservationIDs) != 0 || len(record.ReservationReceipts) != 0 {
		return fmt.Errorf("run %s has an incomplete or runnable Run preparation intent", record.RunID)
	}
	if err := record.Budget.Validate(); err != nil {
		return fmt.Errorf("run %s preparation has an invalid budget: %w", record.RunID, err)
	}
	if err := record.Limits.Validate(); err != nil {
		return fmt.Errorf("run %s preparation has invalid artifact limits: %w", record.RunID, err)
	}
	if record.RunPreparationState == RunPreparationPendingCharge &&
		(record.ExecutionAuthorizationDigest == "" || record.RunReservationID != record.RunID+":run") {
		return fmt.Errorf("run %s has an incomplete charged Run preparation intent", record.RunID)
	}
	if record.RunPreparationState == RunPreparationPendingClassification &&
		(record.ExecutionAuthorizationDigest != "" || record.RunReservationID != "") {
		return fmt.Errorf("run %s has inconsistent Run preparation metadata", record.RunID)
	}
	return nil
}

func validatePendingStartRequest(record Record, options Options, root string, goal work.Goal,
	runtime agent.Runtime, executable, version string, scope []string) error {
	if record.Workspace != root || record.GoalID != goal.ID || record.GoalTitle != goal.Title ||
		record.CompletionPolicy != goal.CompletionPolicy || record.RuntimeName != runtime.Name() ||
		record.RuntimeExecutable != executable || record.RuntimeVersion != version ||
		record.RuntimeCommand != options.RuntimeCommand || record.Snapshot != options.Snapshot ||
		record.Budget != options.Budget || record.Limits != options.Limits || !reflect.DeepEqual(record.Scope, scope) {
		return fmt.Errorf("run %s has an unfinished preparation; direct start must match its exact saved intent", record.RunID)
	}
	return nil
}

func (runner *Runner) finishPendingRunPreparation(identity app.RunnerIdentity, expectedDigest string) error {
	if err := validatePendingRunPreparationRecord(*runner.record); err != nil {
		return err
	}
	if runner.record.ExecutionAuthorizationDigest != "" && runner.record.ExecutionAuthorizationDigest != expectedDigest {
		return fmt.Errorf("run %s preparation refers to a different execution authorization", runner.record.RunID)
	}
	if expectedDigest == "" {
		if runner.record.RunPreparationState == RunPreparationPendingCharge || runner.record.RunReservationID != "" {
			return fmt.Errorf("run %s charged preparation has no current execution authorization", runner.record.RunID)
		}
	} else {
		runner.record.ExecutionAuthorizationDigest = expectedDigest
		runner.record.RunReservationID = runner.record.RunID + ":run"
		runner.record.RunPreparationState = RunPreparationPendingCharge
		if err := runner.save(); err != nil {
			return err
		}
	}
	admission, err := app.PrepareRunnerRunIntent(runner.options.Root, runner.record.GoalID, runner.record.RunID,
		expectedDigest, identity, runner.now())
	if err != nil {
		return err
	}
	if admission.Reservation == nil {
		if expectedDigest != "" {
			return fmt.Errorf("run %s lost its charged RUN reservation during preparation", runner.record.RunID)
		}
		runner.record.RunPreparationState = ""
		return runner.save()
	}
	if expectedDigest == "" || admission.AuthorizationDigest != expectedDigest ||
		admission.Reservation.ID != runner.record.RunID+":run" {
		return fmt.Errorf("run %s admission does not match its durable preparation intent", runner.record.RunID)
	}
	runner.crashAt("after-run-charge")
	receipt, err := work.ExecutionReservationReceiptFor(*admission.Reservation)
	if err != nil {
		return err
	}
	runner.record.ReservationReceipts = []work.ExecutionReservationReceipt{receipt}
	runner.record.RunPreparationState = ""
	return runner.save()
}

func (runner *Runner) save() error {
	if runner.saveRecord != nil {
		return runner.saveRecord()
	}
	return runner.record.save(runner.options.Root, runner.options.Limits, runner.now())
}

func (runner *Runner) runnerIdentity() (app.RunnerIdentity, error) {
	if runner.runtime == nil {
		return app.RunnerIdentity{}, errors.New("runner runtime is not resolved")
	}
	executable, err := runner.runtime.Executable()
	if err != nil {
		return app.RunnerIdentity{}, err
	}
	version, err := runner.runtime.Version()
	if err != nil {
		return app.RunnerIdentity{}, err
	}
	return runner.identityWithTestFacts(app.RunnerIdentity{Runtime: runner.runtime.Name(), ExecutablePath: executable, Version: version,
		Sandbox: string(runner.runtime.SessionEnvironment().Sandbox)}), nil
}

func (runner *Runner) identityWithTestFacts(identity app.RunnerIdentity) app.RunnerIdentity {
	if runner.options.testExecutionIdentity == nil {
		return identity
	}
	testFacts := runner.options.testExecutionIdentity
	identity.Model = testFacts.Model
	identity.Effort = testFacts.Effort
	identity.Sandbox = testFacts.Sandbox
	identity.EngineGeneration = testFacts.EngineGeneration
	return identity
}

// stopNow records the terminal reason and returns nil: a stop is an outcome, not
// an error. Callers report it through the record's Stop field.
func (runner *Runner) stopNow(reason StopReason, detail string, evidence ...string) error {
	at := runner.now()
	// A detail can carry a model's own words — a needs_human question, a summary
	// it wrote. Attempt summaries are bounded for exactly that reason and this is
	// the same text arriving by another door.
	if len(detail) > AttemptSummaryBytes {
		detail = detail[:AttemptSummaryBytes] + " …(truncated)"
	}
	runner.record.Stop = &Stop{Reason: reason, Detail: detail, At: at, EvidenceIDs: evidence}
	runner.print("Stopped: %s — %s\n", reason, detail)
	// The record is saved before the journal: the journal is diagnostic, and a
	// failure to append it must not lose the reason the run ended. A save that
	// fails is different — then there is no durable stop at all, and the caller
	// must hear about it.
	if err := runner.save(); err != nil {
		return err
	}
	return runner.record.journal(runner.options.Root, runner.options.Limits,
		Entry{At: at, Step: runner.record.Steps, Action: "STOP", Detail: string(reason) + ": " + detail})
}

// loop is the whole execution model. Nothing is cached across iterations: state,
// repository facts and the legal action are all re-read every time, because a
// Gate, a Candidate or a Goal status can move while a session runs.
func (runner *Runner) loop() error {
	strikes := 0
	previous := ""
	for {
		if runner.record.Stop != nil {
			return nil
		}
		// State is authoritative after a crash. A completion may have committed
		// before the run record was saved, and repairing that record must not be
		// blocked by an expired budget, a moved checkout, or readiness preflight.
		if completed, err := runner.repairCompletedGoal(); completed || err != nil {
			return err
		}
		if stopped, err := runner.checkLimits(); stopped || err != nil {
			return err
		}
		if stopped, err := runner.checkReadiness(); stopped || err != nil {
			return err
		}
		if stopped, err := runner.checkScope(); stopped || err != nil {
			return err
		}
		var decision app.Decision
		stopped, err := runner.readFacts("", func(ctx context.Context) error {
			var decisionErr error
			decision, decisionErr = app.GoalDecision(ctx, runner.options.Root, runner.options.GoalID)
			return decisionErr
		})
		if stopped || err != nil {
			return err
		}
		fingerprint, err := runner.fingerprint(decision)
		if err != nil {
			return err
		}
		if fingerprint == previous {
			strikes++
			if strikes >= noProgressStrikes {
				return runner.stopNow(StopNoProgress,
					fmt.Sprintf("nothing about the work, its evidence, its gates or the goal changed across %d iterations", strikes))
			}
		} else {
			strikes = 0
		}
		previous = fingerprint

		if err := runner.act(decision); err != nil {
			return err
		}
	}
}

func (runner *Runner) repairCompletedGoal() (bool, error) {
	state, err := storage.Load(runner.options.Root)
	if err != nil {
		return false, err
	}
	goal, ok := state.GoalByID(runner.options.GoalID)
	if !ok || goal.Status != work.GoalCompleted {
		return false, nil
	}
	decision, err := app.GoalDecision(context.Background(), runner.options.Root, runner.options.GoalID)
	if err != nil {
		return true, err
	}
	return true, runner.goalAlreadyCompleted(decision.Action)
}

// checkReadiness keeps readiness preflight outside lifecycle ownership while
// giving an already-recorded run a durable operational stop.
func (runner *Runner) checkReadiness() (bool, error) {
	report, err := app.ReviewGoalStoryReadiness(context.Background(), runner.options.Root, runner.options.GoalID)
	if err != nil {
		return true, err
	}
	if len(report.Defects) == 0 {
		return false, nil
	}
	return true, runner.stopNow(StopReadinessPreflight, report.String())
}

func (runner *Runner) checkLimits() (bool, error) {
	if runner.record.Steps >= runner.options.Budget.MaxSteps {
		return true, runner.stopNow(StopMaxSteps, fmt.Sprintf("%d steps is the configured maximum", runner.options.Budget.MaxSteps))
	}
	return runner.beforeAction()
}

// beforeAction is the gate every new action and every new subprocess passes. A
// check at the top of the loop is not enough on its own: an agent session can
// finish after the deadline has already gone, and starting a verification then
// would spend time the run no longer has. Signals are checked here for the same
// reason — a signal must not be the moment a new session or a new check begins.
func (runner *Runner) beforeAction() (bool, error) {
	if runner.record.Stop != nil {
		return true, nil
	}
	select {
	case <-runner.options.Stop:
		return true, runner.stopNow(runner.stopSignal(), "stopped on signal; resume this run to continue")
	default:
	}
	if stopped, err := runner.honorPause(); stopped || err != nil {
		return stopped, err
	}
	if !runner.now().Before(runner.record.Deadline) {
		return true, runner.stopNow(StopMaxDuration,
			fmt.Sprintf("the run passed its deadline of %s; resuming does not extend it", runner.record.Deadline.Format(time.RFC3339)))
	}
	if err := app.ValidateCurrentExecutionBindings(runner.options.Root, runner.record.GoalID, runner.record.ExecutionAuthorizationDigest); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, err
		}
		return true, runner.stopNow(StopScopeChanged, err.Error())
	}
	return false, nil
}

// checkScope refuses to keep driving a Goal whose Work Item set, Story
// references or dependencies changed underneath the run. The person authorized
// a particular piece of work, and silently absorbing new work is not that.
func (runner *Runner) checkScope() (bool, error) {
	state, err := storage.Load(runner.options.Root)
	if err != nil {
		return true, err
	}
	goal, ok := state.GoalByID(runner.options.GoalID)
	if !ok {
		return true, runner.stopNow(StopScopeChanged, fmt.Sprintf("goal %s no longer exists", runner.options.GoalID))
	}
	currentPolicy := goal.CompletionPolicy
	if currentPolicy == "" {
		currentPolicy = work.CompletionHuman
	}
	completionPolicy := runner.record.CompletionPolicy
	policyMigratedFromHuman := state.SchemaVersion >= work.SchemaVersion && goal.ReviewPolicy == work.ReviewPerGoal &&
		completionPolicy == work.CompletionHuman && currentPolicy == work.CompletionVerified
	if completionPolicy == "" || policyMigratedFromHuman {
		// Pre-v11 run records have no completion policy. Their state migration
		// explicitly establishes the Goal's completion boundary. Schema v12 also
		// converts GOAL/HUMAN states to VERIFIED, so adopt that one migration in
		// older run records instead of making an active run impossible to resume.
		completionPolicy = currentPolicy
		runner.record.CompletionPolicy = completionPolicy
		if err := runner.save(); err != nil {
			return true, fmt.Errorf("save migrated run completion policy: %w", err)
		}
	}
	if completionPolicy != currentPolicy {
		return true, runner.stopNow(StopScopeChanged,
			fmt.Sprintf("goal %s completion policy changed from %s to %s", runner.options.GoalID, completionPolicy, currentPolicy))
	}
	scope, err := app.CurrentGoalScope(runner.options.Root, runner.options.GoalID)
	if err != nil {
		return true, err
	}
	if reflect.DeepEqual(scope, runner.record.Scope) {
		return false, nil
	}
	return true, runner.stopNow(StopScopeChanged,
		fmt.Sprintf("goal %s no longer holds the work items, stories or dependencies this run started against", runner.options.GoalID))
}

// fingerprint is the semantic state a progress check compares. Evidence IDs,
// timestamps, attempt numbers and log sizes are deliberately absent: they all
// change while nothing moves, which is precisely the situation being detected.
func (runner *Runner) fingerprint(decision app.Decision) (string, error) {
	state, err := storage.Load(runner.options.Root)
	if err != nil {
		return "", err
	}
	var facts []string
	for _, item := range state.WorkItems {
		if item.GoalID != runner.options.GoalID {
			continue
		}
		result := "none"
		if verification, ok := state.LatestVerification(item.ID); ok {
			result = string(verification.Result)
		}
		facts = append(facts, fmt.Sprintf("%s=%s/%s/stale:%t/gates:%d", item.ID, item.Status, result,
			state.CandidateStale(item.ID, decision.Repository.Revision, decision.Repository.SnapshotDigest),
			state.OpenGateCount(item.ID)))
	}
	sort.Strings(facts)
	for _, goal := range state.Goals {
		if goal.ID == runner.options.GoalID {
			facts = append(facts, "goal="+string(goal.Status))
		}
	}
	facts = append(facts,
		"action="+string(decision.Action.Kind)+"/"+decision.Action.Item.ID+"/"+decision.Action.Goal.ID+"/"+strings.Join(decision.Action.EvidenceIDs, ","),
		"stall="+string(decision.Stall.Kind),
		"candidate="+decision.Repository.Revision+"/"+decision.Repository.SnapshotDigest)
	return strings.Join(facts, "|"), nil
}

// act performs exactly one legal action. Each branch either uses an existing
// transition or stops; none of them writes lifecycle state directly.
func (runner *Runner) act(decision app.Decision) error {
	action := decision.Action
	switch action.Kind {
	case work.NextActionStart:
		return runner.start(action)
	case work.NextActionResume, work.NextActionRepair:
		return runner.implement(action, decision)
	case work.NextActionReconcile:
		return runner.reconcile(action)
	case work.NextActionReverify:
		return runner.verify(action.Item.ID, "REVERIFY")
	case work.NextActionCompleteGoal:
		return runner.completeGoal(action)
	case work.NextActionGoalCompleted:
		return runner.goalAlreadyCompleted(action)
	case work.NextActionWaitGate:
		return runner.stopNow(StopWaitGate, action.Item.ID+": "+action.Reason)
	case work.NextActionWaitGoal:
		return runner.stopNow(StopWaitGoal, action.Item.ID+": "+action.Reason)
	case work.NextActionWaitHumanReview:
		return runner.stopNow(StopWaitHumanReview, action.Item.ID+": "+action.Reason)
	default:
		return runner.stall(decision)
	}
}

// stall turns "no legal action" into a specific reason. NONE is never reported
// as completion: only the Goal completion projection may authorize the final
// transition, and it is strictly stricter than this.
func (runner *Runner) stall(decision app.Decision) error {
	stall := decision.Stall
	switch stall.Kind {
	case work.StallVerifying:
		if len(decision.Orphaned) > 0 {
			// An abandoned run is closed out by the existing exclusive-lock reclaim
			// path, never by editing VERIFYING state here.
			runner.print("Reclaiming an abandoned verification of %s\n", decision.Orphaned[0])
			return runner.verify(decision.Orphaned[0], "RECLAIM")
		}
		return runner.stopNow(StopVerificationInFlight,
			fmt.Sprintf("a live verification of %s is running in another process", strings.Join(decision.Verifying, ", ")))
	case work.StallBlockedByGate:
		return runner.stopNow(StopWaitGate, fmt.Sprintf("%s: %s", strings.Join(stall.ItemIDs, ", "), stall.Detail))
	case work.StallGoalNotActive:
		return runner.stopNow(StopWaitGoal, stall.Detail)
	case work.StallDependenciesUnsatisfied:
		return runner.stopNow(StopStalled, fmt.Sprintf("%s: %s", strings.Join(stall.ItemIDs, ", "), stall.Detail))
	case work.StallNoRemainingWork:
		// Every Work Item is VERIFIED or DONE, yet the completion projection did
		// not agree. Something it checks — freshness, a Gate — does not hold.
		return runner.stopNow(StopStalled,
			"every work item is VERIFIED or DONE but the goal completion conditions do not hold; run `forgepilot status` to see which")
	default:
		return runner.stopNow(StopStalled, stall.Detail)
	}
}

func (runner *Runner) start(action work.NextAction) error {
	if stopped, err := runner.spendStep(action.Kind, action.Item.ID); stopped || err != nil {
		return err
	}
	stopped, err := runner.readFacts(action.Item.ID, func(ctx context.Context) error {
		return app.StartWork(ctx, runner.options.Root, action.Item.ID, runner.now)
	})
	if stopped {
		return err
	}
	if err != nil {
		return runner.stopNow(StopStalled, fmt.Sprintf("start %s: %v", action.Item.ID, err))
	}
	runner.print("Step %d: START %s\n", runner.record.Steps, action.Item.ID)
	if err := runner.journal("START", action.Item.ID, action.Reason); err != nil {
		return err
	}
	// Re-read rather than implement from the action just consumed: starting is
	// its own transition, and the next legal move is asked for again.
	return nil
}

func (runner *Runner) reconcile(action work.NextAction) error {
	if stopped, err := runner.spendStep(action.Kind, action.Item.ID); stopped || err != nil {
		return err
	}
	var changes []work.ReadinessChange
	stopped, err := runner.readFacts(action.Item.ID, func(ctx context.Context) error {
		var reconcileErr error
		changes, reconcileErr = app.ReconcileGoal(ctx, runner.options.Root, runner.options.GoalID, runner.now)
		return reconcileErr
	})
	if stopped {
		return err
	}
	if err != nil {
		return runner.stopNow(StopStalled, fmt.Sprintf("reconcile %s: %v", runner.options.GoalID, err))
	}
	runner.print("Step %d: RECONCILE %s (%d readiness changes)\n", runner.record.Steps, runner.options.GoalID, len(changes))
	return runner.journal("RECONCILE", action.Item.ID, fmt.Sprintf("%d readiness changes", len(changes)))
}

func (runner *Runner) journal(action, itemID, detail string) error {
	return runner.record.journal(runner.options.Root, runner.options.Limits,
		Entry{At: runner.now(), Step: runner.record.Steps, Action: action, WorkItemID: itemID, Detail: detail})
}

// completeGoal re-resolves Candidate facts inside the shared transaction and
// then records the successful terminal stop. If the projection changed between
// the recommendation and this write, the domain refuses completion rather than
// turning a stale read into a durable Goal status.
func (runner *Runner) completeGoal(action work.NextAction) error {
	if stopped, err := runner.beforeAction(); stopped || err != nil {
		return err
	}
	if stopped, err := runner.spendStep(action.Kind, ""); stopped || err != nil {
		return err
	}
	var result app.GoalCompletionResult
	stopped, err := runner.readFactsForGoalCompletion(func(ctx context.Context) error {
		var completionErr error
		result, completionErr = app.CompleteVerifiedGoal(ctx, runner.options.Root, runner.options.GoalID, action.EvidenceIDs, runner.now)
		return completionErr
	})
	if stopped {
		return err
	}
	if err != nil {
		return runner.stopNow(StopStalled, fmt.Sprintf("complete goal %s: %v", runner.options.GoalID, err))
	}
	runner.print("Goal %s completed automatically. Completion evidence: %s; Verification Evidence: %s\n", runner.options.GoalID, result.CompletionEvidenceID, strings.Join(result.VerificationEvidenceIDs, ", "))
	return runner.stopNow(StopGoalCompleted,
		"every work item has fresh PASS Evidence and no open gates; Goal completed", append([]string{result.CompletionEvidenceID}, result.VerificationEvidenceIDs...)...)
}

func (runner *Runner) goalAlreadyCompleted(action work.NextAction) error {
	if action.Goal.LegacyCompletion != nil {
		runner.print("Goal %s was already completed under schema v11 HUMAN policy; no current verification evidence is asserted\n", runner.options.GoalID)
		return runner.stopNow(StopGoalCompleted, "legacy completed status was preserved during schema migration")
	}
	runner.print("Goal %s is already completed. Completion evidence: %s\n", runner.options.GoalID, strings.Join(action.EvidenceIDs, ", "))
	return runner.stopNow(StopGoalCompleted, "Goal completion was already committed", action.EvidenceIDs...)
}

// verify runs the canonical check through the shared orchestration. Its typed
// result is what decides: an agent's claim, its exit code, and the check's own
// exit code are all irrelevant here — only recorded Evidence counts.
func (runner *Runner) verify(itemID, label string) error {
	// Asked again here rather than only at the top of the loop: this is the other
	// side of the agent-session boundary, and a check is a subprocess that can
	// run for as long as the run had left.
	if stopped, err := runner.beforeAction(); stopped || err != nil {
		return err
	}
	if stopped, err := runner.spendStep(work.NextActionReverify, itemID); stopped || err != nil {
		return err
	}
	runner.print("Step %d: %s %s\n", runner.record.Steps, label, itemID)
	// The check is bound to the earlier of the run's deadline and its own
	// timeout, and to the same stop signal the loop watches. Without this the
	// canonical check was the one place a Ctrl-C did not reach.
	execution := runner.newExecution(runner.options.Budget.VerifyTimeout)
	defer execution.release()
	// Recorded before the verification starts anything external. A crash between
	// here and the first confirmation leaves an entry nobody can resolve, which
	// blocks the next run — the honest answer, because from the outside a crash
	// in the launch window is indistinguishable from a launch that happened.
	// A save that fails stops the step rather than starting the work anyway.
	pendingID := runner.record.addPending(PendingExecution{
		Kind: KindVerification, Phase: PhasePendingStart, WorkItemID: itemID,
		Location: runner.options.Root, ObservedAt: runner.now()})
	if err := runner.save(); err != nil {
		runner.record.resolvePending(pendingID)
		return err
	}
	// No Timeout is passed: app.VerifyOptions.Timeout is the standalone command's
	// own limit, counted in full from when the check starts. The Runner's limit
	// is already the earlier of that timeout and the run's deadline, and arming
	// both would leave two timers measuring the same thing with only one of them
	// honouring min().
	verifyOptions := app.VerifyOptions{Snapshot: runner.options.Snapshot, Now: runner.now}
	if runner.record.ExecutionAuthorizationDigest != "" {
		verifyOptions.ExecutionGoalID = runner.record.GoalID
		verifyOptions.ExecutionAuthorizationDigest = runner.record.ExecutionAuthorizationDigest
	}
	result, err := app.Verify(execution.ctx, runner.options.Root, itemID, runner.options.Output, verifyOptions)
	runner.record.EvidenceIDs = append(runner.record.EvidenceIDs, verificationEvidenceIDs(result)...)
	// Checked before the engineering outcome is acted on, and deliberately not
	// turned into one: Evidence this call earned is already saved and stands,
	// but a group that cannot be confirmed empty means the next step could
	// overlap something still writing this workspace.
	if result.Cleanup != nil {
		// The entry is rewritten rather than dropped, one per group that could not
		// be confirmed, so the next process — a resume, a new run, the same
		// workspace with another Goal — finds it whether or not this one lives to
		// report it. Why the step ended and why its cleanup failed are recorded as
		// separate fields: a cleanup failure must not erase the Ctrl-C.
		runner.record.resolvePending(pendingID)
		observed := runner.now()
		stopped := describe(execution.Cause())
		unresolved := result.Unresolved
		if len(unresolved) == 0 {
			unresolved = []app.Unresolved{{Kind: app.UnresolvedCanonicalCheck, Detail: result.Cleanup.Error()}}
		}
		for _, group := range unresolved {
			runner.record.addPending(PendingExecution{
				Kind: pendingKind(group.Kind), Phase: PhaseCleanupUnconfirmed, WorkItemID: itemID,
				Location: group.Location, Identity: agent.ProcessIdentity{PGID: group.PGID},
				StopReason: stopped, CleanupDetail: group.Detail, ObservedAt: observed})
		}
		// The step's own error is kept beside the cleanup failure. Cleanup takes
		// priority — the next step must not start — but a verification that also
		// had its governance state rewritten, or that was refused, is a second
		// fact the person sorting this out needs; reporting only "something is
		// still running" would send them to look for a process and never mention
		// that the state is no longer trustworthy.
		detail := fmt.Sprintf(
			"%s: a process group started by this verification could not be confirmed stopped: %v (the step ended %s). Find what is still running under this workspace and stop it before running anything else",
			itemID, result.Cleanup, stopped)
		if err != nil {
			detail += fmt.Sprintf("; the step also ended with: %v", err)
		}
		if saveErr := runner.save(); saveErr != nil {
			// The record could not be written, so nothing above is durable. Say what
			// was about to be recorded rather than returning only the write failure.
			return fmt.Errorf("%w; the unrecorded cleanup was: %s", saveErr, detail)
		}
		return runner.stopNow(StopRecoveryBlocked, detail)
	}
	// Confirmed clear: the entry and the confirmation land in the same atomic
	// record replacement.
	runner.record.resolvePending(pendingID)
	if err := runner.save(); err != nil {
		return err
	}
	switch {
	case errors.Is(err, app.ErrVerificationInterrupted):
		// The run and the check ended together; which limit ended them is recorded
		// here, where the limits were set, rather than guessed from the context.
		if reason, ok := runner.stopReasonFor(execution.Cause(), StopVerifyTimeout); ok {
			return runner.stopNow(reason, fmt.Sprintf("%s: %v", itemID, err))
		}
		return err
	case errors.Is(err, app.ErrVerificationTimedOut):
		return runner.stopNow(StopVerifyTimeout, err.Error())
	case app.IsRefusal(err):
		// A refusal is not an engineering failure and must never be recorded as
		// one. It names a condition outside the code: a Gate, a stopped Goal, an
		// unsatisfiable Runtime Contract, a revision with no canonical check.
		return runner.stopNow(StopVerificationRefused, fmt.Sprintf("%s: %v", itemID, err))
	case errors.Is(err, app.ErrStateWrittenDuringCheck):
		// The session ended cleanly and its digest matched; the code it left
		// behind did the writing. Same verdict either way.
		return runner.stopNow(StopStateTampered, err.Error())
	case errors.Is(err, app.ErrExecutionBindingDrift):
		return runner.stopNow(StopScopeChanged, err.Error())
	case errors.Is(err, storage.ErrCapacityExceeded):
		return runner.stopNow(StopCapacityExceeded, err.Error())
	case err != nil:
		return err
	}
	detail := "no evidence"
	if result.HasEvidence {
		ids := make([]string, 0, len(result.EvidenceSet))
		for _, evidence := range result.EvidenceSet {
			ids = append(ids, evidence.ID)
		}
		if len(ids) == 0 {
			ids = append(ids, result.Evidence.ID)
		}
		detail = fmt.Sprintf("%s %s", strings.Join(ids, ","), result.Evidence.Result)
	}
	return runner.journal(label, itemID, detail)
}

func verificationEvidenceIDs(result app.VerifyResult) []string {
	ids := []string{}
	if len(result.ReclaimedSet) > 0 {
		for _, evidence := range result.ReclaimedSet {
			ids = append(ids, evidence.ID)
		}
	} else if result.Reclaimed != nil {
		ids = append(ids, result.Reclaimed.ID)
	}
	if len(result.EvidenceSet) > 0 {
		for _, evidence := range result.EvidenceSet {
			ids = append(ids, evidence.ID)
		}
	} else if result.HasEvidence {
		ids = append(ids, result.Evidence.ID)
	}
	return ids
}

// implement launches one new agent session. Budget is charged and persisted
// before the process starts, so a crash between the two cannot be replayed into
// unlimited attempts.
func (runner *Runner) implement(action work.NextAction, decision app.Decision) error {
	itemID := action.Item.ID
	attempt := runner.record.Attempts[itemID] + 1
	technicalAttempt := runner.record.technicalAttemptsFor(itemID) + 1
	if technicalAttempt > runner.options.Budget.MaxAttemptsPerWork {
		return runner.stopNow(StopMaxAttempts,
			fmt.Sprintf("%s has used its %d technical attempts in this run", itemID, runner.options.Budget.MaxAttemptsPerWork))
	}
	if runner.record.Steps >= runner.options.Budget.MaxSteps {
		return runner.stopNow(StopMaxSteps, fmt.Sprintf("%d steps is the configured maximum", runner.options.Budget.MaxSteps))
	}
	// The last gate before a process is launched. Everything above this is
	// bookkeeping; below it a coding CLI starts writing the workspace, and a run
	// whose deadline has already passed must not be what starts one.
	if stopped, err := runner.beforeAction(); stopped || err != nil {
		return err
	}
	var actionReceipt *work.ExecutionReservationReceipt
	var workerReceipts []work.ExecutionReservationReceipt
	if runner.record.ExecutionAuthorizationDigest != "" {
		identity, err := runner.runnerIdentity()
		if err != nil {
			return err
		}
		reservation, stepReservation, err := app.PrepareChargedWorker(runner.options.Root, runner.record.GoalID, runner.record.RunID,
			string(action.Kind), itemID, runner.record.Steps+1, attempt,
			runner.record.ExecutionAuthorizationDigest, identity, runner.now())
		if err != nil {
			return err
		}
		runner.crashAt("after-action-charge")
		receipt, err := work.ExecutionReservationReceiptFor(reservation)
		if err != nil {
			return err
		}
		if err := runner.appendReservationReceipt(receipt); err != nil {
			return err
		}
		runner.crashAt("after-action-receipt")
		actionReceipt = &receipt
		stepReceipt, err := work.ExecutionReservationReceiptFor(stepReservation)
		if err != nil {
			return err
		}
		if err := runner.appendReservationReceipt(stepReceipt); err != nil {
			return err
		}
		workerReceipts = []work.ExecutionReservationReceipt{receipt, stepReceipt}
	}
	sessionDir, err := runner.sessionDirectory(itemID, attempt)
	if err != nil {
		return err
	}
	startedAt := runner.now()
	// The attempt and step are recorded before any fallible preparation. The
	// durable charges above are stable across an exact retry, and Worker ownership
	// is recorded before the state digest and launch boundary.
	runner.record.Attempts[itemID] = attempt
	runner.record.Steps++
	if stopped, err := runner.checkpoint(); stopped || err != nil {
		return err
	}
	// Anchor governance state before assembling the briefing. A Gate resolution
	// or other state change after this point must invalidate the session result,
	// not leave the worker acting on a stale handoff that still happens to match
	// a digest captured after the change.
	stateBefore, digestErr := storage.StateDigest(runner.options.Root)
	if digestErr != nil {
		return digestErr
	}
	// From this checkpoint onward a crash cannot prove whether the launch below
	// happened, so recovery must see the prospective worker before it exists.
	runner.record.Worker = &Worker{WorkItemID: itemID, Attempt: attempt, SessionDir: sessionDir, StartedAt: startedAt,
		ReservationReceipts: append([]work.ExecutionReservationReceipt(nil), workerReceipts...)}
	pendingID := ""
	if actionReceipt != nil {
		pendingID = runner.record.addPending(PendingExecution{
			Kind: KindAgentSession, Phase: PhasePendingStart, WorkItemID: itemID,
			Location: sessionDir, ReservationReceipts: append([]work.ExecutionReservationReceipt(nil), workerReceipts...), ObservedAt: startedAt})
	}
	if stopped, err := runner.checkpoint(); stopped || err != nil {
		return err
	}
	if actionReceipt != nil {
		runner.crashAt("after-pending-save")
	}

	handoff, current, err := runner.handoff(action, decision, attempt)
	if err != nil {
		if saveErr := runner.withdrawUnstartedWorker(pendingID); saveErr != nil {
			return saveErr
		}
		return err
	}
	if !current {
		return runner.withdrawUnstartedWorker(pendingID)
	}
	// The session writes its own briefing and console log, so the room for them
	// is claimed before it starts rather than discovered afterwards.
	if err := storage.CheckRunCapacity(runner.options.Root, runner.record.RunID,
		sessionReservation(len(handoff), runner.options.Limits.MaxWriteBytes), runner.options.Limits); err != nil {
		if saveErr := runner.withdrawUnstartedWorker(pendingID); saveErr != nil {
			return saveErr
		}
		return runner.stopNow(StopCapacityExceeded, err.Error())
	}
	runner.print("Step %d: %s %s (session %d, technical attempt %d/%d, new session)\n",
		runner.record.Steps, action.Kind, itemID, attempt, technicalAttempt, runner.options.Budget.MaxAttemptsPerWork)
	request := agent.Request{Workspace: runner.record.Workspace, ArtifactDir: sessionDir,
		Handoff: handoff, MaxOutputBytes: runner.options.Limits.MaxWriteBytes}
	// The real last gate. Everything between the earlier check and here —
	// building the handoff, claiming capacity, digesting the state — takes time a
	// signal or a deadline can arrive in, and a stop that arrived during the
	// preparation must not be answered by starting the process anyway and only
	// noticing at Wait. The worker record is withdrawn because this path knows
	// for certain that nothing was launched.
	if stopped, stopErr := runner.beforeAction(); stopped || stopErr != nil {
		if saveErr := runner.withdrawUnstartedWorker(pendingID); saveErr != nil {
			return saveErr
		}
		return stopErr
	}
	// State may have changed while the handoff or capacity reservation was being
	// prepared. Refuse to launch from that stale authorization snapshot; the next
	// loop iteration will ask the typed query for the now-current action.
	currentState, digestErr := storage.StateDigest(runner.options.Root)
	if digestErr != nil {
		if saveErr := runner.withdrawUnstartedWorker(pendingID); saveErr != nil {
			return saveErr
		}
		return digestErr
	}
	if currentState != stateBefore {
		return runner.withdrawUnstartedWorker(pendingID)
	}
	if runner.record.ExecutionAuthorizationDigest != "" {
		identity, err := runner.runnerIdentity()
		if err != nil {
			if saveErr := runner.withdrawUnstartedWorker(pendingID); saveErr != nil {
				return saveErr
			}
			return err
		}
		_, err = app.PrepareChargedArtifactBytes(runner.options.Root, runner.record.GoalID, runner.record.RunID,
			fmt.Sprintf("%s:artifact:%s:%s:%d", runner.record.RunID, action.Kind, itemID, attempt),
			sessionReservation(len(handoff), runner.options.Limits.MaxWriteBytes),
			runner.record.ExecutionAuthorizationDigest, identity, runner.now())
		if err != nil {
			if saveErr := runner.withdrawUnstartedWorker(pendingID); saveErr != nil {
				return saveErr
			}
			if strings.Contains(err.Error(), "artifact-byte cap") {
				return runner.stopNow(StopCapacityExceeded, err.Error())
			}
			return err
		}
		runner.crashAt("after-artifact-charge")
		// The authorization ledger write above is ForgePilot's own durable
		// pre-launch charge. It becomes the new baseline for detecting an
		// untrusted session that writes governance state.
		stateBefore, digestErr = storage.StateDigest(runner.options.Root)
		if digestErr != nil {
			if saveErr := runner.withdrawUnstartedWorker(pendingID); saveErr != nil {
				return saveErr
			}
			return digestErr
		}
	}
	session, err := agent.Start(runner.runtime, request, startedAt)
	if err != nil {
		if saveErr := runner.withdrawUnstartedWorker(pendingID); saveErr != nil {
			return saveErr
		}
		return runner.stopNow(StopAgentExecutionFailed, fmt.Sprintf("%s: %v", itemID, err))
	}
	if actionReceipt != nil {
		runner.crashAt("after-worker-launch")
	}
	// The identity is written the moment it exists: a crash after this point is
	// recoverable, and one before it leaves a worker record with no identity,
	// which recovery treats as unconfirmable rather than as absent.
	incomplete := false
	runner.record.Worker.Identity = session.Started()
	if pendingID != "" {
		runner.record.updatePending(pendingID, func(pending *PendingExecution) {
			pending.Identity = session.Started()
			pending.Phase = PhaseRunning
		})
	}
	if !runner.record.Worker.Identity.Recorded() {
		// The operating system could not be asked what it had just started —
		// usually because the process was gone before `ps` ran. Nothing is broken
		// yet, but a crash from here on will be unrecoverable without a person, so
		// say so now rather than at recovery time.
		runner.print("  warning: could not record an identity for the worker (pid %d); a crash before it finishes will need manual confirmation\n",
			runner.record.Worker.Identity.PID)
		incomplete = true
	}
	if stopped, err := runner.checkpoint(); stopped || err != nil {
		// The worker is running but cannot be recorded. Stopping it is the only
		// honest move: leaving it alive with no durable identity would make the
		// next recovery unable to tell whether anything still writes here. When
		// even that cannot be confirmed, the refusal is the answer — swallowing it
		// would hand the next run a workspace with an invisible writer in it.
		// Whatever happens to the process, this handle is let go of: Wait is never
		// reached on this path, and it is Wait that would otherwise have released
		// the output pipe and the session log.
		defer session.Discard()
		if terminateErr := agent.TerminateOwned(session.Started()); terminateErr != nil {
			return runner.stopNow(StopRecoveryBlocked, fmt.Sprintf(
				"%s could not be recorded (%v) and its worker could not be stopped: %v. Confirm pid %d yourself before running anything else",
				itemID, err, terminateErr, session.Started().PID))
		}
		return err
	}
	if actionReceipt != nil {
		runner.crashAt("after-worker-identity-save")
	}
	// Journalled only once the identity is durable: an append that fails here
	// must not be the thing that leaves a live worker unrecorded.
	if incomplete {
		if err := runner.journal("AGENT", itemID, "worker identity could not be observed"); err != nil {
			return err
		}
	}

	// Bound to the earlier of the run's deadline and this session's own timeout,
	// so a session started shortly before the deadline cannot add a full
	// --agent-timeout on top of it.
	execution := runner.newExecution(runner.options.Budget.AgentTimeout)
	defer execution.release()
	result, waitErr := session.Wait(execution.ctx)
	// Asked before the worker record is withdrawn. That record is what names the
	// pid and pgid whoever has to sort this out needs; clearing it because the
	// call returned would make the next run look clean while something may still
	// be writing this workspace.
	if cleanupErr := stoppedCleanup(waitErr); cleanupErr != nil {
		// Durable beside the worker entry, so the fact survives this process with
		// its two halves apart: why the session was stopped, and why its group
		// could not be confirmed gone.
		identity := runner.record.Worker.Identity
		if identity.PGID == 0 {
			// The group the cleanup itself named, for a session whose own identity
			// could not be observed. Zero stays zero, and a pending execution with
			// no group id is refused later rather than read as nothing to do.
			if pgid, named := process.UnsettledGroup(cleanupErr); named {
				identity.PGID = pgid
			}
		}
		if pendingID != "" {
			runner.record.updatePending(pendingID, func(pending *PendingExecution) {
				pending.Phase = PhaseCleanupUnconfirmed
				pending.Identity = identity
				pending.StopReason = describe(execution.Cause())
				pending.CleanupDetail = cleanupErr.Error()
				pending.ObservedAt = runner.now()
			})
		} else {
			runner.record.addPending(PendingExecution{
				Kind: KindAgentSession, Phase: PhaseCleanupUnconfirmed, WorkItemID: itemID,
				Location: sessionDir, Identity: identity,
				StopReason: describe(execution.Cause()), CleanupDetail: cleanupErr.Error(),
				ObservedAt: runner.now()})
		}
		detail := fmt.Sprintf(
			"%s attempt %d: the session's process group could not be confirmed stopped: %v (the step ended %s). Find what is still running under this workspace and stop it before running anything else",
			itemID, attempt, cleanupErr, describe(execution.Cause()))
		if saveErr := runner.save(); saveErr != nil {
			return fmt.Errorf("%w; the unrecorded cleanup was: %s", saveErr, detail)
		}
		return runner.stopNow(StopRecoveryBlocked, detail)
	}
	runner.record.Worker = nil
	if pendingID != "" {
		runner.record.resolvePending(pendingID)
	}
	if stopped, err := runner.checkpoint(); stopped || err != nil {
		return err
	}
	// Checked before the result is even looked at: a session that edited
	// governance state has invalidated everything downstream of it, including
	// its own account of what it did.
	stateAfter, digestErr := storage.StateDigest(runner.options.Root)
	if digestErr != nil {
		return digestErr
	}
	if stateAfter != stateBefore {
		return runner.stopNow(StopStateTampered, fmt.Sprintf(
			"%s: .forgepilot/state.json changed while the session was running. The session was told not to touch it, so its report cannot be trusted — but a ForgePilot command run in another terminal during the session would look the same from here. Inspect the state, and this run's journal, before running anything else", itemID))
	}
	return runner.afterSession(itemID, attempt, result, waitErr, execution.Cause(), actionReceipt)
}

// withdrawUnstartedWorker clears only process ownership. The attempt and step
// remain charged because both were durably reserved before launch.
func (runner *Runner) withdrawUnstartedWorker(pendingID string) error {
	runner.record.Worker = nil
	if pendingID != "" {
		runner.record.resolvePending(pendingID)
	}
	return runner.save()
}

func (runner *Runner) afterSession(itemID string, attempt int, result agent.Result, waitErr error, cause stopCause,
	actionReceipt *work.ExecutionReservationReceipt) error {
	// A control stop deliberately wins over the agent's exit status. RequestStop
	// makes the pause durable before it terminates the group, so an externally
	// stopped worker must never be reported as an execution failure.
	if stopped, err := runner.honorPause(); stopped || err != nil {
		return err
	}
	switch {
	case errors.Is(waitErr, agent.ErrStopped):
		reason, ok := runner.stopReasonFor(cause, StopAgentTimeout)
		if !ok {
			return waitErr
		}
		return runner.stopNow(reason, fmt.Sprintf(
			"%s attempt %d was stopped; its process group was terminated. Resume this run with `forgepilot run resume %s`", itemID, attempt, runner.record.RunID))
	case agent.IsProtocolError(waitErr):
		if err := runner.recordAttempt(itemID, attempt, "protocol_error", waitErr.Error(), nil); err != nil {
			return err
		}
		if runner.record.Stop != nil {
			return nil
		}
		if runner.record.technicalAttemptsFor(itemID) >= runner.options.Budget.MaxAttemptsPerWork {
			return runner.stopNow(StopRuntimeProtocol, fmt.Sprintf("%s: %v", itemID, waitErr))
		}
		runner.print("  attempt %d returned no usable result: %v\n", attempt, waitErr)
		return runner.journal("AGENT", itemID, "protocol error")
	case waitErr != nil:
		return runner.stopNow(StopAgentExecutionFailed, fmt.Sprintf("%s attempt %d: %v", itemID, attempt, waitErr))
	}
	if runner.record.Stop == nil && result.Outcome == agent.NeedsHuman && actionReceipt != nil {
		// Persist the validated result and its ACTION receipt before the Goal
		// ledger disposition. Resume can then finish the exact transition if the
		// process crashes between these two durable writes.
		if err := runner.recordAttempt(itemID, attempt, string(result.Outcome), result.Summary, actionReceipt); err != nil {
			return err
		}
		runner.crashAt("after-human-result-record")
		if err := app.ConfirmRunnerNeedsHuman(runner.options.Root, runner.record.GoalID, runner.record.RunID,
			itemID, attempt, *actionReceipt); err != nil {
			return err
		}
		runner.crashAt("after-human-disposition")
		if err := runner.recordChargedHumanWait(itemID, *actionReceipt); err != nil {
			return err
		}
		if stopped, err := runner.checkpoint(); stopped || err != nil {
			return err
		}
		return runner.needsHuman(itemID, result)
	}

	if err := runner.recordAttempt(itemID, attempt, string(result.Outcome), result.Summary, nil); err != nil {
		return err
	}
	if runner.record.Stop != nil {
		return nil
	}
	switch result.Outcome {
	case agent.NeedsHuman:
		if actionReceipt == nil {
			runner.record.recordHumanWait(itemID)
		}
		if stopped, err := runner.checkpoint(); stopped || err != nil {
			return err
		}
		return runner.needsHuman(itemID, result)
	case agent.ExecutionFailed:
		// Credentials, tooling and environment failures are the agent's problem,
		// not the code's. Verifying now would manufacture an engineering FAIL for
		// something the repository never did.
		return runner.stopNow(StopAgentExecutionFailed, fmt.Sprintf("%s attempt %d: %s", itemID, attempt, result.Error))
	}
	runner.print("  attempt %d finished: %s\n", attempt, firstLine(result.Summary))
	if err := runner.journal("AGENT", itemID, string(result.Outcome)); err != nil {
		return err
	}
	// An agent saying it is done is a claim. The canonical check on the Candidate
	// is the answer.
	return runner.verify(itemID, "VERIFY")
}

// needsHuman stops and, when the agent stated real alternatives, records the
// question as an open Gate through the existing Gate service. It never invents
// options, never answers, and never resolves what it opened.
func (runner *Runner) needsHuman(itemID string, result agent.Result) error {
	detail := result.Question.Question
	if len(result.Question.Options) >= 2 {
		gateID, err := app.OpenGate(runner.options.Root, itemID, result.Question.Question,
			result.Question.Options, "raised by agent session in run "+runner.record.RunID, runner.now)
		if err != nil {
			detail += fmt.Sprintf(" (could not record it as a Gate: %v)", err)
		} else {
			detail += fmt.Sprintf(" (recorded as Gate %s; resolve it with `forgepilot gate resolve %s`)", gateID, gateID)
		}
	}
	return runner.stopNow(StopNeedsHuman, fmt.Sprintf("%s: %s", itemID, detail))
}

func (runner *Runner) recordAttempt(itemID string, attempt int, outcome, summary string,
	actionReceipt *work.ExecutionReservationReceipt) error {
	recordedAttempt := Attempt{WorkItemID: itemID, Number: attempt, Outcome: outcome,
		Summary: summary, At: runner.now()}
	if actionReceipt != nil {
		receipt := *actionReceipt
		recordedAttempt.ActionReservationReceipt = &receipt
	}
	runner.record.recordAttempt(recordedAttempt)
	_, err := runner.checkpoint()
	return err
}

// spendStep charges one step against the budget and persists it before the work
// happens. Checking only at the top of the loop would let one iteration that
// does two things — implement, then verify — overshoot the ceiling it was given.
func (runner *Runner) spendStep(actionKind work.NextActionKind, workItemID string) (bool, error) {
	if runner.record.Steps >= runner.options.Budget.MaxSteps {
		return true, runner.stopNow(StopMaxSteps, fmt.Sprintf("%d steps is the configured maximum", runner.options.Budget.MaxSteps))
	}
	if runner.record.ExecutionAuthorizationDigest != "" {
		identity, err := runner.runnerIdentity()
		if err != nil {
			return true, err
		}
		reservation, err := app.PrepareChargedStep(runner.options.Root, runner.record.GoalID, runner.record.RunID,
			runner.record.Steps+1, string(actionKind), workItemID, runner.record.ExecutionAuthorizationDigest, identity, runner.now())
		if err != nil {
			return true, err
		}
		runner.crashAt("after-step-charge")
		receipt, err := work.ExecutionReservationReceiptFor(reservation)
		if err != nil {
			return true, err
		}
		if err := runner.appendReservationReceipt(receipt); err != nil {
			return true, err
		}
	}
	runner.record.Steps++
	return runner.checkpoint()
}

func (runner *Runner) appendReservationReceipt(receipt work.ExecutionReservationReceipt) error {
	for _, existing := range runner.record.ReservationReceipts {
		if existing.ReservationID != receipt.ReservationID {
			continue
		}
		if existing != receipt {
			return fmt.Errorf("Run Record has a conflicting receipt for reservation %q", receipt.ReservationID)
		}
		return nil
	}
	runner.record.ReservationReceipts = append(runner.record.ReservationReceipts, receipt)
	return runner.save()
}

func (runner *Runner) crashAt(point string) {
	if runner.options.testCrashAt != nil {
		runner.options.testCrashAt(point)
	}
}

func reconcileChargedWorkerReceipts(record *Record, snapshot app.RunnerReservationSnapshot) (bool, error) {
	if record.ExecutionAuthorizationDigest == "" {
		return false, nil
	}
	reservations := make(map[string]work.ExecutionReservation, len(snapshot.Reservations))
	receipts := make(map[string]work.ExecutionReservationReceipt, len(snapshot.Receipts))
	for i, reservation := range snapshot.Reservations {
		reservations[reservation.ID] = reservation
		if i < len(snapshot.Receipts) {
			receipts[snapshot.Receipts[i].ReservationID] = snapshot.Receipts[i]
		}
	}

	validatePair := func(workItemID string, pair []work.ExecutionReservationReceipt) error {
		if len(pair) != 2 || pair[0].ReservationID == pair[1].ReservationID {
			return fmt.Errorf("charged run %s agent ownership must carry exactly one ACTION and one STEP receipt", record.RunID)
		}
		var action, step *work.ExecutionReservation
		for _, receipt := range pair {
			reservation, ok := reservations[receipt.ReservationID]
			if !ok || receipts[receipt.ReservationID] != receipt {
				return fmt.Errorf("charged run %s agent ownership receipt is not in its ledger-backed Run Record", record.RunID)
			}
			if reservation.RunID != record.RunID {
				return fmt.Errorf("charged run %s agent ownership receipt belongs to another run", record.RunID)
			}
			switch reservation.Kind {
			case work.ExecutionReservationAction:
				if action != nil {
					return fmt.Errorf("charged run %s agent ownership has duplicate ACTION receipts", record.RunID)
				}
				action = &reservation
			case work.ExecutionReservationStep:
				if step != nil {
					return fmt.Errorf("charged run %s agent ownership has duplicate STEP receipts", record.RunID)
				}
				step = &reservation
			default:
				return fmt.Errorf("charged run %s agent ownership receipt has kind %s", record.RunID, reservation.Kind)
			}
		}
		if action == nil || step == nil || action.PlanNodeRef == "" || action.PlanNodeRef != step.PlanNodeRef {
			return fmt.Errorf("charged run %s agent ACTION and STEP do not share one plan node", record.RunID)
		}
		if snapshot.PlanNodeByWorkItem[workItemID] != action.PlanNodeRef {
			return fmt.Errorf("charged run %s agent receipts do not match Work Item %s", record.RunID, workItemID)
		}
		return nil
	}

	var matchingPending *PendingExecution
	for i := range record.Pending {
		pending := &record.Pending[i]
		if pending.Kind != KindAgentSession {
			if len(pending.ReservationReceipts) != 0 {
				return false, fmt.Errorf("charged run %s non-agent Pending execution carries worker receipts", record.RunID)
			}
			continue
		}
		if matchingPending != nil {
			return false, fmt.Errorf("charged run %s has multiple unsettled agent Pending executions", record.RunID)
		}
		matchingPending = pending
		if err := validatePair(pending.WorkItemID, pending.ReservationReceipts); err != nil {
			return false, err
		}
	}
	if record.Worker == nil {
		if matchingPending != nil {
			return false, fmt.Errorf("charged run %s has agent Pending ownership without a Worker record", record.RunID)
		}
		return false, nil
	}
	if matchingPending == nil || matchingPending.WorkItemID != record.Worker.WorkItemID ||
		matchingPending.Location != record.Worker.SessionDir {
		return false, fmt.Errorf("charged run %s Worker has no matching agent Pending ownership", record.RunID)
	}
	if len(record.Worker.ReservationReceipts) == 0 {
		record.Worker.ReservationReceipts = append([]work.ExecutionReservationReceipt(nil), matchingPending.ReservationReceipts...)
		return true, validatePair(record.Worker.WorkItemID, record.Worker.ReservationReceipts)
	}
	if !reflect.DeepEqual(record.Worker.ReservationReceipts, matchingPending.ReservationReceipts) {
		return false, fmt.Errorf("charged run %s Worker and Pending reservation receipts do not match", record.RunID)
	}
	return false, validatePair(record.Worker.WorkItemID, record.Worker.ReservationReceipts)
}

func reconcileHumanWaitReservations(record *Record, snapshot app.RunnerReservationSnapshot) (bool, error) {
	if record.ExecutionAuthorizationDigest == "" {
		return false, nil
	}
	reservations := make(map[string]work.ExecutionReservation, len(snapshot.Reservations))
	for _, reservation := range snapshot.Reservations {
		reservations[reservation.ID] = reservation
	}
	workItemByNode := make(map[string]string, len(snapshot.PlanNodeByWorkItem))
	for workItemID, nodeRef := range snapshot.PlanNodeByWorkItem {
		if previous, exists := workItemByNode[nodeRef]; exists && previous != workItemID {
			return false, fmt.Errorf("charged run %s has ambiguous Work Item mapping for Plan Node %s", record.RunID, nodeRef)
		}
		workItemByNode[nodeRef] = workItemID
	}
	var desired []string
	desiredWorkItem := make(map[string]string)
	for _, disposition := range snapshot.NeedsHumanDispositions {
		if disposition.RunID != record.RunID {
			continue
		}
		reservation, exists := reservations[disposition.ReservationID]
		if !exists || reservation.Kind != work.ExecutionReservationAction || reservation.RunID != record.RunID {
			return false, fmt.Errorf("charged run %s has a needs_human disposition without its ACTION reservation", record.RunID)
		}
		receipt, err := work.ExecutionReservationReceiptFor(reservation)
		if err != nil || receipt.ReservationDigest != disposition.ReservationDigest {
			return false, fmt.Errorf("charged run %s has a mismatched needs_human disposition receipt", record.RunID)
		}
		workItemID := workItemByNode[reservation.PlanNodeRef]
		if workItemID == "" {
			return false, fmt.Errorf("charged run %s needs_human disposition refers to an unmapped Plan Node", record.RunID)
		}
		if _, duplicate := desiredWorkItem[disposition.ReservationID]; duplicate {
			return false, fmt.Errorf("charged run %s has duplicate needs_human dispositions", record.RunID)
		}
		desired = append(desired, disposition.ReservationID)
		desiredWorkItem[disposition.ReservationID] = workItemID
	}
	existing := make(map[string]bool, len(record.HumanWaitReservationIDs))
	existingCounts := make(map[string]int)
	for _, reservationID := range record.HumanWaitReservationIDs {
		workItemID, exists := desiredWorkItem[reservationID]
		if !exists || existing[reservationID] {
			return false, fmt.Errorf("charged run %s Run Record has an invalid or duplicate needs_human receipt", record.RunID)
		}
		existing[reservationID] = true
		existingCounts[workItemID]++
	}
	for workItemID, count := range existingCounts {
		if record.HumanWaits[workItemID] != count {
			return false, fmt.Errorf("charged run %s Run Record has inconsistent needs_human attempt totals", record.RunID)
		}
	}
	changed := !reflect.DeepEqual(record.HumanWaitReservationIDs, desired)
	for _, reservationID := range desired {
		if existing[reservationID] {
			continue
		}
		record.recordHumanWait(desiredWorkItem[reservationID])
		changed = true
	}
	if changed {
		record.HumanWaitReservationIDs = desired
	}
	return changed, nil
}

func reconcileRecordedNeedsHumanDispositions(root string, record *Record) error {
	for _, attempt := range record.History {
		if attempt.Outcome != string(agent.NeedsHuman) || attempt.ActionReservationReceipt == nil {
			continue
		}
		if err := app.ConfirmRunnerNeedsHuman(root, record.GoalID, record.RunID,
			attempt.WorkItemID, attempt.Number, *attempt.ActionReservationReceipt); err != nil {
			return fmt.Errorf("reconcile run %s needs_human result for %s attempt %d: %w",
				record.RunID, attempt.WorkItemID, attempt.Number, err)
		}
	}
	return nil
}

func (runner *Runner) recordChargedHumanWait(workItemID string, receipt work.ExecutionReservationReceipt) error {
	for _, existing := range runner.record.HumanWaitReservationIDs {
		if existing == receipt.ReservationID {
			return nil
		}
	}
	runner.record.HumanWaitReservationIDs = append(runner.record.HumanWaitReservationIDs, receipt.ReservationID)
	runner.record.recordHumanWait(workItemID)
	return nil
}

func appReceipt(receipts []work.ExecutionReservationReceipt, id string) (work.ExecutionReservationReceipt, bool) {
	for _, receipt := range receipts {
		if receipt.ReservationID == id {
			return receipt, true
		}
	}
	return work.ExecutionReservationReceipt{}, false
}

// checkpoint persists the record and reports whether the run must stop. It has
// no special case for a full workspace: the record is exempt from the artifact
// bounds precisely so that the answer to "no room" is never to drop the worker
// from the record in order to make the write fit.
func (runner *Runner) checkpoint() (bool, error) {
	err := runner.save()
	return err != nil, err
}

func (runner *Runner) sessionDirectory(itemID string, attempt int) (string, error) {
	directory, err := storage.RunDirectory(runner.options.Root, runner.record.RunID)
	if err != nil {
		return "", err
	}
	path := filepath.Join(directory, sessionName(itemID, attempt))
	if err := os.MkdirAll(path, 0755); err != nil {
		return "", err
	}
	return path, nil
}

// sessionReservation is the room one session may need: the briefing, the
// console log up to its own bound, the structured result at its maximum, and a
// little for whatever else a runtime writes beside them (Codex leaves a schema
// file). Reserving less would make MaxRunBytes an approximate ceiling rather
// than a real one.
func sessionReservation(handoffBytes int, outputBytes int64) int64 {
	const runtimeSidecarAllowance = 8 * 1024
	return int64(handoffBytes) + outputBytes + agent.MaxResultBytes + runtimeSidecarAllowance
}

func sessionName(itemID string, attempt int) string {
	return fmt.Sprintf("%s-attempt-%d", strings.ToLower(itemID), attempt)
}

// stoppedCleanup reports that a session's process group could not be confirmed
// empty. It is a different question from why the session ended, and it has an
// answer on the ordinary path too: a session that exited cleanly can still have
// left a group behind.
func stoppedCleanup(err error) error {
	if errors.Is(err, process.ErrNotSettled) {
		return err
	}
	return nil
}

func firstLine(text string) string {
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		return text[:index]
	}
	return text
}

// pendingKind maps the stage internal/app observed onto the kind a run record
// stores. The two vocabularies are kept apart on purpose: internal/app must not
// learn what a run record looks like, and internal/runner must not re-derive
// the fact from a message. See docs/adr/0022-pending-cleanup-outlives-the-process.md.
func pendingKind(kind string) string {
	switch kind {
	case app.UnresolvedRuntimePreflight:
		return KindRuntimePreflight
	case app.UnresolvedCanonicalPreflight:
		return KindCanonicalPreflight
	case app.UnresolvedGit:
		return KindGit
	default:
		return KindCanonicalCheck
	}
}
