package work

import "fmt"

// RepositoryState is the external repository fact a status projection needs.
// COMMIT Evidence is compared with Revision; SNAPSHOT Evidence is compared with
// SnapshotDigest. Keeping that fact as a value preserves the domain boundary:
// this package neither reads Git nor knows how a workspace digest is produced.
type RepositoryState struct {
	Revision       string
	SnapshotDigest string
}

// GoalCompletion is a read-only projection. In particular,
// GoalAwaitingFinalReview is not a persisted Goal status and cannot complete a
// Goal without the future Human final-review transition.
type GoalCompletion string

const (
	GoalInProgress          GoalCompletion = "in progress"
	GoalAwaitingFinalReview GoalCompletion = "awaiting goal final review"
	GoalBlockedCompletion   GoalCompletion = "blocked"
	GoalDoneCompletion      GoalCompletion = "done"
	GoalCancelledCompletion GoalCompletion = "cancelled"
)

type GoalSummary struct {
	Goal                    Goal
	Completion              GoalCompletion
	VerificationEvidenceIDs []string
}

// GoalSummary projects whether a Goal-level review boundary is ready. Durable
// VERIFIED state is enough for dependency progression; final review readiness
// is stricter and requires every Work Item's latest PASS to match the current
// repository candidate and every Gate to be closed.
func (s *State) GoalSummary(id string, repository RepositoryState) (GoalSummary, error) {
	goal := s.goal(id)
	if goal == nil {
		return GoalSummary{}, fmt.Errorf("unknown goal %q", id)
	}
	summary := GoalSummary{Goal: *goal, Completion: GoalInProgress}
	switch goal.Status {
	case GoalBlocked:
		summary.Completion = GoalBlockedCompletion
		return summary, nil
	case GoalCompleted:
		summary.Completion = GoalDoneCompletion
		return summary, nil
	case GoalCancelled:
		summary.Completion = GoalCancelledCompletion
		return summary, nil
	}
	if goal.ReviewPolicy != ReviewPerGoal {
		return summary, nil
	}
	found := false
	var verificationIDs []string
	for _, item := range s.itemsByCreation() {
		if item.GoalID != id {
			continue
		}
		found = true
		verification, ok := s.LatestVerification(item.ID)
		if item.Status != Verified || !ok || verification.Result != Pass || s.OpenGateCount(item.ID) > 0 ||
			!candidateMatchesRepository(verification, repository) {
			return summary, nil
		}
		verificationIDs = append(verificationIDs, verification.ID)
	}
	if found {
		summary.Completion = GoalAwaitingFinalReview
		summary.VerificationEvidenceIDs = verificationIDs
	}
	return summary, nil
}

func candidateMatchesRepository(verification Evidence, repository RepositoryState) bool {
	if verification.CandidateKind == SnapshotCandidate {
		return repository.SnapshotDigest != "" && verification.CandidateDigest == repository.SnapshotDigest
	}
	return repository.Revision != "" && verification.Revision == repository.Revision
}

// Completion describes the one current action a Work Item summary presents. It
// is a projection over durable state, never a Work Item lifecycle state.
type Completion string

const (
	CompletionNotStarted            Completion = "not started"
	CompletionImplementing          Completion = "implementing"
	CompletionVerificationNeeded    Completion = "verification required"
	CompletionVerificationFailed    Completion = "verification failed"
	CompletionVerificationStale     Completion = "verification stale"
	CompletionVerifiedForGoalReview Completion = "verified for goal review"
	CompletionAwaitingReview        Completion = "awaiting human review"
	CompletionChangesRequested      Completion = "changes requested"
	CompletionBlockedByGate         Completion = "blocked by gate"
	CompletionGoalBlocked           Completion = "goal blocked"
	CompletionDone                  Completion = "done"
)

