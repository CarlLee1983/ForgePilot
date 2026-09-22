package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// RunnerIdentity is the directly observed local runtime configuration used to
// admit a charged run. It is intentionally facts, not an Agent profile: the
// authorization owns the profile and the app layer compares it before it asks
// the domain to consume any budget.
type RunnerIdentity struct {
	Runtime          string
	ExecutablePath   string
	Version          string
	Model            string
	Effort           string
	Sandbox          string
	EngineGeneration *work.ExecutionEngineGeneration
}

// RunnerRunAdmission distinguishes the pinned legacy Runner path from a
// charged run. A nil Reservation is returned only when the Goal has no
// execution authorization; partial charged metadata is never treated as
// legacy.
type RunnerRunAdmission struct {
	AuthorizationDigest string
	Reservation         *work.ExecutionReservation
}

var errLegacyRunnerAdmission = errors.New("goal has no execution authorization")

// PrepareRunnerRun atomically chooses the legacy or charged direct-run path.
// The state transaction is the classification point: authorization cannot be
// adopted between deciding that this is legacy and preparing a charge.
func PrepareRunnerRun(root, goalID, runID string, identity RunnerIdentity, now time.Time) (RunnerRunAdmission, error) {
	return prepareRunnerRun(root, goalID, runID, nil, identity, now)
}

// PrepareRunnerRunIntent finishes the charge for a Run Record that was saved
// before admission. expectedAuthorizationDigest is the authorization observed
// when that complete intent was written; a concurrent authorization change
// must not charge a run whose durable intent names another authority.
func PrepareRunnerRunIntent(root, goalID, runID, expectedAuthorizationDigest string,
	identity RunnerIdentity, now time.Time) (RunnerRunAdmission, error) {
	return prepareRunnerRun(root, goalID, runID, &expectedAuthorizationDigest, identity, now)
}

func prepareRunnerRun(root, goalID, runID string, expectedAuthorizationDigest *string,
	identity RunnerIdentity, now time.Time) (RunnerRunAdmission, error) {
	if err := refuseUnrecordedChargedRunForIntent(root, goalID, runID); err != nil {
		return RunnerRunAdmission{}, err
	}
	var admission RunnerRunAdmission
	err := storage.Update(root, func(state *work.State) error {
		goal, ok := state.GoalByID(goalID)
		if !ok {
			return fmt.Errorf("unknown goal %q", goalID)
		}
		if goal.Execution == nil {
			if expectedAuthorizationDigest != nil && *expectedAuthorizationDigest != "" {
				return errors.New("execution authorization disappeared after the Run Record intent was saved")
			}
			return errLegacyRunnerAdmission
		}
		if len(goal.Execution.Authorizations) == 0 {
			return fmt.Errorf("goal %q has inconsistent execution authorization state", goalID)
		}
		reservation := work.ExecutionReservation{
			ID: runID + ":run", Kind: work.ExecutionReservationRun, RunID: runID, CreatedAt: now.UTC(),
		}
		if expectedAuthorizationDigest != nil && goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1].Digest != *expectedAuthorizationDigest {
			// A PENDING_CHARGE record can survive a schema migration and a later
			// explicit reauthorization. It may only replay its already-durable RUN
			// charge: no runtime validation, new reservation, or Agent launch is
			// admitted on this historical authority.
			historical := false
			for _, authorization := range goal.Execution.Authorizations {
				if authorization.Digest == *expectedAuthorizationDigest {
					historical = true
					break
				}
			}
			if !historical {
				return errors.New("execution authorization changed after the Run Record intent was saved")
			}
			stored := false
			for _, existing := range goal.Execution.Ledger.Reservations {
				if existing.ID == reservation.ID && existing.Kind == reservation.Kind && existing.RunID == reservation.RunID &&
					existing.PlanNodeRef == reservation.PlanNodeRef && existing.Status == reservation.Status {
					stored = true
					break
				}
			}
			if !stored {
				return errors.New("execution authorization changed after the Run Record intent was saved")
			}
			prepared, err := state.PrepareExecutionReservation(goalID, reservation)
			if err != nil {
				return err
			}
			admission = RunnerRunAdmission{AuthorizationDigest: *expectedAuthorizationDigest, Reservation: &prepared}
			return nil
		}
		if err := validateRunnerAuthorization(state, goalID, "", identity, now); err != nil {
			return err
		}
		prepared, err := state.PrepareExecutionReservation(goalID, reservation)
		if err != nil {
			return err
		}
		authorization := goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1]
		admission = RunnerRunAdmission{AuthorizationDigest: authorization.Digest, Reservation: &prepared}
		return nil
	})
	if errors.Is(err, errLegacyRunnerAdmission) {
		return RunnerRunAdmission{}, nil
	}
	return admission, err
}

// RunnerReservationSnapshot is the ledger-backed view of one exact run's
// reservations and its immutable plan-node mapping. Receipts copied from it are
// journal proofs; the Goal ledger remains the accounting authority.
type RunnerReservationSnapshot struct {
	Reservations           []work.ExecutionReservation
	Receipts               []work.ExecutionReservationReceipt
	NeedsHumanDispositions []work.ExecutionNeedsHumanDisposition
	PlanNodeByWorkItem     map[string]string
}

