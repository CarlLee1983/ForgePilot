package app

import (
	"context"
	"fmt"
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

// StartWork applies the existing start transition, resolving the facts that
// transition depends on inside the locked callback so it cannot act on a fact
// that went stale while the lock was being taken.
func StartWork(ctx context.Context, root, id string, now Now) error {
	return storage.Update(root, func(state *work.State) error {
		facts, err := StartFacts(ctx, state, id, root)
		if err != nil {
			return err
		}
		return state.StartWithRepository(id, facts, now.at())
	})
}

// ReconcileGoal recomputes one Goal's persisted readiness against current
// facts. It appends no Evidence, answers no Gate and changes no review policy.
func ReconcileGoal(ctx context.Context, root, goalID string, now Now) ([]work.ReadinessChange, error) {
	var changes []work.ReadinessChange
	err := storage.Update(root, func(state *work.State) error {
		// The Goal is checked before any Git call so an unknown or stopped Goal is
		// refused for what it is, rather than by whatever the repository says.
		if err := state.ReconcilableGoal(goalID); err != nil {
			return err
		}
		facts, factsErr := GoalReadinessFacts(ctx, state, goalID, root)
		if factsErr != nil {
			return fmt.Errorf("resolve current Candidate before reconciling readiness: %w", factsErr)
		}
		var reconcileErr error
		changes, reconcileErr = state.ReconcileGoalReadiness(goalID, facts, now.at())
		return reconcileErr
	})
	return changes, err
}
