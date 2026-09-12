package work

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const SchemaVersion = 6

type GoalStatus string

const (
	GoalActive    GoalStatus = "ACTIVE"
	GoalBlocked   GoalStatus = "BLOCKED"
	GoalCompleted GoalStatus = "COMPLETED"
	GoalCancelled GoalStatus = "CANCELLED"
)

type Status string

const (
	Pending   Status = "PENDING"
	Ready     Status = "READY"
	Running   Status = "RUNNING"
	Verifying Status = "VERIFYING"
	Review    Status = "REVIEW"
	// Done is terminal and is only ever reached by satisfying the completion
	// conditions in RecordReview; nothing sets it directly.
	Done Status = "DONE"
)

type Goal struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Repository  string     `json:"repository"`
	Status      GoalStatus `json:"status"`
	// Reason explains a Goal that was blocked or cancelled. Neither is worth
	// recording without one: the status alone says a Goal stopped, not why.
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
	Revision        string        `json:"revision"`
	CandidateKind   CandidateKind `json:"candidate_kind"`
	BaseRevision    string        `json:"base_revision"`
	CandidateDigest string        `json:"candidate_digest"`
	WorktreePath    string        `json:"worktree_path"`
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
	SchemaVersion  int        `json:"schema_version"`
	NextWorkID     int        `json:"next_work_id"`
	NextEvidenceID int        `json:"next_evidence_id"`
	NextGateID     int        `json:"next_gate_id"`
	Goals          []Goal     `json:"goals"`
	WorkItems      []Item     `json:"work_items"`
	Evidence       []Evidence `json:"evidence"`
	Gates          []Gate     `json:"gates"`
}

func NewState() State {
	return State{SchemaVersion: SchemaVersion, NextWorkID: 1, NextEvidenceID: 1, NextGateID: 1}
}

func (s *State) AddGoal(id, title, description, repository string, now time.Time) error {
	if id == "" || title == "" {
		return errors.New("goal id and title are required")
	}
	if s.goal(id) != nil {
		return fmt.Errorf("goal %q already exists", id)
	}
	s.Goals = append(s.Goals, Goal{ID: id, Title: title, Description: description, Repository: repository, Status: GoalActive, CreatedAt: now, UpdatedAt: now})
	return nil
}

func (s *State) AddWork(goalID, story string, dependencies []string, now time.Time) (Item, error) {
	goal := s.goal(goalID)
	if goal == nil {
		return Item{}, fmt.Errorf("unknown goal %q", goalID)
	}
	if goal.Status != GoalActive {
		return Item{}, fmt.Errorf("goal %q is not active", goalID)
	}
	seen := map[string]bool{}
	for _, dependency := range dependencies {
		if seen[dependency] {
			return Item{}, fmt.Errorf("duplicate dependency %q", dependency)
		}
		seen[dependency] = true
		item := s.item(dependency)
		if item == nil {
			return Item{}, fmt.Errorf("unknown dependency %q", dependency)
		}
		if item.GoalID != goalID {
			return Item{}, fmt.Errorf("dependency %q belongs to another goal", dependency)
		}
	}
	if s.NextWorkID < 1 {
		s.NextWorkID = 1
	}
	id := fmt.Sprintf("WI-%03d", s.NextWorkID)
	s.NextWorkID++
	status := Ready
	if !s.dependenciesDone(dependencies) {
		status = Pending
	}
	created := Item{ID: id, GoalID: goalID, StoryRef: story, Status: status, DependsOn: append([]string(nil), dependencies...), CreatedAt: now, UpdatedAt: now}
	s.WorkItems = append(s.WorkItems, created)
	return created, nil
}

// RefreshReady promotes PENDING work whose dependencies have all completed. It
// only promotes within an ACTIVE Goal: Next already refuses to select anything
// else, and marking work READY under a Goal that is paused or abandoned would
// leave two parts of the product disagreeing about the same fact.
func (s *State) RefreshReady(now time.Time) {
	for i := range s.WorkItems {
		if s.WorkItems[i].Status != Pending || !s.dependenciesDone(s.WorkItems[i].DependsOn) {
			continue
		}
		if goal := s.goal(s.WorkItems[i].GoalID); goal == nil || goal.Status != GoalActive {
			continue
		}
		s.WorkItems[i].Status, s.WorkItems[i].UpdatedAt = Ready, now
	}
}