// ReconcileRunnerReservationReceipts validates every supplied receipt against
// the current ledger and returns the canonical receipt set for the run. Missing
// receipts are reconstructed from the authoritative ledger so a crash between
// charge and Run Record persistence can be repaired without charging again.
func ReconcileRunnerReservationReceipts(root, goalID, runID, runReservationID string,
	receipts []work.ExecutionReservationReceipt) (RunnerReservationSnapshot, error) {
	var snapshot RunnerReservationSnapshot
	if runID == "" || runReservationID != runID+":run" {
		return snapshot, fmt.Errorf("run %s has an invalid durable RUN reservation identity", runID)
	}
	state, err := storage.Load(root)
	if err != nil {
		return snapshot, err
	}
	goal, ok := state.GoalByID(goalID)
	if !ok || goal.Execution == nil || len(goal.Execution.PlanBindings) == 0 {
		return snapshot, fmt.Errorf("run %s has no current execution authorization ledger or plan binding", runID)
	}
	snapshot.NeedsHumanDispositions = append(snapshot.NeedsHumanDispositions,
		goal.Execution.Ledger.NeedsHumanDispositions...)
	currentBinding := goal.Execution.PlanBindings[len(goal.Execution.PlanBindings)-1]
	snapshot.PlanNodeByWorkItem = make(map[string]string, len(currentBinding.Nodes))
	for _, node := range currentBinding.Nodes {
		snapshot.PlanNodeByWorkItem[node.WorkItemID] = node.PlanNodeRef
	}

	expected := make(map[string]work.ExecutionReservationReceipt)
	runReservations := 0
	for _, reservation := range goal.Execution.Ledger.Reservations {
		if reservation.RunID != runID {
			continue
		}
		snapshot.Reservations = append(snapshot.Reservations, reservation)
		if reservation.Kind == work.ExecutionReservationRun {
			runReservations++
			if reservation.ID != runReservationID {
				return RunnerReservationSnapshot{}, fmt.Errorf("run %s has a conflicting RUN reservation", runID)
			}
		}
		receipt, err := work.ExecutionReservationReceiptFor(reservation)
		if err != nil {
			return RunnerReservationSnapshot{}, fmt.Errorf("run %s has an invalid reservation %q: %w", runID, reservation.ID, err)
		}
		expected[reservation.ID] = receipt
		snapshot.Receipts = append(snapshot.Receipts, receipt)
	}
	seen := make(map[string]bool, len(receipts))
	for _, receipt := range receipts {
		if receipt.ReservationID == "" || seen[receipt.ReservationID] {
			return RunnerReservationSnapshot{}, fmt.Errorf("run %s has an invalid or duplicate reservation receipt", runID)
		}
		seen[receipt.ReservationID] = true
		want, ok := expected[receipt.ReservationID]
		if !ok {
			return RunnerReservationSnapshot{}, fmt.Errorf("run %s receipt has no matching reservation in the current execution ledger", runID)
		}
		if receipt != want {
			return RunnerReservationSnapshot{}, fmt.Errorf("run %s reservation receipt does not match its current ledger entry", runID)
		}
	}
	if runReservations != 1 {
		return RunnerReservationSnapshot{}, fmt.Errorf("run %s has no unique matching RUN reservation in the current execution ledger", runID)
	}
	return snapshot, nil
}

