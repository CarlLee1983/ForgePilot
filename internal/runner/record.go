// Package runner drives one Goal through machine completion or a bounded stop.
// It holds execution history and nothing else: no Work Item lifecycle, no
// readiness of its own, no second opinion about what may happen next. Every
// step re-asks internal/app. See
// docs/adr/0019-runner-executes-forgepilot-decides.md.
package runner

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/agent"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// recordName is the one artifact recovery depends on. It is replaced
// atomically, so a reader sees a whole record or the previous whole record.
const recordName = "run.json"

// journalName is the append-only trail of what happened. It is diagnostic: a
// recovery never trusts it over durable ForgePilot state.
const journalName = "steps.jsonl"

// Budget is every limit one run obeys. None of them may be zero: a run with an
// unbounded budget is exactly the unattended process nobody can reason about.
type Budget struct {
	MaxSteps           int           `json:"max_steps"`
	MaxAttemptsPerWork int           `json:"max_attempts_per_work"`
	MaxDuration        time.Duration `json:"max_duration"`
	AgentTimeout       time.Duration `json:"agent_timeout"`
	VerifyTimeout      time.Duration `json:"verify_timeout"`
	MaxHandoffBytes    int           `json:"max_handoff_bytes"`
}

// Validate refuses a budget that cancels a limit. Zero is not "unlimited" here;
// it is a request this version does not grant.
func (budget Budget) Validate() error {
	limits := map[string]int64{
		"--max-steps":             int64(budget.MaxSteps),
		"--max-attempts-per-work": int64(budget.MaxAttemptsPerWork),
		"--max-duration":          int64(budget.MaxDuration),
		"--agent-timeout":         int64(budget.AgentTimeout),
		"--verify-timeout":        int64(budget.VerifyTimeout),
		"--max-handoff-bytes":     int64(budget.MaxHandoffBytes),
	}
	for name, value := range limits {
		if value <= 0 {
			return fmt.Errorf("%s must be positive; this version does not accept 0 as unlimited", name)
		}
	}
	return nil
}

// Worker records an agent session that was launched. It is written before the
// process starts (without an identity) and again immediately after (with one),
// so a crash in between still leaves evidence that something was launched.
type Worker struct {
	WorkItemID          string                             `json:"work_item_id"`
	Attempt             int                                `json:"attempt"`
	SessionDir          string                             `json:"session_dir"`
	StartedAt           time.Time                          `json:"started_at"`
	Identity            agent.ProcessIdentity              `json:"identity"`
	ReservationReceipts []work.ExecutionReservationReceipt `json:"reservation_receipts,omitempty"`
}

// The phases a pending execution can be in. They are distinguished because the
// answer to "may the next run start" is different for each: a record written
// before a launch cannot prove nothing was launched, and one written after a
// failed cleanup names a group that was observed alive.
const (
	// PhasePendingStart is written before an external process is started, so a
	// crash in the launch window still leaves evidence that something may exist.
	PhasePendingStart = "PENDING_START"
	// PhaseRunning is written once an identity could be observed.
	PhaseRunning = "RUNNING"
	// PhaseCleanupUnconfirmed is written when a group could not be confirmed gone.
	PhaseCleanupUnconfirmed = "CLEANUP_UNCONFIRMED"
)

// The kinds of external execution a Runner starts. Naming them is what makes a
// recovery record say which process is unaccounted for; before this only agent
// sessions were recorded at all, so a canonical check, a runtime probe or a Git
// child that outlived its Runner left nothing for the next process to find.
const (
	KindAgentSession       = "AGENT_SESSION"
	KindVerification       = "VERIFICATION"
	KindRuntimePreflight   = "RUNTIME_PREFLIGHT"
	KindCanonicalCheck     = "CANONICAL_CHECK"
	KindCanonicalPreflight = "CANONICAL_PREFLIGHT"
	KindGit                = "GIT"
)

const PurposeGoalCompletion = "GOAL_COMPLETION"

