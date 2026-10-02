package work

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// maxPlanIDLength bounds Goal and node IDs. Node IDs become Work Item IDs, which
// end up in file names, log keys and command lines.
const maxPlanIDLength = 64

// GoalPlan is the parsed form of a Goal Plan file: one Goal and the DAG of
// nodes that become its Work Items. Node order is the recommendation tie-break
// between simultaneously READY work.
type GoalPlan struct {
	Goal  PlanGoal   `json:"goal"`
	Nodes []PlanNode `json:"nodes"`
}

type PlanGoal struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	RequireApproval bool   `json:"require_approval"`
}

type PlanNode struct {
	ID        string   `json:"id"`
	Story     string   `json:"story"`
	DependsOn []string `json:"depends_on"`
}

// ValidPlanID reports whether id is usable as a Goal ID or node ID: it starts
// with an ASCII letter or digit, continues with letters, digits, '.', '_' or
// '-', and is at most 64 bytes long.
func ValidPlanID(id string) bool {
	if id == "" || len(id) > maxPlanIDLength {
		return false
	}
	// A work ID is a path component of refs/forgepilot/snapshots/<id>/..., and
	// Git refuses "..", a trailing "." and a ".lock" suffix in a ref name.
	if strings.Contains(id, "..") || strings.HasSuffix(id, ".") || strings.HasSuffix(id, ".lock") {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		alphanumeric := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !alphanumeric && (i == 0 || (c != '.' && c != '_' && c != '-')) {
			return false
		}
	}
	return true
}

const planIDRule = "must start with a letter or digit, contain only letters, digits, '.', '_' or '-', be at most 64 characters, must not contain \"..\", and must not end with '.' or '.lock'"

