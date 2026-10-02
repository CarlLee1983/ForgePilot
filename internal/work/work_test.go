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
	if first.Status != Ready || second.Status != Pending {
		t.Fatalf("got %s and %s", first.Status, second.Status)
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
	next, ok := state.Next()
	if !ok || next.ID != first.ID {
		t.Fatalf("next = %#v, %v", next, ok)
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

func TestRefreshAndValidation(t *testing.T) {
	now := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	state := State{SchemaVersion: SchemaVersion, NextEvidenceID: 1, NextGateID: 1, NextVerificationRunID: 1, NextGoalCompletionEvidenceID: 1, Goals: []Goal{{ID: "g", Title: "Goal", Repository: "/repo", Status: GoalActive, ReviewPolicy: ReviewPerWorkItem, CompletionPolicy: CompletionHuman}}, WorkItems: []Item{
		{ID: "WI-001", GoalID: "g", StoryRef: "specs/stories/a", Status: Done},
		{ID: "WI-002", GoalID: "g", StoryRef: "specs/stories/b", Status: Pending, DependsOn: []string{"WI-001"}},
	}}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
	state.RefreshReady(now)
	if state.WorkItems[1].Status != Ready {
		t.Fatalf("got %s", state.WorkItems[1].Status)
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
		{ID: "WI-001", GoalID: "g", StoryRef: "specs/stories/a", Status: Pending, DependsOn: []string{"WI-002"}},
		{ID: "WI-002", GoalID: "g", StoryRef: "specs/stories/b", Status: Pending, DependsOn: []string{"WI-001"}},
	}
	if err := state.Validate(); err == nil {
		t.Fatal("accepted dependency cycle")
	}
}

func TestSchemaV12RequiresCompletionPolicyAndCounter(t *testing.T) {
	now := time.Date(2026, 9, 19, 4, 0, 0, 0, time.UTC)
	missingPolicy := NewState()
	missingPolicy.Goals = []Goal{{ID: "g", Title: "Goal", Repository: "/repo", Status: GoalActive, ReviewPolicy: ReviewPerGoal, CreatedAt: now, UpdatedAt: now}}
	if err := missingPolicy.Validate(); err == nil || !strings.Contains(err.Error(), "completion policy") {
		t.Fatalf("missing completion policy validation = %v", err)
	}

	missingCounter := NewState()
	missingCounter.NextGoalCompletionEvidenceID = 0
	if err := missingCounter.Validate(); err == nil || !strings.Contains(err.Error(), "next_goal_completion_evidence_id") {
		t.Fatalf("missing completion counter validation = %v", err)
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
	if fresh.NextEvidenceID != 1 || fresh.NextGateID != 1 || fresh.NextGoalCompletionEvidenceID != 1 {
		t.Fatalf("fresh counters = %d, %d, %d, want 1, 1, 1", fresh.NextEvidenceID, fresh.NextGateID, fresh.NextGoalCompletionEvidenceID)
	}

	older := fresh
	older.SchemaVersion = SchemaVersion - 1
	err := older.Validate()
	if err == nil {
		t.Fatal("accepted an older schema version")
	}
	if !strings.Contains(err.Error(), "export-plan") || !strings.Contains(err.Error(), "goal import") {
		t.Fatalf("older-version error %q does not point at the export tool and goal import", err)
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
	if !strings.Contains(err.Error(), "newer") || strings.Contains(err.Error(), "export-plan") {
		t.Fatalf("newer-version error %q must say newer and must not suggest exporting", err)
	}
}

// Work Items imported together share a CreatedAt, so what orders simultaneously
// READY work is the order they sit in state.WorkItems: the Goal Plan's node order.
func TestSelectionUsesTimestampThenImportOrder(t *testing.T) {
	old, same := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	state := State{SchemaVersion: SchemaVersion, NextEvidenceID: 1, NextGateID: 1, NextVerificationRunID: 1, NextGoalCompletionEvidenceID: 1, Goals: []Goal{{ID: "active", Title: "Active", Repository: "/repo", Status: GoalActive, ReviewPolicy: ReviewPerWorkItem, CompletionPolicy: CompletionHuman}, {ID: "blocked", Title: "Blocked", Repository: "/repo", Status: GoalBlocked, ReviewPolicy: ReviewPerWorkItem, CompletionPolicy: CompletionHuman}}, WorkItems: []Item{
		{ID: "zeta", GoalID: "active", StoryRef: "specs/stories/c", Status: Ready, CreatedAt: same},
		{ID: "alpha", GoalID: "active", StoryRef: "specs/stories/b", Status: Ready, CreatedAt: same},
		{ID: "mid", GoalID: "active", StoryRef: "specs/stories/a", Status: Ready, CreatedAt: old},
		{ID: "other", GoalID: "blocked", StoryRef: "specs/stories/d", Status: Ready, CreatedAt: old},
	}}
	next, ok := state.Next()
	if !ok || next.ID != "mid" {
		t.Fatalf("next = %#v, %v; want the oldest READY work in an active Goal", next, ok)
	}
	state.WorkItems[2].Status = Done
	next, ok = state.Next()
	if !ok || next.ID != "zeta" {
		t.Fatalf("next = %#v, %v; want node order (zeta before alpha) to break a timestamp tie, not ID order", next, ok)
	}
}

// IDs are only ever written by goal import, but they end up in paths and ref
// names, so a hand-edited or damaged state must not be able to smuggle one in.
func TestValidateRejectsMalformedGoalAndWorkItemIDs(t *testing.T) {
	build := func(goalID, itemID string) State {
		state := NewState()
		state.Goals = []Goal{{ID: goalID, Title: "Goal", Repository: "/repo", Status: GoalActive, ReviewPolicy: ReviewPerWorkItem, CompletionPolicy: CompletionHuman}}
		state.WorkItems = []Item{{ID: itemID, GoalID: goalID, StoryRef: "specs/stories/a", Status: Ready}}
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

// TestRemovedStatusesAreRejected pins the status set to the six that survived
// M3's decision that blocking is not a status: a snapshot naming WAITING_HUMAN
// or a Work Item BLOCKED must be refused rather than quietly carried forward.
func TestRemovedStatusesAreRejected(t *testing.T) {
	for _, removed := range []Status{"WAITING_HUMAN", "BLOCKED"} {
		state := State{SchemaVersion: SchemaVersion, NextEvidenceID: 1, NextGateID: 1, NextVerificationRunID: 1, NextGoalCompletionEvidenceID: 1,
			Goals:     []Goal{{ID: "g", Title: "Goal", Repository: "/repo", Status: GoalActive, ReviewPolicy: ReviewPerWorkItem, CompletionPolicy: CompletionHuman}},
			WorkItems: []Item{{ID: "WI-001", GoalID: "g", StoryRef: "specs/stories/a", Status: removed}}}
		if err := state.Validate(); err == nil {
			t.Fatalf("accepted removed status %q", removed)
		}
	}
}
