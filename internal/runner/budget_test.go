package runner

import (
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
)

// Zero is a request this version does not grant. Accepting it as "unlimited"
// would turn every documented ceiling into a suggestion, which is exactly the
// unattended process nobody can reason about.
func TestBudgetRefusesAnyCancelledLimit(t *testing.T) {
	valid := Budget{MaxSteps: 100, MaxAttemptsPerWork: 3, MaxDuration: 8 * time.Hour,
		AgentTimeout: 30 * time.Minute, VerifyTimeout: 30 * time.Minute, MaxHandoffBytes: 65536}
	if err := valid.Validate(); err != nil {
		t.Fatalf("a fully specified budget was refused: %v", err)
	}
	cancelled := map[string]func(*Budget){
		"--max-steps":             func(budget *Budget) { budget.MaxSteps = 0 },
		"--max-attempts-per-work": func(budget *Budget) { budget.MaxAttemptsPerWork = 0 },
		"--max-duration":          func(budget *Budget) { budget.MaxDuration = 0 },
		"--agent-timeout":         func(budget *Budget) { budget.AgentTimeout = 0 },
		"--verify-timeout":        func(budget *Budget) { budget.VerifyTimeout = 0 },
		"--max-handoff-bytes":     func(budget *Budget) { budget.MaxHandoffBytes = 0 },
	}
	for name, cancel := range cancelled {
		budget := valid
		cancel(&budget)
		err := budget.Validate()
		if err == nil {
			t.Fatalf("%s = 0 was accepted", name)
		}
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("%s = 0 was refused without naming the flag: %v", name, err)
		}
	}
	negative := valid
	negative.MaxSteps = -1
	if negative.Validate() == nil {
		t.Fatal("a negative limit was accepted")
	}
}

// The exit code is the one thing a script reads. Awaiting review is the only
// zero, and anything unclassified must land on an error rather than be read as
// success by default.
func TestExitCodesSeparateReviewFromEveryOtherEnding(t *testing.T) {
	if StopAwaitingGoalReview.ExitCode() != ExitAwaitingReview {
		t.Fatal("the review boundary is not exit 0")
	}
	for _, reason := range []StopReason{StopWaitGate, StopWaitGoal, StopWaitHumanReview, StopNeedsHuman,
		StopAgentExecutionFailed, StopVerificationRefused, StopVerificationInFlight, StopScopeChanged,
		StopRecoveryBlocked, StopStalled} {
		if got := reason.ExitCode(); got != ExitNeedsHuman {
			t.Fatalf("%s = %d, want %d", reason, got, ExitNeedsHuman)
		}
	}
	for _, reason := range []StopReason{StopMaxSteps, StopMaxAttempts, StopMaxDuration, StopAgentTimeout,
		StopVerifyTimeout, StopNoProgress, StopCapacityExceeded} {
		if got := reason.ExitCode(); got != ExitLimit {
			t.Fatalf("%s = %d, want %d", reason, got, ExitLimit)
		}
	}
	if StopInterrupted.ExitCode() != ExitInterrupted {
		t.Fatal("a signal does not use the signal convention")
	}
	if StopRuntimeProtocol.ExitCode() != ExitError {
		t.Fatal("a broken runtime protocol is not an execution error")
	}
	if StopReason("SOMETHING_ADDED_WITHOUT_A_DECISION").ExitCode() != ExitError {
		t.Fatal("an unclassified stop reason does not default to an error")
	}
}

// Attempt summaries are untrusted model text kept only so the next session can
// be told what was already tried. One verbose session must not be able to crowd
// out the briefing that follows it, or the history of the other Work Items.
func TestAttemptHistoryIsBoundedPerWorkItem(t *testing.T) {
	record := &Record{Attempts: map[string]int{}}
	for number := 1; number <= RetainedAttempts+4; number++ {
		record.recordAttempt(Attempt{WorkItemID: "WI-001", Number: number, Outcome: "implementation_finished",
			Summary: strings.Repeat("x", AttemptSummaryBytes*2)})
	}
	record.recordAttempt(Attempt{WorkItemID: "WI-002", Number: 1, Outcome: "needs_human", Summary: "a question"})

	kept := record.AttemptsFor("WI-001")
	if len(kept) != RetainedAttempts {
		t.Fatalf("kept %d attempts, want %d", len(kept), RetainedAttempts)
	}
	// The oldest are dropped, not the newest: what the next session needs is the
	// recent history.
	if kept[0].Number != 5 || kept[len(kept)-1].Number != RetainedAttempts+4 {
		t.Fatalf("kept attempts %d..%d", kept[0].Number, kept[len(kept)-1].Number)
	}
	for _, attempt := range kept {
		if len(attempt.Summary) > AttemptSummaryBytes+len(" …(truncated)") {
			t.Fatalf("attempt %d summary is %d bytes", attempt.Number, len(attempt.Summary))
		}
		if !strings.HasSuffix(attempt.Summary, "(truncated)") {
			t.Fatal("an oversized summary was cut without saying so")
		}
	}
	if other := record.AttemptsFor("WI-002"); len(other) != 1 {
		t.Fatalf("another work item's history was evicted: %v", other)
	}
}

