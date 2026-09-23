package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

type staticClosedRunOwners struct {
	err error
}

func (closer staticClosedRunOwners) CloseRetiredExecutionRuns(string) ([]ClosedExecutionRunOwner, error) {
	return nil, closer.err
}

func TestClosedAuthorizationReleaseRetriesWithoutChangingHistory(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	dir := t.TempDir()
	logPath, failOnce := filepath.Join(dir, "released"), filepath.Join(dir, "fail-once")
	helper := filepath.Join(dir, "bootstrap")
	script := `#!/bin/sh
[ "$1" = retention-v1 ] && [ "$3" = --generation ] && [ "$5" = --payload-digest ] && [ "$7" = --reference ] || exit 9
if [ "$2" = acquire ]; then
  printf '{"protocol_version":1,"result":"acquired","generation_id":"%s","payload_digest":"%s"}\n' "$4" "$6"
elif [ "$2" = release ]; then
  if [ -e '` + failOnce + `' ]; then rm '` + failOnce + `'; exit 9; fi
  printf '%s\n' "$8" >> '` + logPath + `'
  printf '{"protocol_version":1,"result":"released","generation_id":"%s","payload_digest":"%s"}\n' "$4" "$6"
else exit 9; fi
`
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	resolver := &staticBootstrapGenerationResolver{resolved: ResolvedBootstrapGeneration{
		Generation: fixture.request.EngineGeneration.generation(), HelperPath: helper}}
	initialPreview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(initialPreview.Diagnostics) != 0 {
		t.Fatalf("initial preview = %#v, %v", initialPreview, err)
	}
	initial, err := AuthorizePinnedExecutionFile(t.Context(), fixture.root, "execution-request.json", initialPreview.ApprovalToken, "operator", resolver)
	if err != nil {
		t.Fatal(err)
	}
	fixture.request.ExpectedAuthorizationDigest = initial.Authorizations[0].Digest
	fixture.request.Caps.MaxSteps++
	writeExecutionTestRequest(t, fixture.requestPath, fixture.request)
	revisionPreview, err := PlanExecutionRevisionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(revisionPreview.Diagnostics) != 0 {
		t.Fatalf("revision preview = %#v, %v", revisionPreview, err)
	}
	revised, err := RevisePinnedExecutionFile(t.Context(), fixture.root, "execution-request.json", revisionPreview.ApprovalToken, "operator", resolver)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileExecutionRetention(t.Context(), fixture.root, resolver, staticClosedRunOwners{err: errors.New("uncertain Run")}); err == nil {
		t.Fatal("uncertain Run audit allowed authorization release")
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("authorization released before complete Run audit: %v", err)
	}
	if err := os.WriteFile(failOnce, []byte("fail"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileExecutionRetention(t.Context(), fixture.root, resolver, staticClosedRunOwners{}); err == nil {
		t.Fatal("Bootstrap release failure was hidden")
	}
	state, err := storage.Load(fixture.root)
	if err != nil || state.Goals[0].Execution.Authorizations[0].Digest != initial.Authorizations[0].Digest ||
		state.Goals[0].Execution.Authorizations[1].Digest != revised.Authorizations[1].Digest {
		t.Fatalf("failed release changed authorization history: %#v, %v", state.Goals, err)
	}
	result, err := ReconcileExecutionRetention(t.Context(), fixture.root, resolver, staticClosedRunOwners{})
	if err != nil || result.Authorizations != 1 || result.Runs != 0 {
		t.Fatalf("retried historical authorization release = %#v, %v", result, err)
	}
	if err := storage.Update(fixture.root, func(state *work.State) error {
		return state.CancelGoal("goal", "completed elsewhere", time.Now().UTC())
	}); err != nil {
		t.Fatal(err)
	}
	result, err = ReconcileExecutionRetention(t.Context(), fixture.root, resolver, staticClosedRunOwners{})
	if err != nil || result.Authorizations != 2 {
		t.Fatalf("terminal Goal release = %#v, %v", result, err)
	}
	contents, err := os.ReadFile(logPath)
	if err != nil || len(strings.Fields(string(contents))) != 3 {
		t.Fatalf("release retry log = %q, %v", contents, err)
	}
}
