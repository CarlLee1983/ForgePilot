// Package runner drives one Goal to the point where a person must look at it.
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
	WorkItemID string                `json:"work_item_id"`
	Attempt    int                   `json:"attempt"`
	SessionDir string                `json:"session_dir"`
	StartedAt  time.Time             `json:"started_at"`
	Identity   agent.ProcessIdentity `json:"identity"`
}

// Attempt is the bounded record of one agent session's claim. The summary is
// untrusted text written by a model: it is kept so the next session can be told
// what was already tried, and truncated so one verbose session cannot crowd out
// the briefing that follows it.
type Attempt struct {
	WorkItemID string    `json:"work_item_id"`
	Number     int       `json:"number"`
	Outcome    string    `json:"outcome"`
	Summary    string    `json:"summary"`
	At         time.Time `json:"at"`
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
	// EvidenceIDs holds the exact Verification Evidence a final-review wait was
	// judged on, so a person reviewing later sees what the machine saw.
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}

// Record is one run's durable execution history.
type Record struct {
	RunID             string                 `json:"run_id"`
	Workspace         string                 `json:"workspace"`
	GoalID            string                 `json:"goal_id"`
	GoalTitle         string                 `json:"goal_title"`
	Scope             []string               `json:"scope"`
	RuntimeName       string                 `json:"runtime_name"`
	RuntimeExecutable string                 `json:"runtime_executable"`
	RuntimeVersion    string                 `json:"runtime_version"`
	RuntimeCommand    string                 `json:"runtime_command,omitempty"`
	Snapshot          bool                   `json:"snapshot"`
	Budget            Budget                 `json:"budget"`
	Limits            storage.ArtifactLimits `json:"limits"`
	StartedAt         time.Time              `json:"started_at"`
	Deadline          time.Time              `json:"deadline"`
	UpdatedAt         time.Time              `json:"updated_at"`
	Steps             int                    `json:"steps"`
	Attempts          map[string]int         `json:"attempts"`
	Worker            *Worker                `json:"worker,omitempty"`
	History           []Attempt              `json:"history,omitempty"`
	EvidenceIDs       []string               `json:"evidence_ids,omitempty"`
	Stop              *Stop                  `json:"stop,omitempty"`
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
	return record, nil
}

func (record *Record) journal(root string, limits storage.ArtifactLimits, entry Entry) error {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return storage.AppendRunArtifact(root, record.RunID, journalName, append(encoded, '\n'), limits)
}
