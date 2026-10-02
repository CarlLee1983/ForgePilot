package work

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

var planNow = time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)

func node(id string, dependsOn ...string) PlanNode {
	return PlanNode{ID: id, Story: "specs/stories/" + id, DependsOn: dependsOn}
}

func planOf(nodes ...PlanNode) GoalPlan {
	return GoalPlan{Goal: PlanGoal{ID: "billing", Title: "Billing sync", Description: "sync it"}, Nodes: nodes}
}

func TestParseGoalPlanIsStrict(t *testing.T) {
	valid := `{"goal":{"id":"g","title":"T","description":"D","require_approval":true},"nodes":[{"id":"a","story":"specs/stories/a","depends_on":[]},{"id":"b","story":"specs/stories/b","depends_on":["a"]}]}`
	plan, err := ParseGoalPlan([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	want := GoalPlan{
		Goal:  PlanGoal{ID: "g", Title: "T", Description: "D", RequireApproval: true},
		Nodes: []PlanNode{{ID: "a", Story: "specs/stories/a", DependsOn: []string{}}, {ID: "b", Story: "specs/stories/b", DependsOn: []string{"a"}}},
	}
	if !reflect.DeepEqual(plan, want) {
		t.Fatalf("parsed %#v, want %#v", plan, want)
	}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}

	for name, input := range map[string]string{
		"unknown goal field": `{"goal":{"id":"g","title":"T","colour":"red"},"nodes":[]}`,
		"unknown node field": `{"goal":{"id":"g","title":"T"},"nodes":[{"id":"a","story":"s","depends":[]}]}`,
		"unknown top field":  `{"goal":{"id":"g","title":"T"},"nodes":[],"extra":1}`,
		"trailing value":     valid + ` {}`,
		"not json":           `goal: g`,
		"wrong type":         `{"goal":{"id":"g","title":"T","require_approval":"yes"},"nodes":[]}`,
	} {
		if _, err := ParseGoalPlan([]byte(input)); err == nil {
			t.Errorf("%s: parsed without error", name)
		}
	}
}

func TestValidPlanID(t *testing.T) {
	long := strings.Repeat("a", 64)
	for id, want := range map[string]bool{
		"a": true, "WI-001": true, "sync.job_2": true, "9lives": true, long: true,
		"": false, long + "a": false, "-lead": false, ".lead": false, "_lead": false,
		"a..b": false, "x.lock": false, "a.": false, "a.lock.b": true, "lock": true, "has space": false, "slash/y": false, "dollar$": false, "tab\t": false, "é": false, "a:b": false,
	} {
		if got := ValidPlanID(id); got != want {
			t.Errorf("ValidPlanID(%q) = %t, want %t", id, got, want)
		}
	}
}

func TestPlanValidationNamesTheNodeAndField(t *testing.T) {
	cases := []struct {
		name string
		plan GoalPlan
		want []string // every fragment must appear in the error
	}{
		{"missing goal id", GoalPlan{Goal: PlanGoal{Title: "T"}, Nodes: []PlanNode{node("a")}}, []string{"goal.id"}},
		{"bad goal id", GoalPlan{Goal: PlanGoal{ID: "bad id", Title: "T"}, Nodes: []PlanNode{node("a")}}, []string{"goal.id", "bad id"}},
		{"missing title", GoalPlan{Goal: PlanGoal{ID: "g"}, Nodes: []PlanNode{node("a")}}, []string{"goal.title"}},
		{"no nodes", planOf(), []string{"nodes"}},
		{"missing node id", planOf(PlanNode{Story: "s"}), []string{"nodes[0].id"}},
		{"bad node id", planOf(node("ok"), PlanNode{ID: "bad/id", Story: "s"}), []string{`"bad/id"`, "id"}},
		{"duplicate node id", planOf(node("a"), node("b"), node("a")), []string{`"a"`, "duplicated"}},
		{"missing story", planOf(PlanNode{ID: "a"}), []string{`"a"`, "story"}},
		{"self dependency", planOf(node("a", "a")), []string{`"a"`, "depends_on", "itself"}},
		{"duplicate dependency", planOf(node("a"), node("b", "a", "a")), []string{`"b"`, "depends_on", `"a"`, "duplicate"}},
		{"unknown dependency", planOf(node("a", "ghost")), []string{`"a"`, "depends_on", `"ghost"`, "unknown"}},
		{"two node cycle", planOf(node("a", "b"), node("b", "a")), []string{"cycle", "a -> b -> a"}},
		{"long cycle behind a tail", planOf(node("tail", "x"), node("x", "y"), node("y", "z"), node("z", "x")), []string{"cycle", "x -> y -> z -> x"}},
	}
	for _, test := range cases {
		err := test.plan.Validate()
		if err == nil {
			t.Errorf("%s: accepted", test.name)
			continue
		}
		for _, fragment := range test.want {
			if !strings.Contains(err.Error(), fragment) {
				t.Errorf("%s: error %q lacks %q", test.name, err, fragment)
			}
		}
	}

	// A diamond shares a dependency without cycling; it must not be mistaken for one.
	diamond := planOf(node("top", "left", "right"), node("left", "base"), node("right", "base"), node("base"))
	if err := diamond.Validate(); err != nil {
		t.Fatalf("diamond rejected: %v", err)
	}
}

func TestImportCreatesGoalAndWholeDAGInPlanOrder(t *testing.T) {
	state := NewState()
	plan := planOf(node("sync-job", "schema"), node("schema"), node("report", "schema", "sync-job"), node("docs"))
	plan.Goal.RequireApproval = true
	result, err := state.ImportGoalPlan(plan, "/repo", RepositoryState{}, planNow)
	if err != nil {
		t.Fatal(err)
	}
	if !result.GoalCreated || len(result.Added) != 4 || !result.Changed() {
		t.Fatalf("result = %#v", result)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("imported state is invalid: %v", err)
	}
	goal, _ := state.GoalByID("billing")
	if goal.Title != "Billing sync" || goal.Description != "sync it" || goal.Repository != "/repo" || goal.Status != GoalActive || goal.ReviewPolicy != ReviewPerWorkItem || goal.CompletionPolicy != CompletionHuman {
		t.Fatalf("goal = %#v", goal)
	}
	var order []string
	status := map[string]Status{}
	for _, item := range state.WorkItems {
		order = append(order, item.ID)
		status[item.ID] = item.Status
		if item.GoalID != "billing" {
			t.Errorf("%s belongs to %q", item.ID, item.GoalID)
		}
	}
	if !reflect.DeepEqual(order, []string{"sync-job", "schema", "report", "docs"}) {
		t.Fatalf("work item order = %v, want the plan's node order", order)
	}
	if status["schema"] != Ready || status["docs"] != Ready || status["sync-job"] != Pending || status["report"] != Pending {
		t.Fatalf("readiness = %v", status)
	}
	// Node order is the tie-break between READY work: schema precedes docs.
	if next, ok := state.Next(); !ok || next.ID != "schema" {
		t.Fatalf("next = %#v, %v; want schema, the first READY node", next, ok)
	}
}

func TestImportMapsApprovalToTheTransitionalReviewPolicy(t *testing.T) {
	for _, requireApproval := range []bool{true, false} {
		state := NewState()
		plan := planOf(node("a"))
		plan.Goal.RequireApproval = requireApproval
		if _, err := state.ImportGoalPlan(plan, "/repo", RepositoryState{}, planNow); err != nil {
			t.Fatal(err)
		}
		goal, _ := state.GoalByID("billing")
		wantPolicy, wantCompletion := ReviewPerGoal, CompletionVerified
		if requireApproval {
			wantPolicy, wantCompletion = ReviewPerWorkItem, CompletionHuman
		}
		if goal.ReviewPolicy != wantPolicy || goal.CompletionPolicy != wantCompletion {
			t.Errorf("require_approval=%t gave %s/%s, want %s/%s", requireApproval, goal.ReviewPolicy, goal.CompletionPolicy, wantPolicy, wantCompletion)
		}
	}
}

func TestInvalidImportChangesNothing(t *testing.T) {
	state := NewState()
	if _, err := state.ImportGoalPlan(planOf(node("a")), "/repo", RepositoryState{}, planNow); err != nil {
		t.Fatal(err)
	}
	before := state
	before.Goals = append([]Goal(nil), state.Goals...)
	before.WorkItems = append([]Item(nil), state.WorkItems...)

	other := GoalPlan{Goal: PlanGoal{ID: "other", Title: "Other"}, Nodes: []PlanNode{node("fresh"), node("a")}}
	if _, err := state.ImportGoalPlan(other, "/repo", RepositoryState{}, planNow); err == nil || !strings.Contains(err.Error(), `"a"`) || !strings.Contains(err.Error(), "billing") {
		t.Fatalf("a node ID owned by another goal: %v", err)
	}
	cycle := planOf(node("a"), node("new1", "new2"), node("new2", "new1"))
	if _, err := state.ImportGoalPlan(cycle, "/repo", RepositoryState{}, planNow); err == nil {
		t.Fatal("accepted a cycle")
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatalf("a refused import changed state:\nbefore %#v\nafter  %#v", before, state)
	}
}

func TestReimportRules(t *testing.T) {
	base := func() (State, GoalPlan) {
		state := NewState()
		plan := planOf(node("a"), node("b", "a"))
		if _, err := state.ImportGoalPlan(plan, "/repo", RepositoryState{}, planNow); err != nil {
			t.Fatal(err)
		}
		return state, plan
	}
	clone := func(plan GoalPlan) GoalPlan {
		out := plan
		out.Nodes = append([]PlanNode(nil), plan.Nodes...)
		return out
	}
	later := planNow.Add(time.Hour)

	t.Run("identical plan is a no-op", func(t *testing.T) {
		state, plan := base()
		before := state
		before.Goals = append([]Goal(nil), state.Goals...)
		before.WorkItems = append([]Item(nil), state.WorkItems...)
		result, err := state.ImportGoalPlan(clone(plan), "/repo", RepositoryState{}, later)
		if err != nil {
			t.Fatal(err)
		}
		if result.Changed() || len(result.Added) != 0 {
			t.Fatalf("result = %#v, want no change", result)
		}
		if !reflect.DeepEqual(state, before) {
			t.Fatal("a no-op import changed state, including timestamps")
		}
	})

	t.Run("dependency order is not a change", func(t *testing.T) {
		state := NewState()
		plan := planOf(node("a"), node("b"), node("c", "a", "b"))
		if _, err := state.ImportGoalPlan(plan, "/repo", RepositoryState{}, planNow); err != nil {
			t.Fatal(err)
		}
		reordered := clone(plan)
		reordered.Nodes[2] = node("c", "b", "a")
		if result, err := state.ImportGoalPlan(reordered, "/repo", RepositoryState{}, later); err != nil || result.Changed() {
			t.Fatalf("reordered depends_on: %#v, %v; want an unchanged success", result, err)
		}
	})

	t.Run("new nodes depend on new and existing nodes", func(t *testing.T) {
		// Approval is required so DONE is a legal status for the finished node.
		state := NewState()
		plan := planOf(node("a"), node("b", "a"))
		plan.Goal.RequireApproval = true
		if _, err := state.ImportGoalPlan(plan, "/repo", RepositoryState{}, planNow); err != nil {
			t.Fatal(err)
		}
		state.item("a").Status = Done // a finished node can still be depended on
		plan.Nodes = append(plan.Nodes, node("c", "a"), node("d", "c", "b"))
		result, err := state.ImportGoalPlan(plan, "/repo", RepositoryState{}, later)
		if err != nil {
			t.Fatal(err)
		}
		if result.GoalCreated || len(result.Added) != 2 || result.Added[0].ID != "c" || result.Added[1].ID != "d" {
			t.Fatalf("result = %#v", result)
		}
		if err := state.Validate(); err != nil {
			t.Fatal(err)
		}
		if got := state.item("c").Status; got != Ready {
			t.Errorf("c depends only on a DONE node and is %s, want READY", got)
		}
		if got := state.item("d").Status; got != Pending {
			t.Errorf("d depends on unfinished nodes and is %s, want PENDING", got)
		}
		if got := state.item("a").Status; got != Done {
			t.Errorf("re-import rewrote a's status to %s", got)
		}
		var order []string
		for _, item := range state.WorkItems {
			order = append(order, item.ID)
		}
		if !reflect.DeepEqual(order, []string{"a", "b", "c", "d"}) {
			t.Errorf("order = %v; new nodes must follow existing ones", order)
		}
		goal, _ := state.GoalByID("billing")
		if !goal.UpdatedAt.Equal(later) {
			t.Errorf("goal UpdatedAt = %v, want %v", goal.UpdatedAt, later)
		}
	})

	rejections := []struct {
		name   string
		mutate func(state *State, plan *GoalPlan)
		want   string
	}{
		{"changed story", func(_ *State, p *GoalPlan) { p.Nodes[0].Story = "specs/stories/elsewhere" }, `node "a": story`},
		{"changed dependencies", func(_ *State, p *GoalPlan) { p.Nodes[1].DependsOn = nil }, `node "b": depends_on`},
		{"new dependency on an existing node", func(_ *State, p *GoalPlan) { p.Nodes[0].DependsOn = []string{"c"} }, `node "a": depends_on`},
		{"removed node", func(_ *State, p *GoalPlan) { p.Nodes = p.Nodes[:1] }, `node "b"`},
		{"changed title", func(_ *State, p *GoalPlan) { p.Goal.Title = "Renamed" }, "goal.title"},
		{"changed description", func(_ *State, p *GoalPlan) { p.Goal.Description = "other" }, "goal.description"},
		{"changed approval", func(_ *State, p *GoalPlan) { p.Goal.RequireApproval = true }, "goal.require_approval"},
		{"completed goal", func(s *State, _ *GoalPlan) { s.Goals[0].Status = GoalCompleted }, "COMPLETED"},
		{"cancelled goal", func(s *State, _ *GoalPlan) { s.Goals[0].Status, s.Goals[0].Reason = GoalCancelled, "dropped" }, "CANCELLED"},
		{"blocked goal", func(s *State, _ *GoalPlan) { s.Goals[0].Status, s.Goals[0].Reason = GoalBlocked, "paused" }, "BLOCKED"},
	}
	for _, test := range rejections {
		t.Run("rejects "+test.name, func(t *testing.T) {
			state, plan := base()
			plan = clone(plan)
			plan.Nodes = append(plan.Nodes, node("c")) // a legal addition must not rescue a bad plan
			test.mutate(&state, &plan)
			before := state
			before.Goals = append([]Goal(nil), state.Goals...)
			before.WorkItems = append([]Item(nil), state.WorkItems...)
			_, err := state.ImportGoalPlan(plan, "/repo", RepositoryState{}, later)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want one mentioning %q", err, test.want)
			}
			if !reflect.DeepEqual(state, before) {
				t.Fatal("a refused re-import changed state")
			}
		})
	}
}
