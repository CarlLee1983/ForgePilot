package work

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestExecutionRunReservationChargesOnceAndResealsWitness(t *testing.T) {
	now := time.Date(2026, time.September, 21, 9, 0, 0, 0, time.UTC)
	state, execution := initialExecutionFixture(t, now)
	if err := state.AdoptInitialExecution(execution); err != nil {
		t.Fatal(err)
	}

	request := ExecutionReservation{
		ID: "run-001", Kind: ExecutionReservationRun, RunID: "run-001", CreatedAt: now,
	}
	prepared, err := state.PrepareExecutionReservation("goal", request)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Status != ExecutionReservationPrepared {
		t.Fatalf("reservation status = %q, want %q", prepared.Status, ExecutionReservationPrepared)
	}
	goal, ok := state.GoalByID("goal")
	if !ok || goal.Execution == nil {
		t.Fatal("Goal execution aggregate disappeared")
	}
	ledger := goal.Execution.Ledger
	if ledger.Revision != 2 || ledger.RunsConsumed != 1 || ledger.StepsConsumed != 0 {
		t.Fatalf("prepared ledger = %#v; want revision 2, one run, and zero steps", ledger)
	}
	if goal.Execution.Witness.LedgerRevision != ledger.Revision || goal.Execution.Witness.LedgerDigest != ledger.Digest {
		t.Fatalf("Goal witness does not match charged ledger: witness=%#v ledger=%#v", goal.Execution.Witness, ledger)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("charged state is invalid: %v", err)
	}

	before, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	retryAfterRestart := request
	retryAfterRestart.CreatedAt = now.Add(time.Minute)
	replayed, err := state.PrepareExecutionReservation("goal", retryAfterRestart)
	if err != nil {
		t.Fatal(err)
	}
	if replayed != prepared {
		t.Fatalf("idempotent replay = %#v; want %#v", replayed, prepared)
	}
	after, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("idempotent replay changed ledger consumption or witness")
	}

	conflict := request
	conflict.RunID = "run-002"
	if _, err := state.PrepareExecutionReservation("goal", conflict); err == nil {
		t.Fatal("reservation id replay with different contents was accepted")
	}
	afterConflict, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, afterConflict) {
		t.Fatal("conflicting replay mutated ledger consumption or witness")
	}
}

func TestExecutionArtifactByteReservationIsAppendOnlyAndBoundedByAuthorization(t *testing.T) {
	now := time.Date(2026, time.September, 22, 9, 0, 0, 0, time.UTC)
	state, execution := initialExecutionFixture(t, now)
	execution.Authorizations[0].Caps.Artifacts.MaxHandoffBytes = 1
	execution.Authorizations[0].Caps.Artifacts.MaxWriteBytes = 1
	execution.Authorizations[0].Caps.Artifacts.MaxRunBytes = 2
	execution.Authorizations[0].Caps.Artifacts.MaxTotalBytes = 10
	if err := state.AdoptInitialExecution(execution); err != nil {
		t.Fatal(err)
	}

	request := ExecutionArtifactByteReservation{
		ID: "run-001:artifact:WI-001:1", RunID: "run-001", Bytes: 7, CreatedAt: now,
	}
	prepared, err := state.PrepareExecutionArtifactByteReservation("goal", request)
	if err != nil {
		t.Fatal(err)
	}
	goal, _ := state.GoalByID("goal")
	if goal.Execution.Ledger.ArtifactBytesConsumed != 7 || len(goal.Execution.Ledger.ArtifactByteReservations) != 1 ||
		goal.Execution.Ledger.ArtifactByteReservations[0] != prepared {
		t.Fatalf("artifact reservation was not durably charged: %#v", goal.Execution.Ledger)
	}
	if goal.Execution.Witness.LedgerDigest != goal.Execution.Ledger.Digest {
		t.Fatalf("artifact reservation did not reseal the witness: %#v", goal.Execution.Witness)
	}
	before, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	retry := request
	retry.CreatedAt = now.Add(time.Minute)
	if replayed, err := state.PrepareExecutionArtifactByteReservation("goal", retry); err != nil || replayed != prepared {
		t.Fatalf("idempotent artifact reservation replay = %#v, %v; want %#v", replayed, err, prepared)
	}
	if _, err := state.PrepareExecutionArtifactByteReservation("goal", ExecutionArtifactByteReservation{
		ID: "run-002:artifact:WI-001:1", RunID: "run-002", Bytes: 4, CreatedAt: now,
	}); err == nil {
		t.Fatal("artifact reservation exceeded the cumulative authorization cap")
	}
	after, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("replayed or rejected artifact reservation changed ledger consumption")
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("artifact-charged state is invalid: %v", err)
	}
}

