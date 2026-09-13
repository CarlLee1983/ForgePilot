package work

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const SchemaVersion = 8

type GoalStatus string

const (
	GoalActive    GoalStatus = "ACTIVE"
	GoalBlocked   GoalStatus = "BLOCKED"
	GoalCompleted GoalStatus = "COMPLETED"
	GoalCancelled GoalStatus = "CANCELLED"
)

// ReviewPolicy places the routine Human Review boundary. It is durable Goal
// policy, never a per-command permission to skip review.
type ReviewPolicy string

const (
	ReviewPerWorkItem ReviewPolicy = "WORK_ITEM"
	ReviewPerGoal     ReviewPolicy = "GOAL"
)

type Status string

const (
	Pending   Status = "PENDING"
	Ready     Status = "READY"
	Running   Status = "RUNNING"
	Verifying Status = "VERIFYING"
	Review    Status = "REVIEW"
	// Verified means machine verification passed under a Goal-level review
	// policy. It satisfies dependency progression but is not Human acceptance.
	Verified Status = "VERIFIED"
	// Done is terminal and is only ever reached by satisfying the completion
	// conditions in RecordReview; nothing sets it directly.
	Done Status = "DONE"
)

type Goal struct {
	ID           string       `json:"id"`
	Title        string       `json:"title"`
	Description  string       `json:"description"`
	Repository   string       `json:"repository"`
	Status       GoalStatus   `json:"status"`
	ReviewPolicy ReviewPolicy `json:"review_policy"`
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
	LogPath   string            `json:"log_path"`
	Runtime   map[string]string `json:"runtime,omitempty"`
	StartedAt time.Time         `json:"started_at"`
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
	return s.AddGoalWithReviewPolicy(id, title, description, repository, ReviewPerWorkItem, now)
}

func (s *State) AddGoalWithReviewPolicy(id, title, description, repository string, policy ReviewPolicy, now time.Time) error {
	if id == "" || title == "" {
		return errors.New("goal id and title are required")
	}
	if !validReviewPolicy(policy) {
		return fmt.Errorf("invalid review policy %q", policy)
	}
	if s.goal(id) != nil {
		return fmt.Errorf("goal %q already exists", id)
	}
	s.Goals = append(s.Goals, Goal{ID: id, Title: title, Description: description, Repository: repository, Status: GoalActive, ReviewPolicy: policy, CreatedAt: now, UpdatedAt: now})
	return nil
}

func validReviewPolicy(policy ReviewPolicy) bool {
	return policy == ReviewPerWorkItem || policy == ReviewPerGoal
}

func (s *State) AddWork(goalID, story string, dependencies []string, now time.Time) (Item, error) {
	return s.addWork(goalID, story, dependencies, nil, now)
}

// AddWorkWithRepository applies current repository facts when deciding whether
// VERIFIED dependencies make the new Work Item READY.
func (s *State) AddWorkWithRepository(goalID, story string, dependencies []string, repository RepositoryState, now time.Time) (Item, error) {
	return s.addWork(goalID, story, dependencies, &repository, now)
}

func (s *State) addWork(goalID, story string, dependencies []string, repository *RepositoryState, now time.Time) (Item, error) {
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
	if !s.dependenciesSatisfiedAt(dependencies, repository) {
		status = Pending
	}
	created := Item{ID: id, GoalID: goalID, StoryRef: story, Status: status, DependsOn: append([]string(nil), dependencies...), CreatedAt: now, UpdatedAt: now}
	s.WorkItems = append(s.WorkItems, created)
	return created, nil
}

// RefreshReady reconciles PENDING and READY work with the dependency progression
// policy. It only changes work within an ACTIVE Goal: pausing a Goal must not
// rewrite its Work Item statuses merely because the pause itself temporarily
// prevents progression.
func (s *State) RefreshReady(now time.Time) {
	s.refreshReady(nil, now)
}

// RefreshReadyWithRepository may promote work whose VERIFIED dependencies are
// still bound to the current Candidate. Without these facts RefreshReady is
// deliberately conservative and leaves such work PENDING.
func (s *State) RefreshReadyWithRepository(repository RepositoryState, now time.Time) {
	s.refreshReady(&repository, now)
}

func (s *State) refreshReady(repository *RepositoryState, now time.Time) {
	for i := range s.WorkItems {
		s.reconcileReady(&s.WorkItems[i], repository, now)
	}
}

func (s *State) refreshDependents(dependencyID string, repository *RepositoryState, now time.Time) {
	for i := range s.WorkItems {
		for _, id := range s.WorkItems[i].DependsOn {
			if id == dependencyID {
				s.reconcileReady(&s.WorkItems[i], repository, now)
				break
			}
		}
	}
}

func (s *State) refreshGoal(goalID string, repository *RepositoryState, now time.Time) {
	for i := range s.WorkItems {
		if s.WorkItems[i].GoalID == goalID {
			s.reconcileReady(&s.WorkItems[i], repository, now)
		}
	}
}

func (s *State) reconcileReady(item *Item, repository *RepositoryState, now time.Time) {
	if item.Status != Pending && item.Status != Ready {
		return
	}
	if goal := s.goal(item.GoalID); goal == nil || goal.Status != GoalActive {
		return
	}
	target := Pending
	if s.dependenciesSatisfiedAt(item.DependsOn, repository) {
		target = Ready
	}
	if item.Status != target {
		item.Status, item.UpdatedAt = target, now
	}
}

