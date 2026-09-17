// Package readiness evaluates the upstream-owned Story readiness contract from
// already-loaded values. It deliberately does not read the filesystem, state,
// Git, or Markdown.
package readiness

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

const supportedSchemaVersion = 1

// DefectCode is a stable machine-readable readiness finding.
type DefectCode string

const (
	MissingContract              DefectCode = "MISSING_STORY_READINESS_CONTRACT"
	InvalidContract              DefectCode = "INVALID_STORY_READINESS_CONTRACT"
	SourceDigestMismatch         DefectCode = "STORY_READINESS_SOURCE_DIGEST_MISMATCH"
	MissingInputSource           DefectCode = "MISSING_INPUT_SOURCE"
	OutOfScopeOperation          DefectCode = "OUT_OF_SCOPE_OPERATION"
	FutureIdentityDependency     DefectCode = "FUTURE_IDENTITY_DEPENDENCY"
	UndeliveredPrerequisiteInput DefectCode = "UNDELIVERED_PREREQUISITE_INPUT"
	UnplannedDecisionFollowup    DefectCode = "UNPLANNED_DECISION_FOLLOWUP"
)

// Contract is the normalized v1 portion of PraxisBound's readiness sidecar
// ForgePilot consumes. Upstream owns the Story schema and all Markdown meaning.
type Contract struct {
	SchemaVersion      int                 `json:"schema_version"`
	StoryRef           string              `json:"story_ref"`
	StoryMDDigest      string              `json:"story_md_digest"`
	AcceptanceMDDigest string              `json:"acceptance_md_digest"`
	Criteria           []Criterion         `json:"criteria"`
	Inputs             []InputDeclaration  `json:"inputs"`
	Outputs            []OutputDeclaration `json:"outputs"`
	DecisionFollowUps  []DecisionFollowUp  `json:"decision_follow_ups"`
}

type Criterion struct {
	ID               string           `json:"id"`
	Operations       []string         `json:"operations"`
	Owner            string           `json:"owner"`
	FutureIdentities []FutureIdentity `json:"future_identities"`
}

type FutureIdentity struct {
	Kind                 string `json:"kind"`
	Availability         string `json:"availability"`
	PrerequisiteStoryRef string `json:"prerequisite_story_ref,omitempty"`
}

type InputDeclaration struct {
	ID     string      `json:"id"`
	Source InputSource `json:"source"`
}
type InputSource struct {
	PreexistingArtifact *PreexistingArtifact `json:"preexisting_artifact,omitempty"`
	PrerequisiteOutput  *PrerequisiteOutput  `json:"prerequisite_output,omitempty"`
	ExternalPreexisting *ExternalPreexisting `json:"external_preexisting,omitempty"`
}
type PreexistingArtifact struct {
	Path string `json:"path"`
}
type PrerequisiteOutput struct {
	OutputID string `json:"output_id"`
}
type ExternalPreexisting struct {
	Identity string `json:"identity"`
}
type OutputDeclaration struct {
	ID string `json:"id"`
}
type DecisionFollowUp struct {
	GateID           string `json:"gate_id"`
	Choice           string `json:"choice"`
	FollowUpStoryRef string `json:"follow_up_story_ref"`
}

// Item joins one Work Item identity to already-read contract and source bytes.
type Item struct {
	ID               string
	StoryRef         string
	Contract         *Contract
	StoryMD          []byte
	AcceptanceMD     []byte
	Sidecar          []byte
	SourceErrors     map[string]string
	PreflightDefects []Defect
	DependsOn        []string
	CreatedAt        time.Time
}

// Input contains only values. Its caller is responsible for filesystem and
// state access at the app/repository seam.
type Input struct {
	Items     []Item
	Gates     []Gate
	Artifacts map[string]bool
}

// Gate carries only the exact, resolved choice readiness is permitted to use.
type Gate struct {
	ID       string
	Choice   string
	Resolved bool
}

// Defect identifies one readiness problem without making a lifecycle claim.
type Defect struct {
	Code           DefectCode
	WorkItemID     string
	StoryRef       string
	SourceFile     string
	DeclaredDigest string
	ObservedDigest string
	Detail         string
	LocalID        string
}

// Report is the complete deterministic result of one whole-Goal review.
type Report struct {
	Defects []Defect
}

