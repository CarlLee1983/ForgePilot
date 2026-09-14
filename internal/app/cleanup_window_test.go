package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/process"
	"github.com/CarlLee1983/ForgePilot/internal/repository"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// A cleanup allowance is for cleaning up. Opening it at the top of the command
// — which a `cleanup.context()` evaluated as an argument did, whether or not
// there was anything to reclaim — spent it on the snapshot, the checkout, the
// preflight and the check itself, so the tidying that follows a long run began
// with nothing left. Reclaiming an orphan and tidying up afterwards are also
// two stages with the whole of the work between them, so they cannot share one
// deadline. See docs/adr/0021-execution-limits-are-bounded-and-named.md and
// docs/adr/0022-pending-cleanup-outlives-the-process.md.

// longerThanTheCleanupGrace is how long the canonical check runs in these
// tests. It is deliberately just past the grace: the point is a run whose work
// outlives one cleanup window, not a slow test.
var longerThanTheCleanupGrace = process.CleanupGrace + 2*time.Second

// budgetWatcher records the allowance left whenever a named managed command is
// settled, without failing anything.
type budgetWatcher struct {
	mutex sync.Mutex
	seen  map[string][]time.Duration
}

func watchCleanupBudget(t *testing.T, name func(argv []string) string) *budgetWatcher {
	t.Helper()
	watcher := &budgetWatcher{seen: map[string][]time.Duration{}}
	restore := process.InjectCleanupFailure(func(_ int, argv []string, remaining time.Duration) error {
		if label := name(argv); label != "" {
			watcher.mutex.Lock()
			watcher.seen[label] = append(watcher.seen[label], remaining)
			watcher.mutex.Unlock()
		}
		// Nothing is failed: this seam is being used to read a number, and a stop
		// that really happens is what the rest of the command should see.
		return nil
	})
	t.Cleanup(restore)
	return watcher
}

func (watcher *budgetWatcher) last(label string) (time.Duration, bool) {
	watcher.mutex.Lock()
	defer watcher.mutex.Unlock()
	values := watcher.seen[label]
	if len(values) == 0 {
		return 0, false
	}
	return values[len(values)-1], true
}

func (watcher *budgetWatcher) all(label string) []time.Duration {
	watcher.mutex.Lock()
	defer watcher.mutex.Unlock()
	return append([]time.Duration(nil), watcher.seen[label]...)
}

// afterTheCheck labels the worktree removals that happen once the canonical
// check has run, which is the tidying stage, and leaves the pre-clean
// AddWorktree does on the way in unlabelled.
func afterTheCheck(marker string) func([]string) string {
	return func(argv []string) string {
		if !strings.Contains(strings.Join(argv, " "), "worktree remove") {
			return ""
		}
		if _, err := os.Stat(marker); err != nil {
			return "pre-clean"
		}
		return "tidy-up"
	}
}

// slowCheck is a canonical check that passes, announces that it has run, and
// takes longer than one cleanup window to do it.
func slowCheck(marker string) string {
	return fmt.Sprintf("verify:\n\t@sleep %d; : > %s\n", int(longerThanTheCleanupGrace.Seconds()), marker)
}

func TestWorkLongerThanOneCleanupWindowStillLeavesOneForTheTidyingUp(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "check-ran")
	fixture := newVerifyFixture(t, slowCheck(marker))
	watcher := watchCleanupBudget(t, afterTheCheck(marker))

	result, err := Verify(context.Background(), fixture.root, fixture.id, io.Discard,
		VerifyOptions{Snapshot: true, Now: func() time.Time { return time.Now().UTC() }})
	if err != nil {
		t.Fatalf("the verification did not complete: %v", err)
	}
	if !result.HasEvidence || result.Evidence.Result != work.Pass {
		t.Fatalf("the fixture did not produce a PASS: %#v", result)
	}
	remaining, seen := watcher.last("tidy-up")
	if !seen {
		t.Fatal("the checkout was never removed, so there is no cleanup budget to assert about")
	}
	if remaining <= 0 {
		t.Fatalf("the tidying up began with %s left: its window was opened before the work, not before the cleanup", remaining)
	}
	worktree := WorktreePath(fixture.root, fixture.id, result.Candidate.Revision)
	if _, statErr := os.Stat(worktree); !os.IsNotExist(statErr) {
		t.Fatalf("the checkout was left behind: %v", statErr)
	}
}

