package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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

// ResolvedBootstrapGeneration is the current immutable generation as observed
// through its managed Bootstrap helper. HelperPath is the exact helper that
// authenticated the tuple and is safe to pass to BootstrapRetention; it is
// never resolved through PATH.
type ResolvedBootstrapGeneration struct {
	Generation work.ExecutionEngineGeneration
	HelperPath string
}

// EngineGenerationResolver supplies a managed Bootstrap generation to Runner
// admission. It deliberately has no repository identity input: Bootstrap
// retention must not learn which repository or job holds a generation.
type EngineGenerationResolver interface {
	Resolve(context.Context) (ResolvedBootstrapGeneration, error)
}

// EnsureExecutionLaunchIdentity is the only Runner-facing orchestration for
// managed-generation admission. It resolves the generation, acquires its
// retention marker through the exact helper that resolved it, and only then
// reseals the current authorization with the immutable engine identity.
// expectedAuthorizationDigest prevents an old Run intent or exact resume from
// binding a newer authorization on its behalf.
func EnsureExecutionLaunchIdentity(ctx context.Context, root, goalID, expectedAuthorizationDigest string,
	identity RunnerIdentity, resolver EngineGenerationResolver, now time.Time) (work.ExecutionAuthorization, RunnerIdentity, error) {
	return ensureExecutionLaunchIdentity(ctx, root, goalID, expectedAuthorizationDigest, identity, resolver, now, false)
}

// ReacquireExecutionLaunchIdentity is the exact-run counterpart to
// EnsureExecutionLaunchIdentity. An exact or pending resume may renew the
// deterministic retention marker, but it must never turn an older Run Record
// into a first binding for the current authorization.
func ReacquireExecutionLaunchIdentity(ctx context.Context, root, goalID, expectedAuthorizationDigest string,
	identity RunnerIdentity, resolver EngineGenerationResolver, now time.Time) (work.ExecutionAuthorization, RunnerIdentity, error) {
	return ensureExecutionLaunchIdentity(ctx, root, goalID, expectedAuthorizationDigest, identity, resolver, now, true)
}

func ensureExecutionLaunchIdentity(ctx context.Context, root, goalID, expectedAuthorizationDigest string,
	identity RunnerIdentity, resolver EngineGenerationResolver, now time.Time, requireExistingBinding bool) (work.ExecutionAuthorization, RunnerIdentity, error) {
	if resolver == nil {
		return work.ExecutionAuthorization{}, RunnerIdentity{}, errors.New("managed ForgePilot generation resolver is required")
	}
	if expectedAuthorizationDigest == "" {
		return work.ExecutionAuthorization{}, RunnerIdentity{}, errors.New("expected execution authorization digest is required")
	}
	state, err := storage.Load(root)
	if err != nil {
		return work.ExecutionAuthorization{}, RunnerIdentity{}, err
	}
	goal, ok := state.GoalByID(goalID)
	if !ok || goal.Execution == nil || len(goal.Execution.Authorizations) == 0 {
		return work.ExecutionAuthorization{}, RunnerIdentity{}, fmt.Errorf("goal %q has no current execution authorization", goalID)
	}
	current := goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1]
	if current.Digest != expectedAuthorizationDigest {
		return work.ExecutionAuthorization{}, RunnerIdentity{}, errors.New("runner is bound to a different execution authorization")
	}
	if requireExistingBinding && (current.WorkerIdentity == nil || current.EngineGeneration == nil) {
		return work.ExecutionAuthorization{}, RunnerIdentity{}, errors.New("exact Runner resume requires an existing managed launch identity")
	}
	// Model and effort are authorized profile selections in this Runner MVP;
	// their command-line controls are intentionally deferred. Fill only absent
	// values so a future observed or explicitly configured mismatch remains a
	// fail-closed identity error at the binding boundary.
	if identity.Model == "" {
		identity.Model = current.WorkerProfile.Model
	}
	if identity.Effort == "" {
		identity.Effort = current.WorkerProfile.Effort
	}
	resolved, err := resolver.Resolve(ctx)
	if err != nil {
		return work.ExecutionAuthorization{}, RunnerIdentity{}, fmt.Errorf("resolve managed ForgePilot generation: %w", err)
	}
	bound, err := BindExecutionLaunchIdentity(ctx, root, goalID, identity, resolved.Generation,
		BootstrapRetention{HelperPath: resolved.HelperPath}, now)
	if err != nil {
		return work.ExecutionAuthorization{}, RunnerIdentity{}, err
	}
	identity.EngineGeneration = &resolved.Generation
	return bound, identity, nil
}

// BootstrapProcessImage is the immutable ForgePilot image identity captured
// when the process starts. It must never be reconstructed from a stable link
// during admission: an upgrade may change current while this process still
// runs an older generation.
type BootstrapProcessImage struct {
	executablePath string
	homePath       string
	managedRoot    string
}

