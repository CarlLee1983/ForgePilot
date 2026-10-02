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
	ID              string        `json:"id"`
	Type            EvidenceType  `json:"type"`
	Repository      string        `json:"repository"`
	WorkItemID      string        `json:"work_item_id"`
	StoryRef        string        `json:"story_ref"`
	Revision        string        `json:"revision"`
	CandidateKind   CandidateKind `json:"candidate_kind"`
	BaseRevision    string        `json:"base_revision"`
	CandidateDigest string        `json:"candidate_digest"`
	Command         string        `json:"command"`
	// ExitCode is absent for an INTERRUPTED run: no result was produced, so there
	// is no exit code. Recording a zero would read as success to anything that
	// treats zero as passing.
	ExitCode *int   `json:"exit_code"`
	Result   Result `json:"result"`
	// Reviewer and Note belong to Human Review Evidence alone. Reviewer holds a
	// self-asserted identity, never an authenticated one (ADR-0005); Note holds
	// the free text behind the judgement. Verification Evidence leaves both
	// empty, and is refused if it does not.
	Reviewer          string    `json:"reviewer"`
	Note              string    `json:"note"`
	VerificationRunID string    `json:"verification_run_id"`
	CreatedAt         time.Time `json:"created_at"`
}

func (evidence Evidence) Candidate() Candidate {
	return Candidate{Kind: evidence.CandidateKind, Revision: evidence.Revision, BaseRevision: evidence.BaseRevision, Digest: evidence.CandidateDigest}
}

// Verifiable reports whether a Work Item may enter a Verification Run. REVIEW is
// allowed so that a Work Item whose Evidence has gone Stale can be verified again
// against the current revision; re-running against an unchanged revision is also
// permitted, since Evidence only ever accumulates. DONE work is not: it is
// terminal (ADR-0006).
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
//
// A PASS is the whole completion decision for a Goal without an Approval
// Requirement: the work becomes DONE, its dependents are unlocked and, if it was
// the Goal's last unfinished item, the Goal completes, all in this one call. Under
// a Goal that requires approval a PASS moves the work to REVIEW instead. FAIL and
// INTERRUPTED return it to RUNNING.
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
	// it. Without this the domain would offer a jump to DONE from any status.
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
	candidate := item.CurrentRun.Candidate()
	if candidate.Revision != revision {
		return Evidence{}, fmt.Errorf("verification result revision %q does not match the running candidate %q", revision, candidate.Revision)
	}
	if err := candidate.validate(); err != nil {
		return Evidence{}, fmt.Errorf("verification candidate: %w", err)
	}
	evidence := Evidence{
		ID:                s.takeEvidenceID(),
		Type:              VerificationEvidence,
		Repository:        goal.Repository,
		WorkItemID:        item.ID,
		StoryRef:          item.StoryRef,
		Revision:          revision,
		CandidateKind:     candidate.Kind,
		BaseRevision:      candidate.BaseRevision,
		CandidateDigest:   candidate.Digest,
		Command:           command,
		ExitCode:          exitCode,
		Result:            result,
		VerificationRunID: item.CurrentRun.VerificationRunID,
		CreatedAt:         now,
	}
	s.Evidence = append(s.Evidence, evidence)
	item.CurrentRun = nil
	item.UpdatedAt = now
	item.Status = Running
	// A Goal that is no longer ACTIVE keeps the Evidence of a run that was in
	// flight when it was cancelled, but a PASS under it completes nothing.
	if result == Pass && goal.Status == GoalActive {
		if goal.RequireApproval {
			item.Status = Review
		} else {
			s.complete(id, now)
		}
	}
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
		if err := record.Candidate().validate(); err != nil {
			return fmt.Errorf("evidence %q has an invalid candidate: %w", record.ID, err)
		}
		switch record.Type {
		case VerificationEvidence:
			if _, ok := parseVerificationRunID(record.VerificationRunID); !ok {
				return fmt.Errorf("evidence %q has invalid verification run ID %q", record.ID, record.VerificationRunID)
			}
			switch record.Result {
			case Pass, Fail, Interrupted:
			default:
				return fmt.Errorf("evidence %q is a verification with result %q", record.ID, record.Result)
			}
			if (record.Result == Interrupted) != (record.ExitCode == nil) {
				return fmt.Errorf("evidence %q pairs result %q with the wrong exit code", record.ID, record.Result)
			}
			if record.Reviewer != "" || record.Note != "" {
				return fmt.Errorf("evidence %q is a verification but carries review fields", record.ID)
			}
		case ReviewEvidence:
			if record.VerificationRunID != "" {
				return fmt.Errorf("evidence %q is a review but carries a verification run ID", record.ID)
			}
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

func parseVerificationRunID(id string) (int, bool) {
	if !strings.HasPrefix(id, "VR-") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(id, "VR-"))
	return n, err == nil && n > 0 && fmt.Sprintf("VR-%03d", n) == id
}

