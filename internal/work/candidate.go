package work

import (
	"errors"
	"fmt"
	"regexp"
)

// CandidateKind distinguishes a committed revision from an immutable snapshot
// commit synthesized from the developer's current working tree.
type CandidateKind string

const (
	CommitCandidate   CandidateKind = "COMMIT"
	SnapshotCandidate CandidateKind = "SNAPSHOT"
)

// Candidate is the immutable code identity an Evidence record is about. It is
// a value carried by a Verification Run and its Evidence, never mutable state on
// the Work Item itself.
type Candidate struct {
	Kind         CandidateKind
	Revision     string
	BaseRevision string
	Digest       string
}

var candidateDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func (candidate Candidate) validate() error {
	if candidate.Revision == "" {
		return errors.New("candidate requires a revision")
	}
	switch candidate.Kind {
	case CommitCandidate:
		if candidate.BaseRevision != "" || candidate.Digest != "" {
			return errors.New("COMMIT candidate must not carry snapshot fields")
		}
	case SnapshotCandidate:
		if candidate.BaseRevision == "" {
			return errors.New("SNAPSHOT candidate requires a base revision")
		}
		if !candidateDigest.MatchString(candidate.Digest) {
			return fmt.Errorf("SNAPSHOT candidate requires a lowercase sha256 digest, got %q", candidate.Digest)
		}
	default:
		return fmt.Errorf("unknown candidate kind %q", candidate.Kind)
	}
	return nil
}