// ParseGoalPlan decodes a Goal Plan strictly: unknown fields and trailing
// content are errors, so a misspelled key cannot be silently ignored.
func ParseGoalPlan(data []byte) (GoalPlan, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var plan GoalPlan
	if err := decoder.Decode(&plan); err != nil {
		return GoalPlan{}, fmt.Errorf("goal plan: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return GoalPlan{}, errors.New("goal plan: contains extra JSON values")
	}
	return plan, nil
}

// Validate checks everything about a plan that needs no repository facts: IDs,
// required fields, dependency references and cycles. Story paths are checked by
// the caller, which owns the filesystem. The first problem found is returned,
// naming the node and field.
func (p GoalPlan) Validate() error {
	if p.Goal.ID == "" {
		return errors.New("goal.id is required")
	}
	if !ValidPlanID(p.Goal.ID) {
		return fmt.Errorf("goal.id %q is not a valid ID: %s", p.Goal.ID, planIDRule)
	}
	if strings.TrimSpace(p.Goal.Title) == "" {
		return errors.New("goal.title is required")
	}
	if len(p.Nodes) == 0 {
		return errors.New("nodes: at least one node is required")
	}
	nodes := make(map[string]bool, len(p.Nodes))
	for index, node := range p.Nodes {
		if node.ID == "" {
			return fmt.Errorf("nodes[%d].id is required", index)
		}
		if !ValidPlanID(node.ID) {
			return fmt.Errorf("node %q: id is not a valid ID: %s", node.ID, planIDRule)
		}
		if nodes[node.ID] {
			return fmt.Errorf("node %q: id is duplicated", node.ID)
		}
		nodes[node.ID] = true
		if strings.TrimSpace(node.Story) == "" {
			return fmt.Errorf("node %q: story is required", node.ID)
		}
	}
	for _, node := range p.Nodes {
		seen := make(map[string]bool, len(node.DependsOn))
		for _, dependency := range node.DependsOn {
			switch {
			case dependency == node.ID:
				return fmt.Errorf("node %q: depends_on %q: a node cannot depend on itself", node.ID, dependency)
			case seen[dependency]:
				return fmt.Errorf("node %q: depends_on %q: duplicate dependency", node.ID, dependency)
			case !nodes[dependency]:
				return fmt.Errorf("node %q: depends_on %q: unknown node; dependencies must name nodes of this plan", node.ID, dependency)
			}
			seen[dependency] = true
		}
	}
	return p.checkAcyclic()
}

func (p GoalPlan) checkAcyclic() error {
	dependencies := make(map[string][]string, len(p.Nodes))
	for _, node := range p.Nodes {
		dependencies[node.ID] = node.DependsOn
	}
	const visiting, done = 1, 2
	state := make(map[string]int, len(p.Nodes))
	var path []string
	var visit func(string) error
	visit = func(id string) error {
		switch state[id] {
		case done:
			return nil
		case visiting:
			start := 0
			for i, step := range path {
				if step == id {
					start = i
				}
			}
			cycle := append(append([]string(nil), path[start:]...), id)
			return fmt.Errorf("node %q: depends_on forms a cycle: %s", id, strings.Join(cycle, " -> "))
		}
		state[id] = visiting
		path = append(path, id)
		for _, dependency := range dependencies[id] {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		path = path[:len(path)-1]
		state[id] = done
		return nil
	}
	for _, node := range p.Nodes {
		if err := visit(node.ID); err != nil {
			return err
		}
	}
	return nil
}

// HasWorkItem reports whether a Work Item with this ID already exists in any Goal.
func (s *State) HasWorkItem(id string) bool { return s.item(id) != nil }

// PlanImport reports what an import changed. Added holds the new Work Items in
// plan order; an import that changes nothing has GoalCreated false and no Added.
type PlanImport struct {
	GoalCreated bool
	Added       []Item
}

func (r PlanImport) Changed() bool { return r.GoalCreated || len(r.Added) > 0 }

// ImportGoalPlan creates the plan's Goal and every node as a Work Item, or, when
// the Goal already exists, adds the plan's new nodes to it. It either applies the
// whole plan or returns an error having changed nothing.
//
// Re-importing is append-only. The Goal's title, description and approval
// requirement must be unchanged, every existing node must still be in the plan
// with the same story and the same set of dependencies (their order carries no
// meaning), and only ACTIVE Goals accept a plan. New nodes may depend on new or
// existing nodes, including DONE ones. The plan's story paths must already have
// been validated and normalized by the caller; repository is the root a new Goal
// belongs to.
func (s *State) ImportGoalPlan(plan GoalPlan, repository string, now time.Time) (PlanImport, error) {
	if err := plan.Validate(); err != nil {
		return PlanImport{}, err
	}
	goal := s.goal(plan.Goal.ID)
	for _, node := range plan.Nodes {
		if existing := s.item(node.ID); existing != nil && existing.GoalID != plan.Goal.ID {
			return PlanImport{}, fmt.Errorf("node %q: id is already used by a work item of goal %q", node.ID, existing.GoalID)
		}
	}
	if goal != nil {
		if err := s.checkReimport(*goal, plan); err != nil {
			return PlanImport{}, err
		}
	}

	// Readiness is decided before anything is appended, so a new node that
	// depends on another new node is PENDING: nothing new is DONE yet.
	var added []Item
	for _, node := range plan.Nodes {
		if s.item(node.ID) != nil {
			continue
		}
		status := Ready
		if !s.dependenciesDone(node.DependsOn) {
			status = Pending
		}
		added = append(added, Item{ID: node.ID, GoalID: plan.Goal.ID, StoryRef: node.Story, Status: status, DependsOn: append([]string{}, node.DependsOn...), CreatedAt: now, UpdatedAt: now})
	}
	result := PlanImport{Added: added}
	if goal == nil {
		s.Goals = append(s.Goals, Goal{ID: plan.Goal.ID, Title: plan.Goal.Title, Description: plan.Goal.Description, Repository: repository, RequireApproval: plan.Goal.RequireApproval, Status: GoalActive, CreatedAt: now, UpdatedAt: now})
		result.GoalCreated = true
	} else if len(added) > 0 {
		s.goal(plan.Goal.ID).UpdatedAt = now
	}
	s.WorkItems = append(s.WorkItems, added...)
	return result, nil
}

// checkReimport enforces the append-only rule against an existing Goal.
func (s *State) checkReimport(goal Goal, plan GoalPlan) error {
	if goal.Status != GoalActive {
		return fmt.Errorf("goal %q is %s; a plan can only be imported into an ACTIVE goal", goal.ID, goal.Status)
	}
	if goal.Title != plan.Goal.Title {
		return fmt.Errorf("goal.title: %q differs from the imported goal's %q", plan.Goal.Title, goal.Title)
	}
	if goal.Description != plan.Goal.Description {
		return fmt.Errorf("goal.description: %q differs from the imported goal's %q", plan.Goal.Description, goal.Description)
	}
	if goal.RequireApproval != plan.Goal.RequireApproval {
		return fmt.Errorf("goal.require_approval: %t differs from the imported goal's %t", plan.Goal.RequireApproval, goal.RequireApproval)
	}
	planned := make(map[string]PlanNode, len(plan.Nodes))
	for _, node := range plan.Nodes {
		planned[node.ID] = node
	}
	for _, item := range s.WorkItems {
		if item.GoalID != goal.ID {
			continue
		}
		node, ok := planned[item.ID]
		if !ok {
			return fmt.Errorf("node %q: exists in goal %q but is missing from the plan; re-importing can only add nodes", item.ID, goal.ID)
		}
		if node.Story != item.StoryRef {
			return fmt.Errorf("node %q: story %q differs from the imported %q; re-importing can only add nodes", item.ID, node.Story, item.StoryRef)
		}
		if !sameStrings(node.DependsOn, item.DependsOn) {
			return fmt.Errorf("node %q: depends_on %v differs from the imported %v; re-importing can only add nodes", item.ID, node.DependsOn, item.DependsOn)
		}
	}
	return nil
}
