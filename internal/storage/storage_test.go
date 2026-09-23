package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestInitRetryAndFailedWritePreserveState(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	if err := Update(root, func(state *work.State) error { return state.AddGoal("g", "Goal", "", root, time.Now().UTC()) }); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, stateDirectory, "state.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, stateDirectory)
	if err := os.Chmod(directory, 0555); err != nil {
		t.Fatal(err)
	}
	err = Update(root, func(state *work.State) error { return state.AddGoal("other", "Other", "", root, time.Now().UTC()) })
	if restoreErr := os.Chmod(directory, 0755); restoreErr != nil {
		t.Fatal(restoreErr)
	}
	if err == nil {
		t.Fatal("write succeeded in read-only state directory")
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("failed write changed state")
	}
}

func TestLoadRejectsCorruptAndFutureState(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, stateDirectory, "state.json")
	// The second fixture must name a schema version this binary does not yet
	// support. It has to be raised with every bump: left behind, it silently
	// stops testing rejection and starts testing that a valid state loads.
	for _, contents := range []string{"{", `{"schema_version":19,"next_work_id":1,"next_evidence_id":1,"next_gate_id":1,"goals":[],"work_items":[],"evidence":[],"gates":[]}`} {
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(root); err == nil {
			t.Fatalf("accepted %q", contents)
		}
	}
}

func TestUpgradeV15RefusesArtifactAccountingUnderAnOlderHeader(t *testing.T) {
	contents := []byte(`{"schema_version":15,"goals":[{"id":"g","execution":{"ledger":{"artifact_accounting_start_revision":1}}}]}`)
	if _, err := upgrade(contents, 15); err == nil || !strings.Contains(err.Error(), "artifact-byte accounting") {
		t.Fatalf("v15 artifact-accounting header contradiction = %v", err)
	}
}

func TestUpgradeV17DoesNotAdoptForgedRetentionProvenance(t *testing.T) {
	contents := []byte(`{"schema_version":17,"goals":[{"id":"g","execution":{"authorizations":[{"retention_acquired":true}]}}]}`)
	if _, err := upgrade(contents, 17); err == nil || !strings.Contains(err.Error(), "generation retention provenance") {
		t.Fatalf("v17 retention claim = %v; want refusal before migration", err)
	}
}

