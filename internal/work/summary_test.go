package work

import (
	"testing"
	"time"
)

func TestWorkSummaryProjectsCurrentActionableState(t *testing.T) {
	t.Run("ready work has not started", func(t *testing.T) {
		state, _, revision := summaryFixture(t)
		summary := mustWorkSummary(t, &state, RepositoryState{Revision: revision})
		if summary.Completion != CompletionNotStarted || summary.HasVerification || summary.HasReview {
			t.Fatalf("summary = %#v", summary)
		}
	})

	t.Run("running work is implementing", func(t *testing.T) {
		state, now, revision := summaryFixture(t)
		if err := state.Start("WI-001", now); err != nil {
			t.Fatal(err)
		}
		if got := mustWorkSummary(t, &state, RepositoryState{Revision: revision}).Completion; got != CompletionImplementing {
			t.Fatalf("completion = %q", got)
		}
	})

	t.Run("pass awaits a human review", func(t *testing.T) {
		state, now, revision := summaryFixture(t)
		passVerification(t, &state, now, Candidate{Kind: CommitCandidate, Revision: revision})
		summary := mustWorkSummary(t, &state, RepositoryState{Revision: revision})
		if !summary.HasVerification || summary.Verification.Result != Pass || summary.HasReview || summary.Completion != CompletionAwaitingReview {
			t.Fatalf("summary = %#v", summary)
		}
	})

	t.Run("failed verification takes precedence over running", func(t *testing.T) {
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
		summary := mustWorkSummary(t, &state, RepositoryState{Revision: revision})
		if summary.Verification.Result != Fail || summary.Completion != CompletionVerificationFailed {
			t.Fatalf("summary = %#v", summary)
		}
	})

	t.Run("stale pass asks for verification", func(t *testing.T) {
		state, now, revision := summaryFixture(t)
		passVerification(t, &state, now, Candidate{Kind: CommitCandidate, Revision: revision})
		summary := mustWorkSummary(t, &state, RepositoryState{Revision: "2222222222222222222222222222222222222222"})
		if !summary.VerificationStale || summary.Completion != CompletionVerificationStale {
			t.Fatalf("summary = %#v", summary)
		}
	})

	t.Run("rejected review requests changes", func(t *testing.T) {
		state, now, revision := summaryFixture(t)
		passVerification(t, &state, now, Candidate{Kind: CommitCandidate, Revision: revision})
		if _, err := state.RecordReview("WI-001", revision, Rejected, "human@example.com", "fix it", "", now); err != nil {
			t.Fatal(err)
		}
		summary := mustWorkSummary(t, &state, RepositoryState{Revision: revision})
		if !summary.HasReview || summary.Review.Result != Rejected || summary.Completion != CompletionChangesRequested {
			t.Fatalf("summary = %#v", summary)
		}
	})

	t.Run("a new pass supersedes an earlier rejected review", func(t *testing.T) {
		state, now, revision := summaryFixture(t)
		passVerification(t, &state, now, Candidate{Kind: CommitCandidate, Revision: revision})
		if _, err := state.RecordReview("WI-001", revision, Rejected, "human@example.com", "fix it", "", now); err != nil {
			t.Fatal(err)
		}
		if err := state.BeginCandidateVerification("WI-001", Candidate{Kind: CommitCandidate, Revision: revision}, "/tmp/worktree", "", now); err != nil {
			t.Fatal(err)
		}
		if _, err := state.RecordVerification("WI-001", revision, "make verify", 0, now); err != nil {
			t.Fatal(err)
		}
		summary := mustWorkSummary(t, &state, RepositoryState{Revision: revision})
		if summary.Review.Result != Rejected || !summary.HasVerification || summary.Completion != CompletionAwaitingReview {
			t.Fatalf("summary = %#v", summary)
		}
	})

	t.Run("verifying takes precedence over historical evidence", func(t *testing.T) {
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
		if err := state.BeginCandidateVerification("WI-001", Candidate{Kind: CommitCandidate, Revision: revision}, "/tmp/worktree", "", now); err != nil {
			t.Fatal(err)
		}
		if got := mustWorkSummary(t, &state, RepositoryState{Revision: revision}).Completion; got != CompletionVerificationNeeded {
			t.Fatalf("completion = %q", got)
		}
	})

	t.Run("only open gates are blockers", func(t *testing.T) {
		state, now, revision := summaryFixture(t)
		open, err := state.OpenGate("WI-001", "open?", []string{"yes", "no"}, "", now)
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := state.OpenGate("WI-001", "resolved?", []string{"yes", "no"}, "", now)
		if err != nil {
			t.Fatal(err)
		}
		if err := state.ResolveGate(resolved.ID, "yes", "", "human@example.com", now); err != nil {
			t.Fatal(err)
		}
		cancelled, err := state.OpenGate("WI-001", "cancelled?", []string{"yes", "no"}, "", now)
		if err != nil {
			t.Fatal(err)
		}
		if err := state.CancelGate(cancelled.ID, "not needed", "human@example.com", now); err != nil {
			t.Fatal(err)
		}
		summary := mustWorkSummary(t, &state, RepositoryState{Revision: revision})
		if len(summary.BlockingGates) != 1 || summary.BlockingGates[0].ID != open.ID || summary.Completion != CompletionBlockedByGate {
			t.Fatalf("summary = %#v", summary)
		}
	})

	t.Run("a released gate requires approval to be recorded again", func(t *testing.T) {
		state, now, revision := summaryFixture(t)
		passVerification(t, &state, now, Candidate{Kind: CommitCandidate, Revision: revision})
		gate, err := state.OpenGate("WI-001", "open?", []string{"yes", "no"}, "", now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := state.RecordReview("WI-001", revision, Approved, "human@example.com", "", "", now); err != nil {
			t.Fatal(err)
		}
		if err := state.ResolveGate(gate.ID, "yes", "", "human@example.com", now); err != nil {
			t.Fatal(err)
		}
		summary := mustWorkSummary(t, &state, RepositoryState{Revision: revision})
		if summary.Completion != CompletionAwaitingReview || !summary.ApprovalNeedsRerecord {
			t.Fatalf("summary = %#v", summary)
		}
	})

	t.Run("a later matching pass requires approval to be recorded again", func(t *testing.T) {
		state, now, revision := summaryFixture(t)
		passVerification(t, &state, now, Candidate{Kind: CommitCandidate, Revision: revision})
		newRevision := "2222222222222222222222222222222222222222"
		if _, err := state.RecordReview("WI-001", newRevision, Approved, "human@example.com", "", "", now); err != nil {
			t.Fatal(err)
		}
		if err := state.BeginCandidateVerification("WI-001", Candidate{Kind: CommitCandidate, Revision: newRevision}, "/tmp/worktree", "", now); err != nil {
			t.Fatal(err)
		}
		if _, err := state.RecordVerification("WI-001", newRevision, "make verify", 0, now); err != nil {
			t.Fatal(err)
		}
		summary := mustWorkSummary(t, &state, RepositoryState{Revision: newRevision})
		if summary.Completion != CompletionAwaitingReview || !summary.ApprovalNeedsRerecord {
			t.Fatalf("summary = %#v", summary)
		}
	})

	t.Run("blocked goal takes precedence", func(t *testing.T) {
		state, now, revision := summaryFixture(t)
		if err := state.BlockGoal("goal", "awaiting decision", now); err != nil {
			t.Fatal(err)
		}
		summary := mustWorkSummary(t, &state, RepositoryState{Revision: revision})
		if summary.Goal.Status != GoalBlocked || summary.Completion != CompletionGoalBlocked {
			t.Fatalf("summary = %#v", summary)
		}
	})

	t.Run("done remains done", func(t *testing.T) {
		state, now, revision := summaryFixture(t)
		passVerification(t, &state, now, Candidate{Kind: CommitCandidate, Revision: revision})
		if _, err := state.RecordReview("WI-001", revision, Approved, "human@example.com", "", "", now); err != nil {
			t.Fatal(err)
		}
		summary := mustWorkSummary(t, &state, RepositoryState{Revision: "2222222222222222222222222222222222222222"})
		if summary.Item.Status != Done || summary.VerificationStale || summary.Completion != CompletionDone {
			t.Fatalf("summary = %#v", summary)
		}
	})

	t.Run("snapshot reuses digest staleness", func(t *testing.T) {
		state, now, revision := summaryFixture(t)
		candidate := Candidate{Kind: SnapshotCandidate, Revision: "3333333333333333333333333333333333333333", BaseRevision: revision,
			Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
		passVerification(t, &state, now, candidate)
		fresh := mustWorkSummary(t, &state, RepositoryState{SnapshotDigest: candidate.Digest})
		stale := mustWorkSummary(t, &state, RepositoryState{
			SnapshotDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"})
		if fresh.VerificationStale || fresh.Completion != CompletionAwaitingReview || !stale.VerificationStale || stale.Completion != CompletionVerificationStale {
			t.Fatalf("fresh = %#v\nstale = %#v", fresh, stale)
		}
	})
}

func summaryFixture(t *testing.T) (State, time.Time, string) {
	t.Helper()
	now := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	revision := "1111111111111111111111111111111111111111"
	state := NewState()
	if err := state.AddGoal("goal", "Goal", "", "/repo", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.AddWork("goal", "specs/stories/one", nil, now); err != nil {
		t.Fatal(err)
	}
	return state, now, revision
}

func passVerification(t *testing.T, state *State, now time.Time, candidate Candidate) {
	t.Helper()
	if err := state.Start("WI-001", now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginCandidateVerification("WI-001", candidate, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerification("WI-001", candidate.Revision, "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
}

func mustWorkSummary(t *testing.T, state *State, repository RepositoryState) WorkItemSummary {
	t.Helper()
	summary, err := state.WorkSummary("WI-001", repository)
	if err != nil {
		t.Fatal(err)
	}
	return summary
}
