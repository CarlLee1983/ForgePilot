package app

// This file deliberately keeps Goal Plan preflight a read-only boundary.  It
// validates supplied bytes and registered Work Items, but does not infer any
// requirement coverage or ask the repository/runtime for current facts.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

const (
	goalPreflightRequestVersion = "forgepilot.goal-preflight-request/v1"
	GoalPreflightVersion        = "forgepilot.goal-preflight/v1"
	maxGoalPlanArtifactBytes    = 8 << 20
	maxGoalPlanJSONDepth        = 128
	maxGoalPlanDeclarationBytes = 1 << 20
	maxGoalPlanDeclarationDepth = 32
	maxGoalPlanEdges            = 10_000
)

type GoalPlanNodeMapping struct {
	PlanNodeRef string `json:"planNodeRef"`
	WorkItemID  string `json:"workItemId"`
}
type GoalPreflightRequest struct {
	FormatVersion      string                `json:"formatVersion"`
	GoalID             string                `json:"goalId"`
	ManifestPath       string                `json:"manifestPath"`
	CoverageReviewPath string                `json:"coverageReviewPath"`
	NodeMappings       []GoalPlanNodeMapping `json:"nodeMappings"`
}

type GoalPreflightDiagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type GoalPreflightFact struct {
	Status string `json:"status"`
	Value  any    `json:"value,omitempty"`
}
type GoalPlanSourceBinding struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type GoalPlanArtifactBinding struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type GoalPlanNode struct {
	NodeRef           string                  `json:"nodeRef"`
	StoryRef          string                  `json:"storyRef"`
	ReadinessContract GoalPlanArtifactBinding `json:"readinessContract"`
	DependsOn         []string                `json:"dependsOn"`
}
type GoalPlanEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type GoalPlanIdentity struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}
type GoalPlanCoverageIndex struct {
	BatchID     string `json:"batchId"`
	Fingerprint string `json:"fingerprint"`
}
type GoalPlanManifest struct {
	SchemaVersion   string                  `json:"schemaVersion"`
	Plan            GoalPlanIdentity        `json:"plan"`
	Declaration     GoalPlanArtifactBinding `json:"declaration"`
	Nodes           []GoalPlanNode          `json:"nodes"`
	ReviewedSources []GoalPlanSourceBinding `json:"reviewedSources"`
	CoverageIndex   GoalPlanCoverageIndex   `json:"coverageIndex"`
	Digest          string                  `json:"-"`
}
type goalPlanDeclaration struct {
	SchemaVersion string
	Plan          GoalPlanIdentity
	Nodes         []goalPlanDeclarationNode
}
type goalPlanDeclarationNode struct {
	NodeRef   string
	StoryRef  string
	DependsOn []string
}
type GoalPlanReviewer struct {
	Name      string `json:"name"`
	Assurance string `json:"assurance"`
}
type GoalPlanCoverageReview struct {
	SchemaVersion   string                  `json:"schemaVersion"`
	ReviewID        string                  `json:"reviewId"`
	ManifestSHA256  string                  `json:"manifestSha256"`
	ReviewedSources []GoalPlanSourceBinding `json:"reviewedSources"`
	CoverageIndex   GoalPlanCoverageIndex   `json:"coverageIndex"`
	Conclusion      string                  `json:"conclusion"`
	Reviewer        GoalPlanReviewer        `json:"reviewer"`
	ReviewedAt      string                  `json:"reviewedAt"`
}
type GoalPreflightGoal struct {
	ID               string                `json:"id"`
	Status           work.GoalStatus       `json:"status"`
	ReviewPolicy     work.ReviewPolicy     `json:"reviewPolicy"`
	CompletionPolicy work.CompletionPolicy `json:"completionPolicy"`
}
type GoalPreflightProjection struct {
	Version        string                       `json:"version"`
	Goal           GoalPreflightGoal            `json:"goal"`
	Manifest       *GoalPlanManifest            `json:"manifest,omitempty"`
	CoverageReview *GoalPlanCoverageReview      `json:"coverageReview,omitempty"`
	SourceBindings []GoalPlanSourceBinding      `json:"sourceBindings,omitempty"`
	NodeMappings   []GoalPlanNodeMapping        `json:"nodeMappings,omitempty"`
	Diagnostics    []GoalPreflightDiagnostic    `json:"diagnostics"`
	Facts          map[string]GoalPreflightFact `json:"facts"`
}

