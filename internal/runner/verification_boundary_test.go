package runner

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestVerificationBindingDriftAfterCheckRetainsEvidenceAndStopsContinuation(t *testing.T) {
	root, _, _, now, execution := newUnresolvedExecutionRunnerFixture(t)
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte("verify:\n\t@printf 'drift' > "+filepath.Join(root, "manifest.json")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var itemID string
	if err := storage.Update(root, func(state *work.State) error {
		itemID = state.WorkItems[0].ID
		return state.Start(itemID, now)
	}); err != nil {
		t.Fatal(err)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, ok := state.GoalByID(execution.GoalID)
	if !ok || goal.Execution == nil {
		t.Fatal("execution fixture Goal is missing its sealed authorization")
	}
	digest := goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1].Digest

	result, err := app.Verify(t.Context(), root, itemID, io.Discard, app.VerifyOptions{Snapshot: true,
		Now: func() time.Time { return now }, ExecutionGoalID: execution.GoalID,
		ExecutionAuthorizationDigest: digest})
	if !errors.Is(err, app.ErrExecutionBindingDrift) {
		t.Fatalf("verification drift error = %v; want execution binding drift", err)
	}
	if !result.HasEvidence || result.Evidence.Result != work.Pass {
		t.Fatalf("lawfully started verification did not retain PASS Evidence: %#v", result)
	}
	state, err = storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	evidence, ok := state.LatestVerification(itemID)
	if !ok || evidence.ID != result.Evidence.ID || evidence.Result != work.Pass {
		t.Fatalf("saved Evidence = %#v, want retained immutable PASS Evidence %#v", evidence, result.Evidence)
	}
	if err := app.ValidateCurrentExecutionBindings(root, execution.GoalID, digest); err == nil {
		t.Fatal("drifted execution binding still admitted a subsequent action")
	}
}

func TestRunnerVerificationBindingDriftPersistsScopeChangedStop(t *testing.T) {
	root, runtimeCommand, _, now, execution := newUnresolvedExecutionRunnerFixture(t)
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte("verify:\n\t@printf 'drift' > "+filepath.Join(root, "manifest.json")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var itemID string
	if err := storage.Update(root, func(state *work.State) error {
		itemID = state.WorkItems[0].ID
		return state.Start(itemID, now)
	}); err != nil {
		t.Fatal(err)
	}
	identity := resolveExecutionIdentityForRunnerTest(t, root, now)
	options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
	options.Output = io.Discard
	runner, err := newRunner(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.verify(itemID, "VERIFY"); err != nil {
		t.Fatal(err)
	}
	if runner.record.Stop == nil || runner.record.Stop.Reason != StopScopeChanged {
		t.Fatalf("Runner verification stop = %#v; want SCOPE_CHANGED", runner.record.Stop)
	}
	stored, err := LoadRecord(root, runner.record.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Stop == nil || stored.Stop.Reason != StopScopeChanged {
		t.Fatalf("durable Runner stop = %#v; want SCOPE_CHANGED", stored.Stop)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	evidence, ok := state.LatestVerification(itemID)
	if !ok || evidence.Result != work.Pass {
		t.Fatalf("Runner verification Evidence = %#v; want retained PASS", evidence)
	}
	if err := app.ValidateCurrentExecutionBindings(root, execution.GoalID, runner.record.ExecutionAuthorizationDigest); err == nil {
		t.Fatal("drifted execution binding still admitted a subsequent Runner action")
	}
}
