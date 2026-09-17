package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestResumeStopsLegacyRunBeforeRuntimeResolution(t *testing.T) {
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
	// readiness sidecar. Resuming it must fail closed before the runtime lookup.
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

	resumed, err := Resume(Options{Root: root, Now: func() time.Time { return now }}, runID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Stop == nil || resumed.Stop.Reason != StopReadinessPreflight {
		t.Fatalf("resume stop = %#v, want readiness preflight", resumed.Stop)
	}
	if !strings.Contains(resumed.Stop.Detail, string(readiness.MissingContract)) {
		t.Fatalf("resume detail = %q, want missing readiness contract", resumed.Stop.Detail)
	}
}

func testBudget() Budget {
	return Budget{MaxSteps: 10, MaxAttemptsPerWork: 3, MaxDuration: time.Hour, AgentTimeout: time.Minute, VerifyTimeout: time.Minute, MaxHandoffBytes: 64 * 1024}
}

func testLimits() storage.ArtifactLimits {
	return storage.ArtifactLimits{MaxWriteBytes: 1 << 20, MaxRunBytes: 16 << 20, MaxTotalBytes: 128 << 20}
}