var (
	lowerSHA256    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	planIdentity   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	batchIdentity  = regexp.MustCompile(`^[A-Z][A-Z0-9]*(-[A-Z0-9]+)*-[0-9]+(-[a-z0-9]+(-[a-z0-9]+)*)?$`)
	reviewIdentity = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	reviewedAtUTC  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)
)

type verifiedGoalPlanBindings struct {
	Declaration       GoalPlanArtifactBinding
	Readiness         []GoalPlanArtifactBinding
	DeclarationStatus string
	SourcesStatus     string
	ReadinessStatus   string
}

func newGoalPreflightProjection() GoalPreflightProjection {
	return GoalPreflightProjection{Version: GoalPreflightVersion, Facts: map[string]GoalPreflightFact{
		"goal": {Status: "unprobed"}, "manifest": {Status: "unprobed"}, "coverageReview": {Status: "unprobed"},
		"declaration": {Status: "unprobed"}, "sources": {Status: "unprobed"}, "readinessContracts": {Status: "unprobed"}, "registration": {Status: "unprobed"},
		"candidateFreshness": {Status: "unprobed"}, "workerState": {Status: "unprobed"}, "runtimeState": {Status: "unavailable"}, "nextAction": {Status: "unprobed"},
	}}
}

// PreflightGoalPlanFile reads a contained JSON request and returns its
// diagnostic as a projection when the request itself is malformed.
func PreflightGoalPlanFile(ctx context.Context, root, requestPath string) (GoalPreflightProjection, error) {
	p := newGoalPreflightProjection()
	if err := ctx.Err(); err != nil {
		return p, err
	}
	body, err := readContainedRegularFile(root, requestPath)
	if err != nil {
		p.fail("invalid-request", fmt.Sprintf("request: %v", err))
		return p, nil
	}
	request, err := parseGoalPreflightRequest(body)
	if err != nil {
		p.fail("invalid-request", err.Error())
		return p, nil
	}
	return PreflightGoalPlan(ctx, root, request)
}

func parseGoalPreflightRequest(body []byte) (GoalPreflightRequest, error) {
	var request GoalPreflightRequest
	object, err := artifactObject(body)
	if err != nil {
		return request, err
	}
	if err := allowed(object, "formatVersion", "goalId", "manifestPath", "coverageReviewPath", "nodeMappings"); err != nil {
		return request, err
	}
	for _, field := range []struct {
		name string
		into any
	}{
		{"formatVersion", &request.FormatVersion},
		{"goalId", &request.GoalID},
		{"manifestPath", &request.ManifestPath},
		{"coverageReviewPath", &request.CoverageReviewPath},
		{"nodeMappings", &request.NodeMappings},
	} {
		if err := decodeRequired(object, field.name, field.into); err != nil {
			return request, fmt.Errorf("request field %s is required or invalid", field.name)
		}
	}
	return request, nil
}