// CaptureBootstrapProcessImage records the executable image before a caller
// can begin work that relies on the managed Bootstrap generation. The managed root
// is the Bootstrap-owned ~/.local/share/forgepilot root selected at startup.
// The resolver deliberately does not call this itself, because that would
// turn a later stable-link resolution into an authorization fact.
func CaptureBootstrapProcessImage(home string) (BootstrapProcessImage, error) {
	if !validAbsolutePath(home) {
		return BootstrapProcessImage{}, errors.New("Bootstrap home must be an absolute clean path")
	}
	executable, err := os.Executable()
	if err != nil {
		return BootstrapProcessImage{}, fmt.Errorf("discover ForgePilot process image: %w", err)
	}
	canonicalExecutable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return BootstrapProcessImage{}, fmt.Errorf("resolve ForgePilot process image: %w", err)
	}
	if canonicalExecutable != executable {
		return BootstrapProcessImage{}, errors.New("ForgePilot process image was not canonicalized at startup")
	}
	if !validAbsolutePath(executable) {
		return BootstrapProcessImage{}, errors.New("ForgePilot process image is not an absolute clean path")
	}
	return BootstrapProcessImage{executablePath: executable, homePath: home,
		managedRoot: filepath.Join(home, ".local", "share", "forgepilot")}, nil
}

// BootstrapGenerationResolver asks only the helper adjacent to the generation
// that launched this process. It never accepts a caller-selected helper or
// resolves current: the process image anchors helper trust and makes an
// upgrade between launch and admission fail closed.
type BootstrapGenerationResolver struct {
	ProcessImage BootstrapProcessImage
}

func (resolver BootstrapGenerationResolver) Resolve(ctx context.Context) (ResolvedBootstrapGeneration, error) {
	generationID, helperPath, err := resolver.ProcessImage.generationHelperPath()
	if err != nil {
		return ResolvedBootstrapGeneration{}, err
	}
	output, err := exec.CommandContext(ctx, helperPath, "generation-v1", "current").Output()
	if err != nil {
		return ResolvedBootstrapGeneration{}, fmt.Errorf("resolve current Bootstrap generation: %w", err)
	}
	var result struct {
		ProtocolVersion int    `json:"protocol_version"`
		GenerationID    string `json:"generation_id"`
		PayloadDigest   string `json:"payload_digest"`
		ForgePilotPath  string `json:"forgepilot_path"`
		HelperPath      string `json:"helper_path"`
	}
	if err := decodeStrictProtocolJSON(output, &result); err != nil {
		return ResolvedBootstrapGeneration{}, fmt.Errorf("decode Bootstrap generation result: %w", err)
	}
	generation := work.ExecutionEngineGeneration{SourceCommit: result.GenerationID, PayloadSHA256: result.PayloadDigest}
	expectedExecutable := resolver.ProcessImage.executablePath
	if !validAbsolutePath(result.ForgePilotPath) || !validAbsolutePath(result.HelperPath) {
		return ResolvedBootstrapGeneration{}, errors.New("Bootstrap helper returned invalid managed paths")
	}
	if result.ProtocolVersion != 1 || !validEngineGeneration(generation) ||
		generation.SourceCommit != generationID || result.ForgePilotPath != expectedExecutable || result.HelperPath != helperPath {
		return ResolvedBootstrapGeneration{}, errors.New("Bootstrap helper did not confirm the current managed ForgePilot generation")
	}
	return ResolvedBootstrapGeneration{Generation: generation, HelperPath: helperPath}, nil
}

