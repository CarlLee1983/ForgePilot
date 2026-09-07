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