func TestUnknownArtifactUsageFailsClosedUntilAnExplicitReauthorization(t *testing.T) {
	now := time.Date(2026, time.September, 22, 10, 0, 0, 0, time.UTC)
	state, execution := initialExecutionFixture(t, now)
	if err := state.AdoptInitialExecution(execution); err != nil {
		t.Fatal(err)
	}
	goal, _ := state.GoalByID("goal")
	goal.Execution.Ledger.ArtifactAccountingStartRevision = 0
	if err := resealExecutionLedgerAndWitness(goal.Execution); err != nil {
		t.Fatal(err)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("unknown migrated artifact accounting should remain readable: %v", err)
	}
	for _, request := range []ExecutionReservation{
		{ID: "run-001:run", Kind: ExecutionReservationRun, RunID: "run-001", CreatedAt: now},
		{ID: "run-001:recovery", Kind: ExecutionReservationRecovery, RunID: "run-001", CreatedAt: now},
		{ID: "run-001:action", Kind: ExecutionReservationAction, RunID: "run-001", PlanNodeRef: "node-1", CreatedAt: now},
		{ID: "run-001:step", Kind: ExecutionReservationStep, RunID: "run-001", CreatedAt: now},
	} {
		if _, err := state.PrepareExecutionReservation("goal", request); err == nil || !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("unknown artifact history allowed %s reservation: %v", request.Kind, err)
		}
	}
	if _, err := state.PrepareExecutionArtifactByteReservation("goal", ExecutionArtifactByteReservation{
		ID: "run-001:artifact:WI-001:1", RunID: "run-001", Bytes: 1, CreatedAt: now,
	}); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown artifact history allowed output without reauthorization: %v", err)
	}
}

