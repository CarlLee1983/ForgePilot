package forgepilot_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/carl/forgepilot/internal/storage"
	"github.com/carl/forgepilot/internal/work"
)

func TestCLIWorkflowAndFailures(t *testing.T) {
	root, binary := fixture(t)
	run := func(want string, arguments ...string) {
		t.Helper()
		output, err := command(binary, root, arguments...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", arguments, err, output)
		}
		if !strings.Contains(output, want) {
			t.Fatalf("%v output %q does not contain %q", arguments, output, want)
		}
	}
	fail := func(arguments ...string) {
		t.Helper()
		if output, err := command(binary, root, arguments...); err == nil {
			t.Fatalf("%v unexpectedly succeeded: %s", arguments, output)
		}
	}
	run("Initialized", "init")
	run("Initialized", "init")
	run("Next: none", "status")
	run("Goal queue created", "goal", "create", "--id", "queue", "--title", "Queue")
	run("WI-001 READY", "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	run("WI-002 PENDING", "work", "add", "--goal", "queue", "--story", "specs/stories/b.md", "--depends-on", "WI-001")
	run("Next: WI-001", "next")
	run("WI-001 RUNNING", "start", "WI-001")
	run("WI-003 READY", "work", "add", "--goal", "queue", "--story", "specs/stories/c.md")
	run("WI-003 RUNNING", "start", "WI-003")
	run("WI-001 RUNNING", "status")
	run("WI-003 RUNNING", "status")
	run("WI-002 PENDING", "status")
	run("No READY work.", "next")
	fail("goal", "create", "--id", "queue", "--title", "Again")
	fail("work", "add", "--goal", "queue", "--story", "specs/stories/missing.md")
	fail("work", "add", "--goal", "queue", "--story", "specs/stories/b.md", "--depends-on", "WI-404")
	fail("start", "WI-001")
	state, err := storage.Load(root)
	if err != nil || len(state.WorkItems) != 3 {
		t.Fatalf("state = %#v, err=%v", state, err)
	}
	if err := os.WriteFile(filepath.Join(root, ".forgepilot", "state.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	fail("status")
}

func TestConcurrentAddsKeepBothItems(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	var group sync.WaitGroup
	errors := make(chan error, 2)
	for _, story := range []string{"specs/stories/a.md", "specs/stories/b.md"} {
		group.Add(1)
		go func(story string) {
			defer group.Done()
			if output, err := command(binary, root, "work", "add", "--goal", "queue", "--story", story); err != nil {
				errors <- &commandError{err, output}
			}
		}(story)
	}
	group.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.WorkItems) != 2 || state.WorkItems[0].ID == state.WorkItems[1].ID {
		t.Fatalf("items = %#v", state.WorkItems)
	}
}

type commandError struct {
	err    error
	output string
}

func (e *commandError) Error() string { return e.err.Error() + ": " + e.output }

func fixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	if output, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	stories := filepath.Join(root, "specs", "stories")
	if err := os.MkdirAll(stories, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		if err := os.WriteFile(filepath.Join(stories, name), []byte("# story\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".forgepilot/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "forgepilot")
	build := exec.Command("go", "build", "-o", binary, "./cmd/forgepilot")
	build.Dir = projectRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, output)
	}
	return root, binary
}

func projectRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return directory
}

