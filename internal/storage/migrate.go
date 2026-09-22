package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// Migrate upgrades a state snapshot to the schema version this binary supports,
// reporting whether an upgrade actually happened. Upgrading is deliberately a
// separate command: it is one-way, and a state silently moved forward by an
// unrelated command could no longer be read by the binary the user came from.
func Migrate(root string) (bool, error) {
	root, err := canonicalRoot(root)
	if err != nil {
		return false, err
	}
	directory := filepath.Join(root, stateDirectory)
	upgraded := false
	err = withLock(directory, func() error {
		contents, err := os.ReadFile(statePath(directory))
		if err != nil {
			return err
		}
		var header struct {
			SchemaVersion int `json:"schema_version"`
		}
		if err := json.Unmarshal(contents, &header); err != nil {
			return fmt.Errorf("read state: %w", err)
		}
		switch {
		case header.SchemaVersion == work.SchemaVersion:
			return nil
		case header.SchemaVersion > work.SchemaVersion:
			return fmt.Errorf("state uses schema version %d, which is newer than this binary supports (%d)", header.SchemaVersion, work.SchemaVersion)
		case header.SchemaVersion < 1:
			return fmt.Errorf("state has an unusable schema version %d", header.SchemaVersion)
		}
		backup := fmt.Sprintf("%s.v%d.bak", statePath(directory), header.SchemaVersion)
		if _, err := os.Stat(backup); err == nil {
			return fmt.Errorf("backup %s already exists; move it aside before migrating", filepath.Base(backup))
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		state, err := upgrade(contents, header.SchemaVersion)
		if err != nil {
			return err
		}
		if err := state.Validate(); err != nil {
			return fmt.Errorf("migrated state is invalid: %w", err)
		}
		if err := validateRepository(state, root); err != nil {
			return err
		}
		// The backup must survive a crash as reliably as the state it protects:
		// a truncated backup plus a refusal to overwrite it would leave the user
		// with neither a usable backup nor a way forward.
		if err := writeFileAtomically(directory, backup, contents); err != nil {
			return fmt.Errorf("write backup: %w", err)
		}
		if err := save(directory, state); err != nil {
			return err
		}
		upgraded = true
		return nil
	})
	return upgraded, err
}

// upgrade applies every step from the snapshot's version up to the current one,
// so a user who skipped a release migrates once rather than once per version
// they missed. Each step so far has been purely additive, so decoding into the
// current shape and filling in the fields that step introduced is the whole
// migration.
func upgrade(contents []byte, from int) (work.State, error) {
	if from < 1 || from >= work.SchemaVersion {
		return work.State{}, fmt.Errorf("no upgrade path from schema version %d", from)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var state work.State
	if err := decoder.Decode(&state); err != nil {
		return work.State{}, fmt.Errorf("read state: %w", err)
	}
	if err := ensureEOF(decoder); err != nil {
		return work.State{}, fmt.Errorf("read state: %w", err)
	}
	// The declared version is a claim about the file, and the file can disagree
	// with it — a hand-edited header on a newer snapshot is the obvious way. The
	// containers a step is meant to create must therefore be empty before it
	// creates them; filling them in regardless would silently delete the exact
	// history this product exists to keep.
	if from < 2 {
		// v1 → v2 introduced the Evidence container and its ID counter.
		if len(state.Evidence) > 0 || state.NextEvidenceID > 1 {
			return work.State{}, fmt.Errorf("state declares schema version %d but already carries evidence; refusing to migrate over it", from)
		}
		state.NextEvidenceID = 1
		state.Evidence = nil
	}
	if from < 3 {
		// v2 → v3 introduces the Gate container and its ID counter.
		if len(state.Gates) > 0 || state.NextGateID > 1 {
			return work.State{}, fmt.Errorf("state declares schema version %d but already carries gates; refusing to migrate over it", from)
		}
		state.NextGateID = 1
		state.Gates = nil
	}
	// v3 → v4 introduces no container, only an optional field on Evidence, so
	// there is nothing for a step to create and nothing it could discard.
	// v4 → v5 is the same shape again: log path is a field on the existing Run,
	// not a new container, and a run already in flight before v5 existed simply
	// decodes with an empty one — that is the honest fact that its output was
	// never streamed anywhere, not something a step needs to fill in. The
	// version bump below is the whole upgrade.
	if from < 6 {
		// v5 → v6 makes the code identity kind explicit. Every earlier run and
		// Evidence record targeted HEAD, so its existing revision is a COMMIT
		// candidate. This is a schema migration, not a reinterpretation at read
		// time; older and newer binaries continue to reject each other's state.
		for i := range state.Evidence {
			if state.Evidence[i].CandidateKind != "" || state.Evidence[i].BaseRevision != "" || state.Evidence[i].CandidateDigest != "" {
				return work.State{}, fmt.Errorf("state declares schema version %d but already carries candidate identity; refusing to migrate over it", from)
			}
			state.Evidence[i].CandidateKind = work.CommitCandidate
		}
		for i := range state.WorkItems {
			run := state.WorkItems[i].CurrentRun
			if run == nil {
				continue
			}
			if run.CandidateKind != "" || run.BaseRevision != "" || run.CandidateDigest != "" {
				return work.State{}, fmt.Errorf("state declares schema version %d but an in-flight run already carries candidate identity; refusing to migrate over it", from)
			}
			run.CandidateKind = work.CommitCandidate
		}
	}
	// v6 → v7 adds optional runtime metadata to Runs and Verification Evidence.
	// Earlier snapshots did not record it, so migration deliberately leaves it
	// absent rather than inventing facts about the environment that ran a check.
	if from < 8 {
		// v7 → v8 makes Goal review policy explicit. Earlier snapshots always
		// used per-Work-Item review, so retain that behavior rather than leaving
		// a newly mandatory field ambiguous. A non-empty policy contradicts the
		// declared pre-v8 version and must not be silently accepted.
		for i := range state.Goals {
			if state.Goals[i].ReviewPolicy != "" {
				return work.State{}, fmt.Errorf("state declares schema version %d but Goal %q already carries review policy; refusing to migrate over it", from, state.Goals[i].ID)
			}
			state.Goals[i].ReviewPolicy = work.ReviewPerWorkItem
		}
	}
	if from < 9 {
		if state.NextVerificationRunID != 0 {
			return work.State{}, fmt.Errorf("state declares schema version %d but already carries a verification run counter; refusing to migrate over it", from)
		}
		for _, evidence := range state.Evidence {
			if evidence.VerificationRunID != "" {
				return work.State{}, fmt.Errorf("state declares schema version %d but evidence %q already carries verification run identity; refusing to migrate over it", from, evidence.ID)
			}
		}
		for _, item := range state.WorkItems {
			if item.CurrentRun != nil && item.CurrentRun.VerificationRunID != "" {
				return work.State{}, fmt.Errorf("state declares schema version %d but work item %q already carries verification run identity; refusing to migrate over it", from, item.ID)
			}
		}
		legacy := 1
		for i := range state.Evidence {
			if state.Evidence[i].Type == work.VerificationEvidence {
				state.Evidence[i].VerificationRunID = fmt.Sprintf("LVR-%03d", legacy)
				legacy++
			}
		}
		for i := range state.WorkItems {
			if run := state.WorkItems[i].CurrentRun; run != nil {
				run.VerificationRunID = fmt.Sprintf("LVR-%03d", legacy)
				legacy++
			}
		}
		state.NextVerificationRunID = 1
	}
	if from < 10 {
		// v9 → v10 adds the optional Work Item external reference. Earlier
		// snapshots cannot honestly carry one, so leave every migrated value
		// empty and reject a header that understates an existing reference.
		for _, item := range state.WorkItems {
			if item.ExternalRef != "" {
				return work.State{}, fmt.Errorf("state declares schema version %d but work item %q already carries an external reference; refusing to migrate over it", from, item.ID)
			}
		}
	}
	if from < 11 {
		// v10 → v11 adds an explicit Goal completion policy and immutable
		// aggregate completion evidence. GOAL-policy execution now completes a
		// Goal after current verification by default, so old GOAL records are
		// upgraded to VERIFIED. WORK_ITEM records retain their existing human
		// approval semantics. Completion fields under a v10 header are rejected
		// as evidence that the header understates the snapshot.
		if state.NextGoalCompletionEvidenceID != 0 || len(state.GoalCompletionEvidence) > 0 {
			return work.State{}, fmt.Errorf("state declares schema version %d but already carries Goal completion evidence; refusing to migrate over it", from)
		}
		for i := range state.Goals {
			if state.Goals[i].LegacyCompletion != nil {
				return work.State{}, fmt.Errorf("state declares schema version %d but Goal %q already carries legacy completion provenance; refusing to migrate over it", from, state.Goals[i].ID)
			}
			if state.Goals[i].CompletionPolicy != "" {
				return work.State{}, fmt.Errorf("state declares schema version %d but Goal %q already carries completion policy; refusing to migrate over it", from, state.Goals[i].ID)
			}
			state.Goals[i].CompletionPolicy = work.CompletionHuman
			if state.Goals[i].ReviewPolicy == work.ReviewPerGoal {
				state.Goals[i].CompletionPolicy = work.CompletionVerified
			}
		}
		state.NextGoalCompletionEvidenceID = 1
		state.GoalCompletionEvidence = nil
	}
	if from < 12 {
		// v11 → v12 removes the configurable HUMAN final-review boundary from
		// GOAL policy. Active records adopt the sole GOAL completion policy. A
		// completed HUMAN record preserves its terminal lifecycle with separate
		// legacy provenance; it must not be mistaken for current Candidate proof.
		for i := range state.Goals {
			goal := &state.Goals[i]
			if goal.LegacyCompletion != nil {
				return work.State{}, fmt.Errorf("state declares schema version %d but Goal %q already carries v12 legacy completion provenance; refusing to migrate over it", from, goal.ID)
			}
			if goal.ReviewPolicy != work.ReviewPerGoal || goal.CompletionPolicy != work.CompletionHuman {
				continue
			}
			if _, hasEvidence := state.GoalCompletionEvidenceFor(goal.ID); hasEvidence {
				return work.State{}, fmt.Errorf("state declares schema version %d but GOAL/HUMAN Goal %q already carries automatic completion evidence", from, goal.ID)
			}
			if goal.Status == work.GoalCompleted {
				goal.LegacyCompletion = &work.LegacyGoalCompletion{
					SourceSchemaVersion: work.LegacyHumanCompletionSourceSchemaVersion,
					CompletionPolicy:    work.CompletionHuman,
				}
			}
			goal.CompletionPolicy = work.CompletionVerified
		}
	}
	if from < 13 {
		// v12 → v13 adds an optional Goal-owned execution aggregate. Existing
		// snapshots have no adopted plan or authorization; migration never
		// infers one from Work Items, Story paths, or external references.
		for _, goal := range state.Goals {
			if goal.Execution != nil {
				return work.State{}, fmt.Errorf("state declares schema version %d but Goal %q already carries execution authorization data; refusing to migrate over it", from, goal.ID)
			}
		}
	}
	if from < 14 {
		// v13 → v14 adds durable execution reservations. Existing adopted
		// authorizations retain their already-validated zero-use ledger; ForgePilot
		// must not invent historical consumption from Run Records or logs.
		for _, goal := range state.Goals {
			if goal.Execution != nil && len(goal.Execution.Ledger.Reservations) != 0 {
				return work.State{}, fmt.Errorf("state declares schema version %d but Goal %q already carries execution reservations; refusing to migrate over it", from, goal.ID)
			}
		}
	}
	if from < 15 {
		// v14 → v15 permits append-only execution revision history. A v14
		// header claiming more than its sole initial binding/authorization is
		// malformed, not an early compatible revision to be laundered.
		for _, goal := range state.Goals {
			if goal.Execution != nil && (len(goal.Execution.PlanBindings) > 1 || len(goal.Execution.Authorizations) > 1) {
				return work.State{}, fmt.Errorf("state declares schema version %d but Goal %q already carries execution revision history; refusing to migrate over it", from, goal.ID)
			}
		}
	}
	if from < 16 {
		// v15 → v16 persists an artifact-byte accounting start revision. Older
		// authorizations have no trustworthy artifact consumption ledger, so the
		// zero value means unknown and blocks new output until an explicit
		// authorization revision starts prospective accounting. A v15 header that
		// already carries any v16 artifact accounting data understates the file.
		for _, goal := range state.Goals {
			if goal.Execution == nil {
				continue
			}
			ledger := goal.Execution.Ledger
			if ledger.ArtifactAccountingStartRevision != 0 || ledger.ArtifactBytesConsumed != 0 || len(ledger.ArtifactByteReservations) != 0 {
				return work.State{}, fmt.Errorf("state declares schema version %d but Goal %q already carries artifact-byte accounting; refusing to migrate over it", from, goal.ID)
			}
		}
		if err := state.MigrateExecutionArtifactAccountingToUnknown(); err != nil {
			return work.State{}, err
		}
	}
	// v16 → v17 fences the independently versioned execution-control sidecar.
	// The state has no new control fields: advancing this header is what makes
	// a v16 binary fail closed rather than ignore a durable pause or wait it
	// cannot understand. The sidecar itself is strict and remains absent until
	// the first control operation.
	state.SchemaVersion = work.SchemaVersion
	return state, nil
}
