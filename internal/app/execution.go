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
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

const (
	executionPlanRequestVersion = "forgepilot.execution-plan-request/v1"
	ExecutionPlanVersion        = "forgepilot.execution-plan/v1"
	maxExecutionRequestBytes    = 1 << 20
)

type executionPlanRequest struct {
	FormatVersion               string                      `json:"formatVersion"`
	ExpectedAuthorizationDigest string                      `json:"expectedAuthorizationDigest,omitempty"`
	GoalPlanRequest             GoalPreflightRequest        `json:"goalPlanRequest"`
	WorkerProfile               executionWorkerProfileInput `json:"workerProfile"`
	Caps                        executionCapsInput          `json:"caps"`
	ExpiresAt                   string                      `json:"expiresAt"`
}

type executionWorkerProfileInput struct {
	Runtime        string `json:"runtime"`
	ExecutablePath string `json:"executablePath"`
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	Sandbox        string `json:"sandbox"`
}

type executionCapsInput struct {
	MaxSteps                    int `json:"maxSteps"`
	MaxTechnicalAttemptsPerNode int `json:"maxTechnicalAttemptsPerNode"`
	MaxRuns                     int `json:"maxRuns"`
	MaxRecoveries               int `json:"maxRecoveries"`
	MaxHandoffBytes             int `json:"maxHandoffBytes"`
	MaxWriteBytes               int `json:"maxWriteBytes"`
	MaxRunBytes                 int `json:"maxRunBytes"`
	MaxTotalBytes               int `json:"maxTotalBytes"`
}

type ExecutionPlanProjection struct {
	Version                    string                          `json:"version"`
	Workspace                  string                          `json:"workspace,omitempty"`
	GoalID                     string                          `json:"goalId,omitempty"`
	RequestSHA256              string                          `json:"requestSha256,omitempty"`
	RegistrationSHA256         string                          `json:"registrationSha256,omitempty"`
	CurrentAuthorizationDigest string                          `json:"currentAuthorizationDigest,omitempty"`
	ApprovalToken              string                          `json:"approvalToken,omitempty"`
	ExpiresAt                  string                          `json:"expiresAt,omitempty"`
	GoalPlan                   *GoalPreflightProjection        `json:"goalPlan,omitempty"`
	Artifacts                  []work.ExecutionArtifactBinding `json:"artifacts,omitempty"`
	WorkerProfile              work.WorkerProfile              `json:"workerProfile"`
	Caps                       work.ExecutionCaps              `json:"caps"`
	Diagnostics                []GoalPreflightDiagnostic       `json:"diagnostics"`
}

func newExecutionPlanProjection() ExecutionPlanProjection {
	return ExecutionPlanProjection{Version: ExecutionPlanVersion, Diagnostics: []GoalPreflightDiagnostic{}}
}

// PlanExecutionFile returns a read-only approval projection. Request files,
// plan artifacts, registration and the executable are only read; no command
// or runtime probe is started.
func PlanExecutionFile(ctx context.Context, root, requestPath string) (ExecutionPlanProjection, error) {
	projection := newExecutionPlanProjection()
	if err := ctx.Err(); err != nil {
		return projection, err
	}
	body, err := readContainedRegularFileLimit(root, requestPath, maxExecutionRequestBytes)
	if err != nil {
		projection.fail("invalid-request", fmt.Sprintf("request: %v", err))
		return projection, nil
	}
	return planExecutionBytes(ctx, root, body, time.Now().UTC())
}

