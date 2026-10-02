package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/repository"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// importGoal reads a Goal Plan and hands it to app.ImportGoalPlan. The plan
// path is resolved against the directory the command was run from, not the
// repository root: the plan is an input file, and need not live in the
// repository at all.
func importGoal(args []string, cwd, root string, output io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: forgepilot goal import <plan-path>")
	}
	path := args[0]
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read goal plan: %w", err)
	}
	plan, err := work.ParseGoalPlan(contents)
	if err != nil {
		return err
	}
	result, err := app.ImportGoalPlan(root, plan, now)
	if err != nil {
		return err
	}
	switch {
	case result.GoalCreated:
		_, err = fmt.Fprintf(output, "Goal %s imported: %d nodes\n", plan.Goal.ID, len(result.Added))
	case result.Changed():
		_, err = fmt.Fprintf(output, "Goal %s updated: added %d nodes\n", plan.Goal.ID, len(result.Added))
	default:
		_, err = fmt.Fprintf(output, "Goal %s unchanged: the plan adds no nodes\n", plan.Goal.ID)
	}
	if err != nil {
		return err
	}
	// Readiness is computed, so it is read from the state the import produced.
	state, err := storage.Load(root)
	if err != nil {
		return err
	}
	for _, item := range result.Added {
		if _, err := fmt.Fprintf(output, "  %s %s %s\n", item.ID, state.DisplayStatus(item.ID), item.StoryRef); err != nil {
			return err
		}
		// Best-effort: the import already succeeded, so a failure to query git
		// must not turn it into a failing command. The hint is a courtesy.
		if uncommitted, hintErr := repository.Uncommitted(context.Background(), root, item.StoryRef); hintErr == nil && uncommitted {
			if _, err := fmt.Fprintf(output, "    %s is not committed yet; use `forgepilot verify %s --snapshot` to verify the working tree, or commit it before commit-mode verification.\n", item.StoryRef, item.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