// PreflightGoalPlan is intentionally a pure inspection: it reads only the
// supplied artifact/source files and existing ForgePilot state. Validation
// failures are returned as diagnostics (and a projection), not mutations.
func PreflightGoalPlan(ctx context.Context, root string, request GoalPreflightRequest) (GoalPreflightProjection, error) {
	p := newGoalPreflightProjection()
	if err := ctx.Err(); err != nil {
		return p, err
	}
	if request.FormatVersion != goalPreflightRequestVersion {
		p.fail("invalid-request", "formatVersion must be forgepilot.goal-preflight-request/v1")
		return p, nil
	}
	if request.GoalID == "" || !validArtifactPath(request.ManifestPath) || !validArtifactPath(request.CoverageReviewPath) {
		p.fail("invalid-request", "goalId, manifestPath, and coverageReviewPath are required")
		return p, nil
	}
	state, err := storage.Load(root)
	if err != nil {
		setPreflightFact(&p, "goal", "unavailable", nil)
		p.fail("state-unavailable", err.Error())
		return p, err
	}
	goal, ok := state.GoalByID(request.GoalID)
	if !ok {
		setPreflightFact(&p, "goal", "unavailable", nil)
		p.fail("unknown-goal", fmt.Sprintf("unknown goal %q", request.GoalID))
		return p, nil
	}
	p.Goal = GoalPreflightGoal{ID: goal.ID, Status: goal.Status, ReviewPolicy: goal.ReviewPolicy, CompletionPolicy: goal.CompletionPolicy}
	setPreflightFact(&p, "goal", "observed", p.Goal)

	manifestBytes, err := readContainedRegularFile(root, request.ManifestPath)
	if err != nil {
		setPreflightFact(&p, "manifest", "unavailable", nil)
		p.fail("manifest-unavailable", err.Error())
		return p, nil
	}
	manifest, err := parseManifest(manifestBytes)
	if err != nil {
		setPreflightFact(&p, "manifest", "unavailable", nil)
		p.fail(artifactCategory(err), err.Error())
		return p, nil
	}
	p.Manifest = &manifest
	setPreflightFact(&p, "manifest", "observed", manifest.Digest)
	bindings, err := verifyManifestBindings(root, manifest)
	setPreflightFact(&p, "declaration", bindings.DeclarationStatus, factValue(bindings.DeclarationStatus, bindings.Declaration))
	setPreflightFact(&p, "sources", bindings.SourcesStatus, factValue(bindings.SourcesStatus, manifest.ReviewedSources))
	setPreflightFact(&p, "readinessContracts", bindings.ReadinessStatus, factValue(bindings.ReadinessStatus, bindings.Readiness))
	if err != nil {
		p.fail(artifactCategory(err), err.Error())
		return p, nil
	}
	p.SourceBindings = manifest.ReviewedSources
	reviewBytes, err := readContainedRegularFile(root, request.CoverageReviewPath)
	if err != nil {
		setPreflightFact(&p, "coverageReview", "unavailable", nil)
		p.fail("coverage-review-unavailable", err.Error())
		return p, nil
	}
	review, err := parseCoverageReview(reviewBytes, manifest)
	if err != nil {
		setPreflightFact(&p, "coverageReview", "unavailable", nil)
		p.fail(artifactCategory(err), err.Error())
		return p, nil
	}
	p.CoverageReview = &review
	setPreflightFact(&p, "coverageReview", "observed", review.ReviewID)
	if err := validateGoalPlanMapping(state.WorkItems, request.GoalID, manifest, request.NodeMappings); err != nil {
		setPreflightFact(&p, "registration", "unavailable", nil)
		p.fail("mapping-mismatch", err.Error())
		return p, nil
	}
	p.NodeMappings = append([]GoalPlanNodeMapping(nil), request.NodeMappings...)
	setPreflightFact(&p, "registration", "observed", "exact")
	return p, nil
}

func setPreflightFact(p *GoalPreflightProjection, name, status string, value any) {
	p.Facts[name] = GoalPreflightFact{Status: status, Value: value}
}

func factValue(status string, value any) any {
	if status != "observed" {
		return nil
	}
	return value
}

func (p *GoalPreflightProjection) fail(code, message string) {
	p.Diagnostics = append(p.Diagnostics, GoalPreflightDiagnostic{Code: code, Message: message})
}

func readContainedRegularFile(root, path string) ([]byte, error) {
	return readContainedRegularFileLimit(root, path, maxGoalPlanArtifactBytes)
}

func readContainedRegularFileLimit(root, path string, maxBytes int) ([]byte, error) {
	if !validArtifactPath(path) {
		return nil, fmt.Errorf("path %q must be a contained repository-relative path", path)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	resolvedPath := resolvedRoot
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		resolvedPath = filepath.Join(resolvedPath, segment)
		info, err := os.Lstat(resolvedPath)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("path %q contains a symlink", path)
		}
		if i < len(segments)-1 && !info.IsDir() {
			return nil, fmt.Errorf("path %q has a non-directory parent", path)
		}
	}
	info, err := os.Lstat(resolvedPath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("path %q is not a regular file", path)
	}
	if info.Size() > int64(maxBytes) {
		return nil, reject("malformed-artifact", "path %q exceeds %d byte limit", path, maxBytes)
	}
	return os.ReadFile(resolvedPath)
}

func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

type artifactError struct{ category, message string }

func (e artifactError) Error() string { return e.message }
func reject(category, format string, args ...any) error {
	return artifactError{category: category, message: fmt.Sprintf(format, args...)}
}
func artifactCategory(err error) string {
	var e artifactError
	if errors.As(err, &e) {
		return e.category
	}
	return "malformed-artifact"
}

