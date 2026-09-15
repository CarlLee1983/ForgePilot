package app

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/process"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// Two failures, in one order, on one path: the workspace facts read after a
// check has passed reports a process group nobody could confirm empty, and the
// transaction that was going to record the Evidence then fails before it
// replaces state.json.
//
// Before this, only the second of those survived. The unconfirmed group was
// noted on the result *after* the transaction had already returned, so a failed
// transaction returned first and took the group with it — and the Runner, which
// reads result.Cleanup to decide whether anything may start next, saw nothing,
// cleared the pending it had written before the verification began, and left the
// workspace looking clear while something was still running in it.
//
// Whether Evidence was saved and whether a process is still running are two
// different facts. This test is about the second one surviving the first one's
// failure. See docs/adr/0022-pending-cleanup-outlives-the-process.md.

// errStateSaveFailed stands in for whatever a filesystem would have said. It is
// a sentinel so the assertion can be that the *original* failure is still
// identifiable, rather than that some error came back.
var errStateSaveFailed = errors.New("injected: the state could not be replaced")

// doubleFailure records that each injected failure fired and in what order, so
// the test asserts a sequence it observed rather than one it assumes.
type doubleFailure struct {
	mutex    sync.Mutex
	sequence []string
	pgid     int
}

func (failure *doubleFailure) hit(name string) {
	failure.mutex.Lock()
	defer failure.mutex.Unlock()
	failure.sequence = append(failure.sequence, name)
}

func (failure *doubleFailure) observed() ([]string, int) {
	failure.mutex.Lock()
	defer failure.mutex.Unlock()
	return append([]string(nil), failure.sequence...), failure.pgid
}

// failRefreshThenSave arms the two failures as one mechanism: the state save is
// armed from inside the facts failure, so it can only land on the very next
// replacement of state.json — the one this verification's own transaction is
// about to make. Arming it up front would have hit whichever write happened to
// come first, which for this path is `beginRun`.
//
// The facts read is recognised by `rev-parse --absolute-git-dir`, which only
// InspectSnapshot runs: the Candidate capture on the way in prepares its
// snapshot without isolating the object database, so it never asks for it.
func failRefreshThenSave(t *testing.T, root string) *doubleFailure {
	t.Helper()
	failure := &doubleFailure{}
	var disarmSave func()
	t.Cleanup(func() {
		if disarmSave != nil {
			disarmSave()
		}
	})
	restore := process.InjectCleanupFailure(func(pgid int, argv []string, _ time.Duration) error {
		if !strings.Contains(strings.Join(argv, " "), "--absolute-git-dir") {
			return nil
		}
		failure.mutex.Lock()
		failure.pgid = pgid
		failure.mutex.Unlock()
		failure.hit("facts")
		disarmSave = storage.InjectStateSaveFailure(root, func() error {
			failure.hit("save")
			return errStateSaveFailed
		})
		// The group named is the Git child's own. It has already exited, which is
		// right for this test: what is asserted here is that the observation
		// travels, and a group that is genuinely still alive is what the Runner
		// test needs in order to assert that the blocking then holds.
		return &process.NotSettled{PGID: pgid, Reason: "injected: the group could not be confirmed"}
	})
	t.Cleanup(restore)
	return failure
}

