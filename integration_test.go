package forgepilot_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/carl/forgepilot/internal/storage"
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
