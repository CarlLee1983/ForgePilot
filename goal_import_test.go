package forgepilot_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// planText renders a Goal Plan with the given approval flag and nodes, each node
// "id story dep,dep".
func planText(goalID string, requireApproval bool, nodes ...string) string {
	var rendered []string
	for _, node := range nodes {
		fields := strings.Fields(node)
		depends := []string{}
		if len(fields) > 2 {
			depends = strings.Split(fields[2], ",")
		}
		encoded, _ := json.Marshal(map[string]any{"id": fields[0], "story": fields[1], "depends_on": depends})
		rendered = append(rendered, string(encoded))
	}
	approval := "false"
	if requireApproval {
		approval = "true"
	}
	return `{"goal":{"id":"` + goalID + `","title":"Title of ` + goalID + `","require_approval":` + approval + `},"nodes":[` + strings.Join(rendered, ",") + `]}`
}

func stateBytes(t *testing.T, root string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, ".forgepilot", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestGoalImportDrivesTheWholeLoopWithNodeIDsAsWorkItemIDs(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	writeVerify(t, root, passingVerify)

	output, err := command(binary, root, "goal", "import", writePlanText(t, planText("billing", true,
		"sync-job specs/stories/b.md schema",
		"schema specs/stories/a.md",
		"report specs/stories/c.md schema,sync-job",
	)))
	if err != nil {
		t.Fatalf("goal import: %v: %s", err, output)
	}
	for _, want := range []string{"Goal billing imported: 3 nodes", "sync-job PENDING specs/stories/b.md", "schema READY specs/stories/a.md", "report PENDING specs/stories/c.md"} {
		if !strings.Contains(output, want) {
			t.Fatalf("import output %q lacks %q", output, want)
		}
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, item := range state.WorkItems {
		ids = append(ids, item.ID)
	}
	if strings.Join(ids, ",") != "sync-job,schema,report" {
		t.Fatalf("work item IDs = %v, want the plan's node IDs in node order", ids)
	}
	if goal, _ := state.GoalByID("billing"); goal.ReviewPolicy != work.ReviewPerWorkItem {
		t.Fatalf("require_approval true gave policy %s, want WORK_ITEM", goal.ReviewPolicy)
	}

	output, err = command(binary, root, "next")
	if err != nil || !strings.Contains(output, "Next: schema") || !strings.Contains(output, "Action: forgepilot start schema") {
		t.Fatalf("next = %q, %v; want the one READY node", output, err)
	}
	mustRun(t, binary, root, "start", "schema")
	if output, err := command(binary, root, "start", "sync-job"); err == nil {
		t.Fatalf("started a PENDING node: %s", output)
	}
	output, err = command(binary, root, "verify", "schema")
	if err != nil || !strings.Contains(output, "PASS") {
		t.Fatalf("verify = %q, %v", output, err)
	}
	mustRun(t, binary, root, "review", "request", "schema")
	output, err = command(binary, root, "review", "approve", "schema")
	if err != nil || !strings.Contains(output, "schema DONE") {
		t.Fatalf("approve = %q, %v", output, err)
	}
	output, err = command(binary, root, "next")
	if err != nil || !strings.Contains(output, "Next: sync-job") {
		t.Fatalf("next after schema is DONE = %q, %v; want sync-job unlocked", output, err)
	}

	// A node added later may depend on one that is already DONE.
	output, err = command(binary, root, "goal", "import", writePlanText(t, planText("billing", true,
		"sync-job specs/stories/b.md schema",
		"schema specs/stories/a.md",
		"report specs/stories/c.md schema,sync-job",
		"extra specs/stories/c.md schema",
	)))
	if err != nil || !strings.Contains(output, "Goal billing updated: added 1 nodes") || !strings.Contains(output, "extra READY") {
		t.Fatalf("append import = %q, %v; want extra READY because schema is DONE", output, err)
	}
}

func TestGoalImportOrderBreaksTiesBetweenReadyNodes(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "import", writePlanText(t, planText("g", false,
		"zeta specs/stories/c.md",
		"alpha specs/stories/a.md",
		"mid specs/stories/b.md",
	)))
	output, err := command(binary, root, "next")
	if err != nil || !strings.Contains(output, "Next: zeta") {
		t.Fatalf("next = %q, %v; want zeta (first node), not alpha (first by name)", output, err)
	}
	mustRun(t, binary, root, "start", "zeta")
	writeVerify(t, root, passingVerify)
	mustRun(t, binary, root, "verify", "zeta")
	output, err = command(binary, root, "next")
	if err != nil || !strings.Contains(output, "Next: alpha") {
		t.Fatalf("next after zeta = %q, %v; want alpha, the next node in plan order", output, err)
	}
}

func TestGoalImportMapsApprovalToReviewPolicy(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "import", writePlanText(t, planText("strict", true, "a specs/stories/a.md")))
	mustRun(t, binary, root, "goal", "import", writePlanText(t, planText("fast", false, "b specs/stories/b.md")))
	output, err := command(binary, root, "status")
	if err != nil {
		t.Fatalf("status: %v: %s", err, output)
	}
	strict, fast := strings.Index(output, "Goal strict"), strings.Index(output, "Goal fast")
	if strict < 0 || fast < 0 || !strings.Contains(output[strict:fast], "Review policy: WORK_ITEM") || !strings.Contains(output[fast:], "Review policy: GOAL") {
		t.Fatalf("status does not show WORK_ITEM for the approving Goal and GOAL for the other:\n%s", output)
	}
}