// ValidateRunnerResume proves the exact run still belongs to the current
// authorization. It returns false only for an unchanged legacy Run Record and
// Goal with no charge metadata.
func ValidateRunnerResume(root, goalID, runID, authorizationDigest, runReservationID string,
	receipts []work.ExecutionReservationReceipt, artifacts work.ExecutionArtifactLimits,
	identity RunnerIdentity, now time.Time) (bool, error) {
	if err := refuseUnrecordedChargedRunForIntent(root, goalID, runID); err != nil {
		return false, err
	}
	state, err := storage.Load(root)
	if err != nil {
		return false, err
	}
	goal, ok := state.GoalByID(goalID)
	if !ok {
		return false, fmt.Errorf("unknown goal %q", goalID)
	}
	if goal.Execution == nil {
		if authorizationDigest != "" || runReservationID != "" || len(receipts) != 0 {
			return false, fmt.Errorf("run %s has charged metadata but Goal %q has no execution authorization", runID, goalID)
		}
		return false, nil
	}
	if authorizationDigest == "" || runReservationID == "" || runReservationID != runID+":run" {
		return false, fmt.Errorf("run %s is not durably bound to the current execution authorization", runID)
	}
	if goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1].Digest != authorizationDigest {
		return false, fmt.Errorf("runner is bound to a different execution authorization")
	}
	seenReceipts := make(map[string]bool, len(receipts))
	for _, receipt := range receipts {
		if receipt.ReservationID == "" || seenReceipts[receipt.ReservationID] {
			return false, fmt.Errorf("run %s has an invalid or duplicate reservation receipt", runID)
		}
		seenReceipts[receipt.ReservationID] = true
		var found bool
		for _, reservation := range goal.Execution.Ledger.Reservations {
			if reservation.ID != receipt.ReservationID {
				continue
			}
			if reservation.RunID != runID {
				return false, fmt.Errorf("run %s receipt refers to another charged run", runID)
			}
			expected, err := work.ExecutionReservationReceiptFor(reservation)
			if err != nil {
				return false, err
			}
			if expected != receipt {
				return false, fmt.Errorf("run %s reservation receipt does not match its current ledger entry", runID)
			}
			found = true
			break
		}
		if !found {
			return false, fmt.Errorf("run %s receipt has no matching reservation in the current execution ledger", runID)
		}
	}
	if err := validateExactRunnerReservationReceipts(runID, goal.Execution.Ledger.Reservations, receipts); err != nil {
		return false, err
	}
	var runReservation *work.ExecutionReservation
	for _, reservation := range goal.Execution.Ledger.Reservations {
		if reservation.ID != runReservationID {
			continue
		}
		if reservation.Kind != work.ExecutionReservationRun || reservation.RunID != runID {
			return false, fmt.Errorf("run %s reservation does not match the exact charged run", runID)
		}
		runReservation = &reservation
		break
	}
	if runReservation == nil {
		return false, fmt.Errorf("run %s has no durable run reservation in the current execution ledger", runID)
	}
	runReceipt, ok := findReservationReceipt(receipts, runReservationID)
	if !ok {
		return false, fmt.Errorf("run %s has no Run Record receipt for its charged run", runID)
	}
	expectedRunReceipt, err := work.ExecutionReservationReceiptFor(*runReservation)
	if err != nil {
		return false, err
	}
	if runReceipt != expectedRunReceipt {
		return false, fmt.Errorf("run %s Run Record receipt does not match its charged run", runID)
	}
	authorization := goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1]
	if err := validateExactRunArtifactLimits(artifacts, authorization.Caps.Artifacts); err != nil {
		return false, err
	}
	// Exact resume is still bound to the authorization and plan that admitted
	// the run. Recheck every bound artifact before the caller clears the prior
	// stop; otherwise a stale run could erase its historical MAX_* conclusion
	// and only discover the drift on its first new action.
	if err := ValidateCurrentExecutionBindings(root, goalID, authorizationDigest); err != nil {
		return false, err
	}
	if err := validateRunnerAuthorization(&state, goalID, authorizationDigest, identity, now); err != nil {
		return false, err
	}
	return true, nil
}

// ValidateRunnerArtifactLimits checks a charged Run Record before it can be
// charged or resumed. It is intentionally non-consuming so an oversized
// pre-fix pending intent cannot spend a RUN reservation on its way to refusal.
func ValidateRunnerArtifactLimits(root, goalID, authorizationDigest string, artifacts work.ExecutionArtifactLimits) error {
	state, err := storage.Load(root)
	if err != nil {
		return err
	}
	goal, ok := state.GoalByID(goalID)
	if !ok || goal.Execution == nil || len(goal.Execution.Authorizations) == 0 {
		return fmt.Errorf("goal %q has no current execution authorization", goalID)
	}
	authorization := goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1]
	if authorizationDigest == "" || authorization.Digest != authorizationDigest {
		return errors.New("runner is bound to a different execution authorization")
	}
	return validateExactRunArtifactLimits(artifacts, authorization.Caps.Artifacts)
}

// validateExactRunArtifactLimits keeps exact resume from reopening a Run whose
// historical artifact contract would exceed the current Goal authorization.
// A newly charged Run derives these caps before its Run Record is saved; this
// check closes the compatibility path for records written before that rule.
func validateExactRunArtifactLimits(recorded, authorized work.ExecutionArtifactLimits) error {
	if recorded.MaxHandoffBytes < 1 || recorded.MaxWriteBytes < 1 || recorded.MaxRunBytes < 1 || recorded.MaxTotalBytes < 1 ||
		recorded.MaxWriteBytes > recorded.MaxRunBytes || recorded.MaxRunBytes > recorded.MaxTotalBytes {
		return errors.New("exact run artifact limits are invalid")
	}
	if recorded.MaxHandoffBytes > authorized.MaxHandoffBytes ||
		recorded.MaxWriteBytes > authorized.MaxWriteBytes ||
		recorded.MaxRunBytes > authorized.MaxRunBytes ||
		recorded.MaxTotalBytes > authorized.MaxTotalBytes {
		return errors.New("exact run artifact limits exceed the current execution authorization")
	}
	return nil
}

func validateExactRunnerReservationReceipts(runID string, reservations []work.ExecutionReservation,
	receipts []work.ExecutionReservationReceipt) error {
	expected := make(map[string]work.ExecutionReservationReceipt)
	for _, reservation := range reservations {
		if reservation.RunID != runID {
			continue
		}
		receipt, err := work.ExecutionReservationReceiptFor(reservation)
		if err != nil {
			return fmt.Errorf("run %s has an invalid reservation %q: %w", runID, reservation.ID, err)
		}
		expected[reservation.ID] = receipt
	}
	seen := make(map[string]bool, len(receipts))
	for _, receipt := range receipts {
		if receipt.ReservationID == "" || seen[receipt.ReservationID] {
			return fmt.Errorf("run %s has an invalid or duplicate reservation receipt", runID)
		}
		seen[receipt.ReservationID] = true
		want, ok := expected[receipt.ReservationID]
		if !ok || receipt != want {
			return fmt.Errorf("run %s reservation receipt has no exact matching ledger entry", runID)
		}
	}
	for reservationID := range expected {
		if !seen[reservationID] {
			return fmt.Errorf("run %s has no Run Record receipt for charged reservation %q", runID, reservationID)
		}
	}
	return nil
}

