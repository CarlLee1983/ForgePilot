package work

import "time"

// complete moves a Work Item to DONE and settles everything that completion
// decides, in the caller's single transaction: the downstream work it unlocks
// becomes READY, and the Goal completes if this was its last unfinished item.
// A reader therefore never sees a DONE item beside a still-PENDING dependent, or
// a Goal whose every item is DONE but which is still ACTIVE.
func (s *State) complete(id string, now time.Time) {
	item := s.item(id)
	if item == nil {
		return
	}
	item.Status, item.UpdatedAt = Done, now
	s.refreshDependents(id, now)
	s.completeGoalIfFinished(item.GoalID, now)
}

// ReadyDependents names the READY Work Items that depend directly on id, in
// creation order. After id's completion these are the ones it just unlocked.
func (s *State) ReadyDependents(id string) []string {
	var ready []string
	for _, item := range s.itemsByCreation() {
		if item.Status != Ready {
			continue
		}
		for _, dependency := range item.DependsOn {
			if dependency == id {
				ready = append(ready, item.ID)
				break
			}
		}
	}
	return ready
}

// GoalOfWorkItem returns the Goal a Work Item belongs to.
func (s *State) GoalOfWorkItem(id string) (Goal, bool) {
	item := s.item(id)
	if item == nil {
		return Goal{}, false
	}
	return s.GoalByID(item.GoalID)
}
