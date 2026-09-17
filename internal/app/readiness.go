package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/CarlLee1983/ForgePilot/internal/readiness"
	"github.com/CarlLee1983/ForgePilot/internal/repository"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// ReviewGoalStoryReadiness is the app-owned, read-only whole-Goal preflight.
// Repository access ends here; the readiness module receives only values.
func ReviewGoalStoryReadiness(ctx context.Context, root, goalID string) (readiness.Report, error) {
	if err := ctx.Err(); err != nil {
		return readiness.Report{}, err
	}
	state, err := storage.Load(root)
	if err != nil {
		return readiness.Report{}, err
	}
	input, err := readinessInput(root, &state, goalID)
	if err != nil {
		return readiness.Report{}, err
	}
	return readiness.Review(input), nil
}

func readinessInput(root string, loaded *work.State, goalID string) (readiness.Input, error) {
	knownGoal := false
	for _, goal := range loaded.Goals {
		if goal.ID == goalID {
			knownGoal = true
			break
		}
	}
	if !knownGoal {
		return readiness.Input{}, fmt.Errorf("unknown goal %q", goalID)
	}
	input := readiness.Input{Artifacts: map[string]bool{}}
	for _, gate := range loaded.Gates {
		input.Gates = append(input.Gates, readiness.Gate{ID: gate.ID, Choice: gate.Choice, Resolved: gate.Status == work.GateResolved})
	}
	for _, workItem := range loaded.WorkItems {
		if workItem.GoalID != goalID {
			continue
		}
		item := readiness.Item{ID: workItem.ID, StoryRef: workItem.StoryRef, DependsOn: append([]string(nil), workItem.DependsOn...), CreatedAt: workItem.CreatedAt}
		files, loadErr := repository.ReadStoryReadinessFiles(root, workItem.StoryRef)
		if loadErr != nil {
			var fileErr *repository.StoryReadinessFileError
			if errors.As(loadErr, &fileErr) && fileErr.Name == "readiness.json" {
				item.PreflightDefects = append(item.PreflightDefects, readiness.Defect{
					Code: readiness.MissingContract, WorkItemID: workItem.ID, StoryRef: workItem.StoryRef, Detail: loadErr.Error(),
				})
			} else {
				item.PreflightDefects = append(item.PreflightDefects, readiness.Defect{
					Code: readiness.InvalidContract, WorkItemID: workItem.ID, StoryRef: workItem.StoryRef, Detail: loadErr.Error(),
				})
			}
			input.Items = append(input.Items, item)
			continue
		}
		contract, decodeErr := readiness.Decode(files.Sidecar)
		if decodeErr != nil {
			item.PreflightDefects = append(item.PreflightDefects, readiness.Defect{
				Code: readiness.InvalidContract, WorkItemID: workItem.ID, StoryRef: workItem.StoryRef, Detail: decodeErr.Error(),
			})
		} else {
			item.Contract = &contract
			for _, declaration := range contract.Inputs {
				if source := declaration.Source.PreexistingArtifact; source != nil {
					input.Artifacts[source.Path] = repository.ContainedRegularFile(root, source.Path)
				}
			}
		}
		item.Sidecar, item.StoryMD, item.AcceptanceMD = files.Sidecar, files.StoryMD, files.AcceptanceMD
		item.SourceErrors = map[string]string{}
		if files.StoryMDError != nil {
			item.SourceErrors["story.md"] = files.StoryMDError.Error()
		}
		if files.AcceptanceMDError != nil {
			item.SourceErrors["acceptance.md"] = files.AcceptanceMDError.Error()
		}
		input.Items = append(input.Items, item)
	}
	return input, nil
}
