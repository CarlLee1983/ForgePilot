package work

import (
	"strings"
	"testing"
	"time"
)

const (
	revisionOne = "1111111111111111111111111111111111111111"
	revisionTwo = "2222222222222222222222222222222222222222"
)

var lifecycleNow = time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)

func commitAt(revision string) Candidate { return Candidate{Kind: CommitCandidate, Revision: revision} }

// lifecycleState builds a Goal "g" through ImportGoalPlan with the given nodes,
// each written as "id" or "id<dep,dep". Node order is the plan's order.
func lifecycleState(t *testing.T, requireApproval bool, nodes ...string) State {
	t.Helper()
	plan := GoalPlan{Goal: PlanGoal{ID: "g", Title: "Goal", RequireApproval: requireApproval}}
	for _, spec := range nodes {
		id, deps, _ := strings.Cut(spec, "<")
		node := PlanNode{ID: id, Story: "specs/stories/" + id, DependsOn: []string{}}
		if deps != "" {
			node.DependsOn = strings.Split(deps, ",")
		}
		plan.Nodes = append(plan.Nodes, node)
	}
	state := NewState()
	if _, err := state.ImportGoalPlan(plan, "/repo", lifecycleNow); err != nil {
		t.Fatal(err)
	}
	return state
}

// runVerification takes one READY or RUNNING node through a whole Verification
// Run at the given candidate, returning the Evidence it produced.
func runVerification(t *testing.T, state *State, id string, candidate Candidate, exitCode int) Evidence {
	t.Helper()
	if state.WorkItemStatus(id) == Ready {
		if err := state.Start(id, lifecycleNow); err != nil {
			t.Fatalf("start %s: %v", id, err)
		}
	}
	if err := state.BeginCandidateVerification(id, candidate, "/tmp/worktree", "", lifecycleNow); err != nil {
		t.Fatalf("begin verification of %s: %v", id, err)
	}
	evidence, err := state.RecordVerification(id, candidate.Revision, "make verify", exitCode, lifecycleNow)
	if err != nil {
		t.Fatalf("record verification of %s: %v", id, err)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("state invalid after verifying %s: %v", id, err)
	}
	return evidence
}

func wantStatus(t *testing.T, state *State, id string, want Status) {
	t.Helper()
	if got := state.WorkItemStatus(id); got != want {
		t.Fatalf("%s is %s, want %s", id, got, want)
	}
}

func wantGoal(t *testing.T, state *State, want GoalStatus) {
	t.Helper()
	goal, _ := state.GoalByID("g")
	if goal.Status != want {
		t.Fatalf("goal is %s, want %s", goal.Status, want)
	}
}

func TestPassWithoutApprovalRequirementCompletesWorkAndUnlocksDownstream(t *testing.T) {
	state := lifecycleState(t, false, "a", "b<a")
	wantStatus(t, &state, "b", Pending)

	runVerification(t, &state, "a", commitAt(revisionOne), 0)

	// One RecordVerification call is one transaction: everything below was
	// decided by it, with no further command in between.
	wantStatus(t, &state, "a", Done)
	wantStatus(t, &state, "b", Ready)
	wantGoal(t, &state, GoalActive)

	runVerification(t, &state, "b", commitAt(revisionOne), 0)
	wantStatus(t, &state, "b", Done)
	wantGoal(t, &state, GoalCompleted)
}

func TestGoalCompletesOnlyWhenTheLastWorkItemIsDone(t *testing.T) {
	state := lifecycleState(t, false, "a", "b")
	runVerification(t, &state, "a", commitAt(revisionOne), 0)
	wantStatus(t, &state, "a", Done)
	wantStatus(t, &state, "b", Ready)
	wantGoal(t, &state, GoalActive)

	// A failing run on the remaining item is not completion either.
	runVerification(t, &state, "b", commitAt(revisionOne), 1)
	wantStatus(t, &state, "b", Running)
	wantGoal(t, &state, GoalActive)

	runVerification(t, &state, "b", commitAt(revisionOne), 0)
	wantGoal(t, &state, GoalCompleted)
}

