package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestPlanExecutionIsPureAndTokenBindsExactRequestArtifactsAndRegistration(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	before := preflightStateBytes(t, fixture.root)
	projection, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil {
		t.Fatal(err)
	}
	if projection.Version != ExecutionPlanVersion || len(projection.Diagnostics) != 0 || projection.ApprovalToken == "" {
		t.Fatalf("execution plan projection = %#v", projection)
	}
	canonicalRoot, err := filepath.EvalSymlinks(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if projection.GoalID != fixture.request.GoalPlanRequest.GoalID || projection.Workspace != canonicalRoot {
		t.Fatalf("Goal/workspace = %q / %q", projection.GoalID, projection.Workspace)
	}
	if !strings.HasPrefix(projection.ApprovalToken, "sha256:") || !strings.HasPrefix(projection.RequestSHA256, "sha256:") ||
		!strings.HasPrefix(projection.RegistrationSHA256, "sha256:") {
		t.Fatalf("token digests = %#v", projection)
	}
	if projection.WorkerProfile.Model != "explicit-model" || projection.WorkerProfile.Effort != "medium" ||
		projection.WorkerProfile.Sandbox != "workspace-write" || projection.Caps.MaxSteps != 500 || projection.Caps.MaxRuns != 20 {
		t.Fatalf("profile or caps were inferred/changed: %#v / %#v", projection.WorkerProfile, projection.Caps)
	}
	if projection.GoalPlan == nil || len(projection.GoalPlan.NodeMappings) != 2 || len(projection.Artifacts) < 4 {
		t.Fatalf("projection omitted reviewed plan bindings: %#v", projection)
	}
	firstToken := projection.ApprovalToken
	second, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || second.ApprovalToken != firstToken {
		t.Fatalf("unchanged preview token = %q, err=%v; want %q", second.ApprovalToken, err, firstToken)
	}
	if _, err := os.Stat(fixture.markerPath); !os.IsNotExist(err) {
		t.Fatalf("preview launched the selected executable (marker stat error: %v)", err)
	}
	if after := preflightStateBytes(t, fixture.root); !bytes.Equal(before, after) {
		t.Fatal("execution plan preview changed state.json")
	}

	// Whitespace is semantically irrelevant JSON, but the approval token is
	// deliberately tied to the exact request bytes the operator reviewed.
	body, err := os.ReadFile(fixture.requestPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := append([]byte("\n "), body...)
	if err := os.WriteFile(fixture.requestPath, updated, 0600); err != nil {
		t.Fatal(err)
	}
	third, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || third.ApprovalToken == firstToken {
		t.Fatalf("exact-byte change did not invalidate approval token: token=%q err=%v", third.ApprovalToken, err)
	}
}

func TestPlanExecutionRejectsInvalidLimitsProfileExpiryAndMapping(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*executionPlanRequest)
		want   string
	}{
		{name: "zero run cap", mutate: func(request *executionPlanRequest) { request.Caps.MaxRuns = 0 }, want: "invalid-limits"},
		{name: "inverted artifact caps", mutate: func(request *executionPlanRequest) { request.Caps.MaxRunBytes = 1 }, want: "invalid-limits"},
		{name: "incomplete profile", mutate: func(request *executionPlanRequest) { request.WorkerProfile.Model = "" }, want: "invalid-profile"},
		{name: "unsupported sandbox", mutate: func(request *executionPlanRequest) { request.WorkerProfile.Sandbox = "danger-full-access" }, want: "invalid-profile"},
		{name: "expiry beyond maximum", mutate: func(request *executionPlanRequest) {
			request.ExpiresAt = time.Now().UTC().Add(15 * 24 * time.Hour).Truncate(time.Second).Format(time.RFC3339)
		}, want: "invalid-expiry"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExecutionTestFixture(t)
			test.mutate(&fixture.request)
			writeExecutionTestRequest(t, fixture.requestPath, fixture.request)
			projection, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
			if err != nil {
				t.Fatal(err)
			}
			if len(projection.Diagnostics) != 1 || projection.Diagnostics[0].Code != test.want || projection.ApprovalToken != "" {
				t.Fatalf("invalid request projection = %#v", projection)
			}
		})
	}

	t.Run("incomplete mapping", func(t *testing.T) {
		fixture := newExecutionTestFixture(t)
		fixture.request.GoalPlanRequest.NodeMappings = fixture.request.GoalPlanRequest.NodeMappings[:1]
		writeExecutionTestRequest(t, fixture.requestPath, fixture.request)
		projection, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
		if err != nil {
			t.Fatal(err)
		}
		if len(projection.Diagnostics) != 1 || projection.Diagnostics[0].Code != "mapping-mismatch" || projection.ApprovalToken != "" {
			t.Fatalf("incomplete mapping projection = %#v", projection)
		}
	})
}

