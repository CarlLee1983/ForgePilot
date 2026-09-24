package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/control"
	"github.com/CarlLee1983/ForgePilot/internal/runner"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/supervision"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

const superviseUsage = "usage: forgepilot execution supervise <install|status|uninstall> --goal <goal-id> --json\n" +
	"       forgepilot execution supervise run --job <absolute-job-path>"

func superviseExecution(args []string, root string, output io.Writer, resolver app.EngineGenerationResolver) error {
	if len(args) == 0 {
		return errors.New(superviseUsage)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	manager, err := supervision.New(home)
	if err != nil {
		return err
	}
	command := args[0]
	if command == "run" {
		values, err := flags(args[1:], map[string]bool{"job": false})
		if err != nil || values.one("job") == "" {
			return errors.New(superviseUsage)
		}
		return runSupervisedJob(manager, root, values.one("job"), output, resolver)
	}
	remaining, jsonOutput, err := takeJSONFlag(args[1:])
	if err != nil || !jsonOutput {
		return errors.New(superviseUsage)
	}
	values, err := flags(remaining, map[string]bool{"goal": false})
	if err != nil || values.one("goal") == "" {
		return errors.New(superviseUsage)
	}
	goalID := values.one("goal")
	id, err := supervision.ID(root, goalID)
	if err != nil {
		return err
	}
	switch command {
	case "install":
		authorization, err := supervisedAuthorization(root, goalID, resolver, now())
		if err != nil {
			return err
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		job, err := manager.Install(supervision.Spec{Workspace: root, GoalID: goalID, Executable: executable,
			AuthorizationDigest: authorization.Digest, Deadline: authorization.ExpiresAt}, now())
		if err != nil {
			return err
		}
		return writeJSON(output, job)
	case "status":
		status, err := manager.Status(id)
		if err != nil {
			return err
		}
		return writeJSON(output, status)
	case "uninstall":
		// Persist Runner pause before bootout. A concurrent timer admission
		// must pass that pause gate before it can launch a worker.
		if _, err := runner.RequestSupervisedStopResult(root, goalID, "supervision", "supervisor uninstalled", now()); err != nil {
			return err
		}
		if err := manager.Uninstall(id, now()); err != nil {
			return err
		}
		return writeJSON(output, struct {
			Uninstalled bool `json:"uninstalled"`
		}{true})
	default:
		return errors.New(superviseUsage)
	}
}

func supervisedAuthorization(root, goalID string, resolver app.EngineGenerationResolver, at time.Time) (work.ExecutionAuthorization, error) {
	if resolver == nil {
		return work.ExecutionAuthorization{}, errors.New("supervision requires a managed ForgePilot generation")
	}
	state, err := storage.Load(root)
	if err != nil {
		return work.ExecutionAuthorization{}, err
	}
	goal, ok := state.GoalByID(goalID)
	if !ok || goal.Execution == nil || len(goal.Execution.Authorizations) == 0 {
		return work.ExecutionAuthorization{}, fmt.Errorf("goal %q has no execution authorization", goalID)
	}
	authorization := goal.Execution.Authorizations[len(goal.Execution.Authorizations)-1]
	if !at.Before(authorization.ExpiresAt) {
		return work.ExecutionAuthorization{}, errors.New("execution authorization deadline has expired")
	}
	if authorization.EngineGeneration == nil || !authorization.RetentionAcquired || authorization.WorkerIdentity == nil {
		return work.ExecutionAuthorization{}, errors.New("execution authorization has no pinned worker and engine")
	}
	resolved, err := resolver.Resolve(context.Background())
	if err != nil {
		return work.ExecutionAuthorization{}, err
	}
	if resolved.Generation != *authorization.EngineGeneration {
		return work.ExecutionAuthorization{}, errors.New("supervisor engine generation differs from authorization")
	}
	if err := app.ValidateCurrentExecutionBindings(root, goalID, authorization.Digest); err != nil {
		return work.ExecutionAuthorization{}, err
	}
	return authorization, nil
}

func runSupervisedJob(manager *supervision.Manager, root, jobPath string, output io.Writer, resolver app.EngineGenerationResolver) error {
	if !filepath.IsAbs(jobPath) || filepath.Ext(jobPath) != ".json" {
		return errors.New("supervision job path must be absolute JSON path")
	}
	id := strings.TrimSuffix(filepath.Base(jobPath), ".json")
	expectedPath, err := manager.JobPath(id)
	if err != nil || expectedPath != jobPath {
		return errors.New("supervision job path does not match its user-scoped record")
	}
	job, err := manager.Load(id)
	if err != nil {
		return err
	}
	if job.Workspace != root || job.Executable == "" || job.Phase == supervision.PhasePaused || job.Phase == supervision.PhaseBlocked {
		return errors.New("supervised job is paused, blocked, or belongs to another workspace")
	}
	if _, err := manager.RecordEvent(id, "launch", "launchd invocation", now()); err != nil {
		return err
	}
	authorization, err := supervisedAuthorization(root, job.GoalID, resolver, now())
	if err != nil {
		_, _ = manager.SetPhase(id, supervision.PhaseBlocked, err.Error(), now())
		return err
	}
	executable, err := os.Executable()
	if err != nil || executable != job.Executable || authorization.Digest != job.AuthorizationDigest || !authorization.ExpiresAt.Equal(job.Deadline) {
		_, _ = manager.SetPhase(id, supervision.PhaseBlocked, "pinned job binding changed", now())
		return errors.New("supervised job binding changed; explicit revision and reinstall required")
	}
	selected, found, err := runner.LatestAuthorizationRun(root, job.GoalID, authorization.Digest)
	if err != nil {
		return err
	}
	var latest *runner.Record
	if found {
		latest = &selected
	}
	if latest != nil && latest.Stop != nil {
		switch latest.Stop.Reason {
		case runner.StopMaxSteps, runner.StopMaxDuration, runner.StopInterrupted, runner.StopTerminated:
		default:
			phase := supervision.PhasePaused
			if latest.Stop.Reason == runner.StopRecoveryBlocked {
				phase = supervision.PhaseBlocked
			}
			_, _ = manager.SetPhase(id, phase, string(latest.Stop.Reason), now())
			return fmt.Errorf("supervised job waits for explicit resume after %s", latest.Stop.Reason)
		}
	}
	if _, err := manager.SetPhase(id, supervision.PhaseRunning, "Runner admission requested", now()); err != nil {
		return err
	}
	stop, signalled, release := signalStop()
	defer release()
	options := runner.Options{Root: root, GoalID: job.GoalID, Output: output, Now: now, Stop: stop,
		Signalled: signalled, GenerationResolver: resolver, Supervised: true}
	var record runner.Record
	if latest == nil {
		options.RuntimeName = authorization.WorkerProfile.Runtime
		options.RuntimeCommand = authorization.WorkerProfile.ExecutablePath
		options.Snapshot = true
		options.Budget = defaultBudget()
		options.Limits = defaultLimits()
		record, err = runner.Start(options)
	} else if latest.Stop == nil && !now().Before(latest.Deadline) {
		// A sleep or logout can carry an unfinished run past its own deadline.
		// Settle that exact run first; a later invocation may consider a
		// separately charged successor under the still-current authorization.
		record, err = runner.Resume(options, latest.RunID)
	} else {
		record, err = runner.ResumeAuthorizationGoal(options, job.GoalID)
	}
	// A second LaunchAgent invocation can race this one. Runner's workspace
	// lock refuses the loser; only the owner may publish a terminal phase.
	if errors.Is(err, storage.ErrRunnerInFlight) {
		return err
	}
	phase := supervision.PhaseReady
	if err != nil || record.Stop == nil || record.Stop.Reason == runner.StopRecoveryBlocked {
		phase = supervision.PhaseBlocked
	} else if err == nil && record.Stop != nil && record.Stop.Reason != runner.StopMaxSteps && record.Stop.Reason != runner.StopMaxDuration &&
		record.Stop.Reason != runner.StopInterrupted && record.Stop.Reason != runner.StopTerminated {
		phase = supervision.PhasePaused
	}
	if _, phaseErr := manager.SetPhase(id, phase, "Runner returned", now()); phaseErr != nil && err == nil {
		err = phaseErr
	}
	return reportRun(record, err, output)
}

// resumePreRunSupervision handles the stop/launch window before a Run Record
// exists. An ordinary exact or authorization-level resume stays on its
// existing Runner path. Clearing the wildcard pause requires an explicit user
// command, confirmed cleanup, and a fresh pinned authorization check.
func resumePreRunSupervision(root, goalID string, options runner.Options) (runner.Record, bool, error) {
	state, err := control.Read(root)
	if err != nil {
		return runner.Record{}, true, err
	}
	if state.Pause == nil || state.Pause.GoalID != goalID || state.Pause.RunID != runner.SupervisionPauseRunID {
		return runner.Record{}, false, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return runner.Record{}, true, err
	}
	manager, err := supervision.New(home)
	if err != nil {
		return runner.Record{}, true, err
	}
	id, err := supervision.ID(root, goalID)
	if err != nil {
		return runner.Record{}, true, err
	}
	job, err := manager.Load(id)
	if err != nil {
		return runner.Record{}, true, err
	}
	authorization, err := supervisedAuthorization(root, goalID, options.GenerationResolver, now())
	if err != nil {
		return runner.Record{}, true, err
	}
	if authorization.Digest != job.AuthorizationDigest || !authorization.ExpiresAt.Equal(job.Deadline) {
		return runner.Record{}, true, errors.New("supervised job binding changed; explicit revision and reinstall required")
	}
	if err := runner.ClearSupervisionPause(root, goalID); err != nil {
		return runner.Record{}, true, err
	}
	if job.Phase == supervision.PhasePaused || job.Phase == supervision.PhaseBlocked {
		if _, err := manager.Resume(id, now()); err != nil {
			return runner.Record{}, true, err
		}
	}
	_, found, err := runner.LatestAuthorizationRun(root, goalID, authorization.Digest)
	if err != nil {
		return runner.Record{}, true, err
	}
	if found {
		record, err := runner.ResumeAuthorizationGoal(options, goalID)
		return record, true, err
	}
	options.GoalID = goalID
	options.RuntimeName = authorization.WorkerProfile.Runtime
	options.RuntimeCommand = authorization.WorkerProfile.ExecutablePath
	options.Snapshot = true
	options.Budget = defaultBudget()
	options.Limits = defaultLimits()
	record, err := runner.Start(options)
	return record, true, err
}
