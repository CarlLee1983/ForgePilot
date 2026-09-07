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

const (
	VerificationEvidence EvidenceType = "verification"
	ReviewEvidence       EvidenceType = "review"
)

// Result is the outcome an Evidence record carries. PASS, FAIL and INTERRUPTED
// belong to a Verification Run — INTERRUPTED means no result was produced and
// must never be reported as a failure. APPROVED and REJECTED belong to a Human
// Review: a person's judgement about the same revision the machine checked.
type Result string

const (
	Pass        Result = "PASS"
	Fail        Result = "FAIL"
	Interrupted Result = "INTERRUPTED"
	Approved    Result = "APPROVED"
	Rejected    Result = "REJECTED"
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
	// ExitCode is absent for an INTERRUPTED run: no result was produced, so there
	// is no exit code. Recording a zero would read as success to anything that
	// treats zero as passing.
	ExitCode *int   `json:"exit_code"`
	Result   Result `json:"result"`
	// Reviewer and Note belong to Human Review Evidence alone. Reviewer holds a
	// self-asserted identity, never an authenticated one (ADR-0005); Note holds
	// the free text behind the judgement. Verification Evidence leaves both
	// empty, and is refused if it does not.
	Reviewer string `json:"reviewer"`
	Note     string `json:"note"`
	// PR names the pull request a Human Review was carried out on, as
	// owner/name#number. It is identification alone: nothing reads it when
	// deciding whether work completes or whether Evidence has gone stale
	// (ADR-0011), and it is never checked against GitHub (ADR-0010). Optional,
	// and forbidden on Verification Evidence.
	PR        string    `json:"pr"`
	CreatedAt time.Time `json:"created_at"`
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
	if err := s.gateBlock(id); err != nil {
		return err
	}
	if item.Status != Running && item.Status != Review {
		return fmt.Errorf("work item %q is %s; only RUNNING or REVIEW work can be verified", id, item.Status)
	}
	if goal := s.goal(item.GoalID); goal == nil || goal.Status != GoalActive {
		return fmt.Errorf("work item %q does not belong to an active goal", id)
	}
	return nil
}

// CanBeginVerification reports whether a new Verification Run may start. It also
// admits a Work Item left in VERIFYING, because such a run can only be an orphan:
// the caller reaches this while holding the Work Item's verification lock, so no
// live runner can exist.
//
// Everything that blocks a new run blocks it here too. An abandoned run is not a
// licence to ignore an open Gate: reclaiming that run is a separate act, and one
// that ReclaimRun performs without asking this question.
func (s *State) CanBeginVerification(id string) error {
	item := s.item(id)
	if item != nil && item.Status == Verifying && item.CurrentRun != nil {
		if err := s.gateBlock(id); err != nil {
			return err
		}
		if goal := s.goal(item.GoalID); goal == nil || goal.Status != GoalActive {
			return fmt.Errorf("work item %q does not belong to an active goal", id)
		}
		return nil
	}
	return s.Verifiable(id)
}

// RecordVerification appends the Evidence for a finished Verification Run and
// moves the Work Item accordingly. It never overwrites existing Evidence.
func (s *State) RecordVerification(id, revision, command string, exitCode int, now time.Time) (Evidence, error) {
	result := Pass
	if exitCode != 0 {
		result = Fail
	}
	return s.appendEvidence(id, revision, command, &exitCode, result, now)
}

