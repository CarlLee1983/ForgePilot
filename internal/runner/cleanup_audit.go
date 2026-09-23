package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// ExecutionCleanupAuditor applies Runner's process ownership rule to every
// record. The caller holds the workspace lock, then the execution-control lock,
// throughout this call; the adapter never acquires either lock itself.
type ExecutionCleanupAuditor struct{}

func NewExecutionCleanupAuditor() ExecutionCleanupAuditor { return ExecutionCleanupAuditor{} }

var _ app.ExecutionCleanupAuditor = ExecutionCleanupAuditor{}

func (ExecutionCleanupAuditor) ConfirmExecutionCleanup(request app.ExecutionCleanupRequest) (app.ExecutionCleanupProof, error) {
	if request.Root == "" || request.GoalID == "" || request.AnchorRunID == "" || request.AuthorizationDigest == "" {
		return app.ExecutionCleanupProof{}, fmt.Errorf("execution cleanup requires root, goal, anchor run and authorization digest")
	}
	root, err := filepath.EvalSymlinks(request.Root)
	if err != nil {
		return app.ExecutionCleanupProof{}, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return app.ExecutionCleanupProof{}, err
	}
	// Read the directory itself as well as ListRuns: ListRuns intentionally
	// ignores invalid names and non-directories, which is unsafe for an audit.
	IDs, err := cleanupRunIDs(root)
	if err != nil {
		return app.ExecutionCleanupProof{}, err
	}
	state, err := storage.Load(root)
	if err != nil {
		return app.ExecutionCleanupProof{}, fmt.Errorf("read state for full Run binding audit: %w", err)
	}
	records := make(map[string]Record, len(IDs))
	for _, runID := range IDs {
		record, err := cleanupRecord(root, runID, state)
		if err != nil {
			return app.ExecutionCleanupProof{}, err
		}
		records[runID] = record
	}
	if err := cleanupRunCoverage(state, records); err != nil {
		return app.ExecutionCleanupProof{}, err
	}
	anchorFound := false
	for _, runID := range IDs {
		record := records[runID]
		if runID == request.AnchorRunID {
			anchorFound = true
			if record.GoalID != request.GoalID || record.ExecutionAuthorizationDigest != request.AuthorizationDigest ||
				*record.EngineGeneration != request.EngineGeneration {
				return app.ExecutionCleanupProof{}, fmt.Errorf("anchor run %s does not match the requested Goal and authorization", runID)
			}
		} else if record.Stop == nil && record.RetentionClosure == nil {
			return app.ExecutionCleanupProof{}, fmt.Errorf("run %s has no terminal stop and may still resume", runID)
		}
		for _, pending := range record.UnresolvedPending() {
			if err := settleExecution(pending.Identity); err != nil {
				return app.ExecutionCleanupProof{}, fmt.Errorf("run %s pending %s cannot be settled: %w", runID, pending.ID, err)
			}
			record.resolvePending(pending.ID)
			if pending.Kind == KindAgentSession && record.Worker != nil &&
				record.Worker.WorkItemID == pending.WorkItemID && record.Worker.Identity == pending.Identity {
				record.Worker = nil
			}
			if err := record.save(root, storage.ArtifactLimits{}, time.Now()); err != nil {
				return app.ExecutionCleanupProof{}, fmt.Errorf("persist cleanup for run %s: %w", runID, err)
			}
		}
		if record.Worker != nil {
			if err := settleExecution(record.Worker.Identity); err != nil {
				return app.ExecutionCleanupProof{}, fmt.Errorf("run %s worker cannot be settled: %w", runID, err)
			}
			record.Worker = nil
			if err := record.save(root, storage.ArtifactLimits{}, time.Now()); err != nil {
				return app.ExecutionCleanupProof{}, fmt.Errorf("persist worker cleanup for run %s: %w", runID, err)
			}
		}
	}
	if !anchorFound {
		return app.ExecutionCleanupProof{}, fmt.Errorf("anchor run %s is missing", request.AnchorRunID)
	}
	// Re-read every record after writes. A proof is never based on the copies
	// modified in memory during process cleanup.
	after, err := cleanupRunIDs(root)
	if err != nil {
		return app.ExecutionCleanupProof{}, err
	}
	if len(after) != len(IDs) {
		return app.ExecutionCleanupProof{}, fmt.Errorf("Run Record set changed during cleanup audit")
	}
	state, err = storage.Load(root)
	if err != nil {
		return app.ExecutionCleanupProof{}, fmt.Errorf("re-read state for full Run binding audit: %w", err)
	}
	confirmedRecords := make(map[string]Record, len(after))
	for index, runID := range after {
		if runID != IDs[index] {
			return app.ExecutionCleanupProof{}, fmt.Errorf("Run Record set changed during cleanup audit")
		}
		record, err := cleanupRecord(root, runID, state)
		if err != nil {
			return app.ExecutionCleanupProof{}, err
		}
		confirmedRecords[runID] = record
		if record.Worker != nil || len(record.UnresolvedPending()) != 0 {
			return app.ExecutionCleanupProof{}, fmt.Errorf("run %s still has unresolved execution ownership", runID)
		}
		if runID == request.AnchorRunID {
			if record.GoalID != request.GoalID || record.ExecutionAuthorizationDigest != request.AuthorizationDigest ||
				*record.EngineGeneration != request.EngineGeneration {
				return app.ExecutionCleanupProof{}, fmt.Errorf("anchor run %s changed during cleanup audit", runID)
			}
		} else if record.Stop == nil && record.RetentionClosure == nil {
			return app.ExecutionCleanupProof{}, fmt.Errorf("run %s may still resume", runID)
		}
	}
	if err := cleanupRunCoverage(state, confirmedRecords); err != nil {
		return app.ExecutionCleanupProof{}, err
	}
	return app.ExecutionCleanupProof{GoalID: request.GoalID, AnchorRunID: request.AnchorRunID,
		AuthorizationDigest: request.AuthorizationDigest}, nil
}

