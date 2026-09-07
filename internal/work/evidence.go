package work

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// EvidenceType discriminates the kinds of Evidence a Work Item accumulates. Only
// verification exists in M2, but the field is written from the first release so
// that immutable history stays readable when Human Review Evidence arrives.
type EvidenceType string

const VerificationEvidence EvidenceType = "verification"

// VerificationCommand names the canonical check in Evidence. The domain records
// the command; it never runs one.
const VerificationCommand = "make verify"

// Result is the outcome of a Verification Run. Interrupted means no result was
// produced; it must never be reported as a failure.
type Result string

const (
	Pass        Result = "PASS"
	Fail        Result = "FAIL"
	Interrupted Result = "INTERRUPTED"
)

// Evidence is an immutable record binding one outcome to one exact revision.
type Evidence struct {
	ID         string       `json:"id"`
	Type       EvidenceType `json:"type"`
	Repository string       `json:"repository"`
	WorkItemID string       `json:"work_item_id"`
	StoryRef   string       `json:"story_ref"`
	Revision   string       `json:"revision"`
	Command    string       `json:"command"`
	ExitCode   int          `json:"exit_code"`
	Result     Result       `json:"result"`
	CreatedAt  time.Time    `json:"created_at"`
}

// Verifiable reports whether a Work Item may enter a Verification Run. REVIEW is
// allowed so that a Work Item whose Evidence has gone Stale can be verified again
// against the current revision; re-running against an unchanged revision is also
// permitted, since Evidence only ever accumulates.
func (s *State) Verifiable(id string) error {
	item := s.item(id)
	if item == nil {
		return fmt.Errorf("unknown work item %q", id)
	}
	if item.Status != Running && item.Status != Review {
		return fmt.Errorf("work item %q is %s; only RUNNING or REVIEW work can be verified", id, item.Status)
	}
	if goal := s.goal(item.GoalID); goal == nil || goal.Status != GoalActive {
		return fmt.Errorf("work item %q does not belong to an active goal", id)
	}
	return nil
}

// RecordVerification appends the Evidence for a finished Verification Run and
// moves the Work Item accordingly. It never overwrites existing Evidence.
func (s *State) RecordVerification(id, revision, command string, exitCode int, now time.Time) (Evidence, error) {
	result := Pass
	if exitCode != 0 {
		result = Fail
	}
	return s.appendEvidence(id, revision, command, exitCode, result, now)
}

func (s *State) appendEvidence(id, revision, command string, exitCode int, result Result, now time.Time) (Evidence, error) {
	item := s.item(id)
	if item == nil {
		return Evidence{}, fmt.Errorf("unknown work item %q", id)
	}
	goal := s.goal(item.GoalID)
	if goal == nil {
		return Evidence{}, fmt.Errorf("work item %q has unknown goal", id)
	}
	if revision == "" {
		return Evidence{}, errors.New("evidence requires a revision")
	}
	if s.NextEvidenceID < 1 {
		s.NextEvidenceID = 1
	}
	evidence := Evidence{
		ID:         fmt.Sprintf("EV-%03d", s.NextEvidenceID),
		Type:       VerificationEvidence,
		Repository: goal.Repository,
		WorkItemID: item.ID,
		StoryRef:   item.StoryRef,
		Revision:   revision,
		Command:    command,
		ExitCode:   exitCode,
		Result:     result,
		CreatedAt:  now,
	}
	s.NextEvidenceID++
	s.Evidence = append(s.Evidence, evidence)
	item.CurrentRun = nil
	switch result {
	case Pass:
		item.Status = Review
	default:
		item.Status = Running
	}
	item.UpdatedAt = now
	return evidence, nil
}

func validateEvidence(evidence []Evidence, nextID int, items map[string]Item) error {
	seen := map[string]bool{}
	maxID := 0
	for _, record := range evidence {
		number, ok := parseEvidenceID(record.ID)
		if !ok {
			return fmt.Errorf("invalid evidence ID %q", record.ID)
		}
		if seen[record.ID] {
			return fmt.Errorf("duplicate evidence %q", record.ID)
		}
		seen[record.ID] = true
		if record.Type != VerificationEvidence {
			return fmt.Errorf("evidence %q has unknown type %q", record.ID, record.Type)
		}
		switch record.Result {
		case Pass, Fail, Interrupted:
		default:
			return fmt.Errorf("evidence %q has unknown result %q", record.ID, record.Result)
		}
		if record.Revision == "" {
			return fmt.Errorf("evidence %q has no revision", record.ID)
		}
		if _, ok := items[record.WorkItemID]; !ok {
			return fmt.Errorf("evidence %q refers to unknown work item %q", record.ID, record.WorkItemID)
		}
		if number > maxID {
			maxID = number
		}
	}
	if nextID <= maxID {
		return errors.New("next_evidence_id would reuse an ID")
	}
	return nil
}

func parseEvidenceID(id string) (int, bool) {
	if !strings.HasPrefix(id, "EV-") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(id, "EV-"))
	return n, err == nil && n > 0 && fmt.Sprintf("EV-%03d", n) == id
}

// BeginVerification records that a Verification Run is in flight. The revision is
// stored so that an interrupted run can still be attributed to the exact commit
// it was testing.
func (s *State) BeginVerification(id, revision, worktreePath string, now time.Time) error {
	if err := s.Verifiable(id); err != nil {
		return err
	}
	if revision == "" {
		return errors.New("a verification run requires a revision")
	}
	item := s.item(id)
	item.Status = Verifying
	item.CurrentRun = &Run{Revision: revision, WorktreePath: worktreePath, StartedAt: now}
	item.UpdatedAt = now
	return nil
}

// ReclaimRun records an interrupted Verification Run and returns the Work Item to
// RUNNING. It must only be called once the caller has established that no live
// runner remains. INTERRUPTED means no result was produced: it is never a FAIL,
// and a result is never inferred.
func (s *State) ReclaimRun(id string, now time.Time) (Evidence, bool, error) {
	item := s.item(id)
	if item == nil {
		return Evidence{}, false, fmt.Errorf("unknown work item %q", id)
	}
	if item.CurrentRun == nil {
		return Evidence{}, false, nil
	}
	evidence, err := s.appendEvidence(id, item.CurrentRun.Revision, VerificationCommand, 0, Interrupted, now)
	if err != nil {
		return Evidence{}, false, err
	}
	return evidence, true, nil
}

// WorkItemStatus reports a Work Item's current status, or an empty status when no
// such item exists.
func (s *State) WorkItemStatus(id string) Status {
	if item := s.item(id); item != nil {
		return item.Status
	}
	return ""
}

// LatestVerification returns the most recent Verification Evidence for a Work
// Item. Evidence is only ever appended, so the last matching record is the
// current one.
func (s *State) LatestVerification(id string) (Evidence, bool) {
	for i := len(s.Evidence) - 1; i >= 0; i-- {
		if s.Evidence[i].WorkItemID == id && s.Evidence[i].Type == VerificationEvidence {
			return s.Evidence[i], true
		}
	}
	return Evidence{}, false
}

// Stale reports whether a Work Item's latest Verification Evidence was produced
// against a revision other than the given one. Work that has never been verified
// is not stale — it is unverified, which callers must present differently: an
// absent result must never read as an untroubled one.
func (s *State) Stale(id, revision string) bool {
	latest, ok := s.LatestVerification(id)
	if !ok || revision == "" {
		return false
	}
	return latest.Revision != revision
}
