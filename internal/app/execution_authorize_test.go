package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
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
		name   string
		mutate func(t *testing.T, fixture executionTestFixture, token string)
	}{
		{name: "stale token", mutate: func(t *testing.T, fixture executionTestFixture, token string) {
			before := preflightStateBytes(t, fixture.root)
			if _, err := authorizeExecutionAt(t.Context(), fixture.root, "execution-request.json", "sha256:stale", "operator", time.Now().UTC()); err == nil {
				t.Fatal("accepted stale token")
			}
			if after := preflightStateBytes(t, fixture.root); !bytes.Equal(before, after) {
				t.Fatal("stale token changed state")
			}
		}},
		{name: "request byte drift", mutate: func(t *testing.T, fixture executionTestFixture, token string) {
			before := preflightStateBytes(t, fixture.root)
			body, err := os.ReadFile(fixture.requestPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fixture.requestPath, append([]byte("\n"), body...), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := authorizeExecutionAt(t.Context(), fixture.root, "execution-request.json", token, "operator", time.Now().UTC()); err == nil {
				t.Fatal("accepted token for changed request bytes")
			}
			if after := preflightStateBytes(t, fixture.root); !bytes.Equal(before, after) {
				t.Fatal("request drift changed state")
			}
		}},
		{name: "artifact drift", mutate: func(t *testing.T, fixture executionTestFixture, token string) {
			before := preflightStateBytes(t, fixture.root)
			if err := os.WriteFile(filepath.Join(fixture.root, "source.md"), []byte("drift"), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := authorizeExecutionAt(t.Context(), fixture.root, "execution-request.json", token, "operator", time.Now().UTC()); err == nil {
				t.Fatal("accepted changed referenced artifact")
			}
			if after := preflightStateBytes(t, fixture.root); !bytes.Equal(before, after) {
				t.Fatal("artifact drift changed state")
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
			test.mutate(t, fixture, preview.ApprovalToken)
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
	if _, err := authorizeExecutionAt(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator", time.Now().UTC()); err == nil {
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
			_, err := authorizeExecutionAt(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator", time.Now().UTC())
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
