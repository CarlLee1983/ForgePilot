package work

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// BlockGoal stops a whole Goal when its direction turns out to be wrong. It is a
// pause, not a discard: work that was RUNNING or in REVIEW keeps the status it
// had, so unblocking restores nothing because nothing was taken away.
//
// What an inactive Goal stops is starting new work, verifying, and reaching
// DONE. It does not stop recording something that already happened: a
// Verification Run underway when the Goal is blocked still records its Evidence.
func (s *State) BlockGoal(id, reason string, now time.Time) error {
	goal, err := s.goalInStatus(id, "block", GoalActive)
	if err != nil {
		return err
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("blocking a goal requires a reason")
	}
	goal.Status, goal.Reason, goal.UpdatedAt = GoalBlocked, reason, now
	return nil
}

// UnblockGoal returns a paused Goal to ACTIVE, clearing the reason it was paused
// for: that reason described a state the Goal is no longer in.
func (s *State) UnblockGoal(id string, now time.Time) error {
	goal, err := s.goalInStatus(id, "unblock", GoalBlocked)
	if err != nil {
		return err
	}
	goal.Status, goal.Reason, goal.UpdatedAt = GoalActive, "", now
	return nil
}

// CompleteGoal declares a Goal finished. It is deliberately a person's
// declaration rather than something the system infers: an automatic mark would
// have to be reversible when new work arrives, and a reversible COMPLETED sits
// badly beside a DONE that never is.
func (s *State) CompleteGoal(id string, now time.Time) error {
	goal, err := s.goalInStatus(id, "complete", GoalActive)
	if err != nil {
		return err
	}
	for _, item := range s.WorkItems {
		if item.GoalID == id && item.Status != Done {
			return fmt.Errorf("goal %q still has unfinished work: %s is %s", id, item.ID, item.Status)
		}
	}
	goal.Status, goal.UpdatedAt = GoalCompleted, now
	return nil
}

// CancelGoal ends a Goal that is no longer going to be done, so that abandoned
// work has a terminus instead of sitting in ACTIVE forever.
func (s *State) CancelGoal(id, reason string, now time.Time) error {
	goal, err := s.goalInStatus(id, "cancel", GoalActive, GoalBlocked)
	if err != nil {
		return err
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("cancelling a goal requires a reason")
	}
	goal.Status, goal.Reason, goal.UpdatedAt = GoalCancelled, reason, now
	return nil
}

func (s *State) goalInStatus(id, action string, allowed ...GoalStatus) (*Goal, error) {
	goal := s.goal(id)
	if goal == nil {
		return nil, fmt.Errorf("unknown goal %q", id)
	}
	for _, status := range allowed {
		if goal.Status == status {
			return goal, nil
		}
	}
	return nil, fmt.Errorf("cannot %s goal %q: it is %s", action, id, goal.Status)
}
