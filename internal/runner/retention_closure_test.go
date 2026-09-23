package runner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestRetiredRunClosurePrecedesBothOwnerReleasesAndRetries(t *testing.T) {
	root, record, _ := cleanupAuditFixture(t)
	closer := NewExecutionCleanupAuditor()
	if closed, err := closer.CloseRetiredExecutionRuns(root); err != nil || len(closed) != 0 {
		t.Fatalf("active Run closed = %#v, %v", closed, err)
	}
	if err := storage.Update(root, func(state *work.State) error {
		return state.CancelGoal("g", "finished elsewhere", time.Now().UTC())
	}); err != nil {
		t.Fatal(err)
	}
	helpDir := t.TempDir()
	logPath := filepath.Join(helpDir, "released")
	helper := filepath.Join(helpDir, "bootstrap")
	script := `#!/bin/sh
[ "$1" = retention-v1 ] && [ "$2" = release ] && [ "$3" = --generation ] && [ "$5" = --payload-digest ] && [ "$7" = --reference ] || exit 9
printf '%s\n' "$8" >> '` + logPath + `'
printf '{"protocol_version":1,"result":"released","generation_id":"%s","payload_digest":"%s"}\n' "$4" "$6"
`
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	resolver := staticGenerationResolver{resolved: app.ResolvedBootstrapGeneration{
		Generation: *record.EngineGeneration, HelperPath: helper}}
	result, err := app.ReconcileExecutionRetention(t.Context(), root, resolver, closer)
	if err != nil || result.Authorizations != 1 || result.Runs != 1 {
		t.Fatalf("retention reconciliation = %#v, %v", result, err)
	}
	closed, err := LoadRecord(root, record.RunID)
	if err != nil || closed.RetentionClosure == nil || closed.RetentionClosure.Reason != RetentionClosedByGoalTerminal {
		t.Fatalf("durable Run closure = %#v, %v", closed.RetentionClosure, err)
	}
	if exactAuthorizationResumeReusable(closed, closed.ExecutionAuthorizationDigest, time.Now()) {
		t.Fatal("closed Run remained exact-resumable")
	}
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	references := strings.Fields(string(contents))
	if len(references) != 2 || references[0] == references[1] {
		t.Fatalf("authorization and Run releases = %q", contents)
	}
	result, err = app.ReconcileExecutionRetention(t.Context(), root, resolver, closer)
	if err != nil || result.Authorizations != 1 || result.Runs != 1 {
		t.Fatalf("idempotent release retry = %#v, %v", result, err)
	}
}

