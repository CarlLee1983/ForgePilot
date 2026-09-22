package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type executionTestFixture struct {
	root        string
	requestPath string
	markerPath  string
	request     executionPlanRequest
}

func newExecutionTestFixture(t *testing.T) executionTestFixture {
	t.Helper()
	root, planRequest := preflightFixture(t, [][2]string{{"a", "b"}}, false)
	executablePath := filepath.Join(root, "worker-codex")
	markerPath := executablePath + ".executed"
	if err := os.WriteFile(executablePath, []byte("#!/bin/sh\n: > \"$0.executed\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	request := executionPlanRequest{
		FormatVersion:   executionPlanRequestVersion,
		GoalPlanRequest: planRequest,
		WorkerProfile: executionWorkerProfileInput{
			Runtime: "codex", ExecutablePath: executablePath, Model: "explicit-model", Effort: "medium", Sandbox: "workspace-write",
		},
		Caps: executionCapsInput{
			MaxSteps: 500, MaxTechnicalAttemptsPerNode: 5, MaxRuns: 20, MaxRecoveries: 2,
			MaxHandoffBytes: 64 * 1024, MaxWriteBytes: 1 << 20, MaxRunBytes: 16 << 20, MaxTotalBytes: 128 << 20,
		},
		ExpiresAt: time.Now().UTC().Add(7 * 24 * time.Hour).Truncate(time.Second).Format(time.RFC3339),
	}
	requestPath := filepath.Join(root, "execution-request.json")
	writeExecutionTestRequest(t, requestPath, request)
	return executionTestFixture{root: root, requestPath: requestPath, markerPath: markerPath, request: request}
}

func writeExecutionTestRequest(t *testing.T, path string, request executionPlanRequest) []byte {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	return body
}
