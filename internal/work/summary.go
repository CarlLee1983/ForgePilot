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

// Completion describes the one current action a Work Item summary presents. It
// is a projection over durable state, never a Work Item lifecycle state.
type Completion string

const (
	CompletionNotStarted         Completion = "not started"
	CompletionImplementing       Completion = "implementing"
	CompletionVerificationNeeded Completion = "verification required"
	CompletionVerificationFailed Completion = "verification failed"
	CompletionVerificationStale  Completion = "verification stale"
	CompletionAwaitingReview     Completion = "awaiting human review"
	CompletionChangesRequested   Completion = "changes requested"
	CompletionBlockedByGate      Completion = "blocked by gate"
	CompletionGoalBlocked        Completion = "goal blocked"
	CompletionDone               Completion = "done"
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
	case Verifying:
		return CompletionVerificationNeeded
	default:
		// REVIEW with an applicable PASS needs its first (or another) Human
		// approval. This also covers an approval recorded before a later PASS.
		return CompletionAwaitingReview
	}
}
