package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/control"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestChargedWorkerStopPersistsPauseAndPreservesWork(t *testing.T) {
	root, runtimeCommand, sentinel, now, _ := newUnresolvedExecutionRunnerFixture(t)
	_ = resolveExecutionIdentityForRunnerTest(t, root, now)
	revision := strings.TrimSpace(completionRecoveryGit(t, root, "rev-parse", "HEAD"))
	if err := storage.Update(root, func(state *work.State) error {
		if err := state.AddGoalWithPolicies("historical", "Historical Goal", "", root, work.ReviewPerGoal, work.CompletionVerified, now); err != nil {
			return err
		}
		item, err := state.AddWork("historical", "specs/stories/charged", nil, now)
		if err != nil {
			return err
		}
		if err := state.Start(item.ID, now); err != nil {
			return err
		}
		if err := state.BeginVerification(item.ID, revision, root, filepath.Join(root, "prior-verification.log"), now); err != nil {
			return err
		}
		_, err = state.RecordVerification(item.ID, revision, "make verify", 1, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	stopObservation := filepath.Join(root, "worker-stop-observation")
	workFile := filepath.Join(root, "tracked.txt")
	command := exec.Command(os.Args[0], "-test.run=^TestRunnerCrashHelper$")
	command.Env = append(os.Environ(),
		"FORGEPILOT_RUNNER_CRASH_ROOT="+root,
		"FORGEPILOT_RUNNER_CRASH_RUNTIME="+runtimeCommand,
		"FORGEPILOT_RUNNER_CRASH_AT=never",
		"FORGEPILOT_RUNNER_CRASH_NOW="+now.Format(time.RFC3339Nano),
		"FORGEPILOT_LAUNCH_SENTINEL="+sentinel,
		"FORGEPILOT_TEST_CODEX_HOLD=1",
		"FORGEPILOT_TEST_WORK_FILE="+workFile,
		"FORGEPILOT_TEST_CONTROL_FILE="+filepath.Join(root, ".forgepilot", "execution-control.json"),
		"FORGEPILOT_TEST_STOP_OBSERVATION="+stopObservation,
	)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	var live Record
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		runIDs, err := storage.ListRuns(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(runIDs) == 1 {
			live, err = LoadRecord(root, runIDs[0])
			if errors.Is(err, os.ErrNotExist) {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if live.Worker != nil && live.Worker.Identity.Recorded() {
				workBytes, readErr := os.ReadFile(workFile)
				if _, err := os.Stat(sentinel); err == nil && readErr == nil && bytes.Contains(workBytes, []byte("worker edit")) {
					break
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if live.Worker == nil || !live.Worker.Identity.Recorded() {
		journal, _ := os.ReadFile(filepath.Join(root, ".forgepilot", "runs", live.RunID, journalName))
		t.Fatalf("charged worker never became observable: steps=%d pending=%#v worker=%#v stop=%#v history=%#v journal=%s; helper: %s", live.Steps, live.Pending, live.Worker, live.Stop, live.History, journal, output.String())
	}
	stateBefore, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, _ := stateBefore.GoalByID("g")
	if goal.Execution.Ledger.NodeAttempts[0].TechnicalAttempts != 1 || stateBefore.WorkItems[0].Status != work.Running || len(stateBefore.Evidence) != 1 {
		t.Fatalf("stop did not target a live charged Work Item: %#v", stateBefore)
	}
	result, err := RequestStopResult(root, "g", "operator", "inspect work", now.Add(time.Second))
	if err != nil || !result.CleanupConfirmed {
		t.Fatalf("live stop = %#v, %v", result, err)
	}
	controlState, err := control.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if controlState.Pause == nil || controlState.Pause.RunID != live.RunID || controlState.Pause.Reason != "inspect work" {
		t.Fatalf("stop intent was not durable: %#v", controlState.Pause)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("Runner did not finish after worker stop: %v; output: %s", err, output.String())
	}
	if observed, err := os.ReadFile(stopObservation); err != nil || !bytes.Contains(observed, []byte("pause visible before termination")) {
		t.Fatalf("worker did not observe durable pause when terminated: observation=%q error=%v", observed, err)
	}
	stopped, err := LoadRecord(root, live.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Stop == nil || stopped.Stop.Reason != StopUserPaused || stopped.Worker != nil || len(stopped.UnresolvedPending()) != 0 {
		t.Fatalf("stopped run retained unsafe worker ownership: %#v", stopped)
	}
	stateAfter, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !equalJSON(t, stateBefore, stateAfter) {
		t.Fatal("stop changed Work Item, Evidence, or execution ledger state")
	}
	if _, err := os.Stat(filepath.Join(root, ".forgepilot", "runs", live.RunID, journalName)); err != nil {
		t.Fatalf("stop lost the run journal: %v", err)
	}
	if sessionLog, err := os.ReadFile(filepath.Join(live.Worker.SessionDir, "session.log")); err != nil || !bytes.Contains(sessionLog, []byte("session marker")) {
		t.Fatalf("stop lost the session log: contents=%q error=%v", sessionLog, err)
	}
	if workBytes, err := os.ReadFile(workFile); err != nil || !bytes.Contains(workBytes, []byte("worker edit")) {
		t.Fatalf("stop lost the worker's file edit: contents=%q error=%v", workBytes, err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("stop lost the worker's artifact: %v", err)
	}
}

func TestExternalWaitDeclarationNeedsFreshProcessExplicitResume(t *testing.T) {
	root, runtimeCommand, sentinel, now, _ := newUnresolvedExecutionRunnerFixture(t)
	identity := resolveExecutionIdentityForRunnerTest(t, root, now)
	options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
	t.Setenv("FORGEPILOT_TEST_CODEX_RESULT", `{"outcome":"needs_human","summary":"Waiting for deployment","needs_human":{"question":"Has deployment finished?","options":null,"context":null,"external_fact":"deployment_finished"}}`)
	first, err := Start(options)
	if err != nil {
		t.Fatal(err)
	}
	if first.Stop == nil || first.Stop.Reason != StopNeedsHuman {
		t.Fatalf("external result stop = %#v, want NEEDS_HUMAN", first.Stop)
	}
	controlState, err := control.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if controlState.Pause == nil || len(controlState.Waits) != 1 || controlState.Waits[0].Kind != control.WaitExternal {
		t.Fatalf("Codex external fact did not reach a durable external wait: %#v", controlState)
	}
	wait := controlState.Waits[0]
	request := app.ExternalFulfillmentDeclarationRequest{FormatVersion: "forgepilot.external-fulfillment-declaration/v1",
		GoalID: "g", WaitID: wait.ID, Fact: wait.ExpectedFact, DeclaredBy: "operator"}
	contents, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "declaration.json"), contents, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DeclareExternalFulfillmentFile(context.Background(), root, "declaration.json"); err != nil {
		t.Fatal(err)
	}
	controlState, err = control.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if controlState.Pause == nil || len(controlState.ExternalDeclarations) != 1 {
		t.Fatalf("declaration implicitly resumed work: %#v", controlState)
	}
	before, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(root, "specs", "plans", "plan.json")
	plan, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, []byte("changed plan"), 0644); err != nil {
		t.Fatal(err)
	}
	issue56ResumeInFreshProcess(t, root, runtimeCommand, now, "execution binding drift")
	blockedControl, err := control.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if blockedControl.Pause == nil || blockedControl.Pause.WaitID != wait.ID {
		t.Fatalf("stale plan cleared the declared wait: %#v", blockedControl)
	}
	after, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !equalJSON(t, before, after) {
		t.Fatal("stale-plan resume changed the Goal ledger or lifecycle")
	}
	if err := os.WriteFile(planPath, plan, 0644); err != nil {
		t.Fatal(err)
	}
	issue56ResumeInFreshProcess(t, root, runtimeCommand, now, "")
	resumedControl, err := control.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if resumedControl.Pause == nil || resumedControl.Pause.WaitID == wait.ID || len(resumedControl.Waits) != 2 {
		t.Fatalf("explicit resume reused the old declaration or failed to reach a new wait: %#v", resumedControl)
	}
	if len(resumedControl.ExternalDeclarations) != 1 {
		t.Fatalf("resume duplicated the external declaration: %#v", resumedControl.ExternalDeclarations)
	}
	secondWait := resumedControl.Waits[1]
	request.WaitID = secondWait.ID
	contents, err = json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "declaration.json"), contents, 0600); err != nil {
		t.Fatal(err)
	}
	secondDeclaration, err := app.DeclareExternalFulfillmentFile(context.Background(), root, "declaration.json")
	if err != nil {
		t.Fatal(err)
	}
	if secondDeclaration.WaitID != secondWait.ID || secondDeclaration.NodeID != secondWait.NodeID ||
		secondDeclaration.PlanDigest != secondWait.PlanDigest || secondDeclaration.AuthorizationRevision != secondWait.AuthorizationRevision ||
		secondDeclaration.AuthorizationDigest != secondWait.AuthorizationDigest {
		t.Fatalf("external declaration lost its exact wait binding: %#v", secondDeclaration)
	}
	if err := storage.Update(root, func(state *work.State) error {
		goal, _ := state.GoalByID("g")
		binding := goal.Execution.PlanBindings[0]
		binding.Revision = 2
		binding.PlanRevision = 2
		authorization := goal.Execution.Authorizations[0]
		authorization.Revision = 2
		authorization.AuthorizedAt = now.Add(time.Second)
		return state.ReviseExecution("g", binding, authorization)
	}); err != nil {
		t.Fatal(err)
	}
	beforeStaleResume, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	launchesBefore, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatal(err)
	}
	issue56ResumeInFreshProcess(t, root, runtimeCommand, now, "stale after a plan or authorization change")
	afterStaleResume, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !equalJSON(t, beforeStaleResume, afterStaleResume) {
		t.Fatal("stale declaration resume changed the Goal ledger or lifecycle")
	}
	launchesAfter, err := os.ReadFile(sentinel)
	if err != nil || !bytes.Equal(launchesBefore, launchesAfter) {
		t.Fatalf("stale declaration resume launched a worker: before=%q after=%q error=%v", launchesBefore, launchesAfter, err)
	}
	staleControl, err := control.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if staleControl.Pause == nil || staleControl.Pause.WaitID != secondWait.ID {
		t.Fatalf("changed authorization reused a stale declaration: %#v", staleControl)
	}
}

func TestUncertainChargedWorkerBlocksFreshProcessResume(t *testing.T) {
	root, runtimeCommand, sentinel, now, _ := newUnresolvedExecutionRunnerFixture(t)
	_ = resolveExecutionIdentityForRunnerTest(t, root, now)
	runCrashHelper(t, root, runtimeCommand, sentinel, now, "after-pending-save", 2)
	runIDs, err := storage.ListRuns(root)
	if err != nil || len(runIDs) != 1 {
		t.Fatalf("crashed run IDs = %v, %v", runIDs, err)
	}
	if _, err := RequestStopResult(root, "g", "operator", "hold for recovery", now.Add(time.Second)); err == nil || !strings.Contains(err.Error(), "could not be settled") {
		t.Fatalf("uncertain worker stop = %v, want durable pause and fail-closed ownership", err)
	}
	issue56ResumeInFreshProcess(t, root, runtimeCommand, now, "STOP:RECOVERY_BLOCKED")
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, _ := state.GoalByID("g")
	if goal.Execution.Ledger.NodeAttempts[0].TechnicalAttempts != 1 || len(goal.Execution.Ledger.NeedsHumanDispositions) != 0 {
		t.Fatalf("uncertain ownership changed the charged attempt: %#v", goal.Execution.Ledger)
	}
	controlState, err := control.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if controlState.Pause == nil || controlState.Pause.RunID != runIDs[0] {
		t.Fatalf("fresh-process recovery cleared the stop intent: %#v", controlState.Pause)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("fresh-process recovery launched another worker: %v", err)
	}
}

func issue56ResumeInFreshProcess(t *testing.T, root, runtimeCommand string, now time.Time, expectedError string) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestIssue56FreshResumeHelper$")
	command.Env = append(os.Environ(),
		"FORGEPILOT_ISSUE56_RESUME_ROOT="+root,
		"FORGEPILOT_ISSUE56_RESUME_RUNTIME="+runtimeCommand,
		"FORGEPILOT_ISSUE56_RESUME_NOW="+now.Format(time.RFC3339Nano),
		"FORGEPILOT_ISSUE56_EXPECT_ERROR="+expectedError,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fresh-process resume = %v: %s", err, output)
	}
}

func TestIssue56FreshResumeHelper(t *testing.T) {
	root := os.Getenv("FORGEPILOT_ISSUE56_RESUME_ROOT")
	if root == "" {
		return
	}
	now, err := time.Parse(time.RFC3339Nano, os.Getenv("FORGEPILOT_ISSUE56_RESUME_NOW"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, ok := state.GoalByID("g")
	if !ok || goal.Execution == nil {
		t.Fatal("fresh process lost the authorization")
	}
	authorization := goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1]
	identity := app.RunnerIdentity{Runtime: authorization.WorkerProfile.Runtime,
		ExecutablePath: authorization.WorkerProfile.ExecutablePath, Version: authorization.WorkerIdentity.ReportedVersion,
		Model: authorization.WorkerProfile.Model, Effort: authorization.WorkerProfile.Effort,
		Sandbox: authorization.WorkerProfile.Sandbox, EngineGeneration: authorization.EngineGeneration}
	options := chargedRunnerTestOptions(root, os.Getenv("FORGEPILOT_ISSUE56_RESUME_RUNTIME"), now, identity)
	record, err := ResumeAuthorizationGoal(options, "g")
	if expected := os.Getenv("FORGEPILOT_ISSUE56_EXPECT_ERROR"); expected != "" {
		if expected == "STOP:RECOVERY_BLOCKED" {
			if err != nil || record.Stop == nil || record.Stop.Reason != StopRecoveryBlocked {
				t.Fatalf("fresh-process uncertain resume = stop %#v, error %v", record.Stop, err)
			}
			return
		}
		if err == nil || !strings.Contains(err.Error(), expected) {
			t.Fatalf("fresh-process resume error = %v, want %q", err, expected)
		}
	} else if err != nil {
		t.Fatalf("fresh-process explicit resume: %v", err)
	}
}

func TestInvalidChargedWorkerResultsKeepTechnicalAttempt(t *testing.T) {
	for _, test := range []struct {
		name, result, exitCode string
		stop                   StopReason
	}{
		{name: "missing result", stop: StopRuntimeProtocol},
		{name: "malformed result", result: `{"outcome":"needs_human"`, stop: StopRuntimeProtocol},
		{name: "worker failure", exitCode: "7", stop: StopRuntimeProtocol},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, runtimeCommand, _, now, _ := newUnresolvedExecutionRunnerFixture(t)
			identity := resolveExecutionIdentityForRunnerTest(t, root, now)
			options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
			options.Budget.MaxAttemptsPerWork = 1
			t.Setenv("FORGEPILOT_TEST_CODEX_RESULT", test.result)
			t.Setenv("FORGEPILOT_TEST_CODEX_EXIT_CODE", test.exitCode)
			record, err := Start(options)
			if err != nil {
				t.Fatal(err)
			}
			if record.Stop == nil || record.Stop.Reason != test.stop || record.HumanWaits["WI-001"] != 0 {
				t.Fatalf("invalid result stop = %#v, human waits = %d; want %s and zero waits", record.Stop, record.HumanWaits["WI-001"], test.stop)
			}
			state, err := storage.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			goal, _ := state.GoalByID("g")
			if goal.Execution.Ledger.NodeAttempts[0].TechnicalAttempts != 1 ||
				countReservations(goal.Execution.Ledger, work.ExecutionReservationAction) != 1 ||
				len(goal.Execution.Ledger.NeedsHumanDispositions) != 0 {
				t.Fatalf("invalid result changed its technical charge: %#v", goal.Execution.Ledger)
			}
			controlState, err := control.Read(root)
			if err != nil {
				t.Fatal(err)
			}
			if controlState.Pause != nil || len(controlState.Waits) != 0 {
				t.Fatalf("invalid result created a wait: %#v", controlState)
			}
			if strings.TrimSpace(test.exitCode) == "" && len(record.History) != 1 {
				t.Fatalf("protocol error did not retain its attempt history: %#v", record.History)
			}
		})
	}
}