func parseManifest(b []byte) (GoalPlanManifest, error) {
	var out GoalPlanManifest
	root, err := artifactObject(b)
	if err != nil {
		return out, err
	}
	if err := allowed(root, "schemaVersion", "plan", "declaration", "nodes", "reviewedSources", "coverageIndex"); err != nil {
		return out, err
	}
	if err := decodeRequired(root, "schemaVersion", &out.SchemaVersion); err != nil || out.SchemaVersion != "1.0.0" {
		return out, reject("unsupported-schema", "manifest schemaVersion must be 1.0.0")
	}
	if err := decodeRequired(root, "plan", &out.Plan); err != nil || !planIdentity.MatchString(out.Plan.ID) || out.Plan.Revision < 1 || out.Plan.Revision > 9007199254740991 {
		return out, reject("malformed-artifact", "manifest plan identity and positive safe revision are required")
	}
	if err := decodeRequired(root, "declaration", &out.Declaration); err != nil || !validArtifactPath(out.Declaration.Path) || !lowerSHA256.MatchString(out.Declaration.SHA256) {
		return out, reject("malformed-artifact", "manifest declaration path and SHA-256 are required")
	}
	if err := decodeRequired(root, "reviewedSources", &out.ReviewedSources); err != nil || out.ReviewedSources == nil || len(out.ReviewedSources) > 4000 {
		return out, reject("malformed-artifact", "manifest reviewedSources is required")
	}
	if err := validateSourceBindings(out.ReviewedSources); err != nil {
		return out, err
	}
	if err := decodeRequired(root, "coverageIndex", &out.CoverageIndex); err != nil || !validCoverageIndex(out.CoverageIndex) {
		return out, reject("malformed-artifact", "manifest coverageIndex identity is invalid")
	}
	if err := decodeRequired(root, "nodes", &out.Nodes); err != nil || len(out.Nodes) == 0 || len(out.Nodes) > 1000 {
		return out, reject("malformed-artifact", "manifest nodes is required")
	}
	edges := make([]GoalPlanEdge, 0)
	for i := range out.Nodes {
		node := &out.Nodes[i]
		if !planIdentity.MatchString(node.NodeRef) || !validArtifactPath(node.StoryRef) || node.ReadinessContract.Path != node.StoryRef+"/readiness.json" || !lowerSHA256.MatchString(node.ReadinessContract.SHA256) || node.DependsOn == nil || len(node.DependsOn) > 1000 {
			return out, reject("malformed-artifact", "manifest node %d is invalid", i)
		}
		if i > 0 && out.Nodes[i-1].NodeRef >= node.NodeRef {
			return out, reject("invalid-topology", "manifest nodes must be sorted by UTF-8 node reference")
		}
		for j, dependency := range node.DependsOn {
			if len(edges) == maxGoalPlanEdges {
				return out, reject("invalid-topology", "manifest exceeds %d dependency edges", maxGoalPlanEdges)
			}
			if !planIdentity.MatchString(dependency) || (j > 0 && node.DependsOn[j-1] >= dependency) {
				return out, reject("invalid-topology", "node %q dependencies must be unique plan references in UTF-8 order", node.NodeRef)
			}
			edges = append(edges, GoalPlanEdge{From: dependency, To: node.NodeRef})
		}
	}
	if err := validateTopology(out.Nodes, edges); err != nil {
		return out, err
	}
	out.Digest = digest(b)
	return out, nil
}

func validateSourceBindings(bindings []GoalPlanSourceBinding) error {
	if len(bindings) > 4000 {
		return reject("malformed-artifact", "reviewedSources exceeds 4000 entries")
	}
	seen := map[string]bool{}
	previous := ""
	for i, binding := range bindings {
		if !validArtifactPath(binding.Path) || !lowerSHA256.MatchString(binding.SHA256) || seen[binding.Path] {
			return reject("malformed-artifact", "reviewed source binding is invalid")
		}
		if i > 0 && previous >= binding.Path {
			return reject("malformed-artifact", "reviewed sources must be unique and sorted by UTF-8 path bytes")
		}
		seen[binding.Path] = true
		previous = binding.Path
	}
	return nil
}