func findReservationReceipt(receipts []work.ExecutionReservationReceipt, reservationID string) (work.ExecutionReservationReceipt, bool) {
	for _, receipt := range receipts {
		if receipt.ReservationID == reservationID {
			return receipt, true
		}
	}
	return work.ExecutionReservationReceipt{}, false
}

// PrepareChargedRun reserves one total-run unit before a Run Record exists.
// It is called under the state transaction lock, so concurrent direct runs
// cannot both observe the last available unit.
func PrepareChargedRun(root, goalID, runID string, identity RunnerIdentity, now time.Time) (work.ExecutionReservation, error) {
	return prepareChargedReservation(root, goalID, work.ExecutionReservation{
		ID: runID + ":run", Kind: work.ExecutionReservationRun, RunID: runID, CreatedAt: now.UTC(),
	}, identity)
}

// PrepareChargedRecovery records an exact-run recovery before a resumed run
// can launch another worker. Its ID is stable for the run, making a crash at
// this boundary charged once rather than free or double charged.
func PrepareChargedRecovery(root, goalID, runID string, identity RunnerIdentity, now time.Time) (work.ExecutionReservation, error) {
	return prepareChargedReservation(root, goalID, work.ExecutionReservation{
		ID: runID + ":recovery", Kind: work.ExecutionReservationRecovery, RunID: runID, CreatedAt: now.UTC(),
	}, identity)
}

// ValidateChargedRunAuthorization is the non-consuming half of exact-run
// resume admission. The original run reservation remains the total-run charge;
// resume must prove it still belongs to the current authorization rather than
// silently claiming another run unit.
func ValidateChargedRunAuthorization(root, goalID string, identity RunnerIdentity, now time.Time) (work.ExecutionAuthorization, error) {
	state, err := storage.Load(root)
	if err != nil {
		return work.ExecutionAuthorization{}, err
	}
	if err := validateRunnerAuthorization(&state, goalID, "", identity, now); err != nil {
		return work.ExecutionAuthorization{}, err
	}
	goal, _ := state.GoalByID(goalID)
	return goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1], nil
}

// PrepareChargedAction reserves one cumulative technical attempt. Runner step
// consumption is a separate reservation so a confirmed needs_human wait can
// spend another step without spending another technical attempt.
func PrepareChargedAction(root, goalID, runID, actionKind, workItemID string, attempt int, authorizationDigest string, identity RunnerIdentity, now time.Time) (work.ExecutionReservation, error) {
	if attempt < 1 {
		return work.ExecutionReservation{}, fmt.Errorf("technical attempt must be positive")
	}
	if actionKind != string(work.NextActionResume) && actionKind != string(work.NextActionRepair) {
		return work.ExecutionReservation{}, fmt.Errorf("unsupported charged worker action %q", actionKind)
	}
	var reservation work.ExecutionReservation
	err := storage.Update(root, func(state *work.State) error {
		node, err := state.ExecutionPlanNodeRef(goalID, workItemID)
		if err != nil {
			return err
		}
		reservation = work.ExecutionReservation{
			ID:   fmt.Sprintf("%s:action:%s:%s:%d", runID, actionKind, node, attempt),
			Kind: work.ExecutionReservationAction, RunID: runID, PlanNodeRef: node, CreatedAt: now.UTC(),
		}
		if err := validateRunnerAuthorization(state, goalID, authorizationDigest, identity, now); err != nil {
			return err
		}
		prepared, err := state.PrepareExecutionReservation(goalID, reservation)
		if err != nil {
			return err
		}
		reservation = prepared
		return nil
	})
	return reservation, err
}

// PrepareChargedWorker atomically reserves the technical attempt and the
// Runner step that will launch it. The two stable IDs allow an exact retry
// after a crash without duplicating either charge.
func PrepareChargedWorker(root, goalID, runID, actionKind, workItemID string, ordinal, attempt int,
	authorizationDigest string, identity RunnerIdentity, now time.Time) (work.ExecutionReservation, work.ExecutionReservation, error) {
	if ordinal < 1 {
		return work.ExecutionReservation{}, work.ExecutionReservation{}, fmt.Errorf("charged step ordinal must be positive")
	}
	if attempt < 1 {
		return work.ExecutionReservation{}, work.ExecutionReservation{}, fmt.Errorf("technical attempt must be positive")
	}
	if actionKind != string(work.NextActionResume) && actionKind != string(work.NextActionRepair) {
		return work.ExecutionReservation{}, work.ExecutionReservation{}, fmt.Errorf("unsupported charged worker action %q", actionKind)
	}
	var action, step work.ExecutionReservation
	err := storage.Update(root, func(state *work.State) error {
		node, err := state.ExecutionPlanNodeRef(goalID, workItemID)
		if err != nil {
			return err
		}
		if err := validateRunnerAuthorization(state, goalID, authorizationDigest, identity, now); err != nil {
			return err
		}
		actionRequest := work.ExecutionReservation{
			ID:   fmt.Sprintf("%s:action:%s:%s:%d", runID, actionKind, node, attempt),
			Kind: work.ExecutionReservationAction, RunID: runID, PlanNodeRef: node, CreatedAt: now.UTC(),
		}
		stepRequest := work.ExecutionReservation{
			ID:   fmt.Sprintf("%s:step:%d:%s:%s", runID, ordinal, actionKind, node),
			Kind: work.ExecutionReservationStep, RunID: runID, PlanNodeRef: node, CreatedAt: now.UTC(),
		}
		action, err = state.PrepareExecutionReservation(goalID, actionRequest)
		if err != nil {
			return err
		}
		step, err = state.PrepareExecutionReservation(goalID, stepRequest)
		return err
	})
	return action, step, err
}

