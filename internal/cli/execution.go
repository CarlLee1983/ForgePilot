package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/control"
	"github.com/CarlLee1983/ForgePilot/internal/runner"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

const (
	executionPlanUsage            = "usage: forgepilot execution plan --request <path> --json"
	executionAuthorizeUsage       = "usage: forgepilot execution authorize --request <path> --approval-token <token> --by <name> --json"
	executionRevisePlanUsage      = "usage: forgepilot execution revise plan --request <path> --json"
	executionReviseAuthorizeUsage = "usage: forgepilot execution revise authorize --request <path> --approval-token <token> --by <name> --json"
	executionResumeUsage          = "usage: forgepilot execution resume --goal <goal-id> [--json]"
	executionStopUsage            = "usage: forgepilot execution stop --goal <goal-id> --by <name> --reason <reason> [--json]"
	executionDeclareUsage         = "usage: forgepilot execution declare --request <path> --json"
	executionRetentionUsage       = "usage: forgepilot execution retention reconcile --json"
)

func executionCommand(args []string, root string, output io.Writer, resolver app.EngineGenerationResolver) error {
	if len(args) == 0 {
		return errors.New("usage: forgepilot execution <plan|authorize|revise|retention|resume|stop|declare>")
	}
	switch args[0] {
	case "plan":
		return planExecution(args[1:], root, output)
	case "authorize":
		return authorizeExecution(args[1:], root, output, resolver)
	case "revise":
		return reviseExecution(args[1:], root, output, resolver)
	case "retention":
		return reconcileExecutionRetention(args[1:], root, output, resolver)
	case "resume":
		return resumeExecution(args[1:], root, output, resolver)
	case "stop":
		return stopExecution(args[1:], root, output)
	case "declare":
		return declareExecution(args[1:], root, output)
	default:
		return fmt.Errorf("unknown execution subcommand %q", args[0])
	}
}

func reconcileExecutionRetention(args []string, root string, output io.Writer, resolver app.EngineGenerationResolver) error {
	args, jsonOutput, err := takeJSONFlag(args)
	if err != nil || !jsonOutput || len(args) != 1 || args[0] != "reconcile" {
		return errors.New(executionRetentionUsage)
	}
	result, err := app.ReconcileExecutionRetention(context.Background(), root, resolver, runner.NewExecutionCleanupAuditor())
	if err != nil {
		return err
	}
	return writeJSON(output, struct {
		Version string                       `json:"version"`
		Result  app.ExecutionRetentionResult `json:"result"`
	}{Version: "forgepilot.execution-retention-reconcile/v1", Result: result})
}

func stopExecution(args []string, root string, output io.Writer) error {
	args, jsonOutput, err := takeJSONFlag(args)
	if err != nil {
		return err
	}
	values, err := flags(args, map[string]bool{"goal": false, "by": false, "reason": false})
	if err != nil {
		return errors.New(executionStopUsage)
	}
	goalID, requestedBy, reason := values.one("goal"), values.one("by"), values.one("reason")
	if goalID == "" || requestedBy == "" || reason == "" {
		return errors.New(executionStopUsage)
	}
	stopResult, stopErr := runner.RequestStopResult(root, goalID, requestedBy, reason, now())
	if stopErr != nil {
		if !jsonOutput {
			return stopErr
		}
		if writeErr := writeJSON(output, struct {
			Version          string `json:"version"`
			GoalID           string `json:"goalId"`
			Paused           bool   `json:"paused"`
			CleanupConfirmed bool   `json:"cleanupConfirmed"`
			Error            string `json:"error"`
		}{Version: "forgepilot.execution-stop/v1", GoalID: goalID, Paused: stopResult.Pause.GoalID == goalID, CleanupConfirmed: stopResult.CleanupConfirmed, Error: stopErr.Error()}); writeErr != nil {
			return writeErr
		}
		if stopResult.Pause.GoalID != goalID {
			return stopErr
		}
		return &exitStatus{code: runner.StopNeedsHuman.ExitCode(), err: stopErr}
	}
	if jsonOutput {
		return writeJSON(output, struct {
			Version          string `json:"version"`
			GoalID           string `json:"goalId"`
			Paused           bool   `json:"paused"`
			CleanupConfirmed bool   `json:"cleanupConfirmed"`
		}{Version: "forgepilot.execution-stop/v1", GoalID: goalID, Paused: stopResult.Pause.GoalID == goalID, CleanupConfirmed: stopResult.CleanupConfirmed})
	}
	_, err = fmt.Fprintf(output, "Execution for Goal %s paused.\n", goalID)
	return err
}

func declareExecution(args []string, root string, output io.Writer) error {
	args, jsonOutput, err := takeJSONFlag(args)
	if err != nil {
		return err
	}
	if !jsonOutput {
		return errors.New(executionDeclareUsage)
	}
	values, err := flags(args, map[string]bool{"request": false})
	if err != nil || values.one("request") == "" {
		return errors.New(executionDeclareUsage)
	}
	declaration, err := app.DeclareExternalFulfillmentFile(context.Background(), root, values.one("request"))
	if err != nil {
		return err
	}
	return writeJSON(output, struct {
		Version     string                      `json:"version"`
		Declaration control.ExternalDeclaration `json:"declaration"`
	}{Version: "forgepilot.external-fulfillment-declaration/v1", Declaration: declaration})
}