// PendingExecution is one external execution this run started whose cleanup has
// not been confirmed. It is the part of a run record that outlives the process
// that wrote it: `Stop` says why the last run ended and a resume clears it,
// which is exactly the wrong lifetime for "this workspace may still have a
// writer in it". See docs/adr/0022-pending-cleanup-outlives-the-process.md.
//
// Everything a later process needs to judge safety is a field, not prose.
// Detail carries the original words for a person; nothing reads it to decide.
type PendingExecution struct {
	// ID distinguishes two pending executions within one run.
	ID string `json:"id"`
	// Kind and Phase say what was started and how far it got.
	Kind  string `json:"kind"`
	Phase string `json:"phase"`
	// Purpose distinguishes the final facts read that may commit Goal completion.
	// Its durable marker lets recovery clear that exact read only when the Goal's
	// aggregate completion evidence proves the transaction returned successfully.
	Purpose string `json:"purpose,omitempty"`
	// ReservationReceipts link a charged worker Pending to its technical attempt
	// and Runner step in the durable ledger. Legacy Pending records omit them.
	ReservationReceipts []work.ExecutionReservationReceipt `json:"reservation_receipts,omitempty"`
	// WorkItemID is set when the execution belonged to one Work Item.
	WorkItemID string `json:"work_item_id,omitempty"`
	// Location is the checkout, worktree or session directory it worked in. It is
	// also why an unconfirmed group's worktree is not deleted: removing it would
	// destroy the thing this field points at.
	Location string `json:"location,omitempty"`
	// Identity is what was observed of the process group. An identity that is not
	// Recorded() is a fail-closed answer — "we could not tell" — never an absence.
	Identity agent.ProcessIdentity `json:"identity"`
	// Unresolved is the only field that decides anything. It is cleared in the
	// same atomic record replacement that records the confirmation.
	Unresolved bool `json:"unresolved"`
	// StopReason is why the execution was stopped, kept apart from why its
	// cleanup could not be confirmed: a cleanup failure must not erase the answer
	// to "was this a Ctrl-C or an expired run".
	StopReason string `json:"stop_reason,omitempty"`
	// CleanupDetail is the original cleanup report.
	CleanupDetail string    `json:"cleanup_detail,omitempty"`
	ObservedAt    time.Time `json:"observed_at"`
}

// Attempt is the bounded record of one agent session's claim. The summary is
// untrusted text written by a model: it is kept so the next session can be told
// what was already tried, and truncated so one verbose session cannot crowd out
// the briefing that follows it.
type Attempt struct {
	WorkItemID               string                            `json:"work_item_id"`
	Number                   int                               `json:"number"`
	Outcome                  string                            `json:"outcome"`
	Summary                  string                            `json:"summary"`
	At                       time.Time                         `json:"at"`
	ActionReservationReceipt *work.ExecutionReservationReceipt `json:"action_reservation_receipt,omitempty"`
}

// AttemptSummaryBytes bounds one stored attempt summary.
const AttemptSummaryBytes = 2000

// RetainedAttempts is how many earlier attempts on one Work Item are kept for
// the next handoff. Older ones are in the journal and in the session artifacts;
// what a briefing needs is the recent history, not all of it.
const RetainedAttempts = 5

// recordAttempt appends one bounded attempt summary and forgets the oldest.
func (record *Record) recordAttempt(attempt Attempt) {
	if len(attempt.Summary) > AttemptSummaryBytes {
		attempt.Summary = attempt.Summary[:AttemptSummaryBytes] + " …(truncated)"
	}
	record.History = append(record.History, attempt)
	kept := 0
	for index := len(record.History) - 1; index >= 0; index-- {
		if record.History[index].WorkItemID != attempt.WorkItemID {
			continue
		}
		kept++
		if kept > RetainedAttempts {
			record.History = append(record.History[:index], record.History[index+1:]...)
		}
	}
}

// AttemptsFor lists the retained attempts on one Work Item, oldest first.
func (record *Record) AttemptsFor(workItemID string) []Attempt {
	var attempts []Attempt
	for _, attempt := range record.History {
		if attempt.WorkItemID == workItemID {
			attempts = append(attempts, attempt)
		}
	}
	return attempts
}

// Stop is why a run ended.
type Stop struct {
	Reason StopReason `json:"reason"`
	Detail string     `json:"detail"`
	At     time.Time  `json:"at"`
	// EvidenceIDs holds the exact Verification Evidence a final-review wait or
	// automatic completion was judged on. Automatic completion prefixes the
	// aggregate Goal completion evidence ID.
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}

