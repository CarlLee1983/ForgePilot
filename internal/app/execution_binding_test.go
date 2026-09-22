package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateCurrentExecutionBindingsRejectsManifestDriftAndMissingMigratedPath(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("plan = %#v, err=%v", preview, err)
	}
	authorized, err := AuthorizeExecutionFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator")
	if err != nil {
		t.Fatal(err)
	}
	digest := authorized.Authorizations[0].Digest
	if err := ValidateCurrentExecutionBindings(fixture.root, authorized.GoalID, digest); err != nil {
		t.Fatalf("current artifacts rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "manifest.json"), []byte("drift"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCurrentExecutionBindings(fixture.root, authorized.GoalID, digest); err == nil || !strings.Contains(err.Error(), "manifest.json") {
		t.Fatalf("manifest drift error = %v", err)
	}
	if err := storage.Update(fixture.root, func(state *work.State) error {
		for index := range state.Goals {
			if state.Goals[index].ID == authorized.GoalID {
				state.Goals[index].Execution = nil
				return nil
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCurrentExecutionBindings(fixture.root, authorized.GoalID, digest); err == nil || !strings.Contains(err.Error(), "no current execution authorization") {
		t.Fatalf("missing execution authorization error = %v", err)
	}
}