func (s *State) Next() (Item, bool) {
	return s.next(nil)
}

// NextWithRepository applies Candidate freshness when VERIFIED dependencies are
// involved. Read-only CLI projections use it so missing or stale repository
// facts fail closed rather than presenting downstream work as startable.
func (s *State) NextWithRepository(repository RepositoryState) (Item, bool) {
	return s.next(&repository)
}

func (s *State) next(repository *RepositoryState) (Item, bool) {
	items := make([]Item, 0, len(s.WorkItems))
	for _, item := range s.WorkItems {
		if item.Status == Ready && s.dependenciesSatisfiedAt(item.DependsOn, repository) && s.OpenGateCount(item.ID) == 0 {
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
	return s.start(id, nil, now)
}

// StartWithRepository applies the same transition with current repository facts
// so a VERIFIED dependency whose Candidate is stale cannot authorize new work.
func (s *State) StartWithRepository(id string, repository RepositoryState, now time.Time) error {
	return s.start(id, &repository, now)
}

func (s *State) start(id string, repository *RepositoryState, now time.Time) error {
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
	if !s.dependenciesProgressionAuthorized(item.DependsOn) {
		return fmt.Errorf("work item %q has incomplete dependencies", id)
	}
	for _, dependencyID := range item.DependsOn {
		dependency := s.item(dependencyID)
		if dependency != nil && dependency.Status != Done && repository == nil {
			return fmt.Errorf("work item %q requires current repository facts for VERIFIED dependency %q", id, dependencyID)
		}
	}
	if repository != nil && !s.dependenciesSatisfiedAt(item.DependsOn, repository) {
		return fmt.Errorf("work item %q has a stale dependency; verify it against the current candidate first", id)
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
		if !validReviewPolicy(goal.ReviewPolicy) {
			return fmt.Errorf("goal %q has invalid review policy %q", goal.ID, goal.ReviewPolicy)
		}
		if goal.ReviewPolicy == ReviewPerGoal && goal.Status == GoalCompleted {
			return fmt.Errorf("goal %q uses GOAL review policy but is COMPLETED without final-review evidence", goal.ID)
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
		goal := goals[item.GoalID]
		if goal.ReviewPolicy == ReviewPerGoal && (item.Status == Review || item.Status == Done) {
			return fmt.Errorf("work item %q is %s under GOAL review policy", item.ID, item.Status)
		}
		switch item.Status {
		case Pending, Ready, Running, Verifying, Review, Done:
		case Verified:
			if goals[item.GoalID].ReviewPolicy != ReviewPerGoal {
				return fmt.Errorf("work item %q is VERIFIED under %s review policy", item.ID, goals[item.GoalID].ReviewPolicy)
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
			if err := item.CurrentRun.Candidate().validate(); err != nil {
				return fmt.Errorf("work item %q has an invalid current run candidate: %w", item.ID, err)
			}
			if err := validateRuntime(item.CurrentRun.Runtime); err != nil {
				return fmt.Errorf("work item %q has invalid current run runtime: %w", item.ID, err)
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
	for _, item := range s.WorkItems {
		if item.Status != Verified {
			continue
		}
		verification, ok := s.LatestVerification(item.ID)
		if !ok || verification.Result != Pass {
			return fmt.Errorf("work item %q is VERIFIED without a latest PASS Verification", item.ID)
		}
	}
	for _, evidence := range s.Evidence {
		item := items[evidence.WorkItemID]
		if evidence.Type == ReviewEvidence && goals[item.GoalID].ReviewPolicy == ReviewPerGoal {
			return fmt.Errorf("evidence %q is a Work Item review under GOAL review policy", evidence.ID)
		}
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
func (s *State) dependenciesSatisfiedAt(ids []string, repository *RepositoryState) bool {
	for _, id := range ids {
		item := s.item(id)
		if item == nil || !s.satisfiesDependency(*item) {
			return false
		}
		if item.Status != Done {
			if repository == nil {
				return false
			}
			verification, ok := s.LatestVerification(id)
			if !ok || verification.Result != Pass || !candidateMatchesRepository(verification, *repository) {
				return false
			}
		}
	}
	return true
}

func (s *State) dependenciesProgressionAuthorized(ids []string) bool {
	for _, id := range ids {
		item := s.item(id)
		if item == nil || !s.satisfiesDependency(*item) {
			return false
		}
	}
	return true
}

// satisfiesDependency is the single progression rule. VERIFIED is deliberately
// not a second spelling of DONE: it only satisfies a dependency when the owning
// Goal chose Goal-level review and no Gate still withholds authority.
func (s *State) satisfiesDependency(item Item) bool {
	if item.Status == Done {
		return true
	}
	goal := s.goal(item.GoalID)
	return goal != nil && goal.Status == GoalActive && goal.ReviewPolicy == ReviewPerGoal &&
		item.Status == Verified && s.OpenGateCount(item.ID) == 0
}
func workNumber(id string) int { n, _ := parseWorkID(id); return n }

func parseWorkID(id string) (int, bool) {
	if !strings.HasPrefix(id, "WI-") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(id, "WI-"))
	return n, err == nil && n > 0 && fmt.Sprintf("WI-%03d", n) == id
}
