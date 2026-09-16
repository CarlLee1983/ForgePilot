package work

import (
	"testing"
	"time"
)

func TestFanoutPassSharesCanonicalRunButWritesDistinctEvidence(t *testing.T) {
	now := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	s := NewState()
	if err := s.AddGoal("g", "goal", "", "/repo", now); err != nil {
		t.Fatal(err)
	}
	anchor, err := s.AddWork("g", "specs/stories/anchor", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := s.AddWork("g", "specs/stories/peer", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(peer.ID, now); err != nil {
		t.Fatal(err)
	}
	old := Candidate{Kind: CommitCandidate, Revision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if err := s.BeginCandidateVerification(peer.ID, old, "/tmp/peer", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordVerification(peer.ID, old.Revision, "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
	if err := s.RequestReviewWithRepository(peer.ID, "EV-001", RepositoryState{Revision: old.Revision}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(anchor.ID, now); err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{Kind: CommitCandidate, Revision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	plan, err := s.BeginVerificationFanout(anchor.ID, candidate, "/tmp/anchor", "", nil, s.NextVerificationRun(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Optional) != 1 || plan.Optional[0].WorkItemID != peer.ID {
		t.Fatalf("plan = %#v", plan)
	}
	got, skipped, err := s.RecordVerificationFanoutPass(plan, "make verify", &RepositoryState{Revision: candidate.Revision}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 || len(got) != 2 {
		t.Fatalf("evidence=%#v skipped=%#v", got, skipped)
	}
	if got[0].ID == got[1].ID || got[0].VerificationRunID != "VR-002" || got[1].VerificationRunID != got[0].VerificationRunID {
		t.Fatalf("shared run evidence = %#v", got)
	}
	if s.WorkItemStatus(anchor.ID) != Running || s.WorkItemStatus(peer.ID) != Running {
		t.Fatalf("statuses = %s, %s", s.WorkItemStatus(anchor.ID), s.WorkItemStatus(peer.ID))
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestFanoutCompletionSkipsChangedAncestorAndTransitiveDependent(t *testing.T) {
	now := time.Date(2026, 9, 16, 1, 0, 0, 0, time.UTC)
	s, anchor, ancestor, dependent, old := goalPolicyFanoutChain(t, now)
	candidate := Candidate{Kind: CommitCandidate, Revision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	plan, err := s.BeginVerificationFanout(anchor.ID, candidate, "/tmp/anchor", "", nil, s.NextVerificationRun(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Optional) != 2 {
		t.Fatalf("optional cohort = %#v, want ancestor and dependent", plan.Optional)
	}
	if _, err := s.OpenGate(ancestor.ID, "Which contract?", []string{"A", "B"}, "needs a human", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	evidence, skipped, err := s.RecordVerificationFanoutPass(plan, "make verify", &RepositoryState{Revision: candidate.Revision}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 1 || evidence[0].WorkItemID != anchor.ID {
		t.Fatalf("evidence = %#v, want anchor only", evidence)
	}
	if len(skipped) != 2 || skipped[0].WorkItemID != ancestor.ID || skipped[1].WorkItemID != dependent.ID {
		t.Fatalf("skipped = %#v, want changed ancestor then transitive dependent", skipped)
	}
	for _, id := range []string{ancestor.ID, dependent.ID} {
		latest, ok := s.LatestVerification(id)
		if !ok || latest.Candidate() != old {
			t.Fatalf("%s latest verification changed: %#v", id, latest)
		}
	}
}

func TestFanoutPlanReportsInitiallyBlockedStalePeer(t *testing.T) {
	now := time.Date(2026, 9, 16, 2, 0, 0, 0, time.UTC)
	s, anchor, ancestor, _, _ := goalPolicyFanoutChain(t, now)
	if _, err := s.OpenGate(ancestor.ID, "Which contract?", []string{"A", "B"}, "needs a human", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{Kind: CommitCandidate, Revision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	plan, err := s.BeginVerificationFanout(anchor.ID, candidate, "/tmp/anchor", "", nil, s.NextVerificationRun(), now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Optional) != 0 {
		t.Fatalf("optional cohort = %#v, want none", plan.Optional)
	}
	if len(plan.Skipped) != 2 || plan.Skipped[0].WorkItemID != ancestor.ID {
		t.Fatalf("skipped = %#v, want blocked ancestor and its dependent", plan.Skipped)
	}
}

func TestFanoutIncludesRecipientWhoseSimultaneousPrerequisiteIsTheAnchor(t *testing.T) {
	now := time.Date(2026, 9, 16, 3, 0, 0, 0, time.UTC)
	s, anchor, _, dependent, _ := goalPolicyFanoutChain(t, now)
	// The fixture's independent anchor is not the dependency this case needs.
	// Verify that anchor separately, then use the chain ancestor as the run anchor.
	chainAnchor := s.item(dependent.DependsOn[0])
	if chainAnchor == nil {
		t.Fatal("missing chain anchor")
	}
	_ = anchor
	candidate := Candidate{Kind: CommitCandidate, Revision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	plan, err := s.BeginVerificationFanout(chainAnchor.ID, candidate, "/tmp/anchor", "", nil, s.NextVerificationRun(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Optional) != 1 || plan.Optional[0].WorkItemID != dependent.ID {
		t.Fatalf("optional cohort = %#v, want dependent of anchor", plan.Optional)
	}
	evidence, skipped, err := s.RecordVerificationFanoutPass(plan, "make verify", &RepositoryState{Revision: candidate.Revision}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 || len(evidence) != 2 || evidence[1].WorkItemID != dependent.ID {
		t.Fatalf("evidence=%#v skipped=%#v, want anchor plus dependent", evidence, skipped)
	}
}

func TestFanoutSkipsDependentWhenPrerequisiteChangesButRemainsEligible(t *testing.T) {
	now := time.Date(2026, 9, 16, 4, 0, 0, 0, time.UTC)
	s, anchor, ancestor, dependent, _ := goalPolicyFanoutChain(t, now)
	candidate := Candidate{Kind: CommitCandidate, Revision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	plan, err := s.BeginVerificationFanout(anchor.ID, candidate, "/tmp/anchor", "", nil, s.NextVerificationRun(), now)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := s.OpenGate(ancestor.ID, "Which contract?", []string{"A", "B"}, "needs a human", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveGateWithRepository(gate.ID, "A", "resolved", "reviewer", RepositoryState{Revision: candidate.Revision}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	evidence, skipped, err := s.RecordVerificationFanoutPass(plan, "make verify", &RepositoryState{Revision: candidate.Revision}, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 1 || len(skipped) != 2 || skipped[0].WorkItemID != ancestor.ID || skipped[1].WorkItemID != dependent.ID {
		t.Fatalf("evidence=%#v skipped=%#v, want anchor only and both changed members skipped", evidence, skipped)
	}
}

func TestFanoutTreatsSameDigestSnapshotAsFreshDespiteNewSyntheticRevision(t *testing.T) {
	now := time.Date(2026, 9, 16, 5, 0, 0, 0, time.UTC)
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	old := Candidate{Kind: SnapshotCandidate, Revision: "1111111111111111111111111111111111111111", BaseRevision: "0000000000000000000000000000000000000000", Digest: digest}
	current := Candidate{Kind: SnapshotCandidate, Revision: "2222222222222222222222222222222222222222", BaseRevision: old.BaseRevision, Digest: digest}
	s := NewState()
	if err := s.AddGoalWithReviewPolicy("g", "goal", "", "/repo", ReviewPerGoal, now); err != nil {
		t.Fatal(err)
	}
	peer, err := s.AddWork("g", "specs/stories/peer", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	anchor, err := s.AddWork("g", "specs/stories/anchor", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(peer.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginCandidateVerification(peer.ID, old, "/tmp/peer", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordVerificationWithRepository(peer.ID, old.Revision, "make verify", 0, RepositoryState{Revision: old.BaseRevision, SnapshotDigest: digest}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(anchor.ID, now); err != nil {
		t.Fatal(err)
	}
	plan, err := s.BeginVerificationFanout(anchor.ID, current, "/tmp/anchor", "", nil, s.NextVerificationRun(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Optional) != 0 || len(plan.Skipped) != 0 {
		t.Fatalf("same-digest peer was treated as stale: %#v", plan)
	}
}

func TestFanoutTreatsSameDigestSnapshotPrerequisiteAsFresh(t *testing.T) {
	now := time.Date(2026, 9, 16, 5, 30, 0, 0, time.UTC)
	base := "0000000000000000000000000000000000000000"
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	oldPrerequisite := Candidate{Kind: SnapshotCandidate, Revision: "1111111111111111111111111111111111111111", BaseRevision: base, Digest: digest}
	current := Candidate{Kind: SnapshotCandidate, Revision: "2222222222222222222222222222222222222222", BaseRevision: base, Digest: digest}
	stale := Candidate{Kind: SnapshotCandidate, Revision: "3333333333333333333333333333333333333333", BaseRevision: base, Digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}

	s := NewState()
	if err := s.AddGoalWithReviewPolicy("g", "goal", "", "/repo", ReviewPerGoal, now); err != nil {
		t.Fatal(err)
	}
	prerequisite, err := s.AddWork("g", "specs/stories/prerequisite", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := s.AddWork("g", "specs/stories/recipient", []string{prerequisite.ID}, now.Add(time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	anchor, err := s.AddWork("g", "specs/stories/anchor", nil, now.Add(2*time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(prerequisite.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginCandidateVerification(prerequisite.ID, oldPrerequisite, "/tmp/prerequisite", "", now); err != nil {
		t.Fatal(err)
	}
	prerequisiteEvidence, err := s.RecordVerificationWithRepository(prerequisite.ID, oldPrerequisite.Revision, "make verify", 0, RepositoryState{Revision: base, SnapshotDigest: digest}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StartWithRepository(recipient.ID, RepositoryState{Revision: base, SnapshotDigest: digest}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginCandidateVerification(recipient.ID, stale, "/tmp/recipient", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordVerificationWithRepository(recipient.ID, stale.Revision, "make verify", 0, RepositoryState{Revision: base, SnapshotDigest: stale.Digest}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(anchor.ID, now); err != nil {
		t.Fatal(err)
	}

	plan, err := s.BeginVerificationFanout(anchor.ID, current, "/tmp/anchor", "", nil, s.NextVerificationRun(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Optional) != 1 || plan.Optional[0].WorkItemID != recipient.ID {
		t.Fatalf("optional cohort = %#v, want stale recipient with fresh prerequisite", plan.Optional)
	}
	evidence, skipped, err := s.RecordVerificationFanoutPass(plan, "make verify", &RepositoryState{Revision: base, SnapshotDigest: digest}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 || len(evidence) != 2 || evidence[0].WorkItemID != anchor.ID || evidence[1].WorkItemID != recipient.ID {
		t.Fatalf("evidence=%#v skipped=%#v, want anchor and recipient only", evidence, skipped)
	}
	latestPrerequisite, ok := s.LatestVerification(prerequisite.ID)
	if !ok || latestPrerequisite.ID != prerequisiteEvidence.ID {
		t.Fatalf("prerequisite received duplicate Evidence: before=%s after=%#v", prerequisiteEvidence.ID, latestPrerequisite)
	}
}

func TestFanoutPassDemotesReadyDependentsWhenRepositoryFactsAreUnavailable(t *testing.T) {
	now := time.Date(2026, 9, 16, 6, 0, 0, 0, time.UTC)
	s, anchor, _, dependent, old := goalPolicyFanoutChain(t, now)
	downstream, err := s.AddWorkWithRepository("g", "specs/stories/downstream", []string{dependent.ID}, RepositoryState{Revision: old.Revision}, now)
	if err != nil {
		t.Fatal(err)
	}
	if downstream.Status != Ready {
		t.Fatalf("downstream starts %s, want READY", downstream.Status)
	}
	candidate := Candidate{Kind: CommitCandidate, Revision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	plan, err := s.BeginVerificationFanout(anchor.ID, candidate, "/tmp/anchor", "", nil, s.NextVerificationRun(), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RecordVerificationFanoutPass(plan, "make verify", nil, now); err != nil {
		t.Fatal(err)
	}
	if got := s.WorkItemStatus(downstream.ID); got != Pending {
		t.Fatalf("downstream status = %s, want PENDING without live repository facts", got)
	}
}

func goalPolicyFanoutChain(t *testing.T, now time.Time) (*State, Item, Item, Item, Candidate) {
	t.Helper()
	s := NewState()
	if err := s.AddGoalWithReviewPolicy("g", "goal", "", "/repo", ReviewPerGoal, now); err != nil {
		t.Fatal(err)
	}
	ancestor, err := s.AddWork("g", "specs/stories/ancestor", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	dependent, err := s.AddWork("g", "specs/stories/dependent", []string{ancestor.ID}, now.Add(time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	anchor, err := s.AddWork("g", "specs/stories/anchor", nil, now.Add(2*time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	old := Candidate{Kind: CommitCandidate, Revision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if err := s.Start(ancestor.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginCandidateVerification(ancestor.ID, old, "/tmp/ancestor", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordVerificationWithRepository(ancestor.ID, old.Revision, "make verify", 0, RepositoryState{Revision: old.Revision}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.StartWithRepository(dependent.ID, RepositoryState{Revision: old.Revision}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginCandidateVerification(dependent.ID, old, "/tmp/dependent", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordVerificationWithRepository(dependent.ID, old.Revision, "make verify", 0, RepositoryState{Revision: old.Revision}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(anchor.ID, now); err != nil {
		t.Fatal(err)
	}
	return &s, anchor, ancestor, dependent, old
}