func TestPostRenameClosureSyncFailurePreventsReleaseUntilRetry(t *testing.T) {
	root, record, _ := cleanupAuditFixture(t)
	if err := storage.Update(root, func(state *work.State) error {
		return state.CancelGoal("g", "finished elsewhere", time.Now().UTC())
	}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	logPath, helper := filepath.Join(dir, "released"), filepath.Join(dir, "bootstrap")
	script := `#!/bin/sh
[ "$1" = retention-v1 ] && [ "$2" = release ] || exit 9
printf '%s\n' "$8" >> '` + logPath + `'
printf '{"protocol_version":1,"result":"released","generation_id":"%s","payload_digest":"%s"}\n' "$4" "$6"
`
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	resolver := staticGenerationResolver{resolved: app.ResolvedBootstrapGeneration{
		Generation: *record.EngineGeneration, HelperPath: helper}}
	disarm := storage.InjectRunDirectorySyncFailure(root, record.RunID, func() error { return errors.New("injected directory open failure") })
	defer disarm()
	if _, err := app.ReconcileExecutionRetention(t.Context(), root, resolver, NewExecutionCleanupAuditor()); err == nil ||
		!strings.Contains(err.Error(), "injected directory open failure") {
		t.Fatalf("post-rename sync failure = %v", err)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("marker released before durable closure: %v", err)
	}
	visible, err := LoadRecord(root, record.RunID)
	if err != nil || visible.RetentionClosure == nil {
		t.Fatalf("renamed but unsynced closure = %#v, %v", visible.RetentionClosure, err)
	}
	result, err := app.ReconcileExecutionRetention(t.Context(), root, resolver, NewExecutionCleanupAuditor())
	if err != nil || result.Authorizations != 1 || result.Runs != 1 {
		t.Fatalf("safe retry after explicit directory sync = %#v, %v", result, err)
	}
}

func TestUnsettledRetiredRunNeverReleasesAnOwner(t *testing.T) {
	root, record, _ := cleanupAuditFixture(t)
	if err := storage.Update(root, func(state *work.State) error {
		return state.CancelGoal("g", "finished elsewhere", time.Now().UTC())
	}); err != nil {
		t.Fatal(err)
	}
	record.Pending = []PendingExecution{{ID: "pe-1", Kind: KindAgentSession, Phase: PhasePendingStart, Unresolved: true}}
	if err := record.save(root, storage.ArtifactLimits{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := NewExecutionCleanupAuditor().CloseRetiredExecutionRuns(root); err == nil {
		t.Fatal("uncertain process ownership closed a Run")
	}
	after, err := LoadRecord(root, record.RunID)
	if err != nil || after.RetentionClosure != nil {
		t.Fatalf("uncertain Run gained closure: %#v, %v", after.RetentionClosure, err)
	}
}

func TestSupersessionClosesOldRunWithoutChangingItsBinding(t *testing.T) {
	root, record, _ := cleanupAuditFixture(t)
	if err := storage.Update(root, func(state *work.State) error {
		old := state.Goals[0].Execution
		binding := old.PlanBindings[0]
		binding.Revision++
		binding.RequestSHA256 = "sha256:" + strings.Repeat("c", 64)
		binding.Digest = ""
		authorization := old.Authorizations[0]
		authorization.Revision++
		authorization.RequestSHA256 = binding.RequestSHA256
		authorization.AuthorizedAt = authorization.AuthorizedAt.Add(time.Minute)
		authorization.ExpiresAt = authorization.ExpiresAt.Add(time.Minute)
		authorization.Digest = ""
		return state.ReviseExecution("g", binding, authorization)
	}); err != nil {
		t.Fatal(err)
	}
	closed, err := NewExecutionCleanupAuditor().CloseRetiredExecutionRuns(root)
	if err != nil || len(closed) != 1 || closed[0].Reason != RetentionClosedBySupersession ||
		closed[0].AuthorizationDigest != record.ExecutionAuthorizationDigest || closed[0].EngineGeneration != *record.EngineGeneration {
		t.Fatalf("superseded Run closure = %#v, %v", closed, err)
	}
	after, err := LoadRecord(root, record.RunID)
	if err != nil || after.RetentionClosure == nil || after.RetentionClosure.SuccessorDigest == "" ||
		after.ExecutionAuthorizationDigest != record.ExecutionAuthorizationDigest || *after.EngineGeneration != *record.EngineGeneration {
		t.Fatalf("historical Run binding changed: %#v, %v", after, err)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	successor := state.Goals[0].Execution.Authorizations[1]
	anchorID, err := NewRunID(time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	anchor := record
	anchor.RunID = anchorID
	anchor.ExecutionAuthorizationDigest = successor.Digest
	if err := anchor.save(root, storage.ArtifactLimits{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	request := app.ExecutionCleanupRequest{Root: root, GoalID: "g", AnchorRunID: anchorID,
		AuthorizationDigest: successor.Digest, EngineGeneration: *record.EngineGeneration}
	if _, err := NewExecutionCleanupAuditor().ConfirmExecutionCleanup(request); err != nil {
		t.Fatalf("closed historical Run without Stop blocked successor audit: %v", err)
	}
	live, err := currentLiveRun(root, "g")
	if err != nil || live.RunID != anchorID {
		t.Fatalf("closed historical Run displaced current live Run: %#v, %v", live, err)
	}
}

func TestGoalResumePrefersCurrentAuthorizationDespiteDelayedHistoricalClosure(t *testing.T) {
	now := time.Now().UTC()
	current := Record{RunID: "run-current", ExecutionAuthorizationDigest: "current", StartedAt: now, UpdatedAt: now}
	historical := Record{RunID: "run-historical", ExecutionAuthorizationDigest: "old", StartedAt: now.Add(-time.Hour), UpdatedAt: now.Add(time.Hour),
		RetentionClosure: &RetentionClosure{Reason: RetentionClosedBySupersession, At: now.Add(time.Hour), SuccessorDigest: "current"}}
	if preferAuthorizationRun(historical, current, "current") {
		t.Fatal("late closure of a historical Run displaced the current authorization")
	}
	if !preferAuthorizationRun(current, historical, "current") {
		t.Fatal("Goal continuation failed to select the current authorization")
	}
}

func TestLegacyRunAuditRejectsUnknownEntriesAndSymlinkedDirectory(t *testing.T) {
	root, _, _, _, _ := newUnresolvedExecutionRunnerFixture(t)
	auditor := NewExecutionCleanupAuditor()
	if err := auditor.ConfirmNoExecutionRuns(root); err != nil {
		t.Fatalf("empty Run directory audit = %v", err)
	}
	path := filepath.Join(root, ".forgepilot", "runs")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "unknown-entry"), []byte("old run"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := auditor.ConfirmNoExecutionRuns(root); err == nil {
		t.Fatal("legacy audit accepted an unknown Run directory entry")
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), path); err != nil {
		t.Fatal(err)
	}
	if err := auditor.ConfirmNoExecutionRuns(root); err == nil {
		t.Fatal("legacy audit accepted a symlinked Run directory")
	}
}
