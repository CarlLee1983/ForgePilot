package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

type executionTestGenerationResolver struct {
	generation work.ExecutionEngineGeneration
	helper     string
}

func (resolver executionTestGenerationResolver) Resolve(context.Context) (app.ResolvedBootstrapGeneration, error) {
	return app.ResolvedBootstrapGeneration{Generation: resolver.generation, HelperPath: resolver.helper}, nil
}

func TestExecutionPlanEmitsVersionedReadOnlyPreview(t *testing.T) {
	root := preflightCLIFixture(t)
	executable := filepath.Join(root, "worker-codex")
	marker := executable + ".executed"
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n: > \"$0.executed\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	request := map[string]any{
		"formatVersion": executionPlanRequestFormatVersion,
		"goalPlanRequest": map[string]any{
			"formatVersion": "forgepilot.goal-preflight-request/v1", "goalId": "goal",
			"manifestPath": "manifest.json", "coverageReviewPath": "review.json",
			"nodeMappings": []any{
				map[string]any{"planNodeRef": "node-001", "workItemId": "WI-001"},
				map[string]any{"planNodeRef": "node-002", "workItemId": "WI-002"},
			},
		},
		"workerProfile": map[string]any{
			"runtime": "codex", "executablePath": executable, "model": "explicit-model", "effort": "medium", "sandbox": "workspace-write",
		},
		"engineGeneration": map[string]any{"sourceCommit": strings.Repeat("a", 40), "payloadSHA256": "sha256:" + strings.Repeat("b", 64)},
		"caps": map[string]any{
			"maxSteps": 500, "maxTechnicalAttemptsPerNode": 5, "maxRuns": 20, "maxRecoveries": 2,
			"maxHandoffBytes": 65536, "maxWriteBytes": 1048576, "maxRunBytes": 16777216, "maxTotalBytes": 134217728,
		},
		"expiresAt": time.Now().UTC().Add(7 * 24 * time.Hour).Truncate(time.Second).Format(time.RFC3339),
	}
	requestBytes, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "execution-request.json"), requestBytes, 0600); err != nil {
		t.Fatal(err)
	}

	spyDir := t.TempDir()
	logPath := filepath.Join(spyDir, "process.log")
	spy := []byte("#!/bin/sh\nprintf '%s\\n' \"$0\" >> \"$PREFLIGHT_PROCESS_LOG\"\nexit 99\n")
	for _, name := range []string{"git", "make", "ps", "go", "node", "python3", "codex", "mise", "asdf", "sh", "command"} {
		if err := os.WriteFile(filepath.Join(spyDir, name), spy, 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", spyDir)
	t.Setenv("TMPDIR", spyDir)
	t.Setenv("PREFLIGHT_PROCESS_LOG", logPath)
	beforeRoot, beforeSpy := preflightCLITree(t, root), preflightCLITree(t, spyDir)
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"execution", "plan", "--request", "execution-request.json", "--json"}, root, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("exit = %d, stderr=%q, stdout=%q", code, stderr.String(), stdout.String())
	}
	var projection app.ExecutionPlanProjection
	if err := json.Unmarshal(stdout.Bytes(), &projection); err != nil {
		t.Fatalf("stdout is not one JSON projection: %v; output = %q", err, stdout.String())
	}
	if projection.Version != app.ExecutionPlanVersion || len(projection.Diagnostics) != 0 || projection.ApprovalToken == "" ||
		projection.GoalID != "goal" || projection.WorkerProfile.Model != "explicit-model" {
		t.Fatalf("projection = %#v", projection)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("selected worker executable was launched: %v", err)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("process spy was launched: %v", err)
	}
	if after := preflightCLITree(t, root); !reflect.DeepEqual(beforeRoot, after) {
		t.Fatalf("repository files changed during preview: before=%v after=%v", beforeRoot, after)
	}
	if after := preflightCLITree(t, spyDir); !reflect.DeepEqual(beforeSpy, after) {
		t.Fatalf("temporary files changed during preview: before=%v after=%v", beforeSpy, after)
	}

	statePath := filepath.Join(root, ".forgepilot", "state.json")
	beforeState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"execution", "authorize", "--request", "execution-request.json", "--approval-token", projection.ApprovalToken, "--json"}, root, &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || stderr.Len() == 0 {
		t.Fatalf("authorize accepted missing approver: exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if after, err := os.ReadFile(statePath); err != nil || !bytes.Equal(beforeState, after) {
		t.Fatalf("missing approver changed state: err=%v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"execution", "authorize", "--request", "execution-request.json", "--approval-token", projection.ApprovalToken + "-stale", "--by", "operator", "--json"}, root, &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || stderr.Len() == 0 {
		t.Fatalf("authorize accepted stale token: exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if after, err := os.ReadFile(statePath); err != nil || !bytes.Equal(beforeState, after) {
		t.Fatalf("stale token changed state: err=%v", err)
	}

	stdout.Reset()
	stderr.Reset()
	helper := filepath.Join(t.TempDir(), "forgepilot-bootstrap")
	if err := os.WriteFile(helper, []byte(`#!/bin/sh
[ "$1" = retention-v1 ] && [ "$2" = acquire ] || exit 9
printf '{"protocol_version":1,"result":"acquired","generation_id":"%s","payload_digest":"%s"}\n' "$4" "$6"
`), 0700); err != nil {
		t.Fatal(err)
	}
	resolver := executionTestGenerationResolver{generation: work.ExecutionEngineGeneration{SourceCommit: strings.Repeat("a", 40), PayloadSHA256: "sha256:" + strings.Repeat("b", 64)}, helper: helper}
	code = ExecuteWithGenerationResolver([]string{"execution", "authorize", "--request", "execution-request.json", "--approval-token", projection.ApprovalToken, "--by", "operator", "--json"}, root, &stdout, &stderr, resolver)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("authorize exit=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	var authorization executionAuthorizationOutput
	if err := json.Unmarshal(stdout.Bytes(), &authorization); err != nil {
		t.Fatalf("authorize stdout is not one JSON record: %v; output=%q", err, stdout.String())
	}
	if authorization.Version != "forgepilot.execution-authorization/v2" || len(authorization.GoalExecution.Authorizations) != 1 ||
		authorization.GoalExecution.Authorizations[0].Approver != "operator" {
		t.Fatalf("authorization output = %#v", authorization)
	}
	stdout.Reset()
	stderr.Reset()
	code = ExecuteWithGenerationResolver([]string{"execution", "retention", "reconcile", "--json"}, root, &stdout, &stderr, resolver)
	if code != 0 || stderr.Len() != 0 || !bytes.Contains(stdout.Bytes(), []byte(`"authorizations":0`)) ||
		!bytes.Contains(stdout.Bytes(), []byte(`"runs":0`)) {
		t.Fatalf("active-owner reconciliation = exit %d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}

	request["expectedAuthorizationDigest"] = authorization.GoalExecution.Authorizations[0].Digest
	requestBytes, err = json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "execution-request.json"), requestBytes, 0600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"execution", "revise", "plan", "--request", "execution-request.json", "--json"}, root, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("revision plan exit=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	var revision app.ExecutionPlanProjection
	if err := json.Unmarshal(stdout.Bytes(), &revision); err != nil {
		t.Fatalf("revision plan stdout is not one JSON projection: %v; output=%q", err, stdout.String())
	}
	if revision.RevisionDiff == nil {
		t.Fatalf("revision plan JSON omitted revisionDiff: %#v", revision)
	}
	request["caps"].(map[string]any)["maxSteps"] = 501
	requestBytes, err = json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "execution-request.json"), requestBytes, 0600); err != nil {
		t.Fatal(err)
	}
	revision, err = app.PlanExecutionRevisionFile(t.Context(), root, "execution-request.json")
	if err != nil || len(revision.Diagnostics) != 0 {
		t.Fatalf("changed revision preview = %#v, %v", revision, err)
	}
	stdout.Reset()
	stderr.Reset()
	code = ExecuteWithGenerationResolver([]string{"execution", "revise", "authorize", "--request", "execution-request.json",
		"--approval-token", revision.ApprovalToken, "--by", "operator", "--json"}, root, &stdout, &stderr, resolver)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("revision commit with release warning = exit %d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	var committed executionAuthorizationOutput
	if err := json.Unmarshal(stdout.Bytes(), &committed); err != nil || len(committed.GoalExecution.Authorizations) != 2 ||
		committed.RetentionWarning == "" {
		t.Fatalf("committed revision did not report retryable cleanup: %#v, %v", committed, err)
	}
}

func TestExecutionResumeUsesGoalScopedContract(t *testing.T) {
	root := preflightCLIFixture(t)
	var stdout, stderr bytes.Buffer
	if code := Execute([]string{"execution", "resume", "run-legacy"}, root, &stdout, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "execution resume --goal") {
		t.Fatalf("legacy execution resume syntax = exit %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Execute([]string{"execution", "resume", "--goal", "goal"}, root, &stdout, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "no current execution authorization") {
		t.Fatalf("goal-scoped execution resume = exit %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

const executionPlanRequestFormatVersion = "forgepilot.execution-plan-request/v2"