func (s *State) appendEvidence(id, revision, command string, exitCode *int, result Result, now time.Time) (Evidence, error) {
	item := s.item(id)
	if item == nil {
		return Evidence{}, fmt.Errorf("unknown work item %q", id)
	}
	// Only a Verification Run produces Evidence, so only VERIFYING work can leave
	// it. Without this the domain would offer a jump to REVIEW from any status.
	if item.Status != Verifying {
		return Evidence{}, fmt.Errorf("work item %q is %s; only VERIFYING work can record verification evidence", id, item.Status)
	}
	goal := s.goal(item.GoalID)
	if goal == nil {
		return Evidence{}, fmt.Errorf("work item %q has unknown goal", id)
	}
	if revision == "" {
		return Evidence{}, errors.New("evidence requires a revision")
	}
	evidence := Evidence{
		ID:         s.takeEvidenceID(),
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
		if record.Revision == "" {
			return fmt.Errorf("evidence %q has no revision", record.ID)
		}
		switch record.Type {
		case VerificationEvidence:
			switch record.Result {
			case Pass, Fail, Interrupted:
			default:
				return fmt.Errorf("evidence %q is a verification with result %q", record.ID, record.Result)
			}
			if (record.Result == Interrupted) != (record.ExitCode == nil) {
				return fmt.Errorf("evidence %q pairs result %q with the wrong exit code", record.ID, record.Result)
			}
			if record.Reviewer != "" || record.Note != "" || record.PR != "" {
				return fmt.Errorf("evidence %q is a verification but carries review fields", record.ID)
			}
		case ReviewEvidence:
			switch record.Result {
			case Approved, Rejected:
			default:
				return fmt.Errorf("evidence %q is a review with result %q", record.ID, record.Result)
			}
			// A review is a judgement, not a command that ran: it has no exit code
			// and no command, and recording either would invite a reader to treat
			// one kind of Evidence as the other.
			if record.ExitCode != nil || record.Command != "" {
				return fmt.Errorf("evidence %q is a review but carries verification fields", record.ID)
			}
			if record.Reviewer == "" {
				return fmt.Errorf("evidence %q is a review with no reviewer", record.ID)
			}
			if record.Result == Rejected && strings.TrimSpace(record.Note) == "" {
				return fmt.Errorf("evidence %q rejects without a reason", record.ID)
			}
		default:
			return fmt.Errorf("evidence %q has unknown type %q", record.ID, record.Type)
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
	if s.item(id).CurrentRun != nil {
		return fmt.Errorf("work item %q still has an unreclaimed verification run", id)
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
func (s *State) ReclaimRun(id, command string, now time.Time) (Evidence, string, bool, error) {
	item := s.item(id)
	if item == nil {
		return Evidence{}, "", false, fmt.Errorf("unknown work item %q", id)
	}
	if item.CurrentRun == nil {
		return Evidence{}, "", false, nil
	}
	// Take the worktree path from state rather than recomputing it: state holds
	// where the interrupted run actually ran, which survives changes to the
	// naming scheme or the layout.
	abandoned := item.CurrentRun.WorktreePath
	evidence, err := s.appendEvidence(id, item.CurrentRun.Revision, command, nil, Interrupted, now)
	if err != nil {
		return Evidence{}, "", false, err
	}
	return evidence, abandoned, true, nil
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

// LatestReview returns the most recent Human Review Evidence for a Work Item.
// A newer judgement always wins over an older one: a reviewer is allowed to
// change their mind, and "an APPROVED exists somewhere in the history" would
// turn that into a way past a later rejection.
func (s *State) LatestReview(id string) (Evidence, bool) {
	for i := len(s.Evidence) - 1; i >= 0; i-- {
		if s.Evidence[i].WorkItemID == id && s.Evidence[i].Type == ReviewEvidence {
			return s.Evidence[i], true
		}
	}
	return Evidence{}, false
}

// RecordReview appends a person's judgement about one exact revision. REJECTED
// returns the Work Item to RUNNING so the Agent goes straight back to fixing it.
// APPROVED records the judgement and nothing more here; whether it also completes
// the work is decided by the completion conditions.
func (s *State) RecordReview(id, revision string, result Result, reviewer, note string, now time.Time) (Evidence, error) {
	item := s.item(id)
	if item == nil {
		return Evidence{}, fmt.Errorf("unknown work item %q", id)
	}
	if result != Approved && result != Rejected {
		return Evidence{}, fmt.Errorf("%q is not a review result", result)
	}
	// Only verified work is up for review: reviewing anything else would let a
	// judgement stand in for a check that never ran.
	if item.Status != Review {
		return Evidence{}, fmt.Errorf("work item %q is %s; only REVIEW work can be reviewed", id, item.Status)
	}
	goal := s.goal(item.GoalID)
	if goal == nil {
		return Evidence{}, fmt.Errorf("work item %q has unknown goal", id)
	}
	if revision == "" {
		return Evidence{}, errors.New("a review requires a revision")
	}
	if reviewer == "" {
		return Evidence{}, errors.New("a review requires a reviewer")
	}
	if result == Rejected && strings.TrimSpace(note) == "" {
		return Evidence{}, errors.New("rejecting work requires a reason")
	}
	evidence := Evidence{
		ID:         s.takeEvidenceID(),
		Type:       ReviewEvidence,
		Repository: goal.Repository,
		WorkItemID: item.ID,
		StoryRef:   item.StoryRef,
		Revision:   revision,
		Result:     result,
		Reviewer:   reviewer,
		Note:       note,
		CreatedAt:  now,
	}
	s.Evidence = append(s.Evidence, evidence)
	if result == Rejected {
		item.Status, item.UpdatedAt = Running, now
		return evidence, nil
	}
	// The approval is recorded either way. Whether it also completes the work is
	// decided here, in the same transaction, by conditions rather than by a
	// command anyone could issue.
	if len(s.CompletionBlockers(id)) == 0 {
		s.complete(id, now)
	}
	return evidence, nil
}

// takeEvidenceID hands out the next ID on the single sequence both kinds of
// Evidence share.
func (s *State) takeEvidenceID() string {
	if s.NextEvidenceID < 1 {
		s.NextEvidenceID = 1
	}
	id := fmt.Sprintf("EV-%03d", s.NextEvidenceID)
	s.NextEvidenceID++
	return id
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
	// Completed work is never stale. DONE means "finished at that revision",
	// which later commits do not make false, so the prompt to re-verify would
	// correspond to no action anyone should take (ADR-0006).
	if s.WorkItemStatus(id) == Done {
		return false
	}
	return latest.Revision != revision
}
