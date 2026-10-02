package work

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const MaxExecutionAuthorizationLifetime = 14 * 24 * time.Hour

// GoalExecution is the single Goal-owned aggregate for plan adoption and its
// cross-run authorization ledger. The witness cross-links the three durable
// records; it does not own accounting or alter Goal lifecycle.
type GoalExecution struct {
	GoalID         string                   `json:"goal_id"`
	Workspace      string                   `json:"workspace"`
	PlanBindings   []GoalPlanBinding        `json:"plan_bindings"`
	Authorizations []ExecutionAuthorization `json:"authorizations"`
	Ledger         ExecutionLedger          `json:"ledger"`
	Witness        GoalExecutionWitness     `json:"witness"`
}

type GoalPlanBinding struct {
	Revision       int    `json:"revision"`
	RequestSHA256  string `json:"request_sha256"`
	PlanID         string `json:"plan_id"`
	PlanRevision   int64  `json:"plan_revision"`
	ManifestSHA256 string `json:"manifest_sha256"`
	// Manifest is empty only for bindings migrated before the manifest path was
	// persisted. Such a binding remains readable as history but is fail-closed
	// for a new Runner action because its current artifact cannot be rechecked.
	Manifest         ExecutionArtifactBinding   `json:"manifest,omitempty"`
	CoverageReviewID string                     `json:"coverage_review_id"`
	CoverageReview   ExecutionArtifactBinding   `json:"coverage_review"`
	Declaration      ExecutionArtifactBinding   `json:"declaration"`
	ReviewedSources  []ExecutionArtifactBinding `json:"reviewed_sources"`
	Nodes            []ExecutionPlanNodeBinding `json:"nodes"`
	AdoptedAt        time.Time                  `json:"adopted_at"`
	Digest           string                     `json:"digest"`
}

type ExecutionArtifactBinding struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type ExecutionPlanNodeBinding struct {
	PlanNodeRef       string                   `json:"plan_node_ref"`
	StoryRef          string                   `json:"story_ref"`
	ReadinessContract ExecutionArtifactBinding `json:"readiness_contract"`
	DependsOn         []string                 `json:"depends_on"`
	WorkItemID        string                   `json:"work_item_id"`
}

type ExecutionAuthorization struct {
	Revision          int                        `json:"revision"`
	GoalID            string                     `json:"goal_id"`
	Workspace         string                     `json:"workspace"`
	PlanBindingDigest string                     `json:"plan_binding_digest"`
	RequestSHA256     string                     `json:"request_sha256"`
	ApprovalToken     string                     `json:"approval_token"`
	Approver          string                     `json:"approver"`
	AuthorizedAt      time.Time                  `json:"authorized_at"`
	ExpiresAt         time.Time                  `json:"expires_at"`
	Caps              ExecutionCaps              `json:"caps"`
	WorkerProfile     WorkerProfile              `json:"worker_profile"`
	WorkerIdentity    *ResolvedWorkerIdentity    `json:"worker_identity"`
	EngineGeneration  *ExecutionEngineGeneration `json:"engine_generation"`
	// RetentionAcquired is true only for an authorization published after its
	// own marker acquisition. Migrated history has false/unknown provenance.
	RetentionAcquired bool   `json:"retention_acquired,omitempty"`
	Digest            string `json:"digest"`
}

// WorkerProfile is the explicitly requested execution profile. Version
// identity is separate because preview cannot truthfully report what the
// executable would say without launching it.
type WorkerProfile struct {
	Runtime          string `json:"runtime"`
	ExecutablePath   string `json:"executable_path"`
	ExecutableSHA256 string `json:"executable_sha256"`
	Model            string `json:"model"`
	Effort           string `json:"effort"`
	Sandbox          string `json:"sandbox"`
}

type ResolvedWorkerIdentity struct {
	ExecutablePath   string    `json:"executable_path"`
	ExecutableSHA256 string    `json:"executable_sha256"`
	ReportedVersion  string    `json:"reported_version"`
	ObservedAt       time.Time `json:"observed_at"`
}

type ExecutionEngineGeneration struct {
	// SourceCommit and PayloadSHA256 are the immutable tuple that the
	// Bootstrap retention protocol accepts. PayloadSHA256 includes its
	// sha256: prefix, matching the helper's machine protocol.
	SourceCommit  string `json:"source_commit"`
	PayloadSHA256 string `json:"payload_sha256"`
}

type ExecutionCaps struct {
	MaxSteps                    int                     `json:"max_steps"`
	MaxTechnicalAttemptsPerNode int                     `json:"max_technical_attempts_per_node"`
	MaxRuns                     int                     `json:"max_runs"`
	MaxRecoveries               int                     `json:"max_recoveries"`
	Artifacts                   ExecutionArtifactLimits `json:"artifacts"`
}

type ExecutionArtifactLimits struct {
	MaxHandoffBytes int `json:"max_handoff_bytes"`
	MaxWriteBytes   int `json:"max_write_bytes"`
	MaxRunBytes     int `json:"max_run_bytes"`
	MaxTotalBytes   int `json:"max_total_bytes"`
}

