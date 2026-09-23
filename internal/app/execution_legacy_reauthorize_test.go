package app

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

type legacyRunAuditor struct {
	calls int
	err   error
}

func (auditor *legacyRunAuditor) ConfirmNoExecutionRuns(string) error {
	auditor.calls++
	return auditor.err
}

func legacyReauthorizationFixture(t *testing.T) (executionTestFixture, string) {
	t.Helper()
	fixture := newExecutionTestFixture(t)
	preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("initial preview = %#v, %v", preview, err)
	}
	legacy, err := AuthorizeExecutionFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator")
	if err != nil || legacy.Authorizations[0].RetentionAcquired {
		t.Fatalf("legacy authorization = %#v, %v", legacy, err)
	}
	fixture.request.ExpectedAuthorizationDigest = legacy.Authorizations[0].Digest
	fixture.request.Caps.MaxSteps++
	writeExecutionTestRequest(t, fixture.requestPath, fixture.request)
	revision, err := PlanExecutionRevisionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(revision.Diagnostics) != 0 || !revision.LegacyRetentionUnknown {
		t.Fatalf("legacy revision preview = %#v, %v", revision, err)
	}
	return fixture, revision.ApprovalToken
}

func TestExplicitLegacyReauthorizationFirstPinsUnknownGoal(t *testing.T) {
	fixture, token := legacyReauthorizationFixture(t)
	resolver, logPath := pinnedExecutionResolver(t, fixture)
	auditor := &legacyRunAuditor{}
	got, err := ReauthorizeLegacyExecutionFile(t.Context(), fixture.root, "execution-request.json", token, "operator", resolver, auditor)
	if err != nil || auditor.calls != 2 || len(got.Authorizations) != 2 ||
		got.Authorizations[0].RetentionAcquired || !got.Authorizations[1].RetentionAcquired ||
		got.Authorizations[1].EngineGeneration == nil || *got.Authorizations[1].EngineGeneration != resolver.resolved.Generation {
		t.Fatalf("legacy successor = %#v, audit=%d, %v", got, auditor.calls, err)
	}
	references, err := os.ReadFile(logPath)
	if err != nil || len(strings.Fields(string(references))) != 1 {
		t.Fatalf("only successor marker acquired = %q, %v", references, err)
	}
}

func TestLegacyReauthorizationRefusesOldRunOrStateFailure(t *testing.T) {
	fixture, token := legacyReauthorizationFixture(t)
	resolver, logPath := pinnedExecutionResolver(t, fixture)
	auditor := &legacyRunAuditor{err: errors.New("old Run Record exists")}
	if _, err := ReauthorizeLegacyExecutionFile(t.Context(), fixture.root, "execution-request.json", token, "operator", resolver, auditor); err == nil {
		t.Fatal("old Run Record was ignored")
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("marker acquired despite old Run: %v", err)
	}
	auditor.err = nil
	release := storage.InjectStateSaveFailure(fixture.root, func() error { return os.ErrPermission })
	defer release()
	if _, err := ReauthorizeLegacyExecutionFile(t.Context(), fixture.root, "execution-request.json", token, "operator", resolver, auditor); err == nil {
		t.Fatal("legacy successor committed despite state write failure")
	}
	state, err := storage.Load(fixture.root)
	if err != nil || len(state.Goals[0].Execution.Authorizations) != 1 {
		t.Fatalf("failed reauthorization changed history: %#v, %v", state.Goals, err)
	}
	references, err := os.ReadFile(logPath)
	if err != nil || len(strings.Fields(string(references))) != 1 {
		t.Fatalf("safe orphan successor marker = %q, %v", references, err)
	}
}

func TestLegacyReauthorizationRefusesChargedLedger(t *testing.T) {
	fixture, token := legacyReauthorizationFixture(t)
	if err := storage.Update(fixture.root, func(state *work.State) error {
		_, err := state.PrepareExecutionReservation("goal", work.ExecutionReservation{
			ID: "run-legacy:run", Kind: work.ExecutionReservationRun, RunID: "run-legacy",
			CreatedAt: state.Goals[0].Execution.Authorizations[0].AuthorizedAt,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	resolver, logPath := pinnedExecutionResolver(t, fixture)
	if _, err := ReauthorizeLegacyExecutionFile(t.Context(), fixture.root, "execution-request.json", token, "operator", resolver, &legacyRunAuditor{}); err == nil {
		t.Fatal("charged legacy ledger was repinned")
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("marker acquired despite charged history: %v", err)
	}
}
