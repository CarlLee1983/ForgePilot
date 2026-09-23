package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/control"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

type revisionCleanupAuditor struct {
	calls int
	err   error
}

func (auditor *revisionCleanupAuditor) ConfirmExecutionCleanup(request ExecutionCleanupRequest) (ExecutionCleanupProof, error) {
	auditor.calls++
	return ExecutionCleanupProof{GoalID: request.GoalID, AnchorRunID: request.AnchorRunID,
		AuthorizationDigest: request.AuthorizationDigest}, auditor.err
}

func engineRevisionFixture(t *testing.T) (executionTestFixture, string, work.ExecutionEngineGeneration, *staticBootstrapGenerationResolver, *revisionCleanupAuditor) {
	t.Helper()
	fixture := newExecutionTestFixture(t)
	initial, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(initial.Diagnostics) != 0 {
		t.Fatalf("initial preview = %#v, %v", initial, err)
	}
	seedResolver, _ := pinnedExecutionResolver(t, fixture)
	if _, err := AuthorizePinnedExecutionFile(t.Context(), fixture.root, "execution-request.json", initial.ApprovalToken, "operator", seedResolver); err != nil {
		t.Fatal(err)
	}
	old := fixture.request.EngineGeneration.generation()
	if err := storage.Update(fixture.root, func(state *work.State) error {
		profile := state.Goals[0].Execution.Authorizations[0].WorkerProfile
		_, err := state.BindCurrentExecutionIdentity("goal", work.ResolvedWorkerIdentity{
			ExecutablePath: profile.ExecutablePath, ExecutableSHA256: profile.ExecutableSHA256,
			ReportedVersion: "codex 1", ObservedAt: time.Now().UTC(),
		}, old)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	digest := state.Goals[0].Execution.Authorizations[0].Digest
	newGeneration := work.ExecutionEngineGeneration{SourceCommit: strings.Repeat("c", 40), PayloadSHA256: "sha256:" + strings.Repeat("d", 64)}
	fixture.request.ExpectedAuthorizationDigest = digest
	fixture.request.EngineGeneration = executionEngineGenerationInput{SourceCommit: newGeneration.SourceCommit, PayloadSHA256: newGeneration.PayloadSHA256}
	writeExecutionTestRequest(t, fixture.requestPath, fixture.request)
	helper := filepath.Join(t.TempDir(), "forgepilot-bootstrap")
	script := `#!/bin/sh
[ "$1" = retention-v1 ] && [ "$2" = acquire ] && [ "$3" = --generation ] && [ "$5" = --payload-digest ] && [ "$7" = --reference ] || exit 9
printf '{"protocol_version":1,"result":"acquired","generation_id":"%s","payload_digest":"%s"}\n' "$4" "$6"
`
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	resolver := &staticBootstrapGenerationResolver{resolved: ResolvedBootstrapGeneration{Generation: newGeneration, HelperPath: helper}}
	return fixture, digest, old, resolver, &revisionCleanupAuditor{}
}

func TestEngineRevisionNeedsMatchingPauseBeforeCleanupOrAcquire(t *testing.T) {
	fixture, _, _, resolver, auditor := engineRevisionFixture(t)
	preview, err := PlanExecutionRevisionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 || preview.RevisionDiff.EngineGeneration == nil {
		t.Fatalf("revision preview = %#v, %v", preview, err)
	}
	_, err = ReviseExecutionEngineFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken,
		"operator", resolver, auditor)
	if err == nil || auditor.calls != 0 || resolver.calls != 0 {
		t.Fatalf("missing pause: err=%v audit=%d resolve=%d", err, auditor.calls, resolver.calls)
	}
}

func TestEngineRevisionAcquiresBeforePublishingAndKeepsOldHistory(t *testing.T) {
	fixture, oldDigest, oldGeneration, resolver, auditor := engineRevisionFixture(t)
	if err := control.Update(fixture.root, func(state *control.State) error {
		return state.SetPause(control.Pause{GoalID: "goal", RunID: "run-anchor", Reason: "change engine", RequestedBy: "operator",
			RequestedAt: time.Now().UTC()})
	}); err != nil {
		t.Fatal(err)
	}
	preview, err := PlanExecutionRevisionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("revision preview = %#v, %v", preview, err)
	}
	got, err := ReviseExecutionEngineFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken,
		"operator", resolver, auditor)
	if err != nil {
		t.Fatal(err)
	}
	if auditor.calls != 1 || resolver.calls != 2 || len(got.Authorizations) != 2 ||
		got.Authorizations[0].Digest != oldDigest || *got.Authorizations[0].EngineGeneration != oldGeneration ||
		got.Authorizations[1].EngineGeneration == nil || *got.Authorizations[1].EngineGeneration != resolver.resolved.Generation {
		t.Fatalf("revision result: audits=%d resolutions=%d auth=%#v", auditor.calls, resolver.calls, got.Authorizations)
	}
	controlState, err := control.Read(fixture.root)
	if err != nil || controlState.Pause == nil || controlState.Pause.EngineRevision == nil ||
		controlState.Pause.EngineRevision.AuthorizationDigest != oldDigest || controlState.Pause.EngineRevision.EngineGeneration != oldGeneration {
		t.Fatalf("revision pause = %#v, %v", controlState.Pause, err)
	}
}

func TestEngineRevisionStateSaveFailureLeavesPauseAndSafeMarker(t *testing.T) {
	fixture, _, _, resolver, auditor := engineRevisionFixture(t)
	if err := control.Update(fixture.root, func(state *control.State) error {
		return state.SetPause(control.Pause{GoalID: "goal", RunID: "run-anchor", Reason: "change engine", RequestedBy: "operator",
			RequestedAt: time.Now().UTC()})
	}); err != nil {
		t.Fatal(err)
	}
	preview, err := PlanExecutionRevisionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("preview = %#v, %v", preview, err)
	}
	release := storage.InjectStateSaveFailure(fixture.root, func() error { return errors.New("injected save failure") })
	defer release()
	_, err = ReviseExecutionEngineFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken,
		"operator", resolver, auditor)
	if err == nil || !strings.Contains(err.Error(), "injected save failure") {
		t.Fatalf("state save failure = %v", err)
	}
	state, err := storage.Load(fixture.root)
	if err != nil || len(state.Goals[0].Execution.Authorizations) != 1 {
		t.Fatalf("failed revision changed state: %v %#v", err, state.Goals)
	}
	controlState, err := control.Read(fixture.root)
	if err != nil || controlState.Pause == nil || controlState.Pause.EngineRevision == nil {
		t.Fatalf("failed revision lost durable pause: %v %#v", err, controlState.Pause)
	}
}

func TestEngineRevisionRejectsLegacySidecarBeforeAuditOrResolve(t *testing.T) {
	fixture, _, _, resolver, auditor := engineRevisionFixture(t)
	if err := control.Update(fixture.root, func(state *control.State) error {
		return state.SetPause(control.Pause{GoalID: "goal", RunID: "run-anchor", Reason: "change engine", RequestedBy: "operator",
			RequestedAt: time.Now().UTC()})
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fixture.root, ".forgepilot", "execution-control.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.Replace(string(body), `"schema_version": 2`, `"schema_version": 1`, 1))
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	preview, err := PlanExecutionRevisionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("preview = %#v, %v", preview, err)
	}
	if _, err := ReviseExecutionEngineFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken,
		"operator", resolver, auditor); err == nil || auditor.calls != 0 || resolver.calls != 0 {
		t.Fatalf("legacy sidecar = %v, audit=%d, resolve=%d; want early refusal", err, auditor.calls, resolver.calls)
	}
}
