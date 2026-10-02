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
// needs neither Git nor state is refused first (structure, then the literal form
// of each Story path), and the rest is decided inside one locked transaction, so
// an error anywhere leaves state untouched and two concurrent imports cannot
// interleave into a half-built DAG. A plan that adds nothing writes nothing.
func ImportGoalPlan(ctx context.Context, root string, plan work.GoalPlan, now Now) (work.PlanImport, error) {
	if err := plan.Validate(); err != nil {
		return work.PlanImport{}, err
	}
	// Stories are normalized (not yet checked against the filesystem) before the
	// transaction, so a re-import is compared with the same spelling the first
	// import stored.
	nodes := make([]work.PlanNode, len(plan.Nodes))
	for i, node := range plan.Nodes {
		story, err := repository.NormalizeStory(root, node.Story)
		if err != nil {
			return work.PlanImport{}, fmt.Errorf("node %q: story: %w", node.ID, err)
		}
		nodes[i] = work.PlanNode{ID: node.ID, Story: story, DependsOn: node.DependsOn}
	}
	plan.Nodes = nodes

	var result work.PlanImport
	err := storage.Update(root, func(state *work.State) error {
		// Only nodes being added need a Story that exists now. An existing node's
		// Story may have been finished and moved since; ImportGoalPlan still
		// compares its stored spelling.
		for _, node := range plan.Nodes {
			if state.HasWorkItem(node.ID) {
				continue
			}
			if _, err := repository.ValidateStory(root, node.Story); err != nil {
				return fmt.Errorf("node %q: story: %w", node.ID, err)
			}
		}
		// New nodes depend only on nodes of their own Goal, so only that Goal's
		// Candidate facts are resolved: another Goal's unresolvable Candidate must
		// not refuse this import. A Goal that does not exist yet has no Candidate.
		facts := work.RepositoryState{}
		if _, exists := state.GoalByID(plan.Goal.ID); exists {
			var err error
			if facts, err = GoalCandidateFacts(ctx, state, plan.Goal.ID, root); err != nil {
				return fmt.Errorf("resolve current Candidate before importing: %w", err)
			}
		}
		var err error
		if result, err = state.ImportGoalPlan(plan, root, facts, now.at()); err != nil {
			return err
		}
		if !result.Changed() {
			return storage.ErrNoChange
		}
		return nil
	})
	return result, err
}