func validCoverageIndex(index GoalPlanCoverageIndex) bool {
	return len(index.BatchID) <= 128 && batchIdentity.MatchString(index.BatchID) && lowerSHA256.MatchString(index.Fingerprint)
}

func verifyManifestBindings(root string, manifest GoalPlanManifest) (verified verifiedGoalPlanBindings, err error) {
	verified = verifiedGoalPlanBindings{
		Declaration:       manifest.Declaration,
		DeclarationStatus: "unprobed",
		SourcesStatus:     "unprobed",
		ReadinessStatus:   "unprobed",
	}
	observed := map[string]string{}
	bodies := map[string][]byte{}
	readBinding := func(binding GoalPlanArtifactBinding, maxBytes int) ([]byte, error) {
		if prior, exists := observed[binding.Path]; exists {
			if prior != binding.SHA256 {
				return nil, reject("digest-mismatch", "artifact path %q has conflicting declared digests", binding.Path)
			}
			return bodies[binding.Path], nil
		}
		body, err := readContainedRegularFileLimit(root, binding.Path, maxBytes)
		if err != nil {
			var artifactErr artifactError
			if errors.As(err, &artifactErr) {
				return nil, err
			}
			return nil, reject("source-unavailable", "bound artifact %q: %v", binding.Path, err)
		}
		if digest(body) != binding.SHA256 {
			return nil, reject("digest-mismatch", "bound artifact %q digest does not match its current bytes", binding.Path)
		}
		observed[binding.Path] = binding.SHA256
		bodies[binding.Path] = body
		return body, nil
	}
	declarationBytes, err := readBinding(manifest.Declaration, maxGoalPlanDeclarationBytes)
	if err != nil {
		verified.DeclarationStatus = "unavailable"
		return verified, err
	}
	declaration, err := parseGoalPlanDeclaration(declarationBytes)
	if err != nil {
		verified.DeclarationStatus = "unavailable"
		return verified, err
	}
	if err := matchDeclarationToManifest(declaration, manifest); err != nil {
		verified.DeclarationStatus = "unavailable"
		return verified, err
	}
	verified.DeclarationStatus = "observed"
	for _, source := range manifest.ReviewedSources {
		if _, err := readBinding(GoalPlanArtifactBinding{Path: source.Path, SHA256: source.SHA256}, maxGoalPlanArtifactBytes); err != nil {
			verified.SourcesStatus = "unavailable"
			return verified, err
		}
	}
	verified.SourcesStatus = "observed"
	for _, node := range manifest.Nodes {
		if _, err := readBinding(node.ReadinessContract, maxGoalPlanArtifactBytes); err != nil {
			verified.ReadinessStatus = "unavailable"
			return verified, err
		}
		verified.Readiness = append(verified.Readiness, node.ReadinessContract)
	}
	verified.ReadinessStatus = "observed"
	return verified, nil
}

