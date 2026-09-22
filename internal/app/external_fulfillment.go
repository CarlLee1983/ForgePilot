package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/control"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

const externalFulfillmentDeclarationVersion = "forgepilot.external-fulfillment-declaration/v1"

// RunnerWaitInput is the small, untrusted part of a validated agent
// needs_human result that must survive a Runner restart. The charged ACTION
// receipt is kept separately by the Runner and supplies the stable identity.
type RunnerWaitInput struct {
	Question      string
	Context       string
	ExternalFact  string
	ReceiptID     string
	ReceiptDigest string
}

// EnsureRunnerNeedsHumanWait records the wait and its pause in one execution
// control transaction. It is idempotent for a retried ACTION receipt, but it
// refuses a conflicting question or a changed authorization/plan binding.
func EnsureRunnerNeedsHumanWait(root, goalID, runID, workItemID string, attempt int, authorizationDigest string, input RunnerWaitInput, now time.Time) (control.Wait, error) {
	if strings.TrimSpace(goalID) == "" || strings.TrimSpace(runID) == "" || strings.TrimSpace(workItemID) == "" {
		return control.Wait{}, errors.New("needs_human wait requires Goal, Run, and Work Item IDs")
	}
	if attempt < 1 || strings.TrimSpace(input.Question) == "" {
		return control.Wait{}, errors.New("needs_human wait requires an attempt and question")
	}
	if now.IsZero() {
		return control.Wait{}, errors.New("needs_human wait requires a timestamp")
	}
	if strings.TrimSpace(authorizationDigest) == "" {
		return control.Wait{}, errors.New("needs_human wait requires the action authorization digest")
	}
	if strings.TrimSpace(input.ReceiptID) == "" || strings.TrimSpace(input.ReceiptDigest) == "" {
		return control.Wait{}, errors.New("needs_human wait requires the charged ACTION receipt")
	}
	if input.ExternalFact != "" && strings.ContainsAny(input.ExternalFact, "\r\n") {
		return control.Wait{}, errors.New("needs_human external fact is invalid")
	}
	waitID := stableWaitID(runID, workItemID, attempt, input.ReceiptID)
	var result control.Wait
	err := control.Update(root, func(controlState *control.State) error {
		state, err := storage.Load(root)
		if err != nil {
			return err
		}
		goal, ok := state.GoalByID(goalID)
		if !ok {
			return fmt.Errorf("unknown goal %q", goalID)
		}
		binding, authorization, nodeID, err := executionWaitBinding(goal, workItemID, authorizationDigest)
		if err != nil {
			return err
		}
		if err := validateNeedsHumanReceipt(*goal.Execution, goalID, runID, workItemID, nodeID, attempt, input); err != nil {
			return err
		}
		kind := control.WaitHuman
		if input.ExternalFact != "" {
			kind = control.WaitExternal
		}
		wait := control.Wait{ID: waitID, Kind: kind, GoalID: goalID, RunID: runID,
			NodeID: nodeID, PlanDigest: binding.Digest, AuthorizationRevision: uint64(authorization.Revision),
			AuthorizationDigest: authorization.Digest, Question: input.Question,
			Context: input.Context, ExpectedFact: input.ExternalFact}
		if existing, found := waitByID(*controlState, waitID); found {
			if !reflect.DeepEqual(existing, wait) {
				return fmt.Errorf("wait %q already exists with different contents", waitID)
			}
			// A durable wait is immutable. In particular, replaying Run Record
			// history after an explicit acknowledgement must not reactivate it.
			result = existing
			return nil
		}
		if controlState.Pause != nil {
			return fmt.Errorf("execution control is already paused for Goal %q Run %q", controlState.Pause.GoalID, controlState.Pause.RunID)
		}
		if err := controlState.AppendWait(wait); err != nil {
			return err
		}
		if err := controlState.SetPause(control.Pause{GoalID: goalID, RunID: runID,
			Reason: "agent requested human input", RequestedBy: "ForgePilot", RequestedAt: now.UTC(), WaitID: waitID}); err != nil {
			return err
		}
		result = wait
		return nil
	})
	return result, err
}

