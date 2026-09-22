package work

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

// CompletionBlockers reports every condition standing between a Work Item and
// DONE. An empty result means nothing does — either the work is already complete
// or the next approval will complete it.
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
// satisfies, in the caller's single transaction.
func (s *State) complete(id string, now time.Time) {
	item := s.item(id)
	if item == nil {
		return
	}
	item.Status, item.UpdatedAt = Done, now
	s.refreshDependents(id, nil, now)
}

func shortRevision(revision string) string {
	if len(revision) > 12 {
		return revision[:12]
	}
	return revision
}

// GoalCompletionEvidence is the immutable aggregate proof written when a
// VERIFIED-completion Goal crosses its final boundary. The Work Items remain
// VERIFIED; this record names the exact latest Verification Evidence IDs and
// the repository facts observed in the same transaction.
type GoalCompletionEvidence struct {
	ID                      string           `json:"id"`
	GoalID                  string           `json:"goal_id"`
	Repository              string           `json:"repository"`
	CompletionPolicy        CompletionPolicy `json:"completion_policy"`
	Basis                   string           `json:"basis"`
	Revision                string           `json:"revision"`
	SnapshotDigest          string           `json:"snapshot_digest"`
	VerificationEvidenceIDs []string         `json:"verification_evidence_ids"`
	CreatedAt               time.Time        `json:"created_at"`
}

const verifiedCompletionBasis = "CURRENT_VERIFICATION_PASS_NO_OPEN_GATES"

// CompleteVerifiedGoal is the only domain transition for VERIFIED completion.
// It rechecks all conditions before mutating state and returns an immutable
// aggregate record. expectedIDs is the projection the caller acted on; a
// changed set fails closed instead of silently accepting a different Goal.
func (s *State) CompleteVerifiedGoal(id string, repository RepositoryState, expectedIDs []string, now time.Time) (GoalCompletionEvidence, error) {
	goal, err := s.goalInStatus(id, "complete", GoalActive)
	if err != nil {
		return GoalCompletionEvidence{}, err
	}
	if goal.ReviewPolicy != ReviewPerGoal || goal.CompletionPolicy != CompletionVerified {
		return GoalCompletionEvidence{}, fmt.Errorf("goal %q does not use VERIFIED completion policy", id)
	}
	actualIDs, ready := s.goalCompletionEvidence(id, repository)
	if !ready {
		return GoalCompletionEvidence{}, fmt.Errorf("goal %q is not ready to complete: every Work Item must have fresh PASS Evidence and no open Gate", id)
	}
	// The caller must pass the Evidence IDs from the action it just received.
	// Rechecking readiness without pinning it to that exact projection could
	// silently complete a Goal whose verification set changed in between.
	if !sameIDs(actualIDs, expectedIDs) {
		return GoalCompletionEvidence{}, errors.New("goal completion evidence changed while completing; retry the action")
	}
	evidence := GoalCompletionEvidence{
		ID: s.takeGoalCompletionEvidenceID(), GoalID: id, Repository: goal.Repository,
		CompletionPolicy: CompletionVerified, Basis: verifiedCompletionBasis,
		Revision: repository.Revision, SnapshotDigest: repository.SnapshotDigest,
		VerificationEvidenceIDs: append([]string(nil), actualIDs...), CreatedAt: now,
	}
	s.GoalCompletionEvidence = append(s.GoalCompletionEvidence, evidence)
	goal.Status, goal.UpdatedAt = GoalCompleted, now
	return evidence, nil
}