// ADR-0040: completion does not ask whether each PASS names the final
// Candidate. The first item passed at an older revision and the Goal still
// completes.
func TestGoalCompletionDoesNotRequireEveryPassAtTheFinalCandidate(t *testing.T) {
	state := lifecycleState(t, false, "a", "b<a")
	runVerification(t, &state, "a", commitAt(revisionOne), 0)
	runVerification(t, &state, "b", commitAt(revisionTwo), 0)
	wantGoal(t, &state, GoalCompleted)
}

func TestFailAndInterruptedReturnToRunningUnderEitherPolicy(t *testing.T) {
	for _, requireApproval := range []bool{false, true} {
		state := lifecycleState(t, requireApproval, "a")
		runVerification(t, &state, "a", commitAt(revisionOne), 1)
		wantStatus(t, &state, "a", Running)

		if err := state.BeginCandidateVerification("a", commitAt(revisionOne), "/tmp/worktree", "", lifecycleNow); err != nil {
			t.Fatal(err)
		}
		evidence, _, _, found, err := state.ReclaimRun("a", "make verify", lifecycleNow)
		if err != nil || !found || evidence.Result != Interrupted {
			t.Fatalf("require_approval=%t: reclaim = %#v, %v, %v", requireApproval, evidence, found, err)
		}
		wantStatus(t, &state, "a", Running)
		wantGoal(t, &state, GoalActive)
	}
}

func TestPassWithApprovalRequirementEntersReview(t *testing.T) {
	state := lifecycleState(t, true, "a", "b<a")
	runVerification(t, &state, "a", commitAt(revisionOne), 0)
	wantStatus(t, &state, "a", Review)
	// REVIEW is not DONE: nothing downstream moves and the Goal stays open.
	wantStatus(t, &state, "b", Pending)
	wantGoal(t, &state, GoalActive)
}

func TestApproveCompletesWorkUnlocksDownstreamAndCompletesGoal(t *testing.T) {
	state := lifecycleState(t, true, "a", "b<a")
	runVerification(t, &state, "a", commitAt(revisionOne), 0)

	review, err := state.RecordReview("a", revisionOne, Approved, "alice", "looks right", lifecycleNow)
	if err != nil {
		t.Fatal(err)
	}
	if review.Type != ReviewEvidence || review.Result != Approved {
		t.Fatalf("review evidence = %#v", review)
	}
	wantStatus(t, &state, "a", Done)
	wantStatus(t, &state, "b", Ready)
	wantGoal(t, &state, GoalActive)

	runVerification(t, &state, "b", commitAt(revisionOne), 0)
	wantStatus(t, &state, "b", Review)
	wantGoal(t, &state, GoalActive)
	if _, err := state.RecordReview("b", revisionOne, Approved, "alice", "", lifecycleNow); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, &state, "b", Done)
	wantGoal(t, &state, GoalCompleted)
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRejectReturnsToRunningAndNeedsAFreshPassToReenterReview(t *testing.T) {
	state := lifecycleState(t, true, "a")
	runVerification(t, &state, "a", commitAt(revisionOne), 0)

	if _, err := state.RecordReview("a", revisionOne, Rejected, "alice", "  ", lifecycleNow); err == nil {
		t.Fatal("rejected without a reason")
	}
	wantStatus(t, &state, "a", Review)

	if _, err := state.RecordReview("a", revisionOne, Rejected, "alice", "missing a test", lifecycleNow); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, &state, "a", Running)
	wantGoal(t, &state, GoalActive)
	// REVIEW is only reachable through a PASS, so rejection cannot be undone by
	// approving a RUNNING item.
	if _, err := state.RecordReview("a", revisionOne, Approved, "alice", "", lifecycleNow); err == nil {
		t.Fatal("approved work that is RUNNING")
	}

	runVerification(t, &state, "a", commitAt(revisionOne), 0)
	wantStatus(t, &state, "a", Review)
}