// NextVerificationRun returns the ID an app may use to exclusive-create its log.
func (s *State) NextVerificationRun() string {
	return fmt.Sprintf("VR-%03d", max(1, s.NextVerificationRunID))
}

// validateVerificationRuns holds each Verification Run ID to one use: the ID is
// the key of the run's log, so two runs sharing one would share a log.
func validateVerificationRuns(s State) error {
	used := map[string]bool{}
	maxID := 0
	for _, item := range s.WorkItems {
		if item.CurrentRun == nil {
			continue
		}
		id := item.CurrentRun.VerificationRunID
		if used[id] {
			return fmt.Errorf("verification run ID %q is active more than once", id)
		}
		used[id] = true
		n, _ := parseVerificationRunID(id)
		maxID = max(maxID, n)
	}
	for _, e := range s.Evidence {
		if e.Type != VerificationEvidence {
			continue
		}
		if used[e.VerificationRunID] {
			return fmt.Errorf("verification run ID %q is used more than once", e.VerificationRunID)
		}
		used[e.VerificationRunID] = true
		n, _ := parseVerificationRunID(e.VerificationRunID)
		maxID = max(maxID, n)
	}

	if s.NextVerificationRunID <= maxID {
		return errors.New("next_verification_run_id would reuse an ID")
	}
	return nil
}

// BeginVerification records that a Verification Run is in flight. The revision is
// stored so that an interrupted run can still be attributed to the exact commit
// it was testing. logPath is recorded the same way as worktreePath: so that
// reclaiming an orphan later points at where that run actually wrote, not at a
// path re-derived from today's naming scheme.
func (s *State) BeginVerification(id, revision, worktreePath, logPath string, now time.Time) error {
	return s.BeginCandidateVerification(id, Candidate{Kind: CommitCandidate, Revision: revision}, worktreePath, logPath, now)
}

// BeginCandidateVerification fixes the immutable Candidate before the canonical
// check starts. Later Evidence is derived from this Run value rather than from
// live repository state.
func (s *State) BeginCandidateVerification(id string, candidate Candidate, worktreePath, logPath string, now time.Time) error {
	return s.BeginCandidateVerificationWithRunID(id, candidate, worktreePath, logPath, s.NextVerificationRun(), now)
}

// BeginCandidateVerificationWithRunID atomically consumes expectedRunID.
func (s *State) BeginCandidateVerificationWithRunID(id string, candidate Candidate, worktreePath, logPath string, expectedRunID string, now time.Time) error {
	if expectedRunID != s.NextVerificationRun() {
		return errors.New("verification run ID changed; retry")
	}
	if err := s.Verifiable(id); err != nil {
		return err
	}
	if s.item(id).CurrentRun != nil {
		return fmt.Errorf("work item %q still has an unreclaimed verification run", id)
	}
	if err := candidate.validate(); err != nil {
		return fmt.Errorf("a verification run requires a valid candidate: %w", err)
	}
	item := s.item(id)
	item.Status = Verifying
	item.CurrentRun = &Run{VerificationRunID: expectedRunID, Revision: candidate.Revision, CandidateKind: candidate.Kind, BaseRevision: candidate.BaseRevision,
		CandidateDigest: candidate.Digest, WorktreePath: worktreePath, LogPath: logPath, StartedAt: now}
	item.UpdatedAt = now
	s.NextVerificationRunID++
	return nil
}

