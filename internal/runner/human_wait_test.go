package runner

import (
	"testing"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/control"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// This exercises the restart boundary with a real authorized fixture: the
// result is already in the Run Record, while the sidecar wait is replayed after
// an explicit acknowledgement. Reconciliation must not turn history back into
// an active pause.
func TestRecordedNeedsHumanWaitReplayDoesNotReactivateAcknowledgedWait(t *testing.T) {
	root, _, _, now, _ := newUnresolvedExecutionRunnerFixture(t)
	_ = resolveExecutionIdentityForRunnerTest(t, root, now)
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, ok := state.GoalByID("g")
	if !ok || goal.Execution == nil {
		t.Fatal("missing authorized fixture")
	}
	authorization := goal.Execution.Authorizations[0]
	workItemID := state.WorkItems[0].ID
	nodeID := goal.Execution.PlanBindings[0].Nodes[0].PlanNodeRef
	var receipt work.ExecutionReservationReceipt
	if err := storage.Update(root, func(state *work.State) error {
		reservation, err := state.PrepareExecutionReservation("g", work.ExecutionReservation{ID: "run-replay:action:RESUME:" + nodeID + ":1", Kind: work.ExecutionReservationAction, RunID: "run-replay", PlanNodeRef: nodeID, CreatedAt: now})
		if err != nil {
			return err
		}
		receipt, err = work.ExecutionReservationReceiptFor(reservation)
		if err != nil {
			return err
		}
		return state.ConfirmNeedsHumanDisposition("g", "run-replay", receipt)
	}); err != nil {
		t.Fatal(err)
	}
	input := app.RunnerWaitInput{Question: "Which release?", Context: "runner restart", ReceiptID: receipt.ReservationID, ReceiptDigest: receipt.ReservationDigest}
	wait, err := app.EnsureRunnerNeedsHumanWait(root, "g", "run-replay", workItemID, 1, authorization.Digest, input, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.AcknowledgeExecutionResume(root, "g", "run-replay"); err != nil {
		t.Fatal(err)
	}
	record := Record{RunID: "run-replay", GoalID: "g", ExecutionAuthorizationDigest: authorization.Digest,
		History: []Attempt{{WorkItemID: workItemID, Number: 1, Outcome: "needs_human", At: now,
			ActionReservationReceipt: &receipt, NeedsHuman: &NeedsHumanRequest{Question: input.Question, Context: input.Context}}}}
	if err := reconcileRecordedNeedsHumanWaits(root, record); err != nil {
		t.Fatal(err)
	}
	controlState, err := control.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if controlState.Pause != nil {
		t.Fatalf("historical wait %q was reactivated: %#v", wait.ID, controlState.Pause)
	}
}
