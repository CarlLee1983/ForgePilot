package work

import (
	"fmt"
	"time"
)

// ReadinessChange records one persisted readiness move a reconciliation made.
// It is a report about a transition that already happened, not an instruction.
type ReadinessChange struct {
	ItemID string
	From   Status
	To     Status
}

// ReconcilableGoal reports whether a Goal may have its readiness recomputed.
func (s *State) ReconcilableGoal(id string) error {
	goal := s.goal(id)
	if goal == nil {
		return fmt.Errorf("unknown goal %q", id)
	}
	if goal.Status != GoalActive {
		return fmt.Errorf("cannot reconcile goal %q: it is %s", id, goal.Status)
	}
	return nil
}

// ReconcileGoalReadiness recomputes the persisted PENDING/READY readiness of one
// Goal's Work Items. Readiness is durable, so a state edited by hand or written
// by an older binary can disagree with the dependency rule; only a deliberate
// command writes the correction.
//
// It reuses the one dependency rule (every dependency DONE), so it can promote
// exactly what completion would have made READY and nothing more. RUNNING,
// VERIFYING, REVIEW and DONE are left alone, no Evidence or Gate is touched, and
// a Work Item made READY here is still blocked by its own open Gates — blocking
// is a condition, not a status (ADR-0007).
func (s *State) ReconcileGoalReadiness(id string, now time.Time) ([]ReadinessChange, error) {
	if err := s.ReconcilableGoal(id); err != nil {
		return nil, err
	}
	var changes []ReadinessChange
	for i := range s.WorkItems {
		item := &s.WorkItems[i]
		if item.GoalID != id {
			continue
		}
		before := item.Status
		s.reconcileReady(item, now)
		if item.Status != before {
			changes = append(changes, ReadinessChange{ItemID: item.ID, From: before, To: item.Status})
		}
	}
	return changes, nil
}

// advanceable is the shared legality test behind both selecting READY work and
// recommending a readiness reconciliation. Keeping one predicate is what stops
// next from recommending a command that would then report nothing to do.
//
// The reconcile command itself is deliberately broader: it recomputes the
// readiness of a Work Item that carries its own open Gate too, because blocking
// is a condition rather than a status (ADR-0007). Only the recommendation needs
// the Gate test, since a gated item is not something an agent can act on.
func (s *State) advanceable(item Item) bool {
	if s.OpenGateCount(item.ID) != 0 {
		return false
	}
	if goal := s.goal(item.GoalID); goal == nil || goal.Status != GoalActive {
		return false
	}
	return s.dependenciesDone(item.DependsOn)
}