func parseGoalPlanDeclaration(b []byte) (goalPlanDeclaration, error) {
	var out goalPlanDeclaration
	root, err := artifactObjectWithLimits(b, maxGoalPlanDeclarationBytes, maxGoalPlanDeclarationDepth)
	if err != nil {
		return out, err
	}
	if err := allowed(root, "schemaVersion", "plan", "nodes"); err != nil {
		return out, err
	}
	if err := decodeRequired(root, "schemaVersion", &out.SchemaVersion); err != nil || out.SchemaVersion != "1.0.0" {
		return out, reject("unsupported-schema", "declaration schemaVersion must be 1.0.0")
	}
	if err := decodeRequired(root, "plan", &out.Plan); err != nil || !planIdentity.MatchString(out.Plan.ID) || out.Plan.Revision < 1 || out.Plan.Revision > 9007199254740991 {
		return out, reject("malformed-artifact", "declaration plan identity and positive safe revision are required")
	}
	var rawNodes []json.RawMessage
	if err := decodeRequired(root, "nodes", &rawNodes); err != nil || rawNodes == nil || len(rawNodes) == 0 || len(rawNodes) > 1000 {
		return out, reject("malformed-artifact", "declaration nodes must contain 1 to 1000 entries")
	}
	out.Nodes = make([]goalPlanDeclarationNode, 0, len(rawNodes))
	manifestNodes := make([]GoalPlanNode, 0, len(rawNodes))
	edges := make([]GoalPlanEdge, 0)
	for i, rawNode := range rawNodes {
		nodeObject, err := artifactObjectWithLimits(rawNode, maxGoalPlanDeclarationBytes, maxGoalPlanDeclarationDepth)
		if err != nil {
			return out, err
		}
		if err := allowed(nodeObject, "nodeRef", "storyRef", "dependsOn"); err != nil {
			return out, err
		}
		var node goalPlanDeclarationNode
		if err := decodeRequired(nodeObject, "nodeRef", &node.NodeRef); err != nil || !planIdentity.MatchString(node.NodeRef) {
			return out, reject("malformed-artifact", "declaration node %d has an invalid nodeRef", i)
		}
		if err := decodeRequired(nodeObject, "storyRef", &node.StoryRef); err != nil || !validArtifactPath(node.StoryRef) {
			return out, reject("malformed-artifact", "declaration node %d has an invalid storyRef", i)
		}
		if err := decodeRequired(nodeObject, "dependsOn", &node.DependsOn); err != nil || node.DependsOn == nil || len(node.DependsOn) > 1000 {
			return out, reject("malformed-artifact", "declaration node %d dependsOn must be an array of at most 1000 references", i)
		}
		seenDependencies := map[string]bool{}
		for _, dependency := range node.DependsOn {
			if !planIdentity.MatchString(dependency) || dependency == node.NodeRef || seenDependencies[dependency] {
				return out, reject("invalid-topology", "declaration node %q has an invalid or duplicate dependency", node.NodeRef)
			}
			seenDependencies[dependency] = true
			edges = append(edges, GoalPlanEdge{From: dependency, To: node.NodeRef})
			if len(edges) > maxGoalPlanEdges {
				return out, reject("invalid-topology", "declaration exceeds %d dependency edges", maxGoalPlanEdges)
			}
		}
		out.Nodes = append(out.Nodes, node)
		manifestNodes = append(manifestNodes, GoalPlanNode{NodeRef: node.NodeRef})
	}
	if err := validateTopology(manifestNodes, edges); err != nil {
		return out, err
	}
	return out, nil
}

func matchDeclarationToManifest(declaration goalPlanDeclaration, manifest GoalPlanManifest) error {
	if declaration.Plan != manifest.Plan || len(declaration.Nodes) != len(manifest.Nodes) {
		return reject("invalid-topology", "declaration identity or node set does not match manifest")
	}
	declared := make(map[string]goalPlanDeclarationNode, len(declaration.Nodes))
	for _, node := range declaration.Nodes {
		declared[node.NodeRef] = node
	}
	for _, node := range manifest.Nodes {
		declaredNode, exists := declared[node.NodeRef]
		if !exists || declaredNode.StoryRef != node.StoryRef || !sameStringSet(declaredNode.DependsOn, node.DependsOn) {
			return reject("invalid-topology", "declaration node %q does not match manifest topology", node.NodeRef)
		}
	}
	return nil
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	values := make(map[string]bool, len(a))
	for _, value := range a {
		values[value] = true
	}
	for _, value := range b {
		if !values[value] {
			return false
		}
	}
	return true
}

