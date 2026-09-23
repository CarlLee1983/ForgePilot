package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/control"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// ReviseExecutionEngineFile publishes a new authorization only after a durable
// pause, complete Run cleanup, compatibility reads and a new owner's Bootstrap
// marker. The workspace, control and state locks are always taken in that order.
func ReviseExecutionEngineFile(ctx context.Context, root, requestPath, approvalToken, approver string,
	resolver EngineGenerationResolver, auditor ExecutionCleanupAuditor) (work.GoalExecution, error) {
	if strings.TrimSpace(approvalToken) == "" || strings.TrimSpace(approver) == "" || approver != strings.TrimSpace(approver) {
		return work.GoalExecution{}, errors.New("approval token and self-declared approver are required")
	}
	if resolver == nil || auditor == nil {
		return work.GoalExecution{}, errors.New("engine revision requires managed generation resolver and full Run cleanup auditor")
	}
	var committed work.GoalExecution
	err := storage.WithWorkspaceLock(root, func() error {
		return control.WithLock(root, func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			body, err := readContainedRegularFileLimit(root, requestPath, maxExecutionRequestBytes)
			if err != nil {
				return fmt.Errorf("read execution revision request: %w", err)
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
			if len(projection.Diagnostics) != 0 {
				return fmt.Errorf("execution revision is no longer authorizable (%s): %s", projection.Diagnostics[0].Code, projection.Diagnostics[0].Message)
			}
			if projection.ApprovalToken != approvalToken {
				return errors.New("approval token is stale or does not match the exact request and current Goal state; run execution revise plan again")
			}
			goal, ok := state.GoalByID(request.GoalPlanRequest.GoalID)
			if !ok || goal.Execution == nil || len(goal.Execution.Authorizations) == 0 {
				return errors.New("Goal has no current execution authorization")
			}
			old := goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1]
			if old.EngineGeneration == nil || !old.RetentionAcquired {
				return errors.New("old execution authorization has no known engine generation")
			}
			proposed := request.EngineGeneration.generation()
			if proposed == *old.EngineGeneration {
				return errors.New("engine revision requires a changed engine generation")
			}
			controlState, err := control.ReadExactV2Locked(root)
			if err != nil {
				return err
			}
			if controlState.Pause == nil || controlState.Pause.GoalID != goal.ID || controlState.Pause.RunID == "" || controlState.Pause.WaitID != "" {
				return errors.New("engine revision requires a durable direct pause for the current Goal and anchor Run")
			}
			anchorRunID := controlState.Pause.RunID
			intent := control.EngineRevisionPause{AuthorizationDigest: old.Digest, EngineGeneration: *old.EngineGeneration}
			if controlState.Pause.EngineRevision != nil && *controlState.Pause.EngineRevision != intent {
				return errors.New("engine revision pause names a different authorization or generation")
			}
			if controlState.Pause.EngineRevision == nil {
				if err := control.UpdateLocked(root, func(sidecar *control.State) error {
					if sidecar.Pause == nil || !reflect.DeepEqual(*sidecar.Pause, *controlState.Pause) {
						return errors.New("execution pause changed before engine revision intent")
					}
					pause := *sidecar.Pause
					pause.EngineRevision = &intent
					sidecar.Pause = &pause
					return nil
				}); err != nil {
					return err
				}
				controlState, err = control.ReadExactV2Locked(root)
				if err != nil {
					return err
				}
			}
			pauseRevision := controlState.Pause.Revision
			cleanup := ExecutionCleanupRequest{Root: root, GoalID: goal.ID, AnchorRunID: anchorRunID,
				AuthorizationDigest: old.Digest, EngineGeneration: *old.EngineGeneration}
			proof, err := auditor.ConfirmExecutionCleanup(cleanup)
			if err != nil {
				return fmt.Errorf("confirm full Run cleanup: %w", err)
			}
			if proof != (ExecutionCleanupProof{GoalID: goal.ID, AnchorRunID: anchorRunID, AuthorizationDigest: old.Digest}) {
				return errors.New("full Run cleanup proof does not match paused authorization")
			}
			// These strict reads are the app-owned compatibility check. The Run
			// Record portion is supplied by the auditor's second full scan.
			compatibleState, err := storage.Load(root)
			if err != nil {
				return fmt.Errorf("engine compatibility state read: %w", err)
			}
			compatibleControl, err := control.ReadExactV2Locked(root)
			if err != nil {
				return fmt.Errorf("engine compatibility control read: %w", err)
			}
			if compatibleControl.Pause == nil || compatibleControl.Pause.Revision != pauseRevision ||
				compatibleControl.Pause.EngineRevision == nil || *compatibleControl.Pause.EngineRevision != intent {
				return errors.New("engine compatibility pause changed")
			}
			if err := compatibleState.Validate(); err != nil {
				return fmt.Errorf("engine compatibility state validation: %w", err)
			}
			// Build the exact successor on a private state copy. Its plan digest
			// depends on newly assigned Work Item IDs, so the owner reference cannot
			// be derived from the request alone.
			draftState, err := cloneExecutionState(compatibleState)
			if err != nil {
				return err
			}
			authorizedAt := time.Now().UTC()
			draft, err := applyExecutionRevision(&draftState, projection, request, approvalToken, approver, authorizedAt, &proposed)
			if err != nil {
				return err
			}
			draftAuthorization := draft.Authorizations[len(draft.Authorizations)-1]
			reference, err := generationRetentionReference(draftAuthorization, proposed)
			if err != nil {
				return err
			}
			resolved, err := resolver.Resolve(ctx)
			if err != nil {
				return fmt.Errorf("resolve candidate managed ForgePilot generation: %w", err)
			}
			if resolved.Generation != proposed {
				return errors.New("candidate managed ForgePilot generation does not match approved engine revision")
			}
			if err := (BootstrapRetention{HelperPath: resolved.HelperPath}).Acquire(ctx, proposed, reference); err != nil {
				return fmt.Errorf("acquire revised authorization generation retention: %w", err)
			}
			// Failure from here leaves an extra marker. It must never publish an
			// unretained tuple or release the old authorization prematurely.
			return storage.Update(root, func(currentState *work.State) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				confirmedCandidate, err := resolver.Resolve(ctx)
				if err != nil || confirmedCandidate.Generation != proposed || confirmedCandidate.HelperPath != resolved.HelperPath {
					return errors.New("candidate managed ForgePilot generation changed before publication")
				}
				currentControl, err := control.ReadExactV2Locked(root)
				if err != nil || currentControl.Pause == nil || currentControl.Pause.Revision != pauseRevision ||
					currentControl.Pause.EngineRevision == nil || *currentControl.Pause.EngineRevision != intent {
					return errors.New("engine revision pause changed before publication")
				}
				currentBody, err := readContainedRegularFileLimit(root, requestPath, maxExecutionRequestBytes)
				if err != nil {
					return err
				}
				currentProjection, err := planExecutionRevisionForState(ctx, root, currentBody, *currentState, time.Now().UTC())
				if err != nil {
					return err
				}
				if len(currentProjection.Diagnostics) != 0 || currentProjection.ApprovalToken != approvalToken ||
					currentProjection.EngineGeneration != proposed || currentProjection.CurrentAuthorizationDigest != old.Digest {
					return errors.New("engine revision request, authorization or approval changed before publication")
				}
				currentRequest, err := parseExecutionPlanRequest(currentBody)
				if err != nil {
					return err
				}
				if _, err := parseExecutionExpiry(currentRequest.ExpiresAt, time.Now().UTC()); err != nil {
					return fmt.Errorf("execution revision expired before publication: %w", err)
				}
				committed, err = applyExecutionRevision(currentState, currentProjection, currentRequest, approvalToken, approver,
					authorizedAt, &proposed)
				if err != nil {
					return err
				}
				actual := committed.Authorizations[len(committed.Authorizations)-1]
				actualReference, err := generationRetentionReference(actual, proposed)
				if err != nil || actualReference != reference {
					return errors.New("revised authorization owner changed after retention acquisition")
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
