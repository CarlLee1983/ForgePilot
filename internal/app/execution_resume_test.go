package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestExactResumeRefusesBindingOrAuthorizationDriftWithoutChangingHistory(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("plan = %#v, err=%v", preview, err)
	}
	authorized, err := AuthorizeExecutionFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator")
	if err != nil {
		t.Fatal(err)
	}
	const runID = "run-exact-resume"
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
	record, err := json.Marshal(runnerRunRecordAudit{RunID: runID, GoalID: authorized.GoalID,
		ExecutionAuthorizationDigest: authorized.Authorizations[0].Digest, RunReservationID: reservation.ID,
		ReservationReceipts: []work.ExecutionReservationReceipt{receipt}})
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteRunArtifact(fixture.root, runID, "run.json", record, storage.ArtifactLimits{}); err != nil {
		t.Fatal(err)
	}
	before, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	caps := authorized.Authorizations[0].Caps.Artifacts
	limits := work.ExecutionArtifactLimits{MaxHandoffBytes: caps.MaxHandoffBytes, MaxWriteBytes: caps.MaxWriteBytes,
		MaxRunBytes: caps.MaxRunBytes, MaxTotalBytes: caps.MaxTotalBytes}

	if err := os.WriteFile(filepath.Join(fixture.root, "manifest.json"), []byte("binding drift"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRunnerResume(fixture.root, authorized.GoalID, runID, authorized.Authorizations[0].Digest,
		reservation.ID, []work.ExecutionReservationReceipt{receipt}, limits, RunnerIdentity{}, now); err == nil || !strings.Contains(err.Error(), "execution binding drift") {
		t.Fatalf("exact resume binding drift error = %v", err)
	}
	afterBindingDrift, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(marshalState(t, before), marshalState(t, afterBindingDrift)) {
		t.Fatal("binding drift refusal changed the historical authorization or charged ledger")
	}

	if _, err := ValidateRunnerResume(fixture.root, authorized.GoalID, runID, "sha256:authorization-drift",
		reservation.ID, []work.ExecutionReservationReceipt{receipt}, limits, RunnerIdentity{}, now); err == nil || !strings.Contains(err.Error(), "different execution authorization") {
		t.Fatalf("exact resume authorization drift error = %v", err)
	}
	afterAuthorizationDrift, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(marshalState(t, before), marshalState(t, afterAuthorizationDrift)) {
		t.Fatal("authorization drift refusal changed the historical authorization or charged ledger")
	}
}
