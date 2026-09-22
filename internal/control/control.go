// Package control persists execution-control facts that deliberately do not
// belong to the ForgePilot Work Item lifecycle.
package control

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

const SchemaVersion = 1

// WaitKind identifies who or what must provide the missing fact.
type WaitKind string

const (
	WaitHuman    WaitKind = "human"
	WaitExternal WaitKind = "external"
)

// Pause is the one active stop intent for a workspace. Revision is assigned by
// SetPause and is monotonic with the enclosing State revision.
type Pause struct {
	GoalID      string    `json:"goal_id"`
	RunID       string    `json:"run_id"`
	Reason      string    `json:"reason"`
	RequestedBy string    `json:"requested_by"`
	RequestedAt time.Time `json:"requested_at"`
	// WaitID binds a needs-human pause to its immutable wait record. A direct
	// user stop intentionally has no wait ID.
	WaitID   string `json:"wait_id,omitempty"`
	Revision uint64 `json:"revision"`
}

// Wait records a durable human or external fact that must be supplied before a
// paused execution can be resumed.
type Wait struct {
	ID                    string   `json:"id"`
	Kind                  WaitKind `json:"kind"`
	GoalID                string   `json:"goal_id"`
	RunID                 string   `json:"run_id"`
	NodeID                string   `json:"node_id"`
	PlanDigest            string   `json:"plan_digest"`
	AuthorizationRevision uint64   `json:"authorization_revision"`
	AuthorizationDigest   string   `json:"authorization_digest"`
	Question              string   `json:"question"`
	Context               string   `json:"context"`
	ExpectedFact          string   `json:"expected_fact"`
}

// ExternalDeclaration is an unverified, named assertion about an external
// fact. It is append-only and bound exactly to the external Wait it fulfills.
type ExternalDeclaration struct {
	ID                    string    `json:"id"`
	FactName              string    `json:"fact_name"`
	WaitID                string    `json:"wait_id"`
	NodeID                string    `json:"node_id"`
	PlanDigest            string    `json:"plan_digest"`
	AuthorizationRevision uint64    `json:"authorization_revision"`
	AuthorizationDigest   string    `json:"authorization_digest"`
	DeclaredBy            string    `json:"declared_by"`
	DeclaredAt            time.Time `json:"declared_at"`
}

// State is the versioned execution-control sidecar. Revision advances once for
// every successful material Update; it is never inferred from run history.
type State struct {
	SchemaVersion        int                   `json:"schema_version"`
	Revision             uint64                `json:"revision"`
	Pause                *Pause                `json:"pause,omitempty"`
	Waits                []Wait                `json:"waits"`
	ExternalDeclarations []ExternalDeclaration `json:"external_declarations"`
}

// NewState returns the empty v1 control sidecar.
func NewState() State {
	return State{SchemaVersion: SchemaVersion, Waits: []Wait{}, ExternalDeclarations: []ExternalDeclaration{}}
}

// SetPause assigns the next durable pause revision. It must be used inside an
// Update callback; Update commits the matching State revision atomically.
func (state *State) SetPause(pause Pause) error {
	if state == nil {
		return errors.New("nil execution control state")
	}
	if state.Pause != nil {
		if state.Pause.GoalID == pause.GoalID && state.Pause.RunID == pause.RunID && state.Pause.WaitID == pause.WaitID &&
			state.Pause.Reason == pause.Reason && state.Pause.RequestedBy == pause.RequestedBy && state.Pause.RequestedAt.Equal(pause.RequestedAt) {
			return nil
		}
		return fmt.Errorf("execution control is already paused for Goal %q Run %q", state.Pause.GoalID, state.Pause.RunID)
	}
	pause.Revision = state.Revision + 1
	if err := validatePause(pause); err != nil {
		return err
	}
	state.Pause = &pause
	return nil
}

// ClearPause removes the active pause. The enclosing Update still advances the
// sidecar revision, leaving a durable monotonic boundary for a later pause.
func (state *State) ClearPause() { state.Pause = nil }

// AppendWait adds a wait once. Its stable ID is derived by the Runner from the
// charged ACTION receipt, so a crash retry can replay it without a second wait.
func (state *State) AppendWait(wait Wait) error {
	if state == nil {
		return errors.New("nil execution control state")
	}
	for _, existing := range state.Waits {
		if existing.ID != wait.ID {
			continue
		}
		if reflect.DeepEqual(existing, wait) {
			return nil
		}
		return fmt.Errorf("wait %q already exists with different contents", wait.ID)
	}
	if err := validateWait(wait); err != nil {
		return err
	}
	state.Waits = append(state.Waits, wait)
	return nil
}

// AppendExternalDeclaration appends declaration once. Retrying the exact same
// declaration ID and contents is a no-op; reuse with different contents fails.
func (state *State) AppendExternalDeclaration(declaration ExternalDeclaration) error {
	if state == nil {
		return errors.New("nil execution control state")
	}
	for _, existing := range state.ExternalDeclarations {
		if existing.ID != declaration.ID {
			continue
		}
		if reflect.DeepEqual(existing, declaration) {
			return nil
		}
		return fmt.Errorf("external declaration %q already exists with different contents", declaration.ID)
	}
	if err := state.validateDeclaration(declaration); err != nil {
		return err
	}
	state.ExternalDeclarations = append(state.ExternalDeclarations, declaration)
	return nil
}