func planExecutionBytes(ctx context.Context, root string, body []byte, now time.Time) (ExecutionPlanProjection, error) {
	projection := newExecutionPlanProjection()
	request, err := parseExecutionPlanRequest(body)
	if err != nil {
		projection.fail("invalid-request", err.Error())
		return projection, nil
	}
	if request.FormatVersion != executionPlanRequestVersion {
		projection.fail("invalid-request", "formatVersion must be forgepilot.execution-plan-request/v1")
		return projection, nil
	}
	caps, err := executionCaps(request.Caps)
	if err != nil {
		projection.fail("invalid-limits", err.Error())
		return projection, nil
	}
	profile, err := resolveExecutionWorkerProfile(request.WorkerProfile)
	if err != nil {
		projection.fail("invalid-profile", err.Error())
		return projection, nil
	}
	expiresAt, err := parseExecutionExpiry(request.ExpiresAt, now)
	if err != nil {
		projection.fail("invalid-expiry", err.Error())
		return projection, nil
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return projection, err
	}
	projection.Workspace = canonicalRoot
	projection.GoalID = request.GoalPlanRequest.GoalID
	projection.RequestSHA256 = "sha256:" + digest(body)
	projection.ExpiresAt = expiresAt.Format(time.RFC3339Nano)
	projection.WorkerProfile = profile
	projection.Caps = caps

	goalPlan, err := PreflightGoalPlan(ctx, canonicalRoot, request.GoalPlanRequest)
	projection.GoalPlan = &goalPlan
	if err != nil {
		return projection, err
	}
	if len(goalPlan.Diagnostics) != 0 {
		projection.Diagnostics = append(projection.Diagnostics, goalPlan.Diagnostics...)
		return projection, nil
	}
	state, err := storage.Load(canonicalRoot)
	if err != nil {
		return projection, err
	}
	binding, err := executionPlanBinding(goalPlan, request.GoalPlanRequest, projection.RequestSHA256)
	if err != nil {
		projection.fail("invalid-plan", err.Error())
		return projection, nil
	}
	if err := state.ValidateExecutionPlanRegistration(request.GoalPlanRequest.GoalID, binding.Nodes); err != nil {
		projection.fail("registration-changed", err.Error())
		return projection, nil
	}
	registrationDigest, err := executionRegistrationDigest(state, request.GoalPlanRequest.GoalID)
	if err != nil {
		return projection, err
	}
	projection.RegistrationSHA256 = registrationDigest
	projection.Artifacts = executionArtifactDigests(goalPlan, request.GoalPlanRequest)
	tokenInput := executionApprovalInput{
		RequestSHA256: projection.RequestSHA256, Workspace: canonicalRoot, GoalID: request.GoalPlanRequest.GoalID,
		RegistrationSHA256: registrationDigest, AuthorizationPresent: false, ExpiresAt: projection.ExpiresAt,
		Artifacts: projection.Artifacts, WorkerProfile: profile, Caps: caps,
	}
	projection.ApprovalToken, err = executionDomainDigest("forgepilot.execution-plan-approval/v1", tokenInput)
	if err != nil {
		return projection, err
	}
	return projection, nil
}

func parseExecutionPlanRequest(body []byte) (executionPlanRequest, error) {
	var request executionPlanRequest
	object, err := artifactObjectWithLimits(body, maxExecutionRequestBytes, maxGoalPlanJSONDepth)
	if err != nil {
		return request, err
	}
	if err := allowed(object, "formatVersion", "expectedAuthorizationDigest", "goalPlanRequest", "workerProfile", "caps", "expiresAt"); err != nil {
		return request, err
	}
	for _, field := range []struct {
		name string
		into any
	}{
		{"formatVersion", &request.FormatVersion},
		{"goalPlanRequest", &request.GoalPlanRequest},
		{"workerProfile", &request.WorkerProfile},
		{"caps", &request.Caps},
		{"expiresAt", &request.ExpiresAt},
	} {
		if err := decodeRequired(object, field.name, field.into); err != nil {
			return request, fmt.Errorf("request field %s is required or invalid", field.name)
		}
	}
	if raw, exists := object["expectedAuthorizationDigest"]; exists {
		if err := json.Unmarshal(raw, &request.ExpectedAuthorizationDigest); err != nil {
			return request, errors.New("request field expectedAuthorizationDigest is invalid")
		}
	}
	return request, nil
}

