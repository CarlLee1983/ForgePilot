package work

import (
	"strings"
	"testing"
)

const (
	snapshotRevision = "2222222222222222222222222222222222222222"
	baseRevision     = "1111111111111111111111111111111111111111"
	snapshotDigest   = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func TestSnapshotCandidateFlowsFromVerificationThroughReview(t *testing.T) {
	state, now := verifiableState(t)
	candidate := Candidate{
		Kind:         SnapshotCandidate,
		Revision:     snapshotRevision,
		BaseRevision: baseRevision,
		Digest:       snapshotDigest,
	}
	if err := state.BeginCandidateVerification("WI-001", candidate, "/tmp/wt", "/tmp/log", now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItems[0].CurrentRun.Candidate(); got != candidate {
		t.Fatalf("current run candidate = %#v, want %#v", got, candidate)
	}
	evidence, err := state.RecordVerification("WI-001", snapshotRevision, "make verify", 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := evidence.Candidate(); got != candidate {
		t.Fatalf("verification candidate = %#v, want %#v", got, candidate)
	}

	reviewCandidate, err := state.ResolveReviewCandidate("WI-001", evidence.ID, baseRevision, snapshotDigest)
	if err != nil {
		t.Fatal(err)
	}
	if reviewCandidate != candidate {
		t.Fatalf("review candidate = %#v, want verified %#v", reviewCandidate, candidate)
	}
	review, err := state.RecordCandidateReview("WI-001", reviewCandidate, Approved, "reviewer@example.com", "", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if got := review.Candidate(); got != candidate {
		t.Fatalf("review evidence candidate = %#v, want %#v", got, candidate)
	}
	if state.WorkItemStatus("WI-001") != Done {
		t.Fatalf("same snapshot PASS and approval left work %s", state.WorkItemStatus("WI-001"))
	}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotReviewRefusesChangedWorkspace(t *testing.T) {
	state, now := verifiableState(t)
	candidate := Candidate{Kind: SnapshotCandidate, Revision: snapshotRevision, BaseRevision: baseRevision, Digest: snapshotDigest}
	if err := state.BeginCandidateVerification("WI-001", candidate, "/tmp/wt", "/tmp/log", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerification("WI-001", snapshotRevision, "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
	latest, _ := state.LatestVerification("WI-001")
	_, err := state.ResolveReviewCandidate("WI-001", latest.ID, baseRevision,
		"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if err == nil || !strings.Contains(err.Error(), "workspace no longer matches verified snapshot") {
		t.Fatalf("ResolveReviewCandidate error = %v", err)
	}
	if len(state.Evidence) != 1 || state.WorkItemStatus("WI-001") != Review {
		t.Fatalf("refused review changed state: %#v, %s", state.Evidence, state.WorkItemStatus("WI-001"))
	}
}

func TestSnapshotStalenessUsesDigestWhileCommitStalenessUsesRevision(t *testing.T) {
	snapshotState, now := verifiableState(t)
	candidate := Candidate{Kind: SnapshotCandidate, Revision: snapshotRevision, BaseRevision: baseRevision, Digest: snapshotDigest}
	if err := snapshotState.BeginCandidateVerification("WI-001", candidate, "/tmp/wt", "/tmp/log", now); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotState.RecordVerification("WI-001", snapshotRevision, "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
	if snapshotState.CandidateStale("WI-001", "different-head", snapshotDigest) {
		t.Fatal("unchanged snapshot digest reported stale because HEAD differs")
	}
	if snapshotState.CandidateStale("WI-001", baseRevision, "") {
		t.Fatal("unavailable workspace digest was reported as a content change")
	}
	if !snapshotState.CandidateStale("WI-001", baseRevision,
		"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb") {
		t.Fatal("changed snapshot digest was not stale")
	}

	commitState, commitNow := verifiableState(t)
	if err := commitState.BeginVerification("WI-001", baseRevision, "/tmp/wt", "/tmp/log", commitNow); err != nil {
		t.Fatal(err)
	}
	if _, err := commitState.RecordVerification("WI-001", baseRevision, "make verify", 0, commitNow); err != nil {
		t.Fatal(err)
	}
	if !commitState.CandidateStale("WI-001", snapshotRevision, "") {
		t.Fatal("commit evidence did not become stale when HEAD moved")
	}
}

func TestValidateRequiresCurrentRunExactlyWhileVerifying(t *testing.T) {
	state, now := verifiableState(t)
	state.WorkItems[0].Status = Verifying
	if err := state.Validate(); err == nil {
		t.Fatal("accepted VERIFYING work without a current run")
	}

	state, now = verifiableState(t)
	if err := state.BeginVerification("WI-001", baseRevision, "/tmp/wt", "/tmp/log", now); err != nil {
		t.Fatal(err)
	}
	state.WorkItems[0].Status = Running
	if err := state.Validate(); err == nil {
		t.Fatal("accepted a current run on non-VERIFYING work")
	}
}
