package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func cloneExecutionState(state work.State) (work.State, error) {
	encoded, err := json.Marshal(state)
	if err != nil {
		return work.State{}, err
	}
	var copied work.State
	if err := json.Unmarshal(encoded, &copied); err != nil {
		return work.State{}, err
	}
	return copied, nil
}

func applyInitialPinnedExecution(state *work.State, projection ExecutionPlanProjection, request executionPlanRequest,
	approver string, at time.Time) (work.GoalExecution, error) {
	binding, err := executionPlanBinding(*projection.GoalPlan, request.GoalPlanRequest, projection.RequestSHA256)
	if err != nil {
		return work.GoalExecution{}, err
	}
	expiresAt, err := parseExecutionExpiry(request.ExpiresAt, at)
	if err != nil {
		return work.GoalExecution{}, err
	}
	binding.AdoptedAt = at
	generation := projection.EngineGeneration
	execution := work.GoalExecution{
		GoalID: request.GoalPlanRequest.GoalID, Workspace: projection.Workspace,
		PlanBindings: []work.GoalPlanBinding{binding},
		Authorizations: []work.ExecutionAuthorization{{
			Revision: 1, RequestSHA256: projection.RequestSHA256, ApprovalToken: projection.ApprovalToken,
			GoalID: request.GoalPlanRequest.GoalID, Workspace: projection.Workspace, Approver: approver,
			AuthorizedAt: at, ExpiresAt: expiresAt, Caps: projection.Caps, WorkerProfile: projection.WorkerProfile,
			EngineGeneration:  &generation,
			RetentionAcquired: true,
		}},
		Ledger: work.ExecutionLedger{Revision: 1, AuthorizationRevision: 1,
			NodeAttempts: make([]work.ExecutionNodeAttempts, 0, len(binding.Nodes))},
	}
	for _, node := range binding.Nodes {
		execution.Ledger.NodeAttempts = append(execution.Ledger.NodeAttempts, work.ExecutionNodeAttempts{PlanNodeRef: node.PlanNodeRef})
	}
	if err := state.AdoptInitialExecution(execution); err != nil {
		return work.GoalExecution{}, err
	}
	goal, ok := state.GoalByID(execution.GoalID)
	if !ok || goal.Execution == nil {
		return work.GoalExecution{}, errors.New("execution authorization was not adopted")
	}
	return *goal.Execution, nil
}

