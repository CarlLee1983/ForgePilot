package work

// StallKind names why no agent action is legal within one Goal. NONE is not a
// completion: a Runner that treated it as one would stop with work still owed,
// so every reason a Goal can be stuck for has to be nameable here.
type StallKind string

const (
	// StallNone means work can still advance; the Goal is not stalled at all.
	StallNone StallKind = ""
	// StallUnknownGoal and StallGoalNotActive are about the Goal itself rather
	// than about anything inside it.
	StallUnknownGoal   StallKind = "UNKNOWN_GOAL"
	StallGoalNotActive StallKind = "GOAL_NOT_ACTIVE"
	StallEmptyGoal     StallKind = "EMPTY_GOAL"
	// StallVerifying names Work Items marked VERIFYING. Whether such a run is
	// live or an orphan is not a question this package can answer — liveness is
	// a flock fact — so the caller disambiguates and this only says which items.
	StallVerifying StallKind = "VERIFICATION_IN_FLIGHT"
	// StallBlockedByGate and StallDependenciesUnsatisfied are the two ordinary
	// reasons work sits still: someone must decide, or something must finish.
	StallBlockedByGate           StallKind = "BLOCKED_BY_GATE"
	StallDependenciesUnsatisfied StallKind = "DEPENDENCIES_UNSATISFIED"
	// StallNoRemainingWork means every Work Item has reached VERIFIED or DONE.
	// It still is not a completion: the Goal final-review projection decides
	// that, and it is stricter than this.
	StallNoRemainingWork StallKind = "NO_REMAINING_WORK"
	StallUnknown         StallKind = "UNKNOWN"
)

// GoalStall reports why a Goal cannot advance, naming the Work Items the reason
// is about. It is a pure projection and never a recommendation.
type GoalStall struct {
	Kind    StallKind
	ItemIDs []string
	Detail  string
}

// GoalStall classifies one Goal's inability to advance. It answers
// independently of ActionableNextForGoal rather than explaining its NONE,
// because the two questions differ: a WAIT_GATE recommendation still describes
// a Goal that is stalled, and a Runner has to record which kind of stall it is.
func (s *State) GoalStall(id string, repository RepositoryState) GoalStall {
	goal := s.goal(id)
	if goal == nil {
		return GoalStall{Kind: StallUnknownGoal, Detail: "unknown goal " + id}
	}
	if goal.Status != GoalActive {
		return GoalStall{Kind: StallGoalNotActive, Detail: "goal " + id + " is " + string(goal.Status)}
	}
	items := s.itemsByCreationInGoal(id)
	if len(items) == 0 {
		return GoalStall{Kind: StallEmptyGoal, Detail: "goal " + id + " has no work items"}
	}

	var verifying, gated, waiting, remaining []string
	advancing := false
	for _, item := range items {
		switch {
		case item.Status == Running && s.Verifiable(item.ID) == nil:
			advancing = true
		case (item.Status == Ready || item.Status == Pending) && s.advanceable(item, &repository):
			advancing = true
		case (item.Status == Review || item.Status == Verified) && s.staleReverifiable(item, repository):
			advancing = true
		}
		if item.Status == Verifying {
			verifying = append(verifying, item.ID)
		}
		if item.Status != Done && s.OpenGateCount(item.ID) > 0 {
			gated = append(gated, item.ID)
		}
		if (item.Status == Ready || item.Status == Pending) && !s.dependenciesSatisfiedAt(item.DependsOn, &repository) {
			waiting = append(waiting, item.ID)
		}
		if item.Status != Verified && item.Status != Done {
			remaining = append(remaining, item.ID)
		}
	}
	switch {
	case advancing:
		return GoalStall{Kind: StallNone}
	case len(verifying) > 0:
		return GoalStall{Kind: StallVerifying, ItemIDs: verifying,
			Detail: "a verification run is recorded in flight; it is either live or an orphan awaiting reclamation"}
	case len(gated) > 0:
		return GoalStall{Kind: StallBlockedByGate, ItemIDs: gated, Detail: "an unresolved Gate withholds authority"}
	case len(waiting) > 0:
		return GoalStall{Kind: StallDependenciesUnsatisfied, ItemIDs: waiting,
			Detail: "dependencies are not satisfied against the current Candidate"}
	case len(remaining) == 0:
		return GoalStall{Kind: StallNoRemainingWork, Detail: "every work item is VERIFIED or DONE"}
	default:
		return GoalStall{Kind: StallUnknown, ItemIDs: remaining, Detail: "no legal agent action and no nameable blocker"}
	}
}