// An orphan is closed out before the work starts, and that closing-out is its
// own stage with its own allowance. Spending it must not leave the tidying up
// at the end of a long run with an allowance that expired during the check.
func TestReclaimingAnOrphanDoesNotSpendTheLaterTidyingUpsWindow(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "check-ran")
	fixture := newVerifyFixture(t, slowCheck(marker))
	abandoned := leaveAbandonedRun(t, fixture)
	watcher := watchCleanupBudget(t, afterTheCheck(marker))

	result, err := Verify(context.Background(), fixture.root, fixture.id, io.Discard,
		VerifyOptions{Snapshot: true, Now: func() time.Time { return time.Now().UTC() }})
	if err != nil {
		t.Fatalf("the verification did not complete: %v", err)
	}
	if result.Reclaimed == nil {
		t.Fatal("the abandoned run was not reclaimed, so the reclaim stage never ran")
	}
	if _, statErr := os.Stat(abandoned); statErr == nil {
		t.Fatalf("the abandoned checkout %s was not cleared", abandoned)
	}
	remaining, seen := watcher.last("tidy-up")
	if !seen {
		t.Fatal("the checkout was never removed, so there is no cleanup budget to assert about")
	}
	if remaining <= 0 {
		t.Fatalf("the tidying up began with %s left: the reclaim stage's window was still the one in use", remaining)
	}
}

// leaveAbandonedRun marks a Verification Run in flight against a checkout that
// really exists, which is what reclaimOrphan finds and clears on the way in.
func leaveAbandonedRun(t *testing.T, fixture verifyFixture) string {
	t.Helper()
	revision := headRevision(t, fixture.root)
	worktree := WorktreePath(fixture.root, fixture.id, revision)
	if err := repository.AddWorktree(context.Background(), fixture.root, worktree, revision); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := storage.Update(fixture.root, func(state *work.State) error {
		return state.BeginCandidateVerificationWithRuntime(fixture.id,
			work.Candidate{Kind: work.CommitCandidate, Revision: revision}, worktree, "", nil, now)
	}); err != nil {
		t.Fatal(err)
	}
	return worktree
}

