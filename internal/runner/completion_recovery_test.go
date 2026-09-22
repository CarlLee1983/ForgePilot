package runner

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestResumeRepairsCompletionAfterFinalPendingClearSaveFails(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	completionRecoveryGit(t, root, "init", "-q")
	completionRecoveryGit(t, root, "config", "user.name", "ForgePilot test")
	completionRecoveryGit(t, root, "config", "user.email", "forgepilot-test@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("baseline\n"), 0644); err != nil {
		t.Fatal(err)
	}
	completionRecoveryGit(t, root, "add", "tracked.txt")
	completionRecoveryGit(t, root, "commit", "-m", "baseline")
	revision := strings.TrimSpace(completionRecoveryGit(t, root, "rev-parse", "HEAD"))
	if err := storage.Init(root); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 9, 19, 6, 0, 0, 0, time.UTC)
	if err := storage.Update(root, func(state *work.State) error {
		if err := state.AddGoalWithPolicies("g", "Goal", "", root, work.ReviewPerGoal, work.CompletionVerified, now); err != nil {
			return err
		}
		item, err := state.AddWork("g", "specs/stories/one", nil, now)
		if err != nil {
			return err
		}
		if err := state.Start(item.ID, now); err != nil {
			return err
		}
		if err := state.BeginVerification(item.ID, revision, "/tmp/worktree", "", now); err != nil {
			return err
		}
		_, err = state.RecordVerificationWithRepository(item.ID, revision, "make verify", 0, work.RepositoryState{Revision: revision}, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	action := state.ActionableNextForGoal("g", work.RepositoryState{Revision: revision})
	if action.Kind != work.NextActionCompleteGoal {
		t.Fatalf("action = %#v, want COMPLETE_GOAL", action)
	}

	limits := testLimits()
	record := Record{
		RunID: "run-20260919t060000-abcdef", Workspace: root, GoalID: "g", GoalTitle: "Goal",
		CompletionPolicy: work.CompletionVerified, RuntimeName: "missing-runtime", Snapshot: true,
		Budget: testBudget(), Limits: limits, StartedAt: now, Deadline: now.Add(time.Hour),
		Attempts: map[string]int{}, HumanWaits: map[string]int{},
	}
	runner := &Runner{
		options: Options{Root: root, GoalID: "g", Budget: testBudget(), Limits: limits, Now: func() time.Time { return now }},
		record:  &record,
	}
	saveCount := 0
	runner.saveRecord = func() error {
		saveCount++
		if saveCount == 3 {
			return errors.New("injected run-record save failure after Goal commit")
		}
		return runner.record.save(root, limits, now)
	}
	if err := runner.completeGoal(action); err == nil || !strings.Contains(err.Error(), "injected run-record save failure") {
		t.Fatalf("completion error = %v, want injected pending-clear save failure", err)
	}
	if saveCount != 3 {
		t.Fatalf("save attempts = %d, want failure on post-commit pending clear", saveCount)
	}
	committed, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if committed.Goals[0].Status != work.GoalCompleted || len(committed.GoalCompletionEvidence) != 1 {
		t.Fatalf("Goal transaction did not commit before injected record failure: %#v", committed.Goals[0])
	}
	interrupted, err := LoadRecord(root, record.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if pending := interrupted.UnresolvedPending(); len(pending) != 1 || pending[0].Purpose != PurposeGoalCompletion {
		t.Fatalf("durable pending state = %#v, want tagged final facts read", pending)
	}

	// Resume must prove that this particular facts read returned by consulting
	// committed aggregate evidence, clear only its pending marker, then repair
	// the terminal run record before touching readiness or the missing runtime.
	resumed, err := Resume(Options{Root: root, Now: func() time.Time { return now }}, record.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Stop == nil || resumed.Stop.Reason != StopGoalCompleted {
		t.Fatalf("resumed stop = %#v, want GOAL_COMPLETED", resumed.Stop)
	}
	if len(resumed.UnresolvedPending()) != 0 {
		t.Fatalf("resumed run still has pending executions: %#v", resumed.UnresolvedPending())
	}
	if len(resumed.Stop.EvidenceIDs) != 2 || resumed.Stop.EvidenceIDs[0] != "GC-001" {
		t.Fatalf("repaired evidence IDs = %v", resumed.Stop.EvidenceIDs)
	}
}

func TestLegacyGoalCompletionDoesNotProvePendingCandidateFactsRead(t *testing.T) {
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
	now := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
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
	runner := &Runner{
		options: Options{Root: root},
		record:  &Record{RunID: "legacy-completion-recovery", GoalID: "g"},
	}
	pending := PendingExecution{Purpose: PurposeGoalCompletion, Kind: KindGit, Phase: PhasePendingStart}
	committed, err := runner.committedGoalCompletionRead(pending)
	if err != nil {
		t.Fatal(err)
	}
	if committed {
		t.Fatal("legacy terminal provenance incorrectly proved a current Candidate facts read")
	}
}

func completionRecoveryGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return string(output)
}
