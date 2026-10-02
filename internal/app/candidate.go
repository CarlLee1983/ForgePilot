// Package app holds the orchestration behind the CLI. It is the
// only place allowed to combine internal/work's rules with internal/storage's
// transactions and internal/repository's Git facts, so no command grows a
// second copy of a decision.
package app

import (
	"context"

	"github.com/CarlLee1983/ForgePilot/internal/repository"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// CandidateFacts gathers the external facts needed to decide whether the
// persisted Evidence of work in REVIEW still names the repository's current
// Candidate. Callers pass the values into internal/work; that package remains
// filesystem- and Git-free. Only REVIEW work is compared: DONE is never stale
// (ADR-0006), and nothing else has a pending Candidate to compare.
func CandidateFacts(ctx context.Context, state *work.State, root string) (work.RepositoryState, error) {
	needsCommitRevision, needsSnapshotDigest := false, false
	for _, item := range state.WorkItems {
		if item.Status != work.Review {
			continue
		}
		verification, ok := state.LatestVerification(item.ID)
		if !ok {
			continue
		}
		switch verification.CandidateKind {
		case work.CommitCandidate:
			needsCommitRevision = true
		case work.SnapshotCandidate:
			needsSnapshotDigest = true
		}
	}
	facts, err := resolveFacts(ctx, root, needsCommitRevision, needsSnapshotDigest)
	if err != nil {
		return work.RepositoryState{}, err
	}
	// A VERIFYING Work Item whose verifier is gone is an orphan that verify
	// reclaims; telling it from a live run is a fact about the process table.
	for _, item := range state.WorkItems {
		if item.Status == work.Verifying && !storage.VerificationRunning(root, item.ID) {
			if facts.AbandonedRuns == nil {
				facts.AbandonedRuns = map[string]bool{}
			}
			facts.AbandonedRuns[item.ID] = true
		}
	}
	return facts, nil
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
