package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// GenerationRetention is the only bridge from a repository-owned execution
// authorization to Bootstrap's user-home retention store. Implementations must
// acquire the marker before this package writes the generation reference into
// ForgePilot state. A failed state transaction can therefore leave an extra
// marker, but can never leave an unretained durable reference.
type GenerationRetention interface {
	Acquire(context.Context, work.ExecutionEngineGeneration, string) error
}

// BootstrapRetention uses the versioned Bootstrap helper protocol. The helper
// owns its state and lock; this package neither reads nor writes its retention
// directory. HelperPath must name the managed helper selected by the engine
// resolver, not an ambient PATH command.
type BootstrapRetention struct {
	HelperPath string
}

func (retention BootstrapRetention) Acquire(ctx context.Context, generation work.ExecutionEngineGeneration, reference string) error {
	if !filepath.IsAbs(retention.HelperPath) || strings.TrimSpace(retention.HelperPath) != retention.HelperPath {
		return errors.New("Bootstrap retention helper path must be absolute")
	}
	command := exec.CommandContext(ctx, retention.HelperPath,
		"retention-v1", "acquire",
		"--generation", generation.SourceCommit,
		"--payload-digest", generation.PayloadSHA256,
		"--reference", reference,
	)
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("acquire Bootstrap generation retention: %w", err)
	}
	var result struct {
		ProtocolVersion int    `json:"protocol_version"`
		Result          string `json:"result"`
		GenerationID    string `json:"generation_id"`
		PayloadDigest   string `json:"payload_digest"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return fmt.Errorf("decode Bootstrap retention result: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("Bootstrap retention result contains extra JSON values")
	}
	if result.ProtocolVersion != 1 || (result.Result != "acquired" && result.Result != "already_acquired") ||
		result.GenerationID != generation.SourceCommit || result.PayloadDigest != generation.PayloadSHA256 {
		return errors.New("Bootstrap retention helper returned an unexpected acquisition result")
	}
	return nil
}

// BindExecutionLaunchIdentity acquires a generation-retention marker and then
// atomically pins directly observed Worker and engine facts to the current
// authorization. It deliberately leaves a marker behind when the state write
// loses a race or fails: an orphan marker blocks deletion safely, whereas a
// persisted engine reference without a marker would allow a later prune to
// break a resumable authorization.
func BindExecutionLaunchIdentity(ctx context.Context, root, goalID string, identity RunnerIdentity,
	generation work.ExecutionEngineGeneration, retention GenerationRetention, now time.Time) (work.ExecutionAuthorization, error) {
	if retention == nil {
		return work.ExecutionAuthorization{}, errors.New("Bootstrap generation retention is required")
	}
	state, err := storage.Load(root)
	if err != nil {
		return work.ExecutionAuthorization{}, err
	}
	goal, ok := state.GoalByID(goalID)
	if !ok || goal.Execution == nil || len(goal.Execution.Authorizations) == 0 {
		return work.ExecutionAuthorization{}, fmt.Errorf("goal %q has no current execution authorization", goalID)
	}
	current := goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1]
	if err := validateIdentityForBinding(current, identity, generation, now); err != nil {
		return work.ExecutionAuthorization{}, err
	}
	exactlyBound := false
	if current.WorkerIdentity != nil || current.EngineGeneration != nil {
		if current.WorkerIdentity != nil && current.EngineGeneration != nil &&
			current.WorkerIdentity.ExecutablePath == identity.ExecutablePath &&
			current.WorkerIdentity.ExecutableSHA256 == current.WorkerProfile.ExecutableSHA256 &&
			current.WorkerIdentity.ReportedVersion == identity.Version &&
			*current.EngineGeneration == generation {
			exactlyBound = true
		} else {
			return work.ExecutionAuthorization{}, errors.New("current execution authorization is already bound to a different Worker or engine identity")
		}
	}
	reference, err := generationRetentionReference(current, generation)
	if err != nil {
		return work.ExecutionAuthorization{}, err
	}
	if err := retention.Acquire(ctx, generation, reference); err != nil {
		return work.ExecutionAuthorization{}, err
	}
	if exactlyBound {
		return current, nil
	}
	expectedAuthorizationDigest := current.Digest
	var bound work.ExecutionAuthorization
	err = storage.Update(root, func(state *work.State) error {
		goal, ok := state.GoalByID(goalID)
		if !ok || goal.Execution == nil || len(goal.Execution.Authorizations) == 0 {
			return fmt.Errorf("goal %q has no current execution authorization", goalID)
		}
		current := goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1]
		if current.Digest != expectedAuthorizationDigest {
			return errors.New("execution authorization changed while acquiring its Bootstrap generation retention")
		}
		worker := work.ResolvedWorkerIdentity{ExecutablePath: identity.ExecutablePath, ExecutableSHA256: current.WorkerProfile.ExecutableSHA256,
			ReportedVersion: identity.Version, ObservedAt: now.UTC()}
		var err error
		bound, err = state.BindCurrentExecutionIdentity(goalID, worker, generation)
		return err
	})
	if err != nil {
		return work.ExecutionAuthorization{}, err
	}
	return bound, nil
}

// generationRetentionReference is deterministic from immutable authorization
// facts and the generation tuple. Its raw 256-bit value is passed only to the
// Bootstrap helper; Bootstrap persists its hash and ForgePilot does not store
// the raw reference in repository state.
func generationRetentionReference(authorization work.ExecutionAuthorization, generation work.ExecutionEngineGeneration) (string, error) {
	input := struct {
		GoalID            string                         `json:"goal_id"`
		Workspace         string                         `json:"workspace"`
		Revision          int                            `json:"revision"`
		PlanBindingDigest string                         `json:"plan_binding_digest"`
		RequestSHA256     string                         `json:"request_sha256"`
		ApprovalToken     string                         `json:"approval_token"`
		WorkerProfile     work.WorkerProfile             `json:"worker_profile"`
		Generation        work.ExecutionEngineGeneration `json:"generation"`
	}{
		GoalID: authorization.GoalID, Workspace: authorization.Workspace, Revision: authorization.Revision,
		PlanBindingDigest: authorization.PlanBindingDigest, RequestSHA256: authorization.RequestSHA256,
		ApprovalToken: authorization.ApprovalToken, WorkerProfile: authorization.WorkerProfile, Generation: generation,
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("forgepilot.generation-retention-reference/v1"))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(encoded)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func validateIdentityForBinding(authorization work.ExecutionAuthorization, identity RunnerIdentity,
	generation work.ExecutionEngineGeneration, now time.Time) error {
	if !now.UTC().Before(authorization.ExpiresAt) {
		return errors.New("execution authorization has expired")
	}
	profile := authorization.WorkerProfile
	if profile.Runtime != identity.Runtime || profile.ExecutablePath != identity.ExecutablePath ||
		profile.Model != identity.Model || profile.Effort != identity.Effort || profile.Sandbox != identity.Sandbox ||
		strings.TrimSpace(identity.Version) == "" || identity.Version != strings.TrimSpace(identity.Version) {
		return errors.New("runner runtime does not match the current execution Worker Profile")
	}
	digest, err := executableDigest(identity.ExecutablePath)
	if err != nil {
		return fmt.Errorf("digest Worker executable: %w", err)
	}
	if digest != profile.ExecutableSHA256 {
		return errors.New("runner executable does not match the current execution Worker Profile digest")
	}
	if len(generation.SourceCommit) != 40 || strings.Trim(generation.SourceCommit, "0123456789abcdef") != "" ||
		len(generation.PayloadSHA256) != len("sha256:")+64 || !strings.HasPrefix(generation.PayloadSHA256, "sha256:") ||
		strings.Trim(generation.PayloadSHA256[len("sha256:"):], "0123456789abcdef") != "" {
		return errors.New("ForgePilot engine generation identity is invalid")
	}
	return nil
}
