package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/runner"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
)

// exitStatus carries a run's outcome out through the error return without
// pretending it is a failure. A run that stops for a Gate has not errored; it
// has finished doing what it may legally do, and the exit code says which.
type exitStatus struct {
	code int
	err  error
}

func (status *exitStatus) Error() string {
	if status.err != nil {
		return status.err.Error()
	}
	return fmt.Sprintf("exit status %d", status.code)
}

func (status *exitStatus) Unwrap() error { return status.err }

const runUsage = "usage: forgepilot run --goal <goal-id> --runtime <codex|fake> --snapshot [--dry-run] [limits]\n" +
	"       forgepilot run status <run-id> [--json]\n" +
	"       forgepilot run resume <run-id>"

// defaults are the documented budget. They are deliberately finite: an
// unattended process with no ceiling is the thing nobody can reason about.
func defaultBudget() runner.Budget {
	return runner.Budget{
		MaxSteps:           100,
		MaxAttemptsPerWork: 3,
		MaxDuration:        8 * time.Hour,
		AgentTimeout:       30 * time.Minute,
		VerifyTimeout:      30 * time.Minute,
		MaxHandoffBytes:    64 * 1024,
	}
}

func defaultLimits() storage.ArtifactLimits {
	return storage.ArtifactLimits{
		MaxWriteBytes: 1 << 20,
		MaxRunBytes:   16 << 20,
		MaxTotalBytes: 128 << 20,
	}
}

func runCommand(args []string, root string, output io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "status":
			return runStatus(args[1:], root, output)
		case "resume":
			return runResume(args[1:], root, output)
		}
	}
	options, dryRun, err := parseRunOptions(args, root, output)
	if err != nil {
		return err
	}
	if dryRun {
		return printPlan(options, output)
	}
	stop, signalled, release := signalStop()
	defer release()
	options.Stop, options.Signalled = stop, signalled
	record, err := runner.Start(options)
	return reportRun(record, err, output)
}

func runResume(args []string, root string, output io.Writer) error {
	if len(args) != 1 || strings.HasPrefix(args[0], "--") {
		return errors.New("usage: forgepilot run resume <run-id>")
	}
	stop, signalled, release := signalStop()
	defer release()
	record, err := runner.Resume(runner.Options{
		Root: root, Output: output, Now: now, Stop: stop, Signalled: signalled, Limits: defaultLimits(),
	}, args[0])
	return reportRun(record, err, output)
}

// reportRun turns a finished run into an exit code. An operational error is
// still exit 1; everything else is the stop reason's own documented code.
func reportRun(record runner.Record, err error, output io.Writer) error {
	if err != nil {
		return err
	}
	if record.Stop == nil {
		return errors.New("the run ended without recording why")
	}
	if _, err := fmt.Fprintf(output, "Run %s stopped: %s\n", record.RunID, record.Stop.Reason); err != nil {
		return err
	}
	return &exitStatus{code: record.Stop.Reason.ExitCode()}
}

// signalStop closes a channel on SIGINT or SIGTERM so the run can stop its
// worker's process group and save recovery information before exiting. Which
// of the two arrived is reported alongside: both end the run identically, but
// a shell reads 130 for one and 143 for the other.
func signalStop() (<-chan struct{}, func() runner.StopReason, func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	stop := make(chan struct{})
	done := make(chan struct{})
	// Written once before stop is closed and read only after, so the close is
	// the handover: no reader can see it before the writer is finished with it.
	reason := runner.StopInterrupted
	go func() {
		select {
		case received := <-signals:
			if received == syscall.SIGTERM {
				reason = runner.StopTerminated
			}
			close(stop)
		case <-done:
		}
	}()
	return stop, func() runner.StopReason { return reason }, func() {
		signal.Stop(signals)
		close(done)
	}
}

