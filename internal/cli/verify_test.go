package cli

import (
	"testing"

	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestVerificationRetryCommandPreservesCandidateMode(t *testing.T) {
	commit := work.Candidate{Kind: work.CommitCandidate, Revision: "1111111111111111111111111111111111111111"}
	if got := verificationRetryCommand("WI-001", commit); got != "forgepilot verify WI-001" {
		t.Fatalf("commit retry = %q", got)
	}
	snapshot := work.Candidate{Kind: work.SnapshotCandidate, Revision: "2222222222222222222222222222222222222222",
		BaseRevision: "1111111111111111111111111111111111111111", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if got := verificationRetryCommand("WI-001", snapshot); got != "forgepilot verify WI-001 --snapshot" {
		t.Fatalf("snapshot retry = %q", got)
	}
}
