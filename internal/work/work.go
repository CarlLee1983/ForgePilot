package work

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const SchemaVersion = 19

// CheckSchemaVersion refuses any state version this binary does not read. Schema
// 19 is a clean break with no upgrade path: older state is exported to Goal
// Plans by tools/export-plan and re-imported, so the refusal says that rather
// than suggesting an in-place upgrade. Callers check this before decoding the
// state strictly, because an older state's fields are unknown to this shape.
func CheckSchemaVersion(version int) error {
	switch {
	case version < SchemaVersion:
		return fmt.Errorf("state uses schema version %d, which is no longer supported: schema %d replaced the earlier model without an upgrade path. Export its unfinished work to Goal Plans with `go run ./tools/export-plan --state <state.json> --out <dir>` from a ForgePilot source checkout, move the old .forgepilot aside, run `forgepilot init`, then `forgepilot goal import` each plan", version, SchemaVersion)
	case version > SchemaVersion:
		return fmt.Errorf("state uses schema version %d, which is newer than this binary supports (%d)", version, SchemaVersion)
	}
	return nil
}

type GoalStatus string

const (
	GoalActive    GoalStatus = "ACTIVE"
	GoalCompleted GoalStatus = "COMPLETED"
	GoalCancelled GoalStatus = "CANCELLED"
)

type Status string

const (
	// NotStarted is the one persisted state of work that has not begun. Whether
	// it is PENDING or READY is not state: it follows from the dependencies at the
	// moment of reading (see Readiness), so completing a dependency never needs to
	// write its dependents.
	NotStarted Status = "NOT_STARTED"
	Running    Status = "RUNNING"
	Verifying  Status = "VERIFYING"
	// Review exists only under a Goal with an Approval Requirement: a PASS on the
	// current Candidate puts the work here to wait for `review approve`.
	Review Status = "REVIEW"
	// Done is terminal. It is reached by a PASS under a Goal without an Approval
	// Requirement, or by an approval under one that has it; nothing sets it
	// directly.
	Done Status = "DONE"
)

