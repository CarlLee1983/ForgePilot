// Package app holds the orchestration the CLI and the Runner share. It is the
// only place allowed to combine internal/work's rules with internal/storage's
// transactions and internal/repository's Git facts, so neither a command nor a
// Runner ever grows a second copy of a decision. See
// docs/adr/0019-runner-executes-forgepilot-decides.md.
package app

import (
	"context"

	"github.com/CarlLee1983/ForgePilot/internal/repository"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// CandidateFacts gathers the external facts needed to decide whether persisted
// REVIEW/VERIFIED Evidence, or a Verification currently finishing, still names
// the repository's current Candidate. Callers pass the values into
// internal/work; that package remains filesystem- and Git-free.
func CandidateFacts(ctx context.Context, state *work.State, root string) (work.RepositoryState, error) {
	needsCommitRevision, needsSnapshotDigest := false, false
	for _, item := range state.WorkItems {
		kind := work.CandidateKind("")
		if item.Status == work.Verifying && item.CurrentRun != nil {
			kind = item.CurrentRun.CandidateKind
		} else if item.Status == work.Review || item.Status == work.Verified {
			if verification, ok := state.LatestVerification(item.ID); ok {
				kind = verification.CandidateKind
			}
		}
		switch kind {
		case work.CommitCandidate:
			needsCommitRevision = true
		case work.SnapshotCandidate:
			needsSnapshotDigest = true
		}
	}
	return resolveFacts(ctx, root, needsCommitRevision, needsSnapshotDigest)
}

// GoalCandidateFacts is CandidateFacts narrowed to one Goal. A Goal-scoped
// decision must not be refused because an unrelated Goal holds COMMIT Evidence
// and HEAD cannot be read: the answer never depended on that fact. It also
// keeps a precondition and the transaction it guards on one criterion, which
// this project has twice been bitten by getting wrong.
func GoalCandidateFacts(ctx context.Context, state *work.State, goalID, root string) (work.RepositoryState, error) {
	needsCommitRevision, needsSnapshotDigest := false, false
	for _, item := range state.WorkItems {
		if item.GoalID != goalID {
			continue
		}
		kind := work.CandidateKind("")
		if item.Status == work.Verifying && item.CurrentRun != nil {
			kind = item.CurrentRun.CandidateKind
		} else if item.Status == work.Review || item.Status == work.Verified {
			if verification, ok := state.LatestVerification(item.ID); ok {
				kind = verification.CandidateKind
			}
		}
		switch kind {
		case work.CommitCandidate:
			needsCommitRevision = true
		case work.SnapshotCandidate:
			needsSnapshotDigest = true
		}
	}
	return resolveFacts(ctx, root, needsCommitRevision, needsSnapshotDigest)
}

// GoalReadinessFacts resolves only the Candidate facts one Goal's readiness
// depends on, so an unrelated Goal's SNAPSHOT Evidence cannot make reconciling
// this one require a workspace digest — or fail when one cannot be computed.
func GoalReadinessFacts(ctx context.Context, state *work.State, goalID, root string) (work.RepositoryState, error) {
	needsCommitRevision, needsSnapshotDigest := false, false
	for _, kind := range state.ReadinessCandidateKinds(goalID) {
		switch kind {
		case work.SnapshotCandidate:
			needsSnapshotDigest = true
		case work.CommitCandidate:
			needsCommitRevision = true
		}
	}
	return resolveFacts(ctx, root, needsCommitRevision, needsSnapshotDigest)
}

// StartFacts resolves the facts one start transition depends on: the Candidate
// kinds of its VERIFIED prerequisites, and nothing else.
func StartFacts(ctx context.Context, state *work.State, id, root string) (work.RepositoryState, error) {
	summary, err := state.WorkSummary(id, work.RepositoryState{})
	if err != nil {
		return work.RepositoryState{}, err
	}
	needsCommit, needsSnapshot := false, false
	for _, dependencyID := range summary.Item.DependsOn {
		if state.WorkItemStatus(dependencyID) != work.Verified {
			continue
		}
		verification, ok := state.LatestVerification(dependencyID)
		if !ok {
			continue
		}
		if verification.CandidateKind == work.SnapshotCandidate {
			needsSnapshot = true
		} else {
			needsCommit = true
		}
	}
	return resolveFacts(ctx, root, needsCommit, needsSnapshot)
}

// resolveFacts reads exactly the Git facts a caller asked for. Facts that
// nothing needs are never read: a failure to resolve one is a refusal, so
// gathering more than the decision requires would refuse commands that did not
// depend on it.
func resolveFacts(ctx context.Context, root string, needsCommitRevision, needsSnapshotDigest bool) (work.RepositoryState, error) {
	result := work.RepositoryState{}
	if needsCommitRevision {
		revision, err := repository.Head(ctx, root)
		if err != nil {
			return work.RepositoryState{}, err
		}
		result.Revision = revision
	}
	if needsSnapshotDigest {
		workspace, err := repository.InspectSnapshot(ctx, root)
		if err != nil {
			return work.RepositoryState{}, err
		}
		result.SnapshotDigest = workspace.Digest
		if result.Revision == "" {
			result.Revision = workspace.BaseRevision
		}
	}
	return result, nil
}

// abbreviatedRevisionLength is how much of a commit SHA presentation shows. It
// is long enough to identify a commit by eye and short enough to keep a line
// readable.
const abbreviatedRevisionLength = 12

func shortRevision(revision string) string {
	if len(revision) > abbreviatedRevisionLength {
		return revision[:abbreviatedRevisionLength]
	}
	return revision
}
