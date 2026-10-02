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

func (s *State) AddGoal(id, title, description, repository string, now time.Time) error {
	return s.AddGoalWithReviewPolicy(id, title, description, repository, ReviewPerWorkItem, now)
}

func (s *State) AddGoalWithReviewPolicy(id, title, description, repository string, policy ReviewPolicy, now time.Time) error {
	return s.AddGoalWithPolicies(id, title, description, repository, policy, completionPolicyForReviewPolicy(policy), now)
}

func (s *State) AddGoalWithPolicies(id, title, description, repository string, policy ReviewPolicy, completion CompletionPolicy, now time.Time) error {
	if id == "" || title == "" {
		return errors.New("goal id and title are required")
	}
	if !validReviewPolicy(policy) || !validCompletionPolicy(completion) || completion != completionPolicyForReviewPolicy(policy) {
		return fmt.Errorf("invalid policy %q / %q", policy, completion)
	}
	if s.goal(id) != nil {
		return fmt.Errorf("goal %q already exists", id)
	}
	s.Goals = append(s.Goals, Goal{ID: id, Title: title, Description: description, Repository: repository, Status: GoalActive, ReviewPolicy: policy, CompletionPolicy: completion, CreatedAt: now, UpdatedAt: now})
	return nil
}

func (s *State) AddWork(goalID, story string, dependencies []string, now time.Time) (Item, error) {
	return s.AddWorkWithRepository(goalID, story, dependencies, RepositoryState{}, now)
}

func (s *State) AddWorkWithRepository(goalID, story string, dependencies []string, repository RepositoryState, now time.Time) (Item, error) {
	goal := s.goal(goalID)
	if goal == nil {
		return Item{}, fmt.Errorf("unknown goal %q", goalID)
	}
	plan := GoalPlan{Goal: PlanGoal{ID: goal.ID, Title: goal.Title, Description: goal.Description, RequireApproval: goal.ReviewPolicy == ReviewPerWorkItem}}
	for _, item := range s.WorkItems {
		if item.GoalID == goalID {
			plan.Nodes = append(plan.Nodes, PlanNode{ID: item.ID, Story: item.StoryRef, DependsOn: item.DependsOn})
		}
	}
	id := fmt.Sprintf("WI-%03d", len(s.WorkItems)+1)
	plan.Nodes = append(plan.Nodes, PlanNode{ID: id, Story: story, DependsOn: dependencies})
	result, err := s.ImportGoalPlan(plan, goal.Repository, repository, now)
	if err != nil {
		return Item{}, err
	}
	return result.Added[0], nil
}
