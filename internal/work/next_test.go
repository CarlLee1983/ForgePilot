package work

import (
	"reflect"
	"testing"
	"time"
)

func TestActionableNextPrioritizesExistingAgentWork(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	state := NewState()
	if err := state.AddGoal("g", "Goal", "", "/repo", now); err != nil {
		t.Fatal(err)
	}
	first, err := state.AddWork("g", "specs/stories/one", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := state.AddWork("g", "specs/stories/two", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Start(first.ID, now); err != nil {
		t.Fatal(err)
	}

	before := state
	action := state.ActionableNext(RepositoryState{})
	if action.Kind != NextActionResume || action.Item.ID != first.ID || action.Reason != "work is already in progress" {
		t.Fatalf("action = %#v", action)
	}
	if !reflect.DeepEqual(before, state) {
		t.Fatal("ActionableNext changed state")
	}

	// Only one work item may run, so the second start is refused and next keeps
	// pointing at the first rather than offering the second.
	if err := state.Start(second.ID, now); err == nil {
		t.Fatal("started a second work item while the first is RUNNING")
	}
	action = state.ActionableNext(RepositoryState{})
	if action.Kind != NextActionResume || action.Item.ID != first.ID {
		t.Fatalf("action with a second READY item = %#v, want RESUME of the running one", action)
	}
}

func TestActionableNextRepairsFailedRunningWork(t *testing.T) {
	state, now, revision := summaryFixture(t)
	if err := state.Start("WI-001", now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginCandidateVerification("WI-001", Candidate{Kind: CommitCandidate, Revision: revision}, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerification("WI-001", revision, "make verify", 1, now); err != nil {
		t.Fatal(err)
	}
	action := state.ActionableNext(RepositoryState{Revision: revision})
	if action.Kind != NextActionRepair || action.Item.ID != "WI-001" || action.Reason != "latest verification failed" {
		t.Fatalf("action = %#v", action)
	}
}

func TestActionableNextReverifiesStaleCandidates(t *testing.T) {
	t.Run("commit", func(t *testing.T) {
		state, now, revision := summaryFixture(t)
		passVerification(t, &state, now, Candidate{Kind: CommitCandidate, Revision: revision})
		action := state.ActionableNext(RepositoryState{Revision: "2222222222222222222222222222222222222222"})
		if action.Kind != NextActionReverify || action.Item.ID != "WI-001" || action.Reason != "verified candidate is stale" {
			t.Fatalf("action = %#v", action)
		}
	})

	t.Run("snapshot", func(t *testing.T) {
		state, now, revision := summaryFixture(t)
		candidate := Candidate{Kind: SnapshotCandidate, Revision: "2222222222222222222222222222222222222222", BaseRevision: revision,
			Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
		passVerification(t, &state, now, candidate)
		fresh := state.ActionableNext(RepositoryState{SnapshotDigest: candidate.Digest})
		if fresh.Kind != NextActionWait {
			t.Fatalf("fresh snapshot action = %#v", fresh)
		}
		stale := state.ActionableNext(RepositoryState{SnapshotDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"})
		if stale.Kind != NextActionReverify || stale.Item.ID != "WI-001" {
			t.Fatalf("stale snapshot action = %#v", stale)
		}
	})
}

func TestActionableNextUsesExistingReadySelection(t *testing.T) {
	old, same := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	state := State{SchemaVersion: SchemaVersion, NextEvidenceID: 1, NextGateID: 1, NextVerificationRunID: 1,
		Goals: []Goal{{ID: "g", Title: "Goal", Repository: "/repo", Status: GoalActive, RequireApproval: true}},
		WorkItems: []Item{{ID: "WI-003", GoalID: "g", StoryRef: "specs/stories/three", Status: NotStarted, CreatedAt: same},
			{ID: "WI-002", GoalID: "g", StoryRef: "specs/stories/two", Status: NotStarted, CreatedAt: same},
			{ID: "WI-001", GoalID: "g", StoryRef: "specs/stories/one", Status: NotStarted, CreatedAt: old}}}
	action := state.ActionableNext(RepositoryState{})
	if action.Kind != NextActionStart || action.Item.ID != "WI-001" || action.Reason != "earliest READY work" {
		t.Fatalf("action = %#v", action)
	}
}

func TestActionableNextReportsOnlyHumanBlockersWhenNothingCanAdvance(t *testing.T) {
	t.Run("human review", func(t *testing.T) {
		state, now, revision := summaryFixture(t)
		passVerification(t, &state, now, Candidate{Kind: CommitCandidate, Revision: revision})
		action := state.ActionableNext(RepositoryState{Revision: revision})
		want := []Waiting{{Kind: WaitReview, ItemID: "WI-001", GoalID: "goal", Reason: "human review required"}}
		if action.Kind != NextActionWait || action.Reason != "human review required" || !reflect.DeepEqual(action.Waiting, want) {
			t.Fatalf("action = %#v, want WAIT listing %#v", action, want)
		}
	})

	t.Run("open gate", func(t *testing.T) {
		state, now, _ := summaryFixture(t)
		gate, err := state.OpenGate("WI-001", "Decide?", []string{"yes", "no"}, "", now)
		if err != nil {
			t.Fatal(err)
		}
		action := state.ActionableNext(RepositoryState{})
		want := []Waiting{{Kind: WaitGate, ItemID: "WI-001", GoalID: "goal", GateID: gate.ID, Reason: "unresolved Gate " + gate.ID}}
		if action.Kind != NextActionWait || !reflect.DeepEqual(action.Waiting, want) {
			t.Fatalf("action = %#v, want WAIT listing %#v", action, want)
		}
	})

	// A cancelled Goal is terminal: nobody can usefully wait for it, so it is
	// reported as ended instead of as a wait.
	t.Run("cancelled goal", func(t *testing.T) {
		state, now, _ := summaryFixture(t)
		if err := state.CancelGoal("goal", "waiting for direction", now); err != nil {
			t.Fatal(err)
		}
		action := state.ActionableNext(RepositoryState{})
		if action.Kind != NextActionGoalCancelled || action.Goal.ID != "goal" || action.Reason != "waiting for direction" {
			t.Fatalf("action = %#v", action)
		}
	})

	t.Run("a PENDING dependent is not a wait", func(t *testing.T) {
		state, now, _ := summaryFixture(t)
		// WI-001 has not finished, so the dependency is genuinely unsatisfied: a
		// PENDING downstream item is not something a person or an agent can act on.
		if err := state.Start("WI-001", now); err != nil {
			t.Fatal(err)
		}
		blocked, err := state.AddWork("goal", "specs/stories/two", []string{"WI-001"}, now)
		if err != nil {
			t.Fatal(err)
		}
		if got := state.DisplayStatus(blocked.ID); got != "PENDING" {
			t.Fatalf("fixture status = %s", got)
		}
		if action := state.ActionableNext(RepositoryState{}); action.Kind != NextActionResume || action.Item.ID != "WI-001" {
			t.Fatalf("action = %#v", action)
		}
	})
}

func TestActionableNextPrefersIndependentReadyWorkOverHumanWait(t *testing.T) {
	state, now, revision := summaryFixture(t)
	passVerification(t, &state, now, Candidate{Kind: CommitCandidate, Revision: revision})
	if _, err := state.AddWork("goal", "specs/stories/two", nil, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	action := state.ActionableNext(RepositoryState{Revision: revision})
	if action.Kind != NextActionStart || action.Item.ID != "WI-002" {
		t.Fatalf("action = %#v", action)
	}
}

func TestActionableNextHasNoRecommendationForAnEmptyState(t *testing.T) {
	empty := NewState()
	if action := empty.ActionableNext(RepositoryState{}); action.Kind != NextActionNone {
		t.Fatalf("empty action = %#v", action)
	}
}

// When the last Work Item is DONE the Goal is complete and `next` says so
// instead of going quiet, but only the Goal that just finished is news.
func TestActionableNextReportsACompletedGoalOnlyWhenNothingElseIsActive(t *testing.T) {
	state := lifecycleState(t, false, "a")
	if action := state.ActionableNext(RepositoryState{}); action.Kind != NextActionStart {
		t.Fatalf("before completion: %#v", action)
	}
	runVerification(t, &state, "a", commitAt(revisionOne), 0)
	action := state.ActionableNext(RepositoryState{Revision: revisionOne})
	if action.Kind != NextActionGoalCompleted || action.Goal.ID != "g" {
		t.Fatalf("after the last item is DONE: %#v", action)
	}

	// A second Goal with work to do is what the agent should hear about; the
	// finished one is history, not an action.
	later := lifecycleNow.Add(time.Hour)
	state.Goals = append(state.Goals, Goal{ID: "h", Title: "Other", Repository: "/repo", Status: GoalActive, CreatedAt: later, UpdatedAt: later})
	state.WorkItems = append(state.WorkItems, Item{ID: "h1", GoalID: "h", StoryRef: "specs/stories/h1", Status: NotStarted, CreatedAt: later, UpdatedAt: later})
	action = state.ActionableNext(RepositoryState{Revision: revisionOne})
	if action.Kind != NextActionStart || action.Item.ID != "h1" {
		t.Fatalf("a completed Goal masked another Goal's work: %#v", action)
	}

	// While the other Goal is in flight but has nothing actionable, a finished
	// Goal is still not announced: not every Goal has ended.
	state.WorkItems[1].Status = Verifying
	state.WorkItems[1].CurrentRun = &Run{VerificationRunID: "VR-009", Revision: revisionOne, CandidateKind: CommitCandidate}
	if action = state.ActionableNext(RepositoryState{Revision: revisionOne}); action.Kind == NextActionGoalCompleted || action.Kind == NextActionNone {
		t.Fatalf("an ACTIVE Goal remains but the answer was %#v", action)
	}
}

func TestActionableNextNeverRecommendsReverifyingDoneOrCompletedWork(t *testing.T) {
	state := lifecycleState(t, false, "a", "b<a")
	runVerification(t, &state, "a", commitAt(revisionOne), 0)
	// HEAD moved on, and a is DONE: nothing about a is stale or owed.
	action := state.ActionableNext(RepositoryState{Revision: revisionTwo})
	if action.Kind != NextActionStart || action.Item.ID != "b" {
		t.Fatalf("action = %#v", action)
	}
	runVerification(t, &state, "b", commitAt(revisionTwo), 0)
	action = state.ActionableNext(RepositoryState{Revision: "3333333333333333333333333333333333333333"})
	if action.Kind != NextActionGoalCompleted {
		t.Fatalf("action = %#v, want the Goal reported completed without any re-verification", action)
	}
}
