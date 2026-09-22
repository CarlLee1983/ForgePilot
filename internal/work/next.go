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
	NextActionCompleteGoal    NextActionKind = "COMPLETE_GOAL"
	NextActionGoalCompleted   NextActionKind = "GOAL_ALREADY_COMPLETED"
	NextActionWaitHumanReview NextActionKind = "WAIT_HUMAN_REVIEW"
	NextActionWaitGate        NextActionKind = "WAIT_GATE"
	NextActionWaitGoal        NextActionKind = "WAIT_GOAL"
)

// NextAction is the read-only answer to what an agent can legally do next.
// Goal is populated for Goal-level completion or an already-completed Goal.
// Item is empty for those actions and when Kind is NextActionNone.
type NextAction struct {
	Item        Item
	Goal        Goal
	Kind        NextActionKind
	Reason      string
	EvidenceIDs []string
}

// ActionableNext selects one legal agent action without changing State. Running
// and stale WORK_ITEM-policy review work take priority so a new agent session
// does not abandon work already underway; work that can legally advance comes
// next, sharing Next's ordering; GOAL-policy re-verification of history comes
// last, because it is owed at the Goal boundary rather than owed right now.
func (s *State) ActionableNext(repository RepositoryState) NextAction {
	return s.actionableNext("", repository)
}

// ActionableNextForGoal answers the same question within one Goal. Scoping
// happens before the priority rules, never after: taking the global answer and
// discarding it when it belongs to another Goal would let an unrelated Goal
// manufacture a stall here. Only the candidate set narrows — dependency and
// freshness rules still read the whole State, so a prerequisite is judged by
// what it actually is. See docs/adr/0019-runner-executes-forgepilot-decides.md.
func (s *State) ActionableNextForGoal(goalID string, repository RepositoryState) NextAction {
	return s.actionableNext(goalID, repository)
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

	// A stale REVIEW under WORK_ITEM policy keeps its priority: that Work Item is
	// the thing a person is already waiting on, and nothing it gates can move
	// until its Evidence names the current Candidate again.
	for _, item := range items {
		if item.Status == Review && s.staleReverifiable(item, repository) {
			return NextAction{Item: item, Kind: NextActionReverify, Reason: "verified candidate is stale"}
		}
	}

	// Work that can legally move forward is chosen before any history is
	// re-verified. Under GOAL policy every new Candidate makes earlier VERIFIED
	// work stale, so putting those re-verifications first would re-check the whole
	// queue between consecutive Work Items. Deferring them relaxes nothing: the
	// Goal completion boundary below still demands a current PASS for every Work
	// Item, so each deferred re-verification is owed, not forgiven.
	//
	// START and RECONCILE share one creation-ordered pass rather than two loops,
	// so which of them is recommended never depends on loop order.
	for _, item := range items {
		if (item.Status != Ready && item.Status != Pending) || !s.advanceable(item, &repository) {
			continue
		}
		if item.Status == Ready {
			return NextAction{Item: item, Kind: NextActionStart, Reason: "earliest READY work"}
		}
		return NextAction{Item: item, Kind: NextActionReconcile,
			Reason: "dependencies are satisfied but persisted readiness is still PENDING"}
	}

	for _, item := range items {
		if item.Status == Verified && s.staleReverifiable(item, repository) {
			return NextAction{Item: item, Kind: NextActionReverify, Reason: "verified candidate is stale"}
		}
	}

	// No agent action is legal. Surface the oldest concrete human decision that
	// would unblock work; dependency PENDING and in-flight verification are not
	// human-only states, so they do not manufacture a waiting recommendation.
	for _, item := range items {
		if item.Status == Pending || item.Status == Done || item.Status == Verifying {
			continue
		}
		goal := s.goal(item.GoalID)
		if goal != nil && goal.Status == GoalCompleted {
			// GOAL completion leaves its Work Items VERIFIED. They are immutable
			// history, not a wait that should mask actionable work in another Goal.
			continue
		}
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

	for _, goal := range s.Goals {
		if goalID != "" && goal.ID != goalID {
			continue
		}
		summary, err := s.GoalSummary(goal.ID, repository)
		if err != nil {
			continue
		}
		switch summary.Completion {
		case GoalReadyToComplete:
			return NextAction{Goal: goal, Kind: NextActionCompleteGoal, EvidenceIDs: summary.VerificationEvidenceIDs, Reason: "all Work Items have fresh PASS Evidence and no open Gates"}
		}
	}

	return NextAction{Kind: NextActionNone}
}

// staleReverifiable is the one test behind both re-verification passes. REVIEW
// and VERIFIED differ in when they are offered, not in what qualifies them, and
// one predicate keeps that difference visible as scheduling alone.
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
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return workNumber(items[i].ID) < workNumber(items[j].ID)
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items
}
