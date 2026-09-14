package forgepilot_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The Runner reads repository facts between steps — resolving HEAD, digesting
// the working tree to compare a Candidate — and those reads run this
// repository's own filters. A stop that arrived while one was running used to
// come back as a plain error: the loop's decision read reported it as an
// ordinary failure, and start and reconcile reported it as STALLED. Three
// different names for "somebody pressed Ctrl-C", and none of them the one the
// exit code is derived from.
// See docs/adr/0021-execution-limits-are-bounded-and-named.md.

// installCountingCleanFilter installs a clean filter that lets the first
// `after` content reads through and blocks the one after them, so a test can
// say which between-steps read it means rather than taking whichever Git call
// happens to run first. It is a real filter in a real repository: the whole
// exposure is code ForgePilot does not own holding a Git call open.
func installCountingCleanFilter(t *testing.T, root string, control gitControl, after int) {
	t.Helper()
	counter := filepath.Join(t.TempDir(), "filter-count")
	script := filepath.Join(root, "counting-filter.sh")
	write(t, script, fmt.Sprintf(`#!/bin/sh
count=$(cat %[1]s 2>/dev/null || echo 0)
count=$((count + 1))
printf '%%s' "$count" > %[1]s
if [ "$count" -le %[2]d ]; then
  cat
  exit 0
fi
echo $$ > %[3]s
: > %[4]s
sleep 300
`, counter, after, control.pid, control.started))
	if err := os.Chmod(script, 0755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, ".gitattributes"), "*.blocked filter=block\n")
	write(t, filepath.Join(root, "loose.blocked"), "content the filter will be asked about\n")
	commitAll(t, root, "add a counting clean filter")
	mustRun(t, "git", root, "config", "filter.block.clean", script)
}

// A stop that lands in a between-steps facts read is recorded as the stop it
// is, with the exit code that stop is documented to have.
func TestAStopDuringABetweenStepsFactsReadKeepsItsOwnReason(t *testing.T) {
	for _, testCase := range []struct {
		name string
		// skipReads lets the earlier Git content reads through, so the block lands
		// in the named stage rather than in the first snapshot capture.
		skipReads  int
		signal     syscall.Signal
		arguments  []string
		wantExit   int
		wantReason string
	}{
		{name: "interrupt", skipReads: 2, signal: syscall.SIGINT, wantExit: 130, wantReason: "INTERRUPTED",
			arguments: []string{"--verify-timeout", "10m", "--max-duration", "10m"}},
		{name: "termination", skipReads: 2, signal: syscall.SIGTERM, wantExit: 143, wantReason: "TERMINATED",
			arguments: []string{"--verify-timeout", "10m", "--max-duration", "10m"}},
		{name: "run deadline", skipReads: 2, wantExit: 3, wantReason: "MAX_DURATION",
			arguments: []string{"--verify-timeout", "10m", "--max-duration", "40s"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newRunnerFixture(t, "a.md")
			control := newGitControl(t)
			mustRun(t, fixture.binary, fixture.root, "init")
			fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
			installCountingCleanFilter(t, fixture.root, control, testCase.skipReads)
			agent := fixture.fakeAgent(t, implementsCleanly)

			arguments := append([]string{"run", "--goal", "queue", "--runtime", "fake", "--snapshot"}, testCase.arguments...)
			command, output := fixture.startRun(t, agent, arguments...)
			waitForFile(t, control.started)
			sessionsWhenBlocked := len(fixture.sessions(t))

			if testCase.signal != 0 {
				if err := command.Process.Signal(testCase.signal); err != nil {
					t.Fatal(err)
				}
			}
			if code := waitForExit(t, command, output, 120*time.Second); code != testCase.wantExit {
				t.Fatalf("exit = %d, want %d\n%s", code, testCase.wantExit, output.String())
			}
			assertProcessGone(t, control.pid, "the clean filter")

			runID := lastRun(t, fixture.root)
			reason := stopReasonOf(t, fixture.root, runID)
			if reason != testCase.wantReason {
				t.Fatalf("stop reason = %s, want %s\n%s", reason, testCase.wantReason, output.String())
			}
			if reason == "STALLED" {
				t.Fatalf("a stop was reported as a stall\n%s", output.String())
			}
			// A stop is not the moment to start the next piece of work.
			if sessions := fixture.sessions(t); len(sessions) != sessionsWhenBlocked {
				t.Fatalf("a session was started after the run was told to stop: %v", sessions)
			}
			// The read was confirmed clear, so nothing is left blocking the workspace.
			record := loadRunRecord(t, fixture.root, runID)
			if pending, present := record["pending"]; present && pending != nil {
				if entries, ok := pending.([]any); ok && len(entries) != 0 {
					t.Fatalf("a confirmed facts read left a pending execution behind: %v", pending)
				}
			}
		})
	}
}

// An ordinary Git failure during a facts read keeps the operational meaning it
// always had: the stop classification above must not swallow everything.
func TestAnOrdinaryGitFailureDuringAFactsReadIsStillAnOperationalFailure(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	// The same counting filter, but it fails instead of blocking, so the Git call
	// the between-steps facts read makes returns an error that is a failure by no
	// reading — no signal, no deadline, nothing stopped.
	installFailingCleanFilter(t, fixture.root, 2)
	agent := fixture.fakeAgent(t, implementsCleanly)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot",
		"--verify-timeout", "2m", "--max-duration", "2m")
	// Exit 1 is the operational-failure code, asserted rather than "not 130 and
	// not 143": RECOVERY_BLOCKED and STALLED would pass that weaker bar while
	// meaning something quite different.
	if code != 1 {
		t.Fatalf("an ordinary Git failure exited %d, want 1\n%s", code, output)
	}
	for _, forbidden := range []string{"INTERRUPTED", "TERMINATED", "MAX_DURATION", "STALLED", "RECOVERY_BLOCKED"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("an ordinary Git failure was classified as %s\n%s", forbidden, output)
		}
	}
	// And it recorded no stop at all: an operational failure is not an outcome
	// the run reached, so there is nothing for a resume to clear.
	record := loadRunRecord(t, fixture.root, lastRun(t, fixture.root))
	if stop, present := record["stop"]; present && stop != nil {
		t.Fatalf("an ordinary Git failure was recorded as a stop: %v", stop)
	}
}

// installFailingCleanFilter lets the first `after` content reads through and
// fails the one after them.
func installFailingCleanFilter(t *testing.T, root string, after int) {
	t.Helper()
	counter := filepath.Join(t.TempDir(), "filter-count")
	script := filepath.Join(root, "failing-filter.sh")
	write(t, script, fmt.Sprintf(`#!/bin/sh
count=$(cat %[1]s 2>/dev/null || echo 0)
count=$((count + 1))
printf '%%s' "$count" > %[1]s
if [ "$count" -le %[2]d ]; then
  cat
  exit 0
fi
exit 9
`, counter, after))
	if err := os.Chmod(script, 0755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, ".gitattributes"), "*.blocked filter=block\n")
	write(t, filepath.Join(root, "loose.blocked"), "content the filter will be asked about\n")
	commitAll(t, root, "add a failing clean filter")
	mustRun(t, "git", root, "config", "filter.block.clean", script)
	// Without this Git treats a failing clean filter as advisory and stores the
	// content unfiltered, so `git add` still exits zero — which would make this
	// test assert about a run that never failed at all.
	mustRun(t, "git", root, "config", "filter.block.required", "true")
}