func TestRefreshCleanupSurvivesStateSaveFailure(t *testing.T) {
	fixture := newVerifyFixture(t, "verify:\n\t@true\n")
	failure := failRefreshThenSave(t, fixture.root)

	result, err := Verify(nil, fixture.root, fixture.id, io.Discard,
		VerifyOptions{Snapshot: true, Now: func() time.Time { return time.Now().UTC() }})

	sequence, pgid := failure.observed()
	if len(sequence) != 2 || sequence[0] != "facts" || sequence[1] != "save" {
		t.Fatalf("the two failures did not fire in order: %v (err = %v)", sequence, err)
	}
	// The operational failure is still the one that happened, not a summary of it.
	if !errors.Is(err, errStateSaveFailed) {
		t.Fatalf("the original state save failure is no longer identifiable from %v", err)
	}
	// The half that used to be lost.
	if result.Cleanup == nil {
		t.Fatalf("Cleanup was lost to the failed transaction; err = %v", err)
	}
	if !errors.Is(result.Cleanup, process.ErrNotSettled) {
		t.Fatalf("Cleanup = %v, want an unsettled report", result.Cleanup)
	}
	if len(result.Unresolved) != 1 {
		t.Fatalf("want exactly one unresolved execution, got %#v", result.Unresolved)
	}
	unresolved := result.Unresolved[0]
	if unresolved.Kind != UnresolvedGit {
		t.Fatalf("the unresolved execution came from %s, want %s", unresolved.Kind, UnresolvedGit)
	}
	if unresolved.PGID != pgid || unresolved.PGID <= 0 {
		t.Fatalf("the unresolved execution names group %d, want the observed %d", unresolved.PGID, pgid)
	}
	// The workspace the facts read actually ran in, not the checkout: this group
	// was started against the user's own worktree.
	if unresolved.Location != fixture.root {
		t.Fatalf("the unresolved execution points at %q, not the workspace %q", unresolved.Location, fixture.root)
	}
	// A transaction that failed produced nothing, and the in-memory Evidence its
	// callback built is not a saved result.
	if result.HasEvidence {
		t.Fatalf("a failed transaction was reported as saved Evidence: %#v", result.Evidence)
	}
	if result.Status != "" {
		t.Fatalf("a status was reported for a transaction that did not land: %q", result.Status)
	}

	// And the durable side: the state is intact, unchanged, and holds no verdict
	// for a run that never recorded one.
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
	// The checkout a recovery record points at is not tidied away underneath a
	// group that could not be confirmed gone.
	worktree := WorktreePath(fixture.root, fixture.id, result.Candidate.Revision)
	if _, statErr := os.Stat(worktree); statErr != nil {
		t.Fatalf("the checkout an unconfirmed group may still be using was removed: %v", statErr)
	}
}

// The other three corners of the same square: a blocking recovery must come
// from an actual unresolved process, never from "an error happened".
//
// This one reaches the same branch the fix is in — the transaction that records
// the outcome fails — but with a facts read that reported nothing. Arming the
// failure up front would have stopped the command at `beginRun` instead, which
// is a different write and never reaches the code under test.
func TestStateSaveFailureAloneIsAnOrdinaryFailure(t *testing.T) {
	fixture := newVerifyFixture(t, "verify:\n\t@true\n")
	var mutex sync.Mutex
	var armed, saveHits int
	var disarmSave func()
	t.Cleanup(func() {
		if disarmSave != nil {
			disarmSave()
		}
	})
	restore := process.InjectCleanupFailure(func(_ int, argv []string, _ time.Duration) error {
		if !strings.Contains(strings.Join(argv, " "), "--absolute-git-dir") {
			return nil
		}
		mutex.Lock()
		if armed > 0 {
			mutex.Unlock()
			return nil
		}
		armed++
		mutex.Unlock()
		// The facts read itself is confirmed clean: nil is returned, so this
		// verification has no unresolved group anywhere. Only the transaction that
		// was about to record its result fails.
		disarmSave = storage.InjectStateSaveFailure(fixture.root, func() error {
			mutex.Lock()
			defer mutex.Unlock()
			saveHits++
			return errStateSaveFailed
		})
		return nil
	})
	defer restore()

	result, err := Verify(nil, fixture.root, fixture.id, io.Discard,
		VerifyOptions{Snapshot: true, Now: func() time.Time { return time.Now().UTC() }})

	mutex.Lock()
	armedHits, failures := armed, saveHits
	mutex.Unlock()
	if armedHits != 1 || failures != 1 {
		t.Fatalf("the failure was not armed from a clean facts read and fired once: armed = %d, fired = %d (err = %v)",
			armedHits, failures, err)
	}
	if !errors.Is(err, errStateSaveFailed) {
		t.Fatalf("err = %v, want the injected save failure", err)
	}
	if result.Cleanup != nil || len(result.Unresolved) != 0 {
		t.Fatalf("a save failure invented a recovery blocker: %v / %#v", result.Cleanup, result.Unresolved)
	}
	if result.HasEvidence {
		t.Fatalf("a failed transaction was reported as saved Evidence: %#v", result.Evidence)
	}
	state, loadErr := storage.Load(fixture.root)
	if loadErr != nil {
		t.Fatalf("state.json no longer loads after a failed save: %v", loadErr)
	}
	if evidence, ok := state.LatestVerification(fixture.id); ok {
		t.Fatalf("a PASS/FAIL was published by a transaction that failed: %#v", evidence)
	}
	// Nothing was left blocking either: with no unconfirmed group, the checkout
	// is tidied away exactly as it is on any other path.
	worktree := WorktreePath(fixture.root, fixture.id, result.Candidate.Revision)
	if _, statErr := os.Stat(worktree); statErr == nil {
		t.Fatalf("an ordinary save failure kept the checkout %s as if something were running in it", worktree)
	}
}

