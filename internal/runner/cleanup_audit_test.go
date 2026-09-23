package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func cleanupAuditFixture(t *testing.T) (string, Record, app.ExecutionCleanupRequest) {
	t.Helper()
	root, _, _, _, _ := newUnresolvedExecutionRunnerFixture(t)
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	authorization := state.Goals[0].Execution.Authorizations[0]
	runID, err := NewRunID(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	generation := *authorization.EngineGeneration
	record := Record{RunID: runID, Workspace: root, GoalID: "g", ExecutionAuthorizationDigest: authorization.Digest,
		EngineGeneration: &generation, RetentionAcquired: true, Attempts: map[string]int{}}
	if err := record.save(root, storage.ArtifactLimits{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	request := app.ExecutionCleanupRequest{Root: root, GoalID: "g", AnchorRunID: runID,
		AuthorizationDigest: record.ExecutionAuthorizationDigest, EngineGeneration: generation}
	return root, record, request
}

func TestExecutionCleanupAuditorRejectsForgedStoppedRunBinding(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Record)
	}{
		{"digest", func(record *Record) { record.ExecutionAuthorizationDigest = "sha256:" + strings.Repeat("c", 64) }},
		{"generation", func(record *Record) {
			record.EngineGeneration = &work.ExecutionEngineGeneration{SourceCommit: strings.Repeat("d", 40), PayloadSHA256: record.EngineGeneration.PayloadSHA256}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, anchor, request := cleanupAuditFixture(t)
			otherID, err := NewRunID(time.Now().Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			other := anchor
			other.RunID = otherID
			other.Stop = &Stop{Reason: StopGoalCompleted, At: time.Now()}
			test.mutate(&other)
			if err := other.save(root, storage.ArtifactLimits{}, time.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := NewExecutionCleanupAuditor().ConfirmExecutionCleanup(request); err == nil {
				t.Fatal("audit accepted a stopped Run bound to unknown authorization history")
			}
		})
	}
}

func TestExecutionCleanupAuditorRejectsMissingOrForgedChargedHistoricalRun(t *testing.T) {
	root, anchor, request := cleanupAuditFixture(t)
	missingID, err := NewRunID(time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Update(root, func(state *work.State) error {
		_, err := state.PrepareExecutionReservation("g", work.ExecutionReservation{
			ID: missingID + ":run", Kind: work.ExecutionReservationRun,
			RunID: missingID, CreatedAt: time.Date(2026, 9, 21, 8, 1, 0, 0, time.UTC),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewExecutionCleanupAuditor().ConfirmExecutionCleanup(request); err == nil ||
		!strings.Contains(err.Error(), "has no Run Record") {
		t.Fatalf("missing charged historical Run audit = %v; want fail-closed refusal", err)
	}
	standIn := anchor
	standIn.RunID = missingID
	standIn.Stop = &Stop{Reason: StopGoalCompleted, At: time.Now()}
	if err := standIn.save(root, storage.ArtifactLimits{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := NewExecutionCleanupAuditor().ConfirmExecutionCleanup(request); err == nil ||
		!strings.Contains(err.Error(), "does not match its Goal and RUN reservation") {
		t.Fatalf("uncharged stand-in audit = %v; want reservation refusal", err)
	}
	standIn.RunReservationID = missingID + ":run"
	if err := standIn.save(root, storage.ArtifactLimits{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := NewExecutionCleanupAuditor().ConfirmExecutionCleanup(request); err == nil ||
		!strings.Contains(err.Error(), "has no unique RUN receipt") {
		t.Fatalf("receipt-less stand-in audit = %v; want receipt refusal", err)
	}
}

func TestExecutionCleanupAuditorRequiresAFullCleanScan(t *testing.T) {
	root, record, request := cleanupAuditFixture(t)
	auditor := NewExecutionCleanupAuditor()
	proof, err := auditor.ConfirmExecutionCleanup(request)
	if err != nil || proof.AnchorRunID != record.RunID || proof.AuthorizationDigest != record.ExecutionAuthorizationDigest {
		t.Fatalf("clean proof = %#v, %v", proof, err)
	}
	otherID, err := NewRunID(time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	other := record
	other.RunID, other.GoalID = otherID, "other-goal"
	if err := other.save(root, storage.ArtifactLimits{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := auditor.ConfirmExecutionCleanup(request); err == nil {
		t.Fatal("audit accepted another live Run in the workspace")
	}
}

func TestExecutionCleanupAuditorRejectsMalformedAndUnsettledRecords(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, root string, record *Record)
	}{
		{"duplicate key", func(t *testing.T, root string, record *Record) {
			path := filepath.Join(root, ".forgepilot", "runs", record.RunID, recordName)
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			body = append(body[:len(body)-2], []byte(`,"retention_acquired":false}`+"\n")...)
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"unknown field", func(t *testing.T, root string, record *Record) {
			path := filepath.Join(root, ".forgepilot", "runs", record.RunID, recordName)
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			body = append(body[:len(body)-2], []byte(",\"unknown_owner\":true}\n")...)
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"preparation", func(t *testing.T, root string, record *Record) {
			record.RunPreparationState = RunPreparationPendingCharge
			if err := record.save(root, storage.ArtifactLimits{}, time.Now()); err != nil {
				t.Fatal(err)
			}
		}},
		{"unsettled pending", func(t *testing.T, root string, record *Record) {
			record.Pending = []PendingExecution{{ID: "pe-1", Kind: KindAgentSession, Phase: PhasePendingStart, Unresolved: true}}
			if err := record.save(root, storage.ArtifactLimits{}, time.Now()); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, record, request := cleanupAuditFixture(t)
			test.mutate(t, root, &record)
			if _, err := NewExecutionCleanupAuditor().ConfirmExecutionCleanup(request); err == nil {
				t.Fatal("audit accepted an uncertain Run Record")
			}
		})
	}
}

func TestExecutionCleanupAuditorRejectsForgedAnchorGeneration(t *testing.T) {
	_, _, request := cleanupAuditFixture(t)
	request.EngineGeneration.SourceCommit = strings.Repeat("d", 40)
	if _, err := NewExecutionCleanupAuditor().ConfirmExecutionCleanup(request); err == nil {
		t.Fatal("audit accepted a pause bound to another engine generation")
	}
}
