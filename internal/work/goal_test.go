package work

import (
	"strings"
	"testing"
	"time"
)

func TestGoalBlockIsReversibleAndLeavesActiveWorkAlone(t *testing.T) {
	state, now, revision := reviewFixture(t)
	if _, err := state.AddWork("g", "specs/stories/c", nil, now); err != nil {
		t.Fatal(err)
	}
	if err := state.Start("WI-003", now); err != nil {
		t.Fatal(err)
	}

	if err := state.BlockGoal("g", "", now); err == nil {
		t.Fatal("blocked a goal without a reason")
	}
	if err := state.BlockGoal("unknown", "wrong direction", now); err == nil {
		t.Fatal("blocked an unknown goal")
	}
	if err := state.BlockGoal("g", "the direction is wrong", now); err != nil {
		t.Fatal(err)
	}
	if state.Goals[0].Status != GoalBlocked || state.Goals[0].Reason != "the direction is wrong" {
		t.Fatalf("goal = %#v", state.Goals[0])
	}

	// Blocking pauses; it does not discard state. The work that was RUNNING or
	// in REVIEW stays exactly where it was.
	if got := state.WorkItemStatus("WI-003"); got != Running {
		t.Fatalf("WI-003 = %s, want RUNNING", got)
	}
	if got := state.WorkItemStatus("WI-001"); got != Review {
		t.Fatalf("WI-001 = %s, want REVIEW", got)
	}
	// What it does stop is starting new work, verifying, reaching DONE, and
	// being picked as next.
	if err := state.Verifiable("WI-003"); err == nil {
		t.Fatal("verified work under a blocked goal")
	}
	if _, ok := state.Next(); ok {
		t.Fatal("next selected work under a blocked goal")
	}
	if _, err := state.AddWork("g", "specs/stories/b", nil, now); err == nil {
		t.Fatal("added work to a blocked goal")
	}

	if err := state.UnblockGoal("g", now); err != nil {
		t.Fatal(err)
	}
	if state.Goals[0].Status != GoalActive || state.Goals[0].Reason != "" {
		t.Fatalf("goal = %#v, want ACTIVE with no lingering reason", state.Goals[0])
	}
	if err := state.UnblockGoal("g", now); err == nil {
		t.Fatal("unblocked a goal that was not blocked")
	}
	// Nothing was lost: the work is where it was and can move on.
	if err := state.Verifiable("WI-003"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordReview("WI-001", revision, Approved, "carl@example.com", "", now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus("WI-001"); got != Done {
		t.Fatalf("WI-001 = %s, want DONE after unblocking", got)
	}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestBlockedGoalStillRecordsAVerificationAlreadyUnderway draws the line an
// inactive Goal is meant to draw: it stops new work and completion, not the
// recording of something that already happened.
func TestBlockedGoalStillRecordsAVerificationAlreadyUnderway(t *testing.T) {
	now := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	revision := "1111111111111111111111111111111111111111"
	state := NewState()
	if err := state.AddGoal("g", "Goal", "", "/repo", now); err != nil {
		t.Fatal(err)
	}
	item, err := state.AddWork("g", "specs/stories/a", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Start(item.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginVerification(item.ID, revision, "/tmp/worktree", now); err != nil {
		t.Fatal(err)
	}
	if err := state.BlockGoal("g", "the direction is wrong", now); err != nil {
		t.Fatal(err)
	}
	evidence, err := state.RecordVerification(item.ID, revision, "make verify", 0, now)
	if err != nil {
		t.Fatalf("a run already underway lost its result when the goal was blocked: %v", err)
	}
	if evidence.Result != Pass || evidence.Revision != revision {
		t.Fatalf("evidence = %#v", evidence)
	}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestGoalCompleteRequiresEveryWorkItemDone(t *testing.T) {
	state, now, revision := reviewFixture(t)

	err := state.CompleteGoal("g", now)
	if err == nil {
		t.Fatal("completed a goal with unfinished work")
	}
	if !strings.Contains(err.Error(), "WI-001") {
		t.Fatalf("error %q does not name the work that is not done", err)
	}

	if _, err := state.RecordReview("WI-001", revision, Approved, "carl@example.com", "", now); err != nil {
		t.Fatal(err)
	}
	if err := state.CompleteGoal("g", now); err == nil {
		t.Fatal("completed a goal while WI-002 is still open")
	}
	state.WorkItems[1].Status = Done

	if err := state.CompleteGoal("g", now); err != nil {
		t.Fatal(err)
	}
	if state.Goals[0].Status != GoalCompleted {
		t.Fatalf("goal = %#v", state.Goals[0])
	}
	// COMPLETED is an end, not a pause: nothing moves it again.
	if err := state.BlockGoal("g", "reconsidered", now); err == nil {
		t.Fatal("blocked a completed goal")
	}
	if err := state.CancelGoal("g", "reconsidered", now); err == nil {
		t.Fatal("cancelled a completed goal")
	}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestGoalCancelRequiresAReason(t *testing.T) {
	state, now, _ := reviewFixture(t)
	if err := state.CancelGoal("g", "  ", now); err == nil {
		t.Fatal("cancelled a goal without a reason")
	}
	if err := state.CancelGoal("g", "the customer withdrew the request", now); err != nil {
		t.Fatal(err)
	}
	if state.Goals[0].Status != GoalCancelled || state.Goals[0].Reason != "the customer withdrew the request" {
		t.Fatalf("goal = %#v", state.Goals[0])
	}
	if err := state.UnblockGoal("g", now); err == nil {
		t.Fatal("unblocked a cancelled goal")
	}
	if err := state.CompleteGoal("g", now); err == nil {
		t.Fatal("completed a cancelled goal")
	}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
}
