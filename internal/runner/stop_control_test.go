package runner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/control"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestSupervisedResumeCannotAcknowledgeConcurrentStop(t *testing.T) {
	root, runtimeCommand, sentinel, now, execution := newUnresolvedExecutionRunnerFixture(t)
	identity := resolveExecutionIdentityForRunnerTest(t, root, now)
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, _ := state.GoalByID(execution.GoalID)
	digest := goal.Execution.Authorizations[0].Digest
	const runID = "run-20260922t090000-supervised"
	reservation, err := app.PrepareChargedRun(root, execution.GoalID, runID, identity, now)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := work.ExecutionReservationReceiptFor(reservation)
	if err != nil {
		t.Fatal(err)
	}
	record := Record{RunID: runID, Workspace: root, GoalID: execution.GoalID, GoalTitle: "Goal",
		RuntimeName: "codex", RuntimeExecutable: runtimeCommand, RuntimeVersion: identity.Version, RuntimeCommand: runtimeCommand,
		Snapshot: true, ExecutionAuthorizationDigest: digest, EngineGeneration: identity.EngineGeneration,
		RetentionAcquired: true, RunReservationID: reservation.ID,
		ReservationReceipts: []work.ExecutionReservationReceipt{receipt}, Budget: testBudget(), Limits: testLimits(),
		StartedAt: now, Deadline: now.Add(testBudget().MaxDuration), Attempts: map[string]int{}, HumanWaits: map[string]int{}}
	if err := record.save(root, record.Limits, now); err != nil {
		t.Fatal(err)
	}
	options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
	options.Supervised = true
	entered := make(chan struct{})
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	options.testCrashAt = func(point string) {
		if point == "before-resume-acknowledgment" {
			close(entered)
			<-release
		}
	}
	type outcome struct {
		record Record
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		continued, err := ResumeAuthorizationGoal(options, execution.GoalID)
		finished <- outcome{continued, err}
	}()
	select {
	case <-entered:
	case result := <-finished:
		t.Fatalf("supervised resume finished before stop race boundary: stop=%#v err=%v", result.record.Stop, result.err)
	case <-time.After(10 * time.Second):
		t.Fatal("supervised resume did not reach the stop race boundary")
	}
	stopped, stopErr := RequestSupervisedStopResult(root, execution.GoalID, "operator", "stop before worker launch", now)
	if stopErr == nil || stopped.Pause.RunID != runID || stopped.CleanupConfirmed {
		t.Fatalf("stop with an unrecorded worker = %#v, %v", stopped, stopErr)
	}
	close(release)
	result := <-finished
	if result.err != nil || result.record.Stop == nil || result.record.Stop.Reason != StopUserPaused {
		t.Fatalf("supervised continuation ignored concurrent stop: %#v, %v", result.record.Stop, result.err)
	}
	controlState, err := control.Read(root)
	if err != nil || controlState.Pause == nil || controlState.Pause.RunID != runID {
		t.Fatalf("concurrent stop was cleared: %#v, %v", controlState.Pause, err)
	}
	if _, err := os.Stat(sentinel); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worker launched despite concurrent stop: %v", err)
	}
}

func TestSupervisedStopWinsBeforeFirstRunRecordLaunch(t *testing.T) {
	root, runtimeCommand, sentinel, now, _ := newUnresolvedExecutionRunnerFixture(t)
	identity := resolveExecutionIdentityForRunnerTest(t, root, now)
	options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
	entered := make(chan struct{})
	release := make(chan struct{})
	options.testNewRunner = func(options Options) (*Runner, error) {
		close(entered)
		<-release
		return newRunner(options)
	}
	type outcome struct {
		record Record
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		record, err := Start(options)
		finished <- outcome{record, err}
	}()
	<-entered // Start has passed its first pause check; no Run Record exists.
	stopped, err := RequestSupervisedStopResult(root, "g", "operator", "stop before launch", now)
	if err != nil || !stopped.CleanupConfirmed || stopped.Pause.RunID != SupervisionPauseRunID {
		t.Fatalf("pre-run stop = %#v, %v", stopped, err)
	}
	close(release)
	result := <-finished
	if result.err != nil || result.record.Stop == nil || result.record.Stop.Reason != StopUserPaused {
		t.Fatalf("Runner ignored durable pre-run stop: %#v, %v", result.record.Stop, result.err)
	}
	if _, err := os.Stat(sentinel); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worker launched after stop: %v", err)
	}
	state, err := control.Read(root)
	if err != nil || state.Pause == nil || state.Pause.RunID != SupervisionPauseRunID {
		t.Fatalf("pause disappeared across launch race: %#v, %v", state.Pause, err)
	}
	if err := ClearSupervisionPause(root, "g"); err != nil {
		t.Fatalf("explicit supervised resume: %v", err)
	}
	state, err = control.Read(root)
	if err != nil || state.Pause != nil {
		t.Fatalf("explicit resume left pause: %#v, %v", state.Pause, err)
	}
}

func TestRequestStopPersistsPauseBeforeRefusingUnknownWorker(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := storage.Init(root); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	record := Record{RunID: "run-20260922t090000-a1b2c3", Workspace: root, GoalID: "G-001"}
	if err := record.save(root, storage.ArtifactLimits{}, now); err != nil {
		t.Fatal(err)
	}

	err := RequestStop(root, "G-001", "Carl", "stop for inspection", now)
	if err == nil || !strings.Contains(err.Error(), "no recorded worker") {
		t.Fatalf("RequestStop error = %v, want fail-closed unknown-worker refusal", err)
	}
	state, err := control.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if state.Pause == nil || state.Pause.GoalID != "G-001" || state.Pause.RunID != record.RunID ||
		state.Pause.Reason != "stop for inspection" || state.Pause.RequestedBy != "Carl" {
		t.Fatalf("durable pause = %#v, want the requested stop intent", state.Pause)
	}
}

func TestPersistedPauseStopsBeforeANewAction(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := storage.Init(root); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	record := Record{RunID: "run-20260922t090000-a1b2c3", Workspace: root, GoalID: "G-001",
		Deadline: now.Add(time.Hour)}
	if err := record.save(root, storage.ArtifactLimits{}, now); err != nil {
		t.Fatal(err)
	}
	if err := control.Update(root, func(state *control.State) error {
		return state.SetPause(control.Pause{GoalID: record.GoalID, RunID: record.RunID,
			Reason: "operator pause", RequestedBy: "Carl", RequestedAt: now})
	}); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{options: Options{Root: root, Now: func() time.Time { return now }}, record: &record}
	stopped, err := runner.beforeAction()
	if err != nil || !stopped {
		t.Fatalf("beforeAction = (%t, %v), want durable pause stop", stopped, err)
	}
	if record.Stop == nil || record.Stop.Reason != StopUserPaused {
		t.Fatalf("stop = %#v, want USER_PAUSED", record.Stop)
	}
}
