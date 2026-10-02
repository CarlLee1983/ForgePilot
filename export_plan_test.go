package forgepilot_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// The export tool is a separate program with its own idea of the Goal Plan
// format. This runs it on a representative schema-18 state and hands what it
// wrote to the real `goal import`, so the two cannot drift apart unnoticed.
func TestExportedPlansAreAcceptedByGoalImport(t *testing.T) {
	root, binary := fixture(t)

	tool := filepath.Join(t.TempDir(), "export-plan")
	build := exec.Command("go", "build", "-o", tool, "./tools/export-plan")
	build.Dir = projectRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build export-plan: %v: %s", err, output)
	}
	plansDir := filepath.Join(t.TempDir(), "plans")
	legacy := filepath.Join(projectRoot(t), "tools", "export-plan", "testdata", "v18-state.json")
	if output, err := exec.Command(tool, "--state", legacy, "--out", plansDir).CombinedOutput(); err != nil {
		t.Fatalf("export-plan: %v: %s", err, output)
	}

	// The old state is refused by the new binary: that is what sent us here.
	mustRun(t, binary, root, "init")
	if err := os.WriteFile(filepath.Join(root, ".forgepilot", "state.json"), mustRead(t, legacy), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := command(binary, root, "status"); err == nil || !strings.Contains(output, "tools/export-plan") {
		t.Fatalf("status on the old state = %q, %v", output, err)
	}
	// The migration the refusal describes: move the old state aside, init anew.
	if err := os.RemoveAll(filepath.Join(root, ".forgepilot")); err != nil {
		t.Fatal(err)
	}
	mustRun(t, binary, root, "init")

	entries, err := os.ReadDir(plansDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	if len(names) != 3 {
		t.Fatalf("exported %v, want 3 plans", names)
	}
	for _, name := range names {
		if output, err := command(binary, root, "goal", "import", filepath.Join(plansDir, name)); err != nil {
			t.Fatalf("goal import %s: %v: %s", name, err, output)
		}
	}

	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]work.Status{}
	for _, item := range state.WorkItems {
		statuses[item.ID] = item.Status
	}
	want := map[string]work.Status{
		"WI-002": work.Ready, "WI-003": work.Pending, // billing: WI-001 was DONE
		"WI-005": work.Ready, "WI-006": work.Pending, // fast.lane: WI-004 was VERIFIED
		"WI-007": work.Ready, // paused
	}
	if len(statuses) != len(want) {
		t.Fatalf("imported work items %v, want exactly %v", statuses, want)
	}
	for id, status := range want {
		if statuses[id] != status {
			t.Errorf("%s is %s, want %s", id, statuses[id], status)
		}
	}
	for goalID, policy := range map[string]work.ReviewPolicy{"billing": work.ReviewPerWorkItem, "fast.lane": work.ReviewPerGoal, "paused": work.ReviewPerWorkItem} {
		if goal, ok := state.GoalByID(goalID); !ok || goal.ReviewPolicy != policy || goal.Status != work.GoalActive {
			t.Errorf("goal %s = %#v, %v; want ACTIVE under %s", goalID, goal, ok, policy)
		}
	}
	if output, err := command(binary, root, "next"); err != nil || !strings.Contains(output, "Next: WI-002") {
		t.Fatalf("next = %q, %v; want the first READY node of the first imported Goal", output, err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}
