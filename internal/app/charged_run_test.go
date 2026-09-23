package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestChargedRunAdmissionRejectsUnresolvedIdentityAndProfileMismatch(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("plan = %#v, err=%v", preview, err)
	}
	authorized, err := AuthorizeExecutionFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	before, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareChargedRun(fixture.root, authorized.GoalID, "run-001", RunnerIdentity{
		Runtime: "fake", ExecutablePath: fixture.request.WorkerProfile.ExecutablePath, Sandbox: "workspace-write"}, now); err == nil {
		t.Fatal("profile mismatch admitted a charged run")
	}
	afterMismatch, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(marshalState(t, before), marshalState(t, afterMismatch)) {
		t.Fatal("profile mismatch mutated ledger state")
	}

	boundProfile := before.Goals[0].Execution.Authorizations[0].WorkerProfile
	identity := RunnerIdentity{Runtime: boundProfile.Runtime, ExecutablePath: boundProfile.ExecutablePath,
		Version: "observed-worker-version", Sandbox: boundProfile.Sandbox}
	if _, err := PrepareChargedRun(fixture.root, authorized.GoalID, "run-001", identity, now); err == nil {
		t.Fatal("charged run admitted before WorkerIdentity and EngineGeneration were resolved")
	}
	afterUnresolvedRun, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(marshalState(t, before), marshalState(t, afterUnresolvedRun)) {
		t.Fatal("unresolved run identity mutated ledger state")
	}

	if _, _, err := PrepareChargedWorker(fixture.root, authorized.GoalID, "run-001", "RESUME", before.WorkItems[0].ID, 1, 1,
		before.Goals[0].Execution.Authorizations[0].Digest, identity, now); err == nil {
		t.Fatal("charged worker admitted before WorkerIdentity and EngineGeneration were resolved")
	}
	afterUnresolvedAction, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(marshalState(t, before), marshalState(t, afterUnresolvedAction)) {
		t.Fatal("unresolved worker identity mutated ledger state")
	}
	if _, err := PrepareChargedStep(fixture.root, authorized.GoalID, "run-001", 1, "START", before.WorkItems[0].ID,
		before.Goals[0].Execution.Authorizations[0].Digest, identity, now); err == nil {
		t.Fatal("charged step admitted before WorkerIdentity and EngineGeneration were resolved")
	}
	afterUnresolvedStep, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(marshalState(t, before), marshalState(t, afterUnresolvedStep)) {
		t.Fatal("unresolved step identity mutated ledger state")
	}
}

func TestPrepareAuthorizedAgentLaunchReturnsTheDigestMatchedWorkerProfile(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("plan = %#v, err=%v", preview, err)
	}
	seedResolver, _ := pinnedExecutionResolver(t, fixture)
	authorized, err := AuthorizePinnedExecutionFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator", seedResolver)
	if err != nil {
		t.Fatal(err)
	}
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	profile := state.Goals[0].Execution.Authorizations[0].WorkerProfile
	now := time.Now().UTC()
	generation := work.ExecutionEngineGeneration{SourceCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PayloadSHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	identity := RunnerIdentity{Runtime: profile.Runtime, ExecutablePath: profile.ExecutablePath, Version: "codex 1.2.3",
		Model: profile.Model, Effort: profile.Effort, Sandbox: profile.Sandbox}
	bound, err := BindExecutionLaunchIdentity(t.Context(), fixture.root, authorized.GoalID, identity, generation,
		&recordingGenerationRetention{}, now)
	if err != nil {
		t.Fatal(err)
	}
	identity.EngineGeneration = &generation
	launch, err := PrepareAuthorizedAgentLaunch(fixture.root, authorized.GoalID, "run-001", "run-001:artifact:RESUME:WI-001:1",
		1024, bound.Digest, identity, now)
	if err != nil {
		t.Fatal(err)
	}
	if launch.AuthorizationDigest != bound.Digest || launch.WorkerProfile != profile ||
		launch.ArtifactReservation.ID != "run-001:artifact:RESUME:WI-001:1" {
		t.Fatalf("authorized agent launch = %#v; want transaction-bound profile and digest", launch)
	}
}

