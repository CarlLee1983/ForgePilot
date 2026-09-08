package forgepilot_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
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
	failWith := func(want string, arguments ...string) {
		t.Helper()
		output, err := command(binary, root, arguments...)
		if err == nil {
			t.Fatalf("%v unexpectedly succeeded: %s", arguments, output)
		}
		if !strings.Contains(output, want) {
			t.Fatalf("%v output %q does not contain %q", arguments, output, want)
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
	failWith("story reference must be located under specs/stories", "work", "add", "--goal", "queue", "--story", "specs/stories/missing.md")
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

func TestWorkAddWithoutStoriesDirectoryNamesTheMissingDirectory(t *testing.T) {
	root, binary := fixtureWithoutStories(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	output, err := command(binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	if err == nil {
		t.Fatalf("unexpectedly succeeded: %s", output)
	}
	if !strings.Contains(output, "specs/stories does not exist") || !strings.Contains(output, "ForgePilot expects ForgeFlow Story files") {
		t.Fatalf("output %q does not name the missing directory", output)
	}
	if strings.Contains(output, "lstat") {
		t.Fatalf("output %q leaks an internal call name", output)
	}
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

// fixtureIdentity is the Git-configured identity a fixture repository carries,
// so tests can assert on the default self-asserted decision maker.
const fixtureIdentity = "fixture@example.com"

func fixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	if output, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	// Configure an identity in the repository itself so that the default decision
	// maker is the fixture's, not whatever the machine running the test happens
	// to have configured globally.
	if output, err := exec.Command("git", "-C", root, "config", "user.email", fixtureIdentity).CombinedOutput(); err != nil {
		t.Fatalf("git config: %v: %s", err, output)
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

// fixtureWithoutStories is fixture minus specs/stories: a repository that has
// never adopted ForgeFlow, which is the one shape none of the other fixtures
// exercise since they all pre-create the directory.
func fixtureWithoutStories(t *testing.T) (string, string) {
	t.Helper()
	root, binary := fixture(t)
	if err := os.RemoveAll(filepath.Join(root, "specs")); err != nil {
		t.Fatal(err)
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

// rewindToV1 rewrites a current snapshot into the shape M1 wrote: schema version
// 1, with every field a later version introduced removed. It works on the
// decoded document rather than the encoded text so that adding a field to the
// current schema cannot silently turn this into a no-op.
func rewindToV1(t *testing.T, path string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(contents, &snapshot); err != nil {
		t.Fatal(err)
	}
	v1Fields := map[string]bool{"schema_version": true, "next_work_id": true, "goals": true, "work_items": true}
	for field := range snapshot {
		if !v1Fields[field] {
			delete(snapshot, field)
		}
	}
	snapshot["schema_version"] = 1
	v1ItemFields := map[string]bool{"id": true, "goal_id": true, "story_ref": true, "status": true,
		"depends_on": true, "created_at": true, "updated_at": true}
	items, ok := snapshot["work_items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("snapshot has no work items to rewind: %s", contents)
	}
	for _, entry := range items {
		item, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("unexpected work item shape in %s", contents)
		}
		for field := range item {
			if !v1ItemFields[field] {
				delete(item, field)
			}
		}
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateCommandUpgradesLegacyState(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")

	statePath := filepath.Join(root, ".forgepilot", "state.json")
	rewindToV1(t, statePath)

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
	if !strings.Contains(output, "verifying") {
		t.Fatalf("error %q does not say verifying is what was refused", output)
	}
	if !strings.Contains(output, "stray.txt") {
		t.Fatalf("error %q does not list the file that made the worktree dirty", output)
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
// TestRefusedVerifyWritesOnlyTheRunThatEnded pins how much a refused verify is
// allowed to write. Reclaiming an abandoned run is a recording of something that
// already happened and survives the refusal; everything else a verify does is
// starting a new run, and none of that may happen.
func TestRefusedVerifyWritesOnlyTheRunThatEnded(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	mustRun(t, binary, root, "start", "WI-001")
	slow := writeVerify(t, root, "verify:\n\t@sleep 30\n")

	running := startVerify(t, binary, root, "WI-001")
	if err := running.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = running.Wait()

	// Dirty the worktree so the next verify must be refused.
	if err := os.WriteFile(filepath.Join(root, "stray.txt"), []byte("uncommitted\n"), 0644); err != nil {
		t.Fatal(err)
	}
	output, err := command(binary, root, "verify", "WI-001")
	if err == nil {
		t.Fatalf("verified a dirty worktree: %s", output)
	}
	if !strings.Contains(output, "clean") {
		t.Fatalf("error %q does not explain the worktree is not clean", output)
	}
	if !strings.Contains(output, "verifying") {
		t.Fatalf("error %q does not say verifying is what was refused", output)
	}

	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Evidence) != 1 {
		t.Fatalf("evidence = %#v, want only the run that ended", state.Evidence)
	}
	if got := state.Evidence[0]; got.Result != work.Interrupted || got.Revision != slow {
		t.Fatalf("evidence = %#v, want INTERRUPTED at %s", got, slow)
	}
	// No new run was started: the refusal held for everything except the record.
	if state.WorkItemStatus("WI-001") != work.Running || state.WorkItems[0].CurrentRun != nil {
		t.Fatalf("WI-001 = %#v, want RUNNING with no run in flight", state.WorkItems[0])
	}
	if entries, err := os.ReadDir(filepath.Join(root, ".forgepilot", "worktrees")); err == nil && len(entries) != 0 {
		t.Fatalf("a refused verification left worktrees behind: %v", entries)
	}

	// With nothing left to reclaim, a refused verify writes nothing at all.
	before, err := os.ReadFile(filepath.Join(root, ".forgepilot", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if output, err := command(binary, root, "verify", "WI-001"); err == nil {
		t.Fatalf("verified a dirty worktree: %s", output)
	}
	after, err := os.ReadFile(filepath.Join(root, ".forgepilot", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a refused verification with nothing to reclaim wrote to state:\nbefore %s\nafter  %s", before, after)
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

// TestInterruptedRunLeavesATruncatedLogThatReclaimReports covers M5-5: a killed
// run's log is not merely present, it holds what the run produced before it
// died, and the next verify's reclaim reports exactly that path — the one
// state recorded on current_run, not one re-derived from today's naming
// scheme. See docs/adr/0012-verification-log-outside-state.md.
func TestInterruptedRunLeavesATruncatedLogThatReclaimReports(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	mustRun(t, binary, root, "start", "WI-001")
	// Produces output before the kill lands, so the log can be checked for it.
	writeVerify(t, root, "verify:\n\t@echo working before the cut\n\t@sleep 30\n")

	running := startVerify(t, binary, root, "WI-001")

	// Give the recipe's first line a moment to actually reach the log before
	// the process is killed; startVerify only waits for VERIFYING, not for
	// output to have been written.
	deadline := time.Now().Add(5 * time.Second)
	var logPath string
	for time.Now().Before(deadline) {
		state, err := storage.Load(root)
		if err != nil {
			t.Fatal(err)
		}
		logPath = state.WorkItems[0].CurrentRun.LogPath
		if logPath == "" {
			t.Fatalf("current_run has no LogPath: %#v", state.WorkItems[0].CurrentRun)
		}
		content, err := os.ReadFile(logPath)
		if err == nil && strings.Contains(string(content), "working before the cut") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := running.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = running.Wait()

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("interrupted run left no log at %s: %v", logPath, err)
	}
	if !strings.Contains(string(content), "working before the cut") {
		t.Fatalf("log = %q, want the output produced before the kill", content)
	}

	writeVerify(t, root, passingVerify)
	output, err := command(binary, root, "verify", "WI-001")
	if err != nil || !strings.Contains(output, "PASS") {
		t.Fatalf("verify after interruption = %q, %v", output, err)
	}
	if !strings.Contains(output, "INTERRUPTED") {
		t.Fatalf("output %q does not report the reclaimed run", output)
	}
	if !strings.Contains(output, logPath) {
		t.Fatalf("output %q does not report the interrupted run's own log path %q", output, logPath)
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

// logPathFromOutput extracts the log path verify prints before it starts the
// canonical check, so a test can go read what actually ended up in it.
func logPathFromOutput(t *testing.T, output string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "Log: ") {
			return strings.TrimPrefix(line, "Log: ")
		}
	}
	t.Fatalf("output %q does not print a log path", output)
	return ""
}

// TestVerifyStreamsCanonicalOutputToALog covers the acceptance criteria that
// PASS and FAIL each leave behind a log containing that run's actual output,
// the path is printed before the run starts, non-PASS no longer floods stdout
// with the full text, the log carries no ForgePilot-added header, and the log
// living under the already-ignored .forgepilot/ does not dirty `git status`.
func TestVerifyStreamsCanonicalOutputToALog(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	mustRun(t, binary, root, "start", "WI-001")

	writeVerify(t, root, passingVerify)
	output, err := command(binary, root, "verify", "WI-001")
	if err != nil || !strings.Contains(output, "PASS") {
		t.Fatalf("verify = %q, %v", output, err)
	}
	passLog := logPathFromOutput(t, output)
	if !strings.HasPrefix(output, "Log: "+passLog+"\n") {
		t.Fatalf("output %q does not print the log path before the run starts", output)
	}
	if strings.Count(output, passLog) != 1 {
		t.Fatalf("output %q repeats the log path in the result line", output)
	}
	contents, err := os.ReadFile(passLog)
	if err != nil {
		t.Fatalf("read PASS log: %v", err)
	}
	if !strings.Contains(string(contents), "checked") {
		t.Fatalf("PASS log %q does not contain the check's output: %q", passLog, contents)
	}

	writeVerify(t, root, failingVerify)
	output, err = command(binary, root, "verify", "WI-001")
	if err != nil || !strings.Contains(output, "FAIL") {
		t.Fatalf("verify = %q, %v", output, err)
	}
	failLog := logPathFromOutput(t, output)
	if failLog == passLog {
		t.Fatalf("FAIL run reused the PASS run's log path %q", failLog)
	}
	if strings.Contains(output, "broken") {
		t.Fatalf("stdout %q still carries the failing step's output", output)
	}
	contents, err = os.ReadFile(failLog)
	if err != nil {
		t.Fatalf("read FAIL log: %v", err)
	}
	if !strings.Contains(string(contents), "broken") {
		t.Fatalf("FAIL log %q does not contain the failing step's output: %q", failLog, contents)
	}
	if strings.Contains(string(contents), "ForgePilot") {
		t.Fatalf("log %q carries a ForgePilot-added header: %q", failLog, contents)
	}

	gitStatus, err := exec.Command("git", "-C", root, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(gitStatus)) != "" {
		t.Fatalf("verification logs dirtied git status: %s", gitStatus)
	}
}

// TestVerifyLogsAccumulateAcrossRepeatedRuns covers repeated runs against the
// same revision producing separate, non-overwriting logs, and that no
// automatic cleanup removes a log a later run did not itself write.
func TestVerifyLogsAccumulateAcrossRepeatedRuns(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	mustRun(t, binary, root, "start", "WI-001")
	writeVerify(t, root, passingVerify)

	first, err := command(binary, root, "verify", "WI-001")
	if err != nil || !strings.Contains(first, "PASS") {
		t.Fatalf("verify = %q, %v", first, err)
	}
	firstLog := logPathFromOutput(t, first)

	second, err := command(binary, root, "verify", "WI-001")
	if err != nil || !strings.Contains(second, "PASS") {
		t.Fatalf("verify = %q, %v", second, err)
	}
	secondLog := logPathFromOutput(t, second)

	if firstLog == secondLog {
		t.Fatalf("two runs on the same revision shared one log path %q", firstLog)
	}
	if _, err := os.Stat(firstLog); err != nil {
		t.Fatalf("the earlier run's log was removed: %v", err)
	}
	if _, err := os.Stat(secondLog); err != nil {
		t.Fatalf("the later run's log is missing: %v", err)
	}
}

// TestVerifyAbortsWhenTheLogCannotBeCreated covers the log directory being
// uncreatable aborting the command before any state is written: no Evidence,
// no status change, no run left in flight.
func TestVerifyAbortsWhenTheLogCannotBeCreated(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	mustRun(t, binary, root, "start", "WI-001")
	writeVerify(t, root, passingVerify)

	// A plain file where the log directory needs to be created blocks it.
	if err := os.WriteFile(filepath.Join(root, ".forgepilot", "logs"), []byte("occupied"), 0644); err != nil {
		t.Fatal(err)
	}

	output, err := command(binary, root, "verify", "WI-001")
	if err == nil {
		t.Fatalf("verified when the log directory could not be created: %s", output)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Evidence) != 0 {
		t.Fatalf("a verify that could not open its log left evidence: %#v", state.Evidence)
	}
	if state.WorkItems[0].Status != work.Running || state.WorkItems[0].CurrentRun != nil {
		t.Fatalf("a verify that could not open its log changed the work item: %#v", state.WorkItems[0])
	}
}

func TestGateBlocksAdvancementWithoutChangingStatus(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/b.md")
	writeVerify(t, root, passingVerify)

	// A Gate must offer a real choice, and must attach to work that exists.
	for _, arguments := range [][]string{
		{"gate", "open", "--work", "WI-001", "--question", "Which cache?", "--option", "redis"},
		{"gate", "open", "--work", "WI-001", "--question", "Which cache?"},
		{"gate", "open", "--work", "WI-404", "--question", "Which cache?", "--option", "redis", "--option", "in-process"},
		{"gate", "open", "--work", "WI-001", "--option", "redis", "--option", "in-process"},
	} {
		if output, err := command(binary, root, arguments...); err == nil {
			t.Fatalf("%v unexpectedly succeeded: %s", arguments, output)
		}
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Gates) != 0 {
		t.Fatalf("a refused gate was recorded: %#v", state.Gates)
	}

	output, err := command(binary, root, "gate", "open", "--work", "WI-001",
		"--question", "Which cache?", "--option", "redis", "--option", "in-process",
		"--reason", "the latency budget is unstated")
	if err != nil || !strings.Contains(output, "GATE-001") {
		t.Fatalf("gate open = %q, %v", output, err)
	}

	// The blocked item keeps the status it had; only its ability to move changes.
	output, err = command(binary, root, "status")
	if err != nil {
		t.Fatalf("status = %q, %v", output, err)
	}
	if !strings.Contains(output, "WI-001 READY") {
		t.Fatalf("opening a gate changed the work item's status: %s", output)
	}
	if !strings.Contains(output, "Gates: 1 open") {
		t.Fatalf("status does not report the open gate count: %s", output)
	}
	if !strings.Contains(output, "Next: WI-002") {
		t.Fatalf("next still selects gated work: %s", output)
	}

	output, err = command(binary, root, "start", "WI-001")
	if err == nil {
		t.Fatalf("started gated work: %s", output)
	}
	if !strings.Contains(output, "GATE-001") {
		t.Fatalf("error %q does not name the gate that blocks the work", output)
	}

	// A second Gate on the same item, opened while the first is still open.
	mustRun(t, binary, root, "gate", "open", "--work", "WI-001",
		"--question", "Backfill the old rows?", "--option", "yes", "--option", "no")
	if output, err := command(binary, root, "status"); err != nil || !strings.Contains(output, "Gates: 2 open") {
		t.Fatalf("status = %q, %v", output, err)
	}

	// Gating work that is already RUNNING blocks verification, and again leaves
	// the status alone.
	mustRun(t, binary, root, "start", "WI-002")
	mustRun(t, binary, root, "gate", "open", "--work", "WI-002",
		"--question", "Is the schema change reversible?", "--option", "yes", "--option", "no")
	output, err = command(binary, root, "status")
	if err != nil || !strings.Contains(output, "WI-002 RUNNING") {
		t.Fatalf("gating running work changed its status: %q, %v", output, err)
	}
	output, err = command(binary, root, "verify", "WI-002")
	if err == nil {
		t.Fatalf("verified gated work: %s", output)
	}
	if !strings.Contains(output, "GATE-003") {
		t.Fatalf("error %q does not name the gate that blocks verification", output)
	}
	state, err = storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Evidence) != 0 {
		t.Fatalf("a blocked verification left evidence: %#v", state.Evidence)
	}
}

// TestConcurrentGateOpensKeepBothGates proves Gates share the Work Item's locked
// atomic replacement: two processes opening at once neither lose an update nor
// hand out the same ID twice.
func TestConcurrentGateOpensKeepBothGates(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")

	var group sync.WaitGroup
	failures := make(chan error, 2)
	for _, question := range []string{"Which cache?", "Which queue?"} {
		group.Add(1)
		go func(question string) {
			defer group.Done()
			if output, err := command(binary, root, "gate", "open", "--work", "WI-001",
				"--question", question, "--option", "a", "--option", "b"); err != nil {
				failures <- &commandError{err, output}
			}
		}(question)
	}
	group.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Gates) != 2 || state.Gates[0].ID == state.Gates[1].ID {
		t.Fatalf("gates = %#v", state.Gates)
	}
	if state.OpenGateCount("WI-001") != 2 {
		t.Fatalf("open gate count = %d, want 2", state.OpenGateCount("WI-001"))
	}
}

func TestGateResolveAndCancelAreFinalAndVisible(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	mustRun(t, binary, root, "gate", "open", "--work", "WI-001",
		"--question", "Which cache?", "--option", "redis", "--option", "in-process")
	mustRun(t, binary, root, "gate", "open", "--work", "WI-001",
		"--question", "Backfill the old rows?", "--option", "yes", "--option", "no")

	for _, arguments := range [][]string{
		{"gate", "resolve", "GATE-001", "--option", "memcached"},
		{"gate", "resolve", "GATE-001"},
		{"gate", "resolve", "GATE-404", "--option", "redis"},
		{"gate", "cancel", "GATE-001"},
		{"gate", "cancel"},
	} {
		if output, err := command(binary, root, arguments...); err == nil {
			t.Fatalf("%v unexpectedly succeeded: %s", arguments, output)
		}
	}

	// Resolving takes the decision maker from Git configuration by default, and
	// says out loud that the identity is only a claim.
	output, err := command(binary, root, "gate", "resolve", "GATE-001",
		"--option", "redis", "--note", "the latency budget rules it in")
	if err != nil || !strings.Contains(output, "RESOLVED") {
		t.Fatalf("gate resolve = %q, %v", output, err)
	}
	if !strings.Contains(output, fixtureIdentity) || !strings.Contains(output, "self-asserted") {
		t.Fatalf("resolve output %q does not record a self-asserted decision maker", output)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	resolved := state.GatesFor("WI-001")[0]
	if resolved.Status != work.GateResolved || resolved.Choice != "redis" || resolved.DecidedBy != fixtureIdentity {
		t.Fatalf("gate = %#v", resolved)
	}
	if resolved.Note != "the latency budget rules it in" || resolved.DecidedAt == nil {
		t.Fatalf("gate lost the judgement behind the choice: %#v", resolved)
	}

	// One gate closed is not enough to lift the block.
	if output, err := command(binary, root, "start", "WI-001"); err == nil {
		t.Fatalf("started work still blocked by GATE-002: %s", output)
	}

	// A closed Gate is history and cannot be changed again.
	for _, arguments := range [][]string{
		{"gate", "resolve", "GATE-001", "--option", "in-process"},
		{"gate", "cancel", "GATE-001", "--reason", "changed my mind"},
	} {
		if output, err := command(binary, root, arguments...); err == nil {
			t.Fatalf("%v changed a closed gate: %s", arguments, output)
		}
	}

	// Cancelling takes an explicit identity and a required reason.
	output, err = command(binary, root, "gate", "cancel", "GATE-002",
		"--reason", "the rows do not exist yet", "--by", "someone@example.com")
	if err != nil || !strings.Contains(output, "CANCELLED") {
		t.Fatalf("gate cancel = %q, %v", output, err)
	}

	// The cancellation is visible, and the block is lifted.
	output, err = command(binary, root, "status")
	if err != nil {
		t.Fatalf("status = %q, %v", output, err)
	}
	if !strings.Contains(output, "Gates: 0 open") {
		t.Fatalf("status does not report the block lifted: %s", output)
	}
	for _, want := range []string{"GATE-001 RESOLVED", "GATE-002 CANCELLED", "the rows do not exist yet", "someone@example.com"} {
		if !strings.Contains(output, want) {
			t.Fatalf("status %q does not show %q", output, want)
		}
	}
	if !strings.Contains(output, "Next: WI-001") {
		t.Fatalf("next does not select work whose gates are all closed: %s", output)
	}
	if !strings.Contains(output, "WI-001 READY") {
		t.Fatalf("closing gates changed the work item's status: %s", output)
	}
	mustRun(t, binary, root, "start", "WI-001")
}

// reviewable drives a fixture to a Work Item sitting in REVIEW on a PASSing
// revision, and returns that revision.
func reviewable(t *testing.T, binary, root string) string {
	t.Helper()
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/b.md", "--depends-on", "WI-001")
	mustRun(t, binary, root, "start", "WI-001")
	revision := writeVerify(t, root, passingVerify)
	if output, err := command(binary, root, "verify", "WI-001"); err != nil || !strings.Contains(output, "PASS") {
		t.Fatalf("verify = %q, %v", output, err)
	}
	return revision
}

func TestReviewRecordsAJudgementBesideTheVerification(t *testing.T) {
	root, binary := fixture(t)
	revision := reviewable(t, binary, root)

	for _, arguments := range [][]string{
		{"review", "reject", "WI-001"},
		{"review", "approve", "WI-002"},
		{"review", "approve"},
		{"review", "sign-off", "WI-001"},
	} {
		if output, err := command(binary, root, arguments...); err == nil {
			t.Fatalf("%v unexpectedly succeeded: %s", arguments, output)
		}
	}

	// A review of a dirty worktree would name a revision that never held what
	// was reviewed.
	if err := os.WriteFile(filepath.Join(root, "stray.txt"), []byte("uncommitted\n"), 0644); err != nil {
		t.Fatal(err)
	}
	output, err := command(binary, root, "review", "approve", "WI-001")
	if err == nil {
		t.Fatalf("reviewed a dirty worktree: %s", output)
	}
	if !strings.Contains(output, "clean") {
		t.Fatalf("error %q does not explain the worktree is not clean", output)
	}
	// The rejection must name the command actually running, not verify's wording.
	if !strings.Contains(output, "reviewing") {
		t.Fatalf("error %q does not say reviewing is what was refused", output)
	}
	if strings.Contains(output, "verifying") {
		t.Fatalf("error %q wrongly points at verifying instead of reviewing", output)
	}
	if !strings.Contains(output, "stray.txt") {
		t.Fatalf("error %q does not list the file that made the worktree dirty", output)
	}
	if err := os.Remove(filepath.Join(root, "stray.txt")); err != nil {
		t.Fatal(err)
	}

	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Evidence) != 1 {
		t.Fatalf("a refused review left evidence: %#v", state.Evidence)
	}

	// REJECTED sends the work straight back to RUNNING for the Agent to fix.
	output, err = command(binary, root, "review", "reject", "WI-001", "--reason", "the error path is unhandled")
	if err != nil || !strings.Contains(output, "REJECTED") {
		t.Fatalf("review reject = %q, %v", output, err)
	}
	if !strings.Contains(output, "WI-001 RUNNING") {
		t.Fatalf("rejection did not return the work to RUNNING: %s", output)
	}
	state, err = storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Evidence) != 2 {
		t.Fatalf("evidence = %#v", state.Evidence)
	}
	rejection := state.Evidence[1]
	if rejection.ID != "EV-002" || rejection.Type != work.ReviewEvidence || rejection.Result != work.Rejected {
		t.Fatalf("evidence = %#v", rejection)
	}
	if rejection.Revision != revision || rejection.Reviewer != fixtureIdentity || rejection.Note != "the error path is unhandled" {
		t.Fatalf("review evidence is not bound to the revision, reviewer and reason: %#v", rejection)
	}
	if rejection.ExitCode != nil || rejection.Command != "" {
		t.Fatalf("review evidence carries verification fields: %#v", rejection)
	}
	if state.Evidence[0].Result != work.Pass {
		t.Fatal("the earlier verification evidence was overwritten")
	}

	output, err = command(binary, root, "status")
	if err != nil {
		t.Fatalf("status = %q, %v", output, err)
	}
	for _, want := range []string{"EV-002 REJECTED", fixtureIdentity, "the error path is unhandled", "not reviewed"} {
		if !strings.Contains(output, want) {
			t.Fatalf("status %q does not show %q", output, want)
		}
	}

	// Re-verifying the same revision returns it to REVIEW, and an explicit
	// reviewer overrides the Git-configured default.
	if output, err := command(binary, root, "verify", "WI-001"); err != nil || !strings.Contains(output, "PASS") {
		t.Fatalf("verify = %q, %v", output, err)
	}
	output, err = command(binary, root, "review", "approve", "WI-001", "--by", "someone@example.com", "--note", "reads correct")
	if err != nil || !strings.Contains(output, "APPROVED") {
		t.Fatalf("review approve = %q, %v", output, err)
	}
	if !strings.Contains(output, "self-asserted") {
		t.Fatalf("approve output %q does not mark the identity as a claim", output)
	}
	state, err = storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	approval := state.Evidence[len(state.Evidence)-1]
	if approval.Result != work.Approved || approval.Reviewer != "someone@example.com" || approval.Revision != revision {
		t.Fatalf("evidence = %#v", approval)
	}
}

func TestApprovalCompletesWorkAndUnlocksTheQueue(t *testing.T) {
	root, binary := fixture(t)
	revision := reviewable(t, binary, root)
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/c.md",
		"--depends-on", "WI-001", "--depends-on", "WI-002")

	output, err := command(binary, root, "review", "approve", "WI-001")
	if err != nil || !strings.Contains(output, "WI-001 DONE") {
		t.Fatalf("review approve = %q, %v", output, err)
	}
	if !strings.Contains(output, "WI-002 READY") {
		t.Fatalf("approve did not report the unlocked dependent: %s", output)
	}

	// A separate process reads back the completion and the unlock together:
	// there is no moment at which WI-001 is DONE while WI-002 still waits.
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if state.WorkItemStatus("WI-001") != work.Done {
		t.Fatalf("WI-001 = %s, want DONE", state.WorkItemStatus("WI-001"))
	}
	if state.WorkItemStatus("WI-002") != work.Ready {
		t.Fatalf("WI-002 = %s, want READY", state.WorkItemStatus("WI-002"))
	}
	if state.WorkItemStatus("WI-003") != work.Pending {
		t.Fatalf("WI-003 = %s, want PENDING: WI-002 is not done yet", state.WorkItemStatus("WI-003"))
	}

	output, err = command(binary, root, "status")
	if err != nil {
		t.Fatalf("status = %q, %v", output, err)
	}
	if !strings.Contains(output, "WI-001 DONE") || !strings.Contains(output, "Next: WI-002") {
		t.Fatalf("status = %s", output)
	}
	// Completed work still shows the revision it finished on.
	if !strings.Contains(output, revision[:12]) {
		t.Fatalf("status %q does not show the revision WI-001 completed on", output)
	}

	// Later commits do not make a completion stale, and nothing reopens it.
	writeVerify(t, root, "verify:\n\t@echo moved on\n")
	output, err = command(binary, root, "status")
	if err != nil {
		t.Fatalf("status = %q, %v", output, err)
	}
	if strings.Contains(output, "stale") {
		t.Fatalf("completed work was marked stale after a later commit: %s", output)
	}
	for _, arguments := range [][]string{
		{"done", "WI-002"}, {"complete", "WI-002"}, {"reopen", "WI-001"},
		{"start", "WI-001"}, {"verify", "WI-001"}, {"review", "approve", "WI-001"},
	} {
		if output, err := command(binary, root, arguments...); err == nil {
			t.Fatalf("%v gave a way into or out of DONE: %s", arguments, output)
		}
	}
}

func TestApprovalAheadOfVerificationRecordsButDoesNotComplete(t *testing.T) {
	root, binary := fixture(t)
	reviewable(t, binary, root)
	// Move HEAD on: the PASS now covers a revision the approval will not.
	moved := writeVerify(t, root, "verify:\n\t@echo checked again\n")

	output, err := command(binary, root, "review", "approve", "WI-001")
	if err != nil {
		t.Fatalf("review approve = %q, %v", output, err)
	}
	if !strings.Contains(output, "WI-001 REVIEW") {
		t.Fatalf("work completed on a revision that was never verified: %s", output)
	}
	if !strings.Contains(output, "Not complete") {
		t.Fatalf("approve %q did not say why the work is not complete", output)
	}

	output, err = command(binary, root, "status")
	if err != nil {
		t.Fatalf("status = %q, %v", output, err)
	}
	if !strings.Contains(output, "Not complete") || !strings.Contains(output, moved[:12]) {
		t.Fatalf("status %q does not explain the revision mismatch", output)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if latest, ok := state.LatestReview("WI-001"); !ok || latest.Result != work.Approved {
		t.Fatalf("the approval itself was not recorded: %#v, %v", latest, ok)
	}
	if state.WorkItemStatus("WI-002") != work.Pending {
		t.Fatal("a dependent was unlocked without a completion")
	}

	// Verifying the revision that was approved completes it.
	if output, err := command(binary, root, "verify", "WI-001"); err != nil || !strings.Contains(output, "PASS") {
		t.Fatalf("verify = %q, %v", output, err)
	}
	output, err = command(binary, root, "review", "approve", "WI-001")
	if err != nil || !strings.Contains(output, "WI-001 DONE") {
		t.Fatalf("review approve = %q, %v", output, err)
	}
}

func TestGoalLifecycle(t *testing.T) {
	root, binary := fixture(t)
	revision := reviewable(t, binary, root)

	for _, arguments := range [][]string{
		{"goal", "block", "queue"},
		{"goal", "block"},
		{"goal", "cancel", "queue"},
		{"goal", "unblock", "queue"},
		{"goal", "retire", "queue"},
		{"goal", "block", "missing", "--reason", "wrong direction"},
	} {
		if output, err := command(binary, root, arguments...); err == nil {
			t.Fatalf("%v unexpectedly succeeded: %s", arguments, output)
		}
	}

	output, err := command(binary, root, "goal", "block", "queue", "--reason", "the direction is wrong")
	if err != nil || !strings.Contains(output, "BLOCKED") {
		t.Fatalf("goal block = %q, %v", output, err)
	}
	output, err = command(binary, root, "status")
	if err != nil {
		t.Fatalf("status = %q, %v", output, err)
	}
	// The pause does not disturb the work underneath it.
	if !strings.Contains(output, "WI-001 REVIEW") || !strings.Contains(output, "the direction is wrong") {
		t.Fatalf("status = %s", output)
	}
	if !strings.Contains(output, "Next: none") {
		t.Fatalf("next selected work under a blocked goal: %s", output)
	}
	for _, arguments := range [][]string{
		{"start", "WI-002"},
		{"verify", "WI-001"},
		{"work", "add", "--goal", "queue", "--story", "specs/stories/c.md"},
	} {
		if output, err := command(binary, root, arguments...); err == nil {
			t.Fatalf("%v ran under a blocked goal: %s", arguments, output)
		}
	}
	// Approving records the judgement but cannot reach DONE while the goal is
	// paused, and says so.
	output, err = command(binary, root, "review", "approve", "WI-001")
	if err != nil {
		t.Fatalf("review approve = %q, %v", output, err)
	}
	if !strings.Contains(output, "WI-001 REVIEW") || !strings.Contains(output, "Not complete") {
		t.Fatalf("approve completed work under a blocked goal, or did not say why not: %s", output)
	}

	if output, err := command(binary, root, "goal", "unblock", "queue"); err != nil || !strings.Contains(output, "ACTIVE") {
		t.Fatalf("goal unblock = %q, %v", output, err)
	}
	// Nothing was lost: the same revision is still verified and can complete.
	output, err = command(binary, root, "review", "approve", "WI-001")
	if err != nil || !strings.Contains(output, "WI-001 DONE") {
		t.Fatalf("review approve after unblock = %q, %v", output, err)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if latest, ok := state.LatestVerification("WI-001"); !ok || latest.Revision != revision {
		t.Fatalf("the progress made before blocking was lost: %#v, %v", latest, ok)
	}

	// COMPLETED is a declaration, and it is refused while work is unfinished.
	if output, err := command(binary, root, "goal", "complete", "queue"); err == nil {
		t.Fatalf("completed a goal with WI-002 unfinished: %s", output)
	}
	mustRun(t, binary, root, "start", "WI-002")
	if output, err := command(binary, root, "verify", "WI-002"); err != nil || !strings.Contains(output, "PASS") {
		t.Fatalf("verify = %q, %v", output, err)
	}
	if output, err := command(binary, root, "review", "approve", "WI-002"); err != nil || !strings.Contains(output, "WI-002 DONE") {
		t.Fatalf("review approve = %q, %v", output, err)
	}
	if output, err := command(binary, root, "goal", "complete", "queue"); err != nil || !strings.Contains(output, "COMPLETED") {
		t.Fatalf("goal complete = %q, %v", output, err)
	}
	// An ended Goal stays ended.
	for _, arguments := range [][]string{
		{"goal", "block", "queue", "--reason", "reconsidered"},
		{"goal", "cancel", "queue", "--reason", "reconsidered"},
		{"goal", "unblock", "queue"},
	} {
		if output, err := command(binary, root, arguments...); err == nil {
			t.Fatalf("%v moved a completed goal: %s", arguments, output)
		}
	}
}

func TestGoalCancelEndsAbandonedWork(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")

	output, err := command(binary, root, "goal", "cancel", "queue", "--reason", "the customer withdrew the request")
	if err != nil || !strings.Contains(output, "CANCELLED") {
		t.Fatalf("goal cancel = %q, %v", output, err)
	}
	output, err = command(binary, root, "status")
	if err != nil {
		t.Fatalf("status = %q, %v", output, err)
	}
	if !strings.Contains(output, "the customer withdrew the request") || !strings.Contains(output, "Next: none") {
		t.Fatalf("status = %s", output)
	}
	if output, err := command(binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/b.md"); err == nil {
		t.Fatalf("added work to a cancelled goal: %s", output)
	}
}

// TestBlockingMidRunStillRecordsTheEvidence proves the line an inactive Goal
// draws: it stops new work, not the recording of a fact that already happened.
func TestBlockingMidRunStillRecordsTheEvidence(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	mustRun(t, binary, root, "start", "WI-001")
	revision := writeVerify(t, root, "verify:\n\t@sleep 2\n")

	running := startVerify(t, binary, root, "WI-001")
	mustRun(t, binary, root, "goal", "block", "queue", "--reason", "the direction is wrong")
	if err := running.Wait(); err != nil {
		t.Fatalf("verification did not finish after the goal was blocked: %v", err)
	}

	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	latest, ok := state.LatestVerification("WI-001")
	if !ok {
		t.Fatal("a run underway when the goal was blocked lost its evidence")
	}
	if latest.Result != work.Pass || latest.Revision != revision {
		t.Fatalf("evidence = %#v, want PASS at %s", latest, revision)
	}
	if state.WorkItemStatus("WI-001") != work.Review {
		t.Fatalf("WI-001 = %s, want REVIEW", state.WorkItemStatus("WI-001"))
	}
}

// TestEndToEndQueueAdvances is the acceptance M1 deferred to M3. Until now the
// claim "A completes, so B becomes READY" could only be shown with a test
// fixture that set DONE directly, because no product path reached it. This runs
// the real one, end to end, in separate processes against a real repository.
func TestEndToEndQueueAdvances(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/b.md", "--depends-on", "WI-001")
	writeVerify(t, root, passingVerify)

	if output, err := command(binary, root, "next"); err != nil || !strings.Contains(output, "Next: WI-001") {
		t.Fatalf("next = %q, %v", output, err)
	}
	mustRun(t, binary, root, "start", "WI-001")

	// A question comes up mid-flight. The work stops moving without losing the
	// status it had, and comes back to life once the question is answered.
	mustRun(t, binary, root, "gate", "open", "--work", "WI-001",
		"--question", "Should the old rows be backfilled?", "--option", "yes", "--option", "no")
	if output, err := command(binary, root, "verify", "WI-001"); err == nil {
		t.Fatalf("verified work with an open gate: %s", output)
	}
	output, err := command(binary, root, "status")
	if err != nil || !strings.Contains(output, "WI-001 RUNNING") || !strings.Contains(output, "Gates: 1 open") {
		t.Fatalf("status = %q, %v", output, err)
	}
	mustRun(t, binary, root, "gate", "resolve", "GATE-001", "--option", "no", "--note", "there are no old rows yet")

	revision, err := headRevision(root)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := command(binary, root, "verify", "WI-001"); err != nil || !strings.Contains(output, "PASS") {
		t.Fatalf("verify = %q, %v", output, err)
	}
	output, err = command(binary, root, "review", "approve", "WI-001", "--note", "solves the right problem")
	if err != nil || !strings.Contains(output, "WI-001 DONE") {
		t.Fatalf("review approve = %q, %v", output, err)
	}

	// A fresh process reads back the finished queue.
	output, err = command(binary, root, "next")
	if err != nil || !strings.Contains(output, "Next: WI-002") {
		t.Fatalf("next = %q, %v", output, err)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if state.WorkItemStatus("WI-001") != work.Done || state.WorkItemStatus("WI-002") != work.Ready {
		t.Fatalf("queue did not advance: %#v", state.WorkItems)
	}
	verification, ok := state.LatestVerification("WI-001")
	if !ok || verification.Result != work.Pass || verification.Revision != revision {
		t.Fatalf("verification evidence = %#v, %v", verification, ok)
	}
	approval, ok := state.LatestReview("WI-001")
	if !ok || approval.Result != work.Approved || approval.Revision != revision {
		t.Fatalf("review evidence = %#v, %v", approval, ok)
	}
	if resolved := state.GatesFor("WI-001"); len(resolved) != 1 || resolved[0].Choice != "no" {
		t.Fatalf("gate history = %#v", resolved)
	}
	mustRun(t, binary, root, "start", "WI-002")
}

func headRevision(root string) (string, error) {
	output, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// TestMissingIdentityNamesTheFlagThatFixesIt runs without any Git identity
// configured, so the default decision maker cannot be resolved. The error has to
// name a flag that actually exists: guidance that fails when followed is worse
// than none.
func TestMissingIdentityNamesTheFlagThatFixesIt(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	mustRun(t, binary, root, "gate", "open", "--work", "WI-001",
		"--question", "Which cache?", "--option", "redis", "--option", "in-process")
	if output, err := exec.Command("git", "-C", root, "config", "--unset", "user.email").CombinedOutput(); err != nil {
		t.Fatalf("git config --unset: %v: %s", err, output)
	}
	// Ignore the machine's own configuration, so the fixture decides what Git
	// knows rather than whoever is running the tests.
	withoutIdentity := func(arguments ...string) (string, error) {
		command := exec.Command(binary, arguments...)
		command.Dir = root
		command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		output, err := command.CombinedOutput()
		return string(output), err
	}

	output, err := withoutIdentity("gate", "resolve", "GATE-001", "--option", "redis")
	if err == nil {
		t.Fatalf("resolved a gate with no identity to record: %s", output)
	}
	flag := "--by"
	if !strings.Contains(output, flag) {
		t.Fatalf("error %q does not name the flag that fixes it", output)
	}
	// The named flag must be the one the command actually accepts.
	if output, err := withoutIdentity("gate", "resolve", "GATE-001", "--option", "redis", flag, "someone@example.com"); err != nil {
		t.Fatalf("%s did not fix the error it was offered for: %q, %v", flag, output, err)
	}
}

// TestApprovalHeldByAGateSaysWhatIsLeftAfterItCloses covers the state ADR-0008
// forbids: an approval that did not complete must never sit silently. Once the
// last blocker is lifted the conditions all hold, and the user has to be told
// what finishes the work — the completion check runs inside `review approve`,
// so nothing happens until it is run again.
func TestApprovalHeldByAGateSaysWhatIsLeftAfterItCloses(t *testing.T) {
	root, binary := fixture(t)
	reviewable(t, binary, root)
	mustRun(t, binary, root, "gate", "open", "--work", "WI-001",
		"--question", "Which cache?", "--option", "redis", "--option", "in-process")

	output, err := command(binary, root, "review", "approve", "WI-001")
	if err != nil {
		t.Fatalf("review approve = %q, %v", output, err)
	}
	if !strings.Contains(output, "Not complete") || !strings.Contains(output, "gate") {
		t.Fatalf("approve %q does not name the gate holding the completion", output)
	}

	mustRun(t, binary, root, "gate", "resolve", "GATE-001", "--option", "redis")
	output, err = command(binary, root, "status")
	if err != nil {
		t.Fatalf("status = %q, %v", output, err)
	}
	if !strings.Contains(output, "WI-001 REVIEW") {
		t.Fatalf("resolving a gate completed the work on its own: %s", output)
	}
	if !strings.Contains(output, "review approve WI-001") {
		t.Fatalf("status %q leaves an approved, unblocked work item stalled without saying what finishes it", output)
	}

	if output, err := command(binary, root, "review", "approve", "WI-001"); err != nil || !strings.Contains(output, "WI-001 DONE") {
		t.Fatalf("review approve = %q, %v", output, err)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if state.WorkItemStatus("WI-002") != work.Ready {
		t.Fatalf("WI-002 = %s, want READY", state.WorkItemStatus("WI-002"))
	}
}

// TestOrphanIsReclaimedEvenWhenANewRunIsRefused separates the two things verify
// does. An interrupted run is a fact that already happened, so it is recorded
// whatever is blocking the Work Item; starting a *new* run is subject to every
// block. Refusing both together would leave the fact unrecorded and the Work
// Item stuck in VERIFYING with no way out until the block lifted.
func TestOrphanIsReclaimedEvenWhenANewRunIsRefused(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	mustRun(t, binary, root, "start", "WI-001")
	// Long enough that the test decides when the run ends, never the clock.
	slow := writeVerify(t, root, "verify:\n\t@sleep 30\n")

	running := startVerify(t, binary, root, "WI-001")
	orphaned, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	logPath := orphaned.WorkItems[0].CurrentRun.LogPath
	if logPath == "" {
		t.Fatalf("current_run has no LogPath: %#v", orphaned.WorkItems[0].CurrentRun)
	}
	if err := running.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = running.Wait()

	// A question is raised before anyone gets round to re-verifying.
	mustRun(t, binary, root, "gate", "open", "--work", "WI-001",
		"--question", "Which cache?", "--option", "redis", "--option", "in-process")

	output, err := command(binary, root, "verify", "WI-001")
	if err == nil {
		t.Fatalf("started a new run past an open gate: %s", output)
	}
	if !strings.Contains(output, "GATE-001") {
		t.Fatalf("error %q does not name the gate that blocks the new run", output)
	}
	if !strings.Contains(output, "INTERRUPTED") {
		t.Fatalf("output %q does not report the run it reclaimed", output)
	}
	// The reclaim's report is not withheld just because the new run it made way
	// for was then refused: the fact and its log path were already settled
	// before the refusal was even evaluated.
	if !strings.Contains(output, logPath) {
		t.Fatalf("output %q does not report the reclaimed run's log path %q even though the new run was refused", output, logPath)
	}

	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	latest, ok := state.LatestVerification("WI-001")
	if !ok || latest.Result != work.Interrupted || latest.Revision != slow {
		t.Fatalf("the killed run was not recorded: %#v, %v", latest, ok)
	}
	if latest.ExitCode != nil {
		t.Fatalf("an interruption was given an exit code: %#v", latest)
	}
	if state.WorkItemStatus("WI-001") != work.Running || state.WorkItems[0].CurrentRun != nil {
		t.Fatalf("WI-001 = %#v, want RUNNING with no run in flight", state.WorkItems[0])
	}
	if entries, err := os.ReadDir(filepath.Join(root, ".forgepilot", "worktrees")); err == nil && len(entries) != 0 {
		t.Fatalf("the abandoned run's worktree was left behind: %v", entries)
	}

	// Reclaiming happens once: a second refused verify has nothing left to record.
	if output, err := command(binary, root, "verify", "WI-001"); err == nil {
		t.Fatalf("started a new run past an open gate: %s", output)
	} else if strings.Contains(output, "INTERRUPTED") {
		t.Fatalf("a second refusal recorded another interruption: %s", output)
	}
	if state, err := storage.Load(root); err != nil {
		t.Fatal(err)
	} else if len(state.Evidence) != 1 {
		t.Fatalf("evidence = %#v", state.Evidence)
	}

	// With the question answered, verification proceeds normally.
	mustRun(t, binary, root, "gate", "resolve", "GATE-001", "--option", "redis")
	writeVerify(t, root, passingVerify)
	if output, err := command(binary, root, "verify", "WI-001"); err != nil || !strings.Contains(output, "PASS") {
		t.Fatalf("verify = %q, %v", output, err)
	}
}

// TestReviewRecordsThePullRequestItHappenedOn covers the M4 addition at the CLI:
// --pr is optional, stored beside the exact revision it was reviewed at, and a
// malformed value is invalid input rather than a review with a bad outcome.
func TestReviewRecordsThePullRequestItHappenedOn(t *testing.T) {
	root, binary := fixture(t)
	revision := reviewable(t, binary, root)

	for _, reference := range []string{
		"https://github.com/CarlLee1983/ForgePilot/pull/7",
		"carl/forgepilot",
		"carl/forgepilot#0",
		"carl/forgepilot#007",
	} {
		output, err := command(binary, root, "review", "approve", "WI-001", "--pr", reference)
		if err == nil {
			t.Fatalf("accepted %q: %s", reference, output)
		}
		// Distinguish a malformed value from a usage error: the usage line names
		// the same form, so matching only that would accept either failure.
		if !strings.Contains(output, "is not a pull request reference") {
			t.Fatalf("error %q does not say the value is malformed", output)
		}
		state, err := storage.Load(root)
		if err != nil {
			t.Fatal(err)
		}
		// Invalid input writes nothing at all — not even a record that someone
		// tried.
		if len(state.Evidence) != 1 {
			t.Fatalf("a refused review left evidence for %q: %#v", reference, state.Evidence)
		}
		if state.WorkItemStatus("WI-001") != work.Review {
			t.Fatalf("a refused review moved the work: %s", state.WorkItemStatus("WI-001"))
		}
	}

	// Naming the flag and giving it nothing is invalid input, not a review
	// without a pull request.
	if output, err := command(binary, root, "review", "approve", "WI-001", "--pr", ""); err == nil {
		t.Fatalf("accepted an empty --pr: %s", output)
	}

	// Rejection carries the pull request too: being sent back has a venue just
	// as much as being approved does.
	if output, err := command(binary, root, "review", "reject", "WI-001",
		"--reason", "the error path is unhandled", "--pr", "carl/forgepilot#7"); err != nil {
		t.Fatalf("review reject = %q, %v", output, err)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Evidence[1]; got.PR != "carl/forgepilot#7" || got.Result != work.Rejected || got.Revision != revision {
		t.Fatalf("rejection did not record the pull request: %#v", got)
	}

	if output, err := command(binary, root, "verify", "WI-001"); err != nil || !strings.Contains(output, "PASS") {
		t.Fatalf("verify = %q, %v", output, err)
	}
	if output, err := command(binary, root, "review", "approve", "WI-001", "--pr", "carl/forgepilot#7"); err != nil {
		t.Fatalf("review approve = %q, %v", output, err)
	}
	state, err = storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	approval := state.Evidence[len(state.Evidence)-1]
	if approval.PR != "carl/forgepilot#7" || approval.Revision != revision || approval.Result != work.Approved {
		t.Fatalf("approval = %#v", approval)
	}
	// The PR is identification, not a condition: the work completes exactly as
	// it would have without one (ADR-0011).
	if state.WorkItemStatus("WI-001") != work.Done {
		t.Fatalf("work with a PR reference did not complete: %s", state.WorkItemStatus("WI-001"))
	}
}

// TestPRReviewSurvivesAChangeOfHead is M4's acceptance flow. It proves the three
// fields the milestone requires are held together — repository, pull request and
// the exact HEAD — and that moving HEAD makes a new review target rather than
// letting the previous approval carry over.
//
// The second half runs on WI-002 rather than reopening WI-001: DONE is terminal
// and has no reopen (ADR-0006), so "a new HEAD needs a fresh review" is a claim
// about work that has not completed yet.
func TestPRReviewSurvivesAChangeOfHead(t *testing.T) {
	root, binary := fixture(t)
	first := reviewable(t, binary, root)

	output, err := command(binary, root, "review", "approve", "WI-001", "--pr", "carl/forgepilot#7")
	if err != nil || !strings.Contains(output, "WI-001 DONE") {
		t.Fatalf("review approve = %q, %v", output, err)
	}
	if !strings.Contains(output, "WI-002 READY") {
		t.Fatalf("the queue did not advance: %s", output)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	approval, ok := state.LatestReview("WI-001")
	if !ok || approval.PR != "carl/forgepilot#7" || approval.Revision != first || approval.Repository == "" {
		t.Fatalf("approval does not carry repository, pull request and exact head: %#v", approval)
	}

	// WI-002 reaches REVIEW on the same revision, and then HEAD moves.
	mustRun(t, binary, root, "start", "WI-002")
	if output, err := command(binary, root, "verify", "WI-002"); err != nil || !strings.Contains(output, "PASS") {
		t.Fatalf("verify = %q, %v", output, err)
	}
	if err := os.WriteFile(filepath.Join(root, "specs", "stories", "b.md"), []byte("# story\n\nmore\n"), 0644); err != nil {
		t.Fatal(err)
	}
	second := commitAll(t, root, "more work on the pull request")
	if second == first {
		t.Fatal("the fixture did not move HEAD")
	}

	// The approval lands on the new HEAD, which nothing has verified: the PASS
	// from the previous revision is history, not a result that carries over.
	output, err = command(binary, root, "review", "approve", "WI-002", "--pr", "carl/forgepilot#7")
	if err != nil {
		t.Fatalf("review approve = %q, %v", output, err)
	}
	if !strings.Contains(output, "WI-002 REVIEW") || !strings.Contains(output, "Not complete") {
		t.Fatalf("work completed against a revision nothing verified: %s", output)
	}
	state, err = storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if latest, ok := state.LatestVerification("WI-002"); !ok || latest.Revision != first {
		t.Fatalf("the earlier verification was rewritten: %#v", latest)
	}

	// Re-verifying and re-approving the new HEAD completes it, and the fresh
	// Evidence names the new revision rather than the old one.
	if output, err := command(binary, root, "verify", "WI-002"); err != nil || !strings.Contains(output, "PASS") {
		t.Fatalf("verify = %q, %v", output, err)
	}
	output, err = command(binary, root, "review", "approve", "WI-002", "--pr", "carl/forgepilot#7")
	if err != nil || !strings.Contains(output, "WI-002 DONE") {
		t.Fatalf("review approve = %q, %v", output, err)
	}
	state, err = storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	verification, _ := state.LatestVerification("WI-002")
	review, _ := state.LatestReview("WI-002")
	if verification.Revision != second || review.Revision != second || review.PR != "carl/forgepilot#7" {
		t.Fatalf("the new review target was not recorded: %#v, %#v", verification, review)
	}
	// Every earlier record is still there: superseded, not replaced.
	if len(state.Evidence) != 6 {
		t.Fatalf("history was rewritten: %#v", state.Evidence)
	}
}

// TestStatusShowsThePullRequestOnlyWhenThereIsOne keeps the display honest in
// both directions: a recorded pull request is readable, and its absence is a
// legal state that must not be dressed up as something to act on.
func TestStatusShowsThePullRequestOnlyWhenThereIsOne(t *testing.T) {
	root, binary := fixture(t)
	reviewable(t, binary, root)

	if output, err := command(binary, root, "review", "reject", "WI-001", "--reason", "not yet"); err != nil {
		t.Fatalf("review reject = %q, %v", output, err)
	}
	output, err := command(binary, root, "status")
	if err != nil {
		t.Fatalf("status = %q, %v", output, err)
	}
	if !strings.Contains(output, "EV-002 REJECTED") {
		t.Fatalf("status %q does not show the review", output)
	}
	if strings.Contains(output, "PR") || strings.Contains(output, "pull request") {
		t.Fatalf("status %q mentions a pull request for a review that has none", output)
	}

	if output, err := command(binary, root, "verify", "WI-001"); err != nil || !strings.Contains(output, "PASS") {
		t.Fatalf("verify = %q, %v", output, err)
	}
	if output, err := command(binary, root, "review", "approve", "WI-001", "--pr", "carl/forgepilot#7"); err != nil {
		t.Fatalf("review approve = %q, %v", output, err)
	}
	output, err = command(binary, root, "status")
	if err != nil {
		t.Fatalf("status = %q, %v", output, err)
	}
	if !strings.Contains(output, "carl/forgepilot#7") {
		t.Fatalf("status %q does not show the pull request that was recorded", output)
	}
}

// Help must work before init, and outside a repository altogether: the point of
// asking what the commands are is that you have not committed to running any.
func TestHelpDoesNotRequireInitializedState(t *testing.T) {
	root, binary := fixture(t)

	for _, argument := range []string{"help", "--help", "-h"} {
		output, err := command(binary, root, argument)
		if err != nil {
			t.Fatalf("%s in an uninitialized repository: %v: %s", argument, err, output)
		}
		for _, want := range []string{"usage: forgepilot", "verify", "gate", "review"} {
			if !strings.Contains(output, want) {
				t.Fatalf("%s output does not mention %q: %s", argument, want, output)
			}
		}
	}

	// A directory that is not a repository at all is the same case.
	output, err := command(binary, t.TempDir(), "help")
	if err != nil {
		t.Fatalf("help outside a repository: %v: %s", err, output)
	}
}