func validateNeedsHumanReceipt(execution work.GoalExecution, goalID, runID, workItemID, nodeID string, attempt int, input RunnerWaitInput) error {
	var reservation *work.ExecutionReservation
	for i := range execution.Ledger.Reservations {
		candidate := &execution.Ledger.Reservations[i]
		if candidate.ID == input.ReceiptID {
			reservation = candidate
			break
		}
	}
	if reservation == nil || reservation.Kind != work.ExecutionReservationAction || reservation.RunID != runID || reservation.PlanNodeRef != nodeID ||
		(reservation.ID != fmt.Sprintf("%s:action:RESUME:%s:%d", runID, nodeID, attempt) &&
			reservation.ID != fmt.Sprintf("%s:action:REPAIR:%s:%d", runID, nodeID, attempt)) {
		return fmt.Errorf("needs_human receipt %q is not the charged ACTION for Work Item %q", input.ReceiptID, workItemID)
	}
	receipt, err := work.ExecutionReservationReceiptFor(*reservation)
	if err != nil || receipt.ReservationDigest != input.ReceiptDigest {
		return fmt.Errorf("needs_human receipt %q does not match its ACTION reservation", input.ReceiptID)
	}
	for _, disposition := range execution.Ledger.NeedsHumanDispositions {
		if disposition.RunID == runID && disposition.ReservationID == receipt.ReservationID && disposition.ReservationDigest == receipt.ReservationDigest {
			return nil
		}
	}
	return fmt.Errorf("needs_human receipt %q has no durable disposition", input.ReceiptID)
}

func executionWaitBinding(goal work.Goal, workItemID, authorizationDigest string) (work.GoalPlanBinding, work.ExecutionAuthorization, string, error) {
	if goal.Execution == nil {
		return work.GoalPlanBinding{}, work.ExecutionAuthorization{}, "", fmt.Errorf("goal %q has no execution authorization for a wait", goal.ID)
	}
	var authorization *work.ExecutionAuthorization
	for i := range goal.Execution.Authorizations {
		if goal.Execution.Authorizations[i].Digest == authorizationDigest {
			authorization = &goal.Execution.Authorizations[i]
			break
		}
	}
	if authorization == nil {
		return work.GoalPlanBinding{}, work.ExecutionAuthorization{}, "", fmt.Errorf("authorization %q is not part of Goal %q history", authorizationDigest, goal.ID)
	}
	for _, binding := range goal.Execution.PlanBindings {
		if binding.Digest != authorization.PlanBindingDigest {
			continue
		}
		for _, node := range binding.Nodes {
			if node.WorkItemID == workItemID {
				return binding, *authorization, node.PlanNodeRef, nil
			}
		}
		return work.GoalPlanBinding{}, work.ExecutionAuthorization{}, "", fmt.Errorf("work item %q is not mapped by authorization %q", workItemID, authorizationDigest)
	}
	return work.GoalPlanBinding{}, work.ExecutionAuthorization{}, "", fmt.Errorf("authorization %q references an unknown plan binding", authorizationDigest)
}

func executionWaitBindingByNode(goal work.Goal, nodeID, authorizationDigest string) (work.GoalPlanBinding, work.ExecutionAuthorization, error) {
	if goal.Execution == nil {
		return work.GoalPlanBinding{}, work.ExecutionAuthorization{}, fmt.Errorf("goal %q has no execution authorization for a declaration", goal.ID)
	}
	var authorization *work.ExecutionAuthorization
	for i := range goal.Execution.Authorizations {
		if goal.Execution.Authorizations[i].Digest == authorizationDigest {
			authorization = &goal.Execution.Authorizations[i]
			break
		}
	}
	if authorization == nil {
		return work.GoalPlanBinding{}, work.ExecutionAuthorization{}, fmt.Errorf("authorization %q is not part of Goal %q history", authorizationDigest, goal.ID)
	}
	for _, binding := range goal.Execution.PlanBindings {
		if binding.Digest != authorization.PlanBindingDigest {
			continue
		}
		for _, node := range binding.Nodes {
			if node.PlanNodeRef == nodeID {
				return binding, *authorization, nil
			}
		}
		return work.GoalPlanBinding{}, work.ExecutionAuthorization{}, fmt.Errorf("plan node %q is not mapped by authorization %q", nodeID, authorizationDigest)
	}
	return work.GoalPlanBinding{}, work.ExecutionAuthorization{}, fmt.Errorf("authorization %q references an unknown plan binding", authorizationDigest)
}

// AcknowledgeExecutionResume clears a durable pause only after the current
// authorization and plan are re-read. External waits additionally require an
// exact declaration; recording that declaration never clears the pause.
func AcknowledgeExecutionResume(root, goalID, runID string) error {
	if strings.TrimSpace(goalID) == "" || strings.TrimSpace(runID) == "" {
		return errors.New("execution resume requires Goal and Run IDs")
	}
	return control.Update(root, func(controlState *control.State) error {
		pause := controlState.Pause
		if pause == nil {
			return nil
		}
		if pause.GoalID != goalID || pause.RunID != runID {
			return fmt.Errorf("execution pause belongs to Goal %q Run %q", pause.GoalID, pause.RunID)
		}
		state, err := storage.Load(root)
		if err != nil {
			return err
		}
		if err := validateExecutionResumeControl(*controlState, state, goalID, runID); err != nil {
			return err
		}
		controlState.ClearPause()
		return nil
	})
}