// WorkItemSummary is a read-only view of one Work Item's actionable state.
// Evidence and Gates remain their existing durable records; this type merely
// selects the current records a caller needs to present.
type WorkItemSummary struct {
	Item              Item
	Goal              Goal
	Verification      Evidence
	HasVerification   bool
	VerificationStale bool
	Review            Evidence
	HasReview         bool
	// reviewAfterVerification distinguishes a rejection that is still current
	// from one superseded by a subsequent verification run.
	reviewAfterVerification bool
	// ApprovalNeedsRerecord identifies the existing condition where an APPROVED
	// review was recorded before a Gate or Goal later became unblocked.
	ApprovalNeedsRerecord bool
	BlockingGates         []Gate
	Completion            Completion
}

// WorkSummary projects the current state of one Work Item. It reuses the
// existing latest-Evidence and candidate-staleness rules so COMMIT and SNAPSHOT
// verification never grow separate revision semantics in presentation code.
func (s *State) WorkSummary(id string, repository RepositoryState) (WorkItemSummary, error) {
	item := s.item(id)
	if item == nil {
		return WorkItemSummary{}, fmt.Errorf("unknown work item %q", id)
	}
	goal := s.goal(item.GoalID)
	if goal == nil {
		return WorkItemSummary{}, fmt.Errorf("work item %q has unknown goal %q", id, item.GoalID)
	}

	summary := WorkItemSummary{Item: *item, Goal: *goal}
	verificationIndex, reviewIndex := -1, -1
	for i := len(s.Evidence) - 1; i >= 0 && (verificationIndex < 0 || reviewIndex < 0); i-- {
		evidence := s.Evidence[i]
		if evidence.WorkItemID != id {
			continue
		}
		if evidence.Type == VerificationEvidence && verificationIndex < 0 {
			summary.Verification, summary.HasVerification, verificationIndex = evidence, true, i
		}
		if evidence.Type == ReviewEvidence && reviewIndex < 0 {
			summary.Review, summary.HasReview, reviewIndex = evidence, true, i
		}
	}
	summary.reviewAfterVerification = reviewIndex > verificationIndex
	summary.VerificationStale = s.CandidateStale(id, repository.Revision, repository.SnapshotDigest)
	for _, gate := range s.GatesFor(id) {
		if gate.Status == GateOpen {
			summary.BlockingGates = append(summary.BlockingGates, gate)
		}
	}
	if summary.Item.Status == Review && summary.HasReview && summary.Review.Result == Approved &&
		!summary.VerificationStale && len(summary.BlockingGates) == 0 && summary.Goal.Status == GoalActive &&
		len(s.CompletionBlockers(id)) == 0 {
		summary.ApprovalNeedsRerecord = true
	}
	summary.Completion = summaryCompletion(summary)
	return summary, nil
}

func summaryCompletion(summary WorkItemSummary) Completion {
	if summary.Item.Status == Done {
		return CompletionDone
	}
	// CurrentRun is newer than every durable Evidence record. Historical FAIL or
	// REJECTED Evidence must not make an in-flight re-verification look settled.
	if summary.Item.Status == Verifying {
		return CompletionVerificationNeeded
	}
	// Goal and Gate conditions take precedence because they stop every action
	// below them. Their durable status remains visible separately in the view.
	if summary.Goal.Status != GoalActive {
		return CompletionGoalBlocked
	}
	if len(summary.BlockingGates) > 0 {
		return CompletionBlockedByGate
	}
	// A rejection moves work back to RUNNING, but the current actionable fact is
	// still the Human request for changes rather than generic implementation.
	if summary.HasReview && summary.Review.Result == Rejected && summary.reviewAfterVerification {
		return CompletionChangesRequested
	}
	if summary.HasVerification {
		if summary.VerificationStale {
			return CompletionVerificationStale
		}
		switch summary.Verification.Result {
		case Fail:
			return CompletionVerificationFailed
		case Interrupted:
			return CompletionVerificationNeeded
		}
	}

	switch summary.Item.Status {
	case Pending, Ready:
		return CompletionNotStarted
	case Running:
		return CompletionImplementing
	case Verified:
		return CompletionVerifiedForGoalReview
	case Verifying:
		return CompletionVerificationNeeded
	default:
		// REVIEW with an applicable PASS needs its first (or another) Human
		// approval. This also covers an approval recorded before a later PASS.
		return CompletionAwaitingReview
	}
}