func TestExecutionAuthorizationCannotBeAdoptedWhileRunnerOwnsWorkspace(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("plan = %#v, err=%v", preview, err)
	}
	before, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	err = storage.WithWorkspaceLock(fixture.root, func() error {
		command := exec.Command(os.Args[0], "-test.run=^TestExecutionAuthorizationLockHelper$", "-test.v")
		command.Env = append(os.Environ(),
			"FORGEPILOT_TEST_AUTH_LOCK_ROOT="+fixture.root,
			"FORGEPILOT_TEST_AUTH_LOCK_TOKEN="+preview.ApprovalToken,
		)
		output, commandErr := command.CombinedOutput()
		if commandErr != nil {
			return errors.New("authorization lock helper failed: " + string(output))
		}
		if !bytes.Contains(output, []byte("authorization-lock-helper-executed")) {
			return errors.New("authorization lock helper did not execute its assertion: " + string(output))
		}
		after, loadErr := storage.Load(fixture.root)
		if loadErr != nil {
			return loadErr
		}
		if !bytes.Equal(marshalState(t, before), marshalState(t, after)) {
			return errors.New("authorization adoption changed state while a Runner held the workspace lock")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestExecutionAuthorizationLockHelper(t *testing.T) {
	root := os.Getenv("FORGEPILOT_TEST_AUTH_LOCK_ROOT")
	if root == "" {
		return
	}
	token := os.Getenv("FORGEPILOT_TEST_AUTH_LOCK_TOKEN")
	if token == "" {
		t.Fatal("lock helper did not receive the approval token")
	}
	_, err := AuthorizeExecutionFile(t.Context(), root, "execution-request.json", token, "operator")
	if err == nil || !strings.Contains(err.Error(), "while a Runner owns the workspace") {
		t.Fatalf("cross-process authorization adoption error = %v; want live Runner ownership refusal", err)
	}
	t.Log("authorization-lock-helper-executed")
}

func TestExactResumeRequiresRunReceiptInCurrentLedger(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("plan = %#v, err=%v", preview, err)
	}
	authorized, err := AuthorizeExecutionFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	reservation, err := work.ExecutionReservationReceiptFor(work.ExecutionReservation{
		ID: "run-absent:run", Kind: work.ExecutionReservationRun, RunID: "run-absent",
		CreatedAt: now, Status: work.ExecutionReservationPrepared,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ValidateRunnerResume(fixture.root, authorized.GoalID, "run-absent", authorized.Authorizations[0].Digest,
		reservation.ReservationID, []work.ExecutionReservationReceipt{reservation}, work.ExecutionArtifactLimits{}, RunnerIdentity{}, now)
	if err == nil || !strings.Contains(err.Error(), "no matching reservation in the current execution ledger") {
		t.Fatalf("resume error = %v, want receipt rejected against the current ledger", err)
	}
}

func TestChargedExactResumeRejectsArtifactLimitsAboveAuthorization(t *testing.T) {
	for _, field := range []string{"handoff", "write", "run", "total"} {
		t.Run(field, func(t *testing.T) {
			fixture := newExecutionTestFixture(t)
			preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
			if err != nil || len(preview.Diagnostics) != 0 {
				t.Fatalf("plan = %#v, err=%v", preview, err)
			}
			authorized, err := AuthorizeExecutionFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator")
			if err != nil {
				t.Fatal(err)
			}
			const runID = "run-artifact-cap"
			now := time.Now().UTC()
			var reservation work.ExecutionReservation
			if err := storage.Update(fixture.root, func(state *work.State) error {
				var prepareErr error
				reservation, prepareErr = state.PrepareExecutionReservation(authorized.GoalID, work.ExecutionReservation{
					ID: runID + ":run", Kind: work.ExecutionReservationRun, RunID: runID, CreatedAt: now,
				})
				return prepareErr
			}); err != nil {
				t.Fatal(err)
			}
			receipt, err := work.ExecutionReservationReceiptFor(reservation)
			if err != nil {
				t.Fatal(err)
			}
			record, err := json.Marshal(struct {
				RunID                        string                             `json:"run_id"`
				GoalID                       string                             `json:"goal_id"`
				ExecutionAuthorizationDigest string                             `json:"execution_authorization_digest"`
				RunReservationID             string                             `json:"run_reservation_id"`
				ReservationReceipts          []work.ExecutionReservationReceipt `json:"reservation_receipts"`
			}{runID, authorized.GoalID, authorized.Authorizations[0].Digest, reservation.ID, []work.ExecutionReservationReceipt{receipt}})
			if err != nil {
				t.Fatal(err)
			}
			if err := storage.WriteRunArtifact(fixture.root, runID, "run.json", record, storage.ArtifactLimits{}); err != nil {
				t.Fatal(err)
			}
			caps := authorized.Authorizations[0].Caps.Artifacts
			limits := work.ExecutionArtifactLimits{MaxHandoffBytes: caps.MaxHandoffBytes, MaxWriteBytes: caps.MaxWriteBytes,
				MaxRunBytes: caps.MaxRunBytes, MaxTotalBytes: caps.MaxTotalBytes}
			switch field {
			case "handoff":
				limits.MaxHandoffBytes++
			case "write":
				limits.MaxWriteBytes++
			case "run":
				limits.MaxRunBytes++
			case "total":
				limits.MaxTotalBytes++
			}
			_, err = ValidateRunnerResume(fixture.root, authorized.GoalID, runID, authorized.Authorizations[0].Digest,
				reservation.ID, []work.ExecutionReservationReceipt{receipt}, limits, RunnerIdentity{}, now)
			if err == nil || !strings.Contains(err.Error(), "artifact limits exceed") {
				t.Fatalf("exact resume error = %v, want artifact-cap refusal", err)
			}
		})
	}
}

func TestChargedRunAdmissionRequiresGoalBoundRunReceipt(t *testing.T) {
	for _, test := range []struct {
		name    string
		goalID  string
		receipt bool
	}{
		{name: "missing receipt", goalID: "goal"},
		{name: "wrong goal binding", goalID: "different-goal", receipt: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExecutionTestFixture(t)
			preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
			if err != nil || len(preview.Diagnostics) != 0 {
				t.Fatalf("plan = %#v, err=%v", preview, err)
			}
			authorized, err := AuthorizeExecutionFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator")
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			const runID = "run-001"
			var reservation work.ExecutionReservation
			err = storage.Update(fixture.root, func(state *work.State) error {
				var prepareErr error
				reservation, prepareErr = state.PrepareExecutionReservation(authorized.GoalID, work.ExecutionReservation{
					ID: runID + ":run", Kind: work.ExecutionReservationRun, RunID: runID, CreatedAt: now,
				})
				return prepareErr
			})
			if err != nil {
				t.Fatal(err)
			}
			var receipts []work.ExecutionReservationReceipt
			if test.receipt {
				receipt, err := work.ExecutionReservationReceiptFor(reservation)
				if err != nil {
					t.Fatal(err)
				}
				receipts = []work.ExecutionReservationReceipt{receipt}
			}
			record, err := json.Marshal(struct {
				RunID                        string                             `json:"run_id"`
				GoalID                       string                             `json:"goal_id"`
				ExecutionAuthorizationDigest string                             `json:"execution_authorization_digest"`
				RunReservationID             string                             `json:"run_reservation_id"`
				ReservationReceipts          []work.ExecutionReservationReceipt `json:"reservation_receipts,omitempty"`
			}{runID, test.goalID, authorized.Authorizations[0].Digest, reservation.ID, receipts})
			if err != nil {
				t.Fatal(err)
			}
			if err := storage.WriteRunArtifact(fixture.root, runID, "run.json", record, storage.ArtifactLimits{}); err != nil {
				t.Fatal(err)
			}

			if err := refuseUnrecordedChargedRun(fixture.root, authorized.GoalID); err == nil {
				t.Fatal("charged Run Record without a matching Goal-bound receipt was accepted")
			}
		})
	}
}

func TestChargedRunReceiptAuditRequiresEveryActionAndStepReservation(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("plan = %#v, err=%v", preview, err)
	}
	authorized, err := AuthorizeExecutionFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator")
	if err != nil {
		t.Fatal(err)
	}
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	workItemID := state.WorkItems[0].ID
	planNodeRef, err := state.ExecutionPlanNodeRef(authorized.GoalID, workItemID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	const runID = "run-receipts"
	var runReservation work.ExecutionReservation
	if err := storage.Update(fixture.root, func(state *work.State) error {
		var err error
		runReservation, err = state.PrepareExecutionReservation(authorized.GoalID, work.ExecutionReservation{
			ID: runID + ":run", Kind: work.ExecutionReservationRun, RunID: runID, CreatedAt: now,
		})
		if err != nil {
			return err
		}
		if _, err := state.PrepareExecutionReservation(authorized.GoalID, work.ExecutionReservation{
			ID: runID + ":action:RESUME:" + planNodeRef + ":1", Kind: work.ExecutionReservationAction,
			RunID: runID, PlanNodeRef: planNodeRef, CreatedAt: now,
		}); err != nil {
			return err
		}
		_, err = state.PrepareExecutionReservation(authorized.GoalID, work.ExecutionReservation{
			ID: runID + ":step:1:RESUME:" + planNodeRef, Kind: work.ExecutionReservationStep,
			RunID: runID, PlanNodeRef: planNodeRef, CreatedAt: now,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	runReceipt, err := work.ExecutionReservationReceiptFor(runReservation)
	if err != nil {
		t.Fatal(err)
	}
	partialRecord, err := json.Marshal(struct {
		RunID                        string                             `json:"run_id"`
		GoalID                       string                             `json:"goal_id"`
		ExecutionAuthorizationDigest string                             `json:"execution_authorization_digest"`
		RunReservationID             string                             `json:"run_reservation_id"`
		ReservationReceipts          []work.ExecutionReservationReceipt `json:"reservation_receipts"`
	}{runID, authorized.GoalID, authorized.Authorizations[0].Digest, runReservation.ID,
		[]work.ExecutionReservationReceipt{runReceipt}})
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteRunArtifact(fixture.root, runID, "run.json", partialRecord, storage.ArtifactLimits{}); err != nil {
		t.Fatal(err)
	}

	snapshot, err := ReconcileRunnerReservationReceipts(fixture.root, authorized.GoalID, runID,
		runReservation.ID, []work.ExecutionReservationReceipt{runReceipt})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Reservations) != 3 || len(snapshot.Receipts) != 3 || snapshot.PlanNodeByWorkItem[workItemID] != planNodeRef {
		t.Fatalf("receipt reconciliation did not recover the full ledger-backed set: %#v", snapshot)
	}
	if err := refuseUnrecordedChargedRun(fixture.root, authorized.GoalID); err == nil ||
		!strings.Contains(err.Error(), "no Run Record receipt for charged reservation") {
		t.Fatalf("incomplete Run Record error = %v, want missing ACTION/STEP receipt refusal", err)
	}
}

func TestChargedRunAuditRejectsNeedsHumanDispositionWithoutRunRecordResult(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("plan = %#v, err=%v", preview, err)
	}
	authorized, err := AuthorizeExecutionFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator")
	if err != nil {
		t.Fatal(err)
	}
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	workItemID := state.WorkItems[0].ID
	planNodeRef, err := state.ExecutionPlanNodeRef(authorized.GoalID, workItemID)
	if err != nil {
		t.Fatal(err)
	}

	const runID = "run-unconfirmed-disposition"
	now := time.Now().UTC()
	var reservations []work.ExecutionReservation
	if err := storage.Update(fixture.root, func(state *work.State) error {
		requests := []work.ExecutionReservation{
			{ID: runID + ":run", Kind: work.ExecutionReservationRun, RunID: runID, CreatedAt: now},
			{ID: runID + ":action:RESUME:" + planNodeRef + ":1", Kind: work.ExecutionReservationAction, RunID: runID, PlanNodeRef: planNodeRef, CreatedAt: now},
			{ID: runID + ":step:1:RESUME:" + planNodeRef, Kind: work.ExecutionReservationStep, RunID: runID, PlanNodeRef: planNodeRef, CreatedAt: now},
		}
		for _, request := range requests {
			reservation, prepareErr := state.PrepareExecutionReservation(authorized.GoalID, request)
			if prepareErr != nil {
				return prepareErr
			}
			reservations = append(reservations, reservation)
		}
		// This state transition is structurally valid, but it bypasses the app
		// boundary that first requires a durable needs_human Run Record result.
		// Admission must therefore fail closed instead of accepting its refunded
		// technical-attempt capacity.
		receipt, receiptErr := work.ExecutionReservationReceiptFor(reservations[1])
		if receiptErr != nil {
			return receiptErr
		}
		return state.ConfirmNeedsHumanDisposition(authorized.GoalID, runID, receipt)
	}); err != nil {
		t.Fatal(err)
	}
	receipts := make([]work.ExecutionReservationReceipt, 0, len(reservations))
	for _, reservation := range reservations {
		receipt, receiptErr := work.ExecutionReservationReceiptFor(reservation)
		if receiptErr != nil {
			t.Fatal(receiptErr)
		}
		receipts = append(receipts, receipt)
	}
	record, err := json.Marshal(runnerRunRecordAudit{
		RunID: runID, GoalID: authorized.GoalID, ExecutionAuthorizationDigest: authorized.Authorizations[0].Digest,
		RunReservationID: reservations[0].ID, ReservationReceipts: receipts,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteRunArtifact(fixture.root, runID, "run.json", record, storage.ArtifactLimits{}); err != nil {
		t.Fatal(err)
	}

	if err := CheckRunnerRunAdmissionIntegrity(fixture.root, authorized.GoalID); err == nil ||
		!strings.Contains(err.Error(), "without a durable Run Record result") {
		t.Fatalf("unconfirmed needs_human disposition admission error = %v, want fail-closed audit refusal", err)
	}
}

func TestChargedRunAuditAcceptsRetainedNeedsHumanDispositionLinks(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("plan = %#v, err=%v", preview, err)
	}
	authorized, err := AuthorizeExecutionFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator")
	if err != nil {
		t.Fatal(err)
	}
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	workItemID := state.WorkItems[0].ID
	planNodeRef, err := state.ExecutionPlanNodeRef(authorized.GoalID, workItemID)
	if err != nil {
		t.Fatal(err)
	}

	const (
		runID            = "run-retained-dispositions"
		retainedAttempts = 5 // internal/runner.RetainedAttempts; app cannot import runner.
	)
	now := time.Now().UTC()
	var reservations []work.ExecutionReservation
	var humanWaitReservationIDs []string
	var humanWaitReceipts []work.ExecutionReservationReceipt
	if err := storage.Update(fixture.root, func(state *work.State) error {
		run, prepareErr := state.PrepareExecutionReservation(authorized.GoalID, work.ExecutionReservation{
			ID: runID + ":run", Kind: work.ExecutionReservationRun, RunID: runID, CreatedAt: now,
		})
		if prepareErr != nil {
			return prepareErr
		}
		reservations = append(reservations, run)
		for attempt := 1; attempt <= retainedAttempts+1; attempt++ {
			at := now.Add(time.Duration(attempt) * time.Second)
			action, actionErr := state.PrepareExecutionReservation(authorized.GoalID, work.ExecutionReservation{
				ID:   fmt.Sprintf("%s:action:RESUME:%s:%d", runID, planNodeRef, attempt),
				Kind: work.ExecutionReservationAction, RunID: runID, PlanNodeRef: planNodeRef, CreatedAt: at,
			})
			if actionErr != nil {
				return actionErr
			}
			step, stepErr := state.PrepareExecutionReservation(authorized.GoalID, work.ExecutionReservation{
				ID:   fmt.Sprintf("%s:step:%d:RESUME:%s", runID, attempt, planNodeRef),
				Kind: work.ExecutionReservationStep, RunID: runID, PlanNodeRef: planNodeRef, CreatedAt: at,
			})
			if stepErr != nil {
				return stepErr
			}
			receipt, receiptErr := work.ExecutionReservationReceiptFor(action)
			if receiptErr != nil {
				return receiptErr
			}
			if dispositionErr := state.ConfirmNeedsHumanDisposition(authorized.GoalID, runID, receipt); dispositionErr != nil {
				return dispositionErr
			}
			reservations = append(reservations, action, step)
			humanWaitReservationIDs = append(humanWaitReservationIDs, action.ID)
			humanWaitReceipts = append(humanWaitReceipts, receipt)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	receipts := make([]work.ExecutionReservationReceipt, 0, len(reservations))
	for _, reservation := range reservations {
		receipt, receiptErr := work.ExecutionReservationReceiptFor(reservation)
		if receiptErr != nil {
			t.Fatal(receiptErr)
		}
		receipts = append(receipts, receipt)
	}
	// `Record.History` retains only five entries per Work Item, so the first
	// confirmed result is intentionally absent here. The unbounded durable ID
	// list is the recovery/audit linkage for every settled disposition.
	history := make([]runnerAttemptRecordAudit, 0, retainedAttempts)
	for index := 1; index < len(humanWaitReceipts); index++ {
		receipt := humanWaitReceipts[index]
		history = append(history, runnerAttemptRecordAudit{WorkItemID: workItemID, Number: index + 1,
			Outcome: "needs_human", ActionReservationReceipt: &receipt})
	}
	if len(humanWaitReceipts) != retainedAttempts+1 || len(history) != retainedAttempts {
		t.Fatalf("retained history fixture = %d receipts, %d attempts", len(humanWaitReceipts), len(history))
	}
	record, err := json.Marshal(runnerRunRecordAudit{
		RunID: runID, GoalID: authorized.GoalID, ExecutionAuthorizationDigest: authorized.Authorizations[0].Digest,
		RunReservationID: reservations[0].ID, ReservationReceipts: receipts,
		HumanWaitReservationIDs: humanWaitReservationIDs, History: history,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteRunArtifact(fixture.root, runID, "run.json", record, storage.ArtifactLimits{}); err != nil {
		t.Fatal(err)
	}

	if err := CheckRunnerRunAdmissionIntegrity(fixture.root, authorized.GoalID); err != nil {
		t.Fatalf("retained needs_human disposition links blocked direct charged admission: %v", err)
	}
}

func TestChargedRunAuditAcceptsACompleteHistoricalRunAfterAuthorizationRevision(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("plan = %#v, err=%v", preview, err)
	}
	initial, err := AuthorizeExecutionFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator")
	if err != nil {
		t.Fatal(err)
	}
	const runID = "run-historical"
	var reservation work.ExecutionReservation
	if err := storage.Update(fixture.root, func(state *work.State) error {
		var prepareErr error
		reservation, prepareErr = state.PrepareExecutionReservation(initial.GoalID, work.ExecutionReservation{
			ID: runID + ":run", Kind: work.ExecutionReservationRun, RunID: runID, CreatedAt: time.Now().UTC(),
		})
		return prepareErr
	}); err != nil {
		t.Fatal(err)
	}
	receipt, err := work.ExecutionReservationReceiptFor(reservation)
	if err != nil {
		t.Fatal(err)
	}
	record, err := json.Marshal(runnerRunRecordAudit{
		RunID: runID, GoalID: initial.GoalID, ExecutionAuthorizationDigest: initial.Authorizations[0].Digest,
		RunReservationID: reservation.ID, ReservationReceipts: []work.ExecutionReservationReceipt{receipt},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteRunArtifact(fixture.root, runID, "run.json", record, storage.ArtifactLimits{}); err != nil {
		t.Fatal(err)
	}

	rewriteExecutionFixtureAsAdditiveRevision(t, fixture.root)
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	fixture.request.ExpectedAuthorizationDigest = state.Goals[0].Execution.Authorizations[0].Digest
	fixture.request.GoalPlanRequest.NodeMappings = append(fixture.request.GoalPlanRequest.NodeMappings, GoalPlanNodeMapping{PlanNodeRef: "c"})
	writeExecutionTestRequest(t, fixture.requestPath, fixture.request)
	revision, err := PlanExecutionRevisionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(revision.Diagnostics) != 0 {
		t.Fatalf("revision = %#v, err=%v", revision, err)
	}
	if _, err := ReviseExecutionFile(t.Context(), fixture.root, "execution-request.json", revision.ApprovalToken, "revision-operator"); err != nil {
		t.Fatal(err)
	}
	if err := CheckRunnerRunAdmissionIntegrity(fixture.root, initial.GoalID); err != nil {
		t.Fatalf("historical run blocked current admission after a valid revision: %v", err)
	}
}

func marshalState(t *testing.T, state any) []byte {
	t.Helper()
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
