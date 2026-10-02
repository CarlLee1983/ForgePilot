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
	NextActionReconcile       NextActionKind = "RECONCILE"
	NextActionGoalCompleted   NextActionKind = "GOAL_ALREADY_COMPLETED"
	NextActionGoalCancelled   NextActionKind = "GOAL_CANCELLED"
	NextActionWaitHumanReview NextActionKind = "WAIT_HUMAN_REVIEW"
	NextActionWaitGate        NextActionKind = "WAIT_GATE"
)

// NextAction is the read-only answer to what an agent can legally do next.
// Goal is populated for an already-ended (completed or cancelled) Goal. Item is empty for that action
// and when Kind is NextActionNone.
type NextAction struct {
	Item   Item
	Goal   Goal
	Kind   NextActionKind
	Reason string
}

// ActionableNext selects one legal agent action without changing State. Running
// and stale REVIEW work take priority so a new agent session does not abandon
// work already underway; work that can legally advance comes next, sharing
// Next's ordering. When nothing is left to do and every Goal has ended, the most
// recently completed Goal is reported as completed, so an agent finishing the
// last Work Item learns that the whole Goal is done.
func (s *State) ActionableNext(repository RepositoryState) NextAction {
	return s.actionableNext("", repository)
}

func (s *State) actionableNext(goalID string, repository RepositoryState) NextAction {
	items := s.itemsByCreationInGoal(goalID)

	for _, item := range items {
		if item.Status != Running || s.Verifiable(item.ID) != nil {
			continue
		}
		if verification, ok := s.LatestVerification(item.ID); ok && verification.Result == Fail {
			return NextAction{Item: item, Kind: NextActionRepair, Reason: "latest verification failed"}
		}
		return NextAction{Item: item, Kind: NextActionResume, Reason: "work is already in progress"}
	}

	// A stale REVIEW keeps its priority: that Work Item is the thing a person is
	// already waiting on, and approval is refused until its Evidence names the
	// current Candidate again.
	for _, item := range items {
		if item.Status == Review && s.staleReverifiable(item, repository) {
			return NextAction{Item: item, Kind: NextActionReverify, Reason: "verified candidate is stale"}
		}
	}

	// START and RECONCILE share one creation-ordered pass rather than two loops,
	// so which of them is recommended never depends on loop order.
	for _, item := range items {
		if (item.Status != Ready && item.Status != Pending) || !s.advanceable(item) {
			continue
		}
		if item.Status == Ready {
			return NextAction{Item: item, Kind: NextActionStart, Reason: "earliest READY work"}
		}
		return NextAction{Item: item, Kind: NextActionReconcile,
			Reason: "dependencies are satisfied but persisted readiness is still PENDING"}
	}

	// No agent action is legal. Surface the oldest concrete human decision that
	// would unblock work; dependency PENDING and in-flight verification are not
	// human-only states, so they do not manufacture a waiting recommendation.
	for _, item := range items {
		if item.Status == Pending || item.Status == Done || item.Status == Verifying {
			continue
		}
		// An ended Goal is terminal: nothing will change, so it is never a wait.
		if goal := s.goal(item.GoalID); goal != nil && goal.Status != GoalActive {
			continue
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

	if goal, ok := s.endedGoalToReport(goalID); ok {
		if goal.Status == GoalCancelled {
			return NextAction{Goal: goal, Kind: NextActionGoalCancelled, Reason: goal.Reason}
		}
		return NextAction{Goal: goal, Kind: NextActionGoalCompleted, Reason: "every Work Item is DONE"}
	}
	return NextAction{Kind: NextActionNone}
}

// endedGoalToReport picks the Goal `next` announces as over. A Goal is announced
// only once no Goal in scope is still ACTIVE: while another Goal is in progress,
// an old ended one is history, not news. Among ended Goals the most recently
// updated wins, which is the one whose last item was just DONE or that was just
// cancelled.
func (s *State) endedGoalToReport(goalID string) (Goal, bool) {
	var latest *Goal
	for i := range s.Goals {
		goal := &s.Goals[i]
		if goalID != "" && goal.ID != goalID {
			continue
		}
		if goal.Status == GoalActive {
			return Goal{}, false
		}
		if latest == nil || !goal.UpdatedAt.Before(latest.UpdatedAt) {
			latest = goal
		}
	}
	if latest == nil {
		return Goal{}, false
	}
	return *latest, true
}

// staleReverifiable reports REVIEW work whose passing Evidence no longer names
// the current Candidate and which may legally be verified again.
func (s *State) staleReverifiable(item Item, repository RepositoryState) bool {
	return s.Verifiable(item.ID) == nil &&
		s.CandidateStale(item.ID, repository.Revision, repository.SnapshotDigest)
}

func (s *State) itemsByCreation() []Item {
	return s.itemsByCreationInGoal("")
}

// itemsByCreationInGoal orders the candidate Work Items a selection may choose
// from. An empty goalID means every Goal, which is what keeps the global and
// Goal-scoped queries one implementation rather than two orderings.
func (s *State) itemsByCreationInGoal(goalID string) []Item {
	var items []Item
	for _, item := range s.WorkItems {
		if goalID == "" || item.GoalID == goalID {
			items = append(items, item)
		}
	}
	// Stable: Work Items imported together share a CreatedAt, and their order in
	// state.WorkItems is the Goal Plan's node order, which is the tie-break.
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items
}
