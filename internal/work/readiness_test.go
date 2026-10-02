package work

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// encoded is a deep, comparable copy of a State: a struct copy would share its
// slices, so a mutation through them would go unnoticed by an equality check.
func encoded(t *testing.T, state *State) string {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestReadinessIsComputedFromDependencies(t *testing.T) {
	// Each case names the work item to read and the readiness it must have after
	// the listed items have been forced to the given persisted statuses.
	cases := []struct {
		name     string
		statuses map[string]Status
		id       string
		want     string
	}{
		{"no dependencies", nil, "a", "READY"},
		{"nothing done", nil, "c", "PENDING"},
		{"one of two dependencies done", map[string]Status{"a": Done}, "c", "PENDING"},
		{"every dependency done", map[string]Status{"a": Done, "b": Done}, "c", "READY"},
		{"dependency in REVIEW is not done", map[string]Status{"a": Done, "b": Review}, "c", "PENDING"},
		{"dependency running", map[string]Status{"a": Done, "b": Running}, "c", "PENDING"},
		{"started work has no readiness", map[string]Status{"a": Running}, "a", "RUNNING"},
		{"done work has no readiness", map[string]Status{"a": Done}, "a", "DONE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := lifecycleState(t, true, "a", "b", "c<a,b")
			for id, status := range tc.statuses {
				state.item(id).Status = status
			}
			if got := state.DisplayStatus(tc.id); got != tc.want {
				t.Fatalf("%s displays %s, want %s", tc.id, got, tc.want)
			}
			_, hasReadiness := state.Readiness(tc.id)
			if hasReadiness != (tc.want == "READY" || tc.want == "PENDING") {
				t.Fatalf("Readiness ok = %v for displayed status %s", hasReadiness, tc.want)
			}
		})
	}
}

// Completing a dependency changes only the dependency: its dependents' readiness
// follows from reading, and no downstream record is written.
func TestCompletionWritesNothingDownstream(t *testing.T) {
	state := lifecycleState(t, false, "a", "b<a")
	before := encoded(t, &State{WorkItems: state.WorkItems[1:2]})
	updatedBefore := state.item("b").UpdatedAt
	runVerification(t, &state, "a", commitAt(revisionOne), 0)
	if state.item("b").Status != NotStarted || !state.item("b").UpdatedAt.Equal(updatedBefore) {
		t.Fatalf("b was written: %#v", state.item("b"))
	}
	if state.DisplayStatus("b") != "READY" {
		t.Fatalf("b displays %s, want READY", state.DisplayStatus("b"))
	}
	if encoded(t, &State{WorkItems: state.WorkItems[1:2]}) != before {
		t.Fatal("b changed")
	}
}

