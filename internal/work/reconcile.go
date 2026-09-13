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

// ReconcilableGoal reports whether a Goal may have its readiness recomputed. It
// is separate from ReconcileGoalReadiness so a caller can refuse an unknown or
// stopped Goal before resolving any repository facts for it: gathering facts for
// a Goal nothing may be done to is work nobody asked for, and an inactive Goal's
// answer must not depend on whether Git could be read.
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
// Goal's Work Items against current repository facts. It exists because
// readiness is durable while the facts it was derived from are not: a downstream
// item withdrawn to PENDING by a Gate or a moved Candidate stays PENDING after
// the condition lifts, and only a deliberate command may write the correction.
//
// It is a readiness projection made durable, never a verification bypass. It
// reuses the one dependency-progression predicate, so it can promote exactly
// what a fresh AddWork would have made READY and nothing more. RUNNING,
// VERIFYING, REVIEW, VERIFIED and DONE are left alone, no Evidence, Gate or
// review policy is touched, and a Work Item made READY here is still blocked by
// its own open Gates — blocking is a condition, not a status (ADR-0007).
func (s *State) ReconcileGoalReadiness(id string, repository RepositoryState, now time.Time) ([]ReadinessChange, error) {
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
		s.reconcileReady(item, &repository, now)
		if item.Status != before {
			changes = append(changes, ReadinessChange{ItemID: item.ID, From: before, To: item.Status})
		}
	}
	return changes, nil
}

// ReadinessCandidateKinds names the Candidate facts a reconciliation of this
// Goal actually needs, so an adapter resolves those and no others. The filter
// mirrors what dependenciesSatisfiedAt will ask: only the prerequisites of work
// that is still PENDING or READY matter, a DONE prerequisite needs no Candidate
// comparison, and a prerequisite the progression rule already refuses — or whose
// latest Verification is not a PASS — is decided without consulting the
// repository at all. Reading a fact the transaction would not use would let it
// refuse a command whose answer never depended on it.
//
// An absent fact is never read as a fresh one: a prerequisite whose Evidence
// this cannot classify simply fails the freshness test later.
func (s *State) ReadinessCandidateKinds(id string) []CandidateKind {
	seen := map[CandidateKind]bool{}
	var kinds []CandidateKind
	for _, item := range s.WorkItems {
		if item.GoalID != id || (item.Status != Pending && item.Status != Ready) {
			continue
		}
		for _, dependencyID := range item.DependsOn {
			dependency := s.item(dependencyID)
			if dependency == nil || dependency.Status == Done || !s.satisfiesDependency(*dependency) {
				continue
			}
			verification, ok := s.LatestVerification(dependencyID)
			if !ok || verification.Result != Pass || seen[verification.CandidateKind] {
				continue
			}
			seen[verification.CandidateKind] = true
			kinds = append(kinds, verification.CandidateKind)
		}
	}
	return kinds
}

// advanceable is the shared legality test behind both selecting READY work and
// recommending a readiness reconciliation. Keeping one predicate is what stops
// next from recommending a command that would then report nothing to do.
//
// The reconcile command itself is deliberately broader: it recomputes the
// readiness of a Work Item that carries its own open Gate too, because blocking
// is a condition rather than a status (ADR-0007). Only the recommendation needs
// the Gate test, since a gated item is not something an agent can act on.
func (s *State) advanceable(item Item, repository *RepositoryState) bool {
	if s.OpenGateCount(item.ID) != 0 {
		return false
	}
	if goal := s.goal(item.GoalID); goal == nil || goal.Status != GoalActive {
		return false
	}
	return s.dependenciesSatisfiedAt(item.DependsOn, repository)
}