func command(binary, directory string, arguments ...string) (string, error) {
	command := exec.Command(binary, arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	return string(output), err
}

func mustRun(t *testing.T, binary, directory string, arguments ...string) {
	t.Helper()
	if output, err := command(binary, directory, arguments...); err != nil {
		t.Fatalf("%v: %v: %s", arguments, err, output)
	}
}

func TestMigrateCommandUpgradesLegacyState(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")

	statePath := filepath.Join(root, ".forgepilot", "state.json")
	current, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	// Rewind the snapshot to the shape M1 wrote: no v2 fields, schema version 1.
	legacy := strings.NewReplacer(
		`"schema_version": 2`, `"schema_version": 1`,
		`"next_evidence_id": 1,`, "",
		`"current_run": null,`, "",
	).Replace(string(current))
	if strings.Contains(legacy, "next_evidence_id") || strings.Contains(legacy, "current_run") {
		t.Fatalf("failed to build a v1 snapshot from %s", current)
	}
	legacy = strings.Replace(legacy, `,
  "evidence": []`, "", 1)
	if err := os.WriteFile(statePath, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}

	output, err := command(binary, root, "status")
	if err == nil {
		t.Fatalf("status read a v1 state: %s", output)
	}
	if !strings.Contains(output, "migrate") {
		t.Fatalf("status error %q does not tell the user to migrate", output)
	}

	if output, err := command(binary, root, "migrate"); err != nil || !strings.Contains(output, "Migrated") {
		t.Fatalf("migrate = %q, %v", output, err)
	}
	if output, err := command(binary, root, "status"); err != nil || !strings.Contains(output, "WI-001") {
		t.Fatalf("status after migrate = %q, %v", output, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".forgepilot", "state.json.v1.bak")); err != nil {
		t.Fatalf("no backup after migrate: %v", err)
	}
	output, err = command(binary, root, "migrate")
	if err != nil || !strings.Contains(output, "already") {
		t.Fatalf("second migrate = %q, %v", output, err)
	}
}

// passingVerify and failingVerify are canonical checks for the managed project:
// ForgePilot runs whatever `make verify` the repository defines, so the fixture
// controls the outcome by controlling that target.
const passingVerify = "verify:\n\t@echo checked\n"
const failingVerify = "verify:\n\t@echo broken >&2; exit 3\n"

// writeVerify replaces the fixture's canonical check and commits it, returning
// the resulting commit SHA.
func writeVerify(t *testing.T, root, recipe string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte(recipe), 0644); err != nil {
		t.Fatal(err)
	}
	return commitAll(t, root, "set canonical check")
}

func commitAll(t *testing.T, root, message string) string {
	t.Helper()
	git := func(arguments ...string) string {
		t.Helper()
		arguments = append([]string{"-C", root, "-c", "user.email=fixture@example.com", "-c", "user.name=Fixture"}, arguments...)
		output, err := exec.Command("git", arguments...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", arguments, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("add", "-A")
	git("commit", "-q", "-m", message)
	return git("rev-parse", "HEAD")
}

func TestVerifyRecordsEvidenceAgainstTheCommittedRevision(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	mustRun(t, binary, root, "start", "WI-001")

	// A committed revision that defines no canonical check: that is not a
	// failure, it is nothing to verify.
	commitAll(t, root, "stories without a canonical check")
	output, err := command(binary, root, "verify", "WI-001")
	if err == nil {
		t.Fatalf("verified a project with no canonical check: %s", output)
	}
	if !strings.Contains(output, "make verify") {
		t.Fatalf("error %q does not name the missing canonical check", output)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Evidence) != 0 {
		t.Fatalf("a refused verification left evidence: %#v", state.Evidence)
	}

	revision := writeVerify(t, root, passingVerify)
	if output, err := command(binary, root, "verify", "WI-001"); err != nil || !strings.Contains(output, "PASS") {
		t.Fatalf("verify = %q, %v", output, err)
	}
	state, err = storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Evidence) != 1 {
		t.Fatalf("evidence = %#v", state.Evidence)
	}
	if got := state.Evidence[0]; got.Result != work.Pass || got.Revision != revision || got.ExitCode == nil || *got.ExitCode != 0 {
		t.Fatalf("evidence = %#v, want PASS at %s", got, revision)
	}
	if state.WorkItems[0].Status != work.Review {
		t.Fatalf("status after PASS = %s, want REVIEW", state.WorkItems[0].Status)
	}

	// A dirty worktree cannot be verified: the commit would not describe it.
	if err := os.WriteFile(filepath.Join(root, "stray.txt"), []byte("uncommitted\n"), 0644); err != nil {
		t.Fatal(err)
	}
	output, err = command(binary, root, "verify", "WI-001")
	if err == nil {
		t.Fatalf("verified a dirty worktree: %s", output)
	}
	if !strings.Contains(output, "clean") {
		t.Fatalf("error %q does not explain the worktree is not clean", output)
	}
	if err := os.Remove(filepath.Join(root, "stray.txt")); err != nil {
		t.Fatal(err)
	}

	failed := writeVerify(t, root, failingVerify)
	if output, err := command(binary, root, "verify", "WI-001"); err != nil || !strings.Contains(output, "FAIL") {
		t.Fatalf("verify = %q, %v", output, err)
	}
	state, err = storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Evidence) != 2 {
		t.Fatalf("evidence = %#v", state.Evidence)
	}
	// `make` reports its own exit code 2 when a recipe fails, not the recipe's.
	// Evidence records the exit code of the command ForgePilot actually ran.
	if got := state.Evidence[1]; got.Result != work.Fail || got.Revision != failed || got.ExitCode == nil || *got.ExitCode != 2 {
		t.Fatalf("evidence = %#v, want FAIL at %s", got, failed)
	}
	if state.Evidence[0].Result != work.Pass {
		t.Fatal("the earlier PASS evidence was overwritten")
	}
	if state.WorkItems[0].Status != work.Running {
		t.Fatalf("status after FAIL = %s, want RUNNING", state.WorkItems[0].Status)
	}
	if state.WorkItems[0].CurrentRun != nil {
		t.Fatal("a finished run was left in flight")
	}
	if entries, err := os.ReadDir(filepath.Join(root, ".forgepilot", "worktrees")); err == nil && len(entries) != 0 {
		t.Fatalf("verification worktrees were left behind: %v", entries)
	}
}

