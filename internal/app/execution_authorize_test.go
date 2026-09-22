package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestAuthorizeExecutionPublishesOneRevisionOneAggregate(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("preview = %#v, err=%v", preview, err)
	}
	beforeState, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	authorized, err := AuthorizeExecutionFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator")
	if err != nil {
		t.Fatal(err)
	}
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	goal, ok := state.GoalByID(beforeState.Goals[0].ID)
	if !ok || goal.Execution == nil {
		t.Fatalf("adopted Goal = %#v", goal)
	}
	if len(authorized.PlanBindings) != 1 || len(authorized.Authorizations) != 1 ||
		authorized.Authorizations[0].Revision != 1 || len(authorized.PlanBindings[0].Nodes) != len(beforeState.WorkItems) {
		t.Fatalf("authorized aggregate = %#v", authorized)
	}
	if authorized.Ledger.RunsConsumed != 0 || authorized.Ledger.StepsConsumed != 0 || authorized.Ledger.RecoveriesConsumed != 0 {
		t.Fatalf("initial ledger has nonzero totals: %#v", authorized.Ledger)
	}
	for _, node := range authorized.Ledger.NodeAttempts {
		if node.TechnicalAttempts != 0 {
			t.Fatalf("initial node attempts = %#v", authorized.Ledger.NodeAttempts)
		}
	}
	if authorized.Witness.AuthorizationDigest != authorized.Authorizations[0].Digest ||
		authorized.Witness.PlanBindingDigest != authorized.PlanBindings[0].Digest ||
		authorized.Witness.LedgerDigest != authorized.Ledger.Digest {
		t.Fatalf("Goal witness does not cross-link the aggregate: %#v", authorized.Witness)
	}
	if state.Goals[0].Status != beforeState.Goals[0].Status {
		t.Fatal("authorization changed Goal or Work Item lifecycle")
	}
	if state.WorkItems[0].Status != beforeState.WorkItems[0].Status || state.WorkItems[1].Status != beforeState.WorkItems[1].Status {
		t.Fatal("authorization changed Work Item lifecycle")
	}
	if authorized.Authorizations[0].WorkerIdentity != nil || authorized.Authorizations[0].EngineGeneration != nil {
		t.Fatal("pure FP-53 authorization claimed an unobserved Worker or engine version")
	}
}

func TestAuthorizeExecutionFailureAndDriftLeaveStateBytesUnchanged(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(t *testing.T, fixture executionTestFixture)
		approval  func(string) string
		wantError string
	}{
		{name: "stale token", approval: func(string) string { return "sha256:stale" }, wantError: "approval token is stale"},
		{name: "request byte drift", wantError: "approval token is stale", mutate: func(t *testing.T, fixture executionTestFixture) {
			body, err := os.ReadFile(fixture.requestPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fixture.requestPath, append([]byte("\n"), body...), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "artifact drift", wantError: "digest-mismatch", mutate: func(t *testing.T, fixture executionTestFixture) {
			if err := os.WriteFile(filepath.Join(fixture.root, "source.md"), []byte("drift"), 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "invalid limits", wantError: "invalid-limits", mutate: func(t *testing.T, fixture executionTestFixture) {
			fixture.request.Caps.MaxRuns = 0
			writeExecutionTestRequest(t, fixture.requestPath, fixture.request)
		}},
		{name: "invalid profile", wantError: "invalid-profile", mutate: func(t *testing.T, fixture executionTestFixture) {
			fixture.request.WorkerProfile.Model = ""
			writeExecutionTestRequest(t, fixture.requestPath, fixture.request)
		}},
		{name: "incomplete mapping", wantError: "mapping-mismatch", mutate: func(t *testing.T, fixture executionTestFixture) {
			fixture.request.GoalPlanRequest.NodeMappings = fixture.request.GoalPlanRequest.NodeMappings[:1]
			writeExecutionTestRequest(t, fixture.requestPath, fixture.request)
		}},
		{name: "topology mismatch", wantError: "mapping-mismatch", mutate: func(t *testing.T, fixture executionTestFixture) {
			workItemID := fixture.request.GoalPlanRequest.NodeMappings[1].WorkItemID
			if err := storage.Update(fixture.root, func(state *work.State) error {
				for index := range state.WorkItems {
					if state.WorkItems[index].ID == workItemID {
						state.WorkItems[index].DependsOn = nil
						return nil
					}
				}
				return errors.New("mapped Work Item is missing")
			}); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExecutionTestFixture(t)
			preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
			if err != nil || len(preview.Diagnostics) != 0 {
				t.Fatalf("preview = %#v, err=%v", preview, err)
			}
			if test.mutate != nil {
				test.mutate(t, fixture)
			}
			before := preflightStateBytes(t, fixture.root)
			token := preview.ApprovalToken
			if test.approval != nil {
				token = test.approval(token)
			}
			if _, err := authorizeExecutionAt(t.Context(), fixture.root, "execution-request.json", token, "operator", time.Now); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("authorization error = %v, want %q", err, test.wantError)
			}
			if after := preflightStateBytes(t, fixture.root); !bytes.Equal(before, after) {
				t.Fatalf("%s changed state after refused authorization", test.name)
			}
		})
	}
}

func TestAuthorizeExecutionInjectedSaveFailureLeavesNoPartialAggregate(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("preview = %#v, err=%v", preview, err)
	}
	before := preflightStateBytes(t, fixture.root)
	release := storage.InjectStateSaveFailure(fixture.root, func() error { return errors.New("injected atomic save failure") })
	defer release()
	if _, err := authorizeExecutionAt(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator", time.Now); err == nil {
		t.Fatal("authorization succeeded despite injected state-save failure")
	}
	if after := preflightStateBytes(t, fixture.root); !bytes.Equal(before, after) {
		t.Fatal("failed atomic save left a partial plan binding, authorization, ledger, or witness")
	}
}

func TestConcurrentAuthorizeExecutionHasExactlyOneWinner(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("preview = %#v, err=%v", preview, err)
	}
	var group sync.WaitGroup
	group.Add(2)
	errorsOut := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			defer group.Done()
			_, err := authorizeExecutionAt(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator", time.Now)
			errorsOut <- err
		}()
	}
	group.Wait()
	close(errorsOut)
	successes, failures := 0, 0
	for err := range errorsOut {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("authorization outcomes = %d success, %d failure", successes, failures)
	}
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	goal, ok := state.GoalByID("goal")
	if !ok || goal.Execution == nil || len(goal.Execution.Authorizations) != 1 {
		t.Fatalf("concurrent authorization aggregate = %#v", goal)
	}
}