type ExecutionLedger struct {
	Revision              int                     `json:"revision"`
	AuthorizationRevision int                     `json:"authorization_revision"`
	StepsConsumed         int                     `json:"steps_consumed"`
	RunsConsumed          int                     `json:"runs_consumed"`
	RecoveriesConsumed    int                     `json:"recoveries_consumed"`
	NodeAttempts          []ExecutionNodeAttempts `json:"node_attempts"`
	// ArtifactAccountingStartRevision is zero only for v15 authorizations
	// migrated without trustworthy artifact-byte history. Such state stays
	// readable but cannot admit artifact output before explicit reauthorization.
	ArtifactAccountingStartRevision int                                `json:"artifact_accounting_start_revision"`
	ArtifactBytesConsumed           int64                              `json:"artifact_bytes_consumed"`
	ArtifactByteReservations        []ExecutionArtifactByteReservation `json:"artifact_byte_reservations,omitempty"`
	// Reservations are the durable, cross-run accounting authority. A Run
	// Record is deliberately only an execution journal: it must never be used
	// to infer or rebuild this ledger after a crash.
	Reservations []ExecutionReservation `json:"reservations,omitempty"`
	// NeedsHumanDispositions reclassify an already charged ACTION after its
	// matching Run Record receipt confirms a valid needs_human result. They are
	// append-only so a missing confirmation remains a technical attempt.
	NeedsHumanDispositions []ExecutionNeedsHumanDisposition `json:"needs_human_dispositions,omitempty"`
	Digest                 string                           `json:"digest"`
}

type ExecutionNodeAttempts struct {
	PlanNodeRef       string `json:"plan_node_ref"`
	TechnicalAttempts int    `json:"technical_attempts"`
}

// ExecutionArtifactByteReservation is one stable, append-only claim against
// the authorization-wide artifact-byte cap, saved before a session writes.
type ExecutionArtifactByteReservation struct {
	ID        string    `json:"id"`
	RunID     string    `json:"run_id"`
	Bytes     int64     `json:"bytes"`
	CreatedAt time.Time `json:"created_at"`
}

// ExecutionReservation records one unit of authorization consumption before
// the corresponding external work can start. Its ID is caller chosen so a
// crash retry can replay exactly the same reservation without another charge.
type ExecutionReservation struct {
	ID          string                     `json:"id"`
	Kind        ExecutionReservationKind   `json:"kind"`
	RunID       string                     `json:"run_id"`
	PlanNodeRef string                     `json:"plan_node_ref,omitempty"`
	CreatedAt   time.Time                  `json:"created_at"`
	Status      ExecutionReservationStatus `json:"status"`
}

// ExecutionReservationReceipt is the detached Run Record proof for one stored
// reservation. Its digest is intentionally not embedded in ExecutionLedger,
// which would make the ledger digest recursive.
type ExecutionReservationReceipt struct {
	ReservationID     string `json:"reservation_id"`
	ReservationDigest string `json:"reservation_digest"`
}

// ExecutionNeedsHumanDisposition is the durable confirmation that one ACTION
// reservation ended in needs_human. The receipt binds it to the immutable
// reservation contents and RunID prevents a receipt from another run being
// replayed as a reclassification.
type ExecutionNeedsHumanDisposition struct {
	RunID             string `json:"run_id"`
	ReservationID     string `json:"reservation_id"`
	ReservationDigest string `json:"reservation_digest"`
}

type ExecutionReservationKind string

const (
	ExecutionReservationRun      ExecutionReservationKind = "RUN"
	ExecutionReservationAction   ExecutionReservationKind = "ACTION"
	ExecutionReservationStep     ExecutionReservationKind = "STEP"
	ExecutionReservationRecovery ExecutionReservationKind = "RECOVERY"
)

type ExecutionReservationStatus string

const ExecutionReservationPrepared ExecutionReservationStatus = "PREPARED"

type GoalExecutionWitness struct {
	GoalID                string `json:"goal_id"`
	Workspace             string `json:"workspace"`
	PlanBindingDigest     string `json:"plan_binding_digest"`
	AuthorizationRevision int    `json:"authorization_revision"`
	AuthorizationDigest   string `json:"authorization_digest"`
	LedgerRevision        int    `json:"ledger_revision"`
	LedgerDigest          string `json:"ledger_digest"`
	Digest                string `json:"digest"`
}