// An unconfirmed group with a transaction that *did* land: the Evidence stands
// and is durable, and the group still travels back.
func TestRefreshCleanupTravelsWithSavedEvidence(t *testing.T) {
	fixture := newVerifyFixture(t, "verify:\n\t@true\n")
	var pgid int
	restore := process.InjectCleanupFailure(func(observed int, argv []string, _ time.Duration) error {
		if !strings.Contains(strings.Join(argv, " "), "--absolute-git-dir") {
			return nil
		}
		pgid = observed
		return &process.NotSettled{PGID: observed, Reason: "injected: the group could not be confirmed"}
	})
	defer restore()

	result, err := Verify(nil, fixture.root, fixture.id, io.Discard,
		VerifyOptions{Snapshot: true, Now: func() time.Time { return time.Now().UTC() }})
	if err != nil {
		t.Fatalf("the verification failed operationally: %v", err)
	}
	if !result.HasEvidence || result.Evidence.Result != work.Pass {
		t.Fatalf("the Evidence the check earned was not preserved: %#v", result.Evidence)
	}
	if result.Cleanup == nil || !errors.Is(result.Cleanup, process.ErrNotSettled) {
		t.Fatalf("Cleanup = %v, want an unsettled report", result.Cleanup)
	}
	if len(result.Unresolved) != 1 || result.Unresolved[0].PGID != pgid {
		t.Fatalf("the unresolved execution does not name the observed group %d: %#v", pgid, result.Unresolved)
	}
	state, loadErr := storage.Load(fixture.root)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if evidence, ok := state.LatestVerification(fixture.id); !ok || evidence.ID != result.Evidence.ID {
		t.Fatalf("the saved Evidence is not the one reported: %#v", evidence)
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
	if result.Cleanup != nil || len(result.Unresolved) != 0 || result.RefreshWarning != nil {
		t.Fatalf("an undisturbed verification reported cleanup trouble: %v / %#v / %v",
			result.Cleanup, result.Unresolved, result.RefreshWarning)
	}
	// Human review is still where it was: a machine PASS is not acceptance.
	state, loadErr := storage.Load(fixture.root)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if status := state.WorkItemStatus(fixture.id); status == work.Done {
		t.Fatalf("a passing verification completed the work item on its own")
	}
	worktree := WorktreePath(fixture.root, fixture.id, result.Candidate.Revision)
	if _, statErr := os.Stat(worktree); statErr == nil {
		t.Fatalf("the checkout %s was left behind by a clean verification", filepath.Base(worktree))
	}
}