// PrepareChargedStep reserves one total Goal step before its Runner-owned
// state transition or subprocess. It does not consume a technical attempt;
// only IMPLEMENT/REPAIR worker reservations do that.
func PrepareChargedStep(root, goalID, runID string, ordinal int, actionKind, workItemID, authorizationDigest string, identity RunnerIdentity, now time.Time) (work.ExecutionReservation, error) {
	if ordinal < 1 {
		return work.ExecutionReservation{}, fmt.Errorf("charged step ordinal must be positive")
	}
	switch actionKind {
	case string(work.NextActionStart), string(work.NextActionResume), string(work.NextActionRepair),
		string(work.NextActionReconcile), string(work.NextActionReverify), string(work.NextActionCompleteGoal):
	default:
		return work.ExecutionReservation{}, fmt.Errorf("unsupported charged Runner step %q", actionKind)
	}
	var reservation work.ExecutionReservation
	err := storage.Update(root, func(state *work.State) error {
		var node string
		if workItemID != "" {
			resolved, err := state.ExecutionPlanNodeRef(goalID, workItemID)
			if err != nil {
				return err
			}
			node = resolved
		}
		reservationID := fmt.Sprintf("%s:step:%d:%s", runID, ordinal, actionKind)
		if node != "" {
			reservationID += ":" + node
		}
		reservation = work.ExecutionReservation{
			ID: reservationID, Kind: work.ExecutionReservationStep, RunID: runID,
			PlanNodeRef: node, CreatedAt: now.UTC(),
		}
		if err := validateRunnerAuthorization(state, goalID, authorizationDigest, identity, now); err != nil {
			return err
		}
		prepared, err := state.PrepareExecutionReservation(goalID, reservation)
		if err != nil {
			return err
		}
		reservation = prepared
		return nil
	})
	return reservation, err
}

// PrepareChargedArtifactBytes records the maximum artifact allowance for one
// session immediately before the Runner launches the Agent. It is separate
// from the per-run filesystem ceiling: this Goal-owned ledger is the sole
// authority for cumulative authorization usage across runs and revisions.
func PrepareChargedArtifactBytes(root, goalID, runID, reservationID string, bytes int64,
	authorizationDigest string, identity RunnerIdentity, now time.Time) (work.ExecutionArtifactByteReservation, error) {
	reservation := work.ExecutionArtifactByteReservation{
		ID: reservationID, RunID: runID, Bytes: bytes, CreatedAt: now.UTC(),
	}
	err := storage.Update(root, func(state *work.State) error {
		if err := validateRunnerAuthorization(state, goalID, authorizationDigest, identity, now); err != nil {
			return err
		}
		prepared, err := state.PrepareExecutionArtifactByteReservation(goalID, reservation)
		if err != nil {
			return err
		}
		reservation = prepared
		return nil
	})
	return reservation, err
}

// ConfirmRunnerNeedsHuman settles an already charged ACTION only after the
// Run Record durably links the validated outcome to that exact receipt. It is
// not a new launch, so current expiry and remaining capacity do not reject it.
func ConfirmRunnerNeedsHuman(root, goalID, runID, workItemID string, attempt int,
	receipt work.ExecutionReservationReceipt) error {
	contents, err := storage.ReadRunArtifact(root, runID, "run.json")
	if err != nil {
		return fmt.Errorf("read needs_human Run Record: %w", err)
	}
	var record runnerRunRecordAudit
	if err := json.Unmarshal(contents, &record); err != nil || record.RunID != runID || record.GoalID != goalID ||
		record.ExecutionAuthorizationDigest == "" || record.RunReservationID != runID+":run" || record.RunPreparationState != "" {
		return fmt.Errorf("run %s has no valid charged Run Record for needs_human confirmation", runID)
	}
	recordedReceipt, ok := findReservationReceipt(record.ReservationReceipts, receipt.ReservationID)
	if !ok || recordedReceipt != receipt {
		return fmt.Errorf("run %s has no matching durable Run Record receipt for needs_human confirmation", runID)
	}
	matchingAttempts := 0
	for _, recordedAttempt := range record.History {
		if recordedAttempt.WorkItemID == workItemID && recordedAttempt.Number == attempt &&
			recordedAttempt.Outcome == "needs_human" && recordedAttempt.ActionReservationReceipt != nil &&
			*recordedAttempt.ActionReservationReceipt == receipt {
			matchingAttempts++
		}
	}
	if matchingAttempts != 1 {
		return fmt.Errorf("run %s does not durably confirm needs_human for ACTION receipt %q", runID, receipt.ReservationID)
	}
	return storage.Update(root, func(state *work.State) error {
		goal, ok := state.GoalByID(goalID)
		if !ok || goal.Execution == nil || !executionAuthorizationExists(goal.Execution.Authorizations, record.ExecutionAuthorizationDigest) {
			return fmt.Errorf("run %s needs_human result refers to an unknown execution authorization", runID)
		}
		nodeRef, err := state.ExecutionPlanNodeRef(goalID, workItemID)
		if err != nil {
			return err
		}
		var matched *work.ExecutionReservation
		for i := range goal.Execution.Ledger.Reservations {
			candidate := &goal.Execution.Ledger.Reservations[i]
			if candidate.ID == receipt.ReservationID {
				matched = candidate
				break
			}
		}
		if matched == nil || matched.Kind != work.ExecutionReservationAction || matched.RunID != runID || matched.PlanNodeRef != nodeRef ||
			(matched.ID != fmt.Sprintf("%s:action:RESUME:%s:%d", runID, nodeRef, attempt) &&
				matched.ID != fmt.Sprintf("%s:action:REPAIR:%s:%d", runID, nodeRef, attempt)) {
			return fmt.Errorf("run %s needs_human result does not match its charged ACTION", runID)
		}
		return state.ConfirmNeedsHumanDisposition(goalID, runID, receipt)
	})
}

