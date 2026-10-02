package forgepilot_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// Fixtures build their Goals the way users do: through `goal import`. These
// helpers keep, per repository, the Goal Plan each Goal has been given so far.
// createGoal only records the Goal; addWork appends one node to its plan and
// re-imports the whole plan, so every Work Item in every test passed through the
// product's import path. Nodes are named WI-001, WI-002, ... in the order they
// were added across the repository, which is the naming the tests grew up with.
var plans = struct {
	sync.Mutex
	byGoal  map[string]*work.GoalPlan
	counter map[string]int
}{byGoal: map[string]*work.GoalPlan{}, counter: map[string]int{}}

func createGoal(t *testing.T, binary, root, id, title string, requireApproval bool) {
	t.Helper()
	plans.Lock()
	defer plans.Unlock()
	plans.byGoal[root+"\x00"+id] = &work.GoalPlan{Goal: work.PlanGoal{ID: id, Title: title, RequireApproval: requireApproval}}
}

// tryAddWork imports the Goal's plan extended with one more node and returns the
// import's output. The extension is remembered only if the import succeeded.
func tryAddWork(t *testing.T, binary, root, goalID, story string, dependsOn ...string) (string, error) {
	t.Helper()
	plans.Lock()
	defer plans.Unlock()
	current, ok := plans.byGoal[root+"\x00"+goalID]
	if !ok {
		t.Fatalf("goal %q was never created with createGoal", goalID)
	}
	candidate := *current
	candidate.Nodes = append([]work.PlanNode(nil), current.Nodes...)
	id := fmt.Sprintf("WI-%03d", plans.counter[root]+1)
	candidate.Nodes = append(candidate.Nodes, work.PlanNode{ID: id, Story: story, DependsOn: append([]string{}, dependsOn...)})

	output, err := command(binary, root, "goal", "import", writePlan(t, candidate))
	if err == nil {
		plans.byGoal[root+"\x00"+goalID] = &candidate
		plans.counter[root]++
	}
	return output, err
}

// addWork is tryAddWork that must succeed; it returns the new node's ID.
func addWork(t *testing.T, binary, root, goalID, story string, dependsOn ...string) string {
	t.Helper()
	output, err := tryAddWork(t, binary, root, goalID, story, dependsOn...)
	if err != nil {
		t.Fatalf("goal import for %s %s: %v: %s", goalID, story, err, output)
	}
	plans.Lock()
	defer plans.Unlock()
	nodes := plans.byGoal[root+"\x00"+goalID].Nodes
	return nodes[len(nodes)-1].ID
}

// writePlan writes a plan outside the repository, so that it never dirties the
// worktree the tests verify.
func writePlan(t *testing.T, plan work.GoalPlan) string {
	t.Helper()
	encoded, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return writePlanText(t, string(encoded))
}

func writePlanText(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}