// AdoptInitialExecution attaches the first fully mapped plan and its bounded,
// zero-use authorization ledger to a Goal. It deliberately does not change
// Goal or Work Item lifecycle. The state takes ownership of every nested slice.
//
// No production code calls this any more; it is kept only so tests can build a
// digest-valid Goal.Execution fixture until schema 19 removes the data model.
func (s *State) AdoptInitialExecution(execution GoalExecution) error {
	goal := s.goal(execution.GoalID)
	if goal == nil {
		return fmt.Errorf("unknown goal %q", execution.GoalID)
	}
	if goal.Status != GoalActive {
		return fmt.Errorf("goal %q is not active", goal.ID)
	}
	if goal.Execution != nil {
		return fmt.Errorf("goal %q already has an execution authorization", goal.ID)
	}
	for _, authorization := range execution.Authorizations {
		if authorization.WorkerIdentity != nil {
			return errors.New("initial execution cannot claim an unobserved Worker identity")
		}
		if authorization.EngineGeneration != nil && !authorization.RetentionAcquired {
			return errors.New("initial execution cannot claim an engine generation without retention provenance")
		}
	}
	execution = cloneGoalExecution(execution)
	if err := sealInitialExecution(&execution); err != nil {
		return err
	}
	items := make(map[string]Item, len(s.WorkItems))
	for _, item := range s.WorkItems {
		items[item.ID] = item
	}
	if err := validateGoalExecution(execution, *goal, items); err != nil {
		return err
	}
	goal.Execution = &execution
	return nil
}

func sealInitialExecution(execution *GoalExecution) error {
	if len(execution.PlanBindings) != 1 || len(execution.Authorizations) != 1 {
		return errors.New("initial execution requires exactly one plan binding and authorization")
	}
	binding := &execution.PlanBindings[0]
	binding.Digest = ""
	digest, err := executionDigest("forgepilot.goal-plan-binding/v1", *binding)
	if err != nil {
		return err
	}
	binding.Digest = digest

	authorization := &execution.Authorizations[0]
	if authorization.PlanBindingDigest != "" && authorization.PlanBindingDigest != binding.Digest {
		return errors.New("authorization refers to a different Goal Plan binding")
	}
	if authorization.RequestSHA256 != "" && authorization.RequestSHA256 != binding.RequestSHA256 {
		return errors.New("authorization refers to a different execution request")
	}
	authorization.PlanBindingDigest = binding.Digest
	authorization.RequestSHA256 = binding.RequestSHA256
	authorization.Digest = ""
	authorizationDigest, err := executionDigest("forgepilot.execution-authorization/v1", *authorization)
	if err != nil {
		return err
	}
	authorization.Digest = authorizationDigest

	ledger := &execution.Ledger
	if ledger.AuthorizationRevision != 1 {
		return errors.New("initial ledger must refer to authorization revision one")
	}
	if ledger.ArtifactAccountingStartRevision == 0 {
		ledger.ArtifactAccountingStartRevision = 1
	}
	ledger.Digest = ""
	ledgerDigest, err := executionDigest("forgepilot.execution-ledger/v1", *ledger)
	if err != nil {
		return err
	}
	ledger.Digest = ledgerDigest

	witness := GoalExecutionWitness{
		GoalID: execution.GoalID, Workspace: execution.Workspace,
		PlanBindingDigest: binding.Digest, AuthorizationRevision: authorization.Revision,
		AuthorizationDigest: authorization.Digest, LedgerRevision: ledger.Revision, LedgerDigest: ledger.Digest,
	}
	if execution.Witness != (GoalExecutionWitness{}) {
		provided := execution.Witness
		provided.Digest = ""
		if provided != witness {
			return errors.New("Goal execution witness does not match the initial binding, authorization, and ledger")
		}
	}
	witness.Digest, err = executionDigest("forgepilot.goal-execution-witness/v1", witness)
	if err != nil {
		return err
	}
	execution.Witness = witness
	return nil
}

func validateGoalExecution(execution GoalExecution, goal Goal, items map[string]Item) error {
	if execution.GoalID != goal.ID || execution.Workspace != goal.Repository {
		return errors.New("Goal or workspace binding does not match the registered Goal")
	}
	if len(execution.PlanBindings) == 0 || len(execution.PlanBindings) != len(execution.Authorizations) {
		return errors.New("execution requires paired plan binding and authorization history")
	}
	for i, binding := range execution.PlanBindings {
		authorization := execution.Authorizations[i]
		if binding.Revision != i+1 || authorization.Revision != i+1 {
			return errors.New("execution binding and authorization revisions must be contiguous")
		}
		if err := validatePlanBinding(binding); err != nil {
			return err
		}
		if err := validateAuthorization(authorization, binding, execution.GoalID, execution.Workspace); err != nil {
			return err
		}
		if i > 0 {
			if err := validateAdditivePlanRevision(execution.PlanBindings[i-1], binding); err != nil {
				return err
			}
		}
	}
	binding := execution.PlanBindings[len(execution.PlanBindings)-1]
	authorization := execution.Authorizations[len(execution.Authorizations)-1]

	if err := validateExecutionLedger(execution.Ledger, binding, authorization); err != nil {
		return err
	}
	witness := execution.Witness
	if witness.GoalID != execution.GoalID || witness.Workspace != execution.Workspace ||
		witness.PlanBindingDigest != binding.Digest || witness.AuthorizationRevision != authorization.Revision ||
		witness.AuthorizationDigest != authorization.Digest || witness.LedgerRevision != execution.Ledger.Revision ||
		witness.LedgerDigest != execution.Ledger.Digest || !validBoundDigest(witness.Digest) {
		return errors.New("Goal execution witness does not cross-link the current binding, authorization, and ledger")
	}
	expectedWitnessDigest, err := witnessDigest(witness)
	if err != nil || witness.Digest != expectedWitnessDigest {
		return errors.New("Goal execution witness digest does not match its contents")
	}
	return validatePlanNodeRegistration(binding.Nodes, goal.ID, items)
}

