package app

import (
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// Now is the clock this package reads. Callers pass one so tests can be
// deterministic; nil means the wall clock in UTC.
type Now func() time.Time

func (now Now) at() time.Time {
	if now != nil {
		return now().UTC()
	}
	return time.Now().UTC()
}

// StartWork applies the start transition in one locked transaction.
func StartWork(root, id string, now Now) error {
	return storage.Update(root, func(state *work.State) error {
		return state.Start(id, now.at())
	})
}

// ReconcileGoal recomputes one Goal's persisted readiness. It appends no
// Evidence and answers no Gate.
func ReconcileGoal(root, goalID string, now Now) ([]work.ReadinessChange, error) {
	var changes []work.ReadinessChange
	err := storage.Update(root, func(state *work.State) error {
		var reconcileErr error
		changes, reconcileErr = state.ReconcileGoalReadiness(goalID, now.at())
		return reconcileErr
	})
	return changes, err
}