func executionCaps(input executionCapsInput) (work.ExecutionCaps, error) {
	caps := work.ExecutionCaps{
		MaxSteps: input.MaxSteps, MaxTechnicalAttemptsPerNode: input.MaxTechnicalAttemptsPerNode,
		MaxRuns: input.MaxRuns, MaxRecoveries: input.MaxRecoveries,
		Artifacts: work.ExecutionArtifactLimits{
			MaxHandoffBytes: input.MaxHandoffBytes, MaxWriteBytes: input.MaxWriteBytes,
			MaxRunBytes: input.MaxRunBytes, MaxTotalBytes: input.MaxTotalBytes,
		},
	}
	if caps.MaxSteps < 1 || caps.MaxTechnicalAttemptsPerNode < 1 || caps.MaxRuns < 1 || caps.MaxRecoveries < 1 {
		return work.ExecutionCaps{}, errors.New("maxSteps, maxTechnicalAttemptsPerNode, maxRuns, and maxRecoveries must be positive")
	}
	if caps.Artifacts.MaxHandoffBytes < 1 || caps.Artifacts.MaxWriteBytes < 1 || caps.Artifacts.MaxRunBytes < 1 || caps.Artifacts.MaxTotalBytes < 1 {
		return work.ExecutionCaps{}, errors.New("all artifact limits must be positive")
	}
	if caps.Artifacts.MaxWriteBytes > caps.Artifacts.MaxRunBytes || caps.Artifacts.MaxRunBytes > caps.Artifacts.MaxTotalBytes {
		return work.ExecutionCaps{}, errors.New("artifact limits must satisfy maxWriteBytes <= maxRunBytes <= maxTotalBytes")
	}
	return caps, nil
}

