package work

import (
	"strings"
	"testing"
)

// Work of an ended Goal can never start, so it has no readiness to compute: it
// shows its stored NOT_STARTED instead of a READY nobody can act on.
func TestEndedGoalWorkHasNoReadiness(t *testing.T) {
	state := lifecycleState(t, false, "a", "b<a")
	if err := state.CancelGoal("g", "dropped", lifecycleNow); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if _, ok := state.Readiness(id); ok {
			t.Errorf("%s has a readiness under a cancelled Goal", id)
		}
		if got := state.DisplayStatus(id); got != "NOT_STARTED" {
			t.Errorf("%s displays %s, want NOT_STARTED", id, got)
		}
	}
}

// While a verifier is alive next waits for it, and still lists the people-waits
// that exist at the same time so the reader sees the whole picture.
func TestNextWaitForLiveVerifierAlsoListsGatesAndReviews(t *testing.T) {
	state := lifecycleState(t, true, "a", "b", "c")
	runVerification(t, &state, "a", commitAt(revisionOne), 0)
	if _, err := state.OpenGate("c", "Q?", []string{"x", "y"}, "", lifecycleNow); err != nil {
		t.Fatal(err)
	}
	if err := state.Start("b", lifecycleNow); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginCandidateVerification("b", commitAt(revisionOne), "/tmp/w", "", lifecycleNow); err != nil {
		t.Fatal(err)
	}
	action := state.ActionableNext(RepositoryState{Revision: revisionOne})
	var kinds []WaitKind
	for _, waiting := range action.Waiting {
		kinds = append(kinds, waiting.Kind)
	}
	if action.Kind != NextActionWait || len(kinds) != 3 || kinds[0] != WaitVerification || kinds[1] != WaitReview || kinds[2] != WaitGate {
		t.Fatalf("action = %#v, want WAIT listing VERIFICATION, REVIEW and GATE", action)
	}
}

// A REVIEW item cannot be rejected or verified again while another item holds
// the workspace; its obstacles say so and name a way out. A READY item's
// OCCUPIED message names the way out too.
func TestOccupiedObstaclesExplainBlockedActionsAndWayOut(t *testing.T) {
	state := lifecycleState(t, true, "a", "b", "c")
	runVerification(t, &state, "a", commitAt(revisionOne), 0)
	if err := state.Start("b", lifecycleNow); err != nil {
		t.Fatal(err)
	}
	occupied := func(id string) *Obstacle {
		for _, obstacle := range state.Obstacles(id) {
			if obstacle.Kind == ObstacleOccupied {
				obstacle := obstacle
				return &obstacle
			}
		}
		return nil
	}
	review := occupied("a")
	if review == nil || review.Ref != "b" || !strings.Contains(review.Message, "reject") || !strings.Contains(review.Message, "verify") || !strings.Contains(review.Message, "goal cancel") {
		t.Fatalf("obstacles of REVIEW work = %#v, want OCCUPIED by b mentioning reject, verify and goal cancel", state.Obstacles("a"))
	}
	ready := occupied("c")
	if ready == nil || ready.Ref != "b" || !strings.Contains(ready.Message, "goal cancel") || !strings.Contains(ready.Message, "verify") {
		t.Fatalf("obstacles of READY work = %#v, want OCCUPIED by b naming the way out", state.Obstacles("c"))
	}
}