func (s *State) Next() (Item, bool) {
	items := make([]Item, 0, len(s.WorkItems))
	for _, item := range s.WorkItems {
		if item.Status == Ready && s.dependenciesDone(item.DependsOn) && s.OpenGateCount(item.ID) == 0 {
			if goal := s.goal(item.GoalID); goal != nil && goal.Status == GoalActive {
				items = append(items, item)
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return workNumber(items[i].ID) < workNumber(items[j].ID)
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	if len(items) == 0 {
		return Item{}, false
	}
	return items[0], true
}

func (s *State) Start(id string, now time.Time) error {
	item := s.item(id)
	if item == nil {
		return fmt.Errorf("unknown work item %q", id)
	}
	if err := s.gateBlock(id); err != nil {
		return err
	}
	if item.Status != Ready {
		return fmt.Errorf("work item %q is %s, not READY", id, item.Status)
	}
	if goal := s.goal(item.GoalID); goal == nil || goal.Status != GoalActive {
		return fmt.Errorf("work item %q does not belong to an active goal", id)
	}
	if !s.dependenciesDone(item.DependsOn) {
		return fmt.Errorf("work item %q has incomplete dependencies", id)
	}
	item.Status, item.UpdatedAt = Running, now
	return nil
}

func (s State) Validate() error {
	if s.SchemaVersion < SchemaVersion {
		return fmt.Errorf("state uses schema version %d; run forgepilot migrate to upgrade it to %d", s.SchemaVersion, SchemaVersion)
	}
	if s.SchemaVersion > SchemaVersion {
		return fmt.Errorf("state uses schema version %d, which is newer than this binary supports (%d)", s.SchemaVersion, SchemaVersion)
	}
	if s.NextWorkID < 1 {
		return errors.New("next_work_id must be positive")
	}
	if s.NextEvidenceID < 1 {
		return errors.New("next_evidence_id must be positive")
	}
	if s.NextGateID < 1 {
		return errors.New("next_gate_id must be positive")
	}
	goals := map[string]Goal{}
	for _, goal := range s.Goals {
		if goal.ID == "" || goal.Title == "" || goal.Repository == "" {
			return fmt.Errorf("invalid goal %q", goal.ID)
		}
		switch goal.Status {
		case GoalActive, GoalCompleted:
			if goal.Reason != "" {
				return fmt.Errorf("goal %q is %s but carries a reason for stopping", goal.ID, goal.Status)
			}
		case GoalBlocked, GoalCancelled:
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
	maxID := 0
	for _, item := range s.WorkItems {
		if item.ID == "" || item.GoalID == "" || item.StoryRef == "" {
			return fmt.Errorf("invalid work item %q", item.ID)
		}
		if _, ok := parseWorkID(item.ID); !ok {
			return fmt.Errorf("invalid work item ID %q", item.ID)
		}
		if _, ok := goals[item.GoalID]; !ok {
			return fmt.Errorf("work item %q has unknown goal", item.ID)
		}
		switch item.Status {
		case Pending, Ready, Running, Verifying, Review, Done:
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
			if err := item.CurrentRun.Candidate().validate(); err != nil {
				return fmt.Errorf("work item %q has an invalid current run candidate: %w", item.ID, err)
			}
		}
		if _, exists := items[item.ID]; exists {
			return fmt.Errorf("duplicate work item %q", item.ID)
		}
		items[item.ID] = item
		if n := workNumber(item.ID); n > maxID {
			maxID = n
		}
	}
	if s.NextWorkID <= maxID {
		return errors.New("next_work_id would reuse an ID")
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
	if err := validateGates(s.Gates, s.NextGateID, items); err != nil {
		return err
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
func (s *State) dependenciesDone(ids []string) bool {
	for _, id := range ids {
		if item := s.item(id); item == nil || item.Status != Done {
			return false
		}
	}
	return true
}
func workNumber(id string) int { n, _ := parseWorkID(id); return n }

func parseWorkID(id string) (int, bool) {
	if !strings.HasPrefix(id, "WI-") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(id, "WI-"))
	return n, err == nil && n > 0 && fmt.Sprintf("WI-%03d", n) == id
}
