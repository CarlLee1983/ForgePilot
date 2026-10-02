package app

import (
	"context"
	"fmt"

	"github.com/CarlLee1983/ForgePilot/internal/repository"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// ImportGoalPlan creates a Goal and its whole DAG from a parsed Goal Plan, or
// appends the plan's new nodes to the Goal it already names. Everything that
// can be refused without Git or state is refused first (structure, then Story
// paths against the repository), and the rest is decided inside one locked
// transaction, so an error anywhere leaves state untouched and two concurrent
// imports cannot interleave into a half-built DAG.
func ImportGoalPlan(ctx context.Context, root string, plan work.GoalPlan, now Now) (work.PlanImport, error) {
	if err := plan.Validate(); err != nil {
		return work.PlanImport{}, err
	}
	// Story paths are normalized before the transaction so that re-imports are
	// compared with the same spelling the first import stored.
	nodes := make([]work.PlanNode, len(plan.Nodes))
	for i, node := range plan.Nodes {
		story, err := repository.ValidateStory(root, node.Story)
		if err != nil {
			return work.PlanImport{}, fmt.Errorf("node %q: story: %w", node.ID, err)
		}
		nodes[i] = work.PlanNode{ID: node.ID, Story: story, DependsOn: node.DependsOn}
	}
	plan.Nodes = nodes

	var result work.PlanImport
	err := storage.Update(root, func(state *work.State) error {
		facts, err := CandidateFacts(ctx, state, root)
		if err != nil {
			return fmt.Errorf("resolve current Candidate before importing: %w", err)
		}
		result, err = state.ImportGoalPlan(plan, root, facts, now.at())
		return err
	})
	return result, err
}