func TestApproveIsRefusedWhenTheCandidateIsStaleAndSaysSo(t *testing.T) {
	state := lifecycleState(t, true, "a")
	runVerification(t, &state, "a", commitAt(revisionOne), 0)
	evidenceBefore := len(state.Evidence)

	_, err := state.RecordCandidateReview("a", commitAt(revisionTwo), Approved, "alice", "", lifecycleNow)
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("approve against a newer candidate = %v, want a stale refusal", err)
	}
	wantStatus(t, &state, "a", Review)
	if len(state.Evidence) != evidenceBefore {
		t.Fatal("a refused approval still appended Evidence")
	}

	// A snapshot candidate is stale by digest, not by revision.
	snapshot := Candidate{Kind: SnapshotCandidate, Revision: "3333333333333333333333333333333333333333", BaseRevision: revisionOne, Digest: "sha256:" + strings.Repeat("a", 64)}
	other := snapshot
	other.Digest = "sha256:" + strings.Repeat("b", 64)
	state = lifecycleState(t, true, "a")
	runVerification(t, &state, "a", snapshot, 0)
	if _, err := state.RecordCandidateReview("a", other, Approved, "alice", "", lifecycleNow); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("approve against a changed snapshot = %v, want a stale refusal", err)
	}
	if _, err := state.RecordCandidateReview("a", snapshot, Approved, "alice", "", lifecycleNow); err != nil {
		t.Fatalf("approve against the verified snapshot: %v", err)
	}
	wantStatus(t, &state, "a", Done)
}

func TestStaleReviewCanBeVerifiedAgain(t *testing.T) {
	state := lifecycleState(t, true, "a")
	runVerification(t, &state, "a", commitAt(revisionOne), 0)
	if !state.CandidateStale("a", revisionTwo, "") {
		t.Fatal("REVIEW at an older revision is not stale")
	}
	if err := state.Verifiable("a"); err != nil {
		t.Fatalf("a stale REVIEW cannot be verified again: %v", err)
	}

	runVerification(t, &state, "a", commitAt(revisionTwo), 0)
	wantStatus(t, &state, "a", Review)
	if state.CandidateStale("a", revisionTwo, "") {
		t.Fatal("REVIEW is still stale after verifying the current candidate")
	}
	if _, err := state.RecordReview("a", revisionTwo, Approved, "alice", "", lifecycleNow); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, &state, "a", Done)

	// A re-verification that fails sends REVIEW work back to RUNNING.
	state = lifecycleState(t, true, "a")
	runVerification(t, &state, "a", commitAt(revisionOne), 0)
	runVerification(t, &state, "a", commitAt(revisionTwo), 1)
	wantStatus(t, &state, "a", Running)
}

func TestApproveIsRefusedByAnOpenGateUntilItCloses(t *testing.T) {
	state := lifecycleState(t, true, "a")
	runVerification(t, &state, "a", commitAt(revisionOne), 0)
	gate, err := state.OpenGate("a", "Ship it?", []string{"yes", "no"}, "", lifecycleNow)
	if err != nil {
		t.Fatal(err)
	}
	_, err = state.RecordReview("a", revisionOne, Approved, "alice", "", lifecycleNow)
	if err == nil || !strings.Contains(err.Error(), gate.ID) {
		t.Fatalf("approve under an open gate = %v, want a refusal naming %s", err, gate.ID)
	}
	wantStatus(t, &state, "a", Review)
	if err := state.ResolveGate(gate.ID, "yes", "", "alice", lifecycleNow); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordReview("a", revisionOne, Approved, "alice", "", lifecycleNow); err != nil {
		t.Fatalf("approve after the gate closed: %v", err)
	}
	wantStatus(t, &state, "a", Done)
}

func TestReviewIsRefusedWhenTheGoalRequiresNoApproval(t *testing.T) {
	state := lifecycleState(t, false, "a", "b")
	if err := state.Start("a", lifecycleNow); err != nil {
		t.Fatal(err)
	}
	for _, result := range []Result{Approved, Rejected} {
		_, err := state.RecordReview("a", revisionOne, result, "alice", "because", lifecycleNow)
		if err == nil || !strings.Contains(err.Error(), "does not require approval") {
			t.Fatalf("%s on a Goal without an Approval Requirement = %v, want the reason", result, err)
		}
		if err := state.Reviewable("a"); err == nil || !strings.Contains(err.Error(), "does not require approval") {
			t.Fatalf("Reviewable = %v", err)
		}
	}
	if len(state.Evidence) != 0 {
		t.Fatal("a refused review appended Evidence")
	}
}

