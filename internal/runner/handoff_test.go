package runner

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/agent"
	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/readiness"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

type refusingRuntime struct {
	plans int
}

func (*refusingRuntime) Name() string                { return "refusing" }
func (*refusingRuntime) Executable() (string, error) { return "/never/launched", nil }
func (*refusingRuntime) Version() (string, error)    { return "test", nil }
func (*refusingRuntime) SessionEnvironment() agent.SessionEnvironment {
	return agent.SessionEnvironment{Sandbox: agent.SandboxNotConfiguredByForgePilot}
}
func (runtime *refusingRuntime) Plan(agent.Request) (agent.Plan, error) {
	runtime.plans++
	return agent.Plan{}, nil
}

// A quoted excerpt must say when it cut. The briefing it lands in is what a
// session uses to decide what went wrong, and an excerpt that silently starts
// mid-log reads as the whole story.
func TestTailSaysWhenItCut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verify.log")
	var builder strings.Builder
	for i := 0; i < 400; i++ {
		builder.WriteString("a line of output that is long enough to matter\n")
	}
	builder.WriteString("the cause is here\n")
	if err := os.WriteFile(path, []byte(builder.String()), 0644); err != nil {
		t.Fatal(err)
	}

	excerpt := tail(path, 200)
	if !strings.Contains(excerpt, "the cause is here") {
		t.Fatalf("the end of the log is missing:\n%s", excerpt)
	}
	if !strings.Contains(excerpt, "earlier output omitted") {
		t.Fatalf("a truncated excerpt does not say it was truncated:\n%s", excerpt)
	}
	if strings.HasPrefix(excerpt, "a line") || strings.Contains(excerpt, "\nne of output") {
		t.Fatalf("the excerpt starts mid-line:\n%s", excerpt)
	}
}

// A log shorter than the limit is quoted whole, with nothing claiming it was cut.
func TestTailQuotesAShortLogWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verify.log")
	if err := os.WriteFile(path, []byte("canonical check failed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if excerpt := tail(path, 4096); excerpt != "canonical check failed" {
		t.Fatalf("excerpt = %q", excerpt)
	}
}

func TestAgentProfileForActionUsesTypedActionAndRuntimeEnvironment(t *testing.T) {
	environment := agent.SessionEnvironment{Sandbox: agent.SandboxWorkspaceWrite}
	for _, test := range []struct {
		action work.NextActionKind
		want   agent.SessionCheckKind
	}{
		{action: work.NextActionResume, want: agent.SessionCheckImplementation},
		{action: work.NextActionRepair, want: agent.SessionCheckRepair},
	} {
		profile, err := agentProfileForAction(test.action, environment)
		if err != nil {
			t.Fatalf("profile for %s: %v", test.action, err)
		}
		if profile.Kind != test.want || profile.Environment != environment {
			t.Fatalf("profile for %s = %#v, want kind %s and environment %#v", test.action, profile, test.want, environment)
		}
	}
	if _, err := agentProfileForAction(work.NextActionStart, environment); err == nil {
		t.Fatal("profile accepted an action without a check-profile mapping")
	}
}

