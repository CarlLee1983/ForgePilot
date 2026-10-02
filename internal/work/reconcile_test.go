package work

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// reconcileFixture builds a Goal "g" with a→b where a is DONE and b has been
// demoted to PENDING by hand, the way a state written by an older binary or an
// edit could disagree with the dependency rule. Completing a would have made b
// READY in the same transaction; reconcile is the command that repairs a state
// where that did not happen.
func reconcileFixture(t *testing.T) State {
	t.Helper()
	state := lifecycleState(t, false, "a", "b<a")
	runVerification(t, &state, "a", commitAt(revisionOne), 0)
	wantStatus(t, &state, "b", Ready)
	state.item("b").Status = Pending
	return state
}

func encoded(t *testing.T, state State) string {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestReconcileRestoresReadinessAndIsIdempotent(t *testing.T) {
	state := reconcileFixture(t)
	later := lifecycleNow.Add(time.Hour)
	evidenceCount := len(state.Evidence)

	changes, err := state.ReconcileGoalReadiness("g", later)
	if err != nil {
		t.Fatal(err)
	}
	want := []ReadinessChange{{ItemID: "b", From: Pending, To: Ready}}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %#v, want %#v", changes, want)
	}
	wantStatus(t, &state, "b", Ready)
	if got := state.item("b").UpdatedAt; !got.Equal(later) {
		t.Fatalf("changed item UpdatedAt = %v, want %v", got, later)
	}
	aUpdated := state.item("a").UpdatedAt

	before := encoded(t, state)
	again, err := state.ReconcileGoalReadiness("g", later.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("second reconcile reported %#v, want no changes", again)
	}
	if after := encoded(t, state); after != before {
		t.Fatalf("second reconcile changed state:\n%s\n%s", before, after)
	}
	if got := state.item("a").UpdatedAt; !got.Equal(aUpdated) {
		t.Fatalf("prerequisite UpdatedAt moved to %v", got)
	}
	if len(state.Evidence) != evidenceCount {
		t.Fatalf("reconcile appended Evidence: %d, want %d", len(state.Evidence), evidenceCount)
	}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
}

// Readiness is one rule: every dependency DONE. Reconcile never promotes work
// whose prerequisite is merely passing and awaiting approval.
func TestReconcileDoesNotPromoteBehindAPrerequisiteThatIsNotDone(t *testing.T) {
	state := lifecycleState(t, true, "a", "b<a")
	runVerification(t, &state, "a", commitAt(revisionOne), 0)
	wantStatus(t, &state, "a", Review)
	changes, err := state.ReconcileGoalReadiness("g", lifecycleNow.Add(time.Hour))
	if err != nil || len(changes) != 0 {
		t.Fatalf("reconcile = %#v, %v, want no changes", changes, err)
	}
	wantStatus(t, &state, "b", Pending)
}

func TestReconcileRefusesGoalsItMustNotTouch(t *testing.T) {
	for _, refusal := range []struct {
		name    string
		prepare func(t *testing.T, state *State)
		goalID  string
		message string
	}{
		{name: "unknown goal", goalID: "missing", message: `unknown goal "missing"`},
		{
			name: "cancelled goal",
			prepare: func(t *testing.T, state *State) {
				if err := state.CancelGoal("g", "abandoned", lifecycleNow); err != nil {
					t.Fatal(err)
				}
			},
			goalID: "g", message: "it is CANCELLED",
		},
		{
			name: "completed goal",
			prepare: func(t *testing.T, state *State) {
				runVerification(t, state, "b", commitAt(revisionOne), 0)
			},
			goalID: "g", message: "it is COMPLETED",
		},
	} {
		t.Run(refusal.name, func(t *testing.T) {
			state := lifecycleState(t, false, "a", "b<a")
			runVerification(t, &state, "a", commitAt(revisionOne), 0)
			if refusal.prepare != nil {
				refusal.prepare(t, &state)
			}
			before := encoded(t, state)
			_, err := state.ReconcileGoalReadiness(refusal.goalID, lifecycleNow.Add(time.Hour))
			if err == nil {
				t.Fatal("reconciled a goal that must be refused")
			}
			if !strings.Contains(err.Error(), refusal.message) {
				t.Fatalf("error = %v, want it to mention %q", err, refusal.message)
			}
			if after := encoded(t, state); after != before {
				t.Fatal("refused reconciliation changed state")
			}
		})
	}
}

func TestReconcileOnlyTouchesReadinessInItsOwnGoal(t *testing.T) {
	state := reconcileFixture(t)
	later := lifecycleNow.Add(time.Minute)
	state.Goals = append(state.Goals, Goal{ID: "other", Title: "Other", Repository: "/repo", Status: GoalActive, CreatedAt: lifecycleNow, UpdatedAt: lifecycleNow})
	state.WorkItems = append(state.WorkItems,
		Item{ID: "x", GoalID: "other", StoryRef: "specs/stories/x", Status: Done, CreatedAt: lifecycleNow, UpdatedAt: lifecycleNow},
		Item{ID: "y", GoalID: "other", StoryRef: "specs/stories/y", Status: Pending, DependsOn: []string{"x"}, CreatedAt: lifecycleNow, UpdatedAt: lifecycleNow})

	if _, err := state.ReconcileGoalReadiness("g", later); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, &state, "b", Ready)
	// y is just as recoverable as b, and reconciling "g" must leave it alone.
	wantStatus(t, &state, "y", Pending)
	if got := state.item("y").UpdatedAt; !got.Equal(lifecycleNow) {
		t.Fatalf("other goal UpdatedAt moved to %v", got)
	}
}

