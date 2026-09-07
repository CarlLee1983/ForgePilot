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
	state := State{SchemaVersion: SchemaVersion, NextWorkID: 3, NextEvidenceID: 1, NextGateID: 1, Goals: []Goal{{ID: "g", Title: "Goal", Repository: "/repo", Status: GoalActive}}, WorkItems: []Item{
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
	state.WorkItems[1].ID, state.WorkItems[1].DependsOn = "bad", nil
	if err := state.Validate(); err == nil {
		t.Fatal("accepted malformed work ID")
	}
	state.WorkItems = []Item{
		{ID: "WI-001", GoalID: "g", StoryRef: "specs/stories/a", Status: Pending, DependsOn: []string{"WI-002"}},
		{ID: "WI-002", GoalID: "g", StoryRef: "specs/stories/b", Status: Pending, DependsOn: []string{"WI-001"}},
	}
	if err := state.Validate(); err == nil {
		t.Fatal("accepted dependency cycle")
	}
}

func TestSelectionUsesTimestampThenID(t *testing.T) {
	old, same := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	state := State{SchemaVersion: SchemaVersion, NextWorkID: 4, NextEvidenceID: 1, NextGateID: 1, Goals: []Goal{{ID: "active", Title: "Active", Repository: "/repo", Status: GoalActive}, {ID: "blocked", Title: "Blocked", Repository: "/repo", Status: GoalBlocked}}, WorkItems: []Item{
		{ID: "WI-003", GoalID: "active", StoryRef: "specs/stories/c", Status: Ready, CreatedAt: same},
		{ID: "WI-002", GoalID: "active", StoryRef: "specs/stories/b", Status: Ready, CreatedAt: same},
		{ID: "WI-001", GoalID: "active", StoryRef: "specs/stories/a", Status: Ready, CreatedAt: old},
		{ID: "WI-004", GoalID: "blocked", StoryRef: "specs/stories/d", Status: Ready, CreatedAt: old},
	}}
	state.NextWorkID = 5
	next, ok := state.Next()
	if !ok || next.ID != "WI-001" {
		t.Fatalf("next = %#v, %v", next, ok)
	}
}

func TestSchemaVersionErrorsDistinguishOlderFromNewer(t *testing.T) {
	if SchemaVersion != 3 {
		t.Fatalf("SchemaVersion = %d, want 3", SchemaVersion)
	}
	fresh := NewState()
	if fresh.NextEvidenceID != 1 || fresh.NextGateID != 1 {
		t.Fatalf("fresh counters = %d, %d, want 1, 1", fresh.NextEvidenceID, fresh.NextGateID)
	}
	if len(fresh.Evidence) != 0 || len(fresh.Gates) != 0 {
		t.Fatalf("fresh state carries %d evidence and %d gates", len(fresh.Evidence), len(fresh.Gates))
	}
	older := fresh
	older.SchemaVersion = 2
	err := older.Validate()
	if err == nil {
		t.Fatal("accepted an older schema version")
	}
	if !strings.Contains(err.Error(), "migrate") {
		t.Fatalf("older-version error %q does not tell the user to migrate", err)
	}
	newer := fresh
	newer.SchemaVersion = 4
	err = newer.Validate()
	if err == nil {
		t.Fatal("accepted a newer schema version")
	}
	if strings.Contains(err.Error(), "migrate") {
		t.Fatalf("newer-version error %q wrongly suggests migrating", err)
	}
}

// TestRemovedStatusesAreRejected pins the status set to the six that survived
// M3's decision that blocking is not a status: a snapshot naming WAITING_HUMAN
// or a Work Item BLOCKED must be refused rather than quietly carried forward.
func TestRemovedStatusesAreRejected(t *testing.T) {
	for _, removed := range []Status{"WAITING_HUMAN", "BLOCKED"} {
		state := State{SchemaVersion: SchemaVersion, NextWorkID: 2, NextEvidenceID: 1, NextGateID: 1,
			Goals:     []Goal{{ID: "g", Title: "Goal", Repository: "/repo", Status: GoalActive}},
			WorkItems: []Item{{ID: "WI-001", GoalID: "g", StoryRef: "specs/stories/a", Status: removed}}}
		if err := state.Validate(); err == nil {
			t.Fatalf("accepted removed status %q", removed)
		}
	}
}