// Validate rejects malformed or internally inconsistent control facts.
func (state State) Validate() error {
	if state.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported execution-control schema version %d", state.SchemaVersion)
	}
	if state.Waits == nil || state.ExternalDeclarations == nil {
		return errors.New("execution-control arrays must not be null")
	}
	if state.Pause != nil {
		if err := validatePause(*state.Pause); err != nil {
			return err
		}
		if state.Pause.Revision != state.Revision {
			return fmt.Errorf("pause revision %d does not match state revision %d", state.Pause.Revision, state.Revision)
		}
	}
	waits := make(map[string]Wait, len(state.Waits))
	for _, wait := range state.Waits {
		if err := validateWait(wait); err != nil {
			return err
		}
		if _, found := waits[wait.ID]; found {
			return fmt.Errorf("duplicate wait ID %q", wait.ID)
		}
		waits[wait.ID] = wait
	}
	if state.Pause != nil && state.Pause.WaitID != "" {
		wait, found := waits[state.Pause.WaitID]
		if !found || wait.GoalID != state.Pause.GoalID || wait.RunID != state.Pause.RunID {
			return fmt.Errorf("pause references an unknown or mismatched wait %q", state.Pause.WaitID)
		}
	}
	declarations := make(map[string]bool, len(state.ExternalDeclarations))
	for _, declaration := range state.ExternalDeclarations {
		if declarations[declaration.ID] {
			return fmt.Errorf("duplicate external declaration ID %q", declaration.ID)
		}
		declarations[declaration.ID] = true
		if err := validateDeclarationAgainstWait(declaration, waits); err != nil {
			return err
		}
	}
	return nil
}

func (state State) validateDeclaration(declaration ExternalDeclaration) error {
	waits := make(map[string]Wait, len(state.Waits))
	for _, wait := range state.Waits {
		waits[wait.ID] = wait
	}
	return validateDeclarationAgainstWait(declaration, waits)
}

func validatePause(pause Pause) error {
	if err := required("pause goal ID", pause.GoalID); err != nil {
		return err
	}
	if err := required("pause run ID", pause.RunID); err != nil {
		return err
	}
	if err := required("pause reason", pause.Reason); err != nil {
		return err
	}
	if err := required("pause requested-by", pause.RequestedBy); err != nil {
		return err
	}
	if pause.RequestedAt.IsZero() {
		return errors.New("pause requested-at is required")
	}
	if pause.Revision == 0 {
		return errors.New("pause revision must be positive")
	}
	return nil
}

func validateWait(wait Wait) error {
	if err := required("wait ID", wait.ID); err != nil {
		return err
	}
	if wait.Kind != WaitHuman && wait.Kind != WaitExternal {
		return fmt.Errorf("invalid wait kind %q", wait.Kind)
	}
	for name, value := range map[string]string{
		"wait goal ID": wait.GoalID, "wait run ID": wait.RunID, "wait node ID": wait.NodeID,
		"wait plan digest": wait.PlanDigest, "wait authorization digest": wait.AuthorizationDigest,
		"wait question": wait.Question,
	} {
		if err := required(name, value); err != nil {
			return err
		}
	}
	if wait.AuthorizationRevision == 0 {
		return errors.New("wait authorization revision must be positive")
	}
	if wait.Kind == WaitExternal && required("wait expected fact", wait.ExpectedFact) != nil {
		return errors.New("external wait expected fact is required")
	}
	if wait.Kind == WaitHuman && wait.ExpectedFact != "" {
		return errors.New("human wait cannot name an external fact")
	}
	return nil
}

func validateDeclarationAgainstWait(declaration ExternalDeclaration, waits map[string]Wait) error {
	for name, value := range map[string]string{
		"external declaration ID": declaration.ID, "external declaration fact name": declaration.FactName,
		"external declaration wait ID": declaration.WaitID, "external declaration node ID": declaration.NodeID,
		"external declaration plan digest": declaration.PlanDigest, "external declaration authorization digest": declaration.AuthorizationDigest,
		"external declaration declared-by": declaration.DeclaredBy,
	} {
		if err := required(name, value); err != nil {
			return err
		}
	}
	if declaration.DeclaredAt.IsZero() {
		return errors.New("external declaration declared-at is required")
	}
	if declaration.AuthorizationRevision == 0 {
		return errors.New("external declaration authorization revision must be positive")
	}
	wait, found := waits[declaration.WaitID]
	if !found {
		return fmt.Errorf("external declaration %q references unknown wait %q", declaration.ID, declaration.WaitID)
	}
	if wait.Kind != WaitExternal {
		return fmt.Errorf("external declaration %q references non-external wait %q", declaration.ID, declaration.WaitID)
	}
	if declaration.NodeID != wait.NodeID || declaration.PlanDigest != wait.PlanDigest ||
		declaration.AuthorizationRevision != wait.AuthorizationRevision || declaration.AuthorizationDigest != wait.AuthorizationDigest {
		return fmt.Errorf("external declaration %q does not exactly bind wait %q", declaration.ID, declaration.WaitID)
	}
	if declaration.FactName != wait.ExpectedFact {
		return fmt.Errorf("external declaration %q fact does not match wait %q", declaration.ID, declaration.WaitID)
	}
	return nil
}

func required(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	return nil
}