func printPlan(options runner.Options, output io.Writer) error {
	plan, err := runner.DryRun(options)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(output,
		"Dry run for goal %s (%s)\nReview policy: %s\nWork items in scope: %d\nRuntime: %s %s (%s)\n"+
			"Budget: %d steps, %d attempts per work item, %s duration, %s agent timeout, %s verify timeout\n"+
			"Candidate mode: SNAPSHOT\nFirst action: %s %s\nReason: %s\n"+
			"Nothing was started, reconciled, verified or recorded.\n",
		plan.Goal.ID, plan.Goal.Title, plan.Goal.ReviewPolicy, len(plan.Scope),
		plan.RuntimeName, plan.RuntimeVersion, plan.RuntimeExecutable,
		plan.Budget.MaxSteps, plan.Budget.MaxAttemptsPerWork, plan.Budget.MaxDuration,
		plan.Budget.AgentTimeout, plan.Budget.VerifyTimeout,
		plan.Decision.Action.Kind, plan.Decision.Action.Item.ID, plan.Decision.Action.Reason)
	return err
}

// runStatusView separates what a run concluded when it stopped from what the
// Goal's readiness is now. They are different questions: the workspace can move
// after a run ends, and a stored conclusion must never be reported as current.
type runStatusView struct {
	RunID          string         `json:"run_id"`
	Workspace      string         `json:"workspace"`
	GoalID         string         `json:"goal_id"`
	Runtime        string         `json:"runtime"`
	RuntimeVersion string         `json:"runtime_version"`
	StartedAt      string         `json:"started_at"`
	Deadline       string         `json:"deadline"`
	Steps          int            `json:"steps"`
	Attempts       map[string]int `json:"attempts"`
	EvidenceIDs    []string       `json:"evidence_ids,omitempty"`
	StopReason     string         `json:"stop_reason"`
	StopDetail     string         `json:"stop_detail"`
	StopAt         string         `json:"stop_at"`
	ExitCode       int            `json:"exit_code"`
	WorkerPending  bool           `json:"worker_pending"`
	CurrentGoal    string         `json:"current_goal_readiness"`
	CurrentError   string         `json:"current_goal_readiness_error,omitempty"`
	ScopeChanged   bool           `json:"scope_changed"`
	ScopeError     string         `json:"scope_error,omitempty"`
}

func runStatus(args []string, root string, output io.Writer) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		return errors.New("usage: forgepilot run status <run-id> [--json]")
	}
	asJSON := false
	if len(args) == 2 && args[1] == "--json" {
		asJSON = true
	} else if len(args) != 1 {
		return errors.New("usage: forgepilot run status <run-id> [--json]")
	}
	record, err := runner.LoadRecord(root, args[0])
	if err != nil {
		return err
	}
	view := runStatusView{
		RunID: record.RunID, Workspace: record.Workspace, GoalID: record.GoalID,
		Runtime: record.RuntimeName, RuntimeVersion: record.RuntimeVersion,
		StartedAt: record.StartedAt.Format(time.RFC3339), Deadline: record.Deadline.Format(time.RFC3339),
		Steps: record.Steps, Attempts: record.Attempts, EvidenceIDs: record.EvidenceIDs,
		StopReason: "still running or never finished", ExitCode: -1,
		WorkerPending: record.Worker != nil,
	}
	if record.Stop != nil {
		view.StopReason = string(record.Stop.Reason)
		view.StopDetail = record.Stop.Detail
		view.StopAt = record.Stop.At.Format(time.RFC3339)
		view.ExitCode = record.Stop.Reason.ExitCode()
		if len(record.Stop.EvidenceIDs) > 0 {
			view.EvidenceIDs = record.Stop.EvidenceIDs
		}
	}
	if summary, readinessErr := app.GoalReadiness(root, record.GoalID); readinessErr != nil {
		view.CurrentGoal, view.CurrentError = "unknown", readinessErr.Error()
	} else {
		view.CurrentGoal = string(summary.Completion)
	}
	// "Did the scope move?" is the one question this view exists to answer, so a
	// failure to recompute it is reported rather than rendered as "no".
	if scope, scopeErr := app.CurrentGoalScope(root, record.GoalID); scopeErr != nil {
		view.ScopeError = scopeErr.Error()
	} else {
		view.ScopeChanged = strings.Join(scope, "\n") != strings.Join(record.Scope, "\n")
	}
	if asJSON {
		encoded, err := json.MarshalIndent(view, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(output, "%s\n", encoded)
		return err
	}
	_, err = fmt.Fprintf(output,
		"Run %s\nWorkspace: %s\nGoal: %s\nRuntime: %s %s\nStarted: %s\nDeadline: %s\nSteps: %d\n"+
			"When it stopped: %s\nReason: %s\nExit code: %d\nEvidence: %s\n"+
			"Now (recomputed): goal %s is %s\nScope changed since the run started: %s\n",
		view.RunID, view.Workspace, view.GoalID, view.Runtime, view.RuntimeVersion,
		view.StartedAt, view.Deadline, view.Steps,
		view.StopReason, orNone(view.StopDetail), view.ExitCode, orNone(strings.Join(view.EvidenceIDs, ", ")),
		view.GoalID, view.CurrentGoal, scopeAnswer(view))
	return err
}