func TestReviewOnlyAppliesToWorkInReview(t *testing.T) {
	state := lifecycleState(t, true, "a", "b<a")
	for _, id := range []string{"a", "b"} {
		if _, err := state.RecordReview(id, revisionOne, Approved, "alice", "", lifecycleNow); err == nil || !strings.Contains(err.Error(), "only REVIEW work") {
			t.Fatalf("approve %s (%s) = %v", id, state.WorkItemStatus(id), err)
		}
	}
}

func TestDoneIsTerminalAndNeverStale(t *testing.T) {
	for _, requireApproval := range []bool{false, true} {
		state := lifecycleState(t, requireApproval, "a", "b")
		runVerification(t, &state, "a", commitAt(revisionOne), 0)
		if requireApproval {
			if _, err := state.RecordReview("a", revisionOne, Approved, "alice", "", lifecycleNow); err != nil {
				t.Fatal(err)
			}
		}
		wantStatus(t, &state, "a", Done)
		if state.CandidateStale("a", revisionTwo, "") || state.Stale("a", revisionTwo) {
			t.Fatalf("require_approval=%t: DONE work was reported stale", requireApproval)
		}
		if err := state.Start("a", lifecycleNow); err == nil {
			t.Fatal("started DONE work")
		}
		if err := state.Verifiable("a"); err == nil {
			t.Fatal("DONE work can be verified again")
		}
		if _, err := state.RecordReview("a", revisionOne, Rejected, "alice", "redo", lifecycleNow); err == nil {
			t.Fatal("reopened DONE work by rejecting it")
		}
		wantStatus(t, &state, "a", Done)
	}
}

// Cancelling a Goal while a run is in flight still records what the run found,
// but a PASS under a Goal that is no longer ACTIVE does not complete the work.
func TestPassUnderACancelledGoalRecordsEvidenceWithoutCompleting(t *testing.T) {
	state := lifecycleState(t, false, "a")
	if err := state.Start("a", lifecycleNow); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginCandidateVerification("a", commitAt(revisionOne), "/tmp/worktree", "", lifecycleNow); err != nil {
		t.Fatal(err)
	}
	if err := state.CancelGoal("g", "dropped", lifecycleNow); err != nil {
		t.Fatal(err)
	}
	evidence, err := state.RecordVerification("a", revisionOne, "make verify", 0, lifecycleNow)
	if err != nil || evidence.Result != Pass {
		t.Fatalf("record = %#v, %v", evidence, err)
	}
	wantStatus(t, &state, "a", Running)
	wantGoal(t, &state, GoalCancelled)
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestGoalHasOnlyActiveCompletedAndCancelledStates(t *testing.T) {
	state := lifecycleState(t, false, "a")
	for _, removed := range []GoalStatus{"BLOCKED", "PAUSED", ""} {
		state.Goals[0].Status = removed
		if err := state.Validate(); err == nil {
			t.Errorf("accepted goal status %q", removed)
		}
	}
	state.Goals[0].Status = GoalCancelled
	if err := state.Validate(); err == nil {
		t.Error("accepted a cancelled goal without a reason")
	}
	state.Goals[0].Reason = "dropped"
	if err := state.Validate(); err != nil {
		t.Errorf("rejected a cancelled goal with a reason: %v", err)
	}
	if err := state.CancelGoal("g", "again", lifecycleNow); err == nil {
		t.Error("cancelled a goal twice")
	}
}

func TestValidateHoldsTheLifecycleInvariants(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*State)
		want   string
	}{
		{"VERIFIED is not a status", func(s *State) { s.WorkItems[0].Status = "VERIFIED" }, "invalid status"},
		{"REVIEW without a PASS", func(s *State) { s.Evidence = nil; s.NextEvidenceID = 1; s.NextVerificationRunID = 1 }, "REVIEW without"},
		{"REVIEW under a Goal without approval", func(s *State) { s.Goals[0].RequireApproval = false }, "approval"},
		{"completed Goal with unfinished work", func(s *State) { s.Goals[0].Status = GoalCompleted }, "unfinished"},
	}
	for _, tc := range cases {
		state := lifecycleState(t, true, "a")
		runVerification(t, &state, "a", commitAt(revisionOne), 0)
		if err := state.Validate(); err != nil {
			t.Fatalf("%s: baseline invalid: %v", tc.name, err)
		}
		tc.mutate(&state)
		if err := state.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Validate = %v, want it to mention %q", tc.name, err, tc.want)
		}
	}
}
