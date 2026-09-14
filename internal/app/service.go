package app

import (
	"fmt"
	"sort"
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

// Decision is the typed answer to "what may happen next in this Goal". It is
// what a Runner consumes; the CLI's `Next:`/`Action:` lines are presentation of
// the same values and must never be parsed back.
type Decision struct {
	Action     work.NextAction
	Stall      work.GoalStall
	Repository work.RepositoryState
	// Verifying names Work Items whose recorded Verification Run is still live,
	// and Orphaned those whose runner is gone. Both are only populated when the
	// stall is about verification: liveness is a flock fact, so it is resolved
	// here rather than inside internal/work.
	Verifying []string
	Orphaned  []string
}

// GoalDecision re-reads state and the repository facts that decision depends
// on, then asks internal/work for one legal action within the Goal. Nothing is
// cached between calls: every step re-reads, because Gates, Candidates and Goal
// status all move underneath a long-running caller.
func GoalDecision(root, goalID string) (Decision, error) {
	state, err := storage.Load(root)
	if err != nil {
		return Decision{}, err
	}
	facts, err := GoalCandidateFacts(&state, goalID, root)
	if err != nil {
		return Decision{}, err
	}
	decision := Decision{
		Action:     state.ActionableNextForGoal(goalID, facts),
		Stall:      state.GoalStall(goalID, facts),
		Repository: facts,
	}
	if decision.Stall.Kind == work.StallVerifying {
		for _, id := range decision.Stall.ItemIDs {
			if storage.VerificationRunning(root, id) {
				decision.Verifying = append(decision.Verifying, id)
			} else {
				decision.Orphaned = append(decision.Orphaned, id)
			}
		}
	}
	return decision, nil
}

// RunnableGoal reports whether a Goal may be driven by a Runner at all. The
// three conditions are deliberately checked before any repository fact is read:
// a Goal nothing may be done to must be refused for what it is.
func RunnableGoal(root, goalID string) (work.Goal, error) {
	state, err := storage.Load(root)
	if err != nil {
		return work.Goal{}, err
	}
	for _, goal := range state.Goals {
		if goal.ID != goalID {
			continue
		}
		if goal.Status != work.GoalActive {
			return goal, fmt.Errorf("goal %q is %s, not ACTIVE", goalID, goal.Status)
		}
		if goal.ReviewPolicy != work.ReviewPerGoal {
			return goal, fmt.Errorf("goal %q uses %s review policy; the runner only drives GOAL review policy", goalID, goal.ReviewPolicy)
		}
		for _, item := range state.WorkItems {
			if item.GoalID == goalID {
				return goal, nil
			}
		}
		return goal, fmt.Errorf("goal %q has no work items", goalID)
	}
	return work.Goal{}, fmt.Errorf("unknown goal %q", goalID)
}

// StartWork applies the existing start transition, resolving the facts that
// transition depends on inside the locked callback so it cannot act on a fact
// that went stale while the lock was being taken.
func StartWork(root, id string, now Now) error {
	return storage.Update(root, func(state *work.State) error {
		facts, err := StartFacts(state, id, root)
		if err != nil {
			return err
		}
		return state.StartWithRepository(id, facts, now.at())
	})
}

// ReconcileGoal recomputes one Goal's persisted readiness against current
// facts. It appends no Evidence, answers no Gate and changes no review policy.
func ReconcileGoal(root, goalID string, now Now) ([]work.ReadinessChange, error) {
	var changes []work.ReadinessChange
	err := storage.Update(root, func(state *work.State) error {
		// The Goal is checked before any Git call so an unknown or stopped Goal is
		// refused for what it is, rather than by whatever the repository says.
		if err := state.ReconcilableGoal(goalID); err != nil {
			return err
		}
		facts, factsErr := GoalReadinessFacts(state, goalID, root)
		if factsErr != nil {
			return fmt.Errorf("resolve current Candidate before reconciling readiness: %w", factsErr)
		}
		var reconcileErr error
		changes, reconcileErr = state.ReconcileGoalReadiness(goalID, facts, now.at())
		return reconcileErr
	})
	return changes, err
}

// GoalReadiness projects whether a Goal has reached its final-review boundary.
// It is recomputed from current facts every time, which is why a run record's
// stored conclusion and this answer are reported separately: the workspace may
// have moved since the run stopped.
func GoalReadiness(root, goalID string) (work.GoalSummary, error) {
	state, err := storage.Load(root)
	if err != nil {
		return work.GoalSummary{}, err
	}
	facts, err := GoalCandidateFacts(&state, goalID, root)
	if err != nil {
		return work.GoalSummary{}, err
	}
	return state.GoalSummary(goalID, facts)
}

// GoalScope fingerprints the Work Item set a run was started against: each
// item's ID, Story and ordered dependencies. A run compares it every iteration,
// because a Goal whose scope changed underneath it is no longer the Goal the
// person authorized.
func GoalScope(state *work.State, goalID string) []string {
	var scope []string
	for _, item := range state.WorkItems {
		if item.GoalID != goalID {
			continue
		}
		dependencies := append([]string(nil), item.DependsOn...)
		sort.Strings(dependencies)
		scope = append(scope, fmt.Sprintf("%s\x1f%s\x1f%v", item.ID, item.StoryRef, dependencies))
	}
	sort.Strings(scope)
	return scope
}

// CurrentGoalScope reads the fingerprint from durable state.
func CurrentGoalScope(root, goalID string) ([]string, error) {
	state, err := storage.Load(root)
	if err != nil {
		return nil, err
	}
	return GoalScope(&state, goalID), nil
}

// Gate content limits bound what an untrusted agent session can write into
// durable governance state. The Gate itself is the right place for a question
// only a person may answer, but the text arrives from a model, and a Gate is
// read by people in a terminal.
const (
	MaxGateQuestionBytes = 2000
	MaxGateOptionBytes   = 200
	MaxGateOptions       = 8
)

// OpenGate records a decision only a person may make, through the existing Gate
// service. A Runner uses it when an agent session stops with real alternatives:
// the question becomes durable and visible, and nothing about it is answered
// here. Resolving it stays a human act, with a self-asserted identity
// (ADR-0005).
//
// This is the one place a model's words become persisted ForgePilot state, so
// it is bounded here rather than in internal/work: the domain rule is about
// what a Gate is, and this is about who is speaking.
func OpenGate(root, workItemID, question string, options []string, rationale string, now Now) (string, error) {
	question = bound(question, MaxGateQuestionBytes)
	dropped := 0
	if len(options) > MaxGateOptions {
		// Said out loud, not silently: a person about to choose between these is
		// entitled to know the list they are reading is not the list they were
		// offered. Truncated text already says so; a truncated list must too.
		dropped = len(options) - MaxGateOptions
		options = options[:MaxGateOptions]
	}
	bounded := make([]string, 0, len(options))
	for _, option := range options {
		bounded = append(bounded, bound(option, MaxGateOptionBytes))
	}
	options = bounded
	if dropped > 0 {
		question = bound(fmt.Sprintf("%s\n\n(%d further option(s) were offered and are not shown; see the run journal.)", question, dropped),
			MaxGateQuestionBytes+120)
	}
	var gateID string
	err := storage.Update(root, func(state *work.State) error {
		gate, err := state.OpenGate(workItemID, question, options, rationale, now.at())
		if err != nil {
			return err
		}
		gateID = gate.ID
		return nil
	})
	return gateID, err
}

// bound truncates untrusted text and says that it did, so a person reading the
// Gate knows there was more rather than assuming they have the whole question.
func bound(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + " …(truncated)"
}