// ValidateExecutionResumeControl performs the read-only control preflight used
// by authorization-level successor admission. It deliberately does not clear
// the pause; the caller acknowledges only after its new Run intent and charge
// are durable under the workspace lock.
func ValidateExecutionResumeControl(root, goalID, runID string) error {
	if strings.TrimSpace(goalID) == "" || strings.TrimSpace(runID) == "" {
		return errors.New("execution resume requires Goal and Run IDs")
	}
	return control.WithLock(root, func() error {
		controlState, err := control.ReadLocked(root)
		if err != nil {
			return err
		}
		if controlState.Pause == nil {
			return nil
		}
		if controlState.Pause.GoalID != goalID || controlState.Pause.RunID != runID {
			return fmt.Errorf("execution pause belongs to Goal %q Run %q", controlState.Pause.GoalID, controlState.Pause.RunID)
		}
		state, err := storage.Load(root)
		if err != nil {
			return err
		}
		return validateExecutionResumeControl(controlState, state, goalID, runID)
	})
}

func validateExecutionResumeControl(controlState control.State, state work.State, goalID, runID string) error {
	pause := controlState.Pause
	if pause == nil {
		return nil
	}
	goal, ok := state.GoalByID(goalID)
	if !ok || goal.Execution == nil || len(goal.Execution.Authorizations) == 0 || len(goal.Execution.PlanBindings) == 0 {
		return fmt.Errorf("goal %q has no current execution authorization", goalID)
	}
	binding := goal.Execution.PlanBindings[len(goal.Execution.PlanBindings)-1]
	authorization := goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1]
	if pause.WaitID == "" {
		return nil
	}
	wait, found := waitByID(controlState, pause.WaitID)
	if !found || wait.GoalID != goalID || wait.RunID != runID {
		return fmt.Errorf("execution pause references an unknown wait %q", pause.WaitID)
	}
	if wait.PlanDigest != binding.Digest || wait.AuthorizationRevision != uint64(authorization.Revision) || wait.AuthorizationDigest != authorization.Digest {
		return errors.New("execution wait is stale after a plan or authorization change")
	}
	if wait.Kind == control.WaitExternal && !hasMatchingDeclaration(controlState, wait) {
		return fmt.Errorf("external wait %q has no matching declaration", wait.ID)
	}
	return nil
}

func waitByID(state control.State, id string) (control.Wait, bool) {
	for _, wait := range state.Waits {
		if wait.ID == id {
			return wait, true
		}
	}
	return control.Wait{}, false
}

func hasMatchingDeclaration(state control.State, wait control.Wait) bool {
	for _, declaration := range state.ExternalDeclarations {
		if declaration.WaitID == wait.ID && declaration.FactName == wait.ExpectedFact &&
			declaration.NodeID == wait.NodeID && declaration.PlanDigest == wait.PlanDigest &&
			declaration.AuthorizationRevision == wait.AuthorizationRevision &&
			declaration.AuthorizationDigest == wait.AuthorizationDigest {
			return true
		}
	}
	return false
}

// ExternalFulfillmentDeclarationRequest is the strict, user-authored input to
// `execution declare`. ForgePilot derives all binding fields from the durable
// external wait instead of trusting duplicate caller values.
type ExternalFulfillmentDeclarationRequest struct {
	FormatVersion string `json:"formatVersion"`
	GoalID        string `json:"goalId"`
	WaitID        string `json:"waitId"`
	Fact          string `json:"fact"`
	DeclaredBy    string `json:"declaredBy"`
}

