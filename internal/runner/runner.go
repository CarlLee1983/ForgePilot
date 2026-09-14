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
	// Excerpt bounds how much of a failed verification log is quoted into the
	// next handoff.
	Excerpt int
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
}

func (runner *Runner) now() time.Time {
	if runner.options.Now != nil {
		return runner.options.Now().UTC()
	}
	return time.Now().UTC()
}

func (runner *Runner) print(format string, arguments ...any) {
	if runner.options.Output != nil {
		fmt.Fprintf(runner.options.Output, format, arguments...)
	}
}

// Plan is what --dry-run reports: everything checked, nothing done.
type Plan struct {
	Goal              work.Goal
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
	if err := options.Budget.Validate(); err != nil {
		return Plan{}, err
	}
	if err := options.Limits.Validate(); err != nil {
		return Plan{}, err
	}
	if !options.Snapshot {
		return Plan{}, errSnapshotRequired
	}
	goal, err := app.RunnableGoal(options.Root, options.GoalID)
	if err != nil {
		return Plan{}, err
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
	decision, err := app.GoalDecision(options.Root, options.GoalID)
	if err != nil {
		return Plan{}, err
	}
	scope, err := app.CurrentGoalScope(options.Root, options.GoalID)
	if err != nil {
		return Plan{}, err
	}
	return Plan{Goal: goal, RuntimeName: runtime.Name(), RuntimeExecutable: executable,
		RuntimeVersion: version, Budget: options.Budget, Decision: decision, Scope: scope}, nil
}

var errSnapshotRequired = errors.New("--snapshot is required: this version verifies a working-tree snapshot and never commits for you")

// Start begins a new run. It takes the workspace Runner lock for the whole
// execution, so a second Runner — including one reaching the same repository
// through a symlinked path — is refused rather than allowed to overlap.
func Start(options Options) (Record, error) {
	if err := options.Budget.Validate(); err != nil {
		return Record{}, err
	}
	if err := options.Limits.Validate(); err != nil {
		return Record{}, err
	}
	if !options.Snapshot {
		return Record{}, errSnapshotRequired
	}
	var record Record
	err := storage.WithWorkspaceLock(options.Root, func() error {
		// The workspace lock proves no Runner is live. It proves nothing about the
		// workers a dead Runner launched: a SIGKILLed Runner releases its lock
		// while its coding CLI keeps writing this tree. Settling those before
		// anything new starts is what keeps `run` from doing the one thing
		// ADR-0020 promises it will not — create overlapping writers.
		blocked, err := settleAbandonedWorkers(options, "")
		if err != nil {
			return err
		}
		if blocked != nil {
			record = *blocked
			return nil
		}
		runner, err := newRunner(options)
		if err != nil {
			return err
		}
		defer func() { record = *runner.record }()
		return runner.loop()
	})
	return record, err
}

// settleAbandonedWorkers applies the recovery judgement to every earlier run
// that still claims a worker. It returns a record carrying a RECOVERY_BLOCKED
// stop when any of them cannot be settled, because starting a second writer on
// a workspace that may still have one is not a risk worth taking for the
// convenience of not having to name a run id. `except` names the run the caller
// settles itself: a resume recovers its own record through the Runner that is
// about to drive it, and settling it twice would judge it without one.
func settleAbandonedWorkers(options Options, except string) (*Record, error) {
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
			// answer that cannot be wrong.
			return blockedRecord(options, runID, fmt.Sprintf("run %s has an unreadable record: %v", runID, err)), nil
		}
		if existing.Worker == nil {
			continue
		}
		runner := &Runner{options: options, record: &existing}
		if err := runner.recover(); err != nil {
			return nil, err
		}
		if existing.Stop != nil && existing.Stop.Reason == StopRecoveryBlocked {
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
	return &Record{RunID: runID, GoalID: options.GoalID,
		Stop: &Stop{Reason: StopRecoveryBlocked, Detail: detail, At: at}}
}

// Resume continues a stopped run. It keeps the original workspace, Goal,
// runtime and consumed budget: a resume that reset either would turn every
// limit into a suggestion.
func Resume(options Options, runID string) (Record, error) {
	var record Record
	err := storage.WithWorkspaceLock(options.Root, func() error {
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
		blocked, err := settleAbandonedWorkers(resumeOptions(options, existing), runID)
		if err != nil {
			return err
		}
		if blocked != nil {
			record = *blocked
			return nil
		}
		runner := &Runner{options: resumeOptions(options, existing), record: &existing}
		runner.runtime, err = agent.Resolve(existing.RuntimeName, existing.RuntimeCommand)
		if err != nil {
			return err
		}
		defer func() { record = *runner.record }()
		// The previous stop is cleared before anything else: the loop treats a
		// recorded stop as terminal, so carrying one into a resume would report
		// the old reason again without doing any work.
		runner.record.Stop = nil
		if err := runner.recover(); err != nil {
			return err
		}
		return runner.loop()
	})
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
	runner := &Runner{options: options, runtime: runtime}
	startedAt := runner.now()
	runID, err := NewRunID(startedAt)
	if err != nil {
		return nil, err
	}
	scope, err := app.CurrentGoalScope(options.Root, options.GoalID)
	if err != nil {
		return nil, err
	}
	runner.record = &Record{
		RunID: runID, Workspace: root, GoalID: goal.ID, GoalTitle: goal.Title, Scope: scope,
		RuntimeName: runtime.Name(), RuntimeExecutable: executable, RuntimeVersion: version,
		RuntimeCommand: options.RuntimeCommand, Snapshot: options.Snapshot,
		Budget: options.Budget, Limits: options.Limits,
		StartedAt: startedAt, Deadline: startedAt.Add(options.Budget.MaxDuration),
		Attempts: map[string]int{},
	}
	if err := runner.record.save(options.Root, options.Limits, startedAt); err != nil {
		return nil, err
	}
	runner.print("Run %s started for goal %s (%s)\nRuntime: %s %s\nDeadline: %s\n",
		runID, goal.ID, goal.Title, runtime.Name(), version, runner.record.Deadline.Format(time.RFC3339))
	return runner, nil
}

// recover settles an interrupted run's worker before anything new is launched.
// Every branch either establishes that no writer remains or refuses to continue.
// See docs/adr/0020-worker-ownership-is-fail-closed.md.
func (runner *Runner) recover() error {
	worker := runner.record.Worker
	if worker == nil {
		return nil
	}
	liveness, err := agent.Inspect(worker.Identity)
	if err != nil {
		liveness = agent.Unknown
	}
	switch liveness {
	case agent.Ours:
		runner.print("Recovering run %s: stopping the worker left behind for %s (pid %d)\n", runner.record.RunID, worker.WorkItemID, worker.Identity.PID)
		if err := agent.TerminateOwned(worker.Identity); err != nil {
			return runner.stopNow(StopRecoveryBlocked, err.Error())
		}
	case agent.Gone, agent.Unrelated:
		runner.print("Recovering run %s: the worker for %s is gone\n", runner.record.RunID, worker.WorkItemID)
	default:
		// The worker record is deliberately left in place: clearing it would make
		// the next attempt look clean when nothing has actually been established.
		detail := fmt.Sprintf("cannot confirm whether the worker for %s (run %s, pid %d) is still writing this workspace",
			worker.WorkItemID, runner.record.RunID, worker.Identity.PID)
		if err != nil {
			detail += ": " + err.Error()
		}
		detail += fmt.Sprintf("; check that pid yourself, and once you are sure nothing is writing this tree, clear \"worker\" from %s",
			filepath.Join(".forgepilot", "runs", runner.record.RunID, recordName))
		return runner.stopNow(StopRecoveryBlocked, detail)
	}
	runner.record.Worker = nil
	return runner.save()
}

func (runner *Runner) save() error {
	return runner.record.save(runner.options.Root, runner.options.Limits, runner.now())
}

// stopNow records the terminal reason and returns nil: a stop is an outcome, not
// an error. Callers report it through the record's Stop field.
func (runner *Runner) stopNow(reason StopReason, detail string, evidence ...string) error {
	at := runner.now()
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
		if stopped, err := runner.checkLimits(); stopped || err != nil {
			return err
		}
		if stopped, err := runner.checkScope(); stopped || err != nil {
			return err
		}
		decision, err := app.GoalDecision(runner.options.Root, runner.options.GoalID)
		if err != nil {
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

func (runner *Runner) checkLimits() (bool, error) {
	if runner.record.Steps >= runner.options.Budget.MaxSteps {
		return true, runner.stopNow(StopMaxSteps, fmt.Sprintf("%d steps is the configured maximum", runner.options.Budget.MaxSteps))
	}
	if !runner.now().Before(runner.record.Deadline) {
		return true, runner.stopNow(StopMaxDuration,
			fmt.Sprintf("the run passed its deadline of %s; resuming does not extend it", runner.record.Deadline.Format(time.RFC3339)))
	}
	select {
	case <-runner.options.Stop:
		return true, runner.stopNow(StopInterrupted, "stopped on signal; resume this run to continue")
	default:
	}
	return false, nil
}

// checkScope refuses to keep driving a Goal whose Work Item set, Story
// references or dependencies changed underneath the run. The person authorized
// a particular piece of work, and silently absorbing new work is not that.
func (runner *Runner) checkScope() (bool, error) {
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
		"action="+string(decision.Action.Kind)+"/"+decision.Action.Item.ID,
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
	case work.NextActionWaitGoalReview:
		return runner.finish()
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
// as completion: only the Goal final-review projection may say that, and it is
// strictly stricter than this.
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
		// Every Work Item is VERIFIED or DONE, yet the final-review projection did
		// not agree. Something it checks — freshness, a Gate — does not hold.
		return runner.stopNow(StopStalled,
			"every work item is VERIFIED or DONE but the goal final-review conditions do not hold; run `forgepilot status` to see which")
	default:
		return runner.stopNow(StopStalled, stall.Detail)
	}
}

func (runner *Runner) start(action work.NextAction) error {
	if stopped, err := runner.spendStep(); stopped || err != nil {
		return err
	}
	if err := app.StartWork(runner.options.Root, action.Item.ID, runner.now); err != nil {
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
	if stopped, err := runner.spendStep(); stopped || err != nil {
		return err
	}
	changes, err := app.ReconcileGoal(runner.options.Root, runner.options.GoalID, runner.now)
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

// finish records the Goal final-review wait. It saves the exact Evidence the
// projection was judged on and stops. It never approves, never completes the
// Goal, and never turns VERIFIED into DONE.
func (runner *Runner) finish() error {
	summary, err := app.GoalReadiness(runner.options.Root, runner.options.GoalID)
	if err != nil {
		return err
	}
	if summary.Completion != work.GoalAwaitingFinalReview {
		// The projection disagreed with the recommendation between two reads. Fail
		// closed: never report readiness the projection does not currently assert.
		return runner.stopNow(StopStalled, fmt.Sprintf("goal %s is %s, not awaiting final review", runner.options.GoalID, summary.Completion))
	}
	runner.record.EvidenceIDs = summary.VerificationEvidenceIDs
	runner.print("Goal %s is awaiting final review. Evidence: %s\n", runner.options.GoalID, strings.Join(summary.VerificationEvidenceIDs, ", "))
	runner.print("This is not completion: a person still has to review and accept the goal.\n")
	return runner.stopNow(StopAwaitingGoalReview,
		"every work item is VERIFIED against the current candidate with no open gates", summary.VerificationEvidenceIDs...)
}

// verify runs the canonical check through the shared orchestration. Its typed
// result is what decides: an agent's claim, its exit code, and the check's own
// exit code are all irrelevant here — only recorded Evidence counts.
func (runner *Runner) verify(itemID, label string) error {
	if stopped, err := runner.spendStep(); stopped || err != nil {
		return err
	}
	runner.print("Step %d: %s %s\n", runner.record.Steps, label, itemID)
	result, err := app.Verify(context.Background(), runner.options.Root, itemID, runner.options.Output,
		app.VerifyOptions{Snapshot: runner.options.Snapshot, Timeout: runner.options.Budget.VerifyTimeout, Now: runner.now})
	if result.Reclaimed != nil {
		runner.record.EvidenceIDs = append(runner.record.EvidenceIDs, result.Reclaimed.ID)
	}
	if result.HasEvidence {
		runner.record.EvidenceIDs = append(runner.record.EvidenceIDs, result.Evidence.ID)
	}
	switch {
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
	case errors.Is(err, storage.ErrCapacityExceeded):
		return runner.stopNow(StopCapacityExceeded, err.Error())
	case err != nil:
		return err
	}
	detail := "no evidence"
	if result.HasEvidence {
		detail = fmt.Sprintf("%s %s", result.Evidence.ID, result.Evidence.Result)
	}
	return runner.journal(label, itemID, detail)
}

// implement launches one new agent session. Budget is charged and persisted
// before the process starts, so a crash between the two cannot be replayed into
// unlimited attempts.
func (runner *Runner) implement(action work.NextAction, decision app.Decision) error {
	itemID := action.Item.ID
	attempt := runner.record.Attempts[itemID] + 1
	if attempt > runner.options.Budget.MaxAttemptsPerWork {
		return runner.stopNow(StopMaxAttempts,
			fmt.Sprintf("%s has used its %d attempts in this run", itemID, runner.options.Budget.MaxAttemptsPerWork))
	}
	if runner.record.Steps >= runner.options.Budget.MaxSteps {
		return runner.stopNow(StopMaxSteps, fmt.Sprintf("%d steps is the configured maximum", runner.options.Budget.MaxSteps))
	}
	sessionDir, err := runner.sessionDirectory(itemID, attempt)
	if err != nil {
		return err
	}
	startedAt := runner.now()
	// The attempt and the step are charged, and the worker recorded, before the
	// process exists. A crash here costs one attempt; the alternative — charging
	// afterwards — costs an unbounded number.
	runner.record.Attempts[itemID] = attempt
	runner.record.Steps++
	runner.record.Worker = &Worker{WorkItemID: itemID, Attempt: attempt, SessionDir: sessionDir, StartedAt: startedAt}
	if stopped, err := runner.checkpoint(); stopped || err != nil {
		return err
	}

	handoff, err := runner.handoff(action, decision, attempt)
	if err != nil {
		// Nothing was launched, and this path knows that for certain, so the
		// worker record is withdrawn rather than left for recovery to puzzle over.
		runner.record.Worker = nil
		if saveErr := runner.save(); saveErr != nil {
			return saveErr
		}
		return err
	}
	// The session writes its own briefing and console log, so the room for them
	// is claimed before it starts rather than discovered afterwards.
	if err := storage.CheckRunCapacity(runner.options.Root, runner.record.RunID,
		sessionReservation(len(handoff), runner.options.Limits.MaxWriteBytes), runner.options.Limits); err != nil {
		runner.record.Worker = nil
		if saveErr := runner.save(); saveErr != nil {
			return saveErr
		}
		return runner.stopNow(StopCapacityExceeded, err.Error())
	}
	// The session is about to be given write access to the repository, and
	// `.forgepilot/` lives inside it. This is what tells us afterwards whether
	// the briefing's prohibition was honoured.
	stateBefore, digestErr := storage.StateDigest(runner.options.Root)
	if digestErr != nil {
		return digestErr
	}
	runner.print("Step %d: %s %s (attempt %d/%d, new session)\n", runner.record.Steps, action.Kind, itemID, attempt, runner.options.Budget.MaxAttemptsPerWork)
	request := agent.Request{Workspace: runner.record.Workspace, ArtifactDir: sessionDir,
		Handoff: handoff, MaxOutputBytes: runner.options.Limits.MaxWriteBytes}
	session, err := agent.Start(runner.runtime, request, startedAt)
	if err != nil {
		runner.record.Worker = nil
		if saveErr := runner.save(); saveErr != nil {
			return saveErr
		}
		return runner.stopNow(StopAgentExecutionFailed, fmt.Sprintf("%s: %v", itemID, err))
	}
	// The identity is written the moment it exists: a crash after this point is
	// recoverable, and one before it leaves a worker record with no identity,
	// which recovery treats as unconfirmable rather than as absent.
	incomplete := false
	runner.record.Worker.Identity = session.Started()
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
		if terminateErr := agent.TerminateOwned(session.Started()); terminateErr != nil {
			return runner.stopNow(StopRecoveryBlocked, fmt.Sprintf(
				"%s could not be recorded (%v) and its worker could not be stopped: %v. Confirm pid %d yourself before running anything else",
				itemID, err, terminateErr, session.Started().PID))
		}
		return err
	}
	// Journalled only once the identity is durable: an append that fails here
	// must not be the thing that leaves a live worker unrecorded.
	if incomplete {
		if err := runner.journal("AGENT", itemID, "worker identity could not be observed"); err != nil {
			return err
		}
	}

	result, waitErr := session.Wait(runner.options.Budget.AgentTimeout, runner.options.Stop)
	runner.record.Worker = nil
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
			"the session for %s changed .forgepilot/state.json, which it was told not to touch; nothing it reported can be trusted. Inspect the state before running anything else", itemID))
	}
	return runner.afterSession(itemID, attempt, result, waitErr)
}