// scopeAnswer renders the scope comparison, including the case where it could
// not be made.
func scopeAnswer(view runStatusView) string {
	if view.ScopeError != "" {
		return "unknown (" + view.ScopeError + ")"
	}
	return fmt.Sprintf("%t", view.ScopeChanged)
}

func orNone(value string) string {
	if value == "" {
		return "none"
	}
	return value
}

// parseRunOptions reads the run flags. --snapshot has no default: this version
// requires it to be stated, so nobody discovers after the fact that their
// working tree was committed or that HEAD was verified instead.
func parseRunOptions(args []string, root string, output io.Writer) (runner.Options, bool, error) {
	options := runner.Options{Root: root, Output: output, Now: now, Budget: defaultBudget(), Limits: defaultLimits()}
	snapshot, dryRun := false, false
	for index := 0; index < len(args); index++ {
		name := args[index]
		if !strings.HasPrefix(name, "--") {
			return options, false, fmt.Errorf("unexpected argument %q\n%s", name, runUsage)
		}
		switch name {
		case "--snapshot":
			snapshot = true
			continue
		case "--dry-run":
			dryRun = true
			continue
		}
		index++
		if index >= len(args) || strings.HasPrefix(args[index], "--") {
			return options, false, fmt.Errorf("%s requires a value", name)
		}
		value := args[index]
		var err error
		switch name {
		case "--goal":
			options.GoalID = value
		case "--runtime":
			options.RuntimeName = value
		case "--runtime-command":
			options.RuntimeCommand = value
		case "--max-steps":
			options.Budget.MaxSteps, err = strconv.Atoi(value)
		case "--max-attempts-per-work":
			options.Budget.MaxAttemptsPerWork, err = strconv.Atoi(value)
		case "--max-duration":
			options.Budget.MaxDuration, err = time.ParseDuration(value)
		case "--agent-timeout":
			options.Budget.AgentTimeout, err = time.ParseDuration(value)
		case "--verify-timeout":
			options.Budget.VerifyTimeout, err = time.ParseDuration(value)
		case "--max-handoff-bytes":
			options.Budget.MaxHandoffBytes, err = strconv.Atoi(value)
		case "--max-agent-output-bytes":
			options.Limits.MaxWriteBytes, err = parseSize(value)
		case "--max-run-bytes":
			options.Limits.MaxRunBytes, err = parseSize(value)
		case "--max-runs-bytes":
			options.Limits.MaxTotalBytes, err = parseSize(value)
		default:
			return options, false, fmt.Errorf("unknown flag %s\n%s", name, runUsage)
		}
		if err != nil {
			return options, false, fmt.Errorf("%s: %w", name, err)
		}
	}
	if options.GoalID == "" || options.RuntimeName == "" {
		return options, false, errors.New(runUsage)
	}
	if !snapshot {
		return options, false, errors.New("--snapshot is required: this version verifies a working-tree snapshot and never commits for you")
	}
	options.Snapshot = true
	return options, dryRun, nil
}

func parseSize(value string) (int64, error) { return strconv.ParseInt(value, 10, 64) }
