package forgepilot_test

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// These cover the first of the three gaps ticket 07 names: a cancellation that
// reached `make verify` and the runtime probes but stopped at the repository
// boundary, because internal/repository ran Git with no context at all. Git is
// where a managed project's own hooks and filters run, so it is the one place a
// stop could be held indefinitely by code ForgePilot does not own.
// See docs/specs/runner-mvp/issues/07-recovery-hardening.md.

// gitControl are the absolute paths a Git fixture uses to say what it is doing.
// They live outside the repository because a post-checkout hook runs inside a
// detached worktree that is thrown away.
type gitControl struct {
	started string
	pid     string
}

func newGitControl(t *testing.T) gitControl {
	t.Helper()
	directory := t.TempDir()
	return gitControl{
		started: filepath.Join(directory, "git-hook-started"),
		pid:     filepath.Join(directory, "git-hook.pid"),
	}
}

// installBlockingPostCheckout makes `git worktree add` block. It is a real hook
// in a disposable repository, not a stub: the point is that ForgePilot's stop
// path has to reach a process started by Git itself.
func installBlockingPostCheckout(t *testing.T, root string, control gitControl) {
	t.Helper()
	hooks := filepath.Join(root, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0755); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\necho $$ > " + control.pid + "\n: > " + control.started + "\nsleep 300\n"
	write(t, filepath.Join(hooks, "post-checkout"), body)
	if err := os.Chmod(filepath.Join(hooks, "post-checkout"), 0755); err != nil {
		t.Fatal(err)
	}
}

// checkoutStopCase is one way a Runner can be told to stop while Git holds it.
type checkoutStopCase struct {
	name       string
	arguments  []string
	signal     syscall.Signal
	wantExit   int
	wantReason string
}

// A Runner blocked inside `git worktree add` must still answer its stop signal
// and its deadline. Before this the three cases below all hung until the hook
// finished on its own, because the ctx.Err() checks around these calls are
// start gates and say nothing to a process that is already running.
func TestAStopReachesGitWhileItIsCheckingOutTheCandidate(t *testing.T) {
	for _, testCase := range []checkoutStopCase{
		{name: "interrupt", signal: syscall.SIGINT, wantExit: 130, wantReason: "INTERRUPTED",
			arguments: []string{"--verify-timeout", "10m", "--max-duration", "10m"}},
		{name: "termination", signal: syscall.SIGTERM, wantExit: 143, wantReason: "TERMINATED",
			arguments: []string{"--verify-timeout", "10m", "--max-duration", "10m"}},
		{name: "run deadline", wantExit: 3, wantReason: "MAX_DURATION",
			arguments: []string{"--verify-timeout", "10m", "--max-duration", "25s"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newRunnerFixture(t, "a.md")
			control := newGitControl(t)
			check := newCheckControl(t)
			// The canonical check announces itself so the test can assert it never
			// ran: a stop must not be answered by starting the next external process.
			fixture.replaceCanonicalCheck(t, ": > "+check.started+"\nexit 0\n")
			mustRun(t, fixture.binary, fixture.root, "init")
			fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
			installBlockingPostCheckout(t, fixture.root, control)
			agent := fixture.fakeAgent(t, implementsCleanly)

			arguments := append([]string{"run", "--goal", "queue", "--runtime", "fake", "--snapshot"}, testCase.arguments...)
			command, output := fixture.startRun(t, agent, arguments...)
			waitForFile(t, control.started)

			if testCase.signal != 0 {
				if err := command.Process.Signal(testCase.signal); err != nil {
					t.Fatal(err)
				}
			}
			if code := waitForExit(t, command, output, 90*time.Second); code != testCase.wantExit {
				t.Fatalf("exit = %d, want %d\n%s", code, testCase.wantExit, output.String())
			}
			assertProcessGone(t, control.pid, "the post-checkout hook")
			if _, err := os.Stat(check.started); err == nil {
				t.Fatalf("the canonical check was started after the run was told to stop\n%s", output.String())
			}
			runID := lastRun(t, fixture.root)
			if reason := stopReasonOf(t, fixture.root, runID); reason != testCase.wantReason {
				t.Fatalf("stop reason = %s, want %s\n%s", reason, testCase.wantReason, output.String())
			}
			// One story, one session. A stop is not the moment to start the next one.
			if sessions := fixture.sessions(t); len(sessions) != 1 {
				t.Fatalf("sessions = %v, want exactly one", sessions)
			}
		})
	}
}

