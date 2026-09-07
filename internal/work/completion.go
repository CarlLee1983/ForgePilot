package work

import (
	"fmt"
	"time"
)

// CompletionBlockers reports every condition standing between a Work Item and
// DONE. An empty result means nothing does — either the work is already complete
// or the next approval will complete it.
//
// There is no completion command: DONE is only ever the result of these
// conditions holding (ADR-0008). That makes an accurate account of why they do
// not hold the only way a user can find out, so this must never stay silent.
func (s *State) CompletionBlockers(id string) []string {
	item := s.item(id)
	if item == nil || item.Status == Done {
		return nil
	}
	var blockers []string
	review, reviewed := s.LatestReview(id)
	switch {
	case !reviewed:
		blockers = append(blockers, "no human review yet")
	case review.Result != Approved:
		blockers = append(blockers, fmt.Sprintf("the latest human review %s is %s", review.ID, review.Result))
	}
	verification, verified := s.LatestVerification(id)
	switch {
	case !verified:
		blockers = append(blockers, "no verification evidence yet")
	case verification.Result != Pass:
		blockers = append(blockers, fmt.Sprintf("the latest verification %s is %s", verification.ID, verification.Result))
	case reviewed && review.Result == Approved && verification.Revision != review.Revision:
		// Approving before verifying is a legitimate order of work, so this is a
		// reason to wait rather than an error. Both must land on one revision
		// before they add up to a completion.
		blockers = append(blockers, fmt.Sprintf("the latest PASS is at revision %s but the approval is at %s",
			shortRevision(verification.Revision), shortRevision(review.Revision)))
	}
	if count := s.OpenGateCount(id); count > 0 {
		blockers = append(blockers, fmt.Sprintf("%d open gate(s)", count))
	}
	if goal := s.goal(item.GoalID); goal == nil || goal.Status != GoalActive {
		status := GoalStatus("unknown")
		if goal != nil {
			status = goal.Status
		}
		blockers = append(blockers, fmt.Sprintf("goal %q is %s, not ACTIVE", item.GoalID, status))
	}
	return blockers
}

// complete moves a Work Item to DONE and unlocks whatever that completion
// satisfies, in the caller's single transaction. Doing both here is what keeps
// any reader from ever seeing "A is done but B is still waiting on it".
func (s *State) complete(id string, now time.Time) {
	item := s.item(id)
	if item == nil {
		return
	}
	item.Status, item.UpdatedAt = Done, now
	s.RefreshReady(now)
}

func shortRevision(revision string) string {
	if len(revision) > 12 {
		return revision[:12]
	}
	return revision
}
