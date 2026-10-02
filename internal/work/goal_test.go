package work

import (
	"testing"
)

func TestGoalCancelRequiresAReasonAndStopsNewWork(t *testing.T) {
	state, now, _ := reviewFixture(t)
	if _, err := state.AddWork("g", "specs/stories/c", nil, now); err != nil {
		t.Fatal(err)
	}
	if err := state.CancelGoal("unknown", "wrong", now); err == nil {
		t.Fatal("cancelled an unknown goal")
	}
	if err := state.CancelGoal("g", "  ", now); err == nil {
		t.Fatal("cancelled a goal without a reason")
	}
	if err := state.CancelGoal("g", "the customer withdrew the request", now); err != nil {
		t.Fatal(err)
	}
	if state.Goals[0].Status != GoalCancelled || state.Goals[0].Reason != "the customer withdrew the request" {
		t.Fatalf("goal = %#v", state.Goals[0])
	}
	// What a cancelled Goal stops is starting new work, verifying and being
	// picked as next; work already in REVIEW stays where it was.
	if err := state.Start("WI-003", now); err == nil {
		t.Fatal("started work under a cancelled goal")
	}
	if err := state.Verifiable("WI-001"); err == nil {
		t.Fatal("verified work under a cancelled goal")
	}
	if action := state.ActionableNext(RepositoryState{}); action.Kind != NextActionGoalCancelled {
		t.Fatalf("next = %#v, want the cancelled Goal reported and no work selected", action)
	}
	if got := state.WorkItemStatus("WI-001"); got != Review {
		t.Fatalf("WI-001 = %s, want REVIEW untouched", got)
	}
	if err := state.CancelGoal("g", "again", now); err == nil {
		t.Fatal("cancelled a goal twice")
	}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
}

// A completed Goal is an end: it cannot be cancelled afterwards.
func TestCompletedGoalCannotBeCancelled(t *testing.T) {
	state := lifecycleState(t, false, "a")
	runVerification(t, &state, "a", commitAt(revisionOne), 0)
	wantGoal(t, &state, GoalCompleted)
	if err := state.CancelGoal("g", "reconsidered", lifecycleNow); err == nil {
		t.Fatal("cancelled a completed goal")
	}
}