func TestAuthorizeExecutionRechecksExpiryAtTheTransactionTime(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	previewedAt := time.Date(2026, time.September, 22, 8, 0, 0, 0, time.UTC)
	fixture.request.ExpiresAt = previewedAt.Add(time.Second).Format(time.RFC3339)
	requestBytes := writeExecutionTestRequest(t, fixture.requestPath, fixture.request)
	preview, err := planExecutionBytes(t.Context(), fixture.root, requestBytes, previewedAt)
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("preview = %#v, err=%v", preview, err)
	}
	before := preflightStateBytes(t, fixture.root)
	clockCalls := 0
	if _, err := authorizeExecutionAt(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator", func() time.Time {
		clockCalls++
		if clockCalls == 1 {
			return previewedAt
		}
		return previewedAt.Add(2 * time.Second)
	}); err == nil || !strings.Contains(err.Error(), "invalid-expiry") {
		t.Fatalf("authorization error = %v, want expired authorization refusal", err)
	}
	if clockCalls != 2 {
		t.Fatalf("authorization clock calls = %d, want entry and pre-commit checks", clockCalls)
	}
	if after := preflightStateBytes(t, fixture.root); !bytes.Equal(before, after) {
		t.Fatal("expired authorization changed state")
	}
}

func TestAuthorizeExecutionReadsRequestInsideTheStateTransaction(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("preview = %#v, err=%v", preview, err)
	}
	before := preflightStateBytes(t, fixture.root)
	locked, releaseLock := make(chan struct{}), make(chan struct{})
	lockResult := make(chan error, 1)
	go func() {
		lockResult <- storage.Update(fixture.root, func(*work.State) error {
			close(locked)
			<-releaseLock
			return nil
		})
	}()
	<-locked

	beforeStateLock, authorizationResult := make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := authorizeExecutionBeforeStateLock(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator", time.Now, func() {
			close(beforeStateLock)
		})
		authorizationResult <- err
	}()
	<-beforeStateLock
	request, err := os.ReadFile(fixture.requestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.requestPath, append(request, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	close(releaseLock)
	if err := <-lockResult; err != nil {
		t.Fatalf("lock holder: %v", err)
	}
	if err := <-authorizationResult; err == nil || !strings.Contains(err.Error(), "approval token is stale") {
		t.Fatalf("authorization error = %v, want stale-token refusal", err)
	}
	if after := preflightStateBytes(t, fixture.root); !bytes.Equal(before, after) {
		t.Fatal("authorization committed request bytes read before the state transaction")
	}
}