func TestPlanExecutionTokenBindsExactCoverageReviewAndExecutableBytes(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	first, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(first.Diagnostics) != 0 {
		t.Fatalf("first preview = %#v, err=%v", first, err)
	}
	reviewPath := filepath.Join(fixture.root, fixture.request.GoalPlanRequest.CoverageReviewPath)
	review, err := os.ReadFile(reviewPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reviewPath, append([]byte("\n"), review...), 0644); err != nil {
		t.Fatal(err)
	}
	second, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(second.Diagnostics) != 0 || second.ApprovalToken == first.ApprovalToken {
		t.Fatalf("coverage-review byte change did not change the valid approval token: %#v, err=%v", second, err)
	}
	if err := os.WriteFile(fixture.request.WorkerProfile.ExecutablePath, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	third, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(third.Diagnostics) != 0 || third.ApprovalToken == second.ApprovalToken {
		t.Fatalf("executable content change did not change the valid approval token: %#v, err=%v", third, err)
	}
}

func TestPlanExecutionRefusesUncontainedExecutableWithoutLaunchingIt(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	fixture.request.WorkerProfile.ExecutablePath = filepath.Join(fixture.root, "missing-executable")
	writeExecutionTestRequest(t, fixture.requestPath, fixture.request)
	projection, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Diagnostics) != 1 || projection.Diagnostics[0].Code != "invalid-profile" {
		t.Fatalf("missing executable projection = %#v", projection)
	}
	if _, err := os.Stat(fixture.markerPath); !os.IsNotExist(err) {
		t.Fatalf("preview started executable: %v", err)
	}
}

func TestPlanExecutionRejectsControlCharactersInModel(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	fixture.request.WorkerProfile.Model = "model\x01name"
	writeExecutionTestRequest(t, fixture.requestPath, fixture.request)

	projection, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Diagnostics) != 1 || projection.Diagnostics[0].Code != "invalid-profile" || projection.ApprovalToken != "" {
		t.Fatalf("control-character model preview = %#v", projection)
	}
}

func TestPlanExecutionTokenBindsCurrentGoalRegistration(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("preview = %#v, err=%v", preview, err)
	}

	if err := storage.Update(fixture.root, func(state *work.State) error {
		state.Goals[0].ReviewPolicy = work.ReviewPerGoal
		state.Goals[0].CompletionPolicy = work.CompletionVerified
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	beforeAuthorization := preflightStateBytes(t, fixture.root)
	updated, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(updated.Diagnostics) != 0 || updated.ApprovalToken == preview.ApprovalToken {
		t.Fatalf("changed Goal registration did not produce a fresh token: %#v, err=%v", updated, err)
	}
	if _, err := AuthorizeExecutionFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator"); err == nil {
		t.Fatal("authorization accepted the token for a changed Goal registration")
	}
	if after := preflightStateBytes(t, fixture.root); !bytes.Equal(beforeAuthorization, after) {
		t.Fatal("refusing a stale registration token changed durable state")
	}
}