func executionAuthorizationExists(authorizations []work.ExecutionAuthorization, digest string) bool {
	for _, authorization := range authorizations {
		if authorization.Digest == digest {
			return true
		}
	}
	return false
}

func prepareChargedReservation(root, goalID string, reservation work.ExecutionReservation, identity RunnerIdentity) (work.ExecutionReservation, error) {
	if reservation.Kind == work.ExecutionReservationRun {
		if err := refuseUnrecordedChargedRunForIntent(root, goalID, reservation.RunID); err != nil {
			return work.ExecutionReservation{}, err
		}
	}
	err := storage.Update(root, func(state *work.State) error {
		if err := validateRunnerAuthorization(state, goalID, "", identity, reservation.CreatedAt); err != nil {
			return err
		}
		prepared, err := state.PrepareExecutionReservation(goalID, reservation)
		if err != nil {
			return err
		}
		reservation = prepared
		return nil
	})
	return reservation, err
}

// refuseUnrecordedChargedRun audits the complete record/ledger links and does
// not admit an unfinished intent. The two-argument form is used by audits that
// must reject every pending intent.
func refuseUnrecordedChargedRun(root, goalID string) error {
	return refuseUnrecordedChargedRunForIntent(root, goalID, "")
}

// CheckRunnerRunAdmissionIntegrity refuses before Runner persists a new Run
// intent when any earlier charged run still needs exact reconciliation.
func CheckRunnerRunAdmissionIntegrity(root, goalID string) error {
	return refuseUnrecordedChargedRun(root, goalID)
}

type runnerRunRecordAudit struct {
	RunID                        string                             `json:"run_id"`
	GoalID                       string                             `json:"goal_id"`
	ExecutionAuthorizationDigest string                             `json:"execution_authorization_digest"`
	RunReservationID             string                             `json:"run_reservation_id"`
	ReservationReceipts          []work.ExecutionReservationReceipt `json:"reservation_receipts"`
	RunPreparationState          string                             `json:"run_preparation_state"`
	Steps                        int                                `json:"steps"`
	Worker                       *json.RawMessage                   `json:"worker"`
	Pending                      []json.RawMessage                  `json:"pending"`
	History                      []runnerAttemptRecordAudit         `json:"history"`
}

type runnerAttemptRecordAudit struct {
	WorkItemID               string                            `json:"work_item_id"`
	Number                   int                               `json:"number"`
	Outcome                  string                            `json:"outcome"`
	ActionReservationReceipt *work.ExecutionReservationReceipt `json:"action_reservation_receipt,omitempty"`
}

