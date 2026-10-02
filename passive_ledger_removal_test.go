package forgepilot_test

import (
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// A v18 state written while supervised execution existed carries Goal.Execution.
// ForgePilot no longer reads or writes it, but the strict decoder and Validate
// must still accept the data until the schema 19 break removes the model.
func TestStateWithGoalExecutionDataStaysReadable(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")

	now := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	err := storage.Update(root, func(state *work.State) error {
		repository := state.Goals[0].Repository
		item := state.WorkItems[0]
		digest := strings.Repeat("a", 64)
		bound := "sha256:" + digest
		binding := work.GoalPlanBinding{
			Revision: 1, RequestSHA256: bound, PlanID: "plan-1", PlanRevision: 1, ManifestSHA256: digest,
			CoverageReviewID: "review-1",
			CoverageReview:   work.ExecutionArtifactBinding{Path: "coverage-review.json", SHA256: digest},
			Declaration:      work.ExecutionArtifactBinding{Path: "specs/plans/plan.json", SHA256: digest},
			Nodes: []work.ExecutionPlanNodeBinding{{
				PlanNodeRef: "node-a", StoryRef: item.StoryRef, WorkItemID: item.ID,
				ReadinessContract: work.ExecutionArtifactBinding{Path: "specs/stories/a/readiness.json", SHA256: digest},
			}},
			AdoptedAt: now,
		}
		return state.AdoptInitialExecution(work.GoalExecution{
			GoalID: "queue", Workspace: repository, PlanBindings: []work.GoalPlanBinding{binding},
			Authorizations: []work.ExecutionAuthorization{{
				Revision: 1, GoalID: "queue", Workspace: repository, RequestSHA256: bound, ApprovalToken: bound,
				Approver: "operator", AuthorizedAt: now, ExpiresAt: now.Add(24 * time.Hour),
				Caps: work.ExecutionCaps{MaxSteps: 10, MaxTechnicalAttemptsPerNode: 2, MaxRuns: 2, MaxRecoveries: 1,
					Artifacts: work.ExecutionArtifactLimits{MaxHandoffBytes: 1024, MaxWriteBytes: 1 << 20, MaxRunBytes: 1 << 20, MaxTotalBytes: 1 << 20}},
				WorkerProfile: work.WorkerProfile{Runtime: "codex", ExecutablePath: "/usr/local/bin/codex", ExecutableSHA256: digest, Model: "m", Effort: "medium", Sandbox: "workspace-write"},
			}},
			Ledger: work.ExecutionLedger{Revision: 1, AuthorizationRevision: 1, NodeAttempts: []work.ExecutionNodeAttempts{{PlanNodeRef: "node-a"}}},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if state, err := storage.Load(root); err != nil || state.Goals[0].Execution == nil {
		t.Fatalf("fixture lost its execution data: %v", err)
	}

	output, err := command(binary, root, "status")
	if err != nil || !strings.Contains(output, "WI-001 READY") {
		t.Fatalf("status = %v\n%s", err, output)
	}
	output, err = command(binary, root, "next")
	if err != nil || !strings.Contains(output, "Next: WI-001") {
		t.Fatalf("next = %v\n%s", err, output)
	}
	// A write through the CLI must round-trip the data it no longer understands.
	mustRun(t, binary, root, "start", "WI-001")
	state, err := storage.Load(root)
	if err != nil || state.Goals[0].Execution == nil {
		t.Fatalf("a write through the CLI dropped the execution data: %v", err)
	}
}

func TestRemovedCommandsAreUnknownAndAbsentFromHelp(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	for _, arguments := range [][]string{
		{"run", "--goal", "queue", "--runtime", "fake", "--snapshot"},
		{"execution", "plan", "--request", "x", "--json"},
		{"goal", "preflight", "--request", "x", "--json"},
	} {
		output, err := command(binary, root, arguments...)
		if err == nil || !strings.Contains(output, "unknown") {
			t.Errorf("%v = %v, want an unknown-command failure\n%s", arguments, err, output)
		}
	}
	help, err := command(binary, root, "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(help, "\n") {
		for _, removed := range []string{"  run ", "execution", "preflight"} {
			if strings.Contains(line, removed) {
				t.Errorf("help still mentions %q: %s", removed, line)
			}
		}
	}
}