func TestGoalImportRefusesBadPlansAndWritesNothing(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "specs", "stories", "escape")); err != nil {
		t.Fatal(err)
	}
	mustRun(t, binary, root, "goal", "import", writePlanText(t, planText("existing", false, "owned specs/stories/a.md")))
	before := stateBytes(t, root)

	cases := []struct {
		name string
		plan string
		want []string
	}{
		{"cycle", planText("g", false, "a specs/stories/a.md b", "b specs/stories/b.md a"), []string{"cycle"}},
		{"unknown dependency", planText("g", false, "a specs/stories/a.md ghost"), []string{`node "a"`, "depends_on", `"ghost"`}},
		{"self dependency", planText("g", false, "a specs/stories/a.md a"), []string{`node "a"`, "itself"}},
		{"duplicate dependency", planText("g", false, "a specs/stories/a.md", "b specs/stories/b.md a,a"), []string{`node "b"`, "duplicate"}},
		{"duplicate node id", planText("g", false, "a specs/stories/a.md", "a specs/stories/b.md"), []string{`node "a"`, "duplicated"}},
		{"illegal id character", planText("g", false, "a/b specs/stories/a.md"), []string{`"a/b"`, "not a valid ID"}},
		{"illegal goal id", planText("bad id", false, "a specs/stories/a.md"), []string{"goal.id", "bad id"}},
		{"node id owned by another goal", planText("g", false, "owned specs/stories/b.md"), []string{`node "owned"`, `"existing"`}},
		{"missing story file", planText("g", false, "good specs/stories/a.md", "bad specs/stories/missing.md"), []string{`node "bad"`, "story", "specs/stories"}},
		{"story traversal", planText("g", false, "bad specs/stories/../../Makefile"), []string{`node "bad"`, "story", ".."}},
		{"story symlink escape", planText("g", false, "bad specs/stories/escape"), []string{`node "bad"`, "story"}},
		{"story outside specs/stories", planText("g", false, "bad go.mod"), []string{`node "bad"`, "story"}},
		{"unknown field", `{"goal":{"id":"g","title":"T","colour":"red"},"nodes":[{"id":"a","story":"specs/stories/a.md"}]}`, []string{"unknown field", "colour"}},
		{"unknown node field", `{"goal":{"id":"g","title":"T"},"nodes":[{"id":"a","story":"specs/stories/a.md","needs":[]}]}`, []string{"unknown field", "needs"}},
		{"missing title", `{"goal":{"id":"g"},"nodes":[{"id":"a","story":"specs/stories/a.md"}]}`, []string{"goal.title"}},
		{"missing story", `{"goal":{"id":"g","title":"T"},"nodes":[{"id":"a"}]}`, []string{`node "a"`, "story"}},
		{"no nodes", `{"goal":{"id":"g","title":"T"},"nodes":[]}`, []string{"nodes"}},
		{"not json", "goal: g", []string{"goal plan"}},
	}
	for _, test := range cases {
		output, err := command(binary, root, "goal", "import", writePlanText(t, test.plan))
		if err == nil {
			t.Errorf("%s: import succeeded: %s", test.name, output)
			continue
		}
		for _, want := range test.want {
			if !strings.Contains(output, want) {
				t.Errorf("%s: output %q lacks %q", test.name, output, want)
			}
		}
		if after := stateBytes(t, root); after != before {
			t.Errorf("%s: a refused import changed state.json", test.name)
		}
	}

	// An unreadable plan file is refused as well. Together with the byte-for-byte
	// comparison above (the "missing story file" plan has a good node before the
	// bad one), this shows no refusal leaves part of a plan behind.
	if output, err := command(binary, root, "goal", "import", filepath.Join(t.TempDir(), "absent.json")); err == nil || !strings.Contains(output, "read goal plan") {
		t.Fatalf("missing plan file = %q, %v", output, err)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Goals) != 1 || len(state.WorkItems) != 1 {
		t.Fatalf("after every refusal state holds %d goals and %d work items, want the original 1 and 1", len(state.Goals), len(state.WorkItems))
	}
}