func sameIDs(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func (s *State) takeGoalCompletionEvidenceID() string {
	if s.NextGoalCompletionEvidenceID < 1 {
		s.NextGoalCompletionEvidenceID = 1
	}
	id := fmt.Sprintf("GC-%03d", s.NextGoalCompletionEvidenceID)
	s.NextGoalCompletionEvidenceID++
	return id
}

func parseGoalCompletionEvidenceID(id string) (int, bool) {
	if len(id) < 4 || id[:3] != "GC-" {
		return 0, false
	}
	n, err := strconv.Atoi(id[3:])
	return n, err == nil && n > 0 && fmt.Sprintf("GC-%03d", n) == id
}

// GoalCompletionEvidenceFor returns the one aggregate record for a Goal, if it
// has been completed through the VERIFIED boundary.
func (s *State) GoalCompletionEvidenceFor(goalID string) (GoalCompletionEvidence, bool) {
	for i := len(s.GoalCompletionEvidence) - 1; i >= 0; i-- {
		if s.GoalCompletionEvidence[i].GoalID == goalID {
			return s.GoalCompletionEvidence[i], true
		}
	}
	return GoalCompletionEvidence{}, false
}

func validateGoalCompletionEvidence(s State) error {
	seen := map[string]bool{}
	maxID := 0
	goals := map[string]Goal{}
	items := map[string]Item{}
	for _, goal := range s.Goals {
		goals[goal.ID] = goal
	}
	for _, item := range s.WorkItems {
		items[item.ID] = item
	}
	verificationByID := map[string]Evidence{}
	for _, evidence := range s.Evidence {
		if evidence.Type == VerificationEvidence {
			verificationByID[evidence.ID] = evidence
		}
	}
	for _, evidence := range s.GoalCompletionEvidence {
		number, ok := parseGoalCompletionEvidenceID(evidence.ID)
		if !ok {
			return fmt.Errorf("invalid Goal completion evidence ID %q", evidence.ID)
		}
		if seen[evidence.ID] {
			return fmt.Errorf("duplicate Goal completion evidence %q", evidence.ID)
		}
		seen[evidence.ID] = true
		if number > maxID {
			maxID = number
		}
		goal, ok := goals[evidence.GoalID]
		if !ok {
			return fmt.Errorf("Goal completion evidence %q refers to unknown Goal %q", evidence.ID, evidence.GoalID)
		}
		if goal.LegacyCompletion != nil {
			return fmt.Errorf("Goal %q has both legacy and automatic completion provenance", goal.ID)
		}
		if goal.Status != GoalCompleted || goal.ReviewPolicy != ReviewPerGoal || goal.CompletionPolicy != CompletionVerified {
			return fmt.Errorf("Goal completion evidence %q refers to a Goal without VERIFIED completion policy", evidence.ID)
		}
		if evidence.Repository != goal.Repository || evidence.CompletionPolicy != CompletionVerified || evidence.Basis != verifiedCompletionBasis {
			return fmt.Errorf("Goal completion evidence %q has invalid completion binding", evidence.ID)
		}
		if evidence.Revision == "" && evidence.SnapshotDigest == "" {
			return fmt.Errorf("Goal completion evidence %q has no observed Candidate", evidence.ID)
		}
		if len(evidence.VerificationEvidenceIDs) == 0 {
			return fmt.Errorf("Goal completion evidence %q has no Verification Evidence IDs", evidence.ID)
		}
		seenVerification := map[string]bool{}
		for _, verificationID := range evidence.VerificationEvidenceIDs {
			if seenVerification[verificationID] {
				return fmt.Errorf("Goal completion evidence %q repeats Verification Evidence %q", evidence.ID, verificationID)
			}
			seenVerification[verificationID] = true
			verification, ok := verificationByID[verificationID]
			if !ok || verification.Result != Pass {
				return fmt.Errorf("Goal completion evidence %q refers to non-passing Verification Evidence %q", evidence.ID, verificationID)
			}
			switch verification.CandidateKind {
			case CommitCandidate:
				// A Goal may contain both COMMIT and SNAPSHOT Evidence. The
				// aggregate records every repository fact observed by the
				// transaction, so a COMMIT binding must not reject the digest
				// needed by a sibling SNAPSHOT binding.
				if evidence.Revision == "" || verification.Revision != evidence.Revision {
					return fmt.Errorf("Goal completion evidence %q has a mismatched COMMIT candidate", evidence.ID)
				}
			case SnapshotCandidate:
				if evidence.SnapshotDigest == "" || verification.CandidateDigest != evidence.SnapshotDigest || verification.BaseRevision != evidence.Revision {
					return fmt.Errorf("Goal completion evidence %q has a mismatched SNAPSHOT candidate", evidence.ID)
				}
			default:
				return fmt.Errorf("Goal completion evidence %q refers to Verification Evidence %q with unknown Candidate", evidence.ID, verificationID)
			}
			item, ok := items[verification.WorkItemID]
			if !ok || item.GoalID != evidence.GoalID || item.Status != Verified || verification.Repository != goal.Repository || verification.StoryRef != item.StoryRef {
				return fmt.Errorf("Goal completion evidence %q has Verification Evidence %q outside its Goal", evidence.ID, verificationID)
			}
			latest, ok := s.LatestVerification(item.ID)
			if !ok || latest.ID != verificationID {
				return fmt.Errorf("Goal completion evidence %q does not name the latest Verification Evidence for %s", evidence.ID, item.ID)
			}
			if s.OpenGateCount(item.ID) > 0 {
				return fmt.Errorf("Goal completion evidence %q names Work Item %s with an open Gate", evidence.ID, item.ID)
			}
		}
		goalItemCount := 0
		for _, item := range s.WorkItems {
			if item.GoalID == evidence.GoalID {
				goalItemCount++
			}
		}
		if goalItemCount != len(evidence.VerificationEvidenceIDs) {
			return fmt.Errorf("Goal completion evidence %q does not cover every Work Item", evidence.ID)
		}
	}
	if len(s.GoalCompletionEvidence) > 0 && s.NextGoalCompletionEvidenceID <= maxID {
		return errors.New("next_goal_completion_evidence_id would reuse an ID")
	}
	for _, goal := range s.Goals {
		count := 0
		for _, evidence := range s.GoalCompletionEvidence {
			if evidence.GoalID == goal.ID {
				count++
			}
		}
		if count > 1 {
			return fmt.Errorf("Goal %q has multiple completion evidence records", goal.ID)
		}
		if goal.Status == GoalCompleted && goal.CompletionPolicy == CompletionVerified && count != 1 {
			if goal.LegacyCompletion == nil {
				return fmt.Errorf("completed Goal %q must have one automatic or legacy completion provenance record", goal.ID)
			}
		}
		if goal.Status != GoalCompleted && count != 0 {
			return fmt.Errorf("non-completed Goal %q has completion evidence", goal.ID)
		}
	}
	return nil
}
