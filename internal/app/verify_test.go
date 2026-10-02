package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestVerificationVerdictFingerprintIncludesTheWholeLatestVerificationEvidence(t *testing.T) {
	state := &work.State{
		Goals: []work.Goal{{ID: "goal", Repository: "repo", Status: work.GoalActive}},
		WorkItems: []work.Item{{ID: "WI-001", GoalID: "goal", Status: work.Verifying, CurrentRun: &work.Run{
			CandidateDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		}}},
		Evidence: []work.Evidence{{
			ID: "EV-001", Type: work.VerificationEvidence, WorkItemID: "WI-001",
			Revision: "1111111111111111111111111111111111111111", CandidateKind: work.SnapshotCandidate,
			BaseRevision:    "2222222222222222222222222222222222222222",
			CandidateDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Command:         "make verify", Result: work.Pass,
		}},
	}
	before, err := verificationVerdictFingerprint(state, "WI-001")
	if err != nil {
		t.Fatal(err)
	}
	state.Goals[0].Status = work.GoalCancelled
	if afterGoalCancel, err := verificationVerdictFingerprint(state, "WI-001"); err != nil {
		t.Fatal(err)
	} else if before != afterGoalCancel {
		t.Fatal("cancelling the owning Goal changed the verification verdict fingerprint")
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

// A verification that finds the repository's canonical lock held is refused
// before it creates a log, a checkout or a run, and before it executes the check.
func TestConcurrentVerificationIsRefusedBeforeRunArtifacts(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "canonical-runs")
	fixture := newVerifyFixture(t, "verify:\n\t@printf 'run\\n' >> "+counter+"\n")
	statePath := filepath.Join(fixture.root, ".forgepilot", "state.json")
	beforeState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}

	locked := make(chan struct{})
	release := make(chan struct{})
	lockResult := make(chan error, 1)
	go func() {
		lockResult <- storage.WithCanonicalVerificationLock(fixture.root, func() error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	result, verifyErr := Verify(context.Background(), fixture.root, fixture.id, io.Discard, VerifyOptions{})
	close(release)
	if err := <-lockResult; err != nil {
		t.Fatal(err)
	}
	if !isRefusal(verifyErr) {
		t.Fatalf("Verify error = %v, want refusal", verifyErr)
	}
	if result.HasEvidence || result.VerificationRunID != "" {
		t.Fatalf("refused result = %#v, want no new run", result)
	}
	afterState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterState, beforeState) {
		t.Fatal("repository-lock refusal changed state")
	}
	for _, artifacts := range []string{"logs", "worktrees"} {
		if entries, err := os.ReadDir(filepath.Join(fixture.root, ".forgepilot", artifacts)); err == nil && len(entries) != 0 {
			t.Fatalf("refusal left %s: %v", artifacts, entries)
		}
	}
	if _, err := os.Stat(counter); !os.IsNotExist(err) {
		t.Fatalf("canonical check ran or counter stat failed: %v", err)
	}
}
