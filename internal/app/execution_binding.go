package app

import (
	"context"
	"fmt"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// ValidateCurrentExecutionBindings re-reads every artifact that the active
// execution authorization binds before a Runner starts another action. Older
// bindings without a manifest path are intentionally readable history but are
// not actionable: guessing their path would turn migration into authorization.
func ValidateCurrentExecutionBindings(root, goalID, expectedAuthorizationDigest string) error {
	state, err := storage.Load(root)
	if err != nil {
		return err
	}
	goal, ok := state.GoalByID(goalID)
	if !ok {
		return fmt.Errorf("unknown goal %q", goalID)
	}
	if goal.Execution == nil {
		if expectedAuthorizationDigest != "" {
			return fmt.Errorf("goal %q has no current execution authorization", goalID)
		}
		return nil
	}
	if len(goal.Execution.PlanBindings) == 0 || len(goal.Execution.Authorizations) == 0 {
		return fmt.Errorf("goal %q has inconsistent execution bindings", goalID)
	}
	binding := goal.Execution.PlanBindings[len(goal.Execution.PlanBindings)-1]
	authorization := goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1]
	if expectedAuthorizationDigest == "" || authorization.Digest != expectedAuthorizationDigest {
		return fmt.Errorf("execution authorization drifted for goal %q", goalID)
	}
	if authorization.PlanBindingDigest != binding.Digest {
		return fmt.Errorf("execution plan binding drifted for goal %q", goalID)
	}
	artifacts := []work.ExecutionArtifactBinding{binding.Manifest, binding.CoverageReview, binding.Declaration}
	artifacts = append(artifacts, binding.ReviewedSources...)
	for _, node := range binding.Nodes {
		artifacts = append(artifacts, node.ReadinessContract)
	}
	for _, artifact := range artifacts {
		if artifact.Path == "" {
			return fmt.Errorf("execution binding for goal %q has no persisted manifest path; revise the plan before continuing", goalID)
		}
		contents, err := readContainedRegularFileLimit(root, artifact.Path, maxGoalPlanArtifactBytes)
		if err != nil {
			return fmt.Errorf("execution binding drift at %q: %w", artifact.Path, err)
		}
		if digest(contents) != artifact.SHA256 {
			return fmt.Errorf("execution binding drift at %q", artifact.Path)
		}
	}
	return nil
}

// AutomaticRolloverAllowed is the non-mutating admission check for a new run
// after a bounded run cleaned up. It deliberately answers only whether a new
// RUN reservation could be useful; the normal new-run transaction still owns
// the actual charge and runtime identity check.
func AutomaticRolloverAllowed(root, goalID, expectedAuthorizationDigest string, now time.Time) (bool, error) {
	if expectedAuthorizationDigest == "" {
		return false, nil
	}
	if err := ValidateCurrentExecutionBindings(root, goalID, expectedAuthorizationDigest); err != nil {
		return false, nil
	}
	state, err := storage.Load(root)
	if err != nil {
		return false, err
	}
	goal, ok := state.GoalByID(goalID)
	if !ok || goal.Execution == nil || len(goal.Execution.Authorizations) == 0 {
		return false, nil
	}
	authorization := goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1]
	ledger := goal.Execution.Ledger
	if authorization.Digest != expectedAuthorizationDigest || !now.UTC().Before(authorization.ExpiresAt) ||
		ledger.RunsConsumed >= authorization.Caps.MaxRuns || ledger.StepsConsumed >= authorization.Caps.MaxSteps ||
		ledger.RecoveriesConsumed >= authorization.Caps.MaxRecoveries {
		return false, nil
	}
	decision, err := GoalDecision(context.Background(), root, goalID)
	if err != nil {
		return false, err
	}
	switch decision.Action.Kind {
	case work.NextActionResume, work.NextActionRepair:
		nodeRef, err := state.ExecutionPlanNodeRef(goalID, decision.Action.Item.ID)
		if err != nil {
			return false, err
		}
		for _, attempts := range ledger.NodeAttempts {
			if attempts.PlanNodeRef == nodeRef {
				return attempts.TechnicalAttempts < authorization.Caps.MaxTechnicalAttemptsPerNode, nil
			}
		}
		return false, nil
	case work.NextActionStart, work.NextActionReconcile, work.NextActionReverify, work.NextActionCompleteGoal:
		// These actions spend a Runner step but do not launch a technical
		// worker attempt, so the per-node technical-attempt cap is irrelevant.
		return true, nil
	default:
		// Waiting, completion already reached, and no-action decisions must
		// not manufacture another run merely because aggregate caps remain.
		return false, nil
	}
}
