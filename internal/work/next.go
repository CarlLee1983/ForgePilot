package work

import "sort"

// NextActionKind describes a recommendation over existing Work Item state. It
// is deliberately not a lifecycle status and is never persisted.
type NextActionKind string

const (
	NextActionNone            NextActionKind = "NONE"
	NextActionResume          NextActionKind = "RESUME"
	NextActionRepair          NextActionKind = "REPAIR"
	NextActionReverify        NextActionKind = "REVERIFY"
	NextActionStart           NextActionKind = "START"
	NextActionWaitHumanReview NextActionKind = "WAIT_HUMAN_REVIEW"
	NextActionWaitGate        NextActionKind = "WAIT_GATE"
	NextActionWaitGoal        NextActionKind = "WAIT_GOAL"
)

// NextAction is the read-only answer to what an agent can legally do next.
// Item is empty only when Kind is NextActionNone.
type NextAction struct {
	Item   Item
	Kind   NextActionKind
	Reason string
}

// ActionableNext selects one legal agent action without changing State. It
// preserves Next's READY ordering, while running and stale-review work take
// priority so a new agent session does not abandon work already underway.
func (s *State) ActionableNext(repository RepositoryState) NextAction {
	items := s.itemsByCreation()

	for _, item := range items {
		if item.Status != Running || s.Verifiable(item.ID) != nil {
			continue
		}
		if verification, ok := s.LatestVerification(item.ID); ok && verification.Result == Fail {
			return NextAction{Item: item, Kind: NextActionRepair, Reason: "latest verification failed"}
		}
		return NextAction{Item: item, Kind: NextActionResume, Reason: "work is already in progress"}
	}

	for _, item := range items {
		if item.Status == Review && s.Verifiable(item.ID) == nil && s.CandidateStale(item.ID, repository.Revision, repository.SnapshotDigest) {
			return NextAction{Item: item, Kind: NextActionReverify, Reason: "verified candidate is stale"}
		}
	}

	if item, ok := s.Next(); ok {
		return NextAction{Item: item, Kind: NextActionStart, Reason: "earliest READY work"}
	}

	// No agent action is legal. Surface the oldest concrete human decision that
	// would unblock work; dependency PENDING and in-flight verification are not
	// human-only states, so they do not manufacture a waiting recommendation.
	for _, item := range items {
		if item.Status == Pending || item.Status == Done || item.Status == Verifying {
			continue
		}
		goal := s.goal(item.GoalID)
		if goal != nil && goal.Status != GoalActive {
			return NextAction{Item: item, Kind: NextActionWaitGoal, Reason: "goal " + goal.ID + " is " + string(goal.Status)}
		}
		for _, gate := range s.GatesFor(item.ID) {
			if gate.Status == GateOpen {
				return NextAction{Item: item, Kind: NextActionWaitGate, Reason: "unresolved Gate " + gate.ID}
			}
		}
		if item.Status == Review {
			if verification, ok := s.LatestVerification(item.ID); ok && verification.Result == Pass &&
				!s.CandidateStale(item.ID, repository.Revision, repository.SnapshotDigest) {
				return NextAction{Item: item, Kind: NextActionWaitHumanReview, Reason: "human review required"}
			}
		}
	}

	return NextAction{Kind: NextActionNone}
}

func (s *State) itemsByCreation() []Item {
	items := append([]Item(nil), s.WorkItems...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return workNumber(items[i].ID) < workNumber(items[j].ID)
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items
}
