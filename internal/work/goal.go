package work

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// CancelGoal ends a Goal that is no longer going to be done, so that abandoned
// work has a terminus instead of sitting in ACTIVE forever. A Goal has no other
// way to stop: it completes by itself when its last Work Item is DONE
// (completeGoalIfFinished), and blocking is a Gate, not a Goal status.
//
// What a cancelled Goal stops is starting new work, verifying, and reaching
// DONE. It does not stop recording something that already happened: a
// Verification Run underway when the Goal is cancelled still records its
// Evidence.
func (s *State) CancelGoal(id, reason string, now time.Time) error {
	goal := s.goal(id)
	if goal == nil {
		return fmt.Errorf("unknown goal %q", id)
	}
	if goal.Status != GoalActive {
		return fmt.Errorf("cannot cancel goal %q: it is %s", id, goal.Status)
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("cancelling a goal requires a reason")
	}
	goal.Status, goal.Reason, goal.UpdatedAt = GoalCancelled, reason, now
	return nil
}

// completeGoalIfFinished completes an ACTIVE Goal whose Work Items are all DONE.
// It is called from the transaction that made the last item DONE, so a reader
// never sees every item DONE under a Goal that is still ACTIVE. Completion asks
// nothing else: in particular it does not ask whether each PASS names the final
// Candidate (ADR-0040).
func (s *State) completeGoalIfFinished(goalID string, now time.Time) {
	goal := s.goal(goalID)
	if goal == nil || goal.Status != GoalActive {
		return
	}
	for _, item := range s.WorkItems {
		if item.GoalID == goalID && item.Status != Done {
			return
		}
	}
	goal.Status, goal.UpdatedAt = GoalCompleted, now
}