func TestSingleOccupantRule(t *testing.T) {
	t.Run("RUNNING work refuses another start", func(t *testing.T) {
		state := lifecycleState(t, false, "a", "b")
		if err := state.Start("a", lifecycleNow); err != nil {
			t.Fatal(err)
		}
		err := state.Start("b", lifecycleNow)
		if err == nil || !strings.Contains(err.Error(), `"a"`) || !strings.Contains(err.Error(), "RUNNING") {
			t.Fatalf("start b = %v, want a refusal naming a and its RUNNING status", err)
		}
		if state.item("b").Status != NotStarted {
			t.Fatal("a refused start changed the work item")
		}
	})

	t.Run("VERIFYING work refuses another start and says how to recover", func(t *testing.T) {
		state := lifecycleState(t, false, "a", "b")
		if err := state.Start("a", lifecycleNow); err != nil {
			t.Fatal(err)
		}
		if err := state.BeginCandidateVerification("a", commitAt(revisionOne), "/tmp/w", "", lifecycleNow); err != nil {
			t.Fatal(err)
		}
		err := state.Start("b", lifecycleNow)
		if err == nil || !strings.Contains(err.Error(), `"a"`) || !strings.Contains(err.Error(), "VERIFYING") || !strings.Contains(err.Error(), "forgepilot verify a") {
			t.Fatalf("start b = %v, want a refusal naming a, VERIFYING and the verify command", err)
		}
	})

	t.Run("REVIEW work does not occupy", func(t *testing.T) {
		state := lifecycleState(t, true, "a", "b")
		runVerification(t, &state, "a", commitAt(revisionOne), 0)
		wantStatus(t, &state, "a", string(Review))
		if err := state.Start("b", lifecycleNow); err != nil {
			t.Fatalf("start b while a is in REVIEW: %v", err)
		}
	})

	t.Run("the slot spans Goals", func(t *testing.T) {
		state := lifecycleState(t, false, "a")
		other := GoalPlan{Goal: PlanGoal{ID: "h", Title: "Other"}, Nodes: []PlanNode{{ID: "h1", Story: "specs/stories/h1", DependsOn: []string{}}}}
		if _, err := state.ImportGoalPlan(other, "/repo", lifecycleNow); err != nil {
			t.Fatal(err)
		}
		if err := state.Start("a", lifecycleNow); err != nil {
			t.Fatal(err)
		}
		if err := state.Start("h1", lifecycleNow); err == nil {
			t.Fatal("started work of another Goal while a is RUNNING")
		}
	})

	t.Run("work of a cancelled Goal does not occupy", func(t *testing.T) {
		state := lifecycleState(t, false, "a")
		other := GoalPlan{Goal: PlanGoal{ID: "h", Title: "Other"}, Nodes: []PlanNode{{ID: "h1", Story: "specs/stories/h1", DependsOn: []string{}}}}
		if _, err := state.ImportGoalPlan(other, "/repo", lifecycleNow); err != nil {
			t.Fatal(err)
		}
		if err := state.Start("a", lifecycleNow); err != nil {
			t.Fatal(err)
		}
		if err := state.CancelGoal("g", "dropped", lifecycleNow); err != nil {
			t.Fatal(err)
		}
		if err := state.Start("h1", lifecycleNow); err != nil {
			t.Fatalf("a cancelled Goal's RUNNING work still holds the slot: %v", err)
		}
		if err := state.Validate(); err != nil {
			t.Fatalf("state with an ended Goal's RUNNING work and a live one is invalid: %v", err)
		}
	})

	t.Run("rejecting REVIEW work is refused while another item runs", func(t *testing.T) {
		state := lifecycleState(t, true, "a", "b")
		runVerification(t, &state, "a", commitAt(revisionOne), 0)
		if err := state.Start("b", lifecycleNow); err != nil {
			t.Fatal(err)
		}
		if _, err := state.RecordReview("a", revisionOne, Rejected, "carl", "redo", lifecycleNow); err == nil {
			t.Fatal("rejected a into RUNNING while b holds the slot")
		}
		wantStatus(t, &state, "a", string(Review))
	})

	t.Run("re-verifying REVIEW work is refused while another item runs", func(t *testing.T) {
		state := lifecycleState(t, true, "a", "b")
		runVerification(t, &state, "a", commitAt(revisionOne), 0)
		if err := state.Start("b", lifecycleNow); err != nil {
			t.Fatal(err)
		}
		if err := state.BeginCandidateVerification("a", commitAt(revisionTwo), "/tmp/w", "", lifecycleNow); err == nil {
			t.Fatal("began verifying a while b holds the slot")
		}
	})

	t.Run("Validate refuses two occupants", func(t *testing.T) {
		state := lifecycleState(t, false, "a", "b")
		if err := state.Validate(); err != nil {
			t.Fatal(err)
		}
		state.item("a").Status, state.item("b").Status = Running, Running
		if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "one RUNNING or VERIFYING") {
			t.Fatalf("Validate = %v, want it to refuse two RUNNING items", err)
		}
	})
}

func TestStartRefusesWorkWithUnfinishedDependenciesNamingThem(t *testing.T) {
	state := lifecycleState(t, false, "a", "b", "c<a,b")
	state.item("a").Status = Done
	err := state.Start("c", lifecycleNow)
	if err == nil || !strings.Contains(err.Error(), "b") || strings.Contains(err.Error(), "a,") {
		t.Fatalf("start c = %v, want a refusal naming only the unfinished dependency b", err)
	}
}