func resolveExecutionWorkerProfile(input executionWorkerProfileInput) (work.WorkerProfile, error) {
	if input.Runtime != "codex" || !filepath.IsAbs(input.ExecutablePath) || filepath.Clean(input.ExecutablePath) != input.ExecutablePath ||
		strings.TrimSpace(input.Model) == "" || input.Model != strings.TrimSpace(input.Model) ||
		input.Effort != "medium" || input.Sandbox != "workspace-write" {
		return work.WorkerProfile{}, errors.New("workerProfile must explicitly provide a Codex executable, fixed model, medium effort, and workspace-write sandbox")
	}
	canonicalPath, err := filepath.EvalSymlinks(input.ExecutablePath)
	if err != nil {
		return work.WorkerProfile{}, fmt.Errorf("resolve worker executable: %w", err)
	}
	info, err := os.Stat(canonicalPath)
	if err != nil {
		return work.WorkerProfile{}, fmt.Errorf("stat worker executable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return work.WorkerProfile{}, errors.New("worker executable must resolve to a regular executable file")
	}
	file, err := os.Open(canonicalPath)
	if err != nil {
		return work.WorkerProfile{}, fmt.Errorf("read worker executable: %w", err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return work.WorkerProfile{}, fmt.Errorf("hash worker executable: %w", copyErr)
	}
	if closeErr != nil {
		return work.WorkerProfile{}, fmt.Errorf("close worker executable: %w", closeErr)
	}
	profile := work.WorkerProfile{
		Runtime: input.Runtime, ExecutablePath: canonicalPath, ExecutableSHA256: hex.EncodeToString(hash.Sum(nil)),
		Model: input.Model, Effort: input.Effort, Sandbox: input.Sandbox,
	}
	if err := work.ValidateWorkerProfile(profile); err != nil {
		return work.WorkerProfile{}, err
	}
	return profile, nil
}

func parseExecutionExpiry(value string, now time.Time) (time.Time, error) {
	if !strings.HasSuffix(value, "Z") {
		return time.Time{}, errors.New("expiresAt must be an absolute UTC RFC 3339 timestamp ending in Z")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || expiresAt.Format(time.RFC3339Nano) != value {
		return time.Time{}, errors.New("expiresAt must be a valid absolute UTC RFC 3339 timestamp")
	}
	if !expiresAt.After(now) {
		return time.Time{}, errors.New("expiresAt must be in the future")
	}
	if expiresAt.Sub(now) > work.MaxExecutionAuthorizationLifetime {
		return time.Time{}, errors.New("expiresAt must be no more than fourteen days from this operation")
	}
	return expiresAt.UTC(), nil
}

type executionApprovalInput struct {
	RequestSHA256        string                          `json:"requestSha256"`
	Workspace            string                          `json:"workspace"`
	GoalID               string                          `json:"goalId"`
	RegistrationSHA256   string                          `json:"registrationSha256"`
	AuthorizationPresent bool                            `json:"authorizationPresent"`
	ExpiresAt            string                          `json:"expiresAt"`
	Artifacts            []work.ExecutionArtifactBinding `json:"artifacts"`
	WorkerProfile        work.WorkerProfile              `json:"workerProfile"`
	Caps                 work.ExecutionCaps              `json:"caps"`
}

type executionRegistration struct {
	GoalID               string                    `json:"goalId"`
	Workspace            string                    `json:"workspace"`
	Status               work.GoalStatus           `json:"status"`
	ReviewPolicy         work.ReviewPolicy         `json:"reviewPolicy"`
	CompletionPolicy     work.CompletionPolicy     `json:"completionPolicy"`
	AuthorizationPresent bool                      `json:"authorizationPresent"`
	WorkItems            []executionRegisteredWork `json:"workItems"`
}

type executionRegisteredWork struct {
	ID        string   `json:"id"`
	StoryRef  string   `json:"storyRef"`
	DependsOn []string `json:"dependsOn"`
}

func executionRegistrationDigest(state work.State, goalID string) (string, error) {
	goal, ok := state.GoalByID(goalID)
	if !ok {
		return "", fmt.Errorf("unknown goal %q", goalID)
	}
	registration := executionRegistration{
		GoalID: goal.ID, Workspace: goal.Repository, Status: goal.Status,
		ReviewPolicy: goal.ReviewPolicy, CompletionPolicy: goal.CompletionPolicy,
		AuthorizationPresent: goal.Execution != nil,
		WorkItems:            make([]executionRegisteredWork, 0),
	}
	for _, item := range state.WorkItems {
		if item.GoalID != goalID {
			continue
		}
		dependencies := append([]string(nil), item.DependsOn...)
		sort.Strings(dependencies)
		registration.WorkItems = append(registration.WorkItems, executionRegisteredWork{ID: item.ID, StoryRef: item.StoryRef, DependsOn: dependencies})
	}
	sort.Slice(registration.WorkItems, func(i, j int) bool { return registration.WorkItems[i].ID < registration.WorkItems[j].ID })
	return executionDomainDigest("forgepilot.execution-registration/v1", registration)
}

func executionPlanBinding(projection GoalPreflightProjection, request GoalPreflightRequest, requestSHA256 string) (work.GoalPlanBinding, error) {
	if projection.Manifest == nil || projection.CoverageReview == nil || projection.coverageReviewSHA256 == "" {
		return work.GoalPlanBinding{}, errors.New("validated Goal Plan is missing its exact manifest or coverage-review digest")
	}
	mappings := make(map[string]string, len(projection.NodeMappings))
	for _, mapping := range projection.NodeMappings {
		if _, exists := mappings[mapping.PlanNodeRef]; exists {
			return work.GoalPlanBinding{}, fmt.Errorf("duplicate Plan Node Reference %q", mapping.PlanNodeRef)
		}
		mappings[mapping.PlanNodeRef] = mapping.WorkItemID
	}
	manifest := projection.Manifest
	if len(mappings) != len(manifest.Nodes) {
		return work.GoalPlanBinding{}, errors.New("explicit node mapping does not cover the complete manifest")
	}
	binding := work.GoalPlanBinding{
		Revision:      1,
		RequestSHA256: requestSHA256, PlanID: manifest.Plan.ID, PlanRevision: manifest.Plan.Revision,
		ManifestSHA256: manifest.Digest, CoverageReviewID: projection.CoverageReview.ReviewID,
		Manifest:        work.ExecutionArtifactBinding{Path: request.ManifestPath, SHA256: manifest.Digest},
		CoverageReview:  work.ExecutionArtifactBinding{Path: request.CoverageReviewPath, SHA256: projection.coverageReviewSHA256},
		Declaration:     work.ExecutionArtifactBinding{Path: manifest.Declaration.Path, SHA256: manifest.Declaration.SHA256},
		AdoptedAt:       time.Time{},
		ReviewedSources: make([]work.ExecutionArtifactBinding, 0, len(manifest.ReviewedSources)),
		Nodes:           make([]work.ExecutionPlanNodeBinding, 0, len(manifest.Nodes)),
	}
	for _, source := range manifest.ReviewedSources {
		binding.ReviewedSources = append(binding.ReviewedSources, work.ExecutionArtifactBinding{Path: source.Path, SHA256: source.SHA256})
	}
	for _, node := range manifest.Nodes {
		workItemID, exists := mappings[node.NodeRef]
		if !exists {
			return work.GoalPlanBinding{}, fmt.Errorf("Plan Node %q has no explicit Work Item mapping", node.NodeRef)
		}
		binding.Nodes = append(binding.Nodes, work.ExecutionPlanNodeBinding{
			PlanNodeRef: node.NodeRef, StoryRef: node.StoryRef,
			ReadinessContract: work.ExecutionArtifactBinding{Path: node.ReadinessContract.Path, SHA256: node.ReadinessContract.SHA256},
			DependsOn:         append([]string(nil), node.DependsOn...), WorkItemID: workItemID,
		})
	}
	return binding, nil
}

func executionArtifactDigests(projection GoalPreflightProjection, request GoalPreflightRequest) []work.ExecutionArtifactBinding {
	if projection.Manifest == nil {
		return nil
	}
	artifacts := []work.ExecutionArtifactBinding{
		{Path: request.ManifestPath, SHA256: projection.Manifest.Digest},
		{Path: request.CoverageReviewPath, SHA256: projection.coverageReviewSHA256},
		{Path: projection.Manifest.Declaration.Path, SHA256: projection.Manifest.Declaration.SHA256},
	}
	for _, source := range projection.Manifest.ReviewedSources {
		artifacts = append(artifacts, work.ExecutionArtifactBinding{Path: source.Path, SHA256: source.SHA256})
	}
	for _, node := range projection.Manifest.Nodes {
		artifacts = append(artifacts, work.ExecutionArtifactBinding{Path: node.ReadinessContract.Path, SHA256: node.ReadinessContract.SHA256})
	}
	sort.Slice(artifacts, func(i, j int) bool {
		if artifacts[i].Path == artifacts[j].Path {
			return artifacts[i].SHA256 < artifacts[j].SHA256
		}
		return artifacts[i].Path < artifacts[j].Path
	})
	return artifacts
}

func executionDomainDigest(domain string, value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(encoded)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func (projection *ExecutionPlanProjection) fail(code, message string) {
	projection.Diagnostics = append(projection.Diagnostics, GoalPreflightDiagnostic{Code: code, Message: message})
}

// AuthorizeExecutionFile rechecks the current preview under the storage
// transaction lock and publishes one complete revision-one aggregate.
func AuthorizeExecutionFile(ctx context.Context, root, requestPath, approvalToken, approver string) (work.GoalExecution, error) {
	return authorizeExecutionAt(ctx, root, requestPath, approvalToken, approver, time.Now().UTC())
}

func authorizeExecutionAt(ctx context.Context, root, requestPath, approvalToken, approver string, now time.Time) (work.GoalExecution, error) {
	if strings.TrimSpace(approvalToken) == "" || strings.TrimSpace(approver) == "" || approver != strings.TrimSpace(approver) {
		return work.GoalExecution{}, errors.New("approval token and self-declared approver are required")
	}
	body, err := readContainedRegularFileLimit(root, requestPath, maxExecutionRequestBytes)
	if err != nil {
		return work.GoalExecution{}, fmt.Errorf("read execution request: %w", err)
	}
	var committed work.GoalExecution
	err = storage.Update(root, func(state *work.State) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Initial authorization changes the Runner's direct-entry mode from the
		// pinned legacy path to charged admission. Serialize that transition with
		// the Runner's workspace lock so a legacy run cannot observe "no
		// authorization" and then launch after one is adopted.
		if storage.RunnerRunning(root) {
			return errors.New("execution authorization cannot be adopted while a Runner owns the workspace")
		}
		projection, err := planExecutionBytes(ctx, root, body, now)
		if err != nil {
			return err
		}
		if len(projection.Diagnostics) != 0 {
			first := projection.Diagnostics[0]
			return fmt.Errorf("execution plan is no longer authorizable (%s): %s", first.Code, first.Message)
		}
		if projection.ApprovalToken != approvalToken {
			return errors.New("approval token is stale or does not match the exact request and current Goal state; run execution plan again")
		}
		request, err := parseExecutionPlanRequest(body)
		if err != nil {
			return err
		}
		binding, err := executionPlanBinding(*projection.GoalPlan, request.GoalPlanRequest, projection.RequestSHA256)
		if err != nil {
			return err
		}
		binding.AdoptedAt = now.UTC()
		execution := work.GoalExecution{
			GoalID: request.GoalPlanRequest.GoalID, Workspace: projection.Workspace,
			PlanBindings: []work.GoalPlanBinding{binding},
			Authorizations: []work.ExecutionAuthorization{{
				Revision: 1, RequestSHA256: projection.RequestSHA256, ApprovalToken: projection.ApprovalToken,
				GoalID: request.GoalPlanRequest.GoalID, Workspace: projection.Workspace,
				Approver: approver, AuthorizedAt: now.UTC(),
				ExpiresAt: mustExecutionExpiry(request.ExpiresAt), Caps: projection.Caps, WorkerProfile: projection.WorkerProfile,
			}},
			Ledger: work.ExecutionLedger{
				Revision: 1, AuthorizationRevision: 1,
				NodeAttempts: make([]work.ExecutionNodeAttempts, 0, len(binding.Nodes)),
			},
		}
		for _, node := range binding.Nodes {
			execution.Ledger.NodeAttempts = append(execution.Ledger.NodeAttempts, work.ExecutionNodeAttempts{PlanNodeRef: node.PlanNodeRef})
		}
		if err := state.AdoptInitialExecution(execution); err != nil {
			return err
		}
		goal, ok := state.GoalByID(execution.GoalID)
		if !ok || goal.Execution == nil {
			return errors.New("execution authorization was not adopted")
		}
		committed = *goal.Execution
		return nil
	})
	if err != nil {
		return work.GoalExecution{}, err
	}
	return committed, nil
}

func mustExecutionExpiry(value string) time.Time {
	expiresAt, _ := time.Parse(time.RFC3339Nano, value)
	return expiresAt.UTC()
}

// PlanExecutionRevisionFile previews an additive revision. Existing nodes
// retain their Work Item IDs; each new node is explicitly mapped with an empty
// workItemId and receives its durable ID only in the authorized transaction.
func PlanExecutionRevisionFile(ctx context.Context, root, requestPath string) (ExecutionPlanProjection, error) {
	body, err := readContainedRegularFileLimit(root, requestPath, maxExecutionRequestBytes)
	if err != nil {
		return ExecutionPlanProjection{}, fmt.Errorf("read execution revision request: %w", err)
	}
	state, err := storage.Load(root)
	if err != nil {
		return ExecutionPlanProjection{}, err
	}
	return planExecutionRevisionForState(ctx, root, body, state, time.Now().UTC())
}

func planExecutionRevisionForState(ctx context.Context, root string, body []byte, state work.State, now time.Time) (ExecutionPlanProjection, error) {
	projection := newExecutionPlanProjection()
	request, err := parseExecutionPlanRequest(body)
	if err != nil {
		projection.fail("invalid-request", err.Error())
		return projection, nil
	}
	if request.FormatVersion != executionPlanRequestVersion {
		projection.fail("invalid-request", "formatVersion must be forgepilot.execution-plan-request/v1")
		return projection, nil
	}
	goal, ok := state.GoalByID(request.GoalPlanRequest.GoalID)
	if !ok || goal.Execution == nil || len(goal.Execution.Authorizations) == 0 || len(goal.Execution.PlanBindings) == 0 {
		projection.fail("no-current-authorization", "Goal has no execution authorization to revise")
		return projection, nil
	}
	currentAuthorization := goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1]
	if request.ExpectedAuthorizationDigest != currentAuthorization.Digest {
		projection.fail("stale-revision", "expectedAuthorizationDigest must name the current execution authorization")
		return projection, nil
	}
	caps, err := executionCaps(request.Caps)
	if err != nil {
		projection.fail("invalid-limits", err.Error())
		return projection, nil
	}
	profile, err := resolveExecutionWorkerProfile(request.WorkerProfile)
	if err != nil {
		projection.fail("invalid-profile", err.Error())
		return projection, nil
	}
	expiresAt, err := parseExecutionExpiry(request.ExpiresAt, now)
	if err != nil {
		projection.fail("invalid-expiry", err.Error())
		return projection, nil
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return projection, err
	}
	projection.Workspace, projection.GoalID = canonicalRoot, request.GoalPlanRequest.GoalID
	projection.RequestSHA256 = "sha256:" + digest(body)
	projection.CurrentAuthorizationDigest = currentAuthorization.Digest
	projection.ExpiresAt, projection.WorkerProfile, projection.Caps = expiresAt.Format(time.RFC3339Nano), profile, caps
	goalPlan, err := preflightGoalPlanForState(ctx, canonicalRoot, request.GoalPlanRequest, state, func(manifest GoalPlanManifest) error {
		return validateRevisionMapping(goal.Execution.PlanBindings[len(goal.Execution.PlanBindings)-1], manifest, request.GoalPlanRequest.NodeMappings)
	})
	projection.GoalPlan = &goalPlan
	if err != nil || len(goalPlan.Diagnostics) != 0 {
		projection.Diagnostics = append(projection.Diagnostics, goalPlan.Diagnostics...)
		return projection, err
	}
	binding, err := executionPlanBinding(goalPlan, request.GoalPlanRequest, projection.RequestSHA256)
	if err != nil {
		projection.fail("invalid-plan", err.Error())
		return projection, nil
	}
	projection.RegistrationSHA256, err = executionRegistrationDigest(state, goal.ID)
	if err != nil {
		return projection, err
	}
	projection.Artifacts = executionArtifactDigests(goalPlan, request.GoalPlanRequest)
	tokenInput := executionApprovalInput{RequestSHA256: projection.RequestSHA256, Workspace: canonicalRoot, GoalID: goal.ID,
		RegistrationSHA256: projection.RegistrationSHA256, AuthorizationPresent: true, ExpiresAt: projection.ExpiresAt,
		Artifacts: projection.Artifacts, WorkerProfile: profile, Caps: caps}
	projection.ApprovalToken, err = executionDomainDigest("forgepilot.execution-revision-approval/v1", struct {
		Input                      executionApprovalInput
		CurrentAuthorizationDigest string
		CurrentLedgerDigest        string
	}{tokenInput, currentAuthorization.Digest, goal.Execution.Ledger.Digest})
	_ = binding
	return projection, err
}

func validateRevisionMapping(current work.GoalPlanBinding, manifest GoalPlanManifest, mappings []GoalPlanNodeMapping) error {
	if len(mappings) != len(manifest.Nodes) || len(manifest.Nodes) < len(current.Nodes) {
		return errors.New("revised plan must retain every current Plan Node and explicitly map every node")
	}
	currentByRef := make(map[string]work.ExecutionPlanNodeBinding, len(current.Nodes))
	for _, node := range current.Nodes {
		currentByRef[node.PlanNodeRef] = node
	}
	manifestByRef := make(map[string]GoalPlanNode, len(manifest.Nodes))
	for _, node := range manifest.Nodes {
		manifestByRef[node.NodeRef] = node
	}
	seen := make(map[string]bool, len(mappings))
	for _, mapping := range mappings {
		manifestNode, exists := manifestByRef[mapping.PlanNodeRef]
		if !exists || seen[mapping.PlanNodeRef] {
			return errors.New("revision node mappings must uniquely name declared plan nodes")
		}
		seen[mapping.PlanNodeRef] = true
		if prior, exists := currentByRef[mapping.PlanNodeRef]; exists {
			if mapping.WorkItemID != prior.WorkItemID || manifestNode.StoryRef != prior.StoryRef || !sameStringSlice(manifestNode.DependsOn, prior.DependsOn) {
				return fmt.Errorf("revision changes existing Plan Node %q; create a new Goal instead", prior.PlanNodeRef)
			}
		} else if mapping.WorkItemID != "" {
			return fmt.Errorf("new Plan Node %q must use an empty workItemId", mapping.PlanNodeRef)
		}
	}
	return nil
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ReviseExecutionFile atomically creates explicit additive Work Items and
// appends the reviewed binding/authorization. It obtains the Runner lock
// before the state lock so a Runner cannot start between the revision check and
// publication.
func ReviseExecutionFile(ctx context.Context, root, requestPath, approvalToken, approver string) (work.GoalExecution, error) {
	if strings.TrimSpace(approvalToken) == "" || strings.TrimSpace(approver) == "" || approver != strings.TrimSpace(approver) {
		return work.GoalExecution{}, errors.New("approval token and self-declared approver are required")
	}
	body, err := readContainedRegularFileLimit(root, requestPath, maxExecutionRequestBytes)
	if err != nil {
		return work.GoalExecution{}, fmt.Errorf("read execution revision request: %w", err)
	}
	var committed work.GoalExecution
	err = storage.WithWorkspaceLock(root, func() error {
		return storage.Update(root, func(state *work.State) error {
			projection, err := planExecutionRevisionForState(ctx, root, body, *state, time.Now().UTC())
			if err != nil {
				return err
			}
			if len(projection.Diagnostics) != 0 {
				return fmt.Errorf("execution revision is no longer authorizable (%s): %s", projection.Diagnostics[0].Code, projection.Diagnostics[0].Message)
			}
			if projection.ApprovalToken != approvalToken {
				return errors.New("approval token is stale or does not match the exact request and current Goal state; run execution revise plan again")
			}
			request, err := parseExecutionPlanRequest(body)
			if err != nil {
				return err
			}
			binding, err := executionPlanBinding(*projection.GoalPlan, request.GoalPlanRequest, projection.RequestSHA256)
			if err != nil {
				return err
			}
			goal, _ := state.GoalByID(request.GoalPlanRequest.GoalID)
			current := goal.Execution.PlanBindings[len(goal.Execution.PlanBindings)-1]
			byRef := make(map[string]string, len(binding.Nodes))
			for _, node := range binding.Nodes {
				byRef[node.PlanNodeRef] = node.WorkItemID
			}
			pending := make(map[int]bool, len(binding.Nodes))
			for i := range binding.Nodes {
				if binding.Nodes[i].WorkItemID == "" {
					pending[i] = true
				}
			}
			for len(pending) > 0 {
				progressed := false
				for i := range binding.Nodes {
					if !pending[i] {
						continue
					}
					dependencies := make([]string, 0, len(binding.Nodes[i].DependsOn))
					ready := true
					for _, ref := range binding.Nodes[i].DependsOn {
						id := byRef[ref]
						if id == "" {
							ready = false
							break
						}
						dependencies = append(dependencies, id)
					}
					if !ready {
						continue
					}
					item, err := state.AddWork(request.GoalPlanRequest.GoalID, binding.Nodes[i].StoryRef, dependencies, time.Now().UTC())
					if err != nil {
						return err
					}
					binding.Nodes[i].WorkItemID, byRef[binding.Nodes[i].PlanNodeRef] = item.ID, item.ID
					delete(pending, i)
					progressed = true
				}
				if !progressed {
					return errors.New("new Plan Nodes have unresolved dependencies")
				}
			}
			binding.Revision, binding.AdoptedAt = current.Revision+1, time.Now().UTC()
			authorization := goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1]
			authorization.Revision, authorization.RequestSHA256, authorization.ApprovalToken = binding.Revision, binding.RequestSHA256, approvalToken
			authorization.AuthorizedAt, authorization.ExpiresAt, authorization.Caps, authorization.WorkerProfile = binding.AdoptedAt, mustExecutionExpiry(request.ExpiresAt), projection.Caps, projection.WorkerProfile
			authorization.Approver = approver
			authorization.WorkerIdentity, authorization.EngineGeneration = nil, nil
			if err := state.ReviseExecution(goal.ID, binding, authorization); err != nil {
				return err
			}
			committed = *goal.Execution
			return nil
		})
	})
	return committed, err
}