// Artifact limits are ceilings a long unattended run depends on. Zero reads as
// "unchecked" everywhere they are applied, so accepting it from a flag would
// switch those ceilings off entirely.
func TestArtifactLimitsRefuseAnyCancelledBound(t *testing.T) {
	valid := storage.ArtifactLimits{MaxWriteBytes: 1 << 20, MaxRunBytes: 16 << 20, MaxTotalBytes: 128 << 20}
	if err := valid.Validate(); err != nil {
		t.Fatalf("a fully specified set of limits was refused: %v", err)
	}
	cancelled := map[string]storage.ArtifactLimits{
		"--max-agent-output-bytes": {MaxWriteBytes: 0, MaxRunBytes: 16 << 20, MaxTotalBytes: 128 << 20},
		"--max-run-bytes":          {MaxWriteBytes: 1 << 20, MaxRunBytes: 0, MaxTotalBytes: 128 << 20},
		"--max-runs-bytes":         {MaxWriteBytes: 1 << 20, MaxRunBytes: 16 << 20, MaxTotalBytes: 0},
	}
	for name, limits := range cancelled {
		err := limits.Validate()
		if err == nil {
			t.Fatalf("%s = 0 was accepted", name)
		}
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("%s = 0 was refused without naming the flag: %v", name, err)
		}
	}
	// A bound that is tighter than the one inside it can never be satisfied, so
	// it is a configuration error rather than a very strict limit.
	inverted := storage.ArtifactLimits{MaxWriteBytes: 16 << 20, MaxRunBytes: 1 << 20, MaxTotalBytes: 128 << 20}
	if inverted.Validate() == nil {
		t.Fatal("limits that do not widen outwards were accepted")
	}
}

// A resume inherits what the run was started with. Silently restoring the
// defaults would stop a healthy run for capacity it was never short of.
func TestResumeInheritsTheLimitsTheRunStartedWith(t *testing.T) {
	record := Record{
		GoalID: "g", RuntimeName: "fake", Snapshot: true,
		Budget: Budget{MaxSteps: 7, MaxAttemptsPerWork: 2, MaxDuration: time.Hour,
			AgentTimeout: time.Minute, VerifyTimeout: time.Minute, MaxHandoffBytes: 1024},
		Limits: storage.ArtifactLimits{MaxWriteBytes: 11, MaxRunBytes: 22, MaxTotalBytes: 33},
	}
	resumed := resumeOptions(Options{
		GoalID: "someone-elses-goal", RuntimeName: "codex", Snapshot: false,
		Budget: Budget{MaxSteps: 1000},
		Limits: storage.ArtifactLimits{MaxWriteBytes: 1, MaxRunBytes: 2, MaxTotalBytes: 3},
	}, record)

	if resumed.GoalID != "g" || resumed.RuntimeName != "fake" || !resumed.Snapshot {
		t.Fatalf("resume switched the work it was driving: %#v", resumed)
	}
	if resumed.Budget != record.Budget {
		t.Fatalf("budget = %#v, want the run's own", resumed.Budget)
	}
	if resumed.Limits != record.Limits {
		t.Fatalf("limits = %#v, want the run's own", resumed.Limits)
	}
}

// The three artifact bounds exist to stop an agent session from filling the
// disk. The run record is not agent output — it is ForgePilot's own account of
// what it launched, and a workspace that has run out of room for it has already
// lost the only thing recovery cannot do without.
func TestTheRunRecordIsNotSubjectToTheArtifactBounds(t *testing.T) {
	root := t.TempDir()
	runID, err := NewRunID(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	record := &Record{RunID: runID, GoalID: "queue", Workspace: root, Attempts: map[string]int{},
		Worker: &Worker{WorkItemID: "WI-001", Attempt: 1, SessionDir: root, StartedAt: time.Now().UTC()}}
	// Every bound set as low as it can go without being refused outright.
	limits := storage.ArtifactLimits{MaxWriteBytes: 1, MaxRunBytes: 1, MaxTotalBytes: 1}

	if err := record.save(root, limits, time.Now()); err != nil {
		t.Fatalf("a launched worker could not be recorded: %v", err)
	}
	stored, err := LoadRecord(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Worker == nil || stored.Worker.WorkItemID != "WI-001" {
		t.Fatalf("the record came back without the worker it was saving: %+v", stored.Worker)
	}
	// The bounds still govern what a session writes.
	if err := storage.WriteRunArtifact(root, runID, "session.log", []byte("agent output"), limits); err == nil {
		t.Fatal("an agent artifact was written past every bound")
	}
}
