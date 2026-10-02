package forgepilot_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Binary-level basics that an earlier workflow test covered and the migration to
// goal import would otherwise have dropped: init is repeatable, a RUNNING work
// item cannot be started again, and a damaged state file makes status fail
// rather than report something.
func TestCLIBasicsInitRepeatRestartAndDamagedState(t *testing.T) {
	root, binary := fixture(t)
	for range 2 {
		if output, err := command(binary, root, "init"); err != nil || !strings.Contains(output, "Initialized") {
			t.Fatalf("init = %q, %v", output, err)
		}
	}
	createGoal(t, binary, root, "queue", "Queue", true)
	addWork(t, binary, root, "queue", "specs/stories/a.md")
	addWork(t, binary, root, "queue", "specs/stories/b.md", "WI-001")
	if output, err := command(binary, root, "start", "WI-001"); err != nil || !strings.Contains(output, "WI-001 RUNNING") {
		t.Fatalf("start = %q, %v", output, err)
	}
	if output, err := command(binary, root, "start", "WI-001"); err == nil {
		t.Fatalf("started a RUNNING work item a second time: %s", output)
	}
	if output, err := command(binary, root, "start", "WI-002"); err == nil {
		t.Fatalf("started a PENDING work item: %s", output)
	}
	if output, err := command(binary, root, "status"); err != nil || !strings.Contains(output, "WI-001 RUNNING") {
		t.Fatalf("status = %q, %v", output, err)
	}
	if err := os.WriteFile(filepath.Join(root, ".forgepilot", "state.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := command(binary, root, "status"); err == nil {
		t.Fatalf("status succeeded on a damaged state: %s", output)
	}
}

// Existing nodes are compared by their stored story spelling only. A Story that
// was finished and then moved or deleted must not freeze its Goal against growth;
// the existence check is for nodes being added.
func TestGoalReimportDoesNotRecheckStoriesOfExistingNodes(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "import", writePlanText(t, planText("g", false, "a specs/stories/a.md", "b specs/stories/b.md")))
	if err := os.Remove(filepath.Join(root, "specs", "stories", "a.md")); err != nil {
		t.Fatal(err)
	}

	output, err := command(binary, root, "goal", "import", writePlanText(t, planText("g", false, "a specs/stories/a.md", "b specs/stories/b.md", "c specs/stories/c.md b")))
	if err != nil || !strings.Contains(output, "Goal g updated: added 1 nodes") {
		t.Fatalf("append after an old story vanished = %q, %v", output, err)
	}
	// Unchanged plan: still a no-op, still no existence check on a.
	if output, err := command(binary, root, "goal", "import", writePlanText(t, planText("g", false, "a specs/stories/a.md", "b specs/stories/b.md", "c specs/stories/c.md b"))); err != nil || !strings.Contains(output, "unchanged") {
		t.Fatalf("identical plan after an old story vanished = %q, %v", output, err)
	}
	// A new node still needs a real Story...
	output, err = command(binary, root, "goal", "import", writePlanText(t, planText("g", false, "a specs/stories/a.md", "b specs/stories/b.md", "c specs/stories/c.md b", "d specs/stories/a.md")))
	if err == nil || !strings.Contains(output, `node "d"`) {
		t.Fatalf("new node pointing at the vanished story = %q, %v; want a refusal naming d", output, err)
	}
	// ...and an existing node's story is still compared, literally.
	output, err = command(binary, root, "goal", "import", writePlanText(t, planText("g", false, "a specs/stories/b.md", "b specs/stories/b.md", "c specs/stories/c.md b")))
	if err == nil || !strings.Contains(output, `node "a"`) {
		t.Fatalf("changed story on an existing node = %q, %v", output, err)
	}
	// The same story spelled differently normalizes to the stored one.
	if output, err := command(binary, root, "goal", "import", writePlanText(t, planText("g", false, "a ./specs/stories/a.md", "b specs/stories/b.md", "c specs/stories/c.md b"))); err != nil || !strings.Contains(output, "unchanged") {
		t.Fatalf("re-import spelling a's story with a leading ./ = %q, %v; want unchanged", output, err)
	}
}
