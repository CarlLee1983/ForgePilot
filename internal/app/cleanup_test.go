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
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/process"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// A cancellation and a cleanup that could not be confirmed are two facts that
// arrive together, and only one of them used to survive: the ctx.Err() branch
// returned first, so an unconfirmed process group vanished from the result
// whenever a stop happened to reach the same stage. These drive the real
// orchestration — storage transactions, Git, the runtime preflight, the check —
// with the confirmation step failed by an internal seam, because a group that
// genuinely survives SIGKILL is not something a test may create.
// See docs/adr/0022-pending-cleanup-outlives-the-process.md.

// verifyFixture is a repository app.Verify can actually run against.
type verifyFixture struct {
	root string
	id   string
}

func newVerifyFixture(t *testing.T, makefile string) verifyFixture {
	t.Helper()
	root := t.TempDir()
	// t.TempDir on macOS is under a symlinked /var; resolving it keeps the
	// workspace identity the same one storage records.
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	root = resolved
	runGit(t, root, "init", "--initial-branch=main")
	runGit(t, root, "config", "user.email", "fixture@local")
	runGit(t, root, "config", "user.name", "Fixture")
	writeFixtureFile(t, filepath.Join(root, "Makefile"), makefile)
	writeFixtureFile(t, filepath.Join(root, "specs", "stories", "a.md"), "# story\n")
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-m", "seed")

	if err := storage.Init(root); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	var id string
	if err := storage.Update(root, func(state *work.State) error {
		if err := state.AddGoal("g", "Goal", "", root, now); err != nil {
			return err
		}
		item, err := state.AddWork("g", "specs/stories/a.md", nil, now)
		if err != nil {
			return err
		}
		id = item.ID
		return state.Start(item.ID, now)
	}); err != nil {
		t.Fatal(err)
	}
	return verifyFixture{root: root, id: id}
}

func runGit(t *testing.T, root string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}

func writeFixtureFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}

// cancelledDuring runs a verification whose context is cancelled as soon as the
// fixture says the named stage has started, so the cancellation and the failed
// confirmation genuinely coincide rather than being asserted to.
func cancelledDuring(t *testing.T, fixture verifyFixture, marker string) (VerifyResult, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(marker); err == nil {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	return Verify(ctx, fixture.root, fixture.id, io.Discard,
		VerifyOptions{Snapshot: true, Now: func() time.Time { return time.Now().UTC() }})
}

// failOnceStageStarted fails confirmations only from the moment the fixture
// says the stage under test began. Everything before it — resolving HEAD,
// capturing the snapshot, making the checkout — confirms normally, so the test
// exercises one stage rather than turning the whole command into a failure.
func failOnceStageStarted(marker string) func(int) error {
	return func(pgid int) error {
		if _, err := os.Stat(marker); err != nil {
			return nil
		}
		return &process.NotSettled{PGID: pgid, Reason: "injected: the group could not be confirmed"}
	}
}

func TestACancellationDoesNotSwallowAnUnconfirmedCleanup(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		makefile func(marker string) string
		wantKind string
	}{
		{
			// `make -n verify` expands this at parse time, so the preflight itself
			// is the stage that announces it has started and then blocks.
			name:     "the canonical preflight",
			wantKind: UnresolvedCanonicalPreflight,
			makefile: func(marker string) string {
				return fmt.Sprintf("IGNORED := $(shell : > %s; sleep 300)\nverify:\n\t@true\n", marker)
			},
		},
		{
			// A runtime declaration makes ForgePilot probe for a runtime before the
			// preflight; the probe runs in the checkout, so it is bound the same way.
			name:     "the runtime preflight",
			wantKind: UnresolvedRuntimePreflight,
			makefile: func(marker string) string {
				return fmt.Sprintf("IGNORED := $(shell : > %s; sleep 300)\nverify:\n\t@true\n", marker)
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "stage-started")
			fixture := newVerifyFixture(t, testCase.makefile(marker))
			restore := process.InjectCleanupFailure(failOnceStageStarted(marker))
			defer restore()

			result, err := cancelledDuring(t, fixture, marker)
			if err == nil {
				t.Fatal("a cancelled verification reported success")
			}
			// The half that used to disappear.
			if result.Cleanup == nil {
				t.Fatalf("Cleanup was lost to the cancellation; err = %v", err)
			}
			if !errors.Is(result.Cleanup, process.ErrNotSettled) {
				t.Fatalf("Cleanup = %v, want an unsettled report", result.Cleanup)
			}
			if len(result.Unresolved) == 0 {
				t.Fatal("no unresolved execution was described for a caller to persist")
			}
			// And the half that was already there: why it stopped is still readable.
			if ctxErr := context.Canceled; !errors.Is(err, ctxErr) && !strings.Contains(err.Error(), "interrupted") &&
				!strings.Contains(err.Error(), "context canceled") {
				t.Fatalf("the original interruption is no longer readable from %v", err)
			}
			// No engineering result was invented for a run that produced none.
			if result.HasEvidence && result.Evidence.Result == work.Fail {
				t.Fatalf("a cleanup failure was recorded as an engineering FAIL: %v", result.Evidence)
			}
		})
	}
}

// The worktree is where a recovery record points. Removing it while its
// processes are unconfirmed destroys the information recovery needs, so it is
// deliberately left behind.
func TestAnUnconfirmedCleanupLeavesTheCheckoutInPlace(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "stage-started")
	fixture := newVerifyFixture(t, fmt.Sprintf("IGNORED := $(shell : > %s; sleep 300)\nverify:\n\t@true\n", marker))
	restore := process.InjectCleanupFailure(failOnceStageStarted(marker))
	defer restore()

	result, err := cancelledDuring(t, fixture, marker)
	if err == nil || result.Cleanup == nil {
		t.Fatalf("the fixture did not produce an unconfirmed cleanup: %v / %v", err, result.Cleanup)
	}
	if result.Candidate.Revision == "" {
		t.Fatal("no Candidate was captured, so there is no checkout to assert about")
	}
	worktree := WorktreePath(fixture.root, fixture.id, result.Candidate.Revision)
	if _, statErr := os.Stat(worktree); statErr != nil {
		t.Fatalf("the checkout a recovery record points at was removed: %v", statErr)
	}
	for _, unresolved := range result.Unresolved {
		if unresolved.Location == worktree {
			return
		}
	}
	t.Fatalf("no unresolved execution named the checkout: %v", result.Unresolved)
}
