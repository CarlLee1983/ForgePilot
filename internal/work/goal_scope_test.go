package work

import (
	"reflect"
	"testing"
	"time"
)

// twoGoalFixture builds one repository holding two independent GOAL-policy
// Goals. The first Goal is deliberately the one a global query would answer
// with, so a Goal-scoped query that merely filtered the global answer would
// report a stall for the second Goal instead of its own legal work.
func twoGoalFixture(t *testing.T) (State, time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 13, 3, 0, 0, 0, time.UTC)
	state := NewState()
	for _, id := range []string{"alpha", "beta"} {
		if err := state.AddGoalWithReviewPolicy(id, "Goal "+id, "", "/repo", ReviewPerGoal, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := state.AddWork("alpha", "specs/stories/alpha-one", nil, now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.AddWork("beta", "specs/stories/beta-one", nil, now); err != nil {
		t.Fatal(err)
	}
	return state, now
}

func TestActionableNextForGoalAnswersOnlyTheNamedGoal(t *testing.T) {
	state, _ := twoGoalFixture(t)

	before := state
	action := state.ActionableNextForGoal("beta", RepositoryState{})
	if action.Kind != NextActionStart || action.Item.ID != "WI-002" {
		t.Fatalf("beta action = %#v", action)
	}
	if !reflect.DeepEqual(before, state) {
		t.Fatal("ActionableNextForGoal changed state")
	}

	action = state.ActionableNextForGoal("alpha", RepositoryState{})
	if action.Kind != NextActionStart || action.Item.ID != "WI-001" {
		t.Fatalf("alpha action = %#v", action)
	}

	// The global query keeps answering with the earliest work across both Goals.
	if global := state.ActionableNext(RepositoryState{}); global.Kind != NextActionStart || global.Item.ID != "WI-001" {
		t.Fatalf("global action = %#v", global)
	}
}

// A Goal whose own work is blocked must report that, not borrow the other
// Goal's waiting reason — and must not present the other Goal's work either.
func TestActionableNextForGoalDoesNotBorrowAnotherGoalsBlocker(t *testing.T) {
	state, now := twoGoalFixture(t)
	if _, err := state.OpenGate("WI-001", "which way?", []string{"left", "right"}, "", now); err != nil {
		t.Fatal(err)
	}

	if action := state.ActionableNextForGoal("beta", RepositoryState{}); action.Kind != NextActionStart || action.Item.ID != "WI-002" {
		t.Fatalf("beta action = %#v", action)
	}
	action := state.ActionableNextForGoal("alpha", RepositoryState{})
	if action.Kind != NextActionWaitGate || action.Item.ID != "WI-001" {
		t.Fatalf("alpha action = %#v", action)
	}
}

// Scoping selects candidates; it must not narrow what the dependency rules can
// see. A Goal-scoped query still reads the whole state to judge prerequisites.
func TestActionableNextForGoalStillJudgesDependenciesAgainstFullState(t *testing.T) {
	now := time.Date(2026, 9, 13, 3, 0, 0, 0, time.UTC)
	state := NewState()
	if err := state.AddGoalWithReviewPolicy("goal", "Goal", "", "/repo", ReviewPerGoal, now); err != nil {
		t.Fatal(err)
	}
	first, err := state.AddWork("goal", "specs/stories/one", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.AddWork("goal", "specs/stories/two", []string{first.ID}, now); err != nil {
		t.Fatal(err)
	}
	revision := "1111111111111111111111111111111111111111"
	passVerificationFor(t, &state, first.ID, now, Candidate{Kind: CommitCandidate, Revision: revision},
		RepositoryState{Revision: revision})

	action := state.ActionableNextForGoal("goal", RepositoryState{Revision: revision})
	if action.Kind != NextActionStart || action.Item.ID != "WI-002" {
		t.Fatalf("action = %#v", action)
	}
	// With a moved Candidate the prerequisite is stale, so the downstream work is
	// not startable and the owed re-verification is what is left.
	action = state.ActionableNextForGoal("goal", RepositoryState{Revision: "2222222222222222222222222222222222222222"})
	if action.Kind != NextActionReverify || action.Item.ID != first.ID {
		t.Fatalf("stale action = %#v", action)
	}
}

func TestGoalStallClassifiesWhyNothingCanAdvance(t *testing.T) {
	now := time.Date(2026, 9, 13, 3, 0, 0, 0, time.UTC)
	state := NewState()
	if err := state.AddGoalWithReviewPolicy("goal", "Goal", "", "/repo", ReviewPerGoal, now); err != nil {
		t.Fatal(err)
	}

	if stall := state.GoalStall("missing", RepositoryState{}); stall.Kind != StallUnknownGoal {
		t.Fatalf("unknown goal stall = %#v", stall)
	}
	if stall := state.GoalStall("goal", RepositoryState{}); stall.Kind != StallEmptyGoal {
		t.Fatalf("empty goal stall = %#v", stall)
	}

	first, err := state.AddWork("goal", "specs/stories/one", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.AddWork("goal", "specs/stories/two", []string{first.ID}, now); err != nil {
		t.Fatal(err)
	}
	if err := state.Start(first.ID, now); err != nil {
		t.Fatal(err)
	}
	revision := "1111111111111111111111111111111111111111"
	if err := state.BeginVerification(first.ID, revision, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	stall := state.GoalStall("goal", RepositoryState{})
	if stall.Kind != StallVerifying || !reflect.DeepEqual(stall.ItemIDs, []string{first.ID}) {
		t.Fatalf("verifying stall = %#v", stall)
	}

	if _, err := state.RecordVerification(first.ID, revision, "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
	// WI-002 is now startable, so nothing is stalled.
	if stall := state.GoalStall("goal", RepositoryState{Revision: revision}); stall.Kind != StallNone {
		t.Fatalf("advanceable stall = %#v", stall)
	}
	// Without the repository fact the prerequisite cannot be shown fresh, so the
	// downstream work is held by its dependency rather than by a person.
	stall = state.GoalStall("goal", RepositoryState{})
	if stall.Kind != StallDependenciesUnsatisfied || !reflect.DeepEqual(stall.ItemIDs, []string{"WI-002"}) {
		t.Fatalf("dependency stall = %#v", stall)
	}

	if err := state.BlockGoal("goal", "direction changed", now); err != nil {
		t.Fatal(err)
	}
	if stall := state.GoalStall("goal", RepositoryState{}); stall.Kind != StallGoalNotActive {
		t.Fatalf("inactive goal stall = %#v", stall)
	}
}

func passVerificationFor(t *testing.T, state *State, id string, now time.Time, candidate Candidate, repository RepositoryState) {
	t.Helper()
	if err := state.Start(id, now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginCandidateVerification(id, candidate, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerificationWithRepository(id, candidate.Revision, "make verify", 0, repository, now); err != nil {
		t.Fatal(err)
	}
}