type Goal struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Repository  string `json:"repository"`
	// RequireApproval is the Goal's Approval Requirement: when set, a PASS moves
	// work to REVIEW instead of DONE and only `review approve` completes it. It is
	// fixed when the Goal is imported (ADR-0040).
	RequireApproval bool       `json:"require_approval"`
	Status          GoalStatus `json:"status"`
	// Reason explains a cancelled Goal. A status alone says a Goal stopped, not
	// why.
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Item struct {
	ID         string    `json:"id"`
	GoalID     string    `json:"goal_id"`
	StoryRef   string    `json:"story_ref"`
	Status     Status    `json:"status"`
	DependsOn  []string  `json:"depends_on"`
	CurrentRun *Run      `json:"current_run"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Run records a Verification Run that is currently in flight. It is cleared once
// the run produces Evidence, so a non-nil value after the runner has exited marks
// an orphan awaiting reclamation.
type Run struct {
	VerificationRunID string        `json:"verification_run_id"`
	Revision          string        `json:"revision"`
	CandidateKind     CandidateKind `json:"candidate_kind"`
	BaseRevision      string        `json:"base_revision"`
	CandidateDigest   string        `json:"candidate_digest"`
	WorktreePath      string        `json:"worktree_path"`
	// LogPath records where this run's verification output is being streamed, so
	// that reclaiming an orphan points at where it actually wrote rather than a
	// path re-derived from today's naming scheme. It is written when the run
	// begins and vanishes with the rest of the Run once the run produces
	// Evidence. See docs/adr/0012-verification-log-outside-state.md.
	LogPath   string    `json:"log_path"`
	StartedAt time.Time `json:"started_at"`
}

func (run Run) Candidate() Candidate {
	return Candidate{Kind: run.CandidateKind, Revision: run.Revision, BaseRevision: run.BaseRevision, Digest: run.CandidateDigest}
}

type State struct {
	SchemaVersion         int        `json:"schema_version"`
	NextEvidenceID        int        `json:"next_evidence_id"`
	NextGateID            int        `json:"next_gate_id"`
	NextVerificationRunID int        `json:"next_verification_run_id"`
	Goals                 []Goal     `json:"goals"`
	WorkItems             []Item     `json:"work_items"`
	Evidence              []Evidence `json:"evidence"`
	Gates                 []Gate     `json:"gates"`
}

func NewState() State {
	return State{SchemaVersion: SchemaVersion, NextEvidenceID: 1, NextGateID: 1, NextVerificationRunID: 1}
}

// sameStrings treats dependencies as a set: their persisted order is useful for
// presentation, but reordering a node's depends_on list does not change the DAG
// a re-imported plan describes.
func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftValues := make(map[string]bool, len(left))
	for _, value := range left {
		leftValues[value] = true
	}
	rightValues := make(map[string]bool, len(right))
	for _, value := range right {
		if !leftValues[value] {
			return false
		}
		rightValues[value] = true
	}
	return len(leftValues) == len(rightValues)
}

// Start moves NOT_STARTED work to RUNNING. It is refused while the work's own
// dependencies are unfinished, while it has an open Gate, and while another Work
// Item already occupies the workspace (see Occupant).
func (s *State) Start(id string, now time.Time) error {
	item := s.item(id)
	if item == nil {
		return fmt.Errorf("unknown work item %q", id)
	}
	if err := s.gateBlock(id); err != nil {
		return err
	}
	if item.Status != NotStarted {
		return fmt.Errorf("work item %q is %s, not READY", id, item.Status)
	}
	if goal := s.goal(item.GoalID); goal == nil || goal.Status != GoalActive {
		return fmt.Errorf("work item %q does not belong to an active goal", id)
	}
	if pending := s.unfinishedDependencies(item.DependsOn); len(pending) > 0 {
		return fmt.Errorf("work item %q is PENDING: dependencies not DONE: %s", id, strings.Join(pending, ", "))
	}
	if err := s.occupancyBlock(id); err != nil {
		return err
	}
	item.Status, item.UpdatedAt = Running, now
	return nil
}

func (s State) Validate() error {
	if err := CheckSchemaVersion(s.SchemaVersion); err != nil {
		return err
	}
	if s.NextEvidenceID < 1 {
		return errors.New("next_evidence_id must be positive")
	}
	if s.NextGateID < 1 {
		return errors.New("next_gate_id must be positive")
	}
	if s.NextVerificationRunID < 1 {
		return errors.New("next_verification_run_id must be positive")
	}
	goals := map[string]Goal{}
	for _, goal := range s.Goals {
		if !ValidPlanID(goal.ID) || goal.Title == "" || goal.Repository == "" {
			return fmt.Errorf("invalid goal %q", goal.ID)
		}
		switch goal.Status {
		case GoalActive, GoalCompleted:
			if goal.Reason != "" {
				return fmt.Errorf("goal %q is %s but carries a reason for stopping", goal.ID, goal.Status)
			}
		case GoalCancelled:
			if strings.TrimSpace(goal.Reason) == "" {
				return fmt.Errorf("goal %q is %s without a reason", goal.ID, goal.Status)
			}
		default:
			return fmt.Errorf("invalid status for goal %q", goal.ID)
		}
		if _, exists := goals[goal.ID]; exists {
			return fmt.Errorf("duplicate goal %q", goal.ID)
		}
		goals[goal.ID] = goal
	}
	items := map[string]Item{}
	for _, item := range s.WorkItems {
		if !ValidPlanID(item.ID) || item.GoalID == "" || item.StoryRef == "" {
			return fmt.Errorf("invalid work item %q", item.ID)
		}
		goal, ok := goals[item.GoalID]
		if !ok {
			return fmt.Errorf("work item %q has unknown goal", item.ID)
		}
		switch item.Status {
		case NotStarted, Running, Verifying, Done:
		case Review:
			if !goal.RequireApproval {
				return fmt.Errorf("work item %q is REVIEW but goal %q requires no approval", item.ID, goal.ID)
			}
		default:
			return fmt.Errorf("invalid status for %q", item.ID)
		}
		if item.Status == Verifying && item.CurrentRun == nil {
			return fmt.Errorf("work item %q is VERIFYING without a current run", item.ID)
		}
		if item.Status != Verifying && item.CurrentRun != nil {
			return fmt.Errorf("work item %q is %s but carries a current run", item.ID, item.Status)
		}
		if item.CurrentRun != nil {
			if _, ok := parseVerificationRunID(item.CurrentRun.VerificationRunID); !ok {
				return fmt.Errorf("work item %q has invalid verification run ID %q", item.ID, item.CurrentRun.VerificationRunID)
			}
			if err := item.CurrentRun.Candidate().validate(); err != nil {
				return fmt.Errorf("work item %q has an invalid current run candidate: %w", item.ID, err)
			}
		}
		if _, exists := items[item.ID]; exists {
			return fmt.Errorf("duplicate work item %q", item.ID)
		}
		items[item.ID] = item
	}
	occupied := ""
	for _, item := range s.WorkItems {
		if item.Status != Running && item.Status != Verifying || goals[item.GoalID].Status != GoalActive {
			continue
		}
		if occupied != "" {
			return fmt.Errorf("work items %q and %q are both in progress; a workspace allows one RUNNING or VERIFYING work item", occupied, item.ID)
		}
		occupied = item.ID
	}
	for _, item := range s.WorkItems {
		seen := map[string]bool{}
		for _, dependency := range item.DependsOn {
			if dependency == item.ID {
				return fmt.Errorf("work item %q depends on itself", item.ID)
			}
			if seen[dependency] {
				return fmt.Errorf("work item %q has duplicate dependency", item.ID)
			}
			seen[dependency] = true
			parent, ok := items[dependency]
			if !ok {
				return fmt.Errorf("work item %q has unknown dependency %q", item.ID, dependency)
			}
			if parent.GoalID != item.GoalID {
				return fmt.Errorf("work item %q has cross-goal dependency", item.ID)
			}
		}
	}
	if err := validateEvidence(s.Evidence, s.NextEvidenceID, items); err != nil {
		return err
	}
	for _, evidence := range s.Evidence {
		item := items[evidence.WorkItemID]
		goal := goals[item.GoalID]
		if evidence.Repository != goal.Repository {
			return fmt.Errorf("evidence %q repository does not match work item %q", evidence.ID, item.ID)
		}
		if evidence.StoryRef != item.StoryRef {
			return fmt.Errorf("evidence %q story reference does not match work item %q", evidence.ID, item.ID)
		}
		if evidence.Type == ReviewEvidence && !goal.RequireApproval {
			return fmt.Errorf("evidence %q is a review under goal %q, which requires no approval", evidence.ID, goal.ID)
		}
	}
	if err := validateVerificationRuns(s); err != nil {
		return err
	}
	for _, item := range s.WorkItems {
		if item.Status != Review {
			continue
		}
		verification, ok := s.LatestVerification(item.ID)
		if !ok || verification.Result != Pass {
			return fmt.Errorf("work item %q is REVIEW without a latest PASS Verification", item.ID)
		}
	}
	if err := validateGates(s.Gates, s.NextGateID, items); err != nil {
		return err
	}
	for _, goal := range s.Goals {
		if goal.Status != GoalCompleted {
			continue
		}
		for _, item := range s.WorkItems {
			if item.GoalID == goal.ID && item.Status != Done {
				return fmt.Errorf("completed goal %q has unfinished work: %s is %s", goal.ID, item.ID, item.Status)
			}
		}
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("dependency cycle at %q", id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, parent := range items[id].DependsOn {
			if err := visit(parent); err != nil {
				return err
			}
		}
		visiting[id], visited[id] = false, true
		return nil
	}
	for id := range items {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func (s *State) goal(id string) *Goal {
	for i := range s.Goals {
		if s.Goals[i].ID == id {
			return &s.Goals[i]
		}
	}
	return nil
}
func (s *State) item(id string) *Item {
	for i := range s.WorkItems {
		if s.WorkItems[i].ID == id {
			return &s.WorkItems[i]
		}
	}
	return nil
}

// GoalByID returns one Goal without exposing State's storage representation to
// a caller that only needs a read-only query result.
func (s *State) GoalByID(id string) (Goal, bool) {
	goal := s.goal(id)
	if goal == nil {
		return Goal{}, false
	}
	return *goal, true
}

// unfinishedDependencies names the dependencies that are not DONE, in the
// order the item lists them. A passing Verification that is still awaiting
// approval does not count, because REVIEW is not DONE.
func (s *State) unfinishedDependencies(ids []string) []string {
	var pending []string
	for _, id := range ids {
		if item := s.item(id); item == nil || item.Status != Done {
			pending = append(pending, id)
		}
	}
	return pending
}
