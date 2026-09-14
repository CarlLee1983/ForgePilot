package work

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// reconcileFixture builds a GOAL-policy Goal whose second Work Item depends on a
// first one that has already PASSed at revisionA.
func reconcileFixture(t *testing.T) (State, time.Time, string, string) {
	t.Helper()
	now := time.Date(2026, 9, 13, 3, 0, 0, 0, time.UTC)
	state := NewState()
	if err := state.AddGoalWithReviewPolicy("goal", "Goal", "", "/repo", ReviewPerGoal, now); err != nil {
		t.Fatal(err)
	}
	first, err := state.AddWork("goal", "specs/stories/one", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := state.AddWork("goal", "specs/stories/two", []string{first.ID}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Start(first.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginVerification(first.ID, revisionA, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerificationWithRepository(first.ID, revisionA, "make verify", 0, RepositoryState{Revision: revisionA}, now); err != nil {
		t.Fatal(err)
	}
	return state, now, first.ID, second.ID
}

const (
	revisionA = "1111111111111111111111111111111111111111"
	revisionB = "2222222222222222222222222222222222222222"
)

func TestReconcileRestoresReadinessAndIsIdempotent(t *testing.T) {
	state, now, first, second := reconcileFixture(t)
	if state.WorkItemStatus(second) != Ready {
		t.Fatalf("%s = %s, want READY", second, state.WorkItemStatus(second))
	}
	// A Gate opened on the prerequisite withdraws the persisted readiness.
	if _, err := state.OpenGate(first, "which way?", []string{"left", "right"}, "", now); err != nil {
		t.Fatal(err)
	}
	if state.WorkItemStatus(second) != Pending {
		t.Fatalf("%s after gate = %s, want PENDING", second, state.WorkItemStatus(second))
	}
	// The Gate is answered while the repository sits on a different Candidate, so
	// readiness stays withdrawn: the prerequisite's PASS no longer matches.
	if err := state.ResolveGateWithRepository("GATE-001", "left", "", "carl", RepositoryState{Revision: revisionB}, now); err != nil {
		t.Fatal(err)
	}
	if state.WorkItemStatus(second) != Pending {
		t.Fatalf("%s at stale candidate = %s, want PENDING", second, state.WorkItemStatus(second))
	}

	later := now.Add(time.Hour)
	changes, err := state.ReconcileGoalReadiness("goal", RepositoryState{Revision: revisionA}, later)
	if err != nil {
		t.Fatal(err)
	}
	want := []ReadinessChange{{ItemID: second, From: Pending, To: Ready}}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %#v, want %#v", changes, want)
	}
	if state.WorkItemStatus(second) != Ready {
		t.Fatalf("%s after reconcile = %s, want READY", second, state.WorkItemStatus(second))
	}
	if got := itemByID(t, &state, second).UpdatedAt; !got.Equal(later) {
		t.Fatalf("changed item UpdatedAt = %v, want %v", got, later)
	}
	firstUpdated := itemByID(t, &state, first).UpdatedAt
	evidenceCount := len(state.Evidence)

	before := encoded(t, state)
	again, err := state.ReconcileGoalReadiness("goal", RepositoryState{Revision: revisionA}, later.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("second reconcile reported %#v, want no changes", again)
	}
	if after := encoded(t, state); after != before {
		t.Fatalf("second reconcile changed state:\n%s\n%s", before, after)
	}
	if got := itemByID(t, &state, second).UpdatedAt; !got.Equal(later) {
		t.Fatalf("unchanged item UpdatedAt moved to %v", got)
	}
	if got := itemByID(t, &state, first).UpdatedAt; !got.Equal(firstUpdated) {
		t.Fatalf("prerequisite UpdatedAt moved to %v", got)
	}
	if len(state.Evidence) != evidenceCount {
		t.Fatalf("reconcile appended Evidence: %d, want %d", len(state.Evidence), evidenceCount)
	}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileWithdrawsReadinessWhenPrerequisiteWentStale(t *testing.T) {
	state, now, _, second := reconcileFixture(t)
	changes, err := state.ReconcileGoalReadiness("goal", RepositoryState{Revision: revisionB}, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := []ReadinessChange{{ItemID: second, From: Ready, To: Pending}}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %#v, want %#v", changes, want)
	}
}

func TestReconcileRefusesGoalsItMustNotTouch(t *testing.T) {
	for _, refusal := range []struct {
		name    string
		prepare func(t *testing.T, state *State, now time.Time)
		goalID  string
		message string
	}{
		{name: "unknown goal", goalID: "missing", message: `unknown goal "missing"`},
		{
			name:    "blocked goal",
			prepare: func(t *testing.T, state *State, now time.Time) { mustBlock(t, state, "wrong direction", now) },
			goalID:  "goal", message: "it is BLOCKED",
		},
		{
			name: "cancelled goal",
			prepare: func(t *testing.T, state *State, now time.Time) {
				if err := state.CancelGoal("goal", "abandoned", now); err != nil {
					t.Fatal(err)
				}
			},
			goalID: "goal", message: "it is CANCELLED",
		},
		{
			name: "completed goal",
			prepare: func(t *testing.T, state *State, now time.Time) {
				state.Goals[0].ReviewPolicy = ReviewPerWorkItem
				state.Goals[0].Status = GoalCompleted
			},
			goalID: "goal", message: "it is COMPLETED",
		},
	} {
		t.Run(refusal.name, func(t *testing.T) {
			state, now, _, second := reconcileFixture(t)
			if refusal.prepare != nil {
				refusal.prepare(t, &state, now)
			}
			before := encoded(t, state)
			_, err := state.ReconcileGoalReadiness(refusal.goalID, RepositoryState{Revision: revisionB}, now.Add(time.Hour))
			if err == nil {
				t.Fatal("reconciled a goal that must be refused")
			}
			if !strings.Contains(err.Error(), refusal.message) {
				t.Fatalf("error = %v, want it to mention %q", err, refusal.message)
			}
			if after := encoded(t, state); after != before {
				t.Fatalf("refused reconciliation changed state (%s is %s)", second, state.WorkItemStatus(second))
			}
		})
	}
}

func TestReconcileOnlyTouchesReadinessInItsOwnGoal(t *testing.T) {
	state, now, first, second := reconcileFixture(t)
	if err := state.AddGoalWithReviewPolicy("other", "Other", "", "/repo", ReviewPerGoal, now); err != nil {
		t.Fatal(err)
	}
	otherFirst, err := state.AddWork("other", "specs/stories/three", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	otherSecond, err := state.AddWork("other", "specs/stories/four", []string{otherFirst.ID}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Start(otherFirst.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginVerification(otherFirst.ID, revisionA, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerification(otherFirst.ID, revisionA, "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
	if state.WorkItemStatus(otherSecond.ID) != Pending {
		t.Fatalf("%s = %s, want PENDING", otherSecond.ID, state.WorkItemStatus(otherSecond.ID))
	}

	// Reconciling the first Goal at a Candidate that would also satisfy the other
	// Goal must leave the other Goal exactly as it was.
	if _, err := state.ReconcileGoalReadiness("goal", RepositoryState{Revision: revisionA}, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if state.WorkItemStatus(otherSecond.ID) != Pending {
		t.Fatalf("reconciling goal changed %s to %s", otherSecond.ID, state.WorkItemStatus(otherSecond.ID))
	}
	if got := itemByID(t, &state, otherSecond.ID).UpdatedAt; !got.Equal(now) {
		t.Fatalf("other goal UpdatedAt moved to %v", got)
	}
	if state.WorkItemStatus(second) != Ready || state.WorkItemStatus(first) != Verified {
		t.Fatalf("own goal = %s/%s", state.WorkItemStatus(first), state.WorkItemStatus(second))
	}
}

func TestReconcileLeavesNonReadinessStatusesAlone(t *testing.T) {
	now := time.Date(2026, 9, 13, 3, 0, 0, 0, time.UTC)
	for _, status := range []Status{Running, Verifying, Review, Verified, Done} {
		t.Run(string(status), func(t *testing.T) {
			state, _, first, _ := reconcileFixture(t)
			item := itemByID(t, &state, first)
			if item.Status != Verified {
				t.Fatalf("fixture prerequisite = %s", item.Status)
			}
			// Drive the prerequisite into the status under test directly: this test
			// is about which statuses reconcile refuses to rewrite, not about how
			// they are reached.
			item.Status = status
			before := item.UpdatedAt
			if _, err := state.ReconcileGoalReadiness("goal", RepositoryState{Revision: revisionB}, now.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			if got := itemByID(t, &state, first); got.Status != status || !got.UpdatedAt.Equal(before) {
				t.Fatalf("%s became %s at %v", status, got.Status, got.UpdatedAt)
			}
		})
	}
}

func TestReadinessCandidateKindsScopesFactsToOneGoal(t *testing.T) {
	state, now, _, second := reconcileFixture(t)
	if got := state.ReadinessCandidateKinds("goal"); !reflect.DeepEqual(got, []CandidateKind{CommitCandidate}) {
		t.Fatalf("kinds = %#v, want COMMIT", got)
	}

	// A second Goal verified through a SNAPSHOT candidate must not make the first
	// Goal's reconciliation demand a workspace digest.
	if err := state.AddGoalWithReviewPolicy("other", "Other", "", "/repo", ReviewPerGoal, now); err != nil {
		t.Fatal(err)
	}
	otherFirst, err := state.AddWork("other", "specs/stories/three", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.AddWork("other", "specs/stories/four", []string{otherFirst.ID}, now); err != nil {
		t.Fatal(err)
	}
	if err := state.Start(otherFirst.ID, now); err != nil {
		t.Fatal(err)
	}
	snapshot := Candidate{Kind: SnapshotCandidate, Revision: revisionB, BaseRevision: revisionA,
		Digest: "sha256:" + strings.Repeat("a", 64)}
	if err := state.BeginCandidateVerification(otherFirst.ID, snapshot, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerification(otherFirst.ID, revisionB, "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
	if got := state.ReadinessCandidateKinds("goal"); !reflect.DeepEqual(got, []CandidateKind{CommitCandidate}) {
		t.Fatalf("kinds after unrelated snapshot = %#v, want COMMIT only", got)
	}
	if got := state.ReadinessCandidateKinds("other"); !reflect.DeepEqual(got, []CandidateKind{SnapshotCandidate}) {
		t.Fatalf("other goal kinds = %#v, want SNAPSHOT", got)
	}
	// A prerequisite the progression rule already refuses is decided without the
	// repository, so its Evidence must not make reconciliation demand a fact.
	// Re-verifying the first Goal's prerequisite puts it back into VERIFYING.
	if err := state.BeginVerification(state.WorkItems[0].ID, revisionB, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if got := state.ReadinessCandidateKinds("goal"); len(got) != 0 {
		t.Fatalf("kinds for a prerequisite that cannot satisfy anything = %#v, want none", got)
	}
	if _, err := state.RecordVerificationWithRepository(state.WorkItems[0].ID, revisionB, "make verify", 0, RepositoryState{Revision: revisionB}, now); err != nil {
		t.Fatal(err)
	}

	// A Goal with nothing left to reconcile needs no repository facts at all.
	if err := state.StartWithRepository(second, RepositoryState{Revision: revisionB}, now); err != nil {
		t.Fatal(err)
	}
	if got := state.ReadinessCandidateKinds("goal"); len(got) != 0 {
		t.Fatalf("kinds with no PENDING/READY work = %#v, want none", got)
	}
}

// encoded serializes State so that a comparison sees in-place Work Item edits.
// Copying the struct would not: WorkItems is a slice, so a copy shares the same
// backing array and every readiness path writes through it.
func encoded(t *testing.T, state State) string {
	t.Helper()
	contents, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func itemByID(t *testing.T, state *State, id string) *Item {
	t.Helper()
	item := state.item(id)
	if item == nil {
		t.Fatalf("unknown work item %q", id)
	}
	return item
}

func mustBlock(t *testing.T, state *State, reason string, now time.Time) {
	t.Helper()
	if err := state.BlockGoal("goal", reason, now); err != nil {
		t.Fatal(err)
	}
}

func TestActionableNextRecommendsReconcileForRecoverablePendingWork(t *testing.T) {
	state, now, first, second := reconcileFixture(t)
	if _, err := state.OpenGate(first, "which way?", []string{"left", "right"}, "", now); err != nil {
		t.Fatal(err)
	}
	if err := state.ResolveGateWithRepository("GATE-001", "left", "", "carl", RepositoryState{Revision: revisionB}, now); err != nil {
		t.Fatal(err)
	}
	if state.WorkItemStatus(second) != Pending {
		t.Fatalf("%s = %s, want PENDING", second, state.WorkItemStatus(second))
	}

	before := encoded(t, state)
	action := state.ActionableNext(RepositoryState{Revision: revisionA})
	if action.Kind != NextActionReconcile || action.Item.ID != second {
		t.Fatalf("action = %#v, want RECONCILE on %s", action, second)
	}
	if after := encoded(t, state); after != before {
		t.Fatal("ActionableNext changed state")
	}

	// next and reconcile must agree: a recommendation that reconcile would report
	// as unchanged is the spin this shared predicate exists to prevent.
	changes, err := state.ReconcileGoalReadiness("goal", RepositoryState{Revision: revisionA}, now.Add(time.Hour))
	if err != nil || len(changes) != 1 {
		t.Fatalf("reconcile after RECONCILE recommendation = %#v, %v", changes, err)
	}
	if action := state.ActionableNext(RepositoryState{Revision: revisionA}); action.Kind != NextActionStart || action.Item.ID != second {
		t.Fatalf("action after reconcile = %#v, want START on %s", action, second)
	}
}

func TestActionableNextWithholdsReconcileWhenAdvancingWouldBeIllegal(t *testing.T) {
	for _, blocked := range []struct {
		name       string
		prepare    func(t *testing.T, state *State, now time.Time, first, second string)
		repository RepositoryState
	}{
		{
			name:       "prerequisite candidate is stale",
			prepare:    func(t *testing.T, state *State, now time.Time, first, second string) { demote(t, state, second, now) },
			repository: RepositoryState{Revision: revisionB},
		},
		{
			name: "prerequisite still carries an open Gate",
			prepare: func(t *testing.T, state *State, now time.Time, first, second string) {
				if _, err := state.OpenGate(first, "which way?", []string{"left", "right"}, "", now); err != nil {
					t.Fatal(err)
				}
			},
			repository: RepositoryState{Revision: revisionA},
		},
		{
			name: "the pending work item carries its own open Gate",
			prepare: func(t *testing.T, state *State, now time.Time, first, second string) {
				demote(t, state, second, now)
				if _, err := state.OpenGate(second, "which way?", []string{"left", "right"}, "", now); err != nil {
					t.Fatal(err)
				}
			},
			repository: RepositoryState{Revision: revisionA},
		},
	} {
		t.Run(blocked.name, func(t *testing.T) {
			state, now, first, second := reconcileFixture(t)
			blocked.prepare(t, &state, now, first, second)
			if state.WorkItemStatus(second) != Pending {
				t.Fatalf("%s = %s, want PENDING", second, state.WorkItemStatus(second))
			}
			if action := state.ActionableNext(blocked.repository); action.Kind == NextActionReconcile {
				t.Fatalf("recommended RECONCILE for illegal progression: %#v", action)
			}
		})
	}
}

// demote withdraws persisted readiness the way an unrelated re-verification
// would, without touching Evidence.
func demote(t *testing.T, state *State, id string, now time.Time) {
	t.Helper()
	item := itemByID(t, state, id)
	if item.Status != Ready {
		t.Fatalf("%s = %s, want READY before demotion", id, item.Status)
	}
	item.Status = Pending
}

func TestActionableNextDefersStaleVerifiedReverificationBehindAdvanceableWork(t *testing.T) {
	state, now, first, second := reconcileFixture(t)
	third, err := state.AddWorkWithRepository("goal", "specs/stories/three", []string{second}, RepositoryState{Revision: revisionA}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.StartWithRepository(second, RepositoryState{Revision: revisionA}, now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginVerification(second, revisionB, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerificationWithRepository(second, revisionB, "make verify", 0, RepositoryState{Revision: revisionB}, now); err != nil {
		t.Fatal(err)
	}
	// The first Work Item's PASS is now stale, but the third is legally startable.
	if !state.CandidateStale(first, revisionB, "") {
		t.Fatalf("%s is not stale at %s", first, revisionB)
	}
	if state.WorkItemStatus(third.ID) != Ready {
		t.Fatalf("%s = %s, want READY", third.ID, state.WorkItemStatus(third.ID))
	}
	if action := state.ActionableNext(RepositoryState{Revision: revisionB}); action.Kind != NextActionStart || action.Item.ID != third.ID {
		t.Fatalf("action = %#v, want START on %s", action, third.ID)
	}

	// Once nothing can advance, the deferred re-verification is what is left, in
	// creation order.
	if err := state.StartWithRepository(third.ID, RepositoryState{Revision: revisionB}, now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginVerification(third.ID, revisionB, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerificationWithRepository(third.ID, revisionB, "make verify", 0, RepositoryState{Revision: revisionB}, now); err != nil {
		t.Fatal(err)
	}
	action := state.ActionableNext(RepositoryState{Revision: revisionB})
	if action.Kind != NextActionReverify || action.Item.ID != first {
		t.Fatalf("action = %#v, want REVERIFY on %s", action, first)
	}
}

func TestActionableNextKeepsStaleWorkItemReviewAheadOfAdvanceableWork(t *testing.T) {
	now := time.Date(2026, 9, 13, 4, 0, 0, 0, time.UTC)
	state := NewState()
	if err := state.AddGoal("goal", "Goal", "", "/repo", now); err != nil {
		t.Fatal(err)
	}
	reviewing, err := state.AddWork("goal", "specs/stories/one", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	independent, err := state.AddWork("goal", "specs/stories/two", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Start(reviewing.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginVerification(reviewing.ID, revisionA, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerification(reviewing.ID, revisionA, "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
	submitForReview(t, &state, reviewing.ID, now)
	if state.WorkItemStatus(reviewing.ID) != Review {
		t.Fatalf("%s = %s, want REVIEW", reviewing.ID, state.WorkItemStatus(reviewing.ID))
	}
	if state.WorkItemStatus(independent.ID) != Ready {
		t.Fatalf("%s = %s, want READY", independent.ID, state.WorkItemStatus(independent.ID))
	}
	action := state.ActionableNext(RepositoryState{Revision: revisionB})
	if action.Kind != NextActionReverify || action.Item.ID != reviewing.ID {
		t.Fatalf("action = %#v, want REVERIFY on %s", action, reviewing.ID)
	}
}

func TestActionableNextOrdersStartAndReconcileCandidatesTogether(t *testing.T) {
	state, now, _, second := reconcileFixture(t)
	independent, err := state.AddWork("goal", "specs/stories/three", nil, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if state.WorkItemStatus(independent.ID) != Ready {
		t.Fatalf("%s = %s, want READY", independent.ID, state.WorkItemStatus(independent.ID))
	}
	demote(t, &state, second, now)

	// The older PENDING-but-recoverable item wins over the newer READY one: both
	// candidates share one creation-time ordering rather than two loops.
	action := state.ActionableNext(RepositoryState{Revision: revisionA})
	if action.Kind != NextActionReconcile || action.Item.ID != second {
		t.Fatalf("action = %#v, want RECONCILE on %s", action, second)
	}
	itemByID(t, &state, second).CreatedAt = now.Add(2 * time.Minute)
	action = state.ActionableNext(RepositoryState{Revision: revisionA})
	if action.Kind != NextActionStart || action.Item.ID != independent.ID {
		t.Fatalf("reordered action = %#v, want START on %s", action, independent.ID)
	}
}