func validatePlanBinding(binding GoalPlanBinding) error {
	if !validBoundDigest(binding.RequestSHA256) || !validExecutionIdentifier(binding.PlanID) || binding.PlanRevision < 1 ||
		!validHexDigest(binding.ManifestSHA256) || !validExecutionIdentifier(binding.CoverageReviewID) ||
		!validHexDigest(binding.CoverageReview.SHA256) || !validArtifactPath(binding.CoverageReview.Path) ||
		!validHexDigest(binding.Declaration.SHA256) || !validArtifactPath(binding.Declaration.Path) || binding.AdoptedAt.IsZero() {
		return errors.New("Goal Plan artifact binding is incomplete or invalid")
	}
	if binding.Manifest.Path != "" && (!validArtifactPath(binding.Manifest.Path) || binding.Manifest.SHA256 != binding.ManifestSHA256) {
		return errors.New("Goal Plan manifest path binding is invalid")
	}
	if len(binding.Nodes) == 0 {
		return errors.New("Goal Plan binding has no nodes")
	}
	for _, source := range binding.ReviewedSources {
		if !validArtifactPath(source.Path) || !validHexDigest(source.SHA256) {
			return errors.New("reviewed source binding is invalid")
		}
	}
	if !validBoundDigest(binding.Digest) {
		return errors.New("Goal Plan binding digest is invalid")
	}
	expected, err := bindingDigest(binding)
	if err != nil || binding.Digest != expected {
		return errors.New("Goal Plan binding digest does not match its contents")
	}
	return nil
}

func validateAuthorization(authorization ExecutionAuthorization, binding GoalPlanBinding, goalID, workspace string) error {
	if authorization.GoalID != goalID || authorization.Workspace != workspace || authorization.PlanBindingDigest != binding.Digest ||
		authorization.RequestSHA256 != binding.RequestSHA256 || !validBoundDigest(authorization.ApprovalToken) {
		return errors.New("Execution Authorization does not match its Goal Plan binding")
	}
	if !validExecutionIdentifier(authorization.Approver) || authorization.AuthorizedAt.IsZero() ||
		authorization.ExpiresAt.IsZero() || !authorization.ExpiresAt.After(authorization.AuthorizedAt) ||
		authorization.ExpiresAt.Sub(authorization.AuthorizedAt) > MaxExecutionAuthorizationLifetime {
		return errors.New("Execution Authorization approver or fixed expiry is invalid")
	}
	if err := validateExecutionCaps(authorization.Caps); err != nil {
		return err
	}
	if err := ValidateWorkerProfile(authorization.WorkerProfile); err != nil {
		return err
	}
	if identity := authorization.WorkerIdentity; identity != nil {
		if identity.ExecutablePath != authorization.WorkerProfile.ExecutablePath || identity.ExecutableSHA256 != authorization.WorkerProfile.ExecutableSHA256 ||
			!validExecutionIdentifier(identity.ReportedVersion) || identity.ObservedAt.IsZero() {
			return errors.New("resolved Worker identity does not match the requested profile")
		}
	}
	if generation := authorization.EngineGeneration; generation != nil && !validExecutionEngineGeneration(*generation) {
		return errors.New("ForgePilot engine generation identity is invalid")
	}
	if authorization.RetentionAcquired && authorization.EngineGeneration == nil {
		return errors.New("execution retention provenance requires an engine generation")
	}
	if !validBoundDigest(authorization.Digest) {
		return errors.New("Execution Authorization digest is invalid")
	}
	expected, err := authorizationDigest(authorization)
	if err != nil || authorization.Digest != expected {
		return errors.New("Execution Authorization digest does not match its contents")
	}
	return nil
}

func validateAdditivePlanRevision(previous, next GoalPlanBinding) error {
	if len(next.Nodes) < len(previous.Nodes) {
		return errors.New("execution revision deletes an existing Plan Node")
	}
	for _, node := range previous.Nodes {
		candidate, exists := executionPlanNodeByReference(next.Nodes, node.PlanNodeRef)
		if !exists || node.WorkItemID != candidate.WorkItemID || node.StoryRef != candidate.StoryRef || !sameStrings(node.DependsOn, candidate.DependsOn) {
			return fmt.Errorf("execution revision rewrites existing Plan Node %q", node.PlanNodeRef)
		}
	}
	return nil
}

func executionPlanNodeByReference(nodes []ExecutionPlanNodeBinding, reference string) (ExecutionPlanNodeBinding, bool) {
	for _, node := range nodes {
		if node.PlanNodeRef == reference {
			return node, true
		}
	}
	return ExecutionPlanNodeBinding{}, false
}