// RunPreparationState makes a new Run Record nonrunnable until its durable RUN
// reservation has either been confirmed or legacy admission has been decided.
// Empty is the historical/ready value; it is written only for a fully prepared
// charged run or a legacy run that needs no authorization charge.
type RunPreparationState string

const (
	RunPreparationPendingClassification RunPreparationState = "PENDING_CLASSIFICATION"
	RunPreparationPendingCharge         RunPreparationState = "PENDING_CHARGE"
)

// Record is one run's durable execution history.
type Record struct {
	RunID             string                `json:"run_id"`
	Workspace         string                `json:"workspace"`
	GoalID            string                `json:"goal_id"`
	GoalTitle         string                `json:"goal_title"`
	CompletionPolicy  work.CompletionPolicy `json:"completion_policy,omitempty"`
	Scope             []string              `json:"scope"`
	RuntimeName       string                `json:"runtime_name"`
	RuntimeExecutable string                `json:"runtime_executable"`
	RuntimeVersion    string                `json:"runtime_version"`
	RuntimeCommand    string                `json:"runtime_command,omitempty"`
	// ExecutionAuthorizationDigest and RunReservationID bind a charged run to
	// the ledger entry that paid for it. They are not counters: ledger remains
	// the sole cross-run budget authority.
	ExecutionAuthorizationDigest string                             `json:"execution_authorization_digest,omitempty"`
	RunReservationID             string                             `json:"run_reservation_id,omitempty"`
	ReservationReceipts          []work.ExecutionReservationReceipt `json:"reservation_receipts,omitempty"`
	// RunPreparationState keeps an initial record nonrunnable across the
	// record/ledger transaction boundary. A retry must finish this exact intent
	// before it can create another run or launch a worker.
	RunPreparationState     RunPreparationState    `json:"run_preparation_state,omitempty"`
	Snapshot                bool                   `json:"snapshot"`
	Budget                  Budget                 `json:"budget"`
	Limits                  storage.ArtifactLimits `json:"limits"`
	StartedAt               time.Time              `json:"started_at"`
	Deadline                time.Time              `json:"deadline"`
	UpdatedAt               time.Time              `json:"updated_at"`
	Steps                   int                    `json:"steps"`
	Attempts                map[string]int         `json:"attempts"`
	HumanWaits              map[string]int         `json:"human_waits,omitempty"`
	HumanWaitReservationIDs []string               `json:"human_wait_reservation_ids,omitempty"`
	Worker                  *Worker                `json:"worker,omitempty"`
	// Pending holds executions whose cleanup has not been confirmed. It is
	// additive: a record written before this field existed simply has none, which
	// is read as "this run recorded nothing beyond its worker", not as "this
	// workspace is known to be clear".
	Pending []PendingExecution `json:"pending,omitempty"`
	// PendingSeq only ever increases. Naming entries by the current length reused
	// an id as soon as one was resolved, and resolvePending removes by id — so a
	// later resolve cleared an entry that had never been confirmed, which is the
	// one thing this record exists to prevent.
	PendingSeq  int       `json:"pending_seq,omitempty"`
	History     []Attempt `json:"history,omitempty"`
	EvidenceIDs []string  `json:"evidence_ids,omitempty"`
	Stop        *Stop     `json:"stop,omitempty"`
}

// Entry is one line of the journal.
type Entry struct {
	At         time.Time `json:"at"`
	Step       int       `json:"step"`
	Action     string    `json:"action"`
	WorkItemID string    `json:"work_item_id,omitempty"`
	Detail     string    `json:"detail,omitempty"`
}

// NewRunID names a run readably and uniquely. The time prefix makes the
// directory listing chronological; the random suffix keeps two runs started in
// the same second apart.
func NewRunID(at time.Time) (string, error) {
	suffix := make([]byte, 3)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	return fmt.Sprintf("run-%s-%s", at.UTC().Format("20060102t150405"), hex.EncodeToString(suffix)), nil
}

// save replaces the run record atomically. A failure here stops the run: a
// worker must never be launched while there is no durable record of it.
func (record *Record) save(root string, limits storage.ArtifactLimits, at time.Time) error {
	record.UpdatedAt = at.UTC()
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	// None of the three bounds apply here. They are ceilings on what an agent
	// session may emit; this file is ForgePilot's own account of what it
	// launched, and its size is already bounded by its structure — retained
	// attempts times their truncated summaries, plus one entry per Work Item.
	// Refusing to write it leaves a launched worker with no recoverable record,
	// which is the one thing recovery cannot survive, and the refusal arrives
	// precisely when the workspace is fullest — so the caller's only way out
	// would be to drop something from the record to make it fit, which is how a
	// live worker stops being recorded at all. The bounds still govern every
	// artifact a session writes, which is what they were written for.
	limits = storage.ArtifactLimits{}
	return storage.WriteRunArtifact(root, record.RunID, recordName, append(encoded, '\n'), limits)
}