func TestObstaclesExplainWhyUnfinishedWorkCannotAdvance(t *testing.T) {
	kinds := func(obstacles []Obstacle) string {
		var parts []string
		for _, obstacle := range obstacles {
			parts = append(parts, string(obstacle.Kind)+":"+obstacle.Ref)
		}
		return strings.Join(parts, ",")
	}
	cases := []struct {
		name  string
		setup func(t *testing.T, state *State)
		id    string
		want  string
	}{
		{"READY and free to start", func(t *testing.T, state *State) {}, "a", ""},
		{"dependencies not DONE are listed", func(t *testing.T, state *State) {}, "c", "DEPENDENCY:a,DEPENDENCY:b"},
		{"only the unfinished dependency is listed", func(t *testing.T, state *State) { state.item("a").Status = Done }, "c", "DEPENDENCY:b"},
		{"open Gate", func(t *testing.T, state *State) {
			if _, err := state.OpenGate("a", "Q?", []string{"x", "y"}, "", lifecycleNow); err != nil {
				t.Fatal(err)
			}
		}, "a", "GATE:GATE-001"},
		{"READY work held out by RUNNING work", func(t *testing.T, state *State) {
			if err := state.Start("b", lifecycleNow); err != nil {
				t.Fatal(err)
			}
		}, "a", "OCCUPIED:b"},
		{"REVIEW awaits approval", func(t *testing.T, state *State) {
			runVerification(t, state, "a", commitAt(revisionOne), 0)
		}, "a", "AWAITING_APPROVAL:"},
		{"the running work itself has none", func(t *testing.T, state *State) {
			if err := state.Start("a", lifecycleNow); err != nil {
				t.Fatal(err)
			}
		}, "a", ""},
		{"cancelled Goal", func(t *testing.T, state *State) {
			if err := state.CancelGoal("g", "dropped", lifecycleNow); err != nil {
				t.Fatal(err)
			}
		}, "a", "GOAL_ENDED:g"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := lifecycleState(t, true, "a", "b", "c<a,b")
			tc.setup(t, &state)
			if got := kinds(state.Obstacles(tc.id)); got != tc.want {
				t.Fatalf("obstacles of %s = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}

func TestNextPriorityOrder(t *testing.T) {
	stale := RepositoryState{Revision: revisionTwo}
	cases := []struct {
		name      string
		setup     func(t *testing.T, state *State)
		facts     RepositoryState
		wantKind  NextActionKind
		wantID    string
		wantWaits int
	}{
		{"READY work is started in node order", func(t *testing.T, state *State) {}, RepositoryState{}, NextActionStart, "a", 0},
		{"RUNNING work is continued before anything else", func(t *testing.T, state *State) {
			if err := state.Start("b", lifecycleNow); err != nil {
				t.Fatal(err)
			}
		}, RepositoryState{}, NextActionResume, "b", 0},
		{"failed RUNNING work is repaired", func(t *testing.T, state *State) {
			runVerification(t, state, "b", commitAt(revisionOne), 1)
		}, RepositoryState{Revision: revisionOne}, NextActionRepair, "b", 0},
		{"interrupted VERIFYING work is recovered by verify", func(t *testing.T, state *State) {
			if err := state.Start("b", lifecycleNow); err != nil {
				t.Fatal(err)
			}
			if err := state.BeginCandidateVerification("b", commitAt(revisionOne), "/tmp/w", "", lifecycleNow); err != nil {
				t.Fatal(err)
			}
		}, RepositoryState{AbandonedRuns: map[string]bool{"b": true}}, NextActionRecover, "b", 0},
		{"live VERIFYING work is waited for, and nothing else starts", func(t *testing.T, state *State) {
			if err := state.Start("b", lifecycleNow); err != nil {
				t.Fatal(err)
			}
			if err := state.BeginCandidateVerification("b", commitAt(revisionOne), "/tmp/w", "", lifecycleNow); err != nil {
				t.Fatal(err)
			}
		}, RepositoryState{}, NextActionWait, "", 1},
		{"stale REVIEW is verified again before new work starts", func(t *testing.T, state *State) {
			runVerification(t, state, "b", commitAt(revisionOne), 0)
		}, stale, NextActionReverify, "b", 0},
		{"fresh REVIEW does not stop other READY work", func(t *testing.T, state *State) {
			runVerification(t, state, "b", commitAt(revisionOne), 0)
		}, RepositoryState{Revision: revisionOne}, NextActionStart, "a", 0},
		{"a gated READY item is skipped for the next READY one", func(t *testing.T, state *State) {
			if _, err := state.OpenGate("a", "Q?", []string{"x", "y"}, "", lifecycleNow); err != nil {
				t.Fatal(err)
			}
		}, RepositoryState{}, NextActionStart, "b", 0},
		{"a gated RUNNING item blocks starting anything else", func(t *testing.T, state *State) {
			if err := state.Start("b", lifecycleNow); err != nil {
				t.Fatal(err)
			}
			if _, err := state.OpenGate("b", "Q?", []string{"x", "y"}, "", lifecycleNow); err != nil {
				t.Fatal(err)
			}
		}, RepositoryState{}, NextActionWait, "", 1},
		{"everything gated or in REVIEW lists the waits", func(t *testing.T, state *State) {
			runVerification(t, state, "b", commitAt(revisionOne), 0)
			if _, err := state.OpenGate("a", "Q?", []string{"x", "y"}, "", lifecycleNow); err != nil {
				t.Fatal(err)
			}
		}, RepositoryState{Revision: revisionOne}, NextActionWait, "", 2},
		{"a cancelled Goal is reported", func(t *testing.T, state *State) {
			if err := state.CancelGoal("g", "dropped", lifecycleNow); err != nil {
				t.Fatal(err)
			}
		}, RepositoryState{}, NextActionGoalCancelled, "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := lifecycleState(t, true, "a", "b")
			tc.setup(t, &state)
			before := encoded(t, &state)
			action := state.ActionableNext(tc.facts)
			if action.Kind != tc.wantKind || action.Item.ID != tc.wantID || len(action.Waiting) != tc.wantWaits {
				t.Fatalf("action = %#v, want %s %q with %d waits", action, tc.wantKind, tc.wantID, tc.wantWaits)
			}
			if action.Reason == "" && action.Kind != NextActionNone {
				t.Fatalf("action %s carries no reason", action.Kind)
			}
			if encoded(t, &state) != before {
				t.Fatal("ActionableNext changed state")
			}
		})
	}
}

func TestNextBreaksTiesByNodeOrderAcrossGoals(t *testing.T) {
	state := NewState()
	earlier, later := lifecycleNow, lifecycleNow.Add(time.Hour)
	for _, goal := range []struct {
		id   string
		at   time.Time
		node string
	}{{"second", later, "z-node"}, {"first", earlier, "a-node"}} {
		plan := GoalPlan{Goal: PlanGoal{ID: goal.id, Title: goal.id}, Nodes: []PlanNode{{ID: goal.node, Story: "specs/stories/" + goal.node, DependsOn: []string{}}}}
		if _, err := state.ImportGoalPlan(plan, "/repo", goal.at); err != nil {
			t.Fatal(err)
		}
	}
	// Goals sit in import order in State, but "first" was created earlier: the
	// creation time decides across Goals, so its node is recommended.
	if action := state.ActionableNext(RepositoryState{}); action.Kind != NextActionStart || action.Item.ID != "a-node" {
		t.Fatalf("action = %#v, want the node of the Goal created first", action)
	}
}

func TestNextWaitListsEveryGateAndReviewInCreationOrder(t *testing.T) {
	state := lifecycleState(t, true, "a", "b", "c")
	runVerification(t, &state, "a", commitAt(revisionOne), 0)
	for _, id := range []string{"b", "c"} {
		if _, err := state.OpenGate(id, "Q?", []string{"x", "y"}, "", lifecycleNow); err != nil {
			t.Fatal(err)
		}
	}
	action := state.ActionableNext(RepositoryState{Revision: revisionOne})
	if action.Kind != NextActionWait || len(action.Waiting) != 3 {
		t.Fatalf("action = %#v", action)
	}
	got := action.Waiting[0].Kind + "/" + action.Waiting[1].Kind + "/" + action.Waiting[2].Kind
	if got != WaitReview+"/"+WaitGate+"/"+WaitGate || action.Waiting[0].ItemID != "a" || action.Waiting[1].ItemID != "b" || action.Waiting[2].ItemID != "c" {
		t.Fatalf("waiting = %#v", action.Waiting)
	}
}
