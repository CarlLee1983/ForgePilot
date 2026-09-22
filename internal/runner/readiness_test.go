package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/readiness"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestDryRunRefusesReadinessMismatchBeforeRuntimeResolution(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := storage.Init(root); err != nil {
		t.Fatal(err)
	}
	storyDir := filepath.Join(root, "specs", "stories", "example")
	if err := os.MkdirAll(storyDir, 0755); err != nil {
		t.Fatal(err)
	}
	story, acceptance := []byte("# Story\n"), []byte("# Acceptance\n")
	sidecar := `{"schema_version":1,"story_ref":"specs/stories/example","story_md_digest":"` + readiness.Digest(story) + `","acceptance_md_digest":"` + readiness.Digest(acceptance) + `"}`
	for name, contents := range map[string][]byte{
		"readiness.json": []byte(sidecar), "story.md": story, "acceptance.md": []byte("# Changed\n"),
	} {
		if err := os.WriteFile(filepath.Join(storyDir, name), contents, 0644); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	if err := storage.Update(root, func(state *work.State) error {
		if err := state.AddGoalWithReviewPolicy("g", "Goal", "", root, work.ReviewPerGoal, now); err != nil {
			return err
		}
		_, err := state.AddWork("g", "specs/stories/example", nil, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_, err = DryRun(Options{Root: root, GoalID: "g", RuntimeName: "not-a-runtime", Snapshot: true, Budget: testBudget(), Limits: testLimits()})
	if err == nil || !strings.Contains(err.Error(), string(readiness.SourceDigestMismatch)) {
		t.Fatalf("DryRun error = %v, want readiness mismatch before runtime resolution", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".forgepilot", "runs")); !os.IsNotExist(err) {
		t.Fatalf("dry run created a run artifact: %v", err)
	}
}

func TestResumeRefusesLegacyRunBeforeRuntimeResolution(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := storage.Init(root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "specs", "stories"), 0755); err != nil {
		t.Fatal(err)
	}
	// A regular, flat Story is valid historic ForgePilot state but has no v1
	// readiness sidecar. Its old uncharged Run Record must fail closed before
	// either readiness or runtime resolution.
	if err := os.WriteFile(filepath.Join(root, "specs", "stories", "legacy"), []byte("# Legacy\n"), 0644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	if err := storage.Update(root, func(state *work.State) error {
		if err := state.AddGoalWithReviewPolicy("g", "Goal", "", root, work.ReviewPerGoal, now); err != nil {
			return err
		}
		_, err := state.AddWork("g", "specs/stories/legacy", nil, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	runID := "run-20260917t000000-abcdef"
	budget, limits := testBudget(), testLimits()
	record := Record{RunID: runID, Workspace: root, GoalID: "g", GoalTitle: "Goal", RuntimeName: "not-a-runtime", Snapshot: true,
		Budget: budget, Limits: limits, StartedAt: now, Deadline: now.Add(time.Hour), Attempts: map[string]int{}, HumanWaits: map[string]int{},
		Stop: &Stop{Reason: StopInterrupted, At: now}}
	if err := record.save(root, limits, now); err != nil {
		t.Fatal(err)
	}

	if _, err := Resume(Options{Root: root, Now: func() time.Time { return now }}, runID); err == nil ||
		!strings.Contains(err.Error(), "no current execution authorization") {
		t.Fatalf("legacy resume error = %v; want authorization refusal before runtime resolution", err)
	}
}

func TestLegacyRunAdoptsMigratedGoalCompletionPolicy(t *testing.T) {
	assertRunAdoptsMigratedGoalCompletionPolicy(t, "")
}

func TestHumanRunRecordAdoptsMigratedGoalCompletionPolicy(t *testing.T) {
	assertRunAdoptsMigratedGoalCompletionPolicy(t, work.CompletionHuman)
}

func assertRunAdoptsMigratedGoalCompletionPolicy(t *testing.T, recordedPolicy work.CompletionPolicy) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := storage.Init(root); err != nil {
		t.Fatal(err)
	}
	writeReadyStory(t, root, "legacy")
	now := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	if err := storage.Update(root, func(state *work.State) error {
		if err := state.AddGoalWithPolicies("g", "Goal", "", root, work.ReviewPerGoal, work.CompletionVerified, now); err != nil {
			return err
		}
		_, err := state.AddWork("g", "specs/stories/legacy", nil, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	scope, err := app.CurrentGoalScope(root, "g")
	if err != nil {
		t.Fatal(err)
	}
	limits := testLimits()
	runID := "run-20260919t000000-abcdef"
	runner := &Runner{
		options: Options{Root: root, GoalID: "g", Now: func() time.Time { return now }, Limits: limits},
		record: &Record{RunID: runID, Workspace: root, GoalID: "g", GoalTitle: "Goal", Scope: scope,
			CompletionPolicy: recordedPolicy, Budget: testBudget(), Limits: limits,
			Deadline: now.Add(time.Hour), Attempts: map[string]int{}},
	}
	if stopped, err := runner.checkScope(); stopped || err != nil {
		t.Fatalf("checkScope = %v, %v", stopped, err)
	}
	if runner.record.CompletionPolicy != work.CompletionVerified {
		t.Fatalf("adopted completion policy = %q, want VERIFIED", runner.record.CompletionPolicy)
	}
	stored, err := LoadRecord(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CompletionPolicy != work.CompletionVerified {
		t.Fatalf("persisted completion policy = %q, want VERIFIED", stored.CompletionPolicy)
	}
}

func TestResumeRepairsCommittedGoalBeforeReadinessAndRuntimePreflight(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := storage.Init(root); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 19, 1, 0, 0, 0, time.UTC)
	revision := "1111111111111111111111111111111111111111"
	if err := storage.Update(root, func(state *work.State) error {
		if err := state.AddGoalWithPolicies("g", "Goal", "", root, work.ReviewPerGoal, work.CompletionVerified, now); err != nil {
			return err
		}
		item, err := state.AddWork("g", "specs/stories/missing-readiness", nil, now)
		if err != nil {
			return err
		}
		if err := state.Start(item.ID, now); err != nil {
			return err
		}
		if err := state.BeginVerification(item.ID, revision, "/tmp/worktree", "", now); err != nil {
			return err
		}
		if _, err := state.RecordVerificationWithRepository(item.ID, revision, "make verify", 0, work.RepositoryState{Revision: revision}, now); err != nil {
			return err
		}
		summary, err := state.GoalSummary("g", work.RepositoryState{Revision: revision})
		if err != nil {
			return err
		}
		_, err = state.CompleteVerifiedGoal("g", work.RepositoryState{Revision: revision}, summary.VerificationEvidenceIDs, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	limits := testLimits()
	runID := "run-20260919t010000-abcdef"
	record := Record{
		RunID: runID, Workspace: root, GoalID: "g", GoalTitle: "Goal", RuntimeName: "missing-runtime", Snapshot: true,
		Budget: testBudget(), Limits: limits, StartedAt: now, Deadline: now.Add(time.Hour), Attempts: map[string]int{},
		Stop: &Stop{Reason: StopInterrupted, At: now},
	}
	if err := record.save(root, limits, now); err != nil {
		t.Fatal(err)
	}

	resumed, err := Resume(Options{Root: root, Now: func() time.Time { return now }}, runID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Stop == nil || resumed.Stop.Reason != StopGoalCompleted {
		t.Fatalf("resume stop = %#v, want GOAL_COMPLETED", resumed.Stop)
	}
	if len(resumed.Stop.EvidenceIDs) != 2 || resumed.Stop.EvidenceIDs[0] != "GC-001" {
		t.Fatalf("completion evidence IDs = %v", resumed.Stop.EvidenceIDs)
	}
}

func testBudget() Budget {
	return Budget{MaxSteps: 10, MaxAttemptsPerWork: 3, MaxDuration: time.Hour, AgentTimeout: time.Minute, VerifyTimeout: time.Minute, MaxHandoffBytes: 64 * 1024}
}

func testLimits() storage.ArtifactLimits {
	return storage.ArtifactLimits{MaxWriteBytes: 1 << 20, MaxRunBytes: 16 << 20, MaxTotalBytes: 128 << 20}
}
