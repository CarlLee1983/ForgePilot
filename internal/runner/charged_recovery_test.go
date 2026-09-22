package runner

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestUnauthorisedGoalRefusesDirectAndLegacyExactRunBeforeWorkerLaunch(t *testing.T) {
	root, runtimeCommand, sentinel, now, execution := newUnresolvedExecutionRunnerFixture(t)
	if err := storage.Update(root, func(state *work.State) error {
		for index := range state.Goals {
			if state.Goals[index].ID == execution.GoalID {
				state.Goals[index].Execution = nil
				return nil
			}
		}
		return errors.New("unmanaged fixture Goal is missing")
	}); err != nil {
		t.Fatal(err)
	}
	options := chargedRunnerTestOptions(root, runtimeCommand, now, app.RunnerIdentity{})
	before, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Start(options); err == nil || !strings.Contains(err.Error(), "no current execution authorization") {
		t.Fatalf("unmanaged direct run = %v; want authorization refusal", err)
	}
	if runIDs, err := storage.ListRuns(root); err != nil || len(runIDs) != 0 {
		t.Fatalf("unmanaged direct run created Run Records: ids=%v err=%v", runIDs, err)
	}
	legacy := Record{RunID: "run-legacy", Workspace: root, GoalID: execution.GoalID, GoalTitle: "Goal",
		RuntimeName: "codex", RuntimeExecutable: runtimeCommand, RuntimeVersion: "test-codex 1", RuntimeCommand: runtimeCommand,
		Snapshot: true, Budget: testBudget(), Limits: testLimits(), StartedAt: now, Deadline: now.Add(testBudget().MaxDuration),
		Attempts: map[string]int{}, HumanWaits: map[string]int{}, Stop: &Stop{Reason: StopInterrupted, At: now}}
	if err := legacy.save(root, legacy.Limits, now); err != nil {
		t.Fatal(err)
	}
	beforeLegacy, err := os.ReadFile(filepath.Join(root, ".forgepilot", "runs", legacy.RunID, recordName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Resume(options, legacy.RunID); err == nil || !strings.Contains(err.Error(), "no current execution authorization") {
		t.Fatalf("legacy exact resume = %v; want authorization refusal", err)
	}
	afterLegacy, err := os.ReadFile(filepath.Join(root, ".forgepilot", "runs", legacy.RunID, recordName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeLegacy, afterLegacy) {
		t.Fatal("unauthorised exact resume rewrote its legacy Run Record")
	}
	after, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !equalJSON(t, before, after) {
		t.Fatal("unauthorised Runner admission mutated execution state")
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("unauthorised Runner admission launched a worker: %v", err)
	}
}

func TestLegacyRunIntentDoesNotAdoptAuthorizationAddedAfterCrash(t *testing.T) {
	root, runtimeCommand, sentinel, now, execution := newUnresolvedExecutionRunnerFixture(t)
	if err := storage.Update(root, func(state *work.State) error {
		for index := range state.Goals {
			if state.Goals[index].ID == execution.GoalID {
				state.Goals[index].Execution = nil
				return nil
			}
		}
		return errors.New("legacy fixture Goal is missing")
	}); err != nil {
		t.Fatal(err)
	}
	pending := Record{RunID: "run-legacy-intent", Workspace: root, GoalID: execution.GoalID, GoalTitle: "Goal",
		RuntimeName: "codex", RuntimeExecutable: runtimeCommand, RuntimeVersion: "test-codex 1", RuntimeCommand: runtimeCommand,
		Snapshot: true, RunPreparationState: RunPreparationPendingClassification, Budget: testBudget(), Limits: testLimits(),
		StartedAt: now, Deadline: now.Add(testBudget().MaxDuration), Attempts: map[string]int{}, HumanWaits: map[string]int{}}
	if err := pending.save(root, pending.Limits, now); err != nil {
		t.Fatal(err)
	}

	if err := storage.Update(root, func(state *work.State) error {
		return state.AdoptInitialExecution(execution)
	}); err != nil {
		t.Fatalf("add authorization after legacy Run intent: %v", err)
	}
	identity := resolveExecutionIdentityForRunnerTest(t, root, now)
	options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
	options.Budget.MaxSteps = 1
	before, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		label string
		run   func() (Record, error)
		want  string
	}{
		{"direct start", func() (Record, error) { return Start(options) }, "unfinished preparation"},
		{"exact resume", func() (Record, error) { return Resume(options, pending.RunID) }, "execution authorization changed after the Run Record intent was saved"},
	} {
		if _, err := test.run(); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%s error = %v; want refusal containing %q", test.label, err, test.want)
		}
		after, err := storage.Load(root)
		if err != nil {
			t.Fatal(err)
		}
		if !equalJSON(t, before, after) {
			t.Fatalf("%s mutated authorization or charged ledger after refusing stale intent", test.label)
		}
	}
	remainingRuns, err := storage.ListRuns(root)
	if err != nil || len(remainingRuns) != 1 || remainingRuns[0] != pending.RunID {
		t.Fatalf("stale intent refusal changed Run Records: ids=%v err=%v", remainingRuns, err)
	}
	goal, _ := before.GoalByID(execution.GoalID)
	if goal.Execution.Ledger.RunsConsumed != 0 || len(goal.Execution.Ledger.Reservations) != 0 {
		t.Fatalf("stale legacy intent received a charged retry: %#v", goal.Execution.Ledger)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("coding worker launched for an intent classified before authorization: %v", err)
	}
}

func TestChargedStartAndForgedResumeRefuseBeforeLaunchingWorker(t *testing.T) {
	root, runtimeCommand, sentinel, now, execution := newUnresolvedExecutionRunnerFixture(t)
	options := Options{Root: root, GoalID: execution.GoalID, RuntimeName: "codex", RuntimeCommand: runtimeCommand, Snapshot: true,
		Budget: testBudget(), Limits: testLimits(), Now: func() time.Time { return now }}
	before, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Start(options); err == nil || !strings.Contains(err.Error(), "not launchable until WorkerIdentity and engine generation are resolved") {
		t.Fatalf("charged Start error = %v, want fail-closed unresolved identity refusal", err)
	}
	afterStart, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !equalJSON(t, before, afterStart) {
		t.Fatal("refused charged Start mutated authorization or ledger state")
	}

	goal, _ := before.GoalByID(execution.GoalID)
	runID := "run-20260921t080000-abcdef"
	reservation := work.ExecutionReservation{ID: runID + ":run", Kind: work.ExecutionReservationRun, RunID: runID,
		CreatedAt: now, Status: work.ExecutionReservationPrepared}
	receipt, err := work.ExecutionReservationReceiptFor(reservation)
	if err != nil {
		t.Fatal(err)
	}
	forged := Record{
		RunID: runID, Workspace: root, GoalID: execution.GoalID, GoalTitle: "Goal", Scope: []string{"WI-001"},
		RuntimeName: "codex", RuntimeExecutable: runtimeCommand, RuntimeVersion: "test-codex 1", Snapshot: true,
		ExecutionAuthorizationDigest: goal.Execution.Authorizations[0].Digest, RunReservationID: reservation.ID,
		ReservationReceipts: []work.ExecutionReservationReceipt{receipt}, Budget: testBudget(), Limits: testLimits(),
		StartedAt: now, Deadline: now.Add(time.Hour), Attempts: map[string]int{}, HumanWaits: map[string]int{},
		Stop: &Stop{Reason: StopInterrupted, At: now},
	}
	if err := forged.save(root, forged.Limits, now); err != nil {
		t.Fatal(err)
	}
	if _, err := Resume(options, runID); err == nil || !strings.Contains(err.Error(), "no matching reservation in the current execution ledger") {
		t.Fatalf("forged exact Resume error = %v, want ledger-membership refusal", err)
	}
	afterResume, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !equalJSON(t, before, afterResume) {
		t.Fatal("refused exact Resume mutated authorization or ledger state")
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("coding worker was launched before charged admission: %v", err)
	}
}

func TestChargedRunChargeSurvivesRunnerProcessCrashBeforeRunRecord(t *testing.T) {
	root, runtimeCommand, sentinel, now, execution := newUnresolvedExecutionRunnerFixture(t)
	identity := resolveExecutionIdentityForRunnerTest(t, root, now)
	options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
	options.Budget.MaxSteps = 1

	runCrashHelper(t, root, runtimeCommand, sentinel, now, "after-run-charge", 1)

	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, _ := state.GoalByID(execution.GoalID)
	ledger := goal.Execution.Ledger
	if ledger.RunsConsumed != 1 || len(ledger.Reservations) != 1 || ledger.Reservations[0].Kind != work.ExecutionReservationRun {
		t.Fatalf("run charge did not survive the abrupt Runner exit: %#v", ledger)
	}
	runIDs, err := storage.ListRuns(root)
	if err != nil || len(runIDs) != 1 {
		t.Fatalf("durable Run Record intent after charge: ids=%v err=%v", runIDs, err)
	}
	pending, err := LoadRecord(root, runIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if pending.RunPreparationState != RunPreparationPendingCharge || pending.RunReservationID != pending.RunID+":run" ||
		len(pending.ReservationReceipts) != 0 {
		t.Fatalf("charged crash did not leave one recoverable Run intent: %#v", pending)
	}

	resumed, err := Resume(options, pending.RunID)
	if err != nil {
		t.Fatalf("exact retry of durable Run intent: %v", err)
	}
	if resumed.RunID != pending.RunID || resumed.RunPreparationState != "" || resumed.Stop == nil ||
		resumed.Stop.Reason != StopMaxSteps || resumed.Steps != 1 || len(resumed.ReservationReceipts) != 2 {
		t.Fatalf("recovered Run intent = %#v; want finalized charge and one START step", resumed)
	}
	stateAfterRetry, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goalAfterRetry, _ := stateAfterRetry.GoalByID(execution.GoalID)
	if goalAfterRetry.Execution.Ledger.RunsConsumed != 1 || goalAfterRetry.Execution.Ledger.StepsConsumed != 1 ||
		countReservations(goalAfterRetry.Execution.Ledger, work.ExecutionReservationRun) != 1 ||
		countReservations(goalAfterRetry.Execution.Ledger, work.ExecutionReservationStep) != 1 {
		t.Fatalf("exact retry did not finalize the original charge before its START step: %#v", goalAfterRetry.Execution.Ledger)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("coding worker launched before recovered Run charge reached its START budget: %v", err)
	}
}

func TestChargedRunIntentSurvivesRunnerProcessCrashBeforeRunCharge(t *testing.T) {
	root, runtimeCommand, sentinel, now, execution := newUnresolvedExecutionRunnerFixture(t)
	identity := resolveExecutionIdentityForRunnerTest(t, root, now)
	options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
	options.Budget.MaxSteps = 1

	runCrashHelper(t, root, runtimeCommand, sentinel, now, "after-run-intent", 1)

	runIDs, err := storage.ListRuns(root)
	if err != nil || len(runIDs) != 1 {
		t.Fatalf("durable Run Record intent before charge: ids=%v err=%v", runIDs, err)
	}
	pending, err := LoadRecord(root, runIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if pending.RunPreparationState != RunPreparationPendingCharge || pending.RunReservationID != pending.RunID+":run" ||
		len(pending.ReservationReceipts) != 0 {
		t.Fatalf("pre-charge crash did not leave one recoverable Run intent: %#v", pending)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, _ := state.GoalByID(execution.GoalID)
	if goal.Execution.Ledger.RunsConsumed != 0 || len(goal.Execution.Ledger.Reservations) != 0 {
		t.Fatalf("pre-charge crash unexpectedly consumed a RUN reservation: %#v", goal.Execution.Ledger)
	}

	resumed, err := Resume(options, pending.RunID)
	if err != nil {
		t.Fatalf("exact retry of pre-charge Run intent: %v", err)
	}
	if resumed.RunID != pending.RunID || resumed.RunPreparationState != "" || resumed.Stop == nil ||
		resumed.Stop.Reason != StopMaxSteps || resumed.Steps != 1 || len(resumed.ReservationReceipts) != 2 {
		t.Fatalf("recovered pre-charge Run intent = %#v; want one RUN charge and one START step", resumed)
	}
	state, err = storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, _ = state.GoalByID(execution.GoalID)
	if goal.Execution.Ledger.RunsConsumed != 1 || goal.Execution.Ledger.StepsConsumed != 1 ||
		countReservations(goal.Execution.Ledger, work.ExecutionReservationRun) != 1 ||
		countReservations(goal.Execution.Ledger, work.ExecutionReservationStep) != 1 {
		t.Fatalf("pre-charge exact retry did not charge the original Run intent exactly once: %#v", goal.Execution.Ledger)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("coding worker launched before recovered pre-charge intent reached its START budget: %v", err)
	}
}

func TestChargedArtifactReservationSurvivesCrashBeforeWorkerLaunch(t *testing.T) {
	root, runtimeCommand, sentinel, now, execution := newUnresolvedExecutionRunnerFixture(t)
	resolveExecutionIdentityForRunnerTest(t, root, now)
	runCrashHelper(t, root, runtimeCommand, sentinel, now, "after-artifact-charge", 2)

	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, ok := state.GoalByID(execution.GoalID)
	if !ok || goal.Execution == nil {
		t.Fatalf("charged Goal disappeared: %#v", goal)
	}
	ledger := goal.Execution.Ledger
	if ledger.ArtifactBytesConsumed <= 0 || len(ledger.ArtifactByteReservations) != 1 ||
		ledger.ArtifactByteReservations[0].RunID == "" {
		t.Fatalf("crash lost the durable pre-launch artifact reservation: %#v", ledger)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("worker launched before the artifact reservation crash boundary: %v", err)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("artifact-charged crash state is invalid: %v", err)
	}
}

func TestChargedPendingResumeRejectsOversizedArtifactContractBeforeCharging(t *testing.T) {
	root, runtimeCommand, sentinel, now, execution := newUnresolvedExecutionRunnerFixture(t)
	identity := resolveExecutionIdentityForRunnerTest(t, root, now)
	options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
	runCrashHelper(t, root, runtimeCommand, sentinel, now, "after-run-intent", 1)

	runIDs, err := storage.ListRuns(root)
	if err != nil || len(runIDs) != 1 {
		t.Fatalf("Run Record intent: ids=%v err=%v", runIDs, err)
	}
	record, err := LoadRecord(root, runIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if record.RunPreparationState != RunPreparationPendingCharge {
		t.Fatalf("Run Record preparation state = %q, want PENDING_CHARGE", record.RunPreparationState)
	}
	record.Limits.MaxWriteBytes *= 2
	if err := record.save(root, record.Limits, now); err != nil {
		t.Fatal(err)
	}
	beforeRecord, err := os.ReadFile(filepath.Join(root, ".forgepilot", "runs", record.RunID, recordName))
	if err != nil {
		t.Fatal(err)
	}
	beforeState, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Resume(options, record.RunID); err == nil || !strings.Contains(err.Error(), "artifact limits exceed") {
		t.Fatalf("Resume error = %v, want artifact-cap refusal before charging", err)
	}
	afterRecord, err := os.ReadFile(filepath.Join(root, ".forgepilot", "runs", record.RunID, recordName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterRecord, beforeRecord) {
		t.Fatal("artifact-cap refusal rewrote the pending Run Record")
	}
	afterState, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !equalJSON(t, afterState, beforeState) {
		t.Fatalf("artifact-cap refusal charged or changed the execution ledger: before=%#v after=%#v", beforeState, afterState)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("worker launched before pending artifact-cap refusal: %v", err)
	}
	goal, ok := afterState.GoalByID(execution.GoalID)
	if !ok || goal.Execution == nil || goal.Execution.Ledger.RunsConsumed != 0 {
		t.Fatalf("pending artifact-cap refusal consumed a RUN reservation: %#v", goal.Execution)
	}
}

func TestChargedExactResumeAfterRunRecordDoesNotSpendAnotherRun(t *testing.T) {
	root, runtimeCommand, sentinel, now, execution := newUnresolvedExecutionRunnerFixture(t)
	identity := resolveExecutionIdentityForRunnerTest(t, root, now)
	options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
	options.Budget.MaxSteps = 1
	runCrashHelper(t, root, runtimeCommand, sentinel, now, "after-run-record", 1)

	runIDs, err := storage.ListRuns(root)
	if err != nil || len(runIDs) != 1 {
		t.Fatalf("Run Record at resume boundary: ids=%v err=%v", runIDs, err)
	}
	initial, err := LoadRecord(root, runIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if initial.RunReservationID != initial.RunID+":run" || len(initial.ReservationReceipts) != 1 {
		t.Fatalf("charged Run Record does not carry its initial receipt: %#v", initial)
	}

	resumed, err := Resume(options, initial.RunID)
	if err != nil {
		t.Fatalf("exact resume after Run Record persistence: %v", err)
	}
	if resumed.RunID != initial.RunID || resumed.Stop == nil || resumed.Stop.Reason != StopMaxSteps || resumed.Steps != 1 {
		t.Fatalf("exact resume = run %s, stop %#v, steps %d", resumed.RunID, resumed.Stop, resumed.Steps)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, _ := state.GoalByID(execution.GoalID)
	if goal.Execution.Ledger.RunsConsumed != 1 || goal.Execution.Ledger.StepsConsumed != 1 {
		t.Fatalf("exact resume charged a replacement run or lost the START step: %#v", goal.Execution.Ledger)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("worker launched before the one-step run stopped: %v", err)
	}
}

// AC-001: direct execution and exact-run recovery share the same durable
// authorization. The first run starts a real worker; its exact resume keeps
// that run's immutable contract and does not create a replacement RUN charge.
func TestChargedDirectRunAndExactResumeUseTheSameAuthorization(t *testing.T) {
	root, runtimeCommand, sentinel, now, execution := newUnresolvedExecutionRunnerFixture(t)
	identity := resolveExecutionIdentityForRunnerTest(t, root, now)
	options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
	options.Budget.MaxSteps = 4
	t.Setenv("FORGEPILOT_TEST_CODEX_RESULT", `{"outcome":"needs_human","summary":"Need a decision","needs_human":{"question":"Choose an option","options":["one"],"context":""}}`)

	started, err := Start(options)
	if err != nil {
		t.Fatal(err)
	}
	if started.Stop == nil || started.Stop.Reason != StopNeedsHuman || started.Steps != 2 {
		t.Fatalf("direct charged run = stop %#v, steps %d; want a real worker followed by needs_human", started.Stop, started.Steps)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("direct charged run did not launch the real worker: %v", err)
	}
	if started.ExecutionAuthorizationDigest == "" || started.RunReservationID != started.RunID+":run" {
		t.Fatalf("direct charged run has no durable authorization binding: %#v", started)
	}

	resumed, err := Resume(options, started.RunID)
	if err != nil {
		t.Fatalf("exact resume: %v", err)
	}
	if resumed.RunID != started.RunID || resumed.ExecutionAuthorizationDigest != started.ExecutionAuthorizationDigest ||
		resumed.Deadline != started.Deadline || resumed.Budget != started.Budget {
		t.Fatalf("exact resume rewrote its authorization or run contract: started=%#v resumed=%#v", started, resumed)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, ok := state.GoalByID(execution.GoalID)
	if !ok || goal.Execution == nil || goal.Execution.Ledger.RunsConsumed != 1 ||
		goal.Execution.Ledger.StepsConsumed != 3 || countReservations(goal.Execution.Ledger, work.ExecutionReservationAction) != 2 {
		t.Fatalf("direct run and exact resume did not retain one durable charged history: %#v", goal.Execution)
	}
}

// AC-003: a Run Record that was already charged cannot be resumed as a legacy
// run if its current authorization disappears. The earlier direct worker is
// removed from the observation point so this assertion proves resume itself
// did not start another process.
func TestChargedExactResumeRefusesWhenCurrentAuthorizationIsMissing(t *testing.T) {
	root, runtimeCommand, sentinel, now, execution := newUnresolvedExecutionRunnerFixture(t)
	identity := resolveExecutionIdentityForRunnerTest(t, root, now)
	options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
	options.Budget.MaxSteps = 4
	t.Setenv("FORGEPILOT_TEST_CODEX_RESULT", `{"outcome":"needs_human","summary":"Need a decision","needs_human":{"question":"Choose an option","options":["one"],"context":""}}`)

	started, err := Start(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sentinel); err != nil {
		t.Fatalf("remove direct-worker observation: %v", err)
	}
	if err := storage.Update(root, func(state *work.State) error {
		for index := range state.Goals {
			if state.Goals[index].ID == execution.GoalID {
				state.Goals[index].Execution = nil
				return nil
			}
		}
		return errors.New("charged fixture Goal is missing")
	}); err != nil {
		t.Fatal(err)
	}
	before, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Resume(options, started.RunID); err == nil || !strings.Contains(err.Error(), "execution authorization") {
		t.Fatalf("charged exact resume without an authorization = %v; want refusal", err)
	}
	after, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !equalJSON(t, before, after) {
		t.Fatal("missing-authorization refusal rewrote the execution state")
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("missing-authorization resume launched a worker: %v", err)
	}
}

func TestChargedStepReservationReplaysAfterCrashBeforeRunRecordReceipt(t *testing.T) {
	root, runtimeCommand, sentinel, now, execution := newUnresolvedExecutionRunnerFixture(t)
	identity := resolveExecutionIdentityForRunnerTest(t, root, now)
	options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
	options.Budget.MaxSteps = 1
	runCrashHelper(t, root, runtimeCommand, sentinel, now, "after-step-charge", 1)

	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, _ := state.GoalByID(execution.GoalID)
	ledger := goal.Execution.Ledger
	if ledger.RunsConsumed != 1 || ledger.StepsConsumed != 1 || len(ledger.Reservations) != 2 {
		t.Fatalf("step charge did not survive the abrupt Runner exit: %#v", ledger)
	}
	runIDs, err := storage.ListRuns(root)
	if err != nil || len(runIDs) != 1 {
		t.Fatalf("Run Record at step-charge boundary: ids=%v err=%v", runIDs, err)
	}
	record, err := LoadRecord(root, runIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	const stepReservationIDSuffix = ":step:1:START:node-1"
	var chargedStep work.ExecutionReservation
	for _, reservation := range ledger.Reservations {
		if reservation.Kind == work.ExecutionReservationStep {
			chargedStep = reservation
		}
	}
	if chargedStep.ID != record.RunID+stepReservationIDSuffix {
		t.Fatalf("durable START step = %#v; want stable id %s%s", chargedStep, record.RunID, stepReservationIDSuffix)
	}
	if record.Steps != 0 || len(record.ReservationReceipts) != 1 ||
		findRunReceipt(record.ReservationReceipts, chargedStep.ID) != nil {
		t.Fatalf("Run Record crossed the crash point before step receipt/accounting: steps=%d receipts=%#v", record.Steps, record.ReservationReceipts)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("worker launched before the charged START transition: %v", err)
	}

	resumed, err := Resume(options, record.RunID)
	if err != nil {
		t.Fatalf("exact resume after step-charge crash: %v", err)
	}
	if resumed.RunID != record.RunID || resumed.Steps != 1 || resumed.Stop == nil || resumed.Stop.Reason != StopMaxSteps {
		t.Fatalf("resumed run = id %s, steps %d, stop %#v; want same run at its original one-step cap", resumed.RunID, resumed.Steps, resumed.Stop)
	}
	state, err = storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, _ = state.GoalByID(execution.GoalID)
	if goal.Execution.Ledger.RunsConsumed != 1 || goal.Execution.Ledger.StepsConsumed != 1 || len(goal.Execution.Ledger.Reservations) != 2 {
		t.Fatalf("step replay charged again or lost the original charge: %#v", goal.Execution.Ledger)
	}
	record, err = LoadRecord(root, record.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Steps != 1 || findRunReceipt(record.ReservationReceipts, chargedStep.ID) == nil {
		t.Fatalf("exact retry did not bind the replayed step receipt and count: steps=%d receipts=%#v", record.Steps, record.ReservationReceipts)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("worker launched despite the exhausted one-step cap: %v", err)
	}
}

func TestChargedActionReservationReplaysAcrossRunRecordReceiptBoundary(t *testing.T) {
	for _, test := range []struct {
		name                 string
		crashAt              string
		actionReceiptWritten bool
	}{
		{name: "ledger before action receipt", crashAt: "after-action-charge"},
		{name: "action receipt before step receipt", crashAt: "after-action-receipt", actionReceiptWritten: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			chargedActionCrashReplay(t, test.crashAt, test.actionReceiptWritten)
		})
	}
}

func chargedActionCrashReplay(t *testing.T, crashAt string, actionReceiptWritten bool) {
	t.Helper()
	root, runtimeCommand, sentinel, now, execution := newUnresolvedExecutionRunnerFixture(t)
	identity := resolveExecutionIdentityForRunnerTest(t, root, now)
	options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
	options.Budget.MaxSteps = 2

	runCrashHelper(t, root, runtimeCommand, sentinel, now, crashAt, 2)

	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, _ := state.GoalByID(execution.GoalID)
	counts := map[work.ExecutionReservationKind]int{}
	for _, reservation := range goal.Execution.Ledger.Reservations {
		counts[reservation.Kind]++
	}
	if counts[work.ExecutionReservationRun] != 1 || counts[work.ExecutionReservationStep] != 2 || counts[work.ExecutionReservationAction] != 1 {
		t.Fatalf("crash did not leave exactly one durable Run, START step, worker step, and Action charge: %#v", counts)
	}
	runIDs, err := storage.ListRuns(root)
	if err != nil || len(runIDs) != 1 {
		t.Fatalf("Run Record after action reservation: ids=%v err=%v", runIDs, err)
	}
	record, err := LoadRecord(root, runIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if record.Worker != nil || len(record.UnresolvedPending()) != 0 {
		t.Fatalf("crash boundary unexpectedly reached worker preparation: %#v", record)
	}
	actionReceipt := findRunReceipt(record.ReservationReceipts, runIDs[0]+":action:RESUME:node-1:1")
	if (actionReceipt != nil) != actionReceiptWritten {
		t.Fatalf("action receipt persisted at %s = %v; want %v: %#v", crashAt, actionReceipt != nil, actionReceiptWritten, record.ReservationReceipts)
	}

	resumed, err := Resume(options, record.RunID)
	if err != nil {
		t.Fatalf("exact resume after %s crash: %v", crashAt, err)
	}
	if resumed.Stop == nil || resumed.Steps != 2 {
		t.Fatalf("resumed run = stop %#v, steps %d; want the original two-step limit", resumed.Stop, resumed.Steps)
	}
	state, err = storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, _ = state.GoalByID(execution.GoalID)
	counts = map[work.ExecutionReservationKind]int{}
	for _, reservation := range goal.Execution.Ledger.Reservations {
		counts[reservation.Kind]++
	}
	if counts[work.ExecutionReservationAction] != 1 {
		t.Fatalf("exact retry charged the same technical attempt more than once: %#v", counts)
	}
	resumedRecord, err := LoadRecord(root, record.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if findRunReceipt(resumedRecord.ReservationReceipts, record.RunID+":action:RESUME:node-1:1") == nil {
		t.Fatalf("exact retry did not persist the receipt for its replayed Action: %#v", resumedRecord.ReservationReceipts)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("resumed charged action did not launch the real helper executable: %v", err)
	}
}

func TestChargedPendingAndLaunchedWorkerCrashesFailClosed(t *testing.T) {
	for _, test := range []struct {
		name           string
		crashAt        string
		workerLaunched bool
	}{
		{name: "pending persisted before launch", crashAt: "after-pending-save"},
		{name: "worker launched before identity persisted", crashAt: "after-worker-launch", workerLaunched: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, runtimeCommand, sentinel, now, execution := newUnresolvedExecutionRunnerFixture(t)
			identity := resolveExecutionIdentityForRunnerTest(t, root, now)
			options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
			options.Budget.MaxSteps = 2
			runCrashHelper(t, root, runtimeCommand, sentinel, now, test.crashAt, 2)

			runIDs, err := storage.ListRuns(root)
			if err != nil || len(runIDs) != 1 {
				t.Fatalf("Run Record after %s: ids=%v err=%v", test.crashAt, runIDs, err)
			}
			record, err := LoadRecord(root, runIDs[0])
			if err != nil {
				t.Fatal(err)
			}
			pending := record.UnresolvedPending()
			if len(pending) != 1 || pending[0].Kind != KindAgentSession || len(pending[0].ReservationReceipts) != 2 {
				t.Fatalf("charged worker Pending does not carry the action receipt: %#v", pending)
			}
			for _, pendingReceipt := range pending[0].ReservationReceipts {
				if findRunReceipt(record.ReservationReceipts, pendingReceipt.ReservationID) == nil {
					t.Fatalf("Pending receipt %q is absent from the Run Record receipt set: %#v", pendingReceipt.ReservationID, record.ReservationReceipts)
				}
			}
			if record.Worker == nil || record.Worker.Identity.Recorded() {
				t.Fatalf("crash point did not preserve an unconfirmed prospective worker: %#v", record.Worker)
			}
			if len(record.Worker.ReservationReceipts) != 2 || len(pending[0].ReservationReceipts) != 2 ||
				record.Worker.ReservationReceipts[0] != pending[0].ReservationReceipts[0] ||
				record.Worker.ReservationReceipts[1] != pending[0].ReservationReceipts[1] {
				t.Fatalf("Worker and Pending did not persist the same ACTION/STEP receipt pair: worker=%#v pending=%#v", record.Worker.ReservationReceipts, pending[0].ReservationReceipts)
			}
			if _, err := os.Stat(sentinel); test.workerLaunched && err != nil {
				t.Fatalf("helper subprocess did not launch the worker before crashing: %v", err)
			} else if !test.workerLaunched && !os.IsNotExist(err) {
				t.Fatalf("worker launched before the persisted Pending boundary: %v", err)
			}

			resumed, err := Resume(options, record.RunID)
			if err != nil {
				t.Fatalf("Resume did not convert the unconfirmed launch into a durable refusal: %v", err)
			}
			if resumed.Stop == nil || resumed.Stop.Reason != StopRecoveryBlocked {
				t.Fatalf("Resume after %s = stop %#v, want RECOVERY_BLOCKED", test.crashAt, resumed.Stop)
			}
			state, err := storage.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			goal, _ := state.GoalByID(execution.GoalID)
			if goal.Execution.Ledger.StepsConsumed != 2 || goal.Execution.Ledger.NodeAttempts[0].TechnicalAttempts != 1 {
				t.Fatalf("recovery refusal changed the charged action: %#v", goal.Execution.Ledger)
			}
			if test.workerLaunched {
				contents, err := os.ReadFile(sentinel)
				if err != nil || strings.TrimSpace(string(contents)) != "launched" {
					t.Fatalf("Resume duplicated or lost the worker launch: contents=%q err=%v", contents, err)
				}
			}
		})
	}
}

func TestChargedWorkerIdentityCrashRecoversWithoutDuplicateLaunch(t *testing.T) {
	root, runtimeCommand, sentinel, now, execution := newUnresolvedExecutionRunnerFixture(t)
	identity := resolveExecutionIdentityForRunnerTest(t, root, now)
	options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
	options.Budget.MaxSteps = 2
	runCrashHelper(t, root, runtimeCommand, sentinel, now, "after-worker-identity-save", 2)

	runIDs, err := storage.ListRuns(root)
	if err != nil || len(runIDs) != 1 {
		t.Fatalf("Run Record after identity checkpoint: ids=%v err=%v", runIDs, err)
	}
	crashed, err := LoadRecord(root, runIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	pending := crashed.UnresolvedPending()
	if crashed.Worker == nil || len(pending) != 1 || pending[0].Phase != PhaseRunning ||
		pending[0].Identity != crashed.Worker.Identity {
		t.Fatalf("crash did not persist matching Worker/Pending ownership: worker=%#v pending=%#v", crashed.Worker, pending)
	}
	if !crashed.Worker.Identity.Recorded() {
		resumed, err := Resume(options, crashed.RunID)
		if err != nil {
			t.Fatalf("Resume after incomplete worker identity: %v", err)
		}
		if resumed.Stop == nil || resumed.Stop.Reason != StopRecoveryBlocked {
			t.Fatalf("Resume after incomplete worker identity = stop %#v, want RECOVERY_BLOCKED", resumed.Stop)
		}
		return
	}

	resumed, err := Resume(options, crashed.RunID)
	if err != nil {
		t.Fatalf("Resume after durable worker identity: %v", err)
	}
	if resumed.Stop == nil {
		t.Fatalf("Resume did not settle the exact worker before honoring its exhausted budget: %#v", resumed)
	}
	if resumed.Stop.Reason == StopRecoveryBlocked {
		return
	}
	if resumed.Stop.Reason != StopMaxSteps || resumed.Worker != nil || len(resumed.UnresolvedPending()) != 0 {
		t.Fatalf("Resume did not settle the exact worker before honoring its exhausted budget: %#v", resumed)
	}
	contents, err := os.ReadFile(sentinel)
	if err != nil || strings.TrimSpace(string(contents)) != "launched" {
		t.Fatalf("Resume duplicated or lost worker launch: contents=%q err=%v", contents, err)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, _ := state.GoalByID(execution.GoalID)
	if goal.Execution.Ledger.StepsConsumed != 2 || goal.Execution.Ledger.NodeAttempts[0].TechnicalAttempts != 1 {
		t.Fatalf("recovery changed charged worker totals: %#v", goal.Execution.Ledger)
	}
}

func TestResumeAuthorizationRejectsAnchorFromAnotherWorkspace(t *testing.T) {
	root, _, _, now, _ := newUnresolvedExecutionRunnerFixture(t)
	other, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	anchor := Record{RunID: "run-foreign-anchor", Workspace: other, GoalID: "g",
		ExecutionAuthorizationDigest: "sha256:foreign", Attempts: map[string]int{}, HumanWaits: map[string]int{},
		StartedAt: now, Deadline: now.Add(time.Hour)}
	if err := anchor.save(root, storage.ArtifactLimits{}, now); err != nil {
		t.Fatal(err)
	}

	if _, err := ResumeAuthorization(Options{Root: root, Now: func() time.Time { return now }}, anchor.RunID); err == nil ||
		!strings.Contains(err.Error(), "belongs to workspace") {
		t.Fatalf("authorization resume error = %v; want foreign workspace refusal", err)
	}
}

func TestResumeAuthorizationGoalSelectsTheLatestStoppedChargedRun(t *testing.T) {
	root, runtimeCommand, _, now, execution := newUnresolvedExecutionRunnerFixture(t)
	identity := resolveExecutionIdentityForRunnerTest(t, root, now)
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, ok := state.GoalByID(execution.GoalID)
	if !ok || goal.Execution == nil {
		t.Fatal("resolved authorization fixture disappeared")
	}
	authorizationDigest := goal.Execution.Authorizations[0].Digest
	const runID = "run-20260922t090000-abcdef"
	runReservation, err := app.PrepareChargedRun(root, execution.GoalID, runID, identity, now)
	if err != nil {
		t.Fatal(err)
	}
	stepReservation, err := app.PrepareChargedStep(root, execution.GoalID, runID, 1, string(work.NextActionStart), "WI-001",
		authorizationDigest, identity, now)
	if err != nil {
		t.Fatal(err)
	}
	runReceipt, err := work.ExecutionReservationReceiptFor(runReservation)
	if err != nil {
		t.Fatal(err)
	}
	stepReceipt, err := work.ExecutionReservationReceiptFor(stepReservation)
	if err != nil {
		t.Fatal(err)
	}
	budget := testBudget()
	budget.MaxSteps = 1
	record := Record{RunID: runID, Workspace: root, GoalID: execution.GoalID, GoalTitle: "Goal", RuntimeName: "codex",
		RuntimeExecutable: runtimeCommand, RuntimeVersion: identity.Version, RuntimeCommand: runtimeCommand, Snapshot: true,
		ExecutionAuthorizationDigest: authorizationDigest, RunReservationID: runReservation.ID,
		ReservationReceipts: []work.ExecutionReservationReceipt{runReceipt, stepReceipt}, Budget: budget, Limits: testLimits(),
		StartedAt: now, Deadline: now.Add(time.Hour), UpdatedAt: now, Steps: 1, Attempts: map[string]int{}, HumanWaits: map[string]int{},
		Stop: &Stop{Reason: StopMaxSteps, At: now}}
	if err := record.save(root, record.Limits, now); err != nil {
		t.Fatal(err)
	}
	runIDs, err := storage.ListRuns(root)
	if err != nil || len(runIDs) != 1 || runIDs[0] != runID {
		t.Fatalf("stored continuation anchor is not listable: ids=%v err=%v", runIDs, err)
	}
	stored, err := LoadRecord(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Workspace != root || stored.GoalID != execution.GoalID || stored.ExecutionAuthorizationDigest == "" || stored.Stop == nil {
		t.Fatalf("stored continuation anchor is not eligible: %#v", stored)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Workspace != canonicalRoot {
		t.Fatalf("stored continuation anchor workspace differs from canonical root: %q / %q", stored.Workspace, canonicalRoot)
	}

	resumed, err := ResumeAuthorizationGoal(Options{Root: root, Now: func() time.Time { return now },
		testExecutionIdentity: &identity}, execution.GoalID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.RunID == runID || resumed.Stop == nil || resumed.Stop.Reason != StopMaxSteps || resumed.Steps != 1 {
		t.Fatalf("Goal-scoped continuation did not create a fresh bounded successor: %#v", resumed)
	}
	runIDs, err = storage.ListRuns(root)
	if err != nil || len(runIDs) != 2 {
		t.Fatalf("Goal-scoped continuation did not persist exactly one successor: ids=%v err=%v", runIDs, err)
	}
}

func TestChargedResumePreservesStopWhenAdmissionFailsBeforeReadiness(t *testing.T) {
	root, runtimeCommand, sentinel, now, execution := newUnresolvedExecutionRunnerFixture(t)
	identity := resolveExecutionIdentityForRunnerTest(t, root, now)
	options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
	runCrashHelper(t, root, runtimeCommand, sentinel, now, "after-run-record", 1)

	runIDs, err := storage.ListRuns(root)
	if err != nil || len(runIDs) != 1 {
		t.Fatalf("Run Record after charge: ids=%v err=%v", runIDs, err)
	}
	record, err := LoadRecord(root, runIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	originalStop := &Stop{Reason: StopInterrupted, Detail: "keep this stop until admission", At: now.Add(-time.Minute)}
	record.Stop = originalStop
	if err := record.save(root, options.Limits, now); err != nil {
		t.Fatal(err)
	}
	changedStory := filepath.Join(root, "specs", "stories", "charged", "story.md")
	if err := os.WriteFile(changedStory, []byte("# Changed after authorization\n"), 0644); err != nil {
		t.Fatal(err)
	}

	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, ok := state.GoalByID(execution.GoalID)
	if !ok || goal.Execution == nil || len(goal.Execution.Authorizations) != 1 {
		t.Fatalf("missing current execution authorization: %#v", goal)
	}
	expiredAt := goal.Execution.Authorizations[0].ExpiresAt.Add(time.Second)
	options.Now = func() time.Time { return expiredAt }
	if _, err := Resume(options, record.RunID); err == nil || !strings.Contains(err.Error(), "has expired") {
		t.Fatalf("Resume error = %v, want expired authorization before readiness", err)
	}

	after, err := LoadRecord(root, record.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Stop == nil || after.Stop.Reason != originalStop.Reason ||
		after.Stop.Detail != originalStop.Detail || !after.Stop.At.Equal(originalStop.At) {
		t.Fatalf("rejected charged resume changed the previous stop: got %#v want %#v", after.Stop, originalStop)
	}
}

func TestChargedHumanWaitUsesAnotherStepWithoutAnotherTechnicalAttempt(t *testing.T) {
	root, runtimeCommand, _, now, execution := newUnresolvedExecutionRunnerFixture(t)
	identity := resolveExecutionIdentityForRunnerTest(t, root, now)
	options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
	options.Budget.MaxSteps = 4
	t.Setenv("FORGEPILOT_TEST_CODEX_RESULT", `{"outcome":"needs_human","summary":"Need a decision","needs_human":{"question":"Choose an option","options":["one"],"context":""}}`)

	first, err := Start(options)
	if err != nil {
		t.Fatal(err)
	}
	if first.Stop == nil || first.Stop.Reason != StopNeedsHuman || first.Steps != 2 ||
		first.Attempts["WI-001"] != 1 || first.HumanWaits["WI-001"] != 1 {
		t.Fatalf("first charged session accounting = %#v", first)
	}

	second, err := Resume(options, first.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Stop == nil || second.Stop.Reason != StopNeedsHuman || second.Steps != 3 ||
		second.Attempts["WI-001"] != 2 || second.HumanWaits["WI-001"] != 2 {
		t.Fatalf("resumed charged session accounting = %#v", second)
	}
	if len(second.HumanWaitReservationIDs) != 2 ||
		second.HumanWaitReservationIDs[0] == second.HumanWaitReservationIDs[1] {
		t.Fatalf("confirmed needs_human sessions did not retain unique action receipts: %#v", second.HumanWaitReservationIDs)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, _ := state.GoalByID(execution.GoalID)
	if goal.Execution.Ledger.StepsConsumed != 3 || goal.Execution.Ledger.NodeAttempts[0].TechnicalAttempts != 0 ||
		countReservations(goal.Execution.Ledger, work.ExecutionReservationAction) != 2 ||
		countReservations(goal.Execution.Ledger, work.ExecutionReservationStep) != 3 ||
		len(goal.Execution.Ledger.NeedsHumanDispositions) != 2 {
		t.Fatalf("needs_human did not preserve distinct action/step/disposition accounting: %#v", goal.Execution.Ledger)
	}
	for _, reservationID := range second.HumanWaitReservationIDs {
		if findRunReceipt(second.ReservationReceipts, reservationID) == nil {
			t.Fatalf("needs_human receipt %q was not rehydrated from the ledger", reservationID)
		}
	}
}

func TestChargedHumanDispositionSurvivesCrashAndReconcilesOnce(t *testing.T) {
	for _, crashAt := range []string{"after-human-result-record", "after-human-disposition"} {
		t.Run(crashAt, func(t *testing.T) {
			root, runtimeCommand, sentinel, now, execution := newUnresolvedExecutionRunnerFixture(t)
			identity := resolveExecutionIdentityForRunnerTest(t, root, now)
			options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
			options.Budget.MaxSteps = 2
			t.Setenv("FORGEPILOT_TEST_CODEX_RESULT", `{"outcome":"needs_human","summary":"Need a decision","needs_human":{"question":"Choose an option","options":["one"],"context":""}}`)

			runCrashHelper(t, root, runtimeCommand, sentinel, now, crashAt, 2)

			runIDs, err := storage.ListRuns(root)
			if err != nil || len(runIDs) != 1 {
				t.Fatalf("Run Record after needs_human crash: ids=%v err=%v", runIDs, err)
			}
			crashed, err := LoadRecord(root, runIDs[0])
			if err != nil {
				t.Fatal(err)
			}
			if crashed.HumanWaits["WI-001"] != 0 || len(crashed.HumanWaitReservationIDs) != 0 {
				t.Fatalf("crash boundary unexpectedly recorded local human-wait accounting: %#v", crashed)
			}
			state, err := storage.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			goal, _ := state.GoalByID(execution.GoalID)
			wantDispositions := 1
			if crashAt == "after-human-result-record" {
				wantDispositions = 0
			}
			if len(goal.Execution.Ledger.NeedsHumanDispositions) != wantDispositions {
				t.Fatalf("crash at %s left %d dispositions; want %d", crashAt,
					len(goal.Execution.Ledger.NeedsHumanDispositions), wantDispositions)
			}
			if crashAt == "after-human-result-record" {
				if _, err := Start(options); err == nil || !strings.Contains(err.Error(), "resume that exact run") {
					t.Fatalf("new direct run = %v; want refusal until the recorded result is reconciled", err)
				}
				afterRefusal, err := storage.ListRuns(root)
				if err != nil || len(afterRefusal) != 1 {
					t.Fatalf("refused direct run left another intent: ids=%v err=%v", afterRefusal, err)
				}
			}

			resumed, err := Resume(options, crashed.RunID)
			if err != nil {
				t.Fatalf("resume after %s: %v", crashAt, err)
			}
			if resumed.RunID != crashed.RunID || resumed.Stop == nil || resumed.Stop.Reason != StopMaxSteps || resumed.Steps != 2 ||
				resumed.HumanWaits["WI-001"] != 1 || len(resumed.HumanWaitReservationIDs) != 1 {
				t.Fatalf("recovered human disposition was not rehydrated exactly once: %#v", resumed)
			}
			if findRunReceipt(resumed.ReservationReceipts, resumed.HumanWaitReservationIDs[0]) == nil {
				t.Fatalf("recovered human disposition receipt %q was not rehydrated", resumed.HumanWaitReservationIDs[0])
			}

			state, err = storage.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			goal, _ = state.GoalByID(execution.GoalID)
			if goal.Execution.Ledger.StepsConsumed != 2 || goal.Execution.Ledger.NodeAttempts[0].TechnicalAttempts != 0 ||
				countReservations(goal.Execution.Ledger, work.ExecutionReservationAction) != 1 ||
				countReservations(goal.Execution.Ledger, work.ExecutionReservationStep) != 2 ||
				len(goal.Execution.Ledger.NeedsHumanDispositions) != 1 {
				t.Fatalf("recovered human disposition duplicated an action or technical attempt: %#v", goal.Execution.Ledger)
			}

			retried, err := Resume(options, resumed.RunID)
			if err != nil {
				t.Fatalf("second resume after disposition reconciliation: %v", err)
			}
			if retried.HumanWaits["WI-001"] != 1 || len(retried.HumanWaitReservationIDs) != 1 || retried.Steps != 2 {
				t.Fatalf("second resume re-applied human disposition accounting: %#v", retried)
			}
		})
	}
}

func countReservations(ledger work.ExecutionLedger, kind work.ExecutionReservationKind) int {
	count := 0
	for _, reservation := range ledger.Reservations {
		if reservation.Kind == kind {
			count++
		}
	}
	return count
}

func runCrashHelper(t *testing.T, root, runtimeCommand, sentinel string, now time.Time, crashAt string, maxSteps int,
	allowLegacy ...bool) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestRunnerCrashHelper$")
	environment := []string{
		"FORGEPILOT_RUNNER_CRASH_ROOT=" + root,
		"FORGEPILOT_RUNNER_CRASH_RUNTIME=" + runtimeCommand,
		"FORGEPILOT_RUNNER_CRASH_AT=" + crashAt,
		"FORGEPILOT_RUNNER_CRASH_NOW=" + now.Format(time.RFC3339Nano),
		"FORGEPILOT_LAUNCH_SENTINEL=" + sentinel,
	}
	if maxSteps > 0 {
		environment = append(environment, "FORGEPILOT_RUNNER_CRASH_MAX_STEPS="+strconv.Itoa(maxSteps))
	}
	if len(allowLegacy) > 0 && allowLegacy[0] {
		environment = append(environment, "FORGEPILOT_RUNNER_CRASH_ALLOW_LEGACY=1")
	}
	command.Env = append(os.Environ(), environment...)
	output, err := command.CombinedOutput()
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 73 {
		t.Fatalf("crash helper result = %v (%s), want injected exit 73", err, output)
	}
	// command.Start only proves that the kernel accepted the child. At the
	// after-worker-launch boundary the parent intentionally exits before it can
	// wait, so give the separately scheduled helper a bounded chance to publish
	// the fixture's launch observation before the caller asserts that boundary.
	if crashAt == "after-worker-launch" {
		deadline := time.Now().Add(time.Second)
		for {
			if _, statErr := os.Stat(sentinel); statErr == nil {
				break
			} else if !os.IsNotExist(statErr) {
				t.Fatalf("stat crash helper worker sentinel: %v", statErr)
			}
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func findRunReceipt(receipts []work.ExecutionReservationReceipt, id string) *work.ExecutionReservationReceipt {
	for index := range receipts {
		if receipts[index].ReservationID == id {
			return &receipts[index]
		}
	}
	return nil
}

func TestRunnerCrashHelper(t *testing.T) {
	root := os.Getenv("FORGEPILOT_RUNNER_CRASH_ROOT")
	if root == "" {
		return
	}
	now, err := time.Parse(time.RFC3339Nano, os.Getenv("FORGEPILOT_RUNNER_CRASH_NOW"))
	if err != nil {
		t.Fatalf("parse crash-helper clock: %v", err)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, ok := state.GoalByID("g")
	if !ok {
		t.Fatal("crash helper has no Goal fixture")
	}
	identity := app.RunnerIdentity{Runtime: "codex", ExecutablePath: os.Getenv("FORGEPILOT_RUNNER_CRASH_RUNTIME"),
		Version: "test-codex 1", Sandbox: "workspace-write"}
	if goal.Execution == nil && os.Getenv("FORGEPILOT_RUNNER_CRASH_ALLOW_LEGACY") != "1" {
		t.Fatal("crash helper has no authorized Goal fixture")
	}
	if goal.Execution != nil {
		authorization := goal.Execution.Authorizations[0]
		identity.Model = authorization.WorkerProfile.Model
		identity.Effort = authorization.WorkerProfile.Effort
		identity.Sandbox = authorization.WorkerProfile.Sandbox
		identity.EngineGeneration = authorization.EngineGeneration
		identity.Version = authorization.WorkerIdentity.ReportedVersion
	}
	options := chargedRunnerTestOptions(root, os.Getenv("FORGEPILOT_RUNNER_CRASH_RUNTIME"), now, identity)
	if maxSteps := os.Getenv("FORGEPILOT_RUNNER_CRASH_MAX_STEPS"); maxSteps != "" {
		parsed, parseErr := strconv.Atoi(maxSteps)
		if parseErr != nil || parsed < 1 {
			t.Fatalf("parse crash-helper max steps %q: %v", maxSteps, parseErr)
		}
		options.Budget.MaxSteps = parsed
	}
	target := os.Getenv("FORGEPILOT_RUNNER_CRASH_AT")
	options.testCrashAt = func(point string) {
		if point == target {
			os.Exit(73)
		}
	}
	if _, err := Start(options); err != nil {
		t.Errorf("Runner.Start before crash point: %v", err)
		os.Exit(74)
	}
}

func newUnresolvedExecutionRunnerFixture(t *testing.T) (root, runtimeCommand, sentinel string, now time.Time, execution work.GoalExecution) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	completionRecoveryGit(t, root, "init", "-q")
	completionRecoveryGit(t, root, "config", "user.name", "ForgePilot test")
	completionRecoveryGit(t, root, "config", "user.email", "forgepilot-test@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("baseline\n"), 0644); err != nil {
		t.Fatal(err)
	}
	completionRecoveryGit(t, root, "add", "tracked.txt")
	completionRecoveryGit(t, root, "commit", "-m", "baseline")
	if err := storage.Init(root); err != nil {
		t.Fatal(err)
	}
	writeReadyStory(t, root, "charged")

	runtimeCommand = filepath.Join(root, "test-codex")
	sentinel = filepath.Join(root, "worker-launched")
	t.Setenv("FORGEPILOT_LAUNCH_SENTINEL", sentinel)
	runtimeContents := []byte(`#!/bin/sh
if [ "$1" = "--version" ]; then printf 'test-codex 1\n'; exit 0; fi
result=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--output-last-message" ]; then shift; result="$1"; fi
  shift
done
	printf 'launched\n' >> "$FORGEPILOT_LAUNCH_SENTINEL"
	if [ "$FORGEPILOT_RUNNER_CRASH_AT" = "after-worker-launch" ] || [ "$FORGEPILOT_RUNNER_CRASH_AT" = "after-worker-identity-save" ]; then
	  exec sleep 5
	fi
if [ -n "$FORGEPILOT_TEST_CODEX_RESULT" ] && [ -n "$result" ]; then
  printf '%s' "$FORGEPILOT_TEST_CODEX_RESULT" > "$result"
fi
exit 0
`)
	if err := os.WriteFile(runtimeCommand, runtimeContents, 0755); err != nil {
		t.Fatal(err)
	}
	runtimeDigest := sha256.Sum256(runtimeContents)
	runtimeSHA256 := hex.EncodeToString(runtimeDigest[:])
	now = time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)
	artifactDigest := func(contents []byte) string {
		sum := sha256.Sum256(contents)
		return hex.EncodeToString(sum[:])
	}
	readinessContents, err := os.ReadFile(filepath.Join(root, "specs/stories/charged/readiness.json"))
	if err != nil {
		t.Fatal(err)
	}
	artifacts := map[string][]byte{
		"manifest.json":         []byte("charged manifest"),
		"coverage-review.json":  []byte("charged coverage review"),
		"specs/plans/plan.json": []byte("charged declaration"),
	}
	for path, contents := range artifacts {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), contents, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := storage.Update(root, func(state *work.State) error {
		if err := state.AddGoalWithPolicies("g", "Goal", "", root, work.ReviewPerGoal, work.CompletionVerified, now); err != nil {
			return err
		}
		item, err := state.AddWork("g", "specs/stories/charged", nil, now)
		if err != nil {
			return err
		}
		requestDigest := "sha256:" + artifactDigest([]byte("charged request"))
		binding := work.GoalPlanBinding{
			Revision: 1, RequestSHA256: requestDigest, PlanID: "plan-1", PlanRevision: 1,
			ManifestSHA256: artifactDigest(artifacts["manifest.json"]), Manifest: work.ExecutionArtifactBinding{Path: "manifest.json", SHA256: artifactDigest(artifacts["manifest.json"])}, CoverageReviewID: "review-1",
			CoverageReview: work.ExecutionArtifactBinding{Path: "coverage-review.json", SHA256: artifactDigest(artifacts["coverage-review.json"])},
			Declaration:    work.ExecutionArtifactBinding{Path: "specs/plans/plan.json", SHA256: artifactDigest(artifacts["specs/plans/plan.json"])},
			Nodes: []work.ExecutionPlanNodeBinding{{PlanNodeRef: "node-1", StoryRef: item.StoryRef,
				ReadinessContract: work.ExecutionArtifactBinding{Path: "specs/stories/charged/readiness.json", SHA256: artifactDigest(readinessContents)}, WorkItemID: item.ID}},
			AdoptedAt: now,
		}
		execution = work.GoalExecution{
			GoalID: "g", Workspace: root, PlanBindings: []work.GoalPlanBinding{binding},
			Authorizations: []work.ExecutionAuthorization{{
				Revision: 1, GoalID: "g", Workspace: root, RequestSHA256: requestDigest, ApprovalToken: requestDigest,
				Approver: "operator", AuthorizedAt: now, ExpiresAt: now.Add(time.Hour),
				Caps: work.ExecutionCaps{MaxSteps: 4, MaxTechnicalAttemptsPerNode: 2, MaxRuns: 2, MaxRecoveries: 1,
					Artifacts: work.ExecutionArtifactLimits{MaxHandoffBytes: 64 * 1024, MaxWriteBytes: 1 << 20, MaxRunBytes: 16 << 20, MaxTotalBytes: 128 << 20}},
				WorkerProfile: work.WorkerProfile{Runtime: "codex", ExecutablePath: runtimeCommand, ExecutableSHA256: runtimeSHA256,
					Model: "test-model", Effort: "medium", Sandbox: "workspace-write"},
			}},
			Ledger: work.ExecutionLedger{Revision: 1, AuthorizationRevision: 1, NodeAttempts: []work.ExecutionNodeAttempts{{PlanNodeRef: "node-1"}}},
		}
		return state.AdoptInitialExecution(execution)
	}); err != nil {
		t.Fatal(err)
	}
	return root, runtimeCommand, sentinel, now, execution
}

func resolveExecutionIdentityForRunnerTest(t *testing.T, root string, now time.Time) app.RunnerIdentity {
	t.Helper()
	var identity app.RunnerIdentity
	err := storage.Update(root, func(state *work.State) error {
		for goalIndex := range state.Goals {
			goal := &state.Goals[goalIndex]
			if goal.ID != "g" || goal.Execution == nil {
				continue
			}
			execution := goal.Execution
			authorization := &execution.Authorizations[0]
			authorization.WorkerIdentity = &work.ResolvedWorkerIdentity{
				ExecutablePath: authorization.WorkerProfile.ExecutablePath, ExecutableSHA256: authorization.WorkerProfile.ExecutableSHA256,
				ReportedVersion: "test-codex 1", ObservedAt: now,
			}
			generation := &work.ExecutionEngineGeneration{SourceCommit: "test-engine", PayloadSHA256: strings.Repeat("b", 64)}
			authorization.EngineGeneration = generation
			authorization.Digest = ""
			authorization.Digest = executionDigestForRunnerTest(t, "forgepilot.execution-authorization/v1", *authorization)
			execution.Witness.AuthorizationDigest = authorization.Digest
			execution.Witness.Digest = ""
			execution.Witness.Digest = executionDigestForRunnerTest(t, "forgepilot.goal-execution-witness/v1", execution.Witness)
			identity = app.RunnerIdentity{Runtime: authorization.WorkerProfile.Runtime,
				ExecutablePath: authorization.WorkerProfile.ExecutablePath, Version: authorization.WorkerIdentity.ReportedVersion,
				Model: authorization.WorkerProfile.Model, Effort: authorization.WorkerProfile.Effort,
				Sandbox: authorization.WorkerProfile.Sandbox, EngineGeneration: generation}
			return nil
		}
		return errors.New("Goal execution fixture was not found")
	})
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func chargedRunnerTestOptions(root, runtimeCommand string, now time.Time, identity app.RunnerIdentity) Options {
	budget := testBudget()
	return Options{Root: root, GoalID: "g", RuntimeName: "codex", RuntimeCommand: runtimeCommand, Snapshot: true,
		Budget: budget, Limits: testLimits(), Now: func() time.Time { return now }, testExecutionIdentity: &identity}
}

func executionDigestForRunnerTest(t *testing.T, domain string, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(encoded)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func equalJSON(t *testing.T, left, right any) bool {
	t.Helper()
	leftBytes, err := json.Marshal(left)
	if err != nil {
		t.Fatal(err)
	}
	rightBytes, err := json.Marshal(right)
	if err != nil {
		t.Fatal(err)
	}
	return string(leftBytes) == string(rightBytes)
}
