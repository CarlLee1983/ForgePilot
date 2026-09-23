package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// ExecutionRetentionResult counts successful idempotent release calls. A
// failure may leave some already-released owners; retrying is safe.
type ExecutionRetentionResult struct {
	Authorizations int `json:"authorizations"`
	Runs           int `json:"runs"`
}

// ReconcileExecutionRetention first asks Runner to durably close eligible Run
// owners, then releases only owners whose closure is still proven by state and
// the Runner's second full-record read. The workspace lock prevents a new
// Runner or revision from reopening those facts during the release pass.
func ReconcileExecutionRetention(ctx context.Context, root string, resolver EngineGenerationResolver,
	closer ExecutionRetentionCloser) (ExecutionRetentionResult, error) {
	if resolver == nil || closer == nil {
		return ExecutionRetentionResult{}, errors.New("managed resolver and Run closure auditor are required")
	}
	var result ExecutionRetentionResult
	err := storage.WithWorkspaceLock(root, func() error {
		closedRuns, err := closer.CloseRetiredExecutionRuns(root)
		if err != nil {
			return fmt.Errorf("close retired Run owners: %w", err)
		}
		state, err := storage.Load(root)
		if err != nil {
			return err
		}
		resolved, err := resolver.Resolve(ctx)
		if err != nil {
			return fmt.Errorf("resolve managed Bootstrap helper for retention release: %w", err)
		}
		retention := BootstrapRetention{HelperPath: resolved.HelperPath}
		for _, goal := range state.Goals {
			if goal.Execution == nil {
				continue
			}
			last := len(goal.Execution.Authorizations) - 1
			for index, authorization := range goal.Execution.Authorizations {
				if index == last && !terminalExecutionGoal(goal.Status) {
					continue
				}
				if !authorization.RetentionAcquired || authorization.EngineGeneration == nil {
					continue // migrated ownership is unknown, never releasable
				}
				reference, err := generationRetentionReference(authorization, *authorization.EngineGeneration)
				if err != nil {
					return err
				}
				if err := retention.Release(ctx, *authorization.EngineGeneration, reference); err != nil {
					return fmt.Errorf("release closed authorization for Goal %s revision %d: %w", goal.ID, authorization.Revision, err)
				}
				result.Authorizations++
			}
		}
		for _, run := range closedRuns {
			goal, ok := state.GoalByID(run.GoalID)
			if !ok || goal.Execution == nil {
				return fmt.Errorf("closed run %s has no execution Goal", run.RunID)
			}
			matched := false
			for index, authorization := range goal.Execution.Authorizations {
				if authorization.Digest != run.AuthorizationDigest {
					continue
				}
				if !authorization.RetentionAcquired || authorization.EngineGeneration == nil ||
					*authorization.EngineGeneration != run.EngineGeneration {
					return fmt.Errorf("closed run %s disagrees with its acquired authorization", run.RunID)
				}
				if index < len(goal.Execution.Authorizations)-1 {
					if run.Reason != "AUTHORIZATION_SUPERSEDED" ||
						run.SuccessorDigest != goal.Execution.Authorizations[index+1].Digest {
						return fmt.Errorf("closed run %s lacks its exact successor", run.RunID)
					}
				} else if !terminalExecutionGoal(goal.Status) || run.Reason != "GOAL_TERMINAL" || run.SuccessorDigest != "" {
					return fmt.Errorf("closed run %s can still resume", run.RunID)
				}
				reference, err := generationRunRetentionReference(authorization, run.RunID, run.EngineGeneration)
				if err != nil {
					return err
				}
				if err := retention.Release(ctx, run.EngineGeneration, reference); err != nil {
					return fmt.Errorf("release closed Run %s: %w", run.RunID, err)
				}
				result.Runs++
				matched = true
				break
			}
			if !matched {
				return fmt.Errorf("closed run %s has no historical authorization", run.RunID)
			}
		}
		return nil
	})
	return result, err
}

func terminalExecutionGoal(status work.GoalStatus) bool {
	return status == work.GoalCompleted || status == work.GoalCancelled
}
