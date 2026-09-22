package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestExecutionRevisionAtomicallyAddsWorkAndPreservesAuthorizationHistory(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	initial, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(initial.Diagnostics) != 0 {
		t.Fatalf("initial plan = %#v, %v", initial, err)
	}
	if _, err := AuthorizeExecutionFile(t.Context(), fixture.root, "execution-request.json", initial.ApprovalToken, "operator"); err != nil {
		t.Fatal(err)
	}
	const priorRevision = "prior-revision"
	if err := storage.Update(fixture.root, func(state *work.State) error {
		// Seed both kinds of historical state the additive revision must carry
		// forward: an immutable PASS Evidence record and a consumed STEP charge.
		state.WorkItems[0].Status = work.Running
		if err := state.BeginVerification(state.WorkItems[0].ID, priorRevision, fixture.root, "", time.Now().UTC()); err != nil {
			return err
		}
		if _, err := state.RecordVerification(state.WorkItems[0].ID, priorRevision, "make verify", 0, time.Now().UTC()); err != nil {
			return err
		}
		_, err := state.PrepareExecutionReservation("goal", work.ExecutionReservation{
			ID: "run-001:step:001", Kind: work.ExecutionReservationStep, RunID: "run-001", CreatedAt: time.Now().UTC(),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	rewriteExecutionFixtureAsAdditiveRevision(t, fixture.root)
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	priorEvidence := append([]work.Evidence(nil), state.Evidence...)
	priorReservations := append([]work.ExecutionReservation(nil), state.Goals[0].Execution.Ledger.Reservations...)
	priorSteps := state.Goals[0].Execution.Ledger.StepsConsumed
	fixture.request.ExpectedAuthorizationDigest = state.Goals[0].Execution.Authorizations[0].Digest
	fixture.request.GoalPlanRequest.NodeMappings = append(fixture.request.GoalPlanRequest.NodeMappings, GoalPlanNodeMapping{PlanNodeRef: "c"})
	fixture.request.Caps.MaxSteps++
	fixture.request.WorkerProfile.Model = "revised-model"
	fixture.request.ExpiresAt = time.Now().UTC().Add(8 * 24 * time.Hour).Truncate(time.Second).Format(time.RFC3339)
	writeExecutionTestRequest(t, fixture.requestPath, fixture.request)
	preview, err := PlanExecutionRevisionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("revision plan = %#v, %v", preview, err)
	}
	if preview.RevisionDiff == nil || len(preview.RevisionDiff.AddedNodes) != 1 || preview.RevisionDiff.AddedNodes[0].PlanNodeRef != "c" ||
		preview.RevisionDiff.Contract == nil || preview.RevisionDiff.Caps == nil || preview.RevisionDiff.WorkerProfile == nil || preview.RevisionDiff.ExpiresAt == nil {
		t.Fatalf("revision diff = %#v", preview.RevisionDiff)
	}
	if preview.RevisionDiff.Caps.Proposed.MaxSteps != preview.RevisionDiff.Caps.Previous.MaxSteps+1 ||
		preview.RevisionDiff.WorkerProfile.Proposed.Model != "revised-model" ||
		preview.RevisionDiff.ExpiresAt.Proposed != preview.ExpiresAt {
		t.Fatalf("revision diff changes = %#v", preview.RevisionDiff)
	}
	encodedPreview, err := json.Marshal(preview)
	if err != nil || !bytes.Contains(encodedPreview, []byte(`"revisionDiff"`)) {
		t.Fatalf("revision preview JSON omits diff: %q, %v", encodedPreview, err)
	}
	if _, err := ReviseExecutionFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "revision-operator"); err != nil {
		t.Fatal(err)
	}
	state, err = storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	got := state.Goals[0].Execution
	if len(state.WorkItems) != 3 || len(got.PlanBindings) != 2 || len(got.Authorizations) != 2 || got.Ledger.AuthorizationRevision != 2 {
		t.Fatalf("atomic revision state = %#v", state)
	}
	if !reflect.DeepEqual(state.Evidence, priorEvidence) || !reflect.DeepEqual(got.Ledger.Reservations, priorReservations) || got.Ledger.StepsConsumed != priorSteps {
		t.Fatalf("revision did not preserve prior Evidence and cumulative consumption: evidence=%#v ledger=%#v", state.Evidence, got.Ledger)
	}
	if got.Authorizations[0].Approver != "operator" || got.Authorizations[1].Approver != "revision-operator" {
		t.Fatalf("authorization approvers = %#v; revision must retain its new self-declared approver", got.Authorizations)
	}
}

func TestExecutionRevisionApprovalTokenBindsArtifactByteLedger(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	initial, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(initial.Diagnostics) != 0 {
		t.Fatalf("initial plan = %#v, %v", initial, err)
	}
	if _, err := AuthorizeExecutionFile(t.Context(), fixture.root, "execution-request.json", initial.ApprovalToken, "operator"); err != nil {
		t.Fatal(err)
	}
	rewriteExecutionFixtureAsAdditiveRevision(t, fixture.root)
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	fixture.request.ExpectedAuthorizationDigest = state.Goals[0].Execution.Authorizations[0].Digest
	fixture.request.GoalPlanRequest.NodeMappings = append(fixture.request.GoalPlanRequest.NodeMappings, GoalPlanNodeMapping{PlanNodeRef: "c"})
	writeExecutionTestRequest(t, fixture.requestPath, fixture.request)
	before, err := PlanExecutionRevisionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(before.Diagnostics) != 0 {
		t.Fatalf("revision plan before artifact charge = %#v, %v", before, err)
	}
	if err := storage.Update(fixture.root, func(state *work.State) error {
		_, err := state.PrepareExecutionArtifactByteReservation("goal", work.ExecutionArtifactByteReservation{
			ID: "run-001:artifact:WI-001:1", RunID: "run-001", Bytes: 1, CreatedAt: time.Now().UTC(),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	after, err := PlanExecutionRevisionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(after.Diagnostics) != 0 {
		t.Fatalf("revision plan after artifact charge = %#v, %v", after, err)
	}
	if after.ApprovalToken == before.ApprovalToken {
		t.Fatal("revision approval token did not bind the changed artifact-byte ledger")
	}
}

func TestExecutionRevisionCannotCommitWhileRunnerOwnsWorkspace(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	initial, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(initial.Diagnostics) != 0 {
		t.Fatalf("initial plan = %#v, %v", initial, err)
	}
	if _, err := AuthorizeExecutionFile(t.Context(), fixture.root, "execution-request.json", initial.ApprovalToken, "operator"); err != nil {
		t.Fatal(err)
	}
	rewriteExecutionFixtureAsAdditiveRevision(t, fixture.root)
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	fixture.request.ExpectedAuthorizationDigest = state.Goals[0].Execution.Authorizations[0].Digest
	fixture.request.GoalPlanRequest.NodeMappings = append(fixture.request.GoalPlanRequest.NodeMappings, GoalPlanNodeMapping{PlanNodeRef: "c"})
	writeExecutionTestRequest(t, fixture.requestPath, fixture.request)
	preview, err := PlanExecutionRevisionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("revision plan = %#v, %v", preview, err)
	}
	before, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	err = storage.WithWorkspaceLock(fixture.root, func() error {
		command := exec.Command(os.Args[0], "-test.run=^TestExecutionRevisionLockHelper$", "-test.v")
		command.Env = append(os.Environ(),
			"FORGEPILOT_TEST_REVISION_LOCK_ROOT="+fixture.root,
			"FORGEPILOT_TEST_REVISION_LOCK_TOKEN="+preview.ApprovalToken,
		)
		output, commandErr := command.CombinedOutput()
		if commandErr != nil {
			return fmt.Errorf("revision lock helper failed: %s: %w", output, commandErr)
		}
		if !bytes.Contains(output, []byte("revision-lock-helper-executed")) {
			return fmt.Errorf("revision lock helper did not execute its assertion: %s", output)
		}
		after, loadErr := storage.Load(fixture.root)
		if loadErr != nil {
			return loadErr
		}
		if !bytes.Equal(marshalState(t, before), marshalState(t, after)) {
			return errors.New("revision changed state while a Runner held the workspace lock")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestExecutionRevisionLockHelper(t *testing.T) {
	root := os.Getenv("FORGEPILOT_TEST_REVISION_LOCK_ROOT")
	if root == "" {
		return
	}
	token := os.Getenv("FORGEPILOT_TEST_REVISION_LOCK_TOKEN")
	if token == "" {
		t.Fatal("revision lock helper did not receive the approval token")
	}
	_, err := ReviseExecutionFile(t.Context(), root, "execution-request.json", token, "operator")
	if err == nil || !errors.Is(err, storage.ErrRunnerInFlight) {
		t.Fatalf("cross-process revision error = %v; want live Runner ownership refusal", err)
	}
	t.Log("revision-lock-helper-executed")
}

func TestExecutionRevisionRefusesArtifactTotalCapBelowPreservedReservations(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	initial, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(initial.Diagnostics) != 0 {
		t.Fatalf("initial plan = %#v, %v", initial, err)
	}
	if _, err := AuthorizeExecutionFile(t.Context(), fixture.root, "execution-request.json", initial.ApprovalToken, "operator"); err != nil {
		t.Fatal(err)
	}
	if err := storage.Update(fixture.root, func(state *work.State) error {
		_, err := state.PrepareExecutionArtifactByteReservation("goal", work.ExecutionArtifactByteReservation{
			ID: "run-001:artifact:WI-001:1", RunID: "run-001", Bytes: 2, CreatedAt: time.Now().UTC(),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	rewriteExecutionFixtureAsAdditiveRevision(t, fixture.root)
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	fixture.request.ExpectedAuthorizationDigest = state.Goals[0].Execution.Authorizations[0].Digest
	fixture.request.GoalPlanRequest.NodeMappings = append(fixture.request.GoalPlanRequest.NodeMappings, GoalPlanNodeMapping{PlanNodeRef: "c"})
	fixture.request.Caps.MaxHandoffBytes = 1
	fixture.request.Caps.MaxWriteBytes = 1
	fixture.request.Caps.MaxRunBytes = 1
	fixture.request.Caps.MaxTotalBytes = 1
	writeExecutionTestRequest(t, fixture.requestPath, fixture.request)
	preview, err := PlanExecutionRevisionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("revision plan = %#v, %v", preview, err)
	}
	before := marshalState(t, state)
	if _, err := ReviseExecutionFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator"); err == nil ||
		!strings.Contains(err.Error(), "artifact total cap") {
		t.Fatalf("lower artifact cap revision error = %v", err)
	}
	after, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, marshalState(t, after)) {
		t.Fatal("rejected lower artifact-cap revision changed state")
	}
}

func TestExecutionRevisionCreatesAdditiveNodesInDependencyOrderButPreservesManifestOrder(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	initial, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(initial.Diagnostics) != 0 {
		t.Fatalf("initial plan = %#v, %v", initial, err)
	}
	if _, err := AuthorizeExecutionFile(t.Context(), fixture.root, "execution-request.json", initial.ApprovalToken, "operator"); err != nil {
		t.Fatal(err)
	}
	rewriteExecutionFixtureAsAdditiveRevision(t, fixture.root)
	rewriteAdditiveRevisionWithReverseDependencyOrder(t, fixture.root)
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	fixture.request.ExpectedAuthorizationDigest = state.Goals[0].Execution.Authorizations[0].Digest
	fixture.request.GoalPlanRequest.NodeMappings = append(fixture.request.GoalPlanRequest.NodeMappings,
		GoalPlanNodeMapping{PlanNodeRef: "c"}, GoalPlanNodeMapping{PlanNodeRef: "d"})
	writeExecutionTestRequest(t, fixture.requestPath, fixture.request)
	preview, err := PlanExecutionRevisionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("revision plan = %#v, %v", preview, err)
	}
	if _, err := ReviseExecutionFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator"); err != nil {
		t.Fatal(err)
	}
	state, err = storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	binding := state.Goals[0].Execution.PlanBindings[1]
	if len(binding.Nodes) != 4 || binding.Nodes[2].PlanNodeRef != "c" || binding.Nodes[3].PlanNodeRef != "d" {
		t.Fatalf("durable binding lost manifest order: %#v", binding.Nodes)
	}
	if len(state.WorkItems) != 4 || state.WorkItems[2].StoryRef != "story-d" || state.WorkItems[3].StoryRef != "story-c" ||
		len(state.WorkItems[3].DependsOn) != 1 || state.WorkItems[3].DependsOn[0] != state.WorkItems[2].ID {
		t.Fatalf("new Work Items were not created in dependency order: %#v", state.WorkItems)
	}
}

func rewriteExecutionFixtureAsAdditiveRevision(t *testing.T, root string) {
	t.Helper()
	readiness := []byte(`{"format":"opaque","node":"c"}`)
	if err := os.MkdirAll(filepath.Join(root, "story-c"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "story-c", "readiness.json"), readiness, 0644); err != nil {
		t.Fatal(err)
	}
	declarationPath := filepath.Join(root, "declaration.json")
	var declaration map[string]any
	bytes, err := os.ReadFile(declarationPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(bytes, &declaration); err != nil {
		t.Fatal(err)
	}
	declaration["plan"].(map[string]any)["revision"] = float64(2)
	declaration["nodes"] = append(declaration["nodes"].([]any), map[string]any{"nodeRef": "c", "storyRef": "story-c", "dependsOn": []any{"b"}})
	bytes, err = json.Marshal(declaration)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(declarationPath, bytes, 0644); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "manifest.json")
	bytes, err = os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(bytes, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["plan"].(map[string]any)["revision"] = float64(2)
	manifest["declaration"].(map[string]any)["sha256"] = testDigest(bytesOfFile(t, declarationPath))
	manifest["nodes"] = append(manifest["nodes"].([]any), map[string]any{"nodeRef": "c", "storyRef": "story-c", "readinessContract": map[string]any{"path": "story-c/readiness.json", "sha256": testDigest(readiness)}, "dependsOn": []any{"b"}})
	bytes, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, bytes, 0644); err != nil {
		t.Fatal(err)
	}
	mutatePreflightArtifacts(t, root, nil, func(review map[string]any) { review["manifestSha256"] = testDigest(bytes) })
}

func bytesOfFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func rewriteAdditiveRevisionWithReverseDependencyOrder(t *testing.T, root string) {
	t.Helper()
	readiness := []byte(`{"format":"opaque","node":"d"}`)
	if err := os.MkdirAll(filepath.Join(root, "story-d"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "story-d", "readiness.json"), readiness, 0644); err != nil {
		t.Fatal(err)
	}
	declarationPath := filepath.Join(root, "declaration.json")
	var declaration map[string]any
	if err := json.Unmarshal(bytesOfFile(t, declarationPath), &declaration); err != nil {
		t.Fatal(err)
	}
	nodes := declaration["nodes"].([]any)
	nodes[2].(map[string]any)["dependsOn"] = []any{"d"}
	declaration["nodes"] = append(nodes, map[string]any{"nodeRef": "d", "storyRef": "story-d", "dependsOn": []any{"b"}})
	declarationBytes, err := json.Marshal(declaration)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(declarationPath, declarationBytes, 0644); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "manifest.json")
	var manifest map[string]any
	if err := json.Unmarshal(bytesOfFile(t, manifestPath), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["declaration"].(map[string]any)["sha256"] = testDigest(declarationBytes)
	manifestNodes := manifest["nodes"].([]any)
	manifestNodes[2].(map[string]any)["dependsOn"] = []any{"d"}
	manifest["nodes"] = append(manifestNodes, map[string]any{"nodeRef": "d", "storyRef": "story-d",
		"readinessContract": map[string]any{"path": "story-d/readiness.json", "sha256": testDigest(readiness)}, "dependsOn": []any{"b"}})
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0644); err != nil {
		t.Fatal(err)
	}
	mutatePreflightArtifacts(t, root, nil, func(review map[string]any) { review["manifestSha256"] = testDigest(manifestBytes) })
}
