package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestOnePassingCandidateRunFansOutToEveryEligibleStaleWorkItem(t *testing.T) {
	root, counter, now, anchorID, staleIDs := newFanoutFixture(t)

	result, err := Verify(context.Background(), root, anchorID, io.Discard, VerifyOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(counter); err != nil {
		t.Fatal(err)
	} else if string(got) != "run\n" {
		t.Fatalf("canonical executions = %q, want exactly one", got)
	}
	if len(result.EvidenceSet) != 3 {
		t.Fatalf("EvidenceSet = %#v, want anchor plus two stale recipients", result.EvidenceSet)
	}
	wantIDs := []string{anchorID, staleIDs[0], staleIDs[1]}
	for index, evidence := range result.EvidenceSet {
		if evidence.WorkItemID != wantIDs[index] {
			t.Fatalf("EvidenceSet[%d].WorkItemID = %s, want %s", index, evidence.WorkItemID, wantIDs[index])
		}
		if evidence.VerificationRunID == "" || evidence.VerificationRunID != result.VerificationRunID {
			t.Fatalf("EvidenceSet[%d] run = %q, result run = %q", index, evidence.VerificationRunID, result.VerificationRunID)
		}
		if evidence.Result != work.Pass {
			t.Fatalf("EvidenceSet[%d].Result = %s, want PASS", index, evidence.Result)
		}
	}
	if !reflect.DeepEqual(result.Evidence, result.EvidenceSet[0]) {
		t.Fatalf("legacy Evidence = %#v, anchor EvidenceSet[0] = %#v", result.Evidence, result.EvidenceSet[0])
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range append([]string{anchorID}, staleIDs...) {
		if status := state.WorkItemStatus(id); status != work.Verified {
			t.Fatalf("%s status = %s, want VERIFIED", id, status)
		}
		latest, ok := state.LatestVerification(id)
		if !ok || latest.VerificationRunID != result.VerificationRunID {
			t.Fatalf("%s latest Verification = %#v, want shared run %s", id, latest, result.VerificationRunID)
		}
	}
}

func TestFailingCandidateRunRecordsOnlyTheAnchor(t *testing.T) {
	root, counter, now, anchorID, staleIDs := newFanoutFixture(t)
	writeFixtureFile(t, filepath.Join(root, "Makefile"), fmt.Sprintf("verify:\n\t@printf 'fail\\n' >> %q; exit 1\n", counter))
	runGit(t, root, "add", "Makefile")
	runGit(t, root, "commit", "-m", "failing candidate")

	result, err := Verify(context.Background(), root, anchorID, io.Discard, VerifyOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.EvidenceSet) != 1 || result.EvidenceSet[0].WorkItemID != anchorID || result.EvidenceSet[0].Result != work.Fail {
		t.Fatalf("EvidenceSet = %#v, want one anchor FAIL", result.EvidenceSet)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range staleIDs {
		latest, ok := state.LatestVerification(id)
		if !ok || latest.Result != work.Pass || latest.VerificationRunID == result.VerificationRunID {
			t.Fatalf("optional %s latest Verification changed: %#v", id, latest)
		}
		if status := state.WorkItemStatus(id); status != work.Verified {
			t.Fatalf("optional %s status = %s, want VERIFIED", id, status)
		}
	}
}

func TestConcurrentAnchorIsRefusedBeforeRunArtifacts(t *testing.T) {
	root, counter, now, anchorID, _ := newFanoutFixture(t)
	statePath := filepath.Join(root, ".forgepilot", "state.json")
	beforeState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	beforeLogs := directoryNames(t, filepath.Join(root, ".forgepilot", "logs"))
	beforeWorktrees := directoryNames(t, filepath.Join(root, ".forgepilot", "worktrees"))

	locked := make(chan struct{})
	release := make(chan struct{})
	lockResult := make(chan error, 1)
	go func() {
		lockResult <- storage.WithCanonicalVerificationLock(root, func() error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	result, verifyErr := Verify(context.Background(), root, anchorID, io.Discard, VerifyOptions{Now: func() time.Time { return now }})
	close(release)
	if err := <-lockResult; err != nil {
		t.Fatal(err)
	}
	if !IsRefusal(verifyErr) {
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
	if got := directoryNames(t, filepath.Join(root, ".forgepilot", "logs")); !reflect.DeepEqual(got, beforeLogs) {
		t.Fatalf("logs after refusal = %v, want %v", got, beforeLogs)
	}
	if got := directoryNames(t, filepath.Join(root, ".forgepilot", "worktrees")); !reflect.DeepEqual(got, beforeWorktrees) {
		t.Fatalf("worktrees after refusal = %v, want %v", got, beforeWorktrees)
	}
	if _, err := os.Stat(counter); !os.IsNotExist(err) {
		t.Fatalf("canonical check ran or counter stat failed: %v", err)
	}
}

func directoryNames(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func newFanoutFixture(t *testing.T) (root, counter string, now time.Time, anchorID string, staleIDs []string) {
	t.Helper()
	var err error
	root, err = filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	counter = filepath.Join(t.TempDir(), "canonical-runs")
	runGit(t, root, "init", "--initial-branch=main")
	runGit(t, root, "config", "user.email", "fixture@local")
	runGit(t, root, "config", "user.name", "Fixture")
	if err := storage.Init(root); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(root, "Makefile"), fmt.Sprintf("verify:\n\t@printf 'run\\n' >> %q\n", counter))
	for _, story := range []string{"a", "b", "c"} {
		writeFixtureFile(t, filepath.Join(root, "specs", "stories", story+".md"), "# story\n")
	}
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-m", "seed")

	now = time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	var stale []string
	var anchor string
	if err := storage.Update(root, func(state *work.State) error {
		if err := state.AddGoalWithReviewPolicy("g", "Goal", "", root, work.ReviewPerGoal, now); err != nil {
			return err
		}
		for _, story := range []string{"a", "b", "c"} {
			item, err := state.AddWork("g", filepath.Join("specs", "stories", story+".md"), nil, now)
			if err != nil {
				return err
			}
			if err := state.Start(item.ID, now); err != nil {
				return err
			}
			if story == "c" {
				anchor = item.ID
			} else {
				stale = append(stale, item.ID)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	clock := func() time.Time { return now }
	for _, id := range stale {
		if _, err := Verify(context.Background(), root, id, io.Discard, VerifyOptions{Now: clock}); err != nil {
			t.Fatalf("seed verification of %s: %v", id, err)
		}
	}
	writeFixtureFile(t, filepath.Join(root, "candidate.txt"), "new candidate\n")
	runGit(t, root, "add", "candidate.txt")
	runGit(t, root, "commit", "-m", "new candidate")
	if err := os.Remove(counter); err != nil {
		t.Fatal(err)
	}

	return root, counter, now, anchor, stale
}
