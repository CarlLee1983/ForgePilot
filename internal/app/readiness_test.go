package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/readiness"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestReviewGoalStoryReadinessReportsSourceMismatchWithoutWritingState(t *testing.T) {
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
	before, err := os.ReadFile(filepath.Join(root, ".forgepilot", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	report, err := ReviewGoalStoryReadiness(t.Context(), root, "g")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Defects) != 1 || report.Defects[0].Code != readiness.SourceDigestMismatch {
		t.Fatalf("report = %#v", report)
	}
	after, err := os.ReadFile(filepath.Join(root, ".forgepilot", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("read-only readiness review wrote state")
	}
}