func validateExecutionCaps(caps ExecutionCaps) error {
	if caps.MaxSteps < 1 || caps.MaxTechnicalAttemptsPerNode < 1 || caps.MaxRuns < 1 || caps.MaxRecoveries < 1 {
		return errors.New("execution caps must all be positive")
	}
	limits := caps.Artifacts
	if limits.MaxHandoffBytes < 1 || limits.MaxWriteBytes < 1 || limits.MaxRunBytes < 1 || limits.MaxTotalBytes < 1 {
		return errors.New("execution artifact limits must all be positive")
	}
	if limits.MaxWriteBytes > limits.MaxRunBytes || limits.MaxRunBytes > limits.MaxTotalBytes {
		return errors.New("execution artifact limits must satisfy write <= run <= total")
	}
	return nil
}

// ValidateWorkerProfile is the shared pure predicate for preview and durable
// state validation. Filesystem resolution and digesting remain app-layer work.
func ValidateWorkerProfile(profile WorkerProfile) error {
	if profile.Runtime != "codex" || !filepath.IsAbs(profile.ExecutablePath) ||
		filepath.Clean(profile.ExecutablePath) != profile.ExecutablePath || !validHexDigest(profile.ExecutableSHA256) ||
		!validExecutionIdentifier(profile.Model) || profile.Effort != "medium" || profile.Sandbox != "workspace-write" {
		return errors.New("Worker Profile requires an explicit Codex executable, model, medium effort, and workspace-write sandbox")
	}
	return nil
}

func validateExecutionLedger(ledger ExecutionLedger, binding GoalPlanBinding, authorization ExecutionAuthorization) error {
	if ledger.Revision < 1 || ledger.AuthorizationRevision != authorization.Revision || len(ledger.NodeAttempts) != len(binding.Nodes) {
		return errors.New("execution ledger has an invalid revision, authorization, or node mapping")
	}
	nodeAttempts := make(map[string]int, len(binding.Nodes))
	for i, node := range binding.Nodes {
		entry := ledger.NodeAttempts[i]
		if entry.PlanNodeRef != node.PlanNodeRef || entry.TechnicalAttempts < 0 {
			return errors.New("execution ledger node attempts must match every mapped Plan Node Reference")
		}
		nodeAttempts[entry.PlanNodeRef] = entry.TechnicalAttempts
	}
	if ledger.ArtifactAccountingStartRevision < 0 || ledger.ArtifactAccountingStartRevision > authorization.Revision {
		return errors.New("execution ledger has an invalid artifact accounting start revision")
	}
	artifactBytes := int64(0)
	artifactReservations := make(map[string]ExecutionArtifactByteReservation, len(ledger.ArtifactByteReservations))
	for _, reservation := range ledger.ArtifactByteReservations {
		if !validExecutionArtifactByteReservation(reservation) || artifactReservations[reservation.ID].ID != "" {
			return errors.New("execution ledger has an invalid or duplicate artifact-byte reservation")
		}
		artifactReservations[reservation.ID] = reservation
		artifactBytes += reservation.Bytes
	}
	if ledger.ArtifactAccountingStartRevision == 0 && (artifactBytes != 0 || len(artifactReservations) != 0) {
		return errors.New("execution ledger cannot claim artifact bytes before explicit accounting reauthorization")
	}
	if ledger.ArtifactBytesConsumed != artifactBytes || artifactBytes > int64(authorization.Caps.Artifacts.MaxTotalBytes) {
		return errors.New("execution ledger artifact-byte total does not match its reservations or authorization cap")
	}
	runs, steps, recoveries := 0, 0, 0
	reservedAttempts := make(map[string]int, len(nodeAttempts))
	reservations := make(map[string]ExecutionReservation, len(ledger.Reservations))
	for _, reservation := range ledger.Reservations {
		if !validExecutionReservation(reservation) || reservations[reservation.ID].ID != "" {
			return errors.New("execution ledger has an invalid or duplicate reservation")
		}
		reservations[reservation.ID] = reservation
		switch reservation.Kind {
		case ExecutionReservationRun:
			runs++
		case ExecutionReservationRecovery:
			recoveries++
		case ExecutionReservationAction:
			if _, ok := nodeAttempts[reservation.PlanNodeRef]; !ok {
				return errors.New("execution action reservation refers to an unknown Plan Node Reference")
			}
			reservedAttempts[reservation.PlanNodeRef]++
		case ExecutionReservationStep:
			if reservation.PlanNodeRef != "" {
				if _, ok := nodeAttempts[reservation.PlanNodeRef]; !ok {
					return errors.New("execution step reservation refers to an unknown Plan Node Reference")
				}
			}
			steps++
		}
	}
	disposed := make(map[string]bool, len(ledger.NeedsHumanDispositions))
	for _, disposition := range ledger.NeedsHumanDispositions {
		if !validExecutionNeedsHumanDisposition(disposition) || disposed[disposition.ReservationID] {
			return errors.New("execution ledger has an invalid or duplicate needs_human disposition")
		}
		reservation, ok := reservations[disposition.ReservationID]
		if !ok || reservation.Kind != ExecutionReservationAction || reservation.RunID != disposition.RunID {
			return errors.New("needs_human disposition does not refer to its ACTION reservation and run")
		}
		receipt, err := ExecutionReservationReceiptFor(reservation)
		if err != nil || receipt.ReservationDigest != disposition.ReservationDigest {
			return errors.New("needs_human disposition receipt does not match its ACTION reservation")
		}
		disposed[disposition.ReservationID] = true
		reservedAttempts[reservation.PlanNodeRef]--
	}
	if ledger.Revision != authorization.Revision+len(ledger.Reservations)+len(ledger.NeedsHumanDispositions)+len(ledger.ArtifactByteReservations) || ledger.RunsConsumed != runs ||
		ledger.StepsConsumed != steps || ledger.RecoveriesConsumed != recoveries {
		return errors.New("execution ledger totals do not match its reservations")
	}
	for node, attempts := range nodeAttempts {
		if attempts != reservedAttempts[node] {
			return errors.New("execution ledger node attempts do not match action reservations")
		}
	}
	if !validBoundDigest(ledger.Digest) {
		return errors.New("execution ledger digest is invalid")
	}
	expectedDigest, err := ledgerDigest(ledger)
	if err != nil || ledger.Digest != expectedDigest {
		return errors.New("execution ledger digest does not match its contents")
	}
	return nil
}

