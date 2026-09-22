package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/control"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
)

func TestRequestStopPersistsPauseBeforeRefusingUnknownWorker(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := storage.Init(root); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	record := Record{RunID: "run-20260922t090000-a1b2c3", Workspace: root, GoalID: "G-001"}
	if err := record.save(root, storage.ArtifactLimits{}, now); err != nil {
		t.Fatal(err)
	}

	err := RequestStop(root, "G-001", "Carl", "stop for inspection", now)
	if err == nil || !strings.Contains(err.Error(), "no recorded worker") {
		t.Fatalf("RequestStop error = %v, want fail-closed unknown-worker refusal", err)
	}
	state, err := control.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if state.Pause == nil || state.Pause.GoalID != "G-001" || state.Pause.RunID != record.RunID ||
		state.Pause.Reason != "stop for inspection" || state.Pause.RequestedBy != "Carl" {
		t.Fatalf("durable pause = %#v, want the requested stop intent", state.Pause)
	}
}

func TestPersistedPauseStopsBeforeANewAction(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := storage.Init(root); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	record := Record{RunID: "run-20260922t090000-a1b2c3", Workspace: root, GoalID: "G-001",
		Deadline: now.Add(time.Hour)}
	if err := record.save(root, storage.ArtifactLimits{}, now); err != nil {
		t.Fatal(err)
	}
	if err := control.Update(root, func(state *control.State) error {
		return state.SetPause(control.Pause{GoalID: record.GoalID, RunID: record.RunID,
			Reason: "operator pause", RequestedBy: "Carl", RequestedAt: now})
	}); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{options: Options{Root: root, Now: func() time.Time { return now }}, record: &record}
	stopped, err := runner.beforeAction()
	if err != nil || !stopped {
		t.Fatalf("beforeAction = (%t, %v), want durable pause stop", stopped, err)
	}
	if record.Stop == nil || record.Stop.Reason != StopUserPaused {
		t.Fatalf("stop = %#v, want USER_PAUSED", record.Stop)
	}
}