// DeclareExternalFulfillmentFile validates and appends one exact declaration.
// It never resumes the Run or clears its pause.
func DeclareExternalFulfillmentFile(ctx context.Context, root, requestPath string) (control.ExternalDeclaration, error) {
	if err := ctx.Err(); err != nil {
		return control.ExternalDeclaration{}, err
	}
	body, err := readContainedRegularFileLimit(root, requestPath, 64*1024)
	if err != nil {
		return control.ExternalDeclaration{}, err
	}
	object, err := artifactObjectWithLimits(body, 64*1024, maxGoalPlanJSONDepth)
	if err != nil {
		return control.ExternalDeclaration{}, fmt.Errorf("decode external declaration: %w", err)
	}
	if err := allowed(object, "formatVersion", "goalId", "waitId", "fact", "declaredBy"); err != nil {
		return control.ExternalDeclaration{}, fmt.Errorf("decode external declaration: %w", err)
	}
	var request ExternalFulfillmentDeclarationRequest
	for key, target := range map[string]any{"formatVersion": &request.FormatVersion, "goalId": &request.GoalID, "waitId": &request.WaitID, "fact": &request.Fact, "declaredBy": &request.DeclaredBy} {
		if err := decodeRequired(object, key, target); err != nil {
			return control.ExternalDeclaration{}, fmt.Errorf("decode external declaration field %q: %w", key, err)
		}
	}
	if request.FormatVersion != externalFulfillmentDeclarationVersion || strings.TrimSpace(request.GoalID) == "" ||
		strings.TrimSpace(request.WaitID) == "" || strings.TrimSpace(request.Fact) == "" || strings.TrimSpace(request.DeclaredBy) == "" {
		return control.ExternalDeclaration{}, errors.New("external declaration requires formatVersion, goalId, waitId, fact, and declaredBy")
	}
	if strings.ContainsAny(request.Fact, "\r\n") || strings.ContainsAny(request.DeclaredBy, "\r\n") {
		return control.ExternalDeclaration{}, errors.New("external declaration fact and declaredBy may not contain line breaks")
	}
	declaration := control.ExternalDeclaration{}
	err = control.Update(root, func(state *control.State) error {
		if state.Pause == nil || state.Pause.WaitID != request.WaitID {
			return fmt.Errorf("external wait %q is not the active execution pause", request.WaitID)
		}
		wait, found := waitByID(*state, request.WaitID)
		if !found {
			return fmt.Errorf("unknown external wait %q", request.WaitID)
		}
		if wait.GoalID != request.GoalID || wait.Kind != control.WaitExternal {
			return fmt.Errorf("wait %q is not an external wait for Goal %q", request.WaitID, request.GoalID)
		}
		if wait.ExpectedFact != request.Fact {
			return fmt.Errorf("fact does not match external wait %q", request.WaitID)
		}
		workspace, err := storage.Load(root)
		if err != nil {
			return err
		}
		goal, ok := workspace.GoalByID(request.GoalID)
		if !ok {
			return fmt.Errorf("unknown goal %q", request.GoalID)
		}
		binding, authorization, err := executionWaitBindingByNode(goal, wait.NodeID, wait.AuthorizationDigest)
		if err != nil || binding.Digest != wait.PlanDigest || authorization.Revision != int(wait.AuthorizationRevision) {
			if err == nil {
				err = errors.New("external wait is stale after a plan or authorization change")
			}
			return err
		}
		declaration = control.ExternalDeclaration{ID: stableDeclarationID(request.WaitID, request.Fact, request.DeclaredBy), FactName: request.Fact,
			WaitID: wait.ID, NodeID: wait.NodeID, PlanDigest: wait.PlanDigest,
			AuthorizationRevision: wait.AuthorizationRevision, AuthorizationDigest: wait.AuthorizationDigest,
			DeclaredBy: request.DeclaredBy, DeclaredAt: time.Now().UTC()}
		for _, existing := range state.ExternalDeclarations {
			if existing.ID != declaration.ID {
				continue
			}
			if existing.FactName == declaration.FactName && existing.WaitID == declaration.WaitID && existing.NodeID == declaration.NodeID &&
				existing.PlanDigest == declaration.PlanDigest && existing.AuthorizationRevision == declaration.AuthorizationRevision &&
				existing.AuthorizationDigest == declaration.AuthorizationDigest && existing.DeclaredBy == declaration.DeclaredBy {
				declaration = existing
				return nil
			}
			return fmt.Errorf("external declaration %q already exists with different contents", declaration.ID)
		}
		return state.AppendExternalDeclaration(declaration)
	})
	return declaration, err
}

func stableWaitID(runID, workItemID string, attempt int, receiptID string) string {
	seed := fmt.Sprintf("%s\x00%s\x00%d\x00%s", runID, workItemID, attempt, receiptID)
	sum := sha256.Sum256([]byte(seed))
	return "wait-" + hex.EncodeToString(sum[:])
}

func stableDeclarationID(waitID, fact, declaredBy string) string {
	sum := sha256.Sum256([]byte(waitID + "\x00" + fact + "\x00" + declaredBy))
	return "declaration-" + hex.EncodeToString(sum[:])
}