func parseCoverageReview(b []byte, manifest GoalPlanManifest) (GoalPlanCoverageReview, error) {
	var out GoalPlanCoverageReview
	root, err := artifactObject(b)
	if err != nil {
		return out, err
	}
	if err := allowed(root, "schemaVersion", "reviewId", "manifestSha256", "reviewedSources", "coverageIndex", "conclusion", "reviewer", "reviewedAt"); err != nil {
		return out, err
	}
	if err := decodeRequired(root, "schemaVersion", &out.SchemaVersion); err != nil || out.SchemaVersion != "1.0.0" {
		return out, reject("unsupported-schema", "coverage review schemaVersion must be 1.0.0")
	}
	if err := decodeRequired(root, "reviewId", &out.ReviewID); err != nil || !reviewIdentity.MatchString(out.ReviewID) {
		return out, reject("approval-binding-mismatch", "coverage review reviewId must be a UUID")
	}
	if err := decodeRequired(root, "manifestSha256", &out.ManifestSHA256); err != nil || !lowerSHA256.MatchString(out.ManifestSHA256) {
		return out, reject("approval-binding-mismatch", "coverage review manifestSha256 is required")
	}
	if err := decodeRequired(root, "reviewedSources", &out.ReviewedSources); err != nil || out.ReviewedSources == nil || validateSourceBindings(out.ReviewedSources) != nil {
		return out, reject("approval-binding-mismatch", "coverage review reviewedSources is required")
	}
	if err := decodeRequired(root, "coverageIndex", &out.CoverageIndex); err != nil || !validCoverageIndex(out.CoverageIndex) {
		return out, reject("approval-binding-mismatch", "coverage review coverageIndex is invalid")
	}
	if err := decodeRequired(root, "conclusion", &out.Conclusion); err != nil || out.Conclusion != "approved" {
		return out, reject("approval-binding-mismatch", "coverage review conclusion must be approved")
	}
	if err := decodeRequired(root, "reviewer", &out.Reviewer); err != nil || out.Reviewer.Name == "" || len(out.Reviewer.Name) > 256 || out.Reviewer.Assurance != "self-asserted" || !validReviewerName(out.Reviewer.Name) {
		return out, reject("approval-binding-mismatch", "coverage review reviewer must be self-asserted")
	}
	if err := decodeRequired(root, "reviewedAt", &out.ReviewedAt); err != nil || !validUTC(out.ReviewedAt) {
		return out, reject("approval-binding-mismatch", "coverage review reviewedAt must be an ISO 8601 UTC timestamp")
	}
	if out.ManifestSHA256 != manifest.Digest {
		return out, reject("approval-binding-mismatch", "coverage review does not bind the supplied manifest bytes")
	}
	if out.CoverageIndex != manifest.CoverageIndex || !sameBindings(out.ReviewedSources, manifest.ReviewedSources) {
		return out, reject("approval-binding-mismatch", "coverage review approval binding is invalid")
	}
	return out, nil
}

func artifactObject(b []byte) (map[string]json.RawMessage, error) {
	return artifactObjectWithLimits(b, maxGoalPlanArtifactBytes, maxGoalPlanJSONDepth)
}