// ReclaimRun records an interrupted Verification Run and returns the Work Item to
// RUNNING. It must only be called once the caller has established that no live
// runner remains. INTERRUPTED means no result was produced: it is never a FAIL,
// and a result is never inferred.
func (s *State) ReclaimRun(id, command string, now time.Time) (Evidence, string, string, bool, error) {
	item := s.item(id)
	if item == nil {
		return Evidence{}, "", "", false, fmt.Errorf("unknown work item %q", id)
	}
	if item.CurrentRun == nil {
		return Evidence{}, "", "", false, nil
	}
	// Take the worktree and log paths from state rather than recomputing them:
	// state holds where the interrupted run actually wrote, which survives
	// changes to the naming scheme or the layout. See
	// docs/adr/0012-verification-log-outside-state.md.
	abandoned := item.CurrentRun.WorktreePath
	logPath := item.CurrentRun.LogPath
	evidence, err := s.appendEvidence(id, item.CurrentRun.Revision, command, nil, Interrupted, now)
	if err != nil {
		return Evidence{}, "", "", false, err
	}
	return evidence, abandoned, logPath, true, nil
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

// RecordReview appends a person's judgement about one exact revision.
func (s *State) RecordReview(id, revision string, result Result, reviewer, note string, now time.Time) (Evidence, error) {
	return s.RecordCandidateReview(id, Candidate{Kind: CommitCandidate, Revision: revision}, result, reviewer, note, now)
}

// RecordCandidateReview binds a Human Review to the exact Candidate it judged.
//
// APPROVED completes the work in the same transaction (unlocking its dependents
// and completing the Goal if this was the last item), so it is refused rather
// than recorded when it could not: the Candidate must still be the one that
// passed verification, and no Gate may be open. REJECTED returns the work to
// RUNNING so the Agent goes straight back to fixing it; it needs a reason and
// is not held back by a Gate or by staleness, since stopping work needs no
// authority that a stale or gated Candidate lacks.
func (s *State) RecordCandidateReview(id string, candidate Candidate, result Result, reviewer, note string, now time.Time) (Evidence, error) {
	if result != Approved && result != Rejected {
		return Evidence{}, fmt.Errorf("%q is not a review result", result)
	}
	if err := s.Reviewable(id); err != nil {
		return Evidence{}, err
	}
	item := s.item(id)
	goal := s.goal(item.GoalID)
	if err := candidate.validate(); err != nil {
		return Evidence{}, fmt.Errorf("a review requires a valid candidate: %w", err)
	}
	if reviewer == "" {
		return Evidence{}, errors.New("a review requires a reviewer")
	}
	if result == Rejected && strings.TrimSpace(note) == "" {
		return Evidence{}, errors.New("rejecting work requires a reason")
	}
	if result == Approved {
		if err := s.gateBlock(id); err != nil {
			return Evidence{}, err
		}
		verification, verified := s.LatestVerification(id)
		if !verified || verification.Result != Pass {
			return Evidence{}, fmt.Errorf("work item %q has no passing verification to approve", id)
		}
		if !sameCandidate(verification.Candidate(), candidate) {
			return Evidence{}, fmt.Errorf("work item %q has a stale verified candidate: its PASS %s no longer names the current candidate; run forgepilot verify %s%s",
				id, verification.ID, id, verificationRetrySuffix(verification.Candidate()))
		}
	}
	evidence := Evidence{
		ID:              s.takeEvidenceID(),
		Type:            ReviewEvidence,
		Repository:      goal.Repository,
		WorkItemID:      item.ID,
		StoryRef:        item.StoryRef,
		Revision:        candidate.Revision,
		CandidateKind:   candidate.Kind,
		BaseRevision:    candidate.BaseRevision,
		CandidateDigest: candidate.Digest,
		Result:          result,
		Reviewer:        reviewer,
		Note:            note,
		CreatedAt:       now,
	}
	s.Evidence = append(s.Evidence, evidence)
	if result == Rejected {
		item.Status, item.UpdatedAt = Running, now
		return evidence, nil
	}
	s.complete(id, now)
	return evidence, nil
}

// sameCandidate reports whether two Candidates name the same code: a COMMIT by
// its revision, a SNAPSHOT by the digest of its tree.
func sameCandidate(left, right Candidate) bool {
	if left.Kind != right.Kind {
		return false
	}
	if left.Kind == SnapshotCandidate {
		return left.Digest == right.Digest
	}
	return left.Revision == right.Revision
}

func verificationRetrySuffix(candidate Candidate) string {
	if candidate.Kind == SnapshotCandidate {
		return " --snapshot"
	}
	return ""
}

// Reviewable checks the durable boundary before a caller resolves repository or
// reviewer facts. A Goal without an Approval Requirement has no review to give,
// and that cannot be changed by satisfying adapter preconditions first.
func (s *State) Reviewable(id string) error {
	item := s.item(id)
	if item == nil {
		return fmt.Errorf("unknown work item %q", id)
	}
	goal := s.goal(item.GoalID)
	if goal == nil {
		return fmt.Errorf("work item %q has unknown goal", id)
	}
	if !goal.RequireApproval {
		return fmt.Errorf("goal %q does not require approval: its work completes when verification passes, so there is nothing to review", goal.ID)
	}
	// Only verified work is up for review: reviewing anything else would let a
	// judgement stand in for a check that never ran.
	if item.Status != Review {
		return fmt.Errorf("work item %q is %s; only REVIEW work can be reviewed", id, item.Status)
	}
	if goal.Status != GoalActive {
		return fmt.Errorf("work item %q does not belong to an active goal", id)
	}
	return nil
}

// ResolveReviewCandidate applies the distinct review targeting contracts. A
// COMMIT review keeps the clean-HEAD target supplied by the caller. A SNAPSHOT
// review may only reuse the latest verified immutable snapshot when the current
// workspace digest still matches it.
func (s *State) ResolveReviewCandidate(id, expectedVerificationID, currentRevision, currentDigest string) (Candidate, error) {
	latest, ok := s.LatestVerification(id)
	if ok && latest.ID != expectedVerificationID {
		return Candidate{}, errors.New("verification changed while preparing review; retry the review")
	}
	if !ok || latest.CandidateKind == CommitCandidate {
		candidate := Candidate{Kind: CommitCandidate, Revision: currentRevision}
		return candidate, candidate.validate()
	}
	if latest.CandidateKind != SnapshotCandidate {
		return Candidate{}, fmt.Errorf("latest verification has unknown candidate kind %q", latest.CandidateKind)
	}
	if latest.CandidateDigest != currentDigest {
		return Candidate{}, fmt.Errorf("verified snapshot is stale: the workspace no longer matches it; run forgepilot verify %s --snapshot", id)
	}
	return latest.Candidate(), nil
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
	return s.CandidateStale(id, revision, "")
}

// CandidateStale compares COMMIT Evidence with HEAD and SNAPSHOT Evidence with
// the deterministic digest of the current workspace candidate.
func (s *State) CandidateStale(id, revision, digest string) bool {
	latest, ok := s.LatestVerification(id)
	if !ok {
		return false
	}
	// Completed work is never stale. DONE means "finished at that revision",
	// which later commits do not make false, so the prompt to re-verify would
	// correspond to no action anyone should take (ADR-0006).
	if s.WorkItemStatus(id) == Done {
		return false
	}
	if latest.CandidateKind == SnapshotCandidate {
		if digest == "" {
			return false
		}
		return latest.CandidateDigest != digest
	}
	if revision == "" {
		return false
	}
	return latest.Revision != revision
}
