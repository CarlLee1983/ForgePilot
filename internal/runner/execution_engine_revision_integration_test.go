package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/control"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestEngineRevisionAuditsHistoricalRunsAndPreservesTheirBindings(t *testing.T) {
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
	fixture := filepath.Join("..", "app", "testdata", "goal-plan-artifacts", "v1", "valid")
	for _, relative := range []string{
		"goal-plan-manifest.json", "plan-coverage-review.json",
		"repository/specs/batches/BR-001-goal-plan-fixture/batch.json",
		"repository/specs/stories/EX-001-first/acceptance.md",
		"repository/specs/stories/EX-001-first/story.md",
		"repository/specs/stories/EX-001-first/readiness.json",
		"repository/specs/decisions/ADR-001-example.md",
		"repository/specs/features/example/spec.md",
		"repository/specs/plans/example-goal.json",
	} {
		body, err := os.ReadFile(filepath.Join(fixture, relative))
		if err != nil {
			t.Fatal(err)
		}
		target := relative
		if strings.HasPrefix(target, "repository/") {
			target = strings.TrimPrefix(target, "repository/")
		}
		path := filepath.Join(root, target)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0644); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	if err := storage.Update(root, func(state *work.State) error {
		if err := state.AddGoal("goal", "Goal", "", root, now); err != nil {
			return err
		}
		first, err := state.AddWork("goal", "specs/stories/EX-001-first", nil, now)
		if err != nil {
			return err
		}
		_, err = state.AddWork("goal", "specs/stories/EX-001-first", []string{first.ID}, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	worker := filepath.Join(root, "test-codex")
	if err := os.WriteFile(worker, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(root, "bootstrap-helper")
	if err := os.WriteFile(helper, []byte(`#!/bin/sh
[ "$1" = retention-v1 ] || exit 9
printf '{"protocol_version":1,"result":"%sd","generation_id":"%s","payload_digest":"%s"}\n' "$2" "$4" "$6"
`), 0755); err != nil {
		t.Fatal(err)
	}
	oldGeneration := work.ExecutionEngineGeneration{SourceCommit: strings.Repeat("a", 40), PayloadSHA256: "sha256:" + strings.Repeat("b", 64)}
	newGeneration := work.ExecutionEngineGeneration{SourceCommit: strings.Repeat("c", 40), PayloadSHA256: "sha256:" + strings.Repeat("d", 64)}
	resolver := staticGenerationResolver{resolved: app.ResolvedBootstrapGeneration{Generation: oldGeneration, HelperPath: helper}}
	request := map[string]any{
		"formatVersion": "forgepilot.execution-plan-request/v2",
		"goalPlanRequest": app.GoalPreflightRequest{FormatVersion: "forgepilot.goal-preflight-request/v1", GoalID: "goal",
			ManifestPath: "goal-plan-manifest.json", CoverageReviewPath: "plan-coverage-review.json",
			NodeMappings: []app.GoalPlanNodeMapping{{PlanNodeRef: "node-001", WorkItemID: "WI-001"}, {PlanNodeRef: "node-002", WorkItemID: "WI-002"}}},
		"workerProfile":    map[string]any{"runtime": "codex", "executablePath": worker, "model": "test-model", "effort": "medium", "sandbox": "workspace-write"},
		"engineGeneration": map[string]any{"sourceCommit": oldGeneration.SourceCommit, "payloadSHA256": oldGeneration.PayloadSHA256},
		"caps": map[string]any{"maxSteps": 10, "maxTechnicalAttemptsPerNode": 2, "maxRuns": 2, "maxRecoveries": 1,
			"maxHandoffBytes": 64 * 1024, "maxWriteBytes": 1 << 20, "maxRunBytes": 16 << 20, "maxTotalBytes": 128 << 20},
		"expiresAt": now.Add(24 * time.Hour).Format(time.RFC3339),
	}
	requestPath := filepath.Join(root, "execution-request.json")
	writeRequest := func() {
		t.Helper()
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(requestPath, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeRequest()
	preview, err := app.PlanExecutionFile(t.Context(), root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("initial preview: %#v, %v", preview.Diagnostics, err)
	}
	if _, err := app.AuthorizePinnedExecutionFile(t.Context(), root, "execution-request.json", preview.ApprovalToken, "operator", resolver); err != nil {
		t.Fatal(err)
	}
	if err := storage.Update(root, func(state *work.State) error {
		goal, _ := state.GoalByID("goal")
		profile := goal.Execution.Authorizations[0].WorkerProfile
		_, err := state.BindCurrentExecutionIdentity("goal", work.ResolvedWorkerIdentity{
			ExecutablePath: profile.ExecutablePath, ExecutableSHA256: profile.ExecutableSHA256,
			ReportedVersion: "codex 1", ObservedAt: now,
		}, oldGeneration)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	oldDigest := state.Goals[0].Execution.Authorizations[0].Digest
	historicalID, err := NewRunID(now)
	if err != nil {
		t.Fatal(err)
	}
	anchorID, err := NewRunID(now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	receipts := map[string]work.ExecutionReservationReceipt{}
	if err := storage.Update(root, func(state *work.State) error {
		for _, runID := range []string{historicalID, anchorID} {
			reservation, err := state.PrepareExecutionReservation("goal", work.ExecutionReservation{
				ID: runID + ":run", Kind: work.ExecutionReservationRun, RunID: runID, CreatedAt: now,
			})
			if err != nil {
				return err
			}
			receipt, err := work.ExecutionReservationReceiptFor(reservation)
			if err != nil {
				return err
			}
			receipts[runID] = receipt
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, runID := range []string{historicalID, anchorID} {
		record := Record{RunID: runID, Workspace: root, GoalID: "goal", ExecutionAuthorizationDigest: oldDigest,
			EngineGeneration: &oldGeneration, RetentionAcquired: true, RunReservationID: runID + ":run",
			ReservationReceipts: []work.ExecutionReservationReceipt{receipts[runID]}, Attempts: map[string]int{}}
		if runID == historicalID {
			record.Stop = &Stop{Reason: StopMaxSteps, At: now}
		}
		if err := record.save(root, storage.ArtifactLimits{}, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := control.Update(root, func(state *control.State) error {
		return state.SetPause(control.Pause{GoalID: "goal", RunID: anchorID, Reason: "change engine", RequestedBy: "operator", RequestedAt: now})
	}); err != nil {
		t.Fatal(err)
	}
	request["expectedAuthorizationDigest"] = oldDigest
	request["engineGeneration"] = map[string]any{"sourceCommit": newGeneration.SourceCommit, "payloadSHA256": newGeneration.PayloadSHA256}
	writeRequest()
	preview, err = app.PlanExecutionRevisionFile(t.Context(), root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("revision preview: %#v, %v", preview.Diagnostics, err)
	}
	resolver.resolved.Generation = newGeneration
	historical, err := LoadRecord(root, historicalID)
	if err != nil {
		t.Fatal(err)
	}
	historical.RunPreparationState = RunPreparationPendingCharge
	if err := historical.save(root, storage.ArtifactLimits{}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ReviseExecutionEngineFile(t.Context(), root, "execution-request.json", preview.ApprovalToken,
		"operator", resolver, NewExecutionCleanupAuditor()); err == nil {
		t.Fatal("engine revision accepted an unsettled historical Run")
	}
	state, err = storage.Load(root)
	if err != nil || len(state.Goals[0].Execution.Authorizations) != 1 {
		t.Fatalf("rejected revision published authorization: %#v, %v", state.Goals, err)
	}
	historical.RunPreparationState = ""
	if err := historical.save(root, storage.ArtifactLimits{}, now); err != nil {
		t.Fatal(err)
	}
	got, err := app.ReviseExecutionEngineFile(t.Context(), root, "execution-request.json", preview.ApprovalToken,
		"operator", resolver, NewExecutionCleanupAuditor())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Authorizations) != 2 || got.Authorizations[0].Digest != oldDigest ||
		*got.Authorizations[0].EngineGeneration != oldGeneration || *got.Authorizations[1].EngineGeneration != newGeneration {
		t.Fatalf("engine revision changed old authorization or missed successor: %#v", got.Authorizations)
	}
	for _, runID := range []string{historicalID, anchorID} {
		record, err := LoadRecord(root, runID)
		if err != nil || record.ExecutionAuthorizationDigest != oldDigest || *record.EngineGeneration != oldGeneration {
			t.Fatalf("historical Run %s changed binding: %#v, %v", runID, record, err)
		}
	}
	controlState, err := control.Read(root)
	if err != nil || controlState.Pause == nil || controlState.Pause.EngineRevision == nil ||
		controlState.Pause.EngineRevision.AuthorizationDigest != oldDigest ||
		controlState.Pause.EngineRevision.EngineGeneration != oldGeneration {
		t.Fatalf("engine revision lost persisted pause intent: %#v, %v", controlState.Pause, err)
	}
}