// AuthorizePinnedExecutionFile acquires this authorization's retention marker
// before its exact engine tuple enters the state snapshot.
func AuthorizePinnedExecutionFile(ctx context.Context, root, requestPath, approvalToken, approver string,
	resolver EngineGenerationResolver) (work.GoalExecution, error) {
	if resolver == nil || strings.TrimSpace(approvalToken) == "" || strings.TrimSpace(approver) == "" || approver != strings.TrimSpace(approver) {
		return work.GoalExecution{}, errors.New("managed resolver, approval token and self-declared approver are required")
	}
	var committed work.GoalExecution
	err := storage.WithWorkspaceLock(root, func() error {
		body, err := readContainedRegularFileLimit(root, requestPath, maxExecutionRequestBytes)
		if err != nil {
			return err
		}
		request, err := parseExecutionPlanRequest(body)
		if err != nil {
			return err
		}
		projection, err := planExecutionBytes(ctx, root, body, time.Now().UTC())
		if err != nil {
			return err
		}
		if len(projection.Diagnostics) != 0 {
			return fmt.Errorf("execution plan is no longer authorizable (%s): %s", projection.Diagnostics[0].Code, projection.Diagnostics[0].Message)
		}
		if projection.ApprovalToken != approvalToken {
			return errors.New("approval token is stale; run execution plan again")
		}
		resolved, err := resolver.Resolve(ctx)
		if err != nil {
			return fmt.Errorf("resolve managed ForgePilot generation: %w", err)
		}
		generation := request.EngineGeneration.generation()
		if resolved.Generation != generation {
			return errors.New("managed ForgePilot generation does not match approved execution request")
		}
		state, err := storage.Load(root)
		if err != nil {
			return err
		}
		draftState, err := cloneExecutionState(state)
		if err != nil {
			return err
		}
		at := time.Now().UTC()
		draft, err := applyInitialPinnedExecution(&draftState, projection, request, approver, at)
		if err != nil {
			return err
		}
		reference, err := generationRetentionReference(draft.Authorizations[0], generation)
		if err != nil {
			return err
		}
		if err := (BootstrapRetention{HelperPath: resolved.HelperPath}).Acquire(ctx, generation, reference); err != nil {
			return fmt.Errorf("acquire initial authorization generation retention: %w", err)
		}
		return storage.Update(root, func(current *work.State) error {
			confirmedCandidate, err := resolver.Resolve(ctx)
			if err != nil || confirmedCandidate.Generation != generation || confirmedCandidate.HelperPath != resolved.HelperPath {
				return errors.New("managed ForgePilot generation changed before initial authorization publication")
			}
			currentBody, err := readContainedRegularFileLimit(root, requestPath, maxExecutionRequestBytes)
			if err != nil {
				return err
			}
			currentProjection, err := planExecutionBytes(ctx, root, currentBody, time.Now().UTC())
			if err != nil {
				return err
			}
			if len(currentProjection.Diagnostics) != 0 || currentProjection.ApprovalToken != approvalToken ||
				currentProjection.EngineGeneration != generation {
				return errors.New("execution request or approval changed before publication")
			}
			currentRequest, err := parseExecutionPlanRequest(currentBody)
			if err != nil {
				return err
			}
			if _, err := parseExecutionExpiry(currentRequest.ExpiresAt, time.Now().UTC()); err != nil {
				return err
			}
			committed, err = applyInitialPinnedExecution(current, currentProjection, currentRequest, approver, at)
			if err != nil {
				return err
			}
			actualReference, err := generationRetentionReference(committed.Authorizations[0], generation)
			if err != nil || actualReference != reference {
				return errors.New("initial authorization owner changed after retention acquisition")
			}
			return nil
		})
	})
	if err != nil {
		return work.GoalExecution{}, err
	}
	return committed, nil
}

// RevisePinnedExecutionFile changes a plan, profile, caps or expiry while
// retaining the exact current engine tuple under a distinct new owner marker.
func RevisePinnedExecutionFile(ctx context.Context, root, requestPath, approvalToken, approver string,
	resolver EngineGenerationResolver) (work.GoalExecution, error) {
	if resolver == nil || strings.TrimSpace(approvalToken) == "" || strings.TrimSpace(approver) == "" || approver != strings.TrimSpace(approver) {
		return work.GoalExecution{}, errors.New("managed resolver, approval token and self-declared approver are required")
	}
	var committed work.GoalExecution
	err := storage.WithWorkspaceLock(root, func() error {
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
		if len(projection.Diagnostics) != 0 || projection.ApprovalToken != approvalToken {
			return errors.New("execution revision request or approval changed")
		}
		goal, ok := state.GoalByID(request.GoalPlanRequest.GoalID)
		if !ok || goal.Execution == nil || len(goal.Execution.Authorizations) == 0 {
			return errors.New("Goal has no current authorization")
		}
		old := goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1]
		generation := request.EngineGeneration.generation()
		if old.EngineGeneration == nil || !old.RetentionAcquired || *old.EngineGeneration != generation {
			return errors.New("engine change requires a paused engine revision")
		}
		resolved, err := resolver.Resolve(ctx)
		if err != nil {
			return err
		}
		if resolved.Generation != generation {
			return errors.New("managed ForgePilot generation drifted before ordinary revision")
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
			return err
		}
		return storage.Update(root, func(current *work.State) error {
			confirmedCandidate, err := resolver.Resolve(ctx)
			if err != nil || confirmedCandidate.Generation != generation || confirmedCandidate.HelperPath != resolved.HelperPath {
				return errors.New("managed ForgePilot generation changed before ordinary revision publication")
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
				currentProjection.CurrentAuthorizationDigest != old.Digest || currentProjection.EngineGeneration != generation {
				return errors.New("execution revision request or authorization changed before publication")
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
			actualReference, err := generationRetentionReference(committed.Authorizations[len(committed.Authorizations)-1], generation)
			if err != nil || actualReference != reference {
				return errors.New("revised authorization owner changed after retention acquisition")
			}
			return nil
		})
	})
	if err != nil {
		return work.GoalExecution{}, err
	}
	return committed, nil
}