func TestGoalImportUsageAndRelativePlanPath(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	if output, err := command(binary, root, "goal", "import"); err == nil || !strings.Contains(output, "usage: forgepilot goal import <plan-path>") {
		t.Fatalf("goal import without a path = %q, %v", output, err)
	}
	if output, err := command(binary, root, "goal", "import", "a", "b"); err == nil || !strings.Contains(output, "usage") {
		t.Fatalf("goal import with two paths = %q, %v", output, err)
	}
	// A relative path is read from the directory the command runs in, which may
	// be a subdirectory of the repository.
	directory := filepath.Join(root, "plans")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "p.json"), []byte(planText("g", false, "a specs/stories/a.md")), 0644); err != nil {
		t.Fatal(err)
	}
	if output, err := command(binary, directory, "goal", "import", "p.json"); err != nil || !strings.Contains(output, "Goal g imported: 1 nodes") {
		t.Fatalf("relative import = %q, %v", output, err)
	}
}

func TestGoalReimportIsAppendOnly(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	original := planText("g", false, "a specs/stories/a.md", "b specs/stories/b.md a")
	mustRun(t, binary, root, "goal", "import", writePlanText(t, original))
	before := stateBytes(t, root)

	// Identical: success, says nothing changed, writes nothing.
	output, err := command(binary, root, "goal", "import", writePlanText(t, original))
	if err != nil || !strings.Contains(output, "Goal g unchanged") {
		t.Fatalf("identical re-import = %q, %v", output, err)
	}
	if stateBytes(t, root) != before {
		t.Fatal("an identical re-import rewrote state.json")
	}

	// Appending works, and new nodes may depend on old ones and each other.
	grown := planText("g", false, "a specs/stories/a.md", "b specs/stories/b.md a", "c specs/stories/c.md b", "d specs/stories/c.md c,a")
	output, err = command(binary, root, "goal", "import", writePlanText(t, grown))
	if err != nil || !strings.Contains(output, "Goal g updated: added 2 nodes") || !strings.Contains(output, "c PENDING") || !strings.Contains(output, "d PENDING") {
		t.Fatalf("append = %q, %v", output, err)
	}
	state, err := storage.Load(root)
	if err != nil || len(state.WorkItems) != 4 {
		t.Fatalf("after append: %d work items, %v", len(state.WorkItems), err)
	}
	grownState := stateBytes(t, root)

	// An append that arrives in a different dependency order is still identical.
	reordered := planText("g", false, "a specs/stories/a.md", "b specs/stories/b.md a", "c specs/stories/c.md b", "d specs/stories/c.md a,c")
	if output, err := command(binary, root, "goal", "import", writePlanText(t, reordered)); err != nil || !strings.Contains(output, "unchanged") {
		t.Fatalf("re-import with reordered depends_on = %q, %v; want an unchanged success", output, err)
	}

	rejections := []struct {
		name string
		plan string
		want string
	}{
		{"changed story", planText("g", false, "a specs/stories/c.md", "b specs/stories/b.md a", "c specs/stories/c.md b", "d specs/stories/c.md c,a"), `node "a"`},
		{"changed dependencies", planText("g", false, "a specs/stories/a.md", "b specs/stories/b.md", "c specs/stories/c.md b", "d specs/stories/c.md c,a"), `node "b"`},
		{"removed node", planText("g", false, "a specs/stories/a.md", "b specs/stories/b.md a", "c specs/stories/c.md b"), `node "d"`},
		{"changed approval", planText("g", true, "a specs/stories/a.md", "b specs/stories/b.md a", "c specs/stories/c.md b", "d specs/stories/c.md c,a"), "require_approval"},
		{"changed title", strings.Replace(grown, "Title of g", "Another title", 1), "goal.title"},
		{"changed description", strings.Replace(grown, `"title":"Title of g"`, `"title":"Title of g","description":"new"`, 1), "goal.description"},
		{"new node with a missing story", planText("g", false, "a specs/stories/a.md", "b specs/stories/b.md a", "c specs/stories/c.md b", "d specs/stories/c.md c,a", "e specs/stories/nope.md"), `node "e"`},
	}
	for _, test := range rejections {
		output, err := command(binary, root, "goal", "import", writePlanText(t, test.plan))
		if err == nil || !strings.Contains(output, test.want) {
			t.Errorf("%s: %q, %v; want a refusal mentioning %q", test.name, output, err, test.want)
		}
		if stateBytes(t, root) != grownState {
			t.Errorf("%s: a refused re-import changed state.json", test.name)
		}
	}

	// A cancelled Goal accepts nothing, not even the plan it already has.
	mustRun(t, binary, root, "goal", "cancel", "g", "--reason", "dropped")
	for _, plan := range []string{grown, planText("g", false, "a specs/stories/a.md", "b specs/stories/b.md a", "c specs/stories/c.md b", "d specs/stories/c.md c,a", "e specs/stories/c.md")} {
		if output, err := command(binary, root, "goal", "import", writePlanText(t, plan)); err == nil || !strings.Contains(output, "CANCELLED") {
			t.Fatalf("import into a cancelled Goal = %q, %v", output, err)
		}
	}
}

