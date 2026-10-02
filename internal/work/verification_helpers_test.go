package work

import (
	"testing"
	"time"
)

func passVerificationFor(t *testing.T, state *State, id string, now time.Time, candidate Candidate, repository RepositoryState) {
	t.Helper()
	if err := state.Start(id, now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginCandidateVerification(id, candidate, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerificationWithRepository(id, candidate.Revision, "make verify", 0, repository, now); err != nil {
		t.Fatal(err)
	}
}
