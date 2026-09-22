package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/control"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestNeedsHumanWaitIsBoundAndAcknowledgedHistoryIsNotReactivated(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil {
		t.Fatal(err)
	}
	authorized, err := AuthorizeExecutionFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator")
	if err != nil {
		t.Fatal(err)
	}
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	goal, ok := state.GoalByID(authorized.GoalID)
	if !ok || goal.Execution == nil {
		t.Fatal("authorized goal is missing execution aggregate")
	}
	workItemID := state.WorkItems[0].ID
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	nodeID := goal.Execution.PlanBindings[0].Nodes[0].PlanNodeRef
	receipt := prepareNeedsHumanReceipt(t, fixture.root, goal.ID, "run-1", nodeID, now)
	input := RunnerWaitInput{Question: "Which deployment?", Context: "release", ReceiptID: receipt.ReservationID, ReceiptDigest: receipt.ReservationDigest}
	wait, err := EnsureRunnerNeedsHumanWait(fixture.root, goal.ID, "run-1", workItemID, 1,
		goal.Execution.Authorizations[0].Digest, input, now)
	if err != nil {
		t.Fatal(err)
	}
	controlState, err := control.Read(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if controlState.Pause == nil || controlState.Pause.WaitID != wait.ID {
		t.Fatalf("control pause = %#v, want wait %q", controlState.Pause, wait.ID)
	}
	if wait.PlanDigest != goal.Execution.PlanBindings[0].Digest || wait.NodeID == "" || wait.AuthorizationDigest != goal.Execution.Authorizations[0].Digest {
		t.Fatalf("wait binding = %#v", wait)
	}
	if _, err := EnsureRunnerNeedsHumanWait(fixture.root, goal.ID, "run-1", workItemID, 1,
		goal.Execution.Authorizations[0].Digest, input, now); err != nil {
		t.Fatalf("idempotent wait replay: %v", err)
	}
	if err := AcknowledgeExecutionResume(fixture.root, goal.ID, "run-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureRunnerNeedsHumanWait(fixture.root, goal.ID, "run-1", workItemID, 1,
		goal.Execution.Authorizations[0].Digest, input, now); err != nil {
		t.Fatalf("acknowledged wait replay: %v", err)
	}
	if _, err := EnsureRunnerNeedsHumanWait(fixture.root, goal.ID, "run-1", workItemID, 2,
		goal.Execution.Authorizations[0].Digest, input, now); err == nil {
		t.Fatal("needs_human receipt was accepted for a different attempt")
	}
	controlState, err = control.Read(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if controlState.Pause != nil {
		t.Fatalf("acknowledged wait was reactivated: %#v", controlState.Pause)
	}
}

func TestExternalFulfillmentDeclarationIsStrictIdempotentAndDoesNotResume(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil {
		t.Fatal(err)
	}
	authorized, err := AuthorizeExecutionFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator")
	if err != nil {
		t.Fatal(err)
	}
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	goal, _ := state.GoalByID(authorized.GoalID)
	now := time.Now().UTC()
	nodeID := goal.Execution.PlanBindings[0].Nodes[0].PlanNodeRef
	receipt := prepareNeedsHumanReceipt(t, fixture.root, goal.ID, "run-2", nodeID, now)
	wait, err := EnsureRunnerNeedsHumanWait(fixture.root, goal.ID, "run-2", state.WorkItems[0].ID, 1,
		goal.Execution.Authorizations[0].Digest, RunnerWaitInput{Question: "Is deploy complete?", ExternalFact: "deploy-complete", ReceiptID: receipt.ReservationID, ReceiptDigest: receipt.ReservationDigest}, now)
	if err != nil {
		t.Fatal(err)
	}
	request := ExternalFulfillmentDeclarationRequest{FormatVersion: externalFulfillmentDeclarationVersion, GoalID: goal.ID, WaitID: wait.ID, Fact: "deploy-complete", DeclaredBy: "operator"}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "declaration.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	stateBefore, err := os.ReadFile(filepath.Join(fixture.root, ".forgepilot", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := DeclareExternalFulfillmentFile(context.Background(), fixture.root, "declaration.json")
	if err != nil {
		t.Fatal(err)
	}
	second, err := DeclareExternalFulfillmentFile(context.Background(), fixture.root, "declaration.json")
	if err != nil {
		t.Fatalf("idempotent declaration retry: %v", err)
	}
	if first != second {
		t.Fatalf("declaration retry changed record: first=%#v second=%#v", first, second)
	}
	stateAfter, err := os.ReadFile(filepath.Join(fixture.root, ".forgepilot", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(stateBefore) != string(stateAfter) {
		t.Fatal("external declaration changed lifecycle state")
	}
	controlState, err := control.Read(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if controlState.Pause == nil || controlState.Pause.WaitID != wait.ID || len(controlState.ExternalDeclarations) != 1 {
		t.Fatalf("declaration changed active control unexpectedly: %#v", controlState)
	}
	if err := AcknowledgeExecutionResume(fixture.root, goal.ID, "run-2"); err != nil {
		t.Fatal(err)
	}
	if controlState, err = control.Read(fixture.root); err != nil {
		t.Fatal(err)
	} else if controlState.Pause != nil {
		t.Fatalf("resume acknowledgement did not clear pause: %#v", controlState.Pause)
	}
	if _, err := DeclareExternalFulfillmentFile(context.Background(), fixture.root, "declaration.json"); err == nil {
		t.Fatal("historical declaration unexpectedly accepted after pause was cleared")
	}
}

func TestExternalFulfillmentDeclarationRejectsDuplicateKeys(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	request := `{"formatVersion":"forgepilot.external-fulfillment-declaration/v1","formatVersion":"forgepilot.external-fulfillment-declaration/v1","goalId":"g","waitId":"wait","fact":"fact","declaredBy":"operator"}`
	if err := os.WriteFile(filepath.Join(fixture.root, "declaration.json"), []byte(request), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := DeclareExternalFulfillmentFile(context.Background(), fixture.root, "declaration.json"); err == nil {
		t.Fatal("duplicate external declaration key was accepted")
	}
}

func prepareNeedsHumanReceipt(t *testing.T, root, goalID, runID, nodeID string, now time.Time) work.ExecutionReservationReceipt {
	t.Helper()
	var receipt work.ExecutionReservationReceipt
	err := storage.Update(root, func(state *work.State) error {
		reservation, err := state.PrepareExecutionReservation(goalID, work.ExecutionReservation{ID: runID + ":action:RESUME:" + nodeID + ":1", Kind: work.ExecutionReservationAction, RunID: runID, PlanNodeRef: nodeID, CreatedAt: now})
		if err != nil {
			return err
		}
		receipt, err = work.ExecutionReservationReceiptFor(reservation)
		if err != nil {
			return err
		}
		return state.ConfirmNeedsHumanDisposition(goalID, runID, receipt)
	})
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}
