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

func TestAddWorkWithRepositoryAndExternalRefReturnsExistingMatchingRequest(t *testing.T) {
	now := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	state := NewState()
	if err := state.AddGoal("g", "Goal", "", "/repo", now); err != nil {
		t.Fatal(err)
	}

	first, created, err := state.AddWorkWithRepositoryAndExternalRef("g", "specs/stories/a", nil, "PB-001", RepositoryState{}, now)
	if err != nil || !created {
		t.Fatalf("first add = %#v, created=%v, err=%v", first, created, err)
	}
	beforeRetry := state
	repeated, created, err := state.AddWorkWithRepositoryAndExternalRef("g", "specs/stories/a", nil, "PB-001", RepositoryState{}, now.Add(time.Minute))
	if err != nil || created {
		t.Fatalf("retry = %#v, created=%v, err=%v", repeated, created, err)
	}
	if !reflect.DeepEqual(repeated, first) {
		t.Fatalf("retry item = %#v, want %#v", repeated, first)
	}
	if !reflect.DeepEqual(state, beforeRetry) {
		t.Fatalf("matching retry changed state:\nbefore: %#v\nafter:  %#v", beforeRetry, state)
	}
	if _, _, err := state.AddWorkWithRepositoryAndExternalRef("g", "specs/stories/b", nil, "PB-001", RepositoryState{}, now); err == nil {
		t.Fatal("accepted a conflicting external reference")
	}
	beforeDependencyConflict := state
	if _, _, err := state.AddWorkWithRepositoryAndExternalRef("g", "specs/stories/a", []string{first.ID}, "PB-001", RepositoryState{}, now); err == nil {
		t.Fatal("accepted a conflicting dependency set for an external reference")
	}
	if !reflect.DeepEqual(state, beforeDependencyConflict) {
		t.Fatalf("dependency conflict changed state:\nbefore: %#v\nafter:  %#v", beforeDependencyConflict, state)
	}
	if err := state.AddGoal("other", "Other", "", "/repo", now); err != nil {
		t.Fatal(err)
	}
	if _, created, err := state.AddWorkWithRepositoryAndExternalRef("other", "specs/stories/a", nil, "PB-001", RepositoryState{}, now); err != nil || !created {
		t.Fatalf("same key in another Goal = created=%v, err=%v", created, err)
	}
	state.Goals[0].Status, state.Goals[0].Reason = GoalBlocked, "paused"
	beforeInactiveRetry := state
	repeated, created, err = state.AddWorkWithRepositoryAndExternalRef("g", "specs/stories/a", nil, "PB-001", RepositoryState{}, now.Add(time.Minute))
	if err != nil || created || repeated.ID != first.ID {
		t.Fatalf("inactive Goal retry = %#v, created=%v, err=%v", repeated, created, err)
	}
	if !reflect.DeepEqual(state, beforeInactiveRetry) {
		t.Fatalf("inactive Goal retry changed state:\nbefore: %#v\nafter:  %#v", beforeInactiveRetry, state)
	}
	if _, _, err := state.AddWorkWithRepositoryAndExternalRef("g", "specs/stories/a", nil, "", RepositoryState{}, now); err == nil {
		t.Fatal("accepted an empty external reference")
	}
	if _, _, err := state.AddWorkWithRepositoryAndExternalRef("g", "specs/stories/a", nil, "bad\nreference", RepositoryState{}, now); err == nil {
		t.Fatal("accepted a control character in an external reference")
	}
}

