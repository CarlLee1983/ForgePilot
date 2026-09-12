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

	if err := state.Start(second.ID, now); err != nil {
		t.Fatal(err)
	}
	action = state.ActionableNext(RepositoryState{})
	if action.Kind != NextActionResume || action.Item.ID != first.ID {
		t.Fatalf("multiple equally-created RUNNING action = %#v", action)
	}

	state.WorkItems[0].CreatedAt = now.Add(time.Minute)
	action = state.ActionableNext(RepositoryState{})
	if action.Kind != NextActionResume || action.Item.ID != second.ID {
		t.Fatalf("created-at ordered RUNNING action = %#v", action)
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
		if fresh.Kind != NextActionWaitHumanReview {
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
	state := State{SchemaVersion: SchemaVersion, NextWorkID: 4, NextEvidenceID: 1, NextGateID: 1,
		Goals: []Goal{{ID: "g", Title: "Goal", Repository: "/repo", Status: GoalActive}},
		WorkItems: []Item{{ID: "WI-003", GoalID: "g", StoryRef: "specs/stories/three", Status: Ready, CreatedAt: same},
			{ID: "WI-002", GoalID: "g", StoryRef: "specs/stories/two", Status: Ready, CreatedAt: same},
			{ID: "WI-001", GoalID: "g", StoryRef: "specs/stories/one", Status: Ready, CreatedAt: old}}}
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
		if action.Kind != NextActionWaitHumanReview || action.Reason != "human review required" {
			t.Fatalf("action = %#v", action)
		}
	})

	t.Run("open gate", func(t *testing.T) {
		state, now, _ := summaryFixture(t)
		gate, err := state.OpenGate("WI-001", "Decide?", []string{"yes", "no"}, "", now)
		if err != nil {
			t.Fatal(err)
		}
		action := state.ActionableNext(RepositoryState{})
		if action.Kind != NextActionWaitGate || action.Reason != "unresolved Gate "+gate.ID {
			t.Fatalf("action = %#v", action)
		}
	})

	t.Run("blocked goal", func(t *testing.T) {
		state, now, _ := summaryFixture(t)
		if err := state.BlockGoal("goal", "waiting for direction", now); err != nil {
			t.Fatal(err)
		}
		action := state.ActionableNext(RepositoryState{})
		if action.Kind != NextActionWaitGoal || action.Reason != "goal goal is BLOCKED" {
			t.Fatalf("action = %#v", action)
		}
	})

	t.Run("dependency pending is not a waiting recommendation", func(t *testing.T) {
		state, now, _ := summaryFixture(t)
		state.WorkItems[0].Status = Done
		blocked, err := state.AddWork("goal", "specs/stories/two", []string{"WI-001"}, now)
		if err != nil {
			t.Fatal(err)
		}
		state.WorkItems[1].Status = Pending
		if blocked.Status != Ready {
			t.Fatalf("fixture status = %s", blocked.Status)
		}
		action := state.ActionableNext(RepositoryState{})
		if action.Kind != NextActionNone {
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

func TestActionableNextHasNoRecommendationForEmptyOrDoneState(t *testing.T) {
	empty := NewState()
	if action := empty.ActionableNext(RepositoryState{}); action.Kind != NextActionNone {
		t.Fatalf("empty action = %#v", action)
	}
	state, now, revision := summaryFixture(t)
	passVerification(t, &state, now, Candidate{Kind: CommitCandidate, Revision: revision})
	if _, err := state.RecordReview("WI-001", revision, Approved, "human@example.com", "", "", now); err != nil {
		t.Fatal(err)
	}
	if action := state.ActionableNext(RepositoryState{Revision: revision}); action.Kind != NextActionNone {
		t.Fatalf("done action = %#v", action)
	}
}
