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
	"syscall"
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

// stageFixture is one cleanup stage this test can drive on its own: what the
// repository must contain for the stage to run, and how to recognise the
// managed command that stage starts.
type stageFixture struct {
	// makefile is the checkout's canonical check.
	makefile string
	// declaration, when set, is a .tool-versions the runtime preflight must
	// satisfy — which is what makes that stage run at all.
	declaration string
	// shim, when set, is a fake runtime executable put first on PATH.
	shim bool
	// isStage recognises the managed command the stage under test starts, so the
	// confirmation fails for that one and for nothing else. Two subtests that
	// share a fixture and differ only in name prove nothing, which is how this
	// test previously ran the same path twice.
	isStage func(argv []string) bool
}

// blockingShellAssignment blocks inside `make -n verify` itself: a `$(shell …)`
// is expanded while the makefile is parsed, so the preflight is the stage that
// announces it has started and then waits.
func blockingShellAssignment(marker string) string {
	return fmt.Sprintf("IGNORED := $(shell : > %s; sleep 300)\nverify:\n\t@true\n", marker)
}

// failStage fails the confirmation of exactly the stage under test, and records
// every group id it was asked about so the test can make sure it left nothing
// of its own behind.
func failStage(t *testing.T, stage stageFixture) func() {
	t.Helper()
	var groups []int
	var mutex sync.Mutex
	restore := process.InjectCleanupFailure(func(pgid int, argv []string, _ time.Duration) error {
		if !stage.isStage(argv) {
			return nil
		}
		mutex.Lock()
		groups = append(groups, pgid)
		mutex.Unlock()
		return &process.NotSettled{PGID: pgid, Reason: "injected: the group could not be confirmed"}
	})
	t.Cleanup(func() {
		// Only groups this test's own fixture started, named by the seam that
		// refused to confirm them. Nothing else is signalled.
		mutex.Lock()
		defer mutex.Unlock()
		for _, pgid := range groups {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
	})
	return restore
}

// installShim puts a fake runtime first on PATH. It answers --version by
// announcing that the probe has started and then blocking, which is what lets a
// cancellation land inside the runtime preflight specifically.
func installShim(t *testing.T, marker string) {
	t.Helper()
	directory := t.TempDir()
	writeFixtureFile(t, filepath.Join(directory, "node"),
		fmt.Sprintf("#!/bin/sh\n: > %s\nsleep 300\n", marker))
	if err := os.Chmod(filepath.Join(directory, "node"), 0755); err != nil {
		t.Fatal(err)
	}
	// The managers are pointed at empty directories so the only candidate left
	// is the one on PATH: this test is about a stage, not about whatever node
	// installations this machine happens to have.
	for _, variable := range []string{"NVM_DIR", "MISE_DATA_DIR", "ASDF_DATA_DIR"} {
		t.Setenv(variable, t.TempDir())
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestACancellationDoesNotSwallowAnUnconfirmedCleanup(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		wantKind string
		stage    func(marker string) stageFixture
	}{
		{
			name:     "the canonical preflight",
			wantKind: UnresolvedCanonicalPreflight,
			stage: func(marker string) stageFixture {
				return stageFixture{
					makefile: blockingShellAssignment(marker),
					// `make -n verify`, and not the check itself, which is `make verify`.
					isStage: func(argv []string) bool {
						return len(argv) > 1 && filepath.Base(argv[0]) == "make" && argv[1] == "-n"
					},
				}
			},
		},
		{
			name:     "the runtime preflight",
			wantKind: UnresolvedRuntimePreflight,
			stage: func(marker string) stageFixture {
				return stageFixture{
					// Nothing blocks in the makefile: the probe the declaration forces is
					// the only thing that does, so a cancellation can only land there.
					makefile: "verify:\n\t@true\n",
					// A version no real runtime can satisfy, so the probe reaches the
					// shim rather than matching something already installed.
					declaration: "node 0.0.0-forgepilot-test\n",
					shim:        true,
					isStage: func(argv []string) bool {
						return len(argv) > 0 && filepath.Base(argv[0]) == "node"
					},
				}
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "stage-started")
			stage := testCase.stage(marker)
			if stage.shim {
				installShim(t, marker)
			}
			fixture := newVerifyFixture(t, stage.makefile)
			if stage.declaration != "" {
				writeFixtureFile(t, filepath.Join(fixture.root, ".tool-versions"), stage.declaration)
				runGit(t, fixture.root, "add", "-A")
				runGit(t, fixture.root, "commit", "-m", "declare a runtime")
			}
			restore := failStage(t, stage)
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
			// The stage is asserted, not only named: two subtests that run the same
			// path and differ in their titles are one test with two names.
			var found *Unresolved
			for index := range result.Unresolved {
				if result.Unresolved[index].Kind == testCase.wantKind {
					found = &result.Unresolved[index]
					break
				}
			}
			if found == nil {
				t.Fatalf("no unresolved execution came from %s: %#v", testCase.wantKind, result.Unresolved)
			}
			if found.PGID <= 0 {
				t.Fatalf("the unresolved execution names no process group: %#v", *found)
			}
			worktree := WorktreePath(fixture.root, fixture.id, result.Candidate.Revision)
			if found.Location != worktree {
				t.Fatalf("the unresolved execution points at %q, not the checkout %q", found.Location, worktree)
			}
			// And the half that was already there: why it stopped is still readable.
			if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "interrupted") &&
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
	stage := stageFixture{makefile: blockingShellAssignment(marker),
		isStage: func(argv []string) bool {
			return len(argv) > 1 && filepath.Base(argv[0]) == "make" && argv[1] == "-n"
		}}
	fixture := newVerifyFixture(t, stage.makefile)
	restore := failStage(t, stage)
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
