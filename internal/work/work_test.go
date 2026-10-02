package work

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestQueueRules(t *testing.T) {
	now := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	state := NewState()
	if err := state.AddGoal("g", "Goal", "", "/repo", now); err != nil {
		t.Fatal(err)
	}
	first, err := state.AddWork("g", "specs/stories/a", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := state.AddWork("g", "specs/stories/b", []string{first.ID}, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != NotStarted || second.Status != NotStarted {
		t.Fatalf("got %s and %s, want both persisted as NOT_STARTED", first.Status, second.Status)
	}
	if state.DisplayStatus(first.ID) != "READY" || state.DisplayStatus(second.ID) != "PENDING" {
		t.Fatalf("displayed %s and %s, want READY and PENDING", state.DisplayStatus(first.ID), state.DisplayStatus(second.ID))
	}
	if _, err := state.AddWork("g", "specs/stories/missing", []string{"WI-404"}, now); err == nil {
		t.Fatal("accepted unknown dependency")
	}
	if _, err := state.AddWork("g", "specs/stories/duplicate", []string{first.ID, first.ID}, now); err == nil {
		t.Fatal("accepted duplicate dependency")
	}
	if err := state.AddGoal("other", "Other", "", "/repo", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.AddWork("other", "specs/stories/cross", []string{first.ID}, now); err == nil {
		t.Fatal("accepted cross-goal dependency")
	}
	if err := state.Start(second.ID, now); err == nil {
		t.Fatal("started blocked work")
	}
	before := state
	next := state.ActionableNext(RepositoryState{})
	if next.Kind != NextActionStart || next.Item.ID != first.ID {
		t.Fatalf("next = %#v", next)
	}
	if !reflect.DeepEqual(before, state) {
		t.Fatal("next changed state")
	}
	if err := state.Start(first.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := state.Start(first.ID, now); err == nil {
		t.Fatal("started running work twice")
	}
}

func TestValidation(t *testing.T) {
	state := State{SchemaVersion: SchemaVersion, NextEvidenceID: 1, NextGateID: 1, NextVerificationRunID: 1, Goals: []Goal{{ID: "g", Title: "Goal", Repository: "/repo", Status: GoalActive, RequireApproval: true}}, WorkItems: []Item{
		{ID: "WI-001", GoalID: "g", StoryRef: "specs/stories/a", Status: Done},
		{ID: "WI-002", GoalID: "g", StoryRef: "specs/stories/b", Status: NotStarted, DependsOn: []string{"WI-001"}},
	}}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
	state.WorkItems[1].DependsOn = []string{"WI-002"}
	if err := state.Validate(); err == nil {
		t.Fatal("accepted self dependency")
	}
	state.WorkItems[1].ID, state.WorkItems[1].DependsOn = "", nil
	if err := state.Validate(); err == nil {
		t.Fatal("accepted a work item without an ID")
	}
	state.WorkItems = []Item{
		{ID: "WI-001", GoalID: "g", StoryRef: "specs/stories/a", Status: NotStarted, DependsOn: []string{"WI-002"}},
		{ID: "WI-002", GoalID: "g", StoryRef: "specs/stories/b", Status: NotStarted, DependsOn: []string{"WI-001"}},
	}
	if err := state.Validate(); err == nil {
		t.Fatal("accepted dependency cycle")
	}
}

// The fixtures are written relative to SchemaVersion on purpose, and each is
// preceded by a check that the same state validates at the current version:
// without that, a fixture that is invalid for some unrelated reason would pass
// the rejection assertion while no longer testing the version at all.
func TestSchemaVersionErrorsDistinguishOlderFromNewer(t *testing.T) {
	if SchemaVersion != 19 {
		t.Fatalf("SchemaVersion = %d, want 19", SchemaVersion)
	}
	fresh := NewState()
	if err := fresh.Validate(); err != nil {
		t.Fatalf("a fresh state is invalid: %v", err)
	}
	if fresh.SchemaVersion != SchemaVersion {
		t.Fatalf("fresh state is schema %d, want %d", fresh.SchemaVersion, SchemaVersion)
	}
	if fresh.NextEvidenceID != 1 || fresh.NextGateID != 1 || fresh.NextVerificationRunID != 1 {
		t.Fatalf("fresh counters = %d, %d, %d, want 1, 1, 1", fresh.NextEvidenceID, fresh.NextGateID, fresh.NextVerificationRunID)
	}

	older := fresh
	older.SchemaVersion = SchemaVersion - 1
	err := older.Validate()
	if err == nil {
		t.Fatal("accepted an older schema version")
	}
	if !strings.Contains(err.Error(), "no longer supported") || !strings.Contains(err.Error(), "goal import") {
		t.Fatalf("older-version error %q does not say the schema is unsupported and point at goal import", err)
	}
	if strings.Contains(err.Error(), "newer") {
		t.Fatalf("older-version error %q claims the state is newer", err)
	}

	newer := fresh
	newer.SchemaVersion = SchemaVersion + 1
	err = newer.Validate()
	if err == nil {
		t.Fatal("accepted a newer schema version")
	}
	if !strings.Contains(err.Error(), "newer") || strings.Contains(err.Error(), "goal import") {
		t.Fatalf("newer-version error %q must say newer and must not suggest re-importing", err)
	}
}

// Work Items imported together share a CreatedAt, so what orders simultaneously
// READY work is the order they sit in state.WorkItems: the Goal Plan's node order.
func TestSelectionUsesTimestampThenImportOrder(t *testing.T) {
	old, same := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	state := State{SchemaVersion: SchemaVersion, NextEvidenceID: 1, NextGateID: 1, NextVerificationRunID: 1, Goals: []Goal{{ID: "active", Title: "Active", Repository: "/repo", Status: GoalActive, RequireApproval: true}, {ID: "cancelled", Title: "Cancelled", Repository: "/repo", Status: GoalCancelled, Reason: "dropped"}}, WorkItems: []Item{
		{ID: "zeta", GoalID: "active", StoryRef: "specs/stories/c", Status: NotStarted, CreatedAt: same},
		{ID: "alpha", GoalID: "active", StoryRef: "specs/stories/b", Status: NotStarted, CreatedAt: same},
		{ID: "mid", GoalID: "active", StoryRef: "specs/stories/a", Status: NotStarted, CreatedAt: old},
		{ID: "other", GoalID: "cancelled", StoryRef: "specs/stories/d", Status: NotStarted, CreatedAt: old},
	}}
	next := state.ActionableNext(RepositoryState{})
	if next.Kind != NextActionStart || next.Item.ID != "mid" {
		t.Fatalf("next = %#v; want the oldest READY work in an active Goal", next)
	}
	state.WorkItems[2].Status = Done
	next = state.ActionableNext(RepositoryState{})
	if next.Kind != NextActionStart || next.Item.ID != "zeta" {
		t.Fatalf("next = %#v; want node order (zeta before alpha) to break a timestamp tie, not ID order", next)
	}
}

// IDs are only ever written by goal import, but they end up in paths and ref
// names, so a hand-edited or damaged state must not be able to smuggle one in.
func TestValidateRejectsMalformedGoalAndWorkItemIDs(t *testing.T) {
	build := func(goalID, itemID string) State {
		state := NewState()
		state.Goals = []Goal{{ID: goalID, Title: "Goal", Repository: "/repo", Status: GoalActive, RequireApproval: true}}
		state.WorkItems = []Item{{ID: itemID, GoalID: goalID, StoryRef: "specs/stories/a", Status: NotStarted}}
		return state
	}
	if state := build("g", "a"); state.Validate() != nil {
		t.Fatalf("the baseline state is invalid: %v", state.Validate())
	}
	for _, bad := range []string{"../../x", "a/b", "a..b", "x.lock", "has space", ""} {
		if err := build(bad, "a").Validate(); err == nil {
			t.Errorf("accepted goal ID %q", bad)
		}
		if err := build("g", bad).Validate(); err == nil {
			t.Errorf("accepted work item ID %q", bad)
		}
	}
}

// TestRemovedStatusesAreRejected pins the persisted status set to the five that
// remain. Blocking is not a status (M3), and PENDING and READY are computed at
// read time, never stored (ADR-0040): a snapshot naming any of them must be
// refused rather than quietly carried forward.
func TestRemovedStatusesAreRejected(t *testing.T) {
	for _, removed := range []Status{"WAITING_HUMAN", "BLOCKED", "PENDING", "READY", ""} {
		state := State{SchemaVersion: SchemaVersion, NextEvidenceID: 1, NextGateID: 1, NextVerificationRunID: 1,
			Goals:     []Goal{{ID: "g", Title: "Goal", Repository: "/repo", Status: GoalActive, RequireApproval: true}},
			WorkItems: []Item{{ID: "WI-001", GoalID: "g", StoryRef: "specs/stories/a", Status: removed}}}
		if err := state.Validate(); err == nil {
			t.Fatalf("accepted removed status %q", removed)
		}
	}
}
