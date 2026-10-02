package work

import (
	"strings"
	"testing"
)

// OpenGate is allowed on VERIFYING work, so a Gate can appear while a check is
// running. The PASS is then recorded but cannot complete the work: the same
// criterion approve uses (no open Gate) applies, and the state stays valid
// instead of being refused whole at save time.
func TestPassWithAGateOpenedMidRunRecordsEvidenceWithoutCompleting(t *testing.T) {
	state := lifecycleState(t, false, "a", "b<a")
	if err := state.Start("a", lifecycleNow); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginCandidateVerification("a", commitAt(revisionOne), "/tmp/worktree", "", lifecycleNow); err != nil {
		t.Fatal(err)
	}
	gate, err := state.OpenGate("a", "Ship it?", []string{"yes", "no"}, "", lifecycleNow)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := state.RecordVerification("a", revisionOne, "make verify", 0, lifecycleNow)
	if err != nil || evidence.Result != Pass {
		t.Fatalf("record = %#v, %v", evidence, err)
	}
	wantStatus(t, &state, "a", string(Running))
	wantStatus(t, &state, "b", "PENDING")
	wantGoal(t, &state, GoalActive)
	if err := state.Validate(); err != nil {
		t.Fatalf("state invalid after a PASS under an open gate: %v", err)
	}
	if err := state.CompletionBlock("a"); err == nil || !strings.Contains(err.Error(), gate.ID) {
		t.Fatalf("CompletionBlock = %v, want it to name %s", err, gate.ID)
	}

	// Once the Gate closes, verifying again completes the work.
	if err := state.ResolveGate(gate.ID, "yes", "", "alice", lifecycleNow); err != nil {
		t.Fatal(err)
	}
	runVerification(t, &state, "a", commitAt(revisionOne), 0)
	wantStatus(t, &state, "a", string(Done))
	wantStatus(t, &state, "b", "READY")
}

// Under an Approval Requirement a PASS is not completion, so it enters REVIEW
// as usual; approve is what the Gate holds back.
func TestPassWithAGateOpenedMidRunStillEntersReviewUnderApproval(t *testing.T) {
	state := lifecycleState(t, true, "a")
	if err := state.Start("a", lifecycleNow); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginCandidateVerification("a", commitAt(revisionOne), "/tmp/worktree", "", lifecycleNow); err != nil {
		t.Fatal(err)
	}
	if _, err := state.OpenGate("a", "Ship it?", []string{"yes", "no"}, "", lifecycleNow); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerification("a", revisionOne, "make verify", 0, lifecycleNow); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, &state, "a", string(Review))
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordReview("a", revisionOne, Approved, "alice", "", lifecycleNow); err == nil {
		t.Fatal("approved REVIEW work under an open gate")
	}
}

// Review Evidence belongs only to Goals that require approval.
func TestValidateRejectsReviewEvidenceUnderAGoalWithoutApproval(t *testing.T) {
	state := lifecycleState(t, true, "a")
	runVerification(t, &state, "a", commitAt(revisionOne), 0)
	if _, err := state.RecordReview("a", revisionOne, Approved, "alice", "", lifecycleNow); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, &state, "a", string(Done))
	if err := state.Validate(); err != nil {
		t.Fatalf("baseline invalid: %v", err)
	}
	state.Goals[0].RequireApproval = false
	err := state.Validate()
	if err == nil || !strings.Contains(err.Error(), "is a review under goal") {
		t.Fatalf("Validate = %v, want the review-evidence rule", err)
	}
}

// Rejecting needs no authority a stale Candidate lacks: it is not refused for
// staleness, and it is recorded against the PASS it judged.
func TestRejectIsNotRefusedWhenTheCandidateIsStale(t *testing.T) {
	state := lifecycleState(t, true, "a")
	runVerification(t, &state, "a", commitAt(revisionOne), 0)
	verification, _ := state.LatestVerification("a")
	review, err := state.RecordCandidateReview("a", verification.Candidate(), Rejected, "alice", "redo", lifecycleNow)
	if err != nil {
		t.Fatal(err)
	}
	if review.Revision != revisionOne {
		t.Fatalf("review names %s, want the verified candidate %s", review.Revision, revisionOne)
	}
	wantStatus(t, &state, "a", string(Running))
}