func TestUpgradeV15ResealsAuthorizedExecutionWithUnknownArtifactUsage(t *testing.T) {
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	state := work.NewState()
	if err := state.AddGoalWithPolicies("g", "Goal", "", "/workspace", work.ReviewPerGoal, work.CompletionVerified, now); err != nil {
		t.Fatal(err)
	}
	item, err := state.AddWork("g", "specs/stories/migrate", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 64)
	binding := work.GoalPlanBinding{Revision: 1, RequestSHA256: "sha256:" + sha, PlanID: "plan-1", PlanRevision: 1,
		ManifestSHA256: sha, Manifest: work.ExecutionArtifactBinding{Path: "manifest.json", SHA256: sha}, CoverageReviewID: "review-1",
		CoverageReview: work.ExecutionArtifactBinding{Path: "coverage.json", SHA256: sha}, Declaration: work.ExecutionArtifactBinding{Path: "plan.json", SHA256: sha},
		Nodes: []work.ExecutionPlanNodeBinding{{PlanNodeRef: "node-1", StoryRef: item.StoryRef, WorkItemID: item.ID,
			ReadinessContract: work.ExecutionArtifactBinding{Path: "readiness.json", SHA256: sha}}}, AdoptedAt: now}
	execution := work.GoalExecution{GoalID: "g", Workspace: "/workspace", PlanBindings: []work.GoalPlanBinding{binding},
		Authorizations: []work.ExecutionAuthorization{{Revision: 1, GoalID: "g", Workspace: "/workspace", RequestSHA256: "sha256:" + sha,
			ApprovalToken: "sha256:" + sha, Approver: "operator", AuthorizedAt: now, ExpiresAt: now.Add(time.Hour),
			Caps: work.ExecutionCaps{MaxSteps: 2, MaxTechnicalAttemptsPerNode: 1, MaxRuns: 1, MaxRecoveries: 1,
				Artifacts: work.ExecutionArtifactLimits{MaxHandoffBytes: 1, MaxWriteBytes: 1, MaxRunBytes: 2, MaxTotalBytes: 2}},
			WorkerProfile: work.WorkerProfile{Runtime: "codex", ExecutablePath: "/bin/codex", ExecutableSHA256: sha, Model: "test", Effort: "medium", Sandbox: "workspace-write"}}},
		Ledger: work.ExecutionLedger{Revision: 1, AuthorizationRevision: 1, NodeAttempts: []work.ExecutionNodeAttempts{{PlanNodeRef: "node-1"}}}}
	if err := state.AdoptInitialExecution(execution); err != nil {
		t.Fatal(err)
	}
	// Recreate the actual v15 ledger/witness seals, whose JSON omitted the
	// three v16 artifact-accounting fields. This is deliberately not a v16
	// aggregate with only its header rewound.
	legacyExecution := state.Goals[0].Execution
	legacyExecution.Ledger.Digest = legacyV15LedgerDigestForTest(t, legacyExecution.Ledger)
	legacyExecution.Witness.LedgerDigest = legacyExecution.Ledger.Digest
	legacyExecution.Witness.Digest = ""
	legacyExecution.Witness.Digest = legacyExecutionDigestForTest(t, "forgepilot.goal-execution-witness/v1", legacyExecution.Witness)
	contents, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(contents, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy["schema_version"] = float64(15)
	goal := legacy["goals"].([]any)[0].(map[string]any)
	ledger := goal["execution"].(map[string]any)["ledger"].(map[string]any)
	delete(ledger, "artifact_accounting_start_revision")
	delete(ledger, "artifact_bytes_consumed")
	legacyContents, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	migrated, err := upgrade(legacyContents, 15)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrated.Validate(); err != nil {
		t.Fatalf("migrated v15 authorization is invalid: %v", err)
	}
	got, ok := migrated.GoalByID("g")
	if !ok || got.Execution == nil || got.Execution.Ledger.ArtifactAccountingStartRevision != 0 || got.Execution.Ledger.ArtifactBytesConsumed != 0 {
		t.Fatalf("migration did not preserve unknown artifact usage: %#v", got)
	}
	if got.Execution.Witness.LedgerDigest != got.Execution.Ledger.Digest {
		t.Fatalf("migration did not reseal witness: %#v", got.Execution.Witness)
	}
	var corrupt map[string]any
	if err := json.Unmarshal(legacyContents, &corrupt); err != nil {
		t.Fatal(err)
	}
	corruptLedger := corrupt["goals"].([]any)[0].(map[string]any)["execution"].(map[string]any)["ledger"].(map[string]any)
	corruptLedger["digest"] = "sha256:" + strings.Repeat("b", 64)
	corruptContents, err := json.Marshal(corrupt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := upgrade(corruptContents, 15); err == nil || !strings.Contains(err.Error(), "v15 execution aggregate") {
		t.Fatalf("corrupt v15 execution aggregate was migrated: %v", err)
	}
}

type legacyV15LedgerForTest struct {
	Revision               int                                   `json:"revision"`
	AuthorizationRevision  int                                   `json:"authorization_revision"`
	StepsConsumed          int                                   `json:"steps_consumed"`
	RunsConsumed           int                                   `json:"runs_consumed"`
	RecoveriesConsumed     int                                   `json:"recoveries_consumed"`
	NodeAttempts           []work.ExecutionNodeAttempts          `json:"node_attempts"`
	Reservations           []work.ExecutionReservation           `json:"reservations,omitempty"`
	NeedsHumanDispositions []work.ExecutionNeedsHumanDisposition `json:"needs_human_dispositions,omitempty"`
	Digest                 string                                `json:"digest"`
}

func legacyV15LedgerDigestForTest(t *testing.T, ledger work.ExecutionLedger) string {
	t.Helper()
	return legacyExecutionDigestForTest(t, "forgepilot.execution-ledger/v1", legacyV15LedgerForTest{
		Revision: ledger.Revision, AuthorizationRevision: ledger.AuthorizationRevision, StepsConsumed: ledger.StepsConsumed,
		RunsConsumed: ledger.RunsConsumed, RecoveriesConsumed: ledger.RecoveriesConsumed, NodeAttempts: ledger.NodeAttempts,
		Reservations: ledger.Reservations, NeedsHumanDispositions: ledger.NeedsHumanDispositions,
	})
}

func legacyExecutionDigestForTest(t *testing.T, domain string, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(encoded)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func TestRuntimeMetadataIsDefensivelyCopiedBeforeSaving(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	runtime := map[string]string{"go": "1.25.5"}
	if err := Update(root, func(state *work.State) error {
		if err := state.AddGoal("g", "Goal", "", canonicalRoot, now); err != nil {
			return err
		}
		item, err := state.AddWork("g", "specs/stories/a", nil, now)
		if err != nil {
			return err
		}
		if err := state.Start(item.ID, now); err != nil {
			return err
		}
		if err := state.BeginCandidateVerificationWithRuntime(item.ID, work.Candidate{Kind: work.CommitCandidate, Revision: "abc123"}, "/tmp/wt", "", runtime, now); err != nil {
			return err
		}
		runtime["go"] = "mutated-before-save"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	state, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItems[0].CurrentRun.Runtime["go"]; got != "1.25.5" {
		t.Fatalf("saved runtime = %q, want defensive copy", got)
	}
}

func TestLockReleasedAfterProcessExit(t *testing.T) {
	if directory := os.Getenv("FORGEPILOT_LOCK_HOLDER"); directory != "" {
		_ = withLock(directory, func() error { os.Exit(0); return nil })
		return
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, stateDirectory)
	command := exec.Command(os.Args[0], "-test.run=TestLockReleasedAfterProcessExit")
	command.Env = append(os.Environ(), "FORGEPILOT_LOCK_HOLDER="+directory)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("holder: %v: %s", err, output)
	}
	if err := withLock(directory, func() error { return nil }); err != nil {
		t.Fatalf("reacquire lock: %v", err)
	}
}

func TestMovedRepositoryIsRejected(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "original")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	if err := Update(root, func(state *work.State) error { return state.AddGoal("g", "Goal", "", root, time.Now().UTC()) }); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(base, "moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(moved); err == nil {
		t.Fatal("accepted state after repository move")
	}
}

// legacyState writes a schema v1 snapshot, the shape M1 left behind.
func legacyState(t *testing.T, root string) string {
	t.Helper()
	canonical, err := canonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	contents := `{"schema_version":1,"next_work_id":3,"goals":[{"id":"g","title":"Goal","description":"","repository":"` +
		canonical + `","status":"ACTIVE","created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"}],` +
		`"work_items":[{"id":"WI-001","goal_id":"g","story_ref":"specs/stories/a","status":"RUNNING","depends_on":null,` +
		`"created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"},` +
		`{"id":"WI-002","goal_id":"g","story_ref":"specs/stories/b","status":"PENDING","depends_on":["WI-001"],` +
		`"created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(root, stateDirectory, "state.json"), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return contents
}

func TestMigrateUpgradesLegacyStateAndKeepsBackup(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	original := legacyState(t, root)

	_, err := Load(root)
	if err == nil {
		t.Fatal("read a v1 state without migrating")
	}
	if !strings.Contains(err.Error(), "migrate") {
		t.Fatalf("error %q does not tell the user to migrate", err)
	}

	migrated, err := Migrate(root)
	if err != nil || !migrated {
		t.Fatalf("Migrate = %v, %v", migrated, err)
	}
	state, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Goals) != 1 || len(state.WorkItems) != 2 || state.NextWorkID != 3 {
		t.Fatalf("migration lost data: %#v", state)
	}
	if state.WorkItems[1].DependsOn[0] != "WI-001" || state.WorkItems[0].Status != work.Running {
		t.Fatalf("migration changed work items: %#v", state.WorkItems)
	}
	if state.NextEvidenceID != 1 || len(state.Evidence) != 0 || state.WorkItems[0].CurrentRun != nil {
		t.Fatalf("migration did not initialise v2 fields: %#v", state)
	}

	backup, err := os.ReadFile(filepath.Join(root, stateDirectory, "state.json.v1.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != original {
		t.Fatal("backup does not hold the original snapshot")
	}

	// Already current: report no upgrade, succeed, change nothing.
	before, err := os.ReadFile(filepath.Join(root, stateDirectory, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	migrated, err = Migrate(root)
	if err != nil || migrated {
		t.Fatalf("second Migrate = %v, %v", migrated, err)
	}
	after, err := os.ReadFile(filepath.Join(root, stateDirectory, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("migrating an up-to-date state rewrote it")
	}

	// An existing backup must never be overwritten.
	legacyState(t, root)
	if _, err := Migrate(root); err == nil {
		t.Fatal("overwrote an existing backup")
	}
	current, err := os.ReadFile(filepath.Join(root, stateDirectory, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != original {
		t.Fatal("refused migration still modified the state")
	}
}

func TestV10GoalMigrationEnablesContinuousGoalCompletion(t *testing.T) {
	contents := []byte(`{"schema_version":10,"next_work_id":1,"next_evidence_id":1,"next_gate_id":1,"next_verification_run_id":1,"goals":[` +
		`{"id":"auto","title":"Auto","description":"","repository":"/repo","status":"ACTIVE","review_policy":"GOAL"},` +
		`{"id":"human","title":"Human","description":"","repository":"/repo","status":"ACTIVE","review_policy":"WORK_ITEM"}],` +
		`"work_items":[],"evidence":[],"gates":[]}`)
	state, err := upgrade(contents, 10)
	if err != nil {
		t.Fatal(err)
	}
	auto, ok := state.GoalByID("auto")
	if !ok || auto.CompletionPolicy != work.CompletionVerified {
		t.Fatalf("migrated GOAL completion policy = %#v, want VERIFIED", auto)
	}
	human, ok := state.GoalByID("human")
	if !ok || human.CompletionPolicy != work.CompletionHuman {
		t.Fatalf("migrated WORK_ITEM completion policy = %#v, want HUMAN", human)
	}
	if state.NextGoalCompletionEvidenceID != 1 || state.SchemaVersion != work.SchemaVersion {
		t.Fatalf("migrated completion counters/version = %d / %d", state.NextGoalCompletionEvidenceID, state.SchemaVersion)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("migrated state invalid: %v", err)
	}
}

func TestMigrateV11ConvertsGoalHumanCompletionAndKeepsBackup(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	root, err := canonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	legacy := work.State{
		SchemaVersion: 11, NextWorkID: 1, NextEvidenceID: 1, NextGateID: 1, NextVerificationRunID: 1,
		NextGoalCompletionEvidenceID: 1,
		Goals: []work.Goal{{ID: "g", Title: "Goal", Repository: root, Status: work.GoalActive,
			ReviewPolicy: work.ReviewPerGoal, CompletionPolicy: work.CompletionHuman}},
	}
	contents, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath(filepath.Join(root, stateDirectory)), contents, 0600); err != nil {
		t.Fatal(err)
	}
	if migrated, err := Migrate(root); err != nil || !migrated {
		t.Fatalf("Migrate = %v, %v", migrated, err)
	}
	backup, err := os.ReadFile(statePath(filepath.Join(root, stateDirectory)) + ".v11.bak")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backup, contents) {
		t.Fatal("v11 backup does not preserve the original HUMAN Goal snapshot")
	}
	state, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, ok := state.GoalByID("g")
	if !ok || goal.CompletionPolicy != work.CompletionVerified || goal.Status != work.GoalActive || state.SchemaVersion != work.SchemaVersion {
		t.Fatalf("migrated Goal = %#v, schema %d; want ACTIVE VERIFIED under v13", goal, state.SchemaVersion)
	}
}

func TestMigrateV11PreservesCompletedHumanGoalWithLegacyProvenance(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	root, err := canonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	revision := "1111111111111111111111111111111111111111"
	legacy := work.NewState()
	legacy.SchemaVersion = 11
	if err := legacy.AddGoalWithPolicies("g", "Goal", "", root, work.ReviewPerGoal, work.CompletionVerified, now); err != nil {
		t.Fatal(err)
	}
	legacy.Goals[0].CompletionPolicy = work.CompletionHuman
	item, err := legacy.AddWork("g", "specs/stories/a", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Start(item.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := legacy.BeginVerification(item.ID, revision, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.RecordVerificationWithRepository(item.ID, revision, "make verify", 0, work.RepositoryState{Revision: revision}, now); err != nil {
		t.Fatal(err)
	}
	legacy.Goals[0].Status = work.GoalCompleted
	legacy.Goals[0].UpdatedAt = now
	contents, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath(filepath.Join(root, stateDirectory)), contents, 0600); err != nil {
		t.Fatal(err)
	}

	if migrated, err := Migrate(root); err != nil || !migrated {
		t.Fatalf("Migrate = %v, %v", migrated, err)
	}
	backup, err := os.ReadFile(statePath(filepath.Join(root, stateDirectory)) + ".v11.bak")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backup, contents) {
		t.Fatal("v11 backup does not preserve the original completed HUMAN Goal state")
	}
	path := statePath(filepath.Join(root, stateDirectory))
	migratedBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, ok := state.GoalByID("g")
	if !ok || goal.Status != work.GoalCompleted || goal.CompletionPolicy != work.CompletionVerified || state.SchemaVersion != work.SchemaVersion {
		t.Fatalf("migrated Goal = %#v, schema %d; want COMPLETED VERIFIED under v13", goal, state.SchemaVersion)
	}
	if !goal.CreatedAt.Equal(now) || !goal.UpdatedAt.Equal(now) {
		t.Fatalf("migration changed Goal timestamps: created=%s updated=%s, want both %s", goal.CreatedAt, goal.UpdatedAt, now)
	}
	if len(state.GoalCompletionEvidence) != 0 {
		t.Fatalf("legacy HUMAN provenance was misrepresented as automatic completion evidence: %#v", state.GoalCompletionEvidence)
	}
	if goal.LegacyCompletion == nil || goal.LegacyCompletion.SourceSchemaVersion != 11 ||
		goal.LegacyCompletion.CompletionPolicy != work.CompletionHuman {
		t.Fatalf("legacy completion provenance = %#v", goal.LegacyCompletion)
	}
	if state.NextGoalCompletionEvidenceID != legacy.NextGoalCompletionEvidenceID {
		t.Fatalf("migration changed the automatic completion evidence counter from %d to %d", legacy.NextGoalCompletionEvidenceID, state.NextGoalCompletionEvidenceID)
	}
	if afterLoad, err := os.ReadFile(path); err != nil || !bytes.Equal(afterLoad, migratedBytes) {
		t.Fatalf("loading migrated state rewrote it: err=%v", err)
	}
}

func TestMigrateV12PreservesGoalCompletionAndAddsNoExecution(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	root, err := canonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	revision := "1111111111111111111111111111111111111111"
	legacy := work.NewState()
	if err := legacy.AddGoalWithPolicies("g", "Goal", "", root, work.ReviewPerGoal, work.CompletionVerified, now); err != nil {
		t.Fatal(err)
	}
	item, err := legacy.AddWork("g", "specs/stories/a", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Start(item.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := legacy.BeginVerification(item.ID, revision, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.RecordVerificationWithRepository(item.ID, revision, "make verify", 0, work.RepositoryState{Revision: revision}, now); err != nil {
		t.Fatal(err)
	}
	summary, err := legacy.GoalSummary("g", work.RepositoryState{Revision: revision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.CompleteVerifiedGoal("g", work.RepositoryState{Revision: revision}, summary.VerificationEvidenceIDs, now); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Validate(); err != nil {
		t.Fatalf("completed state fixture is invalid: %v", err)
	}
	legacy.SchemaVersion = 12
	contents, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath(filepath.Join(root, stateDirectory)), contents, 0600); err != nil {
		t.Fatal(err)
	}
	if migrated, err := Migrate(root); err != nil || !migrated {
		t.Fatalf("Migrate = %v, %v", migrated, err)
	}
	backup, err := os.ReadFile(statePath(filepath.Join(root, stateDirectory)) + ".v12.bak")
	if err != nil || !bytes.Equal(backup, contents) {
		t.Fatalf("v12 backup = %v, err=%v", backup, err)
	}
	migratedBytes, err := os.ReadFile(statePath(filepath.Join(root, stateDirectory)))
	if err != nil {
		t.Fatal(err)
	}
	state, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, ok := state.GoalByID("g")
	if !ok || goal.CompletionPolicy != work.CompletionVerified || goal.Status != work.GoalCompleted ||
		state.SchemaVersion != work.SchemaVersion || len(state.GoalCompletionEvidence) != 1 || goal.Execution != nil {
		t.Fatalf("migrated Goal/state = %#v / %#v; want completion preserved and no inferred execution under v13", goal, state)
	}
	after, err := os.ReadFile(statePath(filepath.Join(root, stateDirectory)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, migratedBytes) {
		t.Fatal("loading a migrated v13 state rewrote it")
	}
	if _, err := os.Stat(statePath(filepath.Join(root, stateDirectory)) + ".v13.bak"); !os.IsNotExist(err) {
		t.Fatalf("migration created an unexpected v13 backup: %v", err)
	}
}

func TestUpgradeV12RefusesExecutionDataUnderstatedByHeader(t *testing.T) {
	contents := []byte(`{"schema_version":12,"next_work_id":1,"next_evidence_id":1,"next_gate_id":1,"next_verification_run_id":1,"next_goal_completion_evidence_id":1,"goals":[{"id":"g","title":"Goal","description":"","repository":"/repo","status":"ACTIVE","review_policy":"WORK_ITEM","completion_policy":"HUMAN","execution":{}}],"work_items":[],"evidence":[],"gates":[],"goal_completion_evidence":[]}`)
	if _, err := upgrade(contents, 12); err == nil || !strings.Contains(err.Error(), "already carries execution authorization data") {
		t.Fatalf("understated v12 execution data error = %v", err)
	}
}

func TestV10MigrationRefusesCompletionFieldsUnderstatedByItsHeader(t *testing.T) {
	for name, fields := range map[string]string{
		"completion policy":  `"completion_policy":"HUMAN"`,
		"completion counter": `"next_goal_completion_evidence_id":1`,
	} {
		t.Run(name, func(t *testing.T) {
			goalFields := `"id":"g","title":"Goal","description":"","repository":"/repo","status":"ACTIVE","review_policy":"GOAL"`
			stateFields := `"next_work_id":1,"next_evidence_id":1,"next_gate_id":1,"next_verification_run_id":1`
			if strings.Contains(fields, "completion_policy") {
				goalFields += `,` + fields
			} else {
				stateFields += `,` + fields
			}
			contents := []byte(`{"schema_version":10,` + stateFields + `,"goals":[{` + goalFields + `}],"work_items":[],"evidence":[],"gates":[]}`)
			if _, err := upgrade(contents, 10); err == nil || !strings.Contains(err.Error(), "already carries") {
				t.Fatalf("understated schema header error = %v", err)
			}
		})
	}
}

// v2State writes a schema v2 snapshot carrying accumulated Evidence, the shape
// M2 left behind.
func v2State(t *testing.T, root string) string {
	t.Helper()
	canonical, err := canonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	contents := `{"schema_version":2,"next_work_id":3,"next_evidence_id":2,` +
		`"goals":[{"id":"g","title":"Goal","description":"","repository":"` + canonical +
		`","status":"ACTIVE","created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"}],` +
		`"work_items":[{"id":"WI-001","goal_id":"g","story_ref":"specs/stories/a","status":"REVIEW","depends_on":null,` +
		`"current_run":null,"created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"},` +
		`{"id":"WI-002","goal_id":"g","story_ref":"specs/stories/b","status":"PENDING","depends_on":["WI-001"],` +
		`"current_run":null,"created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"}],` +
		`"evidence":[{"id":"EV-001","type":"verification","repository":"` + canonical +
		`","work_item_id":"WI-001","story_ref":"specs/stories/a","revision":"abc123","command":"make verify",` +
		`"exit_code":0,"result":"PASS","created_at":"2026-09-07T00:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(root, stateDirectory, "state.json"), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return contents
}

func TestMigrateUpgradesV2StateAndKeepsEvidence(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	original := v2State(t, root)

	if _, err := Load(root); err == nil {
		t.Fatal("read a v2 state without migrating")
	} else if !strings.Contains(err.Error(), "migrate") {
		t.Fatalf("error %q does not tell the user to migrate", err)
	}

	migrated, err := Migrate(root)
	if err != nil || !migrated {
		t.Fatalf("Migrate = %v, %v", migrated, err)
	}
	state, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if state.SchemaVersion != work.SchemaVersion || state.NextWorkID != 3 || state.NextEvidenceID != 2 {
		t.Fatalf("migration lost counters: %#v", state)
	}
	if len(state.Goals) != 1 || len(state.WorkItems) != 2 || len(state.Evidence) != 1 {
		t.Fatalf("migration lost data: %#v", state)
	}
	if got := state.Evidence[0]; got.ID != "EV-001" || got.Result != work.Pass || got.Revision != "abc123" || got.ExitCode == nil || *got.ExitCode != 0 {
		t.Fatalf("migration changed evidence: %#v", got)
	}
	if state.WorkItems[1].DependsOn[0] != "WI-001" || state.WorkItems[0].Status != work.Review {
		t.Fatalf("migration changed work items: %#v", state.WorkItems)
	}
	// The v3 containers exist and start empty: nothing writes to them yet.
	if state.NextGateID != 1 || len(state.Gates) != 0 {
		t.Fatalf("migration did not initialise v3 fields: %#v", state)
	}

	backup, err := os.ReadFile(filepath.Join(root, stateDirectory, "state.json.v2.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != original {
		t.Fatal("backup does not hold the original snapshot")
	}
}

// TestMigrateWalksEveryVersionInOneStep proves a snapshot left behind by M1
// still reaches the current schema: the user who skipped a release migrates
// once, not once per version they missed.
func TestMigrateWalksEveryVersionInOneStep(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	legacyState(t, root)
	if migrated, err := Migrate(root); err != nil || !migrated {
		t.Fatalf("Migrate = %v, %v", migrated, err)
	}
	state, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if state.SchemaVersion != work.SchemaVersion || state.NextEvidenceID != 1 || state.NextGateID != 1 {
		t.Fatalf("v1 snapshot did not reach the current schema: %#v", state)
	}
	if len(state.WorkItems) != 2 {
		t.Fatalf("migration lost work items: %#v", state.WorkItems)
	}
}

// TestMigrateRefusesToDiscardWhatAStepWouldCreate guards the one way a version
// header can lie: it is a claim about the file, and a hand-edited header on a
// newer snapshot would otherwise send the migration through a step that empties
// containers the file already fills.
func TestMigrateRefusesToDiscardWhatAStepWouldCreate(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	// A current snapshot carrying evidence, with its version header rewound.
	v2State(t, root)
	if migrated, err := Migrate(root); err != nil || !migrated {
		t.Fatalf("Migrate = %v, %v", migrated, err)
	}
	path := filepath.Join(root, stateDirectory, "state.json")
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rewound := strings.Replace(string(current), `"schema_version": 18`, `"schema_version": 1`, 1)
	if rewound == string(current) {
		t.Fatalf("failed to rewind the version header of %s", current)
	}
	if err := os.WriteFile(path, []byte(rewound), 0600); err != nil {
		t.Fatal(err)
	}
	// The v1 backup from the first migration is gone, so nothing blocks on that.
	if err := os.Remove(filepath.Join(root, stateDirectory, "state.json.v2.bak")); err != nil {
		t.Fatal(err)
	}

	if _, err := Migrate(root); err == nil {
		t.Fatal("migrated a snapshot whose header understated what it carries")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != rewound {
		t.Fatal("a refused migration still modified the state")
	}
}

// v3State writes an M3-era snapshot: evidence and gates both populated, so a
// migration that dropped either would be caught rather than merely suspected.
func v3State(t *testing.T, root string) string {
	t.Helper()
	canonical, err := canonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	contents := `{"schema_version":3,"next_work_id":3,"next_evidence_id":3,"next_gate_id":2,` +
		`"goals":[{"id":"g","title":"Goal","description":"","repository":"` + canonical +
		`","status":"ACTIVE","reason":"","created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"}],` +
		`"work_items":[{"id":"WI-001","goal_id":"g","story_ref":"specs/stories/a","status":"REVIEW","depends_on":null,` +
		`"current_run":null,"created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"},` +
		`{"id":"WI-002","goal_id":"g","story_ref":"specs/stories/b","status":"PENDING","depends_on":["WI-001"],` +
		`"current_run":null,"created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"}],` +
		`"evidence":[{"id":"EV-001","type":"verification","repository":"` + canonical +
		`","work_item_id":"WI-001","story_ref":"specs/stories/a","revision":"abc123","command":"make verify",` +
		`"exit_code":0,"result":"PASS","reviewer":"","note":"","created_at":"2026-09-07T00:00:00Z"},` +
		`{"id":"EV-002","type":"review","repository":"` + canonical +
		`","work_item_id":"WI-001","story_ref":"specs/stories/a","revision":"abc123","command":"",` +
		`"exit_code":null,"result":"REJECTED","reviewer":"carl@example.com","note":"not yet",` +
		`"created_at":"2026-09-07T00:00:00Z"}],` +
		`"gates":[{"id":"GATE-001","work_item_id":"WI-001","question":"which?","rationale":"",` +
		`"options":["a","b"],"status":"RESOLVED","choice":"a","note":"","reason":"",` +
		`"decided_by":"carl@example.com","opened_at":"2026-09-07T00:00:00Z",` +
		`"decided_at":"2026-09-07T00:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(root, stateDirectory, "state.json"), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return contents
}

// TestMigrateUpgradesV3StateAndKeepsEverything covers the step that changes no
// data at all. The ceremony still has to happen: the contract the user works to
// is "upgrading means a backup and a version bump", not "sometimes there is a
// backup".
func TestMigrateUpgradesV3StateAndKeepsEverything(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	original := v3State(t, root)

	if _, err := Load(root); err == nil {
		t.Fatal("read a v3 state without migrating")
	} else if !strings.Contains(err.Error(), "migrate") {
		t.Fatalf("error %q does not tell the user to migrate", err)
	}

	migrated, err := Migrate(root)
	if err != nil || !migrated {
		t.Fatalf("Migrate = %v, %v", migrated, err)
	}
	state, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if state.SchemaVersion != work.SchemaVersion {
		t.Fatalf("migration did not reach the current schema: %#v", state)
	}
	if state.NextWorkID != 3 || state.NextEvidenceID != 3 || state.NextGateID != 2 {
		t.Fatalf("migration lost counters: %#v", state)
	}
	if len(state.Goals) != 1 || len(state.WorkItems) != 2 || len(state.Evidence) != 2 || len(state.Gates) != 1 {
		t.Fatalf("migration lost data: %#v", state)
	}
	if got := state.Evidence[1]; got.Result != work.Rejected || got.Reviewer != "carl@example.com" || got.PR != "" {
		t.Fatalf("migration changed review evidence: %#v", got)
	}
	if got := state.Gates[0]; got.ID != "GATE-001" || got.Choice != "a" {
		t.Fatalf("migration changed gates: %#v", got)
	}

	backup, err := os.ReadFile(filepath.Join(root, stateDirectory, "state.json.v3.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != original {
		t.Fatal("backup does not hold the original snapshot")
	}

	// Running it again is safe and says so, so nobody has to remember whether
	// they already upgraded.
	if migrated, err := Migrate(root); err != nil || migrated {
		t.Fatalf("second Migrate = %v, %v", migrated, err)
	}
}

// v4State writes an M4-era snapshot: a resolved review carries a PR reference,
// and one Work Item has a Verification Run in flight without a log path, so a
// migration that dropped either the PR or the in-flight run would be caught.
func v4State(t *testing.T, root string) string {
	t.Helper()
	canonical, err := canonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	contents := `{"schema_version":4,"next_work_id":3,"next_evidence_id":3,"next_gate_id":2,` +
		`"goals":[{"id":"g","title":"Goal","description":"","repository":"` + canonical +
		`","status":"ACTIVE","reason":"","created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"}],` +
		`"work_items":[{"id":"WI-001","goal_id":"g","story_ref":"specs/stories/a","status":"REVIEW","depends_on":null,` +
		`"current_run":null,"created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"},` +
		`{"id":"WI-002","goal_id":"g","story_ref":"specs/stories/b","status":"VERIFYING","depends_on":["WI-001"],` +
		`"current_run":{"revision":"def456","worktree_path":"` + filepath.Join(root, ".forgepilot", "worktrees", "WI-002-def456") +
		`","started_at":"2026-09-07T00:00:00Z"},"created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"}],` +
		`"evidence":[{"id":"EV-001","type":"verification","repository":"` + canonical +
		`","work_item_id":"WI-001","story_ref":"specs/stories/a","revision":"abc123","command":"make verify",` +
		`"exit_code":0,"result":"PASS","reviewer":"","note":"","pr":"","created_at":"2026-09-07T00:00:00Z"},` +
		`{"id":"EV-002","type":"review","repository":"` + canonical +
		`","work_item_id":"WI-001","story_ref":"specs/stories/a","revision":"abc123","command":"",` +
		`"exit_code":null,"result":"APPROVED","reviewer":"carl@example.com","note":"looks good",` +
		`"pr":"CarlLee1983/ForgePilot#42","created_at":"2026-09-07T00:00:00Z"}],` +
		`"gates":[{"id":"GATE-001","work_item_id":"WI-001","question":"which?","rationale":"",` +
		`"options":["a","b"],"status":"RESOLVED","choice":"a","note":"","reason":"",` +
		`"decided_by":"carl@example.com","opened_at":"2026-09-07T00:00:00Z",` +
		`"decided_at":"2026-09-07T00:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(root, stateDirectory, "state.json"), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return contents
}

// TestMigrateUpgradesV4StateAndKeepsEverything covers the step this ticket adds:
// current_run gains a log path, but nothing existing is discarded, including a
// run that was already in flight before the log path field existed.
func TestMigrateUpgradesV4StateAndKeepsEverything(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	original := v4State(t, root)

	if _, err := Load(root); err == nil {
		t.Fatal("read a v4 state without migrating")
	} else if !strings.Contains(err.Error(), "migrate") {
		t.Fatalf("error %q does not tell the user to migrate", err)
	}

	migrated, err := Migrate(root)
	if err != nil || !migrated {
		t.Fatalf("Migrate = %v, %v", migrated, err)
	}
	state, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if state.SchemaVersion != work.SchemaVersion {
		t.Fatalf("migration did not reach the current schema: %#v", state)
	}
	if state.NextWorkID != 3 || state.NextEvidenceID != 3 || state.NextGateID != 2 {
		t.Fatalf("migration lost counters: %#v", state)
	}
	if len(state.Goals) != 1 || len(state.WorkItems) != 2 || len(state.Evidence) != 2 || len(state.Gates) != 1 {
		t.Fatalf("migration lost data: %#v", state)
	}
	if got := state.Evidence[1]; got.Result != work.Approved || got.PR != "CarlLee1983/ForgePilot#42" {
		t.Fatalf("migration changed review evidence: %#v", got)
	}
	// A run already in flight before v5 existed has no log path recorded, and
	// migration must not invent one: an empty LogPath is the honest fact that
	// this run's output was never streamed anywhere.
	run := state.WorkItems[1].CurrentRun
	if run == nil || run.Revision != "def456" || run.LogPath != "" {
		t.Fatalf("migration changed the in-flight run: %#v", run)
	}
	if got := state.Gates[0]; got.ID != "GATE-001" || got.Choice != "a" {
		t.Fatalf("migration changed gates: %#v", got)
	}

	backup, err := os.ReadFile(filepath.Join(root, stateDirectory, "state.json.v4.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != original {
		t.Fatal("backup does not hold the original snapshot")
	}

	// Running it again is safe and says so, so nobody has to remember whether
	// they already upgraded.
	if migrated, err := Migrate(root); err != nil || migrated {
		t.Fatalf("second Migrate = %v, %v", migrated, err)
	}
}

// v5State writes the immediately previous schema. Candidate identity did not
// exist yet, so migration must explicitly preserve every historical revision as
// a COMMIT candidate rather than leaving its meaning implicit.
func v5State(t *testing.T, root string) string {
	t.Helper()
	canonical, err := canonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	contents := `{"schema_version":5,"next_work_id":3,"next_evidence_id":3,"next_gate_id":1,` +
		`"goals":[{"id":"g","title":"Goal","description":"","repository":"` + canonical +
		`","status":"ACTIVE","reason":"","created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"}],` +
		`"work_items":[{"id":"WI-001","goal_id":"g","story_ref":"specs/stories/a","status":"REVIEW","depends_on":null,` +
		`"current_run":null,"created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"},` +
		`{"id":"WI-002","goal_id":"g","story_ref":"specs/stories/b","status":"VERIFYING","depends_on":null,` +
		`"current_run":{"revision":"def456","worktree_path":"` + filepath.Join(root, ".forgepilot", "worktrees", "WI-002-def456") +
		`","log_path":"` + filepath.Join(root, ".forgepilot", "logs", "WI-002-def456.log") +
		`","started_at":"2026-09-07T00:00:00Z"},"created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"}],` +
		`"evidence":[{"id":"EV-001","type":"verification","repository":"` + canonical +
		`","work_item_id":"WI-001","story_ref":"specs/stories/a","revision":"abc123","command":"make verify",` +
		`"exit_code":0,"result":"PASS","reviewer":"","note":"","pr":"","created_at":"2026-09-07T00:00:00Z"},` +
		`{"id":"EV-002","type":"review","repository":"` + canonical +
		`","work_item_id":"WI-001","story_ref":"specs/stories/a","revision":"abc123","command":"",` +
		`"exit_code":null,"result":"APPROVED","reviewer":"carl@example.com","note":"looks good",` +
		`"pr":"","created_at":"2026-09-07T00:00:00Z"}],"gates":[]}`
	if err := os.WriteFile(filepath.Join(root, stateDirectory, "state.json"), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return contents
}

func TestMigrateUpgradesV5RevisionsToExplicitCommitCandidates(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	original := v5State(t, root)

	migrated, err := Migrate(root)
	if err != nil || !migrated {
		t.Fatalf("Migrate = %v, %v", migrated, err)
	}
	state, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, evidence := range state.Evidence {
		if evidence.CandidateKind != work.CommitCandidate || evidence.BaseRevision != "" || evidence.CandidateDigest != "" {
			t.Fatalf("migration did not make evidence candidate explicit: %#v", evidence)
		}
	}
	run := state.WorkItems[1].CurrentRun
	if run == nil || run.CandidateKind != work.CommitCandidate || run.Revision != "def456" || run.BaseRevision != "" || run.CandidateDigest != "" {
		t.Fatalf("migration did not make current run candidate explicit: %#v", run)
	}
	backup, err := os.ReadFile(filepath.Join(root, stateDirectory, "state.json.v5.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != original {
		t.Fatal("backup does not hold the original v5 snapshot")
	}
}

// v6State is the immediately previous schema. Runtime metadata did not exist,
// so its absent runtime fields must remain a valid, honest historical record.
func v6State(t *testing.T, root string) string {
	t.Helper()
	contents := v5State(t, root)
	contents = strings.Replace(contents, `"schema_version":5`, `"schema_version":6`, 1)
	contents = strings.ReplaceAll(contents, `"revision":"abc123","command"`, `"revision":"abc123","candidate_kind":"COMMIT","base_revision":"","candidate_digest":"","command"`)
	contents = strings.Replace(contents, `"revision":"def456","worktree_path"`, `"revision":"def456","candidate_kind":"COMMIT","base_revision":"","candidate_digest":"","worktree_path"`, 1)
	if err := os.WriteFile(filepath.Join(root, stateDirectory, "state.json"), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return contents
}

func TestMigrateUpgradesV6WithoutInventingRuntimeMetadata(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	original := v6State(t, root)
	if _, err := Load(root); err == nil {
		t.Fatal("read a v6 state without migrating")
	}
	migrated, err := Migrate(root)
	if err != nil || !migrated {
		t.Fatalf("Migrate = %v, %v", migrated, err)
	}
	state, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if state.SchemaVersion != work.SchemaVersion {
		t.Fatalf("schema version = %d, want %d", state.SchemaVersion, work.SchemaVersion)
	}
	for _, evidence := range state.Evidence {
		if evidence.Runtime != nil {
			t.Fatalf("migration invented evidence runtime: %#v", evidence)
		}
	}
	run := state.WorkItems[1].CurrentRun
	if run == nil || run.Runtime != nil {
		t.Fatalf("migration changed in-flight run runtime: %#v", run)
	}
	backup, err := os.ReadFile(filepath.Join(root, stateDirectory, "state.json.v6.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != original {
		t.Fatal("backup does not hold the original v6 snapshot")
	}
}

// v7State writes the schema immediately before review policy became mandatory.
// Goals had no review_policy field then, so migration must make its historic
// per-Work-Item behavior explicit.
func v7State(t *testing.T, root string) string {
	t.Helper()
	contents := v6State(t, root)
	contents = strings.Replace(contents, `"schema_version":6`, `"schema_version":7`, 1)
	canonical, err := canonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	contents = strings.Replace(contents,
		`],"work_items"`,
		`,{"id":"h","title":"Second Goal","description":"","repository":"`+canonical+`","status":"ACTIVE","reason":"","created_at":"2026-09-07T00:00:00Z","updated_at":"2026-09-07T00:00:00Z"}],"work_items"`,
		1,
	)
	if err := os.WriteFile(filepath.Join(root, stateDirectory, "state.json"), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return contents
}

func TestMigrateUpgradesV7GoalsToPerWorkItemReviewPolicy(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	original := v7State(t, root)

	if _, err := Load(root); err == nil {
		t.Fatal("read a v7 state without migrating")
	}
	if migrated, err := Migrate(root); err != nil || !migrated {
		t.Fatalf("Migrate = %v, %v", migrated, err)
	}
	state, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, goal := range state.Goals {
		if goal.ReviewPolicy != work.ReviewPerWorkItem {
			t.Fatalf("migrated Goal %q review policy = %q, want %q", goal.ID, goal.ReviewPolicy, work.ReviewPerWorkItem)
		}
	}
	backup, err := os.ReadFile(filepath.Join(root, stateDirectory, "state.json.v7.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != original {
		t.Fatal("backup does not hold the original v7 snapshot")
	}
}

func TestUpgradeV8AssignsDistinctLegacyRunIDs(t *testing.T) {
	state, err := upgrade([]byte(v8VerificationState(t)), 8)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Evidence[0].VerificationRunID; got != "LVR-001" {
		t.Fatalf("legacy Evidence run ID = %q, want LVR-001", got)
	}
	if got := state.WorkItems[0].CurrentRun.VerificationRunID; got != "LVR-002" {
		t.Fatalf("legacy CurrentRun ID = %q, want LVR-002", got)
	}
	if state.NextVerificationRunID != 1 {
		t.Fatalf("next canonical run ID = %d, want 1", state.NextVerificationRunID)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("upgraded state is invalid: %v", err)
	}
}

func TestUpgradeV9AddsEmptyExternalReferences(t *testing.T) {
	now := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	state := work.NewState()
	state.SchemaVersion = 9
	if err := state.AddGoal("g", "Goal", "", "/repo", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.AddWork("g", "specs/stories/a", nil, now); err != nil {
		t.Fatal(err)
	}
	contents, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	contents = stripV11Fields(t, contents)
	contents = bytes.Replace(contents, []byte(`,"external_ref":""`), nil, 1)
	if bytes.Contains(contents, []byte(`"external_ref"`)) {
		t.Fatalf("failed to construct field-absent v9 fixture: %s", contents)
	}

	upgraded, err := upgrade(contents, 9)
	if err != nil {
		t.Fatalf("upgrade = %v", err)
	}
	if upgraded.SchemaVersion != 18 {
		t.Fatalf("schema version = %d, want 18", upgraded.SchemaVersion)
	}
	if got := upgraded.WorkItems[0].ExternalRef; got != "" {
		t.Fatalf("migrated external reference = %q, want empty", got)
	}

	state.WorkItems[0].ExternalRef = "PB-001"
	contents, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	contents = stripV11Fields(t, contents)
	if _, err := upgrade(contents, 9); err == nil || !strings.Contains(err.Error(), "already carries an external reference") {
		t.Fatalf("upgrade understated v9 external reference = %v", err)
	}
}

func TestMigrateV9AddsEmptyExternalReferencesAndKeepsBackup(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := canonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	root = canonicalRoot
	now := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	legacy := work.NewState()
	legacy.SchemaVersion = 9
	if err := legacy.AddGoal("g", "Goal", "", root, now); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.AddWork("g", "specs/stories/a", nil, now); err != nil {
		t.Fatal(err)
	}
	contents, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	contents = stripV11Fields(t, contents)
	contents = bytes.Replace(contents, []byte(`,"external_ref":""`), nil, 1)
	if err := os.WriteFile(filepath.Join(root, stateDirectory, "state.json"), contents, 0600); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Migrate(root)
	if err != nil || !upgraded {
		t.Fatalf("Migrate = %v, %v", upgraded, err)
	}
	backup, err := os.ReadFile(filepath.Join(root, stateDirectory, "state.json.v9.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backup, contents) {
		t.Fatal("v9 backup does not preserve the original field-absent snapshot")
	}
	state, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if state.SchemaVersion != work.SchemaVersion || state.WorkItems[0].ExternalRef != "" {
		t.Fatalf("migrated state = %#v", state)
	}
}

func TestUpgradeV8RefusesPreexistingRunIdentity(t *testing.T) {
	cases := map[string]func(*work.State){
		"malformed evidence ID": func(state *work.State) {
			state.Evidence[0].VerificationRunID = "not-a-run"
		},
		"reused active and settled ID": func(state *work.State) {
			state.Evidence[0].VerificationRunID = "VR-001"
			state.WorkItems[0].CurrentRun.VerificationRunID = "VR-001"
		},
		"mixed shared provenance": func(state *work.State) {
			state.Evidence[0].VerificationRunID = "VR-001"
			other := state.Evidence[0]
			other.ID, other.Revision, other.Result = "EV-002", "cccccccccccccccccccccccccccccccccccccccc", work.Fail
			state.Evidence = append(state.Evidence, other)
			state.NextEvidenceID = 3
		},
		"in-flight run ID": func(state *work.State) {
			state.WorkItems[0].CurrentRun.VerificationRunID = "VR-001"
		},
		"run counter": func(state *work.State) {
			state.NextVerificationRunID = 2
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			var state work.State
			if err := json.Unmarshal([]byte(v8VerificationState(t)), &state); err != nil {
				t.Fatal(err)
			}
			mutate(&state)
			contents, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := upgrade([]byte(contents), 8); err == nil || !strings.Contains(err.Error(), "already carries") {
				t.Fatalf("upgrade error = %v, want pre-v9 identity refusal", err)
			}
		})
	}
}

func v8VerificationState(t *testing.T) string {
	t.Helper()
	now := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	zero := 0
	state := work.State{
		SchemaVersion: 8, NextWorkID: 2, NextEvidenceID: 2, NextGateID: 1,
		Goals: []work.Goal{{ID: "g", Title: "Goal", Repository: "/repo", Status: work.GoalActive, ReviewPolicy: work.ReviewPerWorkItem, CreatedAt: now, UpdatedAt: now}},
		WorkItems: []work.Item{{ID: "WI-001", GoalID: "g", StoryRef: "specs/stories/a", Status: work.Verifying, CurrentRun: &work.Run{
			Revision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", CandidateKind: work.CommitCandidate,
			WorktreePath: "/tmp/worktree", LogPath: "/tmp/log", StartedAt: now,
		}, CreatedAt: now, UpdatedAt: now}},
		Evidence: []work.Evidence{{
			ID: "EV-001", Type: work.VerificationEvidence, Repository: "/repo", WorkItemID: "WI-001", StoryRef: "specs/stories/a",
			Revision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CandidateKind: work.CommitCandidate,
			Command: "make verify", ExitCode: &zero, Result: work.Pass, CreatedAt: now,
		}},
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return string(stripV11Fields(t, encoded))
}

// stripV11Fields makes JSON fixtures written with today's Go structs match the
// actual snapshot shape from before schema v11 introduced these fields.
func stripV11Fields(t *testing.T, contents []byte) []byte {
	t.Helper()
	var state map[string]json.RawMessage
	if err := json.Unmarshal(contents, &state); err != nil {
		t.Fatal(err)
	}
	delete(state, "next_goal_completion_evidence_id")
	delete(state, "goal_completion_evidence")
	var goals []map[string]json.RawMessage
	if raw := state["goals"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &goals); err != nil {
			t.Fatal(err)
		}
		for _, goal := range goals {
			delete(goal, "completion_policy")
		}
		encodedGoals, err := json.Marshal(goals)
		if err != nil {
			t.Fatal(err)
		}
		state["goals"] = encodedGoals
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestMigrateRefusesV7HeaderThatUnderstatesReviewPolicy(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	v7State(t, root)
	if migrated, err := Migrate(root); err != nil || !migrated {
		t.Fatalf("Migrate = %v, %v", migrated, err)
	}
	path := filepath.Join(root, stateDirectory, "state.json")
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rewound := strings.Replace(string(current), `"schema_version": 18`, `"schema_version": 7`, 1)
	if rewound == string(current) {
		t.Fatalf("failed to rewind the version header of %s", current)
	}
	if err := os.WriteFile(path, []byte(rewound), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, stateDirectory, "state.json.v7.bak")); err != nil {
		t.Fatal(err)
	}

	if _, err := Migrate(root); err == nil {
		t.Fatal("migrated a v7 snapshot that already carries review policy")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != rewound {
		t.Fatal("a refused migration still modified the state")
	}
}
