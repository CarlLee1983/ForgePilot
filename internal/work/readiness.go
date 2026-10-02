package work

import (
	"fmt"
	"strings"
)

// Readiness is whether NOT_STARTED work may begin as far as its dependencies go.
// It is computed from the current state each time it is read and never stored
// (ADR-0040), so there is nothing to reconcile.
type Readiness string

const (
	ReadinessPending Readiness = "PENDING"
	ReadinessReady   Readiness = "READY"
)

// Readiness reports the readiness of NOT_STARTED work: READY exactly when every
// dependency is DONE. The second result is false for an unknown item and for
// work that has already started, which has no readiness.
func (s *State) Readiness(id string) (Readiness, bool) {
	item := s.item(id)
	if item == nil || item.Status != NotStarted {
		return "", false
	}
	// Work of an ended Goal can never start, so there is no readiness to report.
	if goal := s.goal(item.GoalID); goal == nil || goal.Status != GoalActive {
		return "", false
	}
	if len(s.unfinishedDependencies(item.DependsOn)) > 0 {
		return ReadinessPending, true
	}
	return ReadinessReady, true
}

// DisplayStatus is the status a reader sees: the persisted status, except that
// NOT_STARTED is shown as its computed readiness, PENDING or READY.
func (s *State) DisplayStatus(id string) string {
	if readiness, ok := s.Readiness(id); ok {
		return string(readiness)
	}
	return string(s.WorkItemStatus(id))
}

// Occupant returns the Work Item that holds the workspace's single execution
// slot: one that is RUNNING or VERIFYING in an ACTIVE Goal. REVIEW does not
// occupy it, because nothing is executing while a person decides. Work of an
// ended Goal does not occupy it either: it can never finish, so it would
// otherwise block the workspace for good.
func (s *State) Occupant() (Item, bool) {
	for _, item := range s.itemsByCreation() {
		if item.Status != Running && item.Status != Verifying {
			continue
		}
		if goal := s.goal(item.GoalID); goal != nil && goal.Status == GoalActive {
			return item, true
		}
	}
	return Item{}, false
}

// occupancyBlock refuses a transition that would put id into RUNNING or
// VERIFYING while a different Work Item already holds the slot.
func (s *State) occupancyBlock(id string) error {
	occupant, ok := s.Occupant()
	if !ok || occupant.ID == id {
		return nil
	}
	message := fmt.Sprintf("work item %q is %s; only one work item can be RUNNING or VERIFYING at a time", occupant.ID, occupant.Status)
	if occupant.Status == Verifying {
		message += fmt.Sprintf(" (if its verification was interrupted, run forgepilot verify %s to recover it)", occupant.ID)
	}
	return fmt.Errorf("work item %q cannot proceed: %s", id, message)
}

// ObstacleKind classifies one reason an unfinished Work Item cannot advance.
type ObstacleKind string

const (
	ObstacleDependency ObstacleKind = "DEPENDENCY"
	ObstacleGate       ObstacleKind = "GATE"
	ObstacleApproval   ObstacleKind = "AWAITING_APPROVAL"
	ObstacleOccupied   ObstacleKind = "OCCUPIED"
	ObstacleVerifying  ObstacleKind = "VERIFYING"
	ObstacleGoalEnded  ObstacleKind = "GOAL_ENDED"
)

// Obstacle is one reason an unfinished Work Item cannot advance. Ref names the
// dependency, Gate or occupying Work Item involved, when there is one.
type Obstacle struct {
	Kind    ObstacleKind
	Ref     string
	Message string
}

// Obstacles explains why an unfinished Work Item cannot advance right now. DONE
// work and work that can advance have none. It only reads state.
func (s *State) Obstacles(id string) []Obstacle {
	item := s.item(id)
	if item == nil || item.Status == Done {
		return nil
	}
	if goal := s.goal(item.GoalID); goal == nil || goal.Status != GoalActive {
		status := GoalStatus("")
		if goal != nil {
			status = goal.Status
		}
		return []Obstacle{{Kind: ObstacleGoalEnded, Ref: item.GoalID, Message: fmt.Sprintf("goal %s is %s", item.GoalID, status)}}
	}
	var obstacles []Obstacle
	switch item.Status {
	case NotStarted:
		for _, dependency := range s.unfinishedDependencies(item.DependsOn) {
			obstacles = append(obstacles, Obstacle{Kind: ObstacleDependency, Ref: dependency,
				Message: fmt.Sprintf("depends on %s, which is %s", dependency, s.DisplayStatus(dependency))})
		}
	case Review:
		obstacles = append(obstacles, Obstacle{Kind: ObstacleApproval, Message: "waiting for review approve"})
		if occupant, ok := s.Occupant(); ok {
			obstacles = append(obstacles, Obstacle{Kind: ObstacleOccupied, Ref: occupant.ID,
				Message: fmt.Sprintf("review reject and re-verify are refused while %s is %s and holds the workspace; %s", occupant.ID, occupant.Status, occupantWayOut(occupant))})
		}
	case Verifying:
		obstacles = append(obstacles, Obstacle{Kind: ObstacleVerifying, Message: "verification is in progress or was interrupted"})
	}
	for _, gate := range s.GatesFor(id) {
		if gate.Status == GateOpen {
			obstacles = append(obstacles, Obstacle{Kind: ObstacleGate, Ref: gate.ID, Message: "open Gate " + gate.ID})
		}
	}
	if item.Status == NotStarted && len(s.unfinishedDependencies(item.DependsOn)) == 0 {
		if occupant, ok := s.Occupant(); ok {
			obstacles = append(obstacles, Obstacle{Kind: ObstacleOccupied, Ref: occupant.ID,
				Message: fmt.Sprintf("%s is %s and holds the workspace; %s", occupant.ID, occupant.Status, occupantWayOut(occupant))})
		}
	}
	return obstacles
}

// occupantWayOut says how the workspace slot gets freed: finish the occupant
// (a PASS completes it or sends it to REVIEW; a FAIL returns it to RUNNING to be
// fixed) or cancel its Goal.
func occupantWayOut(occupant Item) string {
	return fmt.Sprintf("let it finish with forgepilot verify %s (PASS, or fix it after a FAIL), or forgepilot goal cancel %s", occupant.ID, occupant.GoalID)
}

// ObstacleMessages joins obstacle messages for one-line presentation.
func ObstacleMessages(obstacles []Obstacle) string {
	messages := make([]string, 0, len(obstacles))
	for _, obstacle := range obstacles {
		messages = append(messages, obstacle.Message)
	}
	return strings.Join(messages, "; ")
}
