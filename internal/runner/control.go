package runner

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/control"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
)

// StopResult reports the exact pause persisted by RequestStop and whether the
// owned worker was confirmed settled. A cleanup error never rolls back Pause.
type StopResult struct {
	Pause            control.Pause
	CleanupConfirmed bool
}

var ErrNoLiveRun = errors.New("goal has no live run")

// SupervisionPauseRunID fences the interval before the first Run Record exists.
// It is never a real Run ID and only an explicit supervised resume clears it.
const SupervisionPauseRunID = "supervision-before-run"

// RequestStop records a user's stop intent before it attempts to terminate the
// current worker for goalID. The durable intent is deliberately not rolled back
// when ownership is incomplete or cleanup cannot be confirmed: proceeding
// after either answer would be less safe than requiring human recovery.
func RequestStop(root, goalID, requestedBy, reason string, now time.Time) error {
	_, err := RequestStopResult(root, goalID, requestedBy, reason, now)
	return err
}

func RequestStopResult(root, goalID, requestedBy, reason string, now time.Time) (StopResult, error) {
	return requestStopResult(root, goalID, requestedBy, reason, now, false)
}

// RequestSupervisedStopResult also accepts the pre-Run launch window. Its
// wildcard pause is durable before a LaunchAgent can reach the final worker
// launch gate; the normal stop path still settles a recorded worker.
func RequestSupervisedStopResult(root, goalID, requestedBy, reason string, now time.Time) (StopResult, error) {
	return requestStopResult(root, goalID, requestedBy, reason, now, true)
}

func requestStopResult(root, goalID, requestedBy, reason string, now time.Time, supervised bool) (StopResult, error) {
	if strings.TrimSpace(goalID) == "" {
		return StopResult{}, errors.New("goal ID is required")
	}
	if now.IsZero() {
		return StopResult{}, errors.New("stop request time is required")
	}
	var record Record
	var result StopResult
	if err := control.WithLock(root, func() error {
		var err error
		record, err = currentLiveRun(root, goalID)
		if errors.Is(err, ErrNoLiveRun) && supervised {
			record = Record{RunID: SupervisionPauseRunID}
		} else if err != nil {
			return err
		}
		if err := control.UpdateLocked(root, func(state *control.State) error {
			return state.SetPause(control.Pause{GoalID: goalID, RunID: record.RunID,
				Reason: reason, RequestedBy: requestedBy, RequestedAt: now.UTC()})
		}); err != nil {
			return err
		}
		state, err := control.ReadLocked(root)
		if err != nil {
			return err
		}
		if state.Pause == nil {
			return errors.New("execution pause was not durable after stop request")
		}
		result.Pause = *state.Pause
		return nil
	}); err != nil {
		return StopResult{}, err
	}
	if record.RunID == SupervisionPauseRunID {
		result.CleanupConfirmed = true
		return result, nil
	}
	// The pause is now durable and runner admission can no longer pass its
	// control-lock check. Do not keep that lock while terminating: process
	// cleanup can take its bounded grace period, and it must not serialize an
	// unrelated read of the durable control fact.
	if record.Worker == nil {
		return result, fmt.Errorf("run %s for goal %s has no recorded worker; stop intent is durable but worker ownership cannot be confirmed", record.RunID, goalID)
	}
	if err := settleExecution(record.Worker.Identity); err != nil {
		return result, fmt.Errorf("run %s for goal %s stop intent is durable but worker could not be settled: %w", record.RunID, goalID, err)
	}
	result.CleanupConfirmed = true
	return result, nil
}

// currentLiveRun finds the one record that can still drive a Goal. A stopped
// record is history, not a target for a new stop intent. Multiple unstopped
// records are unsafe to guess between, so fail closed.
func currentLiveRun(root, goalID string) (Record, error) {
	runIDs, err := storage.ListRuns(root)
	if err != nil {
		return Record{}, err
	}
	var live *Record
	for _, runID := range runIDs {
		record, err := LoadRecord(root, runID)
		if err != nil {
			return Record{}, fmt.Errorf("read run %s while requesting stop: %w", runID, err)
		}
		if record.GoalID != goalID || record.Stop != nil || record.RetentionClosure != nil {
			continue
		}
		if live != nil {
			return Record{}, fmt.Errorf("goal %s has multiple live runs (%s and %s); refusing to guess which worker to stop", goalID, live.RunID, record.RunID)
		}
		copy := record
		live = &copy
	}
	if live == nil {
		return Record{}, fmt.Errorf("%w: goal %s", ErrNoLiveRun, goalID)
	}
	return *live, nil
}

func pauseFor(root, goalID, runID string) (*control.Pause, error) {
	var pause *control.Pause
	err := control.WithLock(root, func() error {
		state, err := control.ReadLocked(root)
		if err != nil || state.Pause == nil || state.Pause.GoalID != goalID ||
			(runID != "" && state.Pause.RunID != runID && state.Pause.RunID != SupervisionPauseRunID) {
			return err
		}
		copy := *state.Pause
		pause = &copy
		return nil
	})
	return pause, err
}

// ClearSupervisionPause consumes an explicit resume of the pre-Run stop. The
// workspace lock excludes a competing Runner admission while the control
// record is changed; later worker starts still recheck the control lock.
func ClearSupervisionPause(root, goalID string) error {
	return storage.WithWorkspaceLock(root, func() error {
		if blocked, err := settleWorkspace(Options{Root: root}, ""); err != nil {
			return err
		} else if blocked != nil {
			return fmt.Errorf("cannot resume supervised job until run %s cleanup is confirmed: %s", blocked.RunID, blocked.Stop.Detail)
		}
		return control.WithLock(root, func() error {
			state, err := control.ReadLocked(root)
			if err != nil {
				return err
			}
			if state.Pause == nil || state.Pause.GoalID != goalID || state.Pause.RunID != SupervisionPauseRunID {
				return errors.New("goal has no pre-run supervised pause to resume")
			}
			return control.UpdateLocked(root, func(state *control.State) error {
				state.ClearPause()
				return nil
			})
		})
	})
}

func (runner *Runner) honorPause() (bool, error) {
	pause, err := pauseFor(runner.options.Root, runner.record.GoalID, runner.record.RunID)
	if err != nil || pause == nil {
		return false, err
	}
	return true, runner.stopNow(StopUserPaused, fmt.Sprintf("paused by %s: %s", pause.RequestedBy, pause.Reason))
}
