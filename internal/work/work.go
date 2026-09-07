package work

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const SchemaVersion = 2

type GoalStatus string

const (
	GoalActive    GoalStatus = "ACTIVE"
	GoalBlocked   GoalStatus = "BLOCKED"
	GoalCompleted GoalStatus = "COMPLETED"
	GoalCancelled GoalStatus = "CANCELLED"
)

type Status string

const (
	Pending Status = "PENDING"
	Ready   Status = "READY"
	Running Status = "RUNNING"
	Done    Status = "DONE" // Reserved for M3 fixtures; no M1 command can set it.
)

type Goal struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Repository  string     `json:"repository"`
	Status      GoalStatus `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
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
	Revision     string    `json:"revision"`
	WorktreePath string    `json:"worktree_path"`
	StartedAt    time.Time `json:"started_at"`
}

type State struct {
	SchemaVersion  int        `json:"schema_version"`
	NextWorkID     int        `json:"next_work_id"`
	NextEvidenceID int        `json:"next_evidence_id"`
	Goals          []Goal     `json:"goals"`
	WorkItems      []Item     `json:"work_items"`
	Evidence       []Evidence `json:"evidence"`
}

func NewState() State {
	return State{SchemaVersion: SchemaVersion, NextWorkID: 1, NextEvidenceID: 1}
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

func (s *State) RefreshReady(now time.Time) {
	for i := range s.WorkItems {
		if s.WorkItems[i].Status == Pending && s.dependenciesDone(s.WorkItems[i].DependsOn) {
			s.WorkItems[i].Status, s.WorkItems[i].UpdatedAt = Ready, now
		}
	}
}

func (s *State) Next() (Item, bool) {
	items := make([]Item, 0, len(s.WorkItems))
	for _, item := range s.WorkItems {
		if item.Status == Ready && s.dependenciesDone(item.DependsOn) {
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
	goals := map[string]Goal{}
	for _, goal := range s.Goals {
		if goal.ID == "" || goal.Title == "" || goal.Repository == "" || (goal.Status != GoalActive && goal.Status != GoalBlocked && goal.Status != GoalCompleted && goal.Status != GoalCancelled) {
			return fmt.Errorf("invalid goal %q", goal.ID)
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
		if item.Status != Pending && item.Status != Ready && item.Status != Running && item.Status != Done {
			return fmt.Errorf("invalid status for %q", item.ID)
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
