package app

import (
	"testing"

	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestVerificationVerdictFingerprintIncludesTheWholeLatestVerificationEvidence(t *testing.T) {
	state := &work.State{
		Goals: []work.Goal{{ID: "goal", Repository: "repo", ReviewPolicy: work.ReviewPerGoal, Status: work.GoalActive}},
		WorkItems: []work.Item{{ID: "WI-001", GoalID: "goal", Status: work.Verifying, CurrentRun: &work.Run{
			CandidateDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		}}},
		Evidence: []work.Evidence{{
			ID: "EV-001", Type: work.VerificationEvidence, WorkItemID: "WI-001",
			Revision: "1111111111111111111111111111111111111111", CandidateKind: work.SnapshotCandidate,
			BaseRevision:    "2222222222222222222222222222222222222222",
			CandidateDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Command:         "make verify", Result: work.Pass, Runtime: map[string]string{"go": "1.25.5"},
		}},
	}
	before, err := verificationVerdictFingerprint(state, "WI-001")
	if err != nil {
		t.Fatal(err)
	}
	state.Goals[0].Status = work.GoalBlocked
	if afterGoalBlock, err := verificationVerdictFingerprint(state, "WI-001"); err != nil {
		t.Fatal(err)
	} else if before != afterGoalBlock {
		t.Fatal("blocking the owning Goal changed the verification verdict fingerprint")
	}
	state.Goals[0].Status = work.GoalActive
	state.WorkItems[0].CurrentRun.CandidateDigest = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	if afterCurrentRun, err := verificationVerdictFingerprint(state, "WI-001"); err != nil {
		t.Fatal(err)
	} else if before == afterCurrentRun {
		t.Fatal("changing CurrentRun candidate digest left the verdict fingerprint unchanged")
	}
	state.WorkItems[0].CurrentRun.CandidateDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	state.Evidence[0].CandidateDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	after, err := verificationVerdictFingerprint(state, "WI-001")
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("changing the latest Verification Evidence candidate digest left the verdict fingerprint unchanged")
	}
	if err := ensureVerificationVerdictUnchanged(state, "WI-001", before); err == nil {
		t.Fatal("changed Verification Evidence was accepted at the final transaction boundary")
	}
}

func TestVerificationRetryCommandPreservesCandidateMode(t *testing.T) {
	commit := work.Candidate{Kind: work.CommitCandidate, Revision: "1111111111111111111111111111111111111111"}
	if got := VerificationRetryCommand("WI-001", commit); got != "forgepilot verify WI-001" {
		t.Fatalf("commit retry = %q", got)
	}
	snapshot := work.Candidate{Kind: work.SnapshotCandidate, Revision: "2222222222222222222222222222222222222222",
		BaseRevision: "1111111111111111111111111111111111111111", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if got := VerificationRetryCommand("WI-001", snapshot); got != "forgepilot verify WI-001 --snapshot" {
		t.Fatalf("snapshot retry = %q", got)
	}
}
