package runner

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/readiness"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestChargedRunDerivesArtifactLimitsFromAuthorization(t *testing.T) {
	root, runtimeCommand, _, now, _ := newUnresolvedExecutionRunnerFixture(t)
	identity := resolveExecutionIdentityForRunnerTest(t, root, now)
	options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
	options.Budget.MaxSteps = 1
	options.Budget.MaxHandoffBytes = 0
	options.Limits.MaxWriteBytes = 0
	options.Limits.MaxRunBytes = 0
	options.Limits.MaxTotalBytes = 0

	if _, err := Start(options); err != nil {
		t.Fatal(err)
	}
	runIDs, err := storage.ListRuns(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, runID := range runIDs {
		record, err := LoadRecord(root, runID)
		if err != nil {
			t.Fatal(err)
		}
		if record.Budget.MaxHandoffBytes != 64*1024 || record.Limits != testLimits() {
			t.Fatalf("charged run %s persisted caller artifact caps: budget=%#v limits=%#v", runID, record.Budget, record.Limits)
		}
	}
}

func TestAutomaticRolloverCreatesOneNewChargedRunOnlyAfterMaxStepsCleanup(t *testing.T) {
	root, runtimeCommand, _, now, _ := newUnresolvedExecutionRunnerFixture(t)
	identity := resolveExecutionIdentityForRunnerTest(t, root, now)
	options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
	options.Budget.MaxSteps = 1

	final, err := Start(options)
	if err != nil {
		t.Fatal(err)
	}
	if final.Stop == nil || final.Stop.Reason != StopMaxSteps {
		t.Fatalf("final rollover run = %#v; want MAX_STEPS", final)
	}
	runIDs, err := storage.ListRuns(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(runIDs) != 2 {
		t.Fatalf("automatic rollover runs = %v; want exactly the two authorized runs", runIDs)
	}
}

func TestAutomaticRolloverCreatesOneNewChargedRunOnlyAfterMaxDurationCleanup(t *testing.T) {
	root, runtimeCommand, sentinel, now, _ := newUnresolvedExecutionRunnerFixture(t)
	identity := resolveExecutionIdentityForRunnerTest(t, root, now)
	options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
	options.Budget.MaxDuration = time.Minute
	options.Budget.MaxSteps = 10
	options.Now = func() time.Time {
		contents, err := os.ReadFile(sentinel)
		if err == nil && strings.Count(string(contents), "launched\n") >= 2 {
			return now.Add(2 * options.Budget.MaxDuration)
		}
		if err == nil {
			return now.Add(options.Budget.MaxDuration)
		}
		return now
	}

	final, err := Start(options)
	if err != nil {
		t.Fatal(err)
	}
	if final.Stop == nil || final.Stop.Reason != StopMaxDuration {
		t.Fatalf("final rollover run = %#v; want MAX_DURATION", final)
	}
	runIDs, err := storage.ListRuns(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(runIDs) != 2 {
		t.Fatalf("automatic MAX_DURATION rollover runs = %v; want exactly two charged runs", runIDs)
	}
}

func TestAutomaticRolloverRejectsEveryOtherStopReason(t *testing.T) {
	for _, reason := range []StopReason{
		StopMaxAttempts, StopAgentTimeout, StopVerifyTimeout, StopNoProgress,
		StopCapacityExceeded, StopNeedsHuman, StopScopeChanged, StopRecoveryBlocked,
	} {
		t.Run(string(reason), func(t *testing.T) {
			runner := &Runner{record: &Record{Stop: &Stop{Reason: reason}}}
			eligible, err := runner.automaticRolloverEligible()
			if err != nil {
				t.Fatal(err)
			}
			if eligible {
				t.Fatalf("%s became eligible for automatic rollover", reason)
			}
		})
	}
}

func TestAutomaticRolloverRejectsExhaustedRecoveryCap(t *testing.T) {
	root, _, _, now, execution := newUnresolvedExecutionRunnerFixture(t)
	if err := storage.Update(root, func(state *work.State) error {
		_, err := state.PrepareExecutionReservation("g", work.ExecutionReservation{
			ID: "recovery-1", Kind: work.ExecutionReservationRecovery, RunID: "run-recovery", CreatedAt: now,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	allowed, err := app.AutomaticRolloverAllowed(root, "g", execution.Authorizations[0].Digest, now)
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("automatic rollover ignored an exhausted cumulative recovery cap")
	}
}

func TestAutomaticRolloverChecksTheActualNextTechnicalActionNode(t *testing.T) {
	root, _, _, now, _ := newUnresolvedExecutionRunnerFixture(t)
	writeReadyStory(t, root, "charged-second")
	readinessContents, err := os.ReadFile(root + "/specs/stories/charged-second/readiness.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Update(root, func(state *work.State) error {
		second, err := state.AddWork("g", "specs/stories/charged-second", nil, now)
		if err != nil {
			return err
		}
		goal, ok := state.GoalByID("g")
		if !ok || goal.Execution == nil {
			return errors.New("execution fixture Goal is missing")
		}
		binding := goal.Execution.PlanBindings[len(goal.Execution.PlanBindings)-1]
		binding.Revision++
		binding.PlanRevision++
		binding.Nodes = append(binding.Nodes, work.ExecutionPlanNodeBinding{
			PlanNodeRef: "node-2", StoryRef: second.StoryRef,
			ReadinessContract: work.ExecutionArtifactBinding{Path: "specs/stories/charged-second/readiness.json", SHA256: strings.TrimPrefix(readiness.Digest(readinessContents), "sha256:")},
			WorkItemID:        second.ID,
		})
		authorization := goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1]
		authorization.Revision++
		if err := state.ReviseExecution("g", binding, authorization); err != nil {
			return err
		}
		// Make node-1 the actual next action and exhaust only that node. The
		// unrelated node-2 still has capacity; a rollover must not use it as a
		// reason to create a run that cannot execute the next action.
		for index := range state.WorkItems {
			if state.WorkItems[index].ID == "WI-001" {
				state.WorkItems[index].Status = work.Running
			}
		}
		for attempt := 1; attempt <= authorization.Caps.MaxTechnicalAttemptsPerNode; attempt++ {
			if _, err := state.PrepareExecutionReservation("g", work.ExecutionReservation{
				ID: fmt.Sprintf("history:action:%d", attempt), Kind: work.ExecutionReservationAction,
				RunID: "history", PlanNodeRef: "node-1", CreatedAt: now,
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, ok := state.GoalByID("g")
	if !ok || goal.Execution == nil {
		t.Fatal("execution fixture Goal disappeared")
	}
	allowed, err := app.AutomaticRolloverAllowed(root, "g",
		goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1].Digest, now)
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("automatic rollover used capacity on an unrelated node after the next technical action was exhausted")
	}
}

func TestAutomaticRolloverCreateFailureReturnsThePriorRunWithoutPanicking(t *testing.T) {
	root, runtimeCommand, _, now, _ := newUnresolvedExecutionRunnerFixture(t)
	identity := resolveExecutionIdentityForRunnerTest(t, root, now)
	options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
	options.Budget.MaxSteps = 1
	creations := 0
	options.testNewRunner = func(options Options) (*Runner, error) {
		creations++
		if creations == 2 {
			return nil, errors.New("replacement run creation failed")
		}
		return newRunner(options)
	}

	final, err := Start(options)
	if err == nil || !strings.Contains(err.Error(), "replacement run creation failed") {
		t.Fatalf("rollover creation error = %v, want replacement failure", err)
	}
	if final.Stop == nil || final.Stop.Reason != StopMaxSteps {
		t.Fatalf("rollover creation failure lost the prior terminal run: %#v", final)
	}
}