func cleanupRunCoverage(state work.State, records map[string]Record) error {
	matched := make(map[string]bool)
	for _, goal := range state.Goals {
		if goal.Execution == nil {
			continue
		}
		for _, reservation := range goal.Execution.Ledger.Reservations {
			if reservation.Kind != work.ExecutionReservationRun {
				continue
			}
			record, found := records[reservation.RunID]
			if !found {
				return fmt.Errorf("charged run %s has no Run Record for cleanup audit", reservation.RunID)
			}
			if matched[reservation.RunID] || record.GoalID != goal.ID || record.RunReservationID != reservation.ID {
				return fmt.Errorf("charged run %s does not match its Goal and RUN reservation", reservation.RunID)
			}
			expectedReceipt, err := work.ExecutionReservationReceiptFor(reservation)
			if err != nil {
				return fmt.Errorf("charged run %s has invalid RUN reservation: %w", reservation.RunID, err)
			}
			receipts := 0
			for _, receipt := range record.ReservationReceipts {
				if receipt.ReservationID == reservation.ID {
					if receipt != expectedReceipt {
						return fmt.Errorf("charged run %s has a mismatched RUN receipt", reservation.RunID)
					}
					receipts++
				}
			}
			if receipts != 1 {
				return fmt.Errorf("charged run %s has no unique RUN receipt", reservation.RunID)
			}
			matched[reservation.RunID] = true
		}
	}
	for runID, record := range records {
		if record.RunReservationID != "" && !matched[runID] {
			return fmt.Errorf("run %s claims a RUN reservation absent from its Goal ledger", runID)
		}
	}
	return nil
}

func cleanupRunIDs(root string) ([]string, error) {
	path := filepath.Join(root, ".forgepilot", "runs")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		IDs, listErr := storage.ListRuns(root)
		if listErr != nil || len(IDs) != 0 {
			return nil, fmt.Errorf("missing Run Record directory disagrees with Run list")
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Run Record directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("Run Record path is not a directory")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("read Run Record directory: %w", err)
	}
	IDs, err := storage.ListRuns(root)
	if err != nil {
		return nil, err
	}
	if len(entries) != len(IDs) {
		return nil, fmt.Errorf("Run Record directory contains an unrecognized entry")
	}
	return IDs, nil
}

