package work

import "sort"

// NextActionKind describes a recommendation over existing Work Item state. It
// is deliberately not a lifecycle status and is never persisted.
type NextActionKind string

const (
	NextActionNone          NextActionKind = "NONE"
	NextActionResume        NextActionKind = "RESUME"
	NextActionRepair        NextActionKind = "REPAIR"
	NextActionRecover       NextActionKind = "RECOVER"
	NextActionReverify      NextActionKind = "REVERIFY"
	NextActionStart         NextActionKind = "START"
	NextActionWait          NextActionKind = "WAIT"
	NextActionGoalCompleted NextActionKind = "GOAL_ALREADY_COMPLETED"
	NextActionGoalCancelled NextActionKind = "GOAL_CANCELLED"
)

// WaitKind says what a Waiting entry is waiting for.
type WaitKind string

const (
	WaitGate         WaitKind = "GATE"
	WaitReview       WaitKind = "REVIEW"
	WaitVerification WaitKind = "VERIFICATION"
)

// Waiting is one thing nobody but a person (or a verifier already running) can
// move forward. GateID is set for WaitGate.
type Waiting struct {
	Kind   WaitKind
	ItemID string
	GoalID string
	GateID string
	Reason string
}

// NextAction is the read-only answer to what an agent can legally do next.
// Goal is populated for an already-ended (completed or cancelled) Goal. Item is
// empty for that action, for WAIT and when Kind is NextActionNone. Waiting is
// populated for WAIT.
type NextAction struct {
	Item    Item
	Goal    Goal
	Kind    NextActionKind
	Reason  string
	Waiting []Waiting
}

// ActionableNext selects the one legal agent action without changing State, in
// this order:
//
//  1. continue the work holding the workspace (RUNNING, or VERIFYING whose
//     verifier is gone, which verify reclaims);
//  2. verify a REVIEW whose Candidate has gone stale again;
//  3. start the earliest READY work that has no open Gate;
//  4. wait for people: list open Gates and REVIEW work;
//  5. report an ended Goal, or that there is nothing.
//
// While the workspace is held, steps 2 and 3 are skipped: the transitions they
// recommend would be refused. Earliest means creation time, then position in
// state.WorkItems, which is the Goal Plan's node order within a Goal; across
// Goals this is the order Goals were imported.
func (s *State) ActionableNext(repository RepositoryState) NextAction {
	items := s.itemsByCreation()

	if occupant, held := s.Occupant(); held {
		if occupant.Status == Verifying {
			if repository.AbandonedRuns[occupant.ID] {
				return NextAction{Item: occupant, Kind: NextActionRecover, Reason: "verification was interrupted; verify reclaims it"}
			}
			return NextAction{Kind: NextActionWait, Reason: "verification is in progress",
				Waiting: []Waiting{{Kind: WaitVerification, ItemID: occupant.ID, GoalID: occupant.GoalID, Reason: "verification in progress"}}}
		}
		if s.Verifiable(occupant.ID) == nil {
			if verification, ok := s.LatestVerification(occupant.ID); ok && verification.Result == Fail {
				return NextAction{Item: occupant, Kind: NextActionRepair, Reason: "latest verification failed"}
			}
			return NextAction{Item: occupant, Kind: NextActionResume, Reason: "work is already in progress"}
		}
		// The occupant is held by an open Gate: nothing else may start either.
		return s.waitAction(items, repository)
	}

	// A stale REVIEW keeps its priority over new work: that is the thing a person
	// is already waiting on, and approval is refused until its Evidence names the
	// current Candidate again.
	for _, item := range items {
		if item.Status == Review && s.staleReverifiable(item, repository) {
			return NextAction{Item: item, Kind: NextActionReverify, Reason: "verified candidate is stale"}
		}
	}

	for _, item := range items {
		if item.Status != NotStarted || !s.startable(item) {
			continue
		}
		return NextAction{Item: item, Kind: NextActionStart, Reason: "earliest READY work"}
	}

	if action := s.waitAction(items, repository); action.Kind == NextActionWait {
		return action
	}

	if goal, ok := s.endedGoalToReport(); ok {
		if goal.Status == GoalCancelled {
			return NextAction{Goal: goal, Kind: NextActionGoalCancelled, Reason: goal.Reason}
		}
		return NextAction{Goal: goal, Kind: NextActionGoalCompleted, Reason: "every Work Item is DONE"}
	}
	return NextAction{Kind: NextActionNone}
}

// startable is the legality test for recommending a start: the Goal is ACTIVE,
// every dependency is DONE and no Gate is open. The workspace slot is checked by
// the caller, since it is a property of the whole queue.
func (s *State) startable(item Item) bool {
	if goal := s.goal(item.GoalID); goal == nil || goal.Status != GoalActive {
		return false
	}
	return s.OpenGateCount(item.ID) == 0 && len(s.unfinishedDependencies(item.DependsOn)) == 0
}

// waitAction lists, oldest first, what only a person can resolve: open Gates and
// REVIEW work whose Candidate is still current. Work of an ended Goal is never a
// wait, because nothing will change. It returns NONE when there is nothing to
// wait for.
func (s *State) waitAction(items []Item, repository RepositoryState) NextAction {
	var waiting []Waiting
	for _, item := range items {
		if item.Status == Done {
			continue
		}
		if goal := s.goal(item.GoalID); goal == nil || goal.Status != GoalActive {
			continue
		}
		for _, gate := range s.GatesFor(item.ID) {
			if gate.Status == GateOpen {
				waiting = append(waiting, Waiting{Kind: WaitGate, ItemID: item.ID, GoalID: item.GoalID, GateID: gate.ID, Reason: "unresolved Gate " + gate.ID})
			}
		}
		if item.Status == Review && !s.CandidateStale(item.ID, repository.Revision, repository.SnapshotDigest) {
			waiting = append(waiting, Waiting{Kind: WaitReview, ItemID: item.ID, GoalID: item.GoalID, Reason: "human review required"})
		}
	}
	if len(waiting) == 0 {
		return NextAction{Kind: NextActionNone}
	}
	return NextAction{Kind: NextActionWait, Reason: waiting[0].Reason, Waiting: waiting}
}

// endedGoalToReport picks the Goal `next` announces as over. A Goal is announced
// only once no Goal is still ACTIVE: while another Goal is in progress, an old
// ended one is history, not news. Among ended Goals the most recently updated
// wins, which is the one whose last item was just DONE or that was just
// cancelled.
func (s *State) endedGoalToReport() (Goal, bool) {
	var latest *Goal
	for i := range s.Goals {
		goal := &s.Goals[i]
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

// itemsByCreation orders every Work Item by creation time. It is stable: Work
// Items imported together share a CreatedAt, and their order in state.WorkItems
// is the Goal Plan's node order, which is the tie-break.
func (s *State) itemsByCreation() []Item {
	items := append([]Item(nil), s.WorkItems...)
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items
}