func (report Report) String() string {
	parts := make([]string, 0, len(report.Defects))
	for _, defect := range report.Defects {
		part := string(defect.Code) + " " + defect.WorkItemID + " " + defect.StoryRef
		if defect.SourceFile != "" {
			part += " " + defect.SourceFile
		}
		if defect.DeclaredDigest != "" {
			part += " declared=" + defect.DeclaredDigest
		}
		if defect.ObservedDigest != "" {
			part += " observed=" + defect.ObservedDigest
		}
		if defect.Detail != "" {
			part += ": " + defect.Detail
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "; ")
}

// ScopeBindings returns the source identity that authorizes a Runner to keep
// driving a Goal. Callers must first require an empty Review report.
func ScopeBindings(input Input) []string {
	bindings := make([]string, 0, len(input.Items))
	for _, item := range input.Items {
		if item.Contract == nil {
			continue
		}
		bindings = append(bindings, strings.Join([]string{
			"story-readiness", item.ID, item.StoryRef, Digest(item.Sidecar), Digest(item.StoryMD), Digest(item.AcceptanceMD),
		}, "\x1f"))
	}
	sort.Strings(bindings)
	return bindings
}

// Decode reads one strict v1 sidecar. Unknown fields are rejected so a newer
// upstream contract cannot be silently treated as an older, weaker one.
func Decode(contents []byte) (Contract, error) {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var contract Contract
	if err := decoder.Decode(&contract); err != nil {
		return Contract{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Contract{}, errors.New("contract contains extra JSON values")
	}
	return contract, nil
}

// Digest returns the canonical identity used by v1 source binding.
func Digest(contents []byte) string {
	sum := sha256.Sum256(contents)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Review evaluates source bindings. Further contract rules build on the same
// value-only seam rather than teaching callers readiness semantics.
func Review(input Input) Report {
	var report Report
	for _, item := range input.Items {
		report.Defects = append(report.Defects, item.PreflightDefects...)
		if len(item.PreflightDefects) > 0 && item.Contract == nil {
			continue
		}
		if item.Contract == nil {
			report.Defects = append(report.Defects, Defect{Code: MissingContract, WorkItemID: item.ID, StoryRef: item.StoryRef})
			continue
		}
		if err := validateContract(*item.Contract, item.StoryRef); err != nil {
			report.Defects = append(report.Defects, Defect{Code: InvalidContract, WorkItemID: item.ID, StoryRef: item.StoryRef, Detail: err.Error()})
			continue
		}
		report.Defects = append(report.Defects, sourceDigestDefects(item)...)
		report.Defects = append(report.Defects, contractDefects(item, input.Artifacts)...)
	}
	report.Defects = append(report.Defects, wholeGoalDefects(input)...)
	sort.Slice(report.Defects, func(i, j int) bool {
		left, right := report.Defects[i], report.Defects[j]
		leftItem, rightItem := itemFor(input.Items, left.WorkItemID), itemFor(input.Items, right.WorkItemID)
		if !leftItem.CreatedAt.Equal(rightItem.CreatedAt) {
			return leftItem.CreatedAt.Before(rightItem.CreatedAt)
		}
		if left.WorkItemID != right.WorkItemID {
			return left.WorkItemID < right.WorkItemID
		}
		if left.StoryRef != right.StoryRef {
			return left.StoryRef < right.StoryRef
		}
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		if left.LocalID != right.LocalID {
			return left.LocalID < right.LocalID
		}
		return left.SourceFile < right.SourceFile
	})
	return report
}

func contractDefects(item Item, artifacts map[string]bool) []Defect {
	var defects []Defect
	criteria := map[string]bool{}
	for _, criterion := range item.Contract.Criteria {
		if criterion.ID == "" || criteria[criterion.ID] {
			defects = append(defects, Defect{Code: InvalidContract, WorkItemID: item.ID, StoryRef: item.StoryRef, Detail: "criterion IDs must be non-empty and unique"})
			continue
		}
		criteria[criterion.ID] = true
		if !validOwner(criterion.Owner) {
			defects = append(defects, invalid(item, criterion.ID, "unknown criterion owner"))
		}
		for _, operation := range criterion.Operations {
			if !validOperation(operation) {
				defects = append(defects, invalid(item, criterion.ID, "unknown operation "+operation))
			} else if criterion.Owner == "runner_worker" && operation != "plan" && operation != "modify" {
				defects = append(defects, Defect{Code: OutOfScopeOperation, WorkItemID: item.ID, StoryRef: item.StoryRef, Detail: criterion.ID + " " + operation})
			}
		}
		for _, identity := range criterion.FutureIdentities {
			if !validIdentityKind(identity.Kind) || !validAvailability(identity.Availability) || (identity.Availability == "prerequisite") != (identity.PrerequisiteStoryRef != "") {
				defects = append(defects, invalid(item, criterion.ID, "invalid future identity"))
				continue
			}
			if identity.Availability == "produced_by_current_work_item" {
				defects = append(defects, Defect{Code: FutureIdentityDependency, WorkItemID: item.ID, StoryRef: item.StoryRef, Detail: criterion.ID + " " + identity.Kind})
			}
		}
	}
	inputs := map[string]bool{}
	for _, input := range item.Contract.Inputs {
		count := 0
		if input.Source.PreexistingArtifact != nil {
			count++
		}
		if input.Source.PrerequisiteOutput != nil {
			count++
		}
		if input.Source.ExternalPreexisting != nil {
			count++
		}
		if input.ID == "" || inputs[input.ID] || count != 1 {
			defects = append(defects, Defect{Code: MissingInputSource, WorkItemID: item.ID, StoryRef: item.StoryRef, Detail: input.ID})
			continue
		}
		inputs[input.ID] = true
		if source := input.Source.PreexistingArtifact; source != nil && (source.Path == "" || !artifacts[source.Path]) {
			defects = append(defects, Defect{Code: MissingInputSource, WorkItemID: item.ID, StoryRef: item.StoryRef, LocalID: input.ID, Detail: source.Path})
		}
		if source := input.Source.PrerequisiteOutput; source != nil && source.OutputID == "" {
			defects = append(defects, Defect{Code: MissingInputSource, WorkItemID: item.ID, StoryRef: item.StoryRef, LocalID: input.ID})
		}
		if source := input.Source.ExternalPreexisting; source != nil && source.Identity == "" {
			defects = append(defects, Defect{Code: MissingInputSource, WorkItemID: item.ID, StoryRef: item.StoryRef, LocalID: input.ID})
		}
	}
	outputs := map[string]bool{}
	for _, output := range item.Contract.Outputs {
		if output.ID == "" || outputs[output.ID] {
			defects = append(defects, invalid(item, output.ID, "output IDs must be non-empty and unique"))
		}
		outputs[output.ID] = true
	}
	for _, followup := range item.Contract.DecisionFollowUps {
		if followup.GateID == "" || followup.Choice == "" || followup.FollowUpStoryRef == "" {
			defects = append(defects, invalid(item, followup.GateID, "invalid decision follow-up"))
		}
	}
	return defects
}

func wholeGoalDefects(input Input) []Defect {
	byID, stories, outputs := map[string]Item{}, map[string]Item{}, map[string]Item{}
	var defects []Defect
	for _, item := range input.Items {
		if item.Contract == nil {
			continue
		}
		byID[item.ID] = item
		if prior, exists := stories[item.Contract.StoryRef]; exists {
			defects = append(defects, invalid(item, item.Contract.StoryRef, "duplicate Story identity with "+prior.ID))
		} else {
			stories[item.Contract.StoryRef] = item
		}
		for _, output := range item.Contract.Outputs {
			if prior, exists := outputs[output.ID]; exists {
				defects = append(defects, invalid(item, output.ID, "duplicate Goal output with "+prior.ID))
			} else {
				outputs[output.ID] = item
			}
		}
	}
	resolved := map[string]string{}
	for _, gate := range input.Gates {
		if gate.Resolved {
			resolved[gate.ID] = gate.Choice
		}
	}
	for _, item := range input.Items {
		if item.Contract == nil {
			continue
		}
		closure := transitiveDependencies(item, byID)
		for _, criterion := range item.Contract.Criteria {
			for _, identity := range criterion.FutureIdentities {
				if identity.Availability == "prerequisite" {
					producer, exists := stories[identity.PrerequisiteStoryRef]
					if !exists || !closure[producer.ID] {
						defects = append(defects, Defect{Code: FutureIdentityDependency, WorkItemID: item.ID, StoryRef: item.StoryRef, LocalID: criterion.ID, Detail: identity.Kind})
					}
				}
			}
		}
		for _, declaration := range item.Contract.Inputs {
			if source := declaration.Source.PrerequisiteOutput; source != nil {
				producer, exists := outputs[source.OutputID]
				if !exists || !closure[producer.ID] {
					defects = append(defects, Defect{Code: UndeliveredPrerequisiteInput, WorkItemID: item.ID, StoryRef: item.StoryRef, LocalID: declaration.ID, Detail: source.OutputID})
				}
			}
		}
		for _, followup := range item.Contract.DecisionFollowUps {
			if resolved[followup.GateID] == followup.Choice {
				if _, exists := stories[followup.FollowUpStoryRef]; !exists {
					defects = append(defects, Defect{Code: UnplannedDecisionFollowup, WorkItemID: item.ID, StoryRef: item.StoryRef, LocalID: followup.GateID, Detail: followup.Choice + " " + followup.FollowUpStoryRef})
				}
			}
		}
	}
	return defects
}

func transitiveDependencies(item Item, byID map[string]Item) map[string]bool {
	found := map[string]bool{}
	var visit func(string)
	visit = func(id string) {
		current, exists := byID[id]
		if !exists {
			return
		}
		for _, dependency := range current.DependsOn {
			if !found[dependency] {
				found[dependency] = true
				visit(dependency)
			}
		}
	}
	visit(item.ID)
	return found
}
func itemFor(items []Item, id string) Item {
	for _, item := range items {
		if item.ID == id {
			return item
		}
	}
	return Item{}
}
func invalid(item Item, localID, detail string) Defect {
	return Defect{Code: InvalidContract, WorkItemID: item.ID, StoryRef: item.StoryRef, LocalID: localID, Detail: detail}
}
func validOwner(value string) bool {
	return value == "runner_worker" || value == "canonical_verification" || value == "integration_final" || value == "human" || value == "external"
}
func validOperation(value string) bool {
	switch value {
	case "plan", "modify", "add_dependency", "migration", "commit", "push", "deploy", "publish":
		return true
	}
	return false
}
func validIdentityKind(value string) bool {
	return value == "commit" || value == "publication" || value == "release"
}
func validAvailability(value string) bool {
	return value == "preexisting" || value == "produced_by_current_work_item" || value == "prerequisite" || value == "external"
}

func sourceDigestDefects(item Item) []Defect {
	files := []struct {
		name     string
		contents []byte
		declared string
	}{
		{name: "story.md", contents: item.StoryMD, declared: item.Contract.StoryMDDigest},
		{name: "acceptance.md", contents: item.AcceptanceMD, declared: item.Contract.AcceptanceMDDigest},
	}
	var defects []Defect
	for _, file := range files {
		if detail := item.SourceErrors[file.name]; detail != "" {
			defects = append(defects, Defect{Code: SourceDigestMismatch, WorkItemID: item.ID,
				StoryRef: item.StoryRef, SourceFile: file.name, DeclaredDigest: file.declared, Detail: detail})
			continue
		}
		observed := Digest(file.contents)
		if observed != file.declared {
			defects = append(defects, Defect{Code: SourceDigestMismatch, WorkItemID: item.ID,
				StoryRef: item.StoryRef, SourceFile: file.name, DeclaredDigest: file.declared, ObservedDigest: observed})
		}
	}
	return defects
}

func validateContract(contract Contract, storyRef string) error {
	if contract.SchemaVersion != supportedSchemaVersion {
		return fmt.Errorf("unsupported schema version %d", contract.SchemaVersion)
	}
	if contract.StoryRef == "" || contract.StoryRef != storyRef {
		return fmt.Errorf("story reference does not match owning Work Item")
	}
	for _, digest := range []string{contract.StoryMDDigest, contract.AcceptanceMDDigest} {
		if !validDigest(digest) {
			return fmt.Errorf("invalid source digest %q", digest)
		}
	}
	return nil
}

func validDigest(digest string) bool {
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
		return false
	}
	for _, character := range digest[len("sha256:"):] {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}