func TestExternalReferenceRetryTreatsDependenciesAsASet(t *testing.T) {
	now := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	state := NewState()
	if err := state.AddGoal("g", "Goal", "", "/repo", now); err != nil {
		t.Fatal(err)
	}
	first, err := state.AddWork("g", "specs/stories/a", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := state.AddWork("g", "specs/stories/b", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	created, wasCreated, err := state.AddWorkWithRepositoryAndExternalRef("g", "specs/stories/c", []string{first.ID, second.ID}, "PB-003", RepositoryState{}, now)
	if err != nil || !wasCreated {
		t.Fatalf("initial dependency request = %#v, created=%v, err=%v", created, wasCreated, err)
	}
	retry, wasCreated, err := state.AddWorkWithRepositoryAndExternalRef("g", "specs/stories/c", []string{second.ID, first.ID}, "PB-003", RepositoryState{}, now.Add(time.Minute))
	if err != nil || wasCreated || retry.ID != created.ID {
		t.Fatalf("reordered dependency retry = %#v, created=%v, err=%v", retry, wasCreated, err)
	}
}

func TestValidateRejectsDuplicateExternalReferenceWithinGoal(t *testing.T) {
	now := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	state := NewState()
	if err := state.AddGoal("g", "Goal", "", "/repo", now); err != nil {
		t.Fatal(err)
	}
	first, created, err := state.AddWorkWithRepositoryAndExternalRef("g", "specs/stories/a", nil, "PB-001", RepositoryState{}, now)
	if err != nil || !created {
		t.Fatalf("first add = %#v, created=%v, err=%v", first, created, err)
	}
	duplicate := first
	duplicate.ID, duplicate.StoryRef = "WI-002", "specs/stories/b"
	state.WorkItems = append(state.WorkItems, duplicate)
	state.NextWorkID = 3
	if err := state.Validate(); err == nil {
		t.Fatal("validated duplicate Goal-scoped external reference")
	}
}

func TestRefreshAndValidation(t *testing.T) {
	now := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	state := State{SchemaVersion: SchemaVersion, NextWorkID: 3, NextEvidenceID: 1, NextGateID: 1, NextVerificationRunID: 1, NextGoalCompletionEvidenceID: 1, Goals: []Goal{{ID: "g", Title: "Goal", Repository: "/repo", Status: GoalActive, ReviewPolicy: ReviewPerWorkItem, CompletionPolicy: CompletionHuman}}, WorkItems: []Item{
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
	state := State{SchemaVersion: SchemaVersion, NextWorkID: 4, NextEvidenceID: 1, NextGateID: 1, NextVerificationRunID: 1, NextGoalCompletionEvidenceID: 1, Goals: []Goal{{ID: "active", Title: "Active", Repository: "/repo", Status: GoalActive, ReviewPolicy: ReviewPerWorkItem, CompletionPolicy: CompletionHuman}, {ID: "blocked", Title: "Blocked", Repository: "/repo", Status: GoalBlocked, ReviewPolicy: ReviewPerWorkItem, CompletionPolicy: CompletionHuman}}, WorkItems: []Item{
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
	if SchemaVersion != 16 {
		t.Fatalf("SchemaVersion = %d, want 16", SchemaVersion)
	}
	fresh := NewState()
	if fresh.NextEvidenceID != 1 || fresh.NextGateID != 1 {
		t.Fatalf("fresh counters = %d, %d, want 1, 1", fresh.NextEvidenceID, fresh.NextGateID)
	}
	if fresh.NextGoalCompletionEvidenceID != 1 {
		t.Fatalf("fresh Goal completion counter = %d, want 1", fresh.NextGoalCompletionEvidenceID)
	}
	if len(fresh.Evidence) != 0 || len(fresh.Gates) != 0 {
		t.Fatalf("fresh state carries %d evidence and %d gates", len(fresh.Evidence), len(fresh.Gates))
	}
	older := fresh
	older.SchemaVersion = 7
	err := older.Validate()
	if err == nil {
		t.Fatal("accepted an older schema version")
	}
	if !strings.Contains(err.Error(), "migrate") {
		t.Fatalf("older-version error %q does not tell the user to migrate", err)
	}
	newer := fresh
	newer.SchemaVersion = 17
	err = newer.Validate()
	if err == nil {
		t.Fatal("accepted a newer schema version")
	}
	if strings.Contains(err.Error(), "migrate") {
		t.Fatalf("newer-version error %q wrongly suggests migrating", err)
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

// TestRemovedStatusesAreRejected pins the status set to the six that survived
// M3's decision that blocking is not a status: a snapshot naming WAITING_HUMAN
// or a Work Item BLOCKED must be refused rather than quietly carried forward.
func TestRemovedStatusesAreRejected(t *testing.T) {
	for _, removed := range []Status{"WAITING_HUMAN", "BLOCKED"} {
		state := State{SchemaVersion: SchemaVersion, NextWorkID: 2, NextEvidenceID: 1, NextGateID: 1, NextVerificationRunID: 1, NextGoalCompletionEvidenceID: 1,
			Goals:     []Goal{{ID: "g", Title: "Goal", Repository: "/repo", Status: GoalActive, ReviewPolicy: ReviewPerWorkItem, CompletionPolicy: CompletionHuman}},
			WorkItems: []Item{{ID: "WI-001", GoalID: "g", StoryRef: "specs/stories/a", Status: removed}}}
		if err := state.Validate(); err == nil {
			t.Fatalf("accepted removed status %q", removed)
		}
	}
}