func headRevision(t *testing.T, root string) string {
	t.Helper()
	revision, err := repository.Head(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

// Within one cleanup stage the helpers share one allowance. A budget re-granted
// in full at every managed command is not a bound on the path.
func TestOneCleanupStageSpendsOneAllowanceAcrossItsHelpers(t *testing.T) {
	budget := process.NewBudget()
	ctx := process.WithBudget(context.Background(), budget)
	watcher := watchCleanupBudget(t, func(argv []string) string {
		if len(argv) > 0 && filepath.Base(argv[0]) == "true" {
			return "helper"
		}
		return ""
	})

	for index := 0; index < 3; index++ {
		if _, err := process.Start(ctx, exec.Command("/usr/bin/true"), io.Discard); err != nil {
			t.Fatal(err)
		}
		time.Sleep(60 * time.Millisecond)
	}
	seen := watcher.all("helper")
	if len(seen) < 3 {
		t.Fatalf("the stage ran %d managed commands, want 3", len(seen))
	}
	for index := 1; index < len(seen); index++ {
		if seen[index] >= seen[index-1] {
			t.Fatalf("helper %d was granted %s after %s: the allowance was re-granted rather than shared",
				index, seen[index], seen[index-1])
		}
	}
	if remaining := budget.Remaining(); remaining >= process.CleanupGrace {
		t.Fatalf("the stage's own budget is untouched at %s", remaining)
	}
}

// Tidying up runs after the Evidence is durable, so a cleanup it cannot confirm
// must not touch that Evidence — and must not be reported only as a warning on
// stdout, which is all the caller would ever have seen. The caller is the layer
// that has to refuse to start the next step.
// See docs/adr/0022-pending-cleanup-outlives-the-process.md.
func TestAnUnconfirmedTidyUpTravelsWithAPassRatherThanBecomingAWarning(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "check-ran")
	fixture := newVerifyFixture(t, fmt.Sprintf("verify:\n\t@: > %s\n", marker))
	// Only the removal that happens after the check: the pre-clean AddWorktree
	// does on the way in is a different call at a different moment.
	restore := process.InjectCleanupFailure(func(pgid int, argv []string, _ time.Duration) error {
		if !strings.Contains(strings.Join(argv, " "), "worktree remove") {
			return nil
		}
		if _, err := os.Stat(marker); err != nil {
			return nil
		}
		return &process.NotSettled{PGID: pgid, Reason: "injected: the group could not be confirmed"}
	})
	defer restore()

	var output strings.Builder
	result, err := Verify(context.Background(), fixture.root, fixture.id, &output,
		VerifyOptions{Snapshot: true, Now: func() time.Time { return time.Now().UTC() }})
	if err != nil {
		t.Fatalf("a tidy-up failure was turned into a command failure: %v", err)
	}
	// The engineering result stands exactly as it was recorded.
	if !result.HasEvidence || result.Evidence.Result != work.Pass {
		t.Fatalf("the PASS was disturbed by a cleanup failure: %#v", result)
	}
	if result.Cleanup == nil {
		t.Fatalf("the unconfirmed tidy-up never reached the caller; output was:\n%s", output.String())
	}
	if !errors.Is(result.Cleanup, process.ErrNotSettled) {
		t.Fatalf("Cleanup = %v, want an unsettled report", result.Cleanup)
	}
	worktree := WorktreePath(fixture.root, fixture.id, result.Candidate.Revision)
	var found bool
	for _, unresolved := range result.Unresolved {
		if unresolved.Kind == UnresolvedGit && unresolved.Location == worktree && unresolved.PGID > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("no unresolved execution described the tidy-up for a caller to persist: %#v", result.Unresolved)
	}
}

// The readiness refresh that follows a recorded PASS runs Git against the
// workspace, inside the transaction that wrote the Evidence. A refresh that
// merely fails is a warning — the Evidence stands and a rerun repairs it — but
// one that could not confirm a process group it started is not, and it arrives
// late enough to change what happens to the checkout: the tidy-up that runs
// after it then leaves the worktree in place, because that is where a recovery
// record points. See docs/adr/0022-pending-cleanup-outlives-the-process.md.
func TestAnUnconfirmedGroupInTheReadinessRefreshBlocksAndKeepsTheCheckout(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "check-ran")
	fixture := newVerifyFixture(t, fmt.Sprintf("verify:\n\t@: > %s\n", marker))
	// `git add -A` against the workspace is what the snapshot digest in
	// CandidateFacts runs, and only after the check has announced itself. The
	// removal of the checkout is a different command, so it is left alone.
	restore := process.InjectCleanupFailure(func(pgid int, argv []string, _ time.Duration) error {
		joined := strings.Join(argv, " ")
		if !strings.Contains(joined, "add -A") || strings.Contains(joined, "worktree") {
			return nil
		}
		if _, err := os.Stat(marker); err != nil {
			return nil
		}
		return &process.NotSettled{PGID: pgid, Reason: "injected: the group could not be confirmed"}
	})
	defer restore()

	var output strings.Builder
	result, err := Verify(context.Background(), fixture.root, fixture.id, &output,
		VerifyOptions{Snapshot: true, Now: func() time.Time { return time.Now().UTC() }})
	if err != nil {
		t.Fatalf("a refresh failure was turned into a command failure: %v", err)
	}
	if !result.HasEvidence || result.Evidence.Result != work.Pass {
		t.Fatalf("the PASS was disturbed by a refresh failure: %#v", result)
	}
	if result.Cleanup == nil {
		t.Fatalf("the unconfirmed group never reached the caller; output was:\n%s", output.String())
	}
	if !errors.Is(result.Cleanup, process.ErrNotSettled) {
		t.Fatalf("Cleanup = %v, want an unsettled report", result.Cleanup)
	}
	var found bool
	for _, unresolved := range result.Unresolved {
		if unresolved.Kind == UnresolvedGit && unresolved.Location == fixture.root && unresolved.PGID > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("no unresolved execution named the workspace: %#v", result.Unresolved)
	}
	// The branch this test exists for: it flips the tidy-up that follows it.
	worktree := WorktreePath(fixture.root, fixture.id, result.Candidate.Revision)
	if _, statErr := os.Stat(worktree); statErr != nil {
		t.Fatalf("the checkout a recovery record points at was removed anyway: %v", statErr)
	}
}