func (runner *Runner) afterSession(itemID string, attempt int, result agent.Result, waitErr error) error {
	switch {
	case errors.Is(waitErr, agent.ErrStopped):
		return runner.stopNow(StopInterrupted,
			fmt.Sprintf("%s attempt %d was stopped on signal; its process group was terminated. Resume this run with `forgepilot run resume %s`", itemID, attempt, runner.record.RunID))
	case errors.Is(waitErr, agent.ErrTimedOut):
		return runner.stopNow(StopAgentTimeout, fmt.Sprintf("%s attempt %d: %v", itemID, attempt, waitErr))
	case agent.IsProtocolError(waitErr):
		if err := runner.recordAttempt(itemID, attempt, "protocol_error", waitErr.Error()); err != nil {
			return err
		}
		if runner.record.Stop != nil {
			return nil
		}
		if attempt >= runner.options.Budget.MaxAttemptsPerWork {
			return runner.stopNow(StopRuntimeProtocol, fmt.Sprintf("%s: %v", itemID, waitErr))
		}
		runner.print("  attempt %d returned no usable result: %v\n", attempt, waitErr)
		return runner.journal("AGENT", itemID, "protocol error")
	case waitErr != nil:
		return runner.stopNow(StopAgentExecutionFailed, fmt.Sprintf("%s attempt %d: %v", itemID, attempt, waitErr))
	}

	if err := runner.recordAttempt(itemID, attempt, string(result.Outcome), result.Summary); err != nil {
		return err
	}
	if runner.record.Stop != nil {
		return nil
	}
	switch result.Outcome {
	case agent.NeedsHuman:
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

func (runner *Runner) recordAttempt(itemID string, attempt int, outcome, summary string) error {
	runner.record.recordAttempt(Attempt{WorkItemID: itemID, Number: attempt, Outcome: outcome,
		Summary: summary, At: runner.now()})
	_, err := runner.checkpoint()
	return err
}

// spendStep charges one step against the budget and persists it before the work
// happens. Checking only at the top of the loop would let one iteration that
// does two things — implement, then verify — overshoot the ceiling it was given.
func (runner *Runner) spendStep() (bool, error) {
	if runner.record.Steps >= runner.options.Budget.MaxSteps {
		return true, runner.stopNow(StopMaxSteps, fmt.Sprintf("%d steps is the configured maximum", runner.options.Budget.MaxSteps))
	}
	runner.record.Steps++
	return runner.checkpoint()
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

func firstLine(text string) string {
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		return text[:index]
	}
	return text
}