// A decision can become stale after GoalDecision reads state but before a
// session is launched. The implementation boundary must re-project current
// state, withdraw its prospective worker, and let the loop observe WAIT_GATE;
// the runtime's Plan method is the observable launch seam.
func TestAnOpenGateInvalidatesAStaleResumeBeforeRuntimeLaunch(t *testing.T) {
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
	writeReadyStory(t, root, "a")
	now := time.Date(2026, 9, 16, 1, 2, 3, 0, time.UTC)
	if err := storage.Update(root, func(state *work.State) error {
		if err := state.AddGoalWithReviewPolicy("g", "Goal", "", root, work.ReviewPerGoal, now); err != nil {
			return err
		}
		item, err := state.AddWork("g", "specs/stories/a", nil, now)
		if err != nil {
			return err
		}
		return state.Start(item.ID, now)
	}); err != nil {
		t.Fatal(err)
	}
	stale, err := app.GoalDecision(t.Context(), root, "g")
	if err != nil {
		t.Fatal(err)
	}
	if stale.Action.Kind != work.NextActionResume {
		t.Fatalf("initial action = %s, want RESUME", stale.Action.Kind)
	}
	if err := storage.Update(root, func(state *work.State) error {
		_, err := state.OpenGate(stale.Action.Item.ID, "late decision?", []string{"stop", "continue"}, "", now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	scope, err := app.CurrentGoalScope(root, "g")
	if err != nil {
		t.Fatal(err)
	}
	runtime := &refusingRuntime{}
	runID := "run-20260916t000000-abcdef"
	budget := Budget{MaxSteps: 10, MaxAttemptsPerWork: 3, MaxDuration: time.Hour,
		AgentTimeout: time.Minute, VerifyTimeout: time.Minute, MaxHandoffBytes: 64 * 1024}
	runner := &Runner{
		options: Options{Root: root, GoalID: "g", Now: func() time.Time { return now }, Budget: budget},
		runtime: runtime,
		record: &Record{RunID: runID, GoalID: "g", GoalTitle: "Goal", Workspace: root,
			Scope: scope, Budget: budget, Deadline: now.Add(time.Hour),
			Attempts: map[string]int{}, HumanWaits: map[string]int{}},
	}
	if err := runner.implement(stale.Action, stale); err != nil {
		t.Fatal(err)
	}
	if runtime.plans != 0 {
		t.Fatalf("runtime planned %d sessions after the action became stale", runtime.plans)
	}
	stored, err := LoadRecord(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Attempts[stale.Action.Item.ID] != 1 || stored.Steps != 1 {
		t.Fatalf("stale launch reservation was not durable: attempts %v, steps %d", stored.Attempts, stored.Steps)
	}
	if stored.Worker != nil {
		t.Fatalf("stale action left a prospective worker behind: %#v", stored.Worker)
	}
	if err := runner.loop(); err != nil {
		t.Fatal(err)
	}
	if runner.record.Stop == nil || runner.record.Stop.Reason != StopWaitGate {
		t.Fatalf("next loop stop = %#v, want WAIT_GATE", runner.record.Stop)
	}
	if runtime.plans != 0 {
		t.Fatalf("runtime planned %d sessions while the Gate was open", runtime.plans)
	}
}

func writeReadyStory(t *testing.T, root, name string) {
	t.Helper()
	directory := filepath.Join(root, "specs", "stories", name)
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	story, acceptance := []byte("# Story\n"), []byte("# Acceptance\n")
	sidecar := `{"schema_version":1,"story_ref":"specs/stories/` + name + `","story_md_digest":"` + readiness.Digest(story) + `","acceptance_md_digest":"` + readiness.Digest(acceptance) + `"}`
	for file, contents := range map[string][]byte{"readiness.json": []byte(sidecar), "story.md": story, "acceptance.md": acceptance} {
		if err := os.WriteFile(filepath.Join(directory, file), contents, 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func mustLoadState(t *testing.T, root string) *work.State {
	t.Helper()
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return &state
}

// Digesting governance state is preparation, not a process launch. If that
// read fails, the fail-closed attempt charge remains durable but recovery must
// not see a phantom worker that never could have started.
func TestStateDigestFailureKeepsTheAttemptWithoutAPhantomWorker(t *testing.T) {
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
	runID := "run-20260916t000000-abcdef"
	now := time.Date(2026, 9, 16, 1, 2, 3, 0, time.UTC)
	if err := storage.Update(root, func(state *work.State) error {
		return state.AddGoalWithReviewPolicy("g", "Goal", "", root, work.ReviewPerGoal, now)
	}); err != nil {
		t.Fatal(err)
	}
	limits := testLimits()
	runner := &Runner{
		options: Options{
			Root: root, Now: func() time.Time { return now },
			Budget: Budget{MaxSteps: 10, MaxAttemptsPerWork: 3, MaxDuration: time.Hour,
				AgentTimeout: time.Minute, VerifyTimeout: time.Minute, MaxHandoffBytes: 64 * 1024}, Limits: limits,
		},
		record: &Record{
			RunID: runID, Workspace: root, GoalID: "g", GoalTitle: "Goal",
			Deadline: now.Add(time.Hour), Limits: limits, Attempts: map[string]int{}, HumanWaits: map[string]int{},
		},
	}
	checkpointed := false
	runner.saveRecord = func() error {
		if err := runner.record.save(root, limits, now); err != nil {
			return err
		}
		if !checkpointed {
			checkpointed = true
			return os.Remove(filepath.Join(root, ".forgepilot", "state.json"))
		}
		return nil
	}
	action := work.NextAction{Item: work.Item{ID: "WI-001", GoalID: "g", Status: work.Running}, Kind: work.NextActionResume}
	if err := runner.implement(action, app.Decision{}); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("implement error = %v, want missing state digest", err)
	}
	stored, err := LoadRecord(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Attempts["WI-001"] != 1 {
		t.Fatalf("attempt charge = %v, want one", stored.Attempts)
	}
	if stored.Worker != nil {
		t.Fatalf("digest failure left a phantom worker: %#v", stored.Worker)
	}
}