func validExecutionReservation(reservation ExecutionReservation) bool {
	if !validExecutionIdentifier(reservation.ID) || !validExecutionIdentifier(reservation.RunID) ||
		reservation.CreatedAt.IsZero() || reservation.Status != ExecutionReservationPrepared {
		return false
	}
	switch reservation.Kind {
	case ExecutionReservationRun, ExecutionReservationRecovery:
		return reservation.PlanNodeRef == ""
	case ExecutionReservationAction:
		return validExecutionIdentifier(reservation.PlanNodeRef)
	case ExecutionReservationStep:
		return reservation.PlanNodeRef == "" || validExecutionIdentifier(reservation.PlanNodeRef)
	default:
		return false
	}
}

func validExecutionArtifactByteReservation(reservation ExecutionArtifactByteReservation) bool {
	return validExecutionIdentifier(reservation.ID) && validExecutionIdentifier(reservation.RunID) &&
		reservation.Bytes > 0 && !reservation.CreatedAt.IsZero()
}

func validExecutionNeedsHumanDisposition(disposition ExecutionNeedsHumanDisposition) bool {
	return validExecutionIdentifier(disposition.RunID) &&
		validExecutionIdentifier(disposition.ReservationID) &&
		validBoundDigest(disposition.ReservationDigest)
}

// MigrateExecutionArtifactAccountingToUnknown applies the v15 -> v16
// representation change. Older snapshots have no trustworthy artifact-byte
// history, so they remain readable with accounting start revision zero. The
// new ledger serialization changes its digest, therefore its witness must be
// resealed atomically instead of treating an old digest as corruption.
func (s *State) MigrateExecutionArtifactAccountingToUnknown() error {
	for i := range s.Goals {
		execution := s.Goals[i].Execution
		if execution == nil {
			continue
		}
		if execution.Ledger.ArtifactAccountingStartRevision != 0 || execution.Ledger.ArtifactBytesConsumed != 0 || len(execution.Ledger.ArtifactByteReservations) != 0 {
			return fmt.Errorf("Goal %q already carries artifact-byte accounting", s.Goals[i].ID)
		}
		if len(execution.PlanBindings) == 0 || len(execution.Authorizations) == 0 {
			return fmt.Errorf("Goal %q has an incomplete execution authorization", s.Goals[i].ID)
		}
		if err := validateV15ExecutionIntegrity(*execution); err != nil {
			return fmt.Errorf("Goal %q has an invalid v15 execution aggregate: %w", s.Goals[i].ID, err)
		}
		if err := resealExecutionLedgerAndWitness(execution); err != nil {
			return err
		}
	}
	return nil
}

// v15ExecutionLedger is a wire-compatible copy of the ledger before
// artifact-byte accounting existed. Migration verifies this old seal before
// creating the v16 seal, so corrupt data is not silently laundered.
type v15ExecutionLedger struct {
	Revision               int                              `json:"revision"`
	AuthorizationRevision  int                              `json:"authorization_revision"`
	StepsConsumed          int                              `json:"steps_consumed"`
	RunsConsumed           int                              `json:"runs_consumed"`
	RecoveriesConsumed     int                              `json:"recoveries_consumed"`
	NodeAttempts           []ExecutionNodeAttempts          `json:"node_attempts"`
	Reservations           []ExecutionReservation           `json:"reservations,omitempty"`
	NeedsHumanDispositions []ExecutionNeedsHumanDisposition `json:"needs_human_dispositions,omitempty"`
	Digest                 string                           `json:"digest"`
}