func (image BootstrapProcessImage) generationHelperPath() (string, string, error) {
	if !validAbsolutePath(image.executablePath) || !validAbsolutePath(image.homePath) || !validAbsolutePath(image.managedRoot) ||
		image.managedRoot != filepath.Join(image.homePath, ".local", "share", "forgepilot") {
		return "", "", errors.New("Bootstrap process image paths must be absolute clean paths")
	}
	relative, err := filepath.Rel(image.managedRoot, image.executablePath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", errors.New("ForgePilot process image is outside the managed Bootstrap root")
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if len(parts) != 4 || parts[0] != "versions" || !validEngineGeneration(work.ExecutionEngineGeneration{SourceCommit: parts[1], PayloadSHA256: "sha256:" + strings.Repeat("0", 64)}) ||
		parts[2] != "bin" || parts[3] != "forgepilot" {
		return "", "", errors.New("ForgePilot process image is not a managed generation executable")
	}
	helper := filepath.Join(image.managedRoot, "versions", parts[1], "libexec", "forgepilot-bootstrap")
	stableHelper := filepath.Join(image.homePath, ".local", "bin", "forgepilot-bootstrap")
	if err := validateManagedHelperPaths(image, helper); err != nil {
		return "", "", err
	}
	if err := safeManagedSymlink(stableHelper, os.Getuid()); err != nil {
		return "", "", err
	}
	target, err := os.Readlink(stableHelper)
	if err != nil || target != filepath.Join(image.managedRoot, "current", "libexec", "forgepilot-bootstrap") {
		return "", "", errors.New("Bootstrap stable helper link is missing or drifted")
	}
	anchoredHelper, err := filepath.EvalSymlinks(stableHelper)
	canonicalHelper, canonicalErr := filepath.EvalSymlinks(helper)
	if err != nil || canonicalErr != nil || anchoredHelper != canonicalHelper {
		return "", "", errors.New("Bootstrap stable helper does not select the process generation")
	}
	return parts[1], helper, nil
}

// validateManagedHelperPaths checks the metadata facts Bootstrap itself relies
// on before executing the generation-local shell helper. This is intentionally
// not a second manifest/payload implementation: Bootstrap remains the sole
// owner of that validation, but a substituted or unsafe path must never be
// executed to reach it.
func validateManagedHelperPaths(image BootstrapProcessImage, helper string) error {
	uid := os.Getuid()
	for _, path := range []struct {
		path    string
		private bool
	}{
		{image.homePath, false},
		{filepath.Join(image.homePath, ".local"), false},
		{filepath.Join(image.homePath, ".local", "share"), false},
		{filepath.Join(image.homePath, ".local", "bin"), false},
		{image.managedRoot, true},
		{filepath.Join(image.managedRoot, "versions"), true},
		{filepath.Join(image.managedRoot, "versions", filepath.Base(filepath.Dir(filepath.Dir(helper)))), true},
		{filepath.Dir(helper), true},
	} {
		if err := safeManagedDirectory(path.path, uid, path.private); err != nil {
			return err
		}
	}
	for _, path := range []string{image.executablePath, helper} {
		if err := safeManagedExecutable(path, uid); err != nil {
			return err
		}
	}
	return nil
}

func safeManagedDirectory(path string, uid int, private bool) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("managed directory %q is missing or unsafe", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != uid || info.Mode().Perm()&0022 != 0 || info.Mode().Perm()&0700 != 0700 ||
		(private && info.Mode().Perm()&0077 != 0) {
		return fmt.Errorf("managed directory %q has unsafe ownership or mode", path)
	}
	return nil
}

func safeManagedExecutable(path string, uid int) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("managed executable %q is missing or unsafe", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != uid || stat.Nlink != 1 || info.Mode().Perm()&0022 != 0 || info.Mode().Perm()&0400 == 0 {
		return fmt.Errorf("managed executable %q has unsafe ownership, mode, or links", path)
	}
	return nil
}

func safeManagedSymlink(path string, uid int) error {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("managed symlink %q is missing or unsafe", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != uid {
		return fmt.Errorf("managed symlink %q has unexpected ownership", path)
	}
	return nil
}

func validAbsolutePath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && strings.TrimSpace(path) == path
}

// decodeStrictProtocolJSON accepts exactly one JSON object, rejects duplicate
// member names before they are collapsed by encoding/json, then preserves the
// existing unknown-field rejection on the typed protocol result.
func decodeStrictProtocolJSON(input []byte, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(string(input)))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return errors.New("protocol result must be a JSON object")
	}
	members := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := token.(string)
		if !ok {
			return errors.New("protocol result has a non-string member name")
		}
		if _, duplicate := members[name]; duplicate {
			return fmt.Errorf("protocol result has duplicate member %q", name)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		members[name] = value
	}
	if token, err := decoder.Token(); err != nil {
		return err
	} else if delimiter, ok := token.(json.Delim); !ok || delimiter != '}' {
		return errors.New("protocol result did not end its object")
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) || token != nil {
		return errors.New("protocol result contains extra JSON values")
	}
	normalized, err := json.Marshal(members)
	if err != nil {
		return err
	}
	decoder = json.NewDecoder(strings.NewReader(string(normalized)))
	decoder.DisallowUnknownFields()
	return decoder.Decode(destination)
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
	if err := decodeStrictProtocolJSON(output, &result); err != nil {
		return fmt.Errorf("decode Bootstrap retention result: %w", err)
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
	if !validEngineGeneration(generation) {
		return errors.New("ForgePilot engine generation identity is invalid")
	}
	return nil
}

func validEngineGeneration(generation work.ExecutionEngineGeneration) bool {
	return len(generation.SourceCommit) == 40 && strings.Trim(generation.SourceCommit, "0123456789abcdef") == "" &&
		len(generation.PayloadSHA256) == len("sha256:")+64 && strings.HasPrefix(generation.PayloadSHA256, "sha256:") &&
		strings.Trim(generation.PayloadSHA256[len("sha256:"):], "0123456789abcdef") == ""
}
