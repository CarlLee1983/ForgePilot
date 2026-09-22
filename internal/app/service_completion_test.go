package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestLegacyGoalCompletionIsTerminalButNotAutomaticProof(t *testing.T) {
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
	now := time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)
	if err := storage.Update(root, func(state *work.State) error {
		if err := state.AddGoalWithPolicies("g", "Goal", "", root, work.ReviewPerGoal, work.CompletionVerified, now); err != nil {
			return err
		}
		state.Goals[0].Status = work.GoalCompleted
		state.Goals[0].LegacyCompletion = &work.LegacyGoalCompletion{
			SourceSchemaVersion: 11,
			CompletionPolicy:    work.CompletionHuman,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	decision, err := GoalDecision(context.Background(), root, "g")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action.Kind != work.NextActionGoalCompleted || len(decision.Action.EvidenceIDs) != 0 ||
		!strings.Contains(decision.Action.Reason, "schema v11 HUMAN") {
		t.Fatalf("legacy completed Goal decision = %#v", decision.Action)
	}

	if _, err := CompleteVerifiedGoal(context.Background(), root, "g", nil, Now(func() time.Time { return now })); err == nil ||
		!strings.Contains(err.Error(), "legacy HUMAN completion provenance") {
		t.Fatalf("automatic completion idempotency error = %v; want refusal of legacy provenance", err)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if state.Goals[0].Status != work.GoalCompleted || len(state.GoalCompletionEvidence) != 0 {
		t.Fatalf("legacy completion was altered: %#v / %#v", state.Goals[0], state.GoalCompletionEvidence)
	}
}