// LoadRecord reads one run's history back.
func LoadRecord(root, runID string) (Record, error) {
	contents, err := storage.ReadRunArtifact(root, runID, recordName)
	if err != nil {
		return Record{}, fmt.Errorf("read run %s: %w", runID, err)
	}
	var record Record
	if err := json.Unmarshal(contents, &record); err != nil {
		return Record{}, fmt.Errorf("read run %s: %w", runID, err)
	}
	if record.Attempts == nil {
		record.Attempts = map[string]int{}
	}
	if record.HumanWaits == nil {
		record.HumanWaits = map[string]int{}
	}
	for workItemID, attempts := range record.Attempts {
		if attempts < 0 {
			return Record{}, fmt.Errorf("read run %s: %s has negative attempts", runID, workItemID)
		}
	}
	for workItemID, waits := range record.HumanWaits {
		attempts, known := record.Attempts[workItemID]
		if waits < 0 || !known || waits > attempts {
			return Record{}, fmt.Errorf("read run %s: %s has impossible human-wait accounting", runID, workItemID)
		}
	}
	return record, nil
}

// technicalAttemptsFor reports the launched sessions that consumed technical
// retry budget. Old records have no HumanWaits entry, so every historical
// session remains charged rather than being reclassified from partial history.
func (record *Record) technicalAttemptsFor(workItemID string) int {
	attempts := record.Attempts[workItemID]
	waits := record.HumanWaits[workItemID]
	return attempts - waits
}

func (record *Record) recordHumanWait(workItemID string) {
	if record.HumanWaits == nil {
		record.HumanWaits = map[string]int{}
	}
	if record.HumanWaits[workItemID] < record.Attempts[workItemID] {
		record.HumanWaits[workItemID]++
	}
}

func (record *Record) journal(root string, limits storage.ArtifactLimits, entry Entry) error {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return storage.AppendRunArtifact(root, record.RunID, journalName, append(encoded, '\n'), limits)
}

// addPending records an execution before it is started. The caller must persist
// the record before launching anything: a process with no durable record of it
// is the one thing recovery cannot survive.
// See docs/adr/0020-worker-ownership-is-fail-closed.md.
func (record *Record) addPending(pending PendingExecution) string {
	if pending.ID == "" {
		record.PendingSeq++
		// An older record may carry entries but no counter, so the counter is
		// lifted clear of any id already present before it is used.
		for taken := true; taken; {
			taken = false
			candidate := fmt.Sprintf("pe-%d", record.PendingSeq)
			for _, existing := range record.Pending {
				if existing.ID == candidate {
					record.PendingSeq++
					taken = true
					break
				}
			}
		}
		pending.ID = fmt.Sprintf("pe-%d", record.PendingSeq)
	}
	pending.Unresolved = true
	record.Pending = append(record.Pending, pending)
	return pending.ID
}

// updatePending applies a change to one recorded execution.
func (record *Record) updatePending(id string, apply func(*PendingExecution)) {
	for index := range record.Pending {
		if record.Pending[index].ID == id {
			apply(&record.Pending[index])
			return
		}
	}
}

// resolvePending drops one execution from the record. It is called only after
// the group behind it was confirmed gone, so that the clearing and the
// confirmation land in the same atomic replacement.
func (record *Record) resolvePending(id string) {
	kept := record.Pending[:0]
	for _, pending := range record.Pending {
		if pending.ID != id {
			kept = append(kept, pending)
		}
	}
	record.Pending = kept
	if len(record.Pending) == 0 {
		record.Pending = nil
	}
}

// UnresolvedPending lists the executions this record still cannot account for.
func (record *Record) UnresolvedPending() []PendingExecution {
	var unresolved []PendingExecution
	for _, pending := range record.Pending {
		if pending.Unresolved {
			unresolved = append(unresolved, pending)
		}
	}
	return unresolved
}