func resumeExecution(args []string, root string, output io.Writer, resolver app.EngineGenerationResolver) error {
	args, jsonOutput, err := takeJSONFlag(args)
	if err != nil {
		return err
	}
	values, err := flags(args, map[string]bool{"goal": false})
	if err != nil {
		return errors.New(executionResumeUsage)
	}
	goalID := values.one("goal")
	if goalID == "" {
		return errors.New(executionResumeUsage)
	}
	stop, signalled, release := signalStop()
	defer release()
	runOutput := output
	if jsonOutput {
		runOutput = io.Discard
	}
	record, err := runner.ResumeAuthorizationGoal(runner.Options{
		Root: root, Output: runOutput, Now: now, Stop: stop, Signalled: signalled,
		GenerationResolver: resolver,
	}, goalID)
	if jsonOutput && err == nil {
		if record.Stop == nil {
			return errors.New("the execution resume ended without recording why")
		}
		if writeErr := writeJSON(output, struct {
			Version string        `json:"version"`
			Run     runner.Record `json:"run"`
		}{Version: "forgepilot.execution-resume/v1", Run: record}); writeErr != nil {
			return writeErr
		}
		return &exitStatus{code: record.Stop.Reason.ExitCode()}
	}
	return reportRun(record, err, output)
}

func reviseExecution(args []string, root string, output io.Writer, resolver app.EngineGenerationResolver) error {
	if len(args) == 0 {
		return errors.New("usage: forgepilot execution revise <plan|authorize>")
	}
	switch args[0] {
	case "plan":
		args, jsonOutput, err := takeJSONFlag(args[1:])
		if err != nil {
			return err
		}
		if !jsonOutput {
			return errors.New(executionRevisePlanUsage)
		}
		values, err := flags(args, map[string]bool{"request": false})
		if err != nil {
			return err
		}
		if values.one("request") == "" {
			return errors.New(executionRevisePlanUsage)
		}
		projection, planErr := app.PlanExecutionRevisionFile(context.Background(), root, values.one("request"))
		if err := writeJSON(output, projection); err != nil {
			return err
		}
		return planErr
	case "authorize":
		args, jsonOutput, err := takeJSONFlag(args[1:])
		if err != nil {
			return err
		}
		if !jsonOutput {
			return errors.New(executionReviseAuthorizeUsage)
		}
		values, err := flags(args, map[string]bool{"request": false, "approval-token": false, "by": false})
		if err != nil {
			return err
		}
		request, token, by := values.one("request"), values.one("approval-token"), values.one("by")
		if request == "" || token == "" || by == "" {
			return errors.New(executionReviseAuthorizeUsage)
		}
		preview, err := app.PlanExecutionRevisionFile(context.Background(), root, request)
		if err != nil {
			return err
		}
		if len(preview.Diagnostics) != 0 {
			return fmt.Errorf("execution revision is not authorizable (%s): %s", preview.Diagnostics[0].Code, preview.Diagnostics[0].Message)
		}
		var execution work.GoalExecution
		if preview.LegacyRetentionUnknown {
			execution, err = app.ReauthorizeLegacyExecutionFile(context.Background(), root, request, token, by,
				resolver, runner.NewExecutionCleanupAuditor())
		} else if preview.RevisionDiff != nil && preview.RevisionDiff.EngineGeneration != nil {
			execution, err = app.ReviseExecutionEngineFile(context.Background(), root, request, token, by,
				resolver, runner.NewExecutionCleanupAuditor())
		} else {
			execution, err = app.RevisePinnedExecutionFile(context.Background(), root, request, token, by, resolver)
		}
		if err != nil {
			return err
		}
		result, cleanupErr := app.ReconcileExecutionRetention(context.Background(), root, resolver, runner.NewExecutionCleanupAuditor())
		response := executionAuthorizationOutput{Version: "forgepilot.execution-revision/v2", GoalExecution: execution,
			RetentionCleanup: &result}
		if cleanupErr != nil {
			response.RetentionWarning = cleanupErr.Error()
		}
		return writeJSON(output, response)
	default:
		return fmt.Errorf("unknown execution revise subcommand %q", args[0])
	}
}

func planExecution(args []string, root string, output io.Writer) error {
	args, jsonOutput, err := takeJSONFlag(args)
	if err != nil {
		return err
	}
	if !jsonOutput {
		return errors.New(executionPlanUsage)
	}
	values, err := flags(args, map[string]bool{"request": false})
	if err != nil {
		return err
	}
	requestPath := values.one("request")
	if requestPath == "" {
		return errors.New(executionPlanUsage)
	}
	projection, planErr := app.PlanExecutionFile(context.Background(), root, requestPath)
	if err := writeJSON(output, projection); err != nil {
		return err
	}
	return planErr
}

func authorizeExecution(args []string, root string, output io.Writer, resolver app.EngineGenerationResolver) error {
	args, jsonOutput, err := takeJSONFlag(args)
	if err != nil {
		return err
	}
	if !jsonOutput {
		return errors.New(executionAuthorizeUsage)
	}
	values, err := flags(args, map[string]bool{"request": false, "approval-token": false, "by": false})
	if err != nil {
		return err
	}
	requestPath, token, approver := values.one("request"), values.one("approval-token"), values.one("by")
	if requestPath == "" || token == "" || approver == "" {
		return errors.New(executionAuthorizeUsage)
	}
	execution, err := app.AuthorizePinnedExecutionFile(context.Background(), root, requestPath, token, approver, resolver)
	if err != nil {
		return err
	}
	return writeJSON(output, executionAuthorizationOutput{Version: "forgepilot.execution-authorization/v2", GoalExecution: execution})
}

type executionAuthorizationOutput struct {
	Version          string                        `json:"version"`
	GoalExecution    work.GoalExecution            `json:"goalExecution"`
	RetentionCleanup *app.ExecutionRetentionResult `json:"retentionCleanup,omitempty"`
	RetentionWarning string                        `json:"retentionWarning,omitempty"`
}