func v15LedgerDigest(ledger ExecutionLedger) (string, error) {
	legacy := v15ExecutionLedger{Revision: ledger.Revision, AuthorizationRevision: ledger.AuthorizationRevision,
		StepsConsumed: ledger.StepsConsumed, RunsConsumed: ledger.RunsConsumed, RecoveriesConsumed: ledger.RecoveriesConsumed,
		NodeAttempts: ledger.NodeAttempts, Reservations: ledger.Reservations, NeedsHumanDispositions: ledger.NeedsHumanDispositions}
	return executionDigest("forgepilot.execution-ledger/v1", legacy)
}

func validateV15ExecutionIntegrity(execution GoalExecution) error {
	if !validBoundDigest(execution.Ledger.Digest) {
		return errors.New("execution ledger digest is invalid")
	}
	expectedLedger, err := v15LedgerDigest(execution.Ledger)
	if err != nil || execution.Ledger.Digest != expectedLedger {
		return errors.New("execution ledger digest does not match v15 contents")
	}
	binding := execution.PlanBindings[len(execution.PlanBindings)-1]
	authorization := execution.Authorizations[len(execution.Authorizations)-1]
	witness := execution.Witness
	if witness.GoalID != execution.GoalID || witness.Workspace != execution.Workspace ||
		witness.PlanBindingDigest != binding.Digest || witness.AuthorizationRevision != authorization.Revision ||
		witness.AuthorizationDigest != authorization.Digest || witness.LedgerRevision != execution.Ledger.Revision ||
		witness.LedgerDigest != execution.Ledger.Digest || !validBoundDigest(witness.Digest) {
		return errors.New("Goal execution witness does not cross-link v15 contents")
	}
	expectedWitness, err := witnessDigest(witness)
	if err != nil || witness.Digest != expectedWitness {
		return errors.New("Goal execution witness digest does not match v15 contents")
	}
	// Apply current structural validation on a copy with its v16-format digest.
	// The original is not mutated until both legacy seals are confirmed above.
	ledger := execution.Ledger
	ledger.Digest = ""
	currentDigest, err := ledgerDigest(ledger)
	if err != nil {
		return err
	}
	ledger.Digest = currentDigest
	return validateExecutionLedger(ledger, binding, authorization)
}

func resealExecutionLedgerAndWitness(execution *GoalExecution) error {
	execution.Ledger.Digest = ""
	digest, err := ledgerDigest(execution.Ledger)
	if err != nil {
		return err
	}
	execution.Ledger.Digest = digest
	if len(execution.PlanBindings) == 0 || len(execution.Authorizations) == 0 {
		return errors.New("execution has no current binding or authorization")
	}
	binding := execution.PlanBindings[len(execution.PlanBindings)-1]
	authorization := execution.Authorizations[len(execution.Authorizations)-1]
	execution.Witness.GoalID = execution.GoalID
	execution.Witness.Workspace = execution.Workspace
	execution.Witness.PlanBindingDigest = binding.Digest
	execution.Witness.AuthorizationRevision = authorization.Revision
	execution.Witness.AuthorizationDigest = authorization.Digest
	execution.Witness.LedgerRevision = execution.Ledger.Revision
	execution.Witness.LedgerDigest = execution.Ledger.Digest
	execution.Witness.Digest = ""
	witnessDigest, err := witnessDigest(execution.Witness)
	if err != nil {
		return err
	}
	execution.Witness.Digest = witnessDigest
	return nil
}

func validatePlanNodeRegistration(nodes []ExecutionPlanNodeBinding, goalID string, items map[string]Item) error {
	registered := make(map[string]Item)
	for id, item := range items {
		if item.GoalID == goalID {
			registered[id] = item
		}
	}
	if len(nodes) != len(registered) {
		return errors.New("Plan Node mapping must cover every registered Work Item exactly once")
	}
	nodeIDs := make(map[string]string, len(nodes))
	workItems := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		if !validExecutionIdentifier(node.PlanNodeRef) || node.StoryRef == "" ||
			!validArtifactPath(node.ReadinessContract.Path) || !validHexDigest(node.ReadinessContract.SHA256) ||
			node.WorkItemID == "" {
			return errors.New("Plan Node mapping contains an incomplete node or readiness contract")
		}
		if _, duplicate := nodeIDs[node.PlanNodeRef]; duplicate {
			return fmt.Errorf("duplicate Plan Node Reference %q", node.PlanNodeRef)
		}
		if workItems[node.WorkItemID] {
			return fmt.Errorf("Work Item %q is mapped from more than one Plan Node Reference", node.WorkItemID)
		}
		item, exists := registered[node.WorkItemID]
		if !exists {
			return fmt.Errorf("Plan Node %q maps to unknown or cross-Goal Work Item %q", node.PlanNodeRef, node.WorkItemID)
		}
		if item.StoryRef != node.StoryRef {
			return fmt.Errorf("Plan Node %q Story reference does not match Work Item %q", node.PlanNodeRef, item.ID)
		}
		nodeIDs[node.PlanNodeRef] = node.WorkItemID
		workItems[node.WorkItemID] = true
	}
	for _, node := range nodes {
		item := registered[node.WorkItemID]
		dependencies := make([]string, 0, len(node.DependsOn))
		seen := make(map[string]bool, len(node.DependsOn))
		for _, dependency := range node.DependsOn {
			workItemID, exists := nodeIDs[dependency]
			if !exists || dependency == node.PlanNodeRef || seen[dependency] {
				return fmt.Errorf("Plan Node %q has an invalid dependency reference %q", node.PlanNodeRef, dependency)
			}
			seen[dependency] = true
			dependencies = append(dependencies, workItemID)
		}
		if !sameStrings(dependencies, item.DependsOn) {
			return fmt.Errorf("Plan Node %q topology does not match Work Item %q dependencies", node.PlanNodeRef, item.ID)
		}
	}
	return nil
}

