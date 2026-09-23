package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
)

func pinnedExecutionResolver(t *testing.T, fixture executionTestFixture) (*staticBootstrapGenerationResolver, string) {
	t.Helper()
	directory := t.TempDir()
	logPath := filepath.Join(directory, "references")
	helper := filepath.Join(directory, "forgepilot-bootstrap")
	script := `#!/bin/sh
[ "$1" = retention-v1 ] && [ "$2" = acquire ] && [ "$3" = --generation ] && [ "$5" = --payload-digest ] && [ "$7" = --reference ] || exit 9
printf '%s\n' "$8" >> '` + logPath + `'
printf '{"protocol_version":1,"result":"acquired","generation_id":"%s","payload_digest":"%s"}\n' "$4" "$6"
`
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return &staticBootstrapGenerationResolver{resolved: ResolvedBootstrapGeneration{
		Generation: fixture.request.EngineGeneration.generation(), HelperPath: helper}}, logPath
}

func TestPinnedInitialAndOrdinaryRevisionAcquireDistinctOwners(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	resolver, logPath := pinnedExecutionResolver(t, fixture)
	initial, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(initial.Diagnostics) != 0 || initial.EngineGeneration != resolver.resolved.Generation {
		t.Fatalf("initial projection = %#v, %v", initial, err)
	}
	first, err := AuthorizePinnedExecutionFile(t.Context(), fixture.root, "execution-request.json", initial.ApprovalToken, "operator", resolver)
	if err != nil || first.Authorizations[0].EngineGeneration == nil ||
		*first.Authorizations[0].EngineGeneration != resolver.resolved.Generation {
		t.Fatalf("initial pinned authorization = %#v, %v", first, err)
	}
	fixture.request.ExpectedAuthorizationDigest = first.Authorizations[0].Digest
	fixture.request.Caps.MaxSteps++
	writeExecutionTestRequest(t, fixture.requestPath, fixture.request)
	revision, err := PlanExecutionRevisionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(revision.Diagnostics) != 0 || revision.RevisionDiff.EngineGeneration != nil {
		t.Fatalf("ordinary revision projection = %#v, %v", revision, err)
	}
	second, err := RevisePinnedExecutionFile(t.Context(), fixture.root, "execution-request.json", revision.ApprovalToken, "operator", resolver)
	if err != nil || len(second.Authorizations) != 2 || second.Authorizations[1].EngineGeneration == nil ||
		*second.Authorizations[1].EngineGeneration != resolver.resolved.Generation {
		t.Fatalf("ordinary pinned revision = %#v, %v", second, err)
	}
	refs, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(refs))
	if len(lines) != 2 || lines[0] == lines[1] {
		t.Fatalf("authorization owner references = %q; want two distinct references", refs)
	}
}

func TestPinnedInitialStateSaveFailureDoesNotPublishGeneration(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	resolver, logPath := pinnedExecutionResolver(t, fixture)
	preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("preview = %#v, %v", preview, err)
	}
	release := storage.InjectStateSaveFailure(fixture.root, func() error { return os.ErrPermission })
	defer release()
	if _, err := AuthorizePinnedExecutionFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator", resolver); err == nil {
		t.Fatal("authorization committed despite state save failure")
	}
	state, err := storage.Load(fixture.root)
	if err != nil || state.Goals[0].Execution != nil {
		t.Fatalf("failed state write published engine generation: %v %#v", err, state.Goals)
	}
	refs, err := os.ReadFile(logPath)
	if err != nil || len(strings.Fields(string(refs))) != 1 {
		t.Fatalf("safe acquired marker = %q, %v", refs, err)
	}
}
