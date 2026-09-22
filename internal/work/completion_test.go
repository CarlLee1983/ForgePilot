package work

import (
	"strings"
	"testing"
	"time"
)

func TestVerifiedGoalCompletionRequiresExactFreshEvidenceAndPersistsAggregate(t *testing.T) {
	now := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	revision := "1111111111111111111111111111111111111111"
	state := NewState()
	if err := state.AddGoalWithPolicies("goal", "Goal", "", "/repo", ReviewPerGoal, CompletionVerified, now); err != nil {
		t.Fatal(err)
	}
	item, err := state.AddWork("goal", "specs/stories/one", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Start(item.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginVerification(item.ID, revision, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	verification, err := state.RecordVerificationWithRepository(item.ID, revision, "make verify", 0, RepositoryState{Revision: revision}, now)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := state.GoalSummary("goal", RepositoryState{Revision: revision})
	if err != nil || summary.Completion != GoalReadyToComplete {
		t.Fatalf("summary = %#v, err = %v", summary, err)
	}
	completion, err := state.CompleteVerifiedGoal("goal", RepositoryState{Revision: revision}, summary.VerificationEvidenceIDs, now)
	if err != nil {
		t.Fatal(err)
	}
	if completion.ID != "GC-001" || completion.VerificationEvidenceIDs[0] != verification.ID {
		t.Fatalf("completion = %#v", completion)
	}
	if state.Goals[0].Status != GoalCompleted || state.WorkItems[0].Status != Verified {
		t.Fatalf("statuses = %s / %s", state.Goals[0].Status, state.WorkItems[0].Status)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("completed state invalid: %v", err)
	}
	workSummary, err := state.WorkSummary(item.ID, RepositoryState{Revision: revision})
	if err != nil || workSummary.Completion != CompletionGoalCompleted {
		t.Fatalf("completed Work Item summary = %#v, err = %v", workSummary, err)
	}
	if _, err := state.OpenGate(item.ID, "Late question?", []string{"yes", "no"}, "", now); err == nil {
		t.Fatal("opened a Gate on a completed Goal")
	}

	// A second transition cannot append a second aggregate record or reopen the
	// terminal Goal.
	if _, err := state.CompleteVerifiedGoal("goal", RepositoryState{Revision: revision}, summary.VerificationEvidenceIDs, now); err == nil {
		t.Fatal("completed the same Goal twice")
	}
}

func TestVerifiedGoalCompletionFailsClosedOnCandidateOrEvidenceDrift(t *testing.T) {
	now := time.Date(2026, 9, 19, 1, 0, 0, 0, time.UTC)
	revision := "1111111111111111111111111111111111111111"
	state := NewState()
	if err := state.AddGoalWithPolicies("goal", "Goal", "", "/repo", ReviewPerGoal, CompletionVerified, now); err != nil {
		t.Fatal(err)
	}
	item, _ := state.AddWork("goal", "specs/stories/one", nil, now)
	if err := state.Start(item.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginVerification(item.ID, revision, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	verification, err := state.RecordVerificationWithRepository(item.ID, revision, "make verify", 0, RepositoryState{Revision: revision}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.CompleteVerifiedGoal("goal", RepositoryState{Revision: revision}, []string{"EV-999"}, now); err == nil || !strings.Contains(err.Error(), "evidence changed") {
		t.Fatalf("wrong expected IDs error = %v", err)
	}
	if state.Goals[0].Status != GoalActive || len(state.GoalCompletionEvidence) != 0 {
		t.Fatal("failed completion mutated state")
	}
	if _, err := state.CompleteVerifiedGoal("goal", RepositoryState{Revision: revision}, nil, now); err == nil || !strings.Contains(err.Error(), "evidence changed") {
		t.Fatalf("nil expected IDs error = %v, want exact-set refusal", err)
	}
	if _, err := state.CompleteVerifiedGoal("goal", RepositoryState{Revision: "2222222222222222222222222222222222222222"}, []string{verification.ID}, now); err == nil {
		t.Fatal("completed a stale Candidate")
	}
	if state.Goals[0].Status != GoalActive {
		t.Fatal("stale completion mutated Goal status")
	}
}

func TestVerifiedGoalCompletionAcceptsMixedCandidateKinds(t *testing.T) {
	now := time.Date(2026, 9, 19, 2, 0, 0, 0, time.UTC)
	commitRevision := "1111111111111111111111111111111111111111"
	baseRevision := commitRevision
	snapshotRevision := "2222222222222222222222222222222222222222"
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	state := NewState()
	if err := state.AddGoalWithPolicies("goal", "Goal", "", "/repo", ReviewPerGoal, CompletionVerified, now); err != nil {
		t.Fatal(err)
	}
	commitItem, err := state.AddWork("goal", "specs/stories/commit", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	snapshotItem, err := state.AddWork("goal", "specs/stories/snapshot", nil, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Start(commitItem.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginCandidateVerification(commitItem.ID, Candidate{Kind: CommitCandidate, Revision: commitRevision}, "/tmp/commit", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerificationWithRepository(commitItem.ID, commitRevision, "make verify", 0, RepositoryState{Revision: commitRevision, SnapshotDigest: digest}, now); err != nil {
		t.Fatal(err)
	}
	if err := state.Start(snapshotItem.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginCandidateVerification(snapshotItem.ID, Candidate{Kind: SnapshotCandidate, Revision: snapshotRevision, BaseRevision: baseRevision, Digest: digest}, "/tmp/snapshot", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerificationWithRepository(snapshotItem.ID, snapshotRevision, "make verify", 0, RepositoryState{Revision: baseRevision, SnapshotDigest: digest}, now); err != nil {
		t.Fatal(err)
	}

	repository := RepositoryState{Revision: commitRevision, SnapshotDigest: digest}
	summary, err := state.GoalSummary("goal", repository)
	if err != nil || summary.Completion != GoalReadyToComplete {
		t.Fatalf("mixed-candidate summary = %#v, err = %v", summary, err)
	}
	completion, err := state.CompleteVerifiedGoal("goal", repository, summary.VerificationEvidenceIDs, now)
	if err != nil {
		t.Fatal(err)
	}
	if completion.ID != "GC-001" || len(completion.VerificationEvidenceIDs) != 2 {
		t.Fatalf("mixed-candidate completion = %#v", completion)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("mixed-candidate completed state invalid: %v", err)
	}
}

func TestLegacyGoalCompletionPreservesTerminalStatusWithoutClaimingVerification(t *testing.T) {
	now := time.Date(2026, 9, 19, 4, 0, 0, 0, time.UTC)
	revision := "1111111111111111111111111111111111111111"
	state := NewState()
	if err := state.AddGoalWithPolicies("goal", "Goal", "", "/repo", ReviewPerGoal, CompletionVerified, now); err != nil {
		t.Fatal(err)
	}
	item, err := state.AddWork("goal", "specs/stories/one", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Start(item.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginVerification(item.ID, revision, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerificationWithRepository(item.ID, revision, "make verify", 0, RepositoryState{Revision: revision}, now); err != nil {
		t.Fatal(err)
	}
	state.Goals[0].Status = GoalCompleted
	state.Goals[0].LegacyCompletion = &LegacyGoalCompletion{SourceSchemaVersion: 11, CompletionPolicy: CompletionHuman}

	if err := state.Validate(); err != nil {
		t.Fatalf("legacy-completed Goal is invalid: %v", err)
	}
	summary, err := state.GoalSummary("goal", RepositoryState{Revision: revision})
	if err != nil || summary.Completion != GoalDoneCompletion || len(summary.VerificationEvidenceIDs) != 0 {
		t.Fatalf("legacy-completed summary = %#v, err = %v; want done with no inferred Verification IDs", summary, err)
	}
	if len(state.GoalCompletionEvidence) != 0 {
		t.Fatal("legacy provenance created automatic Goal completion evidence")
	}

	invalid := state
	invalid.Goals = append([]Goal(nil), state.Goals...)
	invalid.Goals[0].LegacyCompletion = &LegacyGoalCompletion{SourceSchemaVersion: 12, CompletionPolicy: CompletionHuman}
	if err := invalid.Validate(); err == nil {
		t.Fatal("accepted legacy provenance from a source schema other than v11")
	}

	invalid = state
	invalid.Goals = append([]Goal(nil), state.Goals...)
	invalid.Goals[0].LegacyCompletion = &LegacyGoalCompletion{SourceSchemaVersion: 11, CompletionPolicy: CompletionVerified}
	if err := invalid.Validate(); err == nil {
		t.Fatal("accepted legacy provenance with a non-HUMAN source policy")
	}

	invalid = state
	invalid.Goals = append([]Goal(nil), state.Goals...)
	invalid.Goals[0].LegacyCompletion = &LegacyGoalCompletion{SourceSchemaVersion: 11, CompletionPolicy: CompletionHuman}
	invalid.Goals[0].Status = GoalActive
	if err := invalid.Validate(); err == nil {
		t.Fatal("accepted legacy provenance on an active Goal")
	}

	invalid = state
	invalid.Goals = append([]Goal(nil), state.Goals...)
	invalid.Goals[0].LegacyCompletion = &LegacyGoalCompletion{SourceSchemaVersion: 11, CompletionPolicy: CompletionHuman}
	invalid.GoalCompletionEvidence = []GoalCompletionEvidence{{ID: "GC-001", GoalID: "goal"}}
	if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), "both legacy and automatic") {
		t.Fatalf("legacy provenance plus automatic evidence error = %v", err)
	}
}

func TestGoalCompletionRejectsSnapshotDigestWithContradictoryRevision(t *testing.T) {
	now := time.Date(2026, 9, 19, 3, 0, 0, 0, time.UTC)
	baseRevision := "1111111111111111111111111111111111111111"
	otherRevision := "2222222222222222222222222222222222222222"
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	state := NewState()
	if err := state.AddGoalWithPolicies("goal", "Goal", "", "/repo", ReviewPerGoal, CompletionVerified, now); err != nil {
		t.Fatal(err)
	}
	item, err := state.AddWork("goal", "specs/stories/snapshot", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Start(item.ID, now); err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{Kind: SnapshotCandidate, Revision: otherRevision, BaseRevision: baseRevision, Digest: digest}
	if err := state.BeginCandidateVerification(item.ID, candidate, "/tmp/snapshot", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerificationWithRepository(item.ID, candidate.Revision, "make verify", 0, RepositoryState{Revision: baseRevision, SnapshotDigest: digest}, now); err != nil {
		t.Fatal(err)
	}
	summary, err := state.GoalSummary("goal", RepositoryState{Revision: otherRevision, SnapshotDigest: digest})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Completion != GoalInProgress || len(summary.VerificationEvidenceIDs) != 0 {
		t.Fatalf("contradictory snapshot facts produced a completion projection: %#v", summary)
	}
}
