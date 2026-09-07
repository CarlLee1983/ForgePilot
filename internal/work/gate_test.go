package work

import (
	"strings"
	"testing"
	"time"
)

// gateFixture builds a Goal with one READY Work Item and one that depends on it.
func gateFixture(t *testing.T) (State, time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	state := NewState()
	if err := state.AddGoal("g", "Goal", "", "/repo", now); err != nil {
		t.Fatal(err)
	}
	first, err := state.AddWork("g", "specs/stories/a", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.AddWork("g", "specs/stories/b", []string{first.ID}, now); err != nil {
		t.Fatal(err)
	}
	return state, now
}

func TestOpenGateRequiresAChoiceAndAKnownWorkItem(t *testing.T) {
	state, now := gateFixture(t)
	options := []string{"redis", "in-process"}

	if _, err := state.OpenGate("WI-001", "", options, "", now); err == nil {
		t.Fatal("accepted a gate with no question")
	}
	if _, err := state.OpenGate("WI-001", "Which cache?", []string{"redis"}, "", now); err == nil {
		t.Fatal("accepted a gate offering a single option")
	}
	if _, err := state.OpenGate("WI-001", "Which cache?", nil, "", now); err == nil {
		t.Fatal("accepted a gate offering no options")
	}
	if _, err := state.OpenGate("WI-001", "Which cache?", []string{"redis", "redis"}, "", now); err == nil {
		t.Fatal("accepted a gate offering the same option twice")
	}
	if _, err := state.OpenGate("WI-404", "Which cache?", options, "", now); err == nil {
		t.Fatal("accepted a gate on an unknown work item")
	}
	if len(state.Gates) != 0 {
		t.Fatalf("a refused gate was still recorded: %#v", state.Gates)
	}

	gate, err := state.OpenGate("WI-001", "Which cache?", options, "latency budget is unclear", now)
	if err != nil {
		t.Fatal(err)
	}
	if gate.ID != "GATE-001" || gate.Status != GateOpen {
		t.Fatalf("gate = %#v", gate)
	}
	if gate.Question != "Which cache?" || gate.Rationale != "latency budget is unclear" {
		t.Fatalf("gate lost its question or rationale: %#v", gate)
	}
	if len(gate.Options) != 2 || gate.Options[0] != "redis" || gate.Options[1] != "in-process" {
		t.Fatalf("gate lost its options: %#v", gate.Options)
	}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestOpenGateDoesNotOccupyTheStatusField is the whole point of ADR-0007:
// blocking is a separate condition, so a RUNNING Work Item with a Gate is still
// RUNNING and has nothing to restore when the Gate closes.
func TestOpenGateDoesNotOccupyTheStatusField(t *testing.T) {
	state, now := gateFixture(t)
	if err := state.Start("WI-001", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.OpenGate("WI-001", "Which cache?", []string{"redis", "in-process"}, "", now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus("WI-001"); got != Running {
		t.Fatalf("opening a gate changed the status to %s", got)
	}
}

func TestOpenGatesBlockAdvancementUntilEveryOneIsClosed(t *testing.T) {
	state, now := gateFixture(t)
	options := []string{"yes", "no"}
	first, err := state.OpenGate("WI-001", "Ship behind a flag?", options, "", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := state.OpenGate("WI-001", "Backfill the old rows?", options, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatalf("two gates share the ID %q", first.ID)
	}
	if got := state.OpenGateCount("WI-001"); got != 2 {
		t.Fatalf("OpenGateCount = %d, want 2", got)
	}

	err = state.Start("WI-001", now)
	if err == nil {
		t.Fatal("started work with open gates")
	}
	if !strings.Contains(err.Error(), "gate") {
		t.Fatalf("error %q does not name the gate that blocks the work", err)
	}
	if err := state.Verifiable("WI-001"); err == nil {
		t.Fatal("verified work with open gates")
	}
	if _, ok := state.Next(); ok {
		t.Fatal("next selected work with open gates")
	}

	// Work on the same Goal that carries no Gate stays selectable: blocking is
	// per Work Item, not per queue.
	third, err := state.AddWork("g", "specs/stories/c", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	next, ok := state.Next()
	if !ok || next.ID != third.ID {
		t.Fatalf("next = %#v, %v, want %s", next, ok, third.ID)
	}
}

// TestGatesCannotBeOpenedOnCompletedWork keeps the Gate set meaningful: DONE is
// terminal, so a question that could block it can no longer be asked.
func TestGatesCannotBeOpenedOnCompletedWork(t *testing.T) {
	state, now := gateFixture(t)
	state.WorkItems[0].Status = Done
	if _, err := state.OpenGate("WI-001", "Which cache?", []string{"redis", "in-process"}, "", now); err == nil {
		t.Fatal("opened a gate on completed work")
	}
}