func TestGoalImportHintsAboutStoriesThatAreNotCommitted(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	output, err := command(binary, root, "goal", "import", writePlanText(t, planText("g", false, "first specs/stories/a.md")))
	if err != nil {
		t.Fatalf("goal import: %v: %s", err, output)
	}
	want := "Goal g imported: 1 nodes\n" +
		"  first READY specs/stories/a.md\n" +
		"    specs/stories/a.md is not committed yet; use `forgepilot verify first --snapshot` to verify the working tree, or commit it before commit-mode verification.\n"
	if output != want {
		t.Fatalf("import output:\nwant:\n%s\ngot:\n%s", want, output)
	}

	// Committed and clean: no hint, even with unrelated noise in the worktree.
	commitAll(t, root, "seed stories")
	if err := os.WriteFile(filepath.Join(root, "unrelated.txt"), []byte("noise\n"), 0644); err != nil {
		t.Fatal(err)
	}
	output, err = command(binary, root, "goal", "import", writePlanText(t, planText("g", false, "first specs/stories/a.md", "second specs/stories/b.md")))
	if err != nil {
		t.Fatalf("goal import: %v: %s", err, output)
	}
	want = "Goal g updated: added 1 nodes\n  second READY specs/stories/b.md\n"
	if output != want {
		t.Fatalf("committed-story import output:\nwant:\n%s\ngot:\n%s", want, output)
	}

	// Committed but modified: the hint names the new node, not the old ones.
	if err := os.WriteFile(filepath.Join(root, "specs", "stories", "c.md"), []byte("# story\nmore\n"), 0644); err != nil {
		t.Fatal(err)
	}
	output, err = command(binary, root, "goal", "import", writePlanText(t, planText("g", false, "first specs/stories/a.md", "second specs/stories/b.md", "third specs/stories/c.md")))
	if err != nil || !strings.Contains(output, "specs/stories/c.md is not committed yet; use `forgepilot verify third --snapshot`") {
		t.Fatalf("modified-story import = %q, %v", output, err)
	}
}

func TestGoalImportWithoutStoriesDirectoryNamesTheMissingDirectory(t *testing.T) {
	root, binary := fixtureWithoutStories(t)
	mustRun(t, binary, root, "init")
	output, err := command(binary, root, "goal", "import", writePlanText(t, planText("g", false, "a specs/stories/a.md")))
	if err == nil {
		t.Fatalf("unexpectedly succeeded: %s", output)
	}
	if !strings.Contains(output, "specs/stories does not exist") || !strings.Contains(output, "ForgePilot expects PraxisBound Story files") || !strings.Contains(output, `node "a"`) {
		t.Fatalf("output %q does not name the node and the missing directory", output)
	}
	if strings.Contains(output, "lstat") {
		t.Fatalf("output %q leaks an internal call name", output)
	}
}

