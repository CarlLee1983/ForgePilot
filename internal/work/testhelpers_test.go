package work

import (
	"errors"
	"fmt"
	"time"
)

// These helpers build fixtures the way production now does: a Goal exists only
// through a Goal Plan import. AddGoal* records the Goal alone, because tests
// routinely need a Goal before any node exists; AddWork re-imports the Goal's
// plan with one more node, so every fixture Work Item passes through
// ImportGoalPlan. IDs are WI-001, WI-002, ... in creation order, which is the
// naming the older tests were written against.

// AddGoal records a Goal without an Approval Requirement: a PASS completes its
// work.
func (s *State) AddGoal(id, title, description, repository string, now time.Time) error {
	return s.addGoal(id, title, description, repository, false, now)
}

// AddGoalRequiringApproval records a Goal whose work goes through REVIEW.
func (s *State) AddGoalRequiringApproval(id, title, description, repository string, now time.Time) error {
	return s.addGoal(id, title, description, repository, true, now)
}

func (s *State) addGoal(id, title, description, repository string, requireApproval bool, now time.Time) error {
	if id == "" || title == "" {
		return errors.New("goal id and title are required")
	}
	if s.goal(id) != nil {
		return fmt.Errorf("goal %q already exists", id)
	}
	s.Goals = append(s.Goals, Goal{ID: id, Title: title, Description: description, Repository: repository, RequireApproval: requireApproval, Status: GoalActive, CreatedAt: now, UpdatedAt: now})
	return nil
}

func (s *State) AddWork(goalID, story string, dependencies []string, now time.Time) (Item, error) {
	goal := s.goal(goalID)
	if goal == nil {
		return Item{}, fmt.Errorf("unknown goal %q", goalID)
	}
	plan := GoalPlan{Goal: PlanGoal{ID: goal.ID, Title: goal.Title, Description: goal.Description, RequireApproval: goal.RequireApproval}}
	for _, item := range s.WorkItems {
		if item.GoalID == goalID {
			plan.Nodes = append(plan.Nodes, PlanNode{ID: item.ID, Story: item.StoryRef, DependsOn: item.DependsOn})
		}
	}
	id := fmt.Sprintf("WI-%03d", len(s.WorkItems)+1)
	plan.Nodes = append(plan.Nodes, PlanNode{ID: id, Story: story, DependsOn: dependencies})
	result, err := s.ImportGoalPlan(plan, goal.Repository, now)
	if err != nil {
		return Item{}, err
	}
	return result.Added[0], nil
}
