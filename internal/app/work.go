package app

import (
	"context"
	"fmt"

	"github.com/CarlLee1983/ForgePilot/internal/repository"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// WorkAddRequest is the typed creation input shared by CLI callers. An empty
// ExternalRef retains the historical non-idempotent Work Item behavior.
type WorkAddRequest struct {
	GoalID       string
	StoryRef     string
	Dependencies []string
	ExternalRef  string
}

// WorkAddResult says both which Work Item the request names and whether this
// invocation created it. Created is false only for an exact external-ref retry.
type WorkAddResult struct {
	Item    work.Item
	Created bool
}

// AddWork validates the repository Story, then makes the same locked decision
// as the durable write. Existing external refs return before resolving Candidate
// facts, because a retry does not create or re-evaluate a Work Item.
func AddWork(ctx context.Context, root string, request WorkAddRequest, now Now) (WorkAddResult, error) {
	story, err := repository.ValidateStory(root, request.StoryRef)
	if err != nil {
		return WorkAddResult{}, err
	}
	result := WorkAddResult{Created: true}
	err = storage.Update(root, func(state *work.State) error {
		if request.ExternalRef != "" {
			if state.HasWorkItemByExternalRef(request.GoalID, request.ExternalRef) {
				item, created, addErr := state.AddWorkWithRepositoryAndExternalRef(request.GoalID, story, request.Dependencies, request.ExternalRef, work.RepositoryState{}, now.at())
				result.Item, result.Created = item, created
				return addErr
			}
		}
		facts, factsErr := CandidateFacts(ctx, state, root)
		if factsErr != nil {
			return fmt.Errorf("resolve current Candidate before adding work: %w", factsErr)
		}
		if request.ExternalRef != "" {
			item, created, addErr := state.AddWorkWithRepositoryAndExternalRef(request.GoalID, story, request.Dependencies, request.ExternalRef, facts, now.at())
			result.Item, result.Created = item, created
			return addErr
		}
		item, addErr := state.AddWorkWithRepository(request.GoalID, story, request.Dependencies, facts, now.at())
		result.Item = item
		return addErr
	})
	return result, err
}
