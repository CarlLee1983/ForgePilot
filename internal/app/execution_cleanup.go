package app

import "github.com/CarlLee1983/ForgePilot/internal/work"

// ExecutionCleanupRequest identifies the paused authorization whose old Run
// owners must be settled before an engine-changing revision can be published.
type ExecutionCleanupRequest struct {
	Root, GoalID, AnchorRunID, AuthorizationDigest string
	EngineGeneration                               work.ExecutionEngineGeneration
}

// ExecutionCleanupProof is a fresh result from a full Run Record audit. It is
// consumed only while the workspace and execution-control locks remain held.
type ExecutionCleanupProof struct {
	GoalID, AnchorRunID, AuthorizationDigest string
}

// ExecutionCleanupAuditor keeps Run Record recovery in runner without making
// app import runner. The caller holds the workspace and control locks.
type ExecutionCleanupAuditor interface {
	ConfirmExecutionCleanup(ExecutionCleanupRequest) (ExecutionCleanupProof, error)
}

// ClosedExecutionRunOwner is returned only after Runner has durably saved and
// then re-read a Run closure under the caller's workspace lock.
type ClosedExecutionRunOwner struct {
	GoalID, RunID, AuthorizationDigest string
	EngineGeneration                   work.ExecutionEngineGeneration
	Reason, SuccessorDigest            string
}

// ExecutionRetentionCloser owns Run Record cleanup and durable closure. App
// alone derives references and calls Bootstrap after this audit returns.
type ExecutionRetentionCloser interface {
	CloseRetiredExecutionRuns(root string) ([]ClosedExecutionRunOwner, error)
}

// LegacyExecutionRunAuditor proves there are no old Run Records to infer from
// when a migrated, retention-unknown authorization receives its first pin.
type LegacyExecutionRunAuditor interface {
	ConfirmNoExecutionRuns(root string) error
}