func refuseUnrecordedChargedRunForIntent(root, goalID, allowedPendingRunID string) error {
	state, err := storage.Load(root)
	if err != nil {
		return err
	}
	goal, ok := state.GoalByID(goalID)
	if !ok {
		return nil
	}
	runIDs, err := storage.ListRuns(root)
	if err != nil {
		return err
	}
	records := make(map[string]runnerRunRecordAudit, len(runIDs))
	for _, runID := range runIDs {
		contents, err := storage.ReadRunArtifact(root, runID, "run.json")
		if err != nil {
			return fmt.Errorf("run %s has no readable Run Record; refuse admission until it is reconciled: %w", runID, err)
		}
		var record runnerRunRecordAudit
		if err := json.Unmarshal(contents, &record); err != nil || record.RunID != runID {
			return fmt.Errorf("run %s has an accounting-inconsistent Run Record; refuse admission until it is reconciled", runID)
		}
		if record.RunPreparationState != "" {
			switch record.RunPreparationState {
			case "PENDING_CLASSIFICATION", "PENDING_CHARGE":
			default:
				return fmt.Errorf("run %s has an unknown Run preparation state", runID)
			}
			if runID != allowedPendingRunID || record.GoalID != goalID || record.Steps != 0 ||
				record.Worker != nil || len(record.Pending) != 0 || len(record.ReservationReceipts) != 0 {
				return fmt.Errorf("run %s has an unfinished Run preparation; recover that exact intent before new admission", runID)
			}
			if record.RunPreparationState == "PENDING_CHARGE" &&
				(record.ExecutionAuthorizationDigest == "" || record.RunReservationID != runID+":run") {
				return fmt.Errorf("run %s has an incomplete charged Run preparation intent", runID)
			}
			if record.RunPreparationState == "PENDING_CLASSIFICATION" &&
				(record.ExecutionAuthorizationDigest != "" || record.RunReservationID != "") {
				return fmt.Errorf("run %s has inconsistent Run preparation metadata", runID)
			}
		}
		records[runID] = record
	}
	if goal.Execution == nil {
		for runID, record := range records {
			if record.GoalID == goalID && (record.ExecutionAuthorizationDigest != "" || record.RunReservationID != "" ||
				len(record.ReservationReceipts) != 0 || hasNeedsHumanAttemptReceipt(record.History)) {
				return fmt.Errorf("run %s has charged metadata but Goal %q has no execution authorization", runID, goalID)
			}
		}
		return nil
	}
	if len(goal.Execution.Authorizations) == 0 {
		return fmt.Errorf("goal %q has inconsistent execution authorization state", goalID)
	}
	authorizations := make(map[string]work.ExecutionAuthorization, len(goal.Execution.Authorizations))
	for _, authorization := range goal.Execution.Authorizations {
		authorizations[authorization.Digest] = authorization
	}
	reservationsByRun := make(map[string][]work.ExecutionReservation)
	for _, reservation := range goal.Execution.Ledger.Reservations {
		reservationsByRun[reservation.RunID] = append(reservationsByRun[reservation.RunID], reservation)
	}
	for runID, record := range records {
		if record.GoalID != goalID {
			continue
		}
		if record.RunPreparationState != "" {
			if record.ExecutionAuthorizationDigest != "" {
				if _, exists := authorizations[record.ExecutionAuthorizationDigest]; !exists {
					return fmt.Errorf("run %s preparation refers to an unknown execution authorization", runID)
				}
			}
			for _, reservation := range reservationsByRun[runID] {
				if reservation.Kind != work.ExecutionReservationRun || reservation.ID != runID+":run" {
					return fmt.Errorf("run %s pending preparation has conflicting ledger reservations", runID)
				}
			}
			continue
		}
		charged := record.ExecutionAuthorizationDigest != "" || record.RunReservationID != "" || len(record.ReservationReceipts) != 0
		if !charged {
			continue
		}
		if _, exists := authorizations[record.ExecutionAuthorizationDigest]; !exists || record.RunReservationID != runID+":run" {
			return fmt.Errorf("charged run %s has an accounting-inconsistent Run Record; refuse admission until it is reconciled", runID)
		}
		found := false
		for _, reservation := range reservationsByRun[runID] {
			if reservation.Kind == work.ExecutionReservationRun && reservation.ID == record.RunReservationID {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("charged Run Record %s has no matching RUN reservation in the current execution ledger", runID)
		}
	}
	for _, reservation := range goal.Execution.Ledger.Reservations {
		if reservation.Kind != work.ExecutionReservationRun {
			continue
		}
		record, exists := records[reservation.RunID]
		if !exists {
			return fmt.Errorf("charged run %s has no readable Run Record; refuse admission until it is reconciled", reservation.RunID)
		}
		if record.GoalID != goalID || record.RunReservationID != reservation.ID {
			return fmt.Errorf("charged run %s has an accounting-inconsistent Run Record; refuse a new run until it is reconciled", reservation.RunID)
		}
		if _, exists := authorizations[record.ExecutionAuthorizationDigest]; !exists {
			return fmt.Errorf("charged run %s refers to an unknown execution authorization; refuse a new run until it is reconciled", reservation.RunID)
		}
		if record.RunPreparationState != "" {
			if reservation.RunID == allowedPendingRunID && record.RunPreparationState == "PENDING_CHARGE" {
				continue
			}
			return fmt.Errorf("charged run %s has an unfinished Run preparation", reservation.RunID)
		}
		expectedReceipt, err := work.ExecutionReservationReceiptFor(reservation)
		if err != nil {
			return fmt.Errorf("charged run %s has an invalid ledger reservation; refuse a new run until it is reconciled: %w", reservation.RunID, err)
		}
		receiptCount := 0
		for _, receipt := range record.ReservationReceipts {
			if receipt.ReservationID != reservation.ID {
				continue
			}
			receiptCount++
			if receipt != expectedReceipt {
				return fmt.Errorf("charged run %s has an accounting-inconsistent Run Record receipt; refuse a new run until it is reconciled", reservation.RunID)
			}
		}
		if receiptCount != 1 {
			return fmt.Errorf("charged run %s has no unique matching Run Record receipt; refuse a new run until it is reconciled", reservation.RunID)
		}
		if err := validateExactRunnerReservationReceipts(reservation.RunID,
			goal.Execution.Ledger.Reservations, record.ReservationReceipts); err != nil {
			return fmt.Errorf("charged run %s has incomplete accounting receipts; refuse a new run until it is reconciled: %w", reservation.RunID, err)
		}
	}
	if err := auditNeedsHumanAttemptDispositions(goalID, allowedPendingRunID, records, reservationsByRun,
		goal.Execution.Ledger.NeedsHumanDispositions, authorizations); err != nil {
		return err
	}
	return nil
}

func hasNeedsHumanAttemptReceipt(history []runnerAttemptRecordAudit) bool {
	for _, attempt := range history {
		if attempt.Outcome == "needs_human" && attempt.ActionReservationReceipt != nil {
			return true
		}
	}
	return false
}

func auditNeedsHumanAttemptDispositions(goalID, allowedPendingRunID string,
	records map[string]runnerRunRecordAudit, reservationsByRun map[string][]work.ExecutionReservation,
	dispositions []work.ExecutionNeedsHumanDisposition, authorizations map[string]work.ExecutionAuthorization) error {
	dispositionsByID := make(map[string]work.ExecutionNeedsHumanDisposition, len(dispositions))
	for _, disposition := range dispositions {
		dispositionsByID[disposition.ReservationID] = disposition
	}
	for runID, record := range records {
		if record.GoalID != goalID {
			continue
		}
		seen := make(map[string]bool)
		for _, attempt := range record.History {
			if attempt.Outcome != "needs_human" || attempt.ActionReservationReceipt == nil {
				continue
			}
			receipt := *attempt.ActionReservationReceipt
			if _, exists := authorizations[record.ExecutionAuthorizationDigest]; !exists {
				return fmt.Errorf("run %s needs_human attempt refers to an unknown execution authorization", runID)
			}
			if runID == "" || attempt.WorkItemID == "" || attempt.Number < 1 ||
				seen[receipt.ReservationID] {
				return fmt.Errorf("run %s has an invalid needs_human attempt receipt", runID)
			}
			seen[receipt.ReservationID] = true
			recordedReceipt, ok := findReservationReceipt(record.ReservationReceipts, receipt.ReservationID)
			if !ok || recordedReceipt != receipt {
				return fmt.Errorf("run %s needs_human attempt is missing its Run Record receipt", runID)
			}
			var reservation *work.ExecutionReservation
			for i := range reservationsByRun[runID] {
				candidate := &reservationsByRun[runID][i]
				if candidate.ID == receipt.ReservationID {
					reservation = candidate
					break
				}
			}
			if reservation == nil || reservation.Kind != work.ExecutionReservationAction || reservation.RunID != runID {
				return fmt.Errorf("run %s needs_human attempt receipt has no matching ACTION reservation", runID)
			}
			expectedReceipt, err := work.ExecutionReservationReceiptFor(*reservation)
			if err != nil || expectedReceipt != receipt {
				return fmt.Errorf("run %s needs_human attempt receipt does not match its ACTION reservation", runID)
			}
			disposition, exists := dispositionsByID[receipt.ReservationID]
			if !exists {
				if runID != allowedPendingRunID {
					return fmt.Errorf("run %s has an unconfirmed needs_human result; resume that exact run before new admission", runID)
				}
				continue
			}
			if disposition.RunID != runID || disposition.ReservationDigest != receipt.ReservationDigest {
				return fmt.Errorf("run %s needs_human disposition does not match its recorded ACTION result", runID)
			}
		}
	}
	return nil
}

func validateRunnerAuthorization(state *work.State, goalID, expectedDigest string, identity RunnerIdentity, now time.Time) error {
	goal, ok := state.GoalByID(goalID)
	if !ok || goal.Execution == nil || len(goal.Execution.Authorizations) == 0 {
		return fmt.Errorf("goal %q has no current execution authorization", goalID)
	}
	authorization := goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1]
	if expectedDigest != "" && authorization.Digest != expectedDigest {
		return fmt.Errorf("runner is bound to a different execution authorization")
	}
	if !now.UTC().Before(authorization.ExpiresAt) {
		return fmt.Errorf("execution authorization for goal %q has expired", goalID)
	}
	if authorization.WorkerIdentity == nil || authorization.EngineGeneration == nil ||
		identity.EngineGeneration == nil || identity.Model == "" || identity.Effort == "" {
		return fmt.Errorf("execution authorization for goal %q is not launchable until WorkerIdentity and engine generation are resolved", goalID)
	}
	profile := authorization.WorkerProfile
	if profile.Runtime != identity.Runtime || profile.ExecutablePath != identity.ExecutablePath ||
		profile.Model != identity.Model || profile.Effort != identity.Effort || profile.Sandbox != identity.Sandbox {
		return fmt.Errorf("runner runtime does not match the current execution Worker Profile")
	}
	workerIdentity := authorization.WorkerIdentity
	if workerIdentity.ExecutablePath != identity.ExecutablePath || workerIdentity.ReportedVersion != identity.Version ||
		workerIdentity.ObservedAt.IsZero() || workerIdentity.ObservedAt.After(now.UTC()) {
		return fmt.Errorf("runner does not match the current execution Worker identity")
	}
	if *authorization.EngineGeneration != *identity.EngineGeneration {
		return fmt.Errorf("runner does not match the current execution engine generation")
	}
	digest, err := executableDigest(identity.ExecutablePath)
	if err != nil {
		return fmt.Errorf("digest Worker executable: %w", err)
	}
	if digest != profile.ExecutableSHA256 || workerIdentity.ExecutableSHA256 != digest {
		return fmt.Errorf("runner executable does not match the current execution Worker Profile digest")
	}
	return nil
}

func executableDigest(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("executable path is not absolute")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
