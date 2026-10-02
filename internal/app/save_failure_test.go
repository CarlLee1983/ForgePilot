package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// errStateSaveFailed stands in for whatever a filesystem would have said. It is
// a sentinel so the assertion can be that the *original* failure is still
// identifiable, rather than that some error came back.
var errStateSaveFailed = errors.New("injected: the state could not be replaced")

// A transaction that fails after the check already ran produces nothing: no
// Evidence, no status, and no recovery blocker invented out of "an error
// happened". The save failure is armed from outside, while the check is still
// running, so that it lands on the one write under test — the transaction that
// records the outcome. Arming it up front would have stopped the command at
// `beginRun` instead, which is a different write.
func TestStateSaveFailureAloneIsAnOrdinaryFailure(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "check-running")
	fixture := newVerifyFixture(t, fmt.Sprintf("verify:\n\t@: > %s; sleep 1\n", marker))
	saveHits := make(chan struct{}, 1)
	armed := make(chan func(), 1)
	go func() {
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(marker); err == nil {
				armed <- storage.InjectStateSaveFailure(fixture.root, func() error {
					saveHits <- struct{}{}
					return errStateSaveFailed
				})
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		armed <- func() {}
	}()

	result, err := Verify(nil, fixture.root, fixture.id, io.Discard,
		VerifyOptions{Snapshot: true, Now: func() time.Time { return time.Now().UTC() }})
	disarm := <-armed
	defer disarm()

	if len(saveHits) != 1 {
		t.Fatalf("the injected save failure fired %d times, want once (err = %v)", len(saveHits), err)
	}
	if !errors.Is(err, errStateSaveFailed) {
		t.Fatalf("err = %v, want the injected save failure", err)
	}
	if result.Cleanup != nil || len(result.Unresolved) != 0 {
		t.Fatalf("a save failure invented a recovery blocker: %v / %#v", result.Cleanup, result.Unresolved)
	}
	if result.HasEvidence || result.Status != "" {
		t.Fatalf("a failed transaction was reported as a saved result: %#v / %q", result.Evidence, result.Status)
	}
	state, loadErr := storage.Load(fixture.root)
	if loadErr != nil {
		t.Fatalf("state.json no longer loads after a failed save: %v", loadErr)
	}
	if evidence, ok := state.LatestVerification(fixture.id); ok {
		t.Fatalf("a PASS/FAIL was published by a transaction that failed: %#v", evidence)
	}
	if status := state.WorkItemStatus(fixture.id); status != work.Verifying {
		t.Fatalf("the work item is %s; the run was neither closed out nor left as it was", status)
	}
	// Nothing was left blocking either: with no unconfirmed group, the checkout
	// is tidied away exactly as it is on any other path.
	worktree := WorktreePath(fixture.root, fixture.id, result.Candidate.Revision)
	if _, statErr := os.Stat(worktree); statErr == nil {
		t.Fatalf("an ordinary save failure kept the checkout %s as if something were running in it", worktree)
	}
}

// The ordinary path, unchanged: no unconfirmed group, a transaction that lands,
// a PASS that reaches state and a checkout that is tidied away.
func TestAnUndisturbedVerificationStillPassesAndTidiesUp(t *testing.T) {
	fixture := newVerifyFixture(t, "verify:\n\t@true\n")
	result, err := Verify(nil, fixture.root, fixture.id, io.Discard,
		VerifyOptions{Snapshot: true, Now: func() time.Time { return time.Now().UTC() }})
	if err != nil {
		t.Fatalf("the verification failed operationally: %v", err)
	}
	if !result.HasEvidence || result.Evidence.Result != work.Pass {
		t.Fatalf("result = %#v, want a saved PASS", result)
	}
	if result.Cleanup != nil || len(result.Unresolved) != 0 {
		t.Fatalf("an undisturbed verification reported cleanup trouble: %v / %#v",
			result.Cleanup, result.Unresolved)
	}
	// The fixture's Goal requires approval, so a machine PASS is not acceptance.
	state, loadErr := storage.Load(fixture.root)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if status := state.WorkItemStatus(fixture.id); status != work.Review {
		t.Fatalf("a passing verification under an Approval Requirement left the work item %s, want REVIEW", status)
	}
	worktree := WorktreePath(fixture.root, fixture.id, result.Candidate.Revision)
	if _, statErr := os.Stat(worktree); statErr == nil {
		t.Fatalf("the checkout %s was left behind by a clean verification", filepath.Base(worktree))
	}
}