func artifactObjectWithLimits(b []byte, maxBytes, maxDepth int) (map[string]json.RawMessage, error) {
	if len(b) > maxBytes {
		return nil, reject("malformed-artifact", "artifact exceeds %d byte limit", maxBytes)
	}
	if !utf8.Valid(b) {
		return nil, reject("malformed-artifact", "artifact must be valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if err := checkJSONValue(decoder, 0, maxDepth); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, reject("malformed-artifact", "artifact contains extra JSON values")
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(b, &result); err != nil || result == nil {
		return nil, reject("malformed-artifact", "artifact must be a JSON object")
	}
	return result, nil
}
func checkJSONValue(d *json.Decoder, depth, maxDepth int) error {
	if depth > maxDepth {
		return reject("malformed-artifact", "artifact exceeds %d JSON nesting levels", maxDepth)
	}
	token, err := d.Token()
	if err != nil {
		return reject("malformed-artifact", "invalid JSON")
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delim != '{' && delim != '[' {
		return reject("malformed-artifact", "invalid JSON")
	}
	seen := map[string]bool{}
	for d.More() {
		if delim == '{' {
			key, err := d.Token()
			if err != nil {
				return reject("malformed-artifact", "invalid JSON")
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return reject("malformed-artifact", "duplicate JSON object key")
			}
			seen[name] = true
		}
		if err := checkJSONValue(d, depth+1, maxDepth); err != nil {
			return err
		}
	}
	if _, err := d.Token(); err != nil {
		return reject("malformed-artifact", "invalid JSON")
	}
	return nil
}
func allowed(object map[string]json.RawMessage, names ...string) error {
	allowed := map[string]bool{}
	for _, n := range names {
		allowed[n] = true
	}
	for name := range object {
		if !allowed[name] {
			return reject("malformed-artifact", "unknown field %q", name)
		}
	}
	return nil
}
func decodeRequired(object map[string]json.RawMessage, key string, into any) error {
	raw, ok := object[key]
	if !ok {
		return errors.New("missing")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("extra")
	}
	return nil
}
func sameBindings(a, b []GoalPlanSourceBinding) bool {
	if len(a) != len(b) {
		return false
	}
	right := map[string]string{}
	for _, source := range b {
		right[source.Path] = source.SHA256
	}
	for _, source := range a {
		if right[source.Path] != source.SHA256 {
			return false
		}
	}
	return true
}

func validArtifactPath(path string) bool {
	if !utf8.ValidString(path) || len(path) == 0 || len(path) > 1024 || filepath.IsAbs(path) || strings.Contains(path, "\\") || strings.HasPrefix(path, "//") {
		return false
	}
	if len(path) >= 2 && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) && path[1] == ':' {
		return false
	}
	for _, r := range path {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) || r == '\ufeff' {
			return false
		}
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func validReviewerName(name string) bool {
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) == 0 || utf8.RuneCountInString(name) > 256 || strings.TrimSpace(name) != name {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return false
		}
	}
	return true
}

func validUTC(value string) bool {
	if !reviewedAtUTC.MatchString(value) {
		return false
	}
	_, err := time.Parse("2006-01-02T15:04:05.000Z", value)
	return err == nil
}

func validateTopology(nodes []GoalPlanNode, edges []GoalPlanEdge) error {
	known := map[string]bool{}
	for _, node := range nodes {
		if known[node.NodeRef] {
			return reject("invalid-topology", "duplicate plan node reference %q", node.NodeRef)
		}
		known[node.NodeRef] = true
	}
	if len(nodes) == 0 {
		return reject("invalid-topology", "manifest must declare nodes")
	}
	graph := map[string][]string{}
	edgeSet := map[string]bool{}
	for _, edge := range edges {
		if edge.From == "" || edge.To == "" || edge.From == edge.To || !known[edge.From] || !known[edge.To] {
			return reject("invalid-topology", "invalid plan edge %q -> %q", edge.From, edge.To)
		}
		key := edge.From + "\x00" + edge.To
		if edgeSet[key] {
			return reject("invalid-topology", "duplicate plan edge %q -> %q", edge.From, edge.To)
		}
		edgeSet[key] = true
		graph[edge.From] = append(graph[edge.From], edge.To)
	}
	colors := map[string]int{}
	var visit func(string) bool
	visit = func(id string) bool {
		colors[id] = 1
		for _, next := range graph[id] {
			if colors[next] == 1 || (colors[next] == 0 && visit(next)) {
				return true
			}
		}
		colors[id] = 2
		return false
	}
	for id := range known {
		if colors[id] == 0 && visit(id) {
			return reject("invalid-topology", "manifest topology contains a cycle")
		}
	}
	return nil
}
func validateGoalPlanMapping(items []work.Item, goalID string, manifest GoalPlanManifest, mappings []GoalPlanNodeMapping) error {
	goalItems := map[string]work.Item{}
	for _, item := range items {
		if item.GoalID == goalID {
			goalItems[item.ID] = item
		}
	}
	if len(goalItems) != len(manifest.Nodes) || len(mappings) != len(manifest.Nodes) {
		return errors.New("plan nodes and Goal Work Items must map one-to-one")
	}
	nodes := map[string]GoalPlanNode{}
	for _, node := range manifest.Nodes {
		nodes[node.NodeRef] = node
	}
	nodeToItem := map[string]string{}
	itemToNode := map[string]string{}
	for _, mapping := range mappings {
		if _, ok := nodes[mapping.PlanNodeRef]; !ok || goalItems[mapping.WorkItemID].ID == "" || nodeToItem[mapping.PlanNodeRef] != "" || itemToNode[mapping.WorkItemID] != "" {
			return errors.New("node mappings must uniquely name declared plan nodes and Goal Work Items")
		}
		nodeToItem[mapping.PlanNodeRef] = mapping.WorkItemID
		itemToNode[mapping.WorkItemID] = mapping.PlanNodeRef
	}
	for ref, node := range nodes {
		id := nodeToItem[ref]
		item := goalItems[id]
		if id == "" || item.StoryRef != node.StoryRef {
			return fmt.Errorf("plan node %q does not match its Work Item story", ref)
		}
		want := map[string]bool{}
		for _, parent := range node.DependsOn {
			want[nodeToItem[parent]] = true
		}
		got := map[string]bool{}
		for _, dependency := range item.DependsOn {
			got[dependency] = true
		}
		if len(want) != len(got) {
			return fmt.Errorf("plan node %q dependencies do not match its Work Item", ref)
		}
		for dependency := range want {
			if !got[dependency] {
				return fmt.Errorf("plan node %q dependencies do not match its Work Item", ref)
			}
		}
	}
	return nil
}
