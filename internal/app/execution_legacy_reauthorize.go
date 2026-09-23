package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/control"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// ReauthorizeLegacyExecutionFile explicitly replaces an unlaunchable migrated
// authorization with the first acquired v2 owner. It never infers or releases
// an old marker. Any prior Run or ledger charge requires a separate recovery
// decision, so this narrow transition refuses those Goals.
func ReauthorizeLegacyExecutionFile(ctx context.Context, root, requestPath, approvalToken, approver string,
	resolver EngineGenerationResolver, auditor LegacyExecutionRunAuditor) (work.GoalExecution, error) {
	if resolver == nil || auditor == nil || strings.TrimSpace(approvalToken) == "" ||
		strings.TrimSpace(approver) == "" || approver != strings.TrimSpace(approver) {
		return work.GoalExecution{}, errors.New("legacy reauthorization requires managed resolver, Run audit, approval token and approver")
	}
	var committed work.GoalExecution
	err := storage.WithWorkspaceLock(root, func() error {
		return control.WithLock(root, func() error {
			body, err := readContainedRegularFileLimit(root, requestPath, maxExecutionRequestBytes)
			if err != nil {
				return err
			}
			request, err := parseExecutionPlanRequest(body)
			if err != nil {
				return err
			}
			state, err := storage.Load(root)
			if err != nil {
				return err
			}
			projection, err := planExecutionRevisionForState(ctx, root, body, state, time.Now().UTC())
			if err != nil {
				return err
			}
			if len(projection.Diagnostics) != 0 || projection.ApprovalToken != approvalToken || !projection.LegacyRetentionUnknown {
				return errors.New("legacy reauthorization request, approval or current authorization changed")
			}
			goal, ok := state.GoalByID(request.GoalPlanRequest.GoalID)
			if !ok || goal.Execution == nil || len(goal.Execution.Authorizations) == 0 {
				return errors.New("Goal has no current execution authorization")
			}
			old := goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1]
			if err := checkLegacyReauthorizationSafety(goal, auditor, root); err != nil {
				return err
			}
			controlState, err := control.ReadLocked(root)
			if err != nil || controlState.Pause != nil || len(controlState.Waits) != 0 || len(controlState.ExternalDeclarations) != 0 {
				return errors.New("legacy reauthorization requires readable control with no pause or wait history")
			}
			controlRevision := controlState.Revision
			generation := request.EngineGeneration.generation()
			resolved, err := resolver.Resolve(ctx)
			if err != nil {
				return err
			}
			if resolved.Generation != generation {
				return errors.New("managed ForgePilot generation does not match explicit legacy reauthorization")
			}
			draftState, err := cloneExecutionState(state)
			if err != nil {
				return err
			}
			at := time.Now().UTC()
			draft, err := applyExecutionRevision(&draftState, projection, request, approvalToken, approver, at, &generation)
			if err != nil {
				return err
			}
			reference, err := generationRetentionReference(draft.Authorizations[len(draft.Authorizations)-1], generation)
			if err != nil {
				return err
			}
			if err := (BootstrapRetention{HelperPath: resolved.HelperPath}).Acquire(ctx, generation, reference); err != nil {
				return fmt.Errorf("acquire first known authorization marker: %w", err)
			}
			return storage.Update(root, func(current *work.State) error {
				confirmed, err := resolver.Resolve(ctx)
				if err != nil || confirmed.Generation != generation || confirmed.HelperPath != resolved.HelperPath {
					return errors.New("managed ForgePilot generation changed before legacy reauthorization publication")
				}
				currentControl, err := control.ReadLocked(root)
				if err != nil || currentControl.Revision != controlRevision || currentControl.Pause != nil ||
					len(currentControl.Waits) != 0 || len(currentControl.ExternalDeclarations) != 0 {
					return errors.New("execution control changed before legacy reauthorization publication")
				}
				currentBody, err := readContainedRegularFileLimit(root, requestPath, maxExecutionRequestBytes)
				if err != nil {
					return err
				}
				currentProjection, err := planExecutionRevisionForState(ctx, root, currentBody, *current, time.Now().UTC())
				if err != nil {
					return err
				}
				if len(currentProjection.Diagnostics) != 0 || currentProjection.ApprovalToken != approvalToken ||
					!currentProjection.LegacyRetentionUnknown || currentProjection.CurrentAuthorizationDigest != old.Digest ||
					currentProjection.EngineGeneration != generation {
					return errors.New("legacy reauthorization request or prior authorization changed before publication")
				}
				currentGoal, ok := current.GoalByID(goal.ID)
				if !ok || currentGoal.Execution == nil {
					return errors.New("legacy execution Goal disappeared")
				}
				if err := checkLegacyReauthorizationSafety(currentGoal, auditor, root); err != nil {
					return err
				}
				currentRequest, err := parseExecutionPlanRequest(currentBody)
				if err != nil {
					return err
				}
				if _, err := parseExecutionExpiry(currentRequest.ExpiresAt, time.Now().UTC()); err != nil {
					return err
				}
				committed, err = applyExecutionRevision(current, currentProjection, currentRequest, approvalToken, approver, at, &generation)
				if err != nil {
					return err
				}
				actual, err := generationRetentionReference(committed.Authorizations[len(committed.Authorizations)-1], generation)
				if err != nil || actual != reference {
					return errors.New("legacy successor owner changed after marker acquisition")
				}
				return nil
			})
		})
	})
	if err != nil {
		return work.GoalExecution{}, err
	}
	return committed, nil
}

func checkLegacyReauthorizationSafety(goal work.Goal, auditor LegacyExecutionRunAuditor, root string) error {
	if goal.Execution == nil || len(goal.Execution.Authorizations) == 0 ||
		goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1].RetentionAcquired {
		return errors.New("Goal has no retention-unknown current authorization")
	}
	ledger := goal.Execution.Ledger
	if ledger.RunsConsumed != 0 || ledger.RecoveriesConsumed != 0 || ledger.StepsConsumed != 0 ||
		len(ledger.Reservations) != 0 {
		return errors.New("legacy reauthorization cannot infer ownership of charged execution history")
	}
	if err := auditor.ConfirmNoExecutionRuns(root); err != nil {
		return fmt.Errorf("legacy Run Record audit: %w", err)
	}
	return nil
}
