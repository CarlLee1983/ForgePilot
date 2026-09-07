package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/carl/forgepilot/internal/work"
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
	state.SchemaVersion = work.SchemaVersion
	return state, nil
}