func cleanupRecord(root, runID string, state work.State) (Record, error) {
	contents, err := storage.ReadRunArtifact(root, runID, recordName)
	if err != nil {
		return Record{}, err
	}
	if !utf8.Valid(contents) || len(contents) > 32<<20 {
		return Record{}, fmt.Errorf("run %s is not a bounded UTF-8 Run Record", runID)
	}
	if err := checkRunJSON(bytes.NewReader(contents)); err != nil {
		return Record{}, fmt.Errorf("run %s has ambiguous JSON: %w", runID, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var strictlyRead Record
	if err := decoder.Decode(&strictlyRead); err != nil {
		return Record{}, fmt.Errorf("run %s is not readable by this engine: %w", runID, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Record{}, fmt.Errorf("run %s contains extra JSON", runID)
	}
	record, err := LoadRecord(root, runID)
	if err != nil {
		return Record{}, err
	}
	if record.RunID != runID || record.Workspace != root || record.GoalID == "" ||
		record.ExecutionAuthorizationDigest == "" || record.EngineGeneration == nil || !record.RetentionAcquired {
		return Record{}, fmt.Errorf("run %s has missing or inconsistent ownership facts", runID)
	}
	if err := work.ValidateExecutionEngineGeneration(*record.EngineGeneration); err != nil {
		return Record{}, fmt.Errorf("run %s has invalid engine generation: %w", runID, err)
	}
	goal, found := state.GoalByID(record.GoalID)
	if !found || goal.Execution == nil {
		return Record{}, fmt.Errorf("run %s names a Goal without execution authorization history", runID)
	}
	matched := false
	for _, authorization := range goal.Execution.Authorizations {
		if authorization.Digest != record.ExecutionAuthorizationDigest {
			continue
		}
		if !authorization.RetentionAcquired || authorization.EngineGeneration == nil ||
			*authorization.EngineGeneration != *record.EngineGeneration {
			return Record{}, fmt.Errorf("run %s engine does not match its historical authorization", runID)
		}
		matched = true
		break
	}
	if !matched {
		return Record{}, fmt.Errorf("run %s names an unknown historical authorization", runID)
	}
	if record.RunPreparationState != "" {
		return Record{}, fmt.Errorf("run %s has unfinished or unknown preparation %q", runID, record.RunPreparationState)
	}
	if record.RetentionClosure != nil {
		expected, eligible, err := expectedRetentionClosure(state, record)
		if err != nil || !eligible || record.RetentionClosure.At.IsZero() ||
			record.RetentionClosure.Reason != expected.Reason ||
			record.RetentionClosure.SuccessorDigest != expected.SuccessorDigest ||
			record.Worker != nil || len(record.UnresolvedPending()) != 0 {
			return Record{}, fmt.Errorf("run %s has an invalid or unclean retention closure", runID)
		}
	}
	seenPending := make(map[string]bool, len(record.Pending))
	for _, pending := range record.Pending {
		if pending.ID == "" || seenPending[pending.ID] {
			return Record{}, fmt.Errorf("run %s has missing or duplicate pending ID", runID)
		}
		seenPending[pending.ID] = true
		if pending.Unresolved && (pending.ID == "" || pending.Kind == "" ||
			(pending.Phase != PhasePendingStart && pending.Phase != PhaseRunning && pending.Phase != PhaseCleanupUnconfirmed)) {
			return Record{}, fmt.Errorf("run %s has malformed pending execution", runID)
		}
	}
	return record, nil
}

func checkRunJSON(reader *bytes.Reader) error {
	decoder := json.NewDecoder(reader)
	if err := checkRunJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("extra JSON value")
	}
	return nil
}

func checkRunJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON nesting exceeds compatibility limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delim != '{' && delim != '[' {
		return fmt.Errorf("invalid JSON container")
	}
	seen := make(map[string]bool)
	for decoder.More() {
		if delim == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return fmt.Errorf("duplicate or invalid JSON object key")
			}
			seen[name] = true
		}
		if err := checkRunJSONValue(decoder, depth+1); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}