// TestVerifyRunsOutsideTheMainWorktree proves the canonical check does not see
// the main worktree's contents. The probe file is gitignored, so the worktree
// stays clean and verification is allowed, yet a fresh checkout of the commit
// cannot contain it: if the check passes, it did not run in the main tree.
func TestVerifyRunsOutsideTheMainWorktree(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	mustRun(t, binary, root, "start", "WI-001")

	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".forgepilot/\nprobe.txt\n"), 0644); err != nil {
		t.Fatal(err)
	}
	writeVerify(t, root, "verify:\n\t@test ! -e probe.txt || (echo 'ran in the main worktree' >&2; exit 1)\n")
	if err := os.WriteFile(filepath.Join(root, "probe.txt"), []byte("main worktree only\n"), 0644); err != nil {
		t.Fatal(err)
	}

	output, err := command(binary, root, "verify", "WI-001")
	if err != nil || !strings.Contains(output, "PASS") {
		t.Fatalf("verify = %q, %v", output, err)
	}
	if _, err := os.Stat(filepath.Join(root, "probe.txt")); err != nil {
		t.Fatalf("verification disturbed the main worktree: %v", err)
	}
}

// startVerify launches a verification in the background and waits until state
// shows the run in flight, so the test can observe or interrupt it.
func startVerify(t *testing.T, binary, root, id string) *exec.Cmd {
	t.Helper()
	running := exec.Command(binary, "verify", id)
	running.Dir = root
	if err := running.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		state, err := storage.Load(root)
		if err == nil {
			for _, item := range state.WorkItems {
				if item.ID == id && item.CurrentRun != nil && item.Status == work.Verifying {
					return running
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = running.Process.Kill()
	t.Fatalf("%s never entered VERIFYING", id)
	return nil
}

func TestVerifyIsVisibleConcurrentAndRecoversFromInterruption(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/b.md")
	mustRun(t, binary, root, "start", "WI-001")
	mustRun(t, binary, root, "start", "WI-002")
	// Long enough that the test controls when the run ends, never the clock.
	slow := writeVerify(t, root, "verify:\n\t@sleep 30\n")

	running := startVerify(t, binary, root, "WI-001")

	// Queries stay available while a verification holds no state lock.
	output, err := command(binary, root, "status")
	if err != nil || !strings.Contains(output, "WI-001 VERIFYING") {
		t.Fatalf("status during verification = %q, %v", output, err)
	}
	if output, err := command(binary, root, "next"); err != nil {
		t.Fatalf("next during verification = %q, %v", output, err)
	}

	// The same Work Item cannot be verified twice at once.
	if output, err := command(binary, root, "verify", "WI-001"); err == nil {
		t.Fatalf("verified WI-001 twice concurrently: %s", output)
	}
	// A different Work Item can be: reaching VERIFYING while WI-001 is still in
	// flight is the claim; the lock is per Work Item, not global.
	second := startVerify(t, binary, root, "WI-002")
	if err := second.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = second.Wait()

	// Interrupt the first run: the OS releases its lock, leaving an orphan.
	if err := running.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = running.Wait()
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if state.WorkItems[0].Status != work.Verifying || state.WorkItems[0].CurrentRun == nil {
		t.Fatalf("killed run did not leave an orphan: %#v", state.WorkItems[0])
	}
	if output, err := command(binary, root, "status"); err != nil || !strings.Contains(output, "runner is gone") {
		t.Fatalf("status did not report the orphan: %q, %v", output, err)
	}
	before, err := os.ReadFile(filepath.Join(root, ".forgepilot", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	mustRun(t, binary, root, "status")
	mustRun(t, binary, root, "next")
	after, err := os.ReadFile(filepath.Join(root, ".forgepilot", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("a query reclaimed the orphan; only verify may write")
	}

	writeVerify(t, root, passingVerify)
	if output, err := command(binary, root, "verify", "WI-001"); err != nil || !strings.Contains(output, "PASS") {
		t.Fatalf("verify after interruption = %q, %v", output, err)
	}
	state, err = storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	var interrupted *work.Evidence
	for i, record := range state.Evidence {
		if record.Result == work.Interrupted {
			interrupted = &state.Evidence[i]
		}
		if record.WorkItemID == "WI-001" && record.Result == work.Fail {
			t.Fatalf("an interruption was recorded as a failure: %#v", record)
		}
	}
	if interrupted == nil {
		t.Fatalf("no INTERRUPTED evidence after a killed run: %#v", state.Evidence)
	}
	if interrupted.Revision != slow {
		t.Fatalf("INTERRUPTED evidence carries %q, want the killed run's revision %q", interrupted.Revision, slow)
	}
	if state.WorkItems[0].Status != work.Review || state.WorkItems[0].CurrentRun != nil {
		t.Fatalf("WI-001 = %#v after recovery", state.WorkItems[0])
	}
}

func TestStatusReportsEvidenceAndStaleness(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	mustRun(t, binary, root, "start", "WI-001")
	writeVerify(t, root, passingVerify)

	output, err := command(binary, root, "status")
	if err != nil || !strings.Contains(output, "not verified") {
		t.Fatalf("status before any verification = %q, %v", output, err)
	}
	if strings.Contains(output, "PASS") {
		t.Fatalf("unverified work reported as passing: %q", output)
	}

	mustRun(t, binary, root, "verify", "WI-001")
	output, err = command(binary, root, "status")
	if err != nil || !strings.Contains(output, "PASS") {
		t.Fatalf("status after PASS = %q, %v", output, err)
	}
	if strings.Contains(output, "stale") {
		t.Fatalf("evidence for the current revision marked stale: %q", output)
	}

	// A new commit does not change any status, but the PASS no longer applies.
	if err := os.WriteFile(filepath.Join(root, "specs", "stories", "a.md"), []byte("# story revised\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, ".forgepilot", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	commitAll(t, root, "revise story")
	output, err = command(binary, root, "status")
	if err != nil || !strings.Contains(output, "stale") {
		t.Fatalf("status after a new commit = %q, %v", output, err)
	}
	if !strings.Contains(output, "WI-001 REVIEW") {
		t.Fatalf("a new commit changed the work item's status: %q", output)
	}
	after, err := os.ReadFile(filepath.Join(root, ".forgepilot", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("status wrote to state while reporting staleness")
	}

	// REVIEW work can be verified again to obtain evidence that does apply.
	if output, err := command(binary, root, "verify", "WI-001"); err != nil || !strings.Contains(output, "PASS") {
		t.Fatalf("re-verify of REVIEW work = %q, %v", output, err)
	}
	if output, err := command(binary, root, "status"); err != nil || strings.Contains(output, "stale") {
		t.Fatalf("status after re-verification = %q, %v", output, err)
	}
	// Re-running against an unchanged revision is allowed and only accumulates.
	mustRun(t, binary, root, "verify", "WI-001")
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Evidence) != 3 {
		t.Fatalf("evidence = %#v, want three accumulated records", state.Evidence)
	}
}

// TestVerifyRefusesWhenTheRevisionHasNoCanonicalCheck covers the gap between the
// worktree the preconditions inspect and the checkout the run happens in: a
// gitignored Makefile leaves the main worktree clean and looking verifiable while
// the committed revision has no canonical check at all. That is not a failure.
func TestVerifyRefusesWhenTheRevisionHasNoCanonicalCheck(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	mustRun(t, binary, root, "start", "WI-001")

	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".forgepilot/\nMakefile\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte(passingVerify), 0644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, root, "ignore the makefile")

	output, err := command(binary, root, "verify", "WI-001")
	if err == nil {
		t.Fatalf("verified a revision with no canonical check: %s", output)
	}
	if strings.Contains(output, "FAIL") {
		t.Fatalf("a project that cannot be verified was reported as failing: %q", output)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Evidence) != 0 {
		t.Fatalf("a refused verification left evidence: %#v", state.Evidence)
	}
	if state.WorkItems[0].Status != work.Running || state.WorkItems[0].CurrentRun != nil {
		t.Fatalf("a refused verification moved the work item: %#v", state.WorkItems[0])
	}
}

// TestRefusedVerifyLeavesStateUntouched pins the ordering the spec requires:
// preconditions are checked before anything is written, so a command the user
// sees fail has not quietly reclaimed an orphan or moved the work item.
func TestRefusedVerifyLeavesStateUntouched(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	mustRun(t, binary, root, "start", "WI-001")
	writeVerify(t, root, "verify:\n\t@sleep 30\n")

	running := startVerify(t, binary, root, "WI-001")
	if err := running.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = running.Wait()

	// Dirty the worktree so the next verify must be refused.
	if err := os.WriteFile(filepath.Join(root, "stray.txt"), []byte("uncommitted\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, ".forgepilot", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	output, err := command(binary, root, "verify", "WI-001")
	if err == nil {
		t.Fatalf("verified a dirty worktree: %s", output)
	}
	after, err := os.ReadFile(filepath.Join(root, ".forgepilot", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a refused verification wrote to state:\nbefore %s\nafter  %s", before, after)
	}
}

// TestInterruptedEvidenceHasNoExitCode: an interrupted run produced no result, so
// it has no exit code. Recording a zero there would read as success to anything
// that treats exit code zero as passing.
func TestInterruptedEvidenceHasNoExitCode(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	mustRun(t, binary, root, "start", "WI-001")
	writeVerify(t, root, "verify:\n\t@sleep 30\n")

	running := startVerify(t, binary, root, "WI-001")
	if err := running.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = running.Wait()
	writeVerify(t, root, passingVerify)
	mustRun(t, binary, root, "verify", "WI-001")

	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range state.Evidence {
		if record.Result == work.Interrupted && record.ExitCode != nil {
			t.Fatalf("INTERRUPTED evidence carries exit code %d: %#v", *record.ExitCode, record)
		}
		if record.Result == work.Pass && (record.ExitCode == nil || *record.ExitCode != 0) {
			t.Fatalf("PASS evidence lost its exit code: %#v", record)
		}
	}
}

// TestVerifyRecoversFromLeftoverWorktrees: a killed run leaves both the directory
// and git's registration behind. The next verify must clear them rather than fail
// on git's "missing but already registered" error.
func TestVerifyRecoversFromLeftoverWorktrees(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	mustRun(t, binary, root, "start", "WI-001")
	revision := writeVerify(t, root, passingVerify)

	// Reproduce both leftovers: a live registration whose directory is intact,
	// and a stale registration whose directory has been removed by hand.
	occupied := filepath.Join(root, ".forgepilot", "worktrees", "WI-001-"+revision[:12])
	if output, err := exec.Command("git", "-C", root, "worktree", "add", "--detach", occupied, revision).CombinedOutput(); err != nil {
		t.Fatalf("seed worktree: %v: %s", err, output)
	}
	orphanedRegistration := filepath.Join(root, ".forgepilot", "worktrees", "stale")
	if output, err := exec.Command("git", "-C", root, "worktree", "add", "--detach", orphanedRegistration, revision).CombinedOutput(); err != nil {
		t.Fatalf("seed stale worktree: %v: %s", err, output)
	}
	if err := os.RemoveAll(orphanedRegistration); err != nil {
		t.Fatal(err)
	}

	if output, err := command(binary, root, "verify", "WI-001"); err != nil || !strings.Contains(output, "PASS") {
		t.Fatalf("verify with leftover worktrees = %q, %v", output, err)
	}
	output, err := exec.Command("git", "-C", root, "worktree", "list").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output), ".forgepilot") {
		t.Fatalf("verification worktrees remain registered: %s", output)
	}
}
