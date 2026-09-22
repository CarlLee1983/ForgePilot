package app

import (
	"bytes"
	"encoding/json"
	"errors"
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

	if _, err := PrepareChargedAction(fixture.root, authorized.GoalID, "run-001", "RESUME", before.WorkItems[0].ID, 1,
		before.Goals[0].Execution.Authorizations[0].Digest, identity, now); err == nil {
		t.Fatal("charged action admitted before WorkerIdentity and EngineGeneration were resolved")
	}
	afterUnresolvedAction, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(marshalState(t, before), marshalState(t, afterUnresolvedAction)) {
		t.Fatal("unresolved action identity mutated ledger state")
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