func TestExecutionRunCapIsCumulativeAcrossDifferentRuns(t *testing.T) {
	now := time.Date(2026, time.September, 21, 9, 30, 0, 0, time.UTC)
	state, execution := initialExecutionFixture(t, now)
	execution.Authorizations[0].Caps.MaxRuns = 1
	if err := state.AdoptInitialExecution(execution); err != nil {
		t.Fatal(err)
	}
	if _, err := state.PrepareExecutionReservation("goal", ExecutionReservation{
		ID: "run-001:run", Kind: ExecutionReservationRun, RunID: "run-001", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.PrepareExecutionReservation("goal", ExecutionReservation{
		ID: "run-002:run", Kind: ExecutionReservationRun, RunID: "run-002", CreatedAt: now.Add(time.Minute),
	}); err == nil {
		t.Fatal("a new Run bypassed the cumulative authorization run cap")
	}
	after, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rejected cross-run charge changed the durable ledger or witness")
	}
	goal, _ := state.GoalByID("goal")
	if goal.Execution.Ledger.RunsConsumed != 1 || len(goal.Execution.Ledger.Reservations) != 1 {
		t.Fatalf("run cap rejection changed cumulative accounting: %#v", goal.Execution.Ledger)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("run-capped state is invalid: %v", err)
	}
}

func TestExecutionReservationReceiptBindsStoredReservation(t *testing.T) {
	now := time.Date(2026, time.September, 21, 10, 0, 0, 0, time.UTC)
	state, execution := initialExecutionFixture(t, now)
	if err := state.AdoptInitialExecution(execution); err != nil {
		t.Fatal(err)
	}
	reservation, err := state.PrepareExecutionReservation("goal", ExecutionReservation{
		ID: "run-001", Kind: ExecutionReservationRun, RunID: "run-001", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	receipt, err := ExecutionReservationReceiptFor(reservation)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := ExecutionReservationReceiptFor(reservation)
	if err != nil {
		t.Fatal(err)
	}
	if receipt != duplicate || receipt.ReservationID != reservation.ID || receipt.ReservationDigest == "" {
		t.Fatalf("reservation receipt is not stable and complete: %#v / %#v", receipt, duplicate)
	}
	changedChargeTime := reservation
	changedChargeTime.CreatedAt = now.Add(time.Second)
	changedReceipt, err := ExecutionReservationReceiptFor(changedChargeTime)
	if err != nil {
		t.Fatal(err)
	}
	if changedReceipt == receipt {
		t.Fatal("reservation receipt did not bind the durable charge time")
	}
}

func TestExecutionStepReservationConsumesCumulativeStepCapWithoutTechnicalAttempt(t *testing.T) {
	now := time.Date(2026, time.September, 21, 11, 0, 0, 0, time.UTC)
	state, execution := initialExecutionFixture(t, now)
	execution.Authorizations[0].Caps.MaxSteps = 1
	if err := state.AdoptInitialExecution(execution); err != nil {
		t.Fatal(err)
	}

	first, err := state.PrepareExecutionReservation("goal", ExecutionReservation{
		ID: "run-001:step:1:START", Kind: ExecutionReservationStep, RunID: "run-001", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Kind != ExecutionReservationStep {
		t.Fatalf("step reservation kind = %q", first.Kind)
	}
	goal, _ := state.GoalByID("goal")
	if goal.Execution.Ledger.StepsConsumed != 1 || goal.Execution.Ledger.NodeAttempts[0].TechnicalAttempts != 0 {
		t.Fatalf("non-technical step accounting = %#v", goal.Execution.Ledger)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("step-charged state is invalid: %v", err)
	}

	if _, err := state.PrepareExecutionReservation("goal", ExecutionReservation{
		ID: "run-002:step:1:START", Kind: ExecutionReservationStep, RunID: "run-002", CreatedAt: now.Add(time.Minute),
	}); err == nil {
		t.Fatal("a second run bypassed the cumulative total step cap")
	}
	goal, _ = state.GoalByID("goal")
	if goal.Execution.Ledger.StepsConsumed != 1 || len(goal.Execution.Ledger.Reservations) != 1 {
		t.Fatalf("rejected step changed cross-run accounting: %#v", goal.Execution.Ledger)
	}
}

func TestExecutionActionAttemptAndRunnerStepCapsAreIndependent(t *testing.T) {
	now := time.Date(2026, time.September, 21, 12, 0, 0, 0, time.UTC)
	state, execution := initialExecutionFixture(t, now)
	execution.Authorizations[0].Caps.MaxSteps = 2
	execution.Authorizations[0].Caps.MaxTechnicalAttemptsPerNode = 1
	if err := state.AdoptInitialExecution(execution); err != nil {
		t.Fatal(err)
	}

	actionRequest := ExecutionReservation{ID: "run-001:action:RESUME:node-first:1", Kind: ExecutionReservationAction,
		RunID: "run-001", PlanNodeRef: "node-first", CreatedAt: now}
	action, err := state.PrepareExecutionReservation("goal", actionRequest)
	if err != nil {
		t.Fatal(err)
	}
	goal, _ := state.GoalByID("goal")
	if goal.Execution.Ledger.StepsConsumed != 0 || goal.Execution.Ledger.NodeAttempts[0].TechnicalAttempts != 1 {
		t.Fatalf("Action accounting = %#v; want one technical attempt and no Runner step", goal.Execution.Ledger)
	}

	for ordinal := 1; ordinal <= 2; ordinal++ {
		_, err := state.PrepareExecutionReservation("goal", ExecutionReservation{
			ID: fmt.Sprintf("run-001:step:%d:RESUME:node-first", ordinal), Kind: ExecutionReservationStep,
			RunID: "run-001", PlanNodeRef: "node-first", CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("step %d: %v", ordinal, err)
		}
	}
	goal, _ = state.GoalByID("goal")
	if goal.Execution.Ledger.StepsConsumed != 2 || goal.Execution.Ledger.NodeAttempts[0].TechnicalAttempts != 1 {
		t.Fatalf("independent step/attempt accounting = %#v", goal.Execution.Ledger)
	}

	retry := actionRequest
	retry.CreatedAt = now.Add(time.Minute)
	replayed, err := state.PrepareExecutionReservation("goal", retry)
	if err != nil || replayed != action {
		t.Fatalf("human-wait retry action = %#v, err=%v; want the existing attempt charge", replayed, err)
	}
	if _, err := state.PrepareExecutionReservation("goal", ExecutionReservation{
		ID: "run-002:step:1:RESUME:node-first", Kind: ExecutionReservationStep,
		RunID: "run-002", PlanNodeRef: "node-first", CreatedAt: now,
	}); err == nil {
		t.Fatal("a third Runner step bypassed the cumulative step cap")
	}
	if _, err := state.PrepareExecutionReservation("goal", ExecutionReservation{
		ID: "run-001:action:RESUME:node-first:2", Kind: ExecutionReservationAction,
		RunID: "run-001", PlanNodeRef: "node-first", CreatedAt: now,
	}); err == nil {
		t.Fatal("a second technical attempt bypassed the per-node attempt cap")
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("independent step/attempt ledger is invalid: %v", err)
	}
}

func TestExecutionConfirmedNeedsHumanDispositionReclassifiesActionOnce(t *testing.T) {
	now := time.Date(2026, time.September, 21, 13, 0, 0, 0, time.UTC)
	state, execution := initialExecutionFixture(t, now)
	if err := state.AdoptInitialExecution(execution); err != nil {
		t.Fatal(err)
	}
	action, err := state.PrepareExecutionReservation("goal", ExecutionReservation{
		ID: "run-001:action:RESUME:node-first:1", Kind: ExecutionReservationAction,
		RunID: "run-001", PlanNodeRef: "node-first", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := ExecutionReservationReceiptFor(action)
	if err != nil {
		t.Fatal(err)
	}
	beforeReservation := action
	if err := state.ConfirmNeedsHumanDisposition("goal", "run-001", receipt); err != nil {
		t.Fatal(err)
	}
	goal, _ := state.GoalByID("goal")
	ledger := goal.Execution.Ledger
	if ledger.NodeAttempts[0].TechnicalAttempts != 0 || len(ledger.NeedsHumanDispositions) != 1 || ledger.Revision != 3 {
		t.Fatalf("confirmed needs_human ledger = %#v; want zero technical attempts and one disposition", ledger)
	}
	if ledger.Reservations[0] != beforeReservation {
		t.Fatalf("needs_human disposition mutated immutable action reservation: got %#v want %#v", ledger.Reservations[0], beforeReservation)
	}
	if goal.Execution.Witness.LedgerRevision != ledger.Revision || goal.Execution.Witness.LedgerDigest != ledger.Digest {
		t.Fatalf("witness was not resealed with needs_human disposition: witness=%#v ledger=%#v", goal.Execution.Witness, ledger)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("needs_human reclassification made state invalid: %v", err)
	}

	before, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.ConfirmNeedsHumanDisposition("goal", "run-001", receipt); err != nil {
		t.Fatalf("idempotent needs_human confirmation: %v", err)
	}
	after, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("idempotent needs_human confirmation changed the ledger")
	}
}

func TestExecutionNeedsHumanDispositionRejectsMismatchedReceiptOrRun(t *testing.T) {
	now := time.Date(2026, time.September, 21, 14, 0, 0, 0, time.UTC)
	state, execution := initialExecutionFixture(t, now)
	if err := state.AdoptInitialExecution(execution); err != nil {
		t.Fatal(err)
	}
	action, err := state.PrepareExecutionReservation("goal", ExecutionReservation{
		ID: "run-001:action:RESUME:node-first:1", Kind: ExecutionReservationAction,
		RunID: "run-001", PlanNodeRef: "node-first", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := ExecutionReservationReceiptFor(action)
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	badReceipt := receipt
	badReceipt.ReservationDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	for _, attempt := range []struct {
		runID   string
		receipt ExecutionReservationReceipt
	}{
		{runID: "run-001", receipt: badReceipt},
		{runID: "run-002", receipt: receipt},
	} {
		if err := state.ConfirmNeedsHumanDisposition("goal", attempt.runID, attempt.receipt); err == nil {
			t.Fatalf("mismatched needs_human confirmation was accepted: %#v", attempt)
		}
		after, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatalf("rejected needs_human confirmation mutated state: %#v", attempt)
		}
	}
}

func TestExecutionNeedsHumanDispositionFreesAttemptCapButRetainsStepCharge(t *testing.T) {
	now := time.Date(2026, time.September, 21, 15, 0, 0, 0, time.UTC)
	state, execution := initialExecutionFixture(t, now)
	execution.Authorizations[0].Caps.MaxTechnicalAttemptsPerNode = 1
	execution.Authorizations[0].Caps.MaxSteps = 1
	if err := state.AdoptInitialExecution(execution); err != nil {
		t.Fatal(err)
	}
	action, err := state.PrepareExecutionReservation("goal", ExecutionReservation{
		ID: "run-001:action:RESUME:node-first:1", Kind: ExecutionReservationAction,
		RunID: "run-001", PlanNodeRef: "node-first", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.PrepareExecutionReservation("goal", ExecutionReservation{
		ID: "run-001:step:1:RESUME:node-first", Kind: ExecutionReservationStep,
		RunID: "run-001", PlanNodeRef: "node-first", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	receipt, err := ExecutionReservationReceiptFor(action)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.ConfirmNeedsHumanDisposition("goal", "run-001", receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := state.PrepareExecutionReservation("goal", ExecutionReservation{
		ID: "run-001:action:RESUME:node-first:2", Kind: ExecutionReservationAction,
		RunID: "run-001", PlanNodeRef: "node-first", CreatedAt: now,
	}); err != nil {
		t.Fatalf("confirmed needs_human did not free technical attempt capacity: %v", err)
	}
	if _, err := state.PrepareExecutionReservation("goal", ExecutionReservation{
		ID: "run-001:step:2:RESUME:node-first", Kind: ExecutionReservationStep,
		RunID: "run-001", PlanNodeRef: "node-first", CreatedAt: now,
	}); err == nil {
		t.Fatal("confirmed needs_human refunded a consumed Runner step")
	}
	goal, _ := state.GoalByID("goal")
	if goal.Execution.Ledger.StepsConsumed != 1 || goal.Execution.Ledger.NodeAttempts[0].TechnicalAttempts != 1 {
		t.Fatalf("post-disposition accounting = %#v; want one consumed step and one technical attempt", goal.Execution.Ledger)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("post-disposition state is invalid: %v", err)
	}
}