func TestReconcileLeavesNonReadinessStatusesAlone(t *testing.T) {
	for _, status := range []Status{Running, Verifying, Review, Done} {
		t.Run(string(status), func(t *testing.T) {
			state := reconcileFixture(t)
			item := state.item("b")
			// Drive the item into the status under test directly: this test is
			// about which statuses reconcile refuses to rewrite, not about how
			// they are reached.
			item.Status = status
			before := item.UpdatedAt
			if _, err := state.ReconcileGoalReadiness("g", lifecycleNow.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			if got := state.item("b"); got.Status != status || !got.UpdatedAt.Equal(before) {
				t.Fatalf("%s became %s at %v", status, got.Status, got.UpdatedAt)
			}
		})
	}
}

func TestActionableNextRecommendsReconcileForRecoverablePendingWork(t *testing.T) {
	state := reconcileFixture(t)

	before := encoded(t, state)
	action := state.ActionableNext(RepositoryState{})
	if action.Kind != NextActionReconcile || action.Item.ID != "b" {
		t.Fatalf("action = %#v, want RECONCILE on b", action)
	}
	if after := encoded(t, state); after != before {
		t.Fatal("ActionableNext changed state")
	}

	// next and reconcile must agree: a recommendation that reconcile would report
	// as unchanged is the spin this shared predicate exists to prevent.
	changes, err := state.ReconcileGoalReadiness("g", lifecycleNow.Add(time.Hour))
	if err != nil || len(changes) != 1 {
		t.Fatalf("reconcile after RECONCILE recommendation = %#v, %v", changes, err)
	}
	if action := state.ActionableNext(RepositoryState{}); action.Kind != NextActionStart || action.Item.ID != "b" {
		t.Fatalf("action after reconcile = %#v, want START on b", action)
	}
}

func TestActionableNextWithholdsReconcileWhenAdvancingWouldBeIllegal(t *testing.T) {
	for _, blocked := range []struct {
		name  string
		build func(t *testing.T) State
	}{
		{
			name: "prerequisite is awaiting approval, not DONE",
			build: func(t *testing.T) State {
				state := lifecycleState(t, true, "a", "b<a")
				runVerification(t, &state, "a", commitAt(revisionOne), 0)
				return state
			},
		},
		{
			name: "the pending work item carries its own open Gate",
			build: func(t *testing.T) State {
				state := reconcileFixture(t)
				if _, err := state.OpenGate("b", "which way?", []string{"left", "right"}, "", lifecycleNow); err != nil {
					t.Fatal(err)
				}
				return state
			},
		},
	} {
		t.Run(blocked.name, func(t *testing.T) {
			state := blocked.build(t)
			wantStatus(t, &state, "b", Pending)
			if action := state.ActionableNext(RepositoryState{Revision: revisionOne}); action.Kind == NextActionReconcile {
				t.Fatalf("recommended RECONCILE for illegal progression: %#v", action)
			}
		})
	}
}

func TestActionableNextKeepsStaleWorkItemReviewAheadOfAdvanceableWork(t *testing.T) {
	state := lifecycleState(t, true, "reviewing", "independent")
	runVerification(t, &state, "reviewing", commitAt(revisionOne), 0)
	wantStatus(t, &state, "reviewing", Review)
	wantStatus(t, &state, "independent", Ready)
	action := state.ActionableNext(RepositoryState{Revision: revisionTwo})
	if action.Kind != NextActionReverify || action.Item.ID != "reviewing" {
		t.Fatalf("action = %#v, want REVERIFY on reviewing", action)
	}
}

func TestActionableNextOrdersStartAndReconcileCandidatesTogether(t *testing.T) {
	state := reconcileFixture(t)
	state.WorkItems = append(state.WorkItems, Item{ID: "c", GoalID: "g", StoryRef: "specs/stories/c", Status: Ready,
		CreatedAt: lifecycleNow.Add(time.Minute), UpdatedAt: lifecycleNow.Add(time.Minute)})

	// The older PENDING-but-recoverable item wins over the newer READY one: both
	// candidates share one creation-time ordering rather than two loops.
	action := state.ActionableNext(RepositoryState{})
	if action.Kind != NextActionReconcile || action.Item.ID != "b" {
		t.Fatalf("action = %#v, want RECONCILE on b", action)
	}
	state.item("b").CreatedAt = lifecycleNow.Add(2 * time.Minute)
	action = state.ActionableNext(RepositoryState{})
	if action.Kind != NextActionStart || action.Item.ID != "c" {
		t.Fatalf("reordered action = %#v, want START on c", action)
	}
}