func TestOldSchemaStateIsRefusedWithTheExportInstructions(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	// A v18 state carries fields the current shape has dropped; the refusal must
	// be about the version, not about an unknown field.
	legacy := `{"schema_version":18,"next_work_id":2,"next_evidence_id":1,"next_gate_id":1,"next_verification_run_id":1,"next_goal_completion_evidence_id":1,` +
		`"goals":[{"id":"g","title":"G","description":"","repository":"` + root + `","status":"ACTIVE","review_policy":"WORK_ITEM","completion_policy":"HUMAN","execution":{},"reason":"","created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"}],` +
		`"work_items":[{"id":"WI-001","goal_id":"g","story_ref":"specs/stories/a.md","external_ref":"PB-1","status":"READY","depends_on":null,"current_run":null,"created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"}],` +
		`"evidence":[],"goal_completion_evidence":[],"gates":[]}`
	path := filepath.Join(root, ".forgepilot", "state.json")
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	for _, arguments := range [][]string{
		{"status"}, {"next"}, {"start", "WI-001"}, {"goal", "import", writePlanText(t, planText("n", false, "a specs/stories/a.md"))},
	} {
		output, err := command(binary, root, arguments...)
		if err == nil {
			t.Fatalf("%v ran against a schema 18 state: %s", arguments[0], output)
		}
		for _, want := range []string{"schema version 18", "tools/export-plan", "--state", "--out", "goal import"} {
			if !strings.Contains(output, want) {
				t.Errorf("%v: output %q lacks %q", arguments[0], output, want)
			}
		}
		if strings.Contains(output, "unknown field") {
			t.Errorf("%v: refusal is an unknown-field error, not a version error: %s", arguments[0], output)
		}
	}
	if raw, _ := os.ReadFile(path); string(raw) != legacy {
		t.Fatal("a refused command rewrote the old state")
	}

	// A newer state is refused too, without the export instructions.
	newer := strings.Replace(legacy, `"schema_version":18`, `"schema_version":20`, 1)
	if err := os.WriteFile(path, []byte(newer), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := command(binary, root, "status")
	if err == nil || !strings.Contains(output, "newer than this binary supports") || strings.Contains(output, "export-plan") {
		t.Fatalf("newer schema = %q, %v", output, err)
	}
}

func TestConcurrentImportsOfDifferentGoalsKeepBoth(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	plans := map[string]string{
		"one": writePlanText(t, planText("one", false, "a1 specs/stories/a.md", "a2 specs/stories/b.md a1")),
		"two": writePlanText(t, planText("two", false, "b1 specs/stories/c.md", "b2 specs/stories/c.md b1", "b3 specs/stories/c.md b2")),
	}
	var group sync.WaitGroup
	failures := make(chan error, len(plans))
	for _, path := range plans {
		group.Add(1)
		go func() {
			defer group.Done()
			if output, err := command(binary, root, "goal", "import", path); err != nil {
				failures <- &commandError{err, output}
			}
		}()
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
	counts := map[string]int{}
	for _, item := range state.WorkItems {
		counts[item.GoalID]++
	}
	if len(state.Goals) != 2 || counts["one"] != 2 || counts["two"] != 3 {
		t.Fatalf("goals %d, nodes per goal %v; want both goals whole (2 and 3 nodes)", len(state.Goals), counts)
	}
}

func TestConcurrentImportsOfTheSamePlanCreateItOnce(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	path := writePlanText(t, planText("g", false, "a specs/stories/a.md", "b specs/stories/b.md a", "c specs/stories/c.md a"))

	const racers = 4
	var group sync.WaitGroup
	outputs := make(chan string, racers)
	failures := make(chan error, racers)
	for range racers {
		group.Add(1)
		go func() {
			defer group.Done()
			output, err := command(binary, root, "goal", "import", path)
			if err != nil {
				failures <- &commandError{err, output}
				return
			}
			outputs <- output
		}()
	}
	group.Wait()
	close(outputs)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	created, unchanged := 0, 0
	for output := range outputs {
		switch {
		case strings.Contains(output, "Goal g imported: 3 nodes"):
			created++
		case strings.Contains(output, "Goal g unchanged"):
			unchanged++
		default:
			t.Fatalf("unexpected import output %q", output)
		}
	}
	if created != 1 || unchanged != racers-1 {
		t.Fatalf("%d imports created the Goal and %d reported no change; want exactly 1 and %d", created, unchanged, racers-1)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Goals) != 1 || len(state.WorkItems) != 3 {
		t.Fatalf("state holds %d goals and %d work items, want 1 and 3", len(state.Goals), len(state.WorkItems))
	}
}