func bindingDigest(binding GoalPlanBinding) (string, error) {
	binding.Digest = ""
	return executionDigest("forgepilot.goal-plan-binding/v1", binding)
}

func authorizationDigest(authorization ExecutionAuthorization) (string, error) {
	authorization.Digest = ""
	return executionDigest("forgepilot.execution-authorization/v1", authorization)
}

func ledgerDigest(ledger ExecutionLedger) (string, error) {
	ledger.Digest = ""
	return executionDigest("forgepilot.execution-ledger/v1", ledger)
}

func witnessDigest(witness GoalExecutionWitness) (string, error) {
	witness.Digest = ""
	return executionDigest("forgepilot.goal-execution-witness/v1", witness)
}

func executionDigest(domain string, value any) (string, error) {
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

// ExecutionReservationReceiptFor hashes the normalized reservation value that
// the ledger actually stored. Later ledger charges do not invalidate this
// receipt because it is independent of the enclosing ledger digest.
func ExecutionReservationReceiptFor(reservation ExecutionReservation) (ExecutionReservationReceipt, error) {
	if !validExecutionReservation(reservation) {
		return ExecutionReservationReceipt{}, errors.New("execution reservation is incomplete or invalid")
	}
	digest, err := executionDigest("forgepilot.execution-reservation/v1", reservation)
	if err != nil {
		return ExecutionReservationReceipt{}, err
	}
	return ExecutionReservationReceipt{ReservationID: reservation.ID, ReservationDigest: digest}, nil
}

func validBoundDigest(value string) bool {
	return strings.HasPrefix(value, "sha256:") && validHexDigest(strings.TrimPrefix(value, "sha256:"))
}

func validHexDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func validExecutionEngineGeneration(generation ExecutionEngineGeneration) bool {
	if len(generation.SourceCommit) != 40 || strings.Trim(generation.SourceCommit, "0123456789abcdef") != "" {
		return false
	}
	return validBoundDigest(generation.PayloadSHA256)
}

func validArtifactPath(value string) bool {
	if value == "" || strings.Contains(value, "\\") || path.IsAbs(value) || path.Clean(value) != value {
		return false
	}
	return value != "." && value != ".." && !strings.HasPrefix(value, "../")
}

func validExecutionIdentifier(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && validExternalRef(value)
}

func cloneGoalExecution(execution GoalExecution) GoalExecution {
	clone := execution
	clone.PlanBindings = append([]GoalPlanBinding(nil), execution.PlanBindings...)
	for i := range clone.PlanBindings {
		clone.PlanBindings[i].ReviewedSources = append([]ExecutionArtifactBinding(nil), execution.PlanBindings[i].ReviewedSources...)
		clone.PlanBindings[i].Nodes = append([]ExecutionPlanNodeBinding(nil), execution.PlanBindings[i].Nodes...)
		for node := range clone.PlanBindings[i].Nodes {
			clone.PlanBindings[i].Nodes[node].DependsOn = append([]string(nil), execution.PlanBindings[i].Nodes[node].DependsOn...)
		}
	}
	clone.Authorizations = append([]ExecutionAuthorization(nil), execution.Authorizations...)
	for i := range clone.Authorizations {
		if identity := execution.Authorizations[i].WorkerIdentity; identity != nil {
			copied := *identity
			clone.Authorizations[i].WorkerIdentity = &copied
		}
		if generation := execution.Authorizations[i].EngineGeneration; generation != nil {
			copied := *generation
			clone.Authorizations[i].EngineGeneration = &copied
		}
	}
	clone.Ledger.NodeAttempts = append([]ExecutionNodeAttempts(nil), execution.Ledger.NodeAttempts...)
	clone.Ledger.ArtifactByteReservations = append([]ExecutionArtifactByteReservation(nil), execution.Ledger.ArtifactByteReservations...)
	clone.Ledger.Reservations = append([]ExecutionReservation(nil), execution.Ledger.Reservations...)
	clone.Ledger.NeedsHumanDispositions = append([]ExecutionNeedsHumanDisposition(nil), execution.Ledger.NeedsHumanDispositions...)
	return clone
}
