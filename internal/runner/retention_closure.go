package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

var _ app.ExecutionRetentionCloser = ExecutionCleanupAuditor{}
var _ app.LegacyExecutionRunAuditor = ExecutionCleanupAuditor{}

// ConfirmNoExecutionRuns refuses even an unrecognized directory entry. A
// migrated authorization cannot safely infer ownership from old Run files.
func (ExecutionCleanupAuditor) ConfirmNoExecutionRuns(root string) error {
	path := filepath.Join(root, ".forgepilot", "runs")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("legacy Run Record path is not a directory")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("legacy reauthorization requires an empty Run Record directory")
	}
	return nil
}

// CloseRetiredExecutionRuns is called with the workspace lock held. It checks
// every Run Record and ledger reservation, settles process ownership, saves
// closures, then re-reads all records before returning releaseable owners.
func (ExecutionCleanupAuditor) CloseRetiredExecutionRuns(root string) ([]app.ClosedExecutionRunOwner, error) {
	IDs, err := cleanupRunIDs(root)
	if err != nil {
		return nil, err
	}
	state, err := storage.Load(root)
	if err != nil {
		return nil, err
	}
	records := make(map[string]Record, len(IDs))
	for _, runID := range IDs {
		record, err := cleanupRecord(root, runID, state)
		if err != nil {
			return nil, err
		}
		records[runID] = record
	}
	if err := cleanupRunCoverage(state, records); err != nil {
		return nil, err
	}
	for _, runID := range IDs {
		record := records[runID]
		expected, eligible, err := expectedRetentionClosure(state, record)
		if err != nil {
			return nil, err
		}
		if !eligible || record.RetentionClosure != nil {
			continue
		}
		for _, pending := range record.UnresolvedPending() {
			if err := settleExecution(pending.Identity); err != nil {
				return nil, fmt.Errorf("run %s pending %s cannot be settled for retention closure: %w", runID, pending.ID, err)
			}
			record.resolvePending(pending.ID)
			if pending.Kind == KindAgentSession && record.Worker != nil &&
				record.Worker.WorkItemID == pending.WorkItemID && record.Worker.Identity == pending.Identity {
				record.Worker = nil
			}
		}
		if record.Worker != nil {
			if err := settleExecution(record.Worker.Identity); err != nil {
				return nil, fmt.Errorf("run %s worker cannot be settled for retention closure: %w", runID, err)
			}
			record.Worker = nil
		}
		if len(record.UnresolvedPending()) != 0 {
			return nil, fmt.Errorf("run %s still has unresolved process ownership", runID)
		}
		expected.At = time.Now().UTC()
		record.RetentionClosure = &expected
		if err := record.save(root, storage.ArtifactLimits{}, expected.At); err != nil {
			return nil, fmt.Errorf("persist run %s retention closure: %w", runID, err)
		}
	}
	after, err := cleanupRunIDs(root)
	if err != nil {
		return nil, err
	}
	if len(after) != len(IDs) {
		return nil, fmt.Errorf("Run Record set changed during retention closure")
	}
	state, err = storage.Load(root)
	if err != nil {
		return nil, err
	}
	confirmed := make(map[string]Record, len(after))
	var closed []app.ClosedExecutionRunOwner
	for index, runID := range after {
		if runID != IDs[index] {
			return nil, fmt.Errorf("Run Record set changed during retention closure")
		}
		record, err := cleanupRecord(root, runID, state)
		if err != nil {
			return nil, err
		}
		confirmed[runID] = record
		if record.RetentionClosure == nil {
			continue
		}
		if err := storage.SyncRunRecordDirectory(root, runID); err != nil {
			return nil, fmt.Errorf("confirm durable run %s retention closure: %w", runID, err)
		}
		closed = append(closed, app.ClosedExecutionRunOwner{GoalID: record.GoalID, RunID: runID,
			AuthorizationDigest: record.ExecutionAuthorizationDigest, EngineGeneration: *record.EngineGeneration,
			Reason: record.RetentionClosure.Reason, SuccessorDigest: record.RetentionClosure.SuccessorDigest})
	}
	if err := cleanupRunCoverage(state, confirmed); err != nil {
		return nil, err
	}
	return closed, nil
}

func expectedRetentionClosure(state work.State, record Record) (RetentionClosure, bool, error) {
	goal, ok := state.GoalByID(record.GoalID)
	if !ok || goal.Execution == nil {
		return RetentionClosure{}, false, fmt.Errorf("run %s has no authorization history", record.RunID)
	}
	for index, authorization := range goal.Execution.Authorizations {
		if authorization.Digest != record.ExecutionAuthorizationDigest {
			continue
		}
		if index+1 < len(goal.Execution.Authorizations) {
			return RetentionClosure{Reason: RetentionClosedBySupersession,
				SuccessorDigest: goal.Execution.Authorizations[index+1].Digest}, true, nil
		}
		if goal.Status == work.GoalCompleted || goal.Status == work.GoalCancelled {
			return RetentionClosure{Reason: RetentionClosedByGoalTerminal}, true, nil
		}
		return RetentionClosure{}, false, nil
	}
	return RetentionClosure{}, false, fmt.Errorf("run %s has no matching authorization", record.RunID)
}