// installBlockingCleanFilter makes `git add` block, which is what snapshot
// capture does first. A filter is the other half of the same exposure: it runs
// on content rather than on checkout, and it runs against the user's own
// worktree rather than a disposable one — so this is also where a cancellation
// that damaged something would show.
func installBlockingCleanFilter(t *testing.T, root string, control gitControl) {
	t.Helper()
	script := filepath.Join(root, "block-filter.sh")
	write(t, script, "#!/bin/sh\necho $$ > "+control.pid+"\n: > "+control.started+"\nsleep 300\n")
	if err := os.Chmod(script, 0755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, ".gitattributes"), "*.blocked filter=block\n")
	commitAll(t, root, "add a clean filter the snapshot will run into")
	mustRun(t, "git", root, "config", "filter.block.clean", script)
}

// Cancelling a snapshot capture must leave the user's repository exactly as it
// was. Capture runs read-tree, add -A and write-tree against a private index;
// being stopped in the middle of that must not move HEAD, switch branch, touch
// the real index or change a file.
func TestCancellingSnapshotCaptureLeavesTheUsersRepositoryAlone(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	control := newGitControl(t)
	check := newCheckControl(t)
	fixture.replaceCanonicalCheck(t, ": > "+check.started+"\nexit 0\n")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	installBlockingCleanFilter(t, fixture.root, control)

	// A staged change and an untracked file, so "the staging state survived" is a
	// statement about something that was actually there.
	write(t, filepath.Join(fixture.root, "staged.txt"), "staged contents\n")
	mustRun(t, "git", fixture.root, "add", "staged.txt")
	write(t, filepath.Join(fixture.root, "loose.blocked"), "content the filter will be asked about\n")

	headBefore := strings.TrimSpace(gitOutput(t, fixture.root, "rev-parse", "HEAD"))
	branchBefore := strings.TrimSpace(gitOutput(t, fixture.root, "rev-parse", "--abbrev-ref", "HEAD"))
	statusBefore := userVisibleStatus(t, fixture.root)

	agent := fixture.fakeAgent(t, implementsCleanly)
	command, output := fixture.startRun(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot",
		"--verify-timeout", "10m", "--max-duration", "10m")
	waitForFile(t, control.started)

	if err := command.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if code := waitForExit(t, command, output, 90*time.Second); code != 130 {
		t.Fatalf("exit = %d, want 130\n%s", code, output.String())
	}
	assertProcessGone(t, control.pid, "the clean filter")
	if _, err := os.Stat(check.started); err == nil {
		t.Fatal("the canonical check ran although the snapshot was never captured")
	}

	if head := strings.TrimSpace(gitOutput(t, fixture.root, "rev-parse", "HEAD")); head != headBefore {
		t.Fatalf("HEAD moved: %s -> %s", headBefore, head)
	}
	if branch := strings.TrimSpace(gitOutput(t, fixture.root, "rev-parse", "--abbrev-ref", "HEAD")); branch != branchBefore {
		t.Fatalf("branch changed: %s -> %s", branchBefore, branch)
	}
	if status := userVisibleStatus(t, fixture.root); status != statusBefore {
		t.Fatalf("working tree and index changed:\nbefore:\n%s\nafter:\n%s", statusBefore, status)
	}
	if contents := readTestFile(t, filepath.Join(fixture.root, "loose.blocked")); contents != "content the filter will be asked about\n" {
		t.Fatalf("the untracked file was rewritten: %q", contents)
	}
	// Nothing was verified, so nothing may claim to have been.
	if results := evidenceResults(t, fixture.root); countResult(results, work.Pass) != 0 || countResult(results, work.Fail) != 0 {
		t.Fatalf("evidence = %v; a cancelled capture produced an engineering result", results)
	}
}

// userVisibleStatus is the worktree state this test is about: everything except
// the marker the agent session legitimately wrote. A session writing into the
// workspace is the Runner working, not the damage being looked for.
func userVisibleStatus(t *testing.T, root string) string {
	t.Helper()
	var kept []string
	for _, line := range strings.Split(gitOutput(t, root, "status", "--porcelain"), "\n") {
		if strings.TrimSpace(line) == "" || strings.Contains(line, "wi-") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}
