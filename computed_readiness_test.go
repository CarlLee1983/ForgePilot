package forgepilot_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func readState(t *testing.T, root string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(root, ".forgepilot", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

// jsonOf runs a command whose output must be one JSON document.
func jsonOf(t *testing.T, binary, root string, arguments ...string) map[string]any {
	t.Helper()
	output, err := command(binary, root, arguments...)
	if err != nil {
		t.Fatalf("%v = %q, %v", arguments, output, err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("%v printed invalid JSON: %v\n%s", arguments, err, output)
	}
	return decoded
}

// Completing work makes its dependent READY with no write to the dependent: the
// stored record stays NOT_STARTED and status computes READY from the dependency.
func TestDoneMakesDownstreamReadyWithoutWritingIt(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	writeVerify(t, root, passingVerify)
	createGoal(t, binary, root, "queue", "Queue", false)
	first := addWork(t, binary, root, "queue", "specs/stories/a.md")
	second := addWork(t, binary, root, "queue", "specs/stories/b.md", first)

	output, err := command(binary, root, "status")
	if err != nil || !strings.Contains(output, first+" READY") || !strings.Contains(output, second+" PENDING") {
		t.Fatalf("status before = %q, %v; want %s READY and %s PENDING", output, err, first, second)
	}
	if !strings.Contains(output, "Cannot advance: depends on "+first+", which is READY") {
		t.Fatalf("status does not say why %s cannot advance: %q", second, output)
	}

	mustRun(t, binary, root, "start", first)
	mustRun(t, binary, root, "verify", first)

	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	stored := state.WorkItems[1]
	if stored.ID != second || stored.Status != work.NotStarted || !stored.UpdatedAt.Equal(stored.CreatedAt) {
		t.Fatalf("the dependent was written when its dependency finished: %#v", stored)
	}
	if !strings.Contains(readState(t, root), `"status": "NOT_STARTED"`) && !strings.Contains(readState(t, root), `"status":"NOT_STARTED"`) {
		t.Fatalf("state does not persist NOT_STARTED:\n%s", readState(t, root))
	}
	if strings.Contains(readState(t, root), "READY") || strings.Contains(readState(t, root), "PENDING") {
		t.Fatalf("state persists a readiness:\n%s", readState(t, root))
	}
	output, err = command(binary, root, "status")
	if err != nil || !strings.Contains(output, second+" READY") || strings.Contains(output, "Cannot advance") {
		t.Fatalf("status after = %q, %v; want %s READY with nothing blocking it", output, err, second)
	}
	status := jsonOf(t, binary, root, "status", "--json")
	items := status["goals"].([]any)[0].(map[string]any)["work_items"].([]any)
	if got := items[1].(map[string]any)["status"]; got != "READY" {
		t.Fatalf("status --json shows %v for the dependent, want READY", got)
	}
}

func TestStartIsRefusedWhileAnotherWorkItemHoldsTheWorkspace(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	writeVerify(t, root, passingVerify)
	createGoal(t, binary, root, "queue", "Queue", true)
	first := addWork(t, binary, root, "queue", "specs/stories/a.md")
	second := addWork(t, binary, root, "queue", "specs/stories/b.md")

	mustRun(t, binary, root, "start", first)
	output, err := command(binary, root, "start", second)
	if err == nil || !strings.Contains(output, first) || !strings.Contains(output, "RUNNING") {
		t.Fatalf("start while %s runs = %q, %v; want a refusal naming it", first, output, err)
	}
	status, err := command(binary, root, "status")
	if err != nil || !strings.Contains(status, "Cannot advance: "+first+" is RUNNING and holds the workspace") {
		t.Fatalf("status = %q, %v; want the occupant named as the reason %s waits", status, err, second)
	}

	// REVIEW does not hold the workspace: once the first PASS waits for approval
	// the second may start.
	mustRun(t, binary, root, "verify", first)
	if output, err := command(binary, root, "start", second); err != nil {
		t.Fatalf("start while %s is in REVIEW = %q, %v", first, output, err)
	}
}

// A verify that died leaves its work VERIFYING, which still holds the workspace;
// the refusal says how to recover rather than leaving the queue stuck.
func TestOrphanedVerifyingWorkHoldsTheWorkspaceAndNextRecommendsRecovery(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	createGoal(t, binary, root, "queue", "Queue", true)
	first := addWork(t, binary, root, "queue", "specs/stories/a.md")
	second := addWork(t, binary, root, "queue", "specs/stories/b.md")
	mustRun(t, binary, root, "start", first)
	writeVerify(t, root, "verify:\n\t@sleep 30\n")
	running := startVerify(t, binary, root, first)

	// While the verifier lives, next waits for it instead of offering a start.
	live := jsonOf(t, binary, root, "next", "--json")
	if live["action"] != "WAIT" || live["work_id"] != "" {
		t.Fatalf("next during a live verification = %v, want WAIT", live)
	}

	if err := running.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = running.Wait()

	output, err := command(binary, root, "start", second)
	if err == nil || !strings.Contains(output, "VERIFYING") || !strings.Contains(output, "forgepilot verify "+first) {
		t.Fatalf("start with an orphan = %q, %v; want a refusal pointing at verify %s", output, err, first)
	}
	before := readState(t, root)
	orphan := jsonOf(t, binary, root, "next", "--json")
	if readState(t, root) != before {
		t.Fatal("next reclaimed the orphan; only verify may write")
	}
	if orphan["action"] != "RECOVER" || orphan["work_id"] != first || orphan["instruction"] != "forgepilot verify "+first {
		t.Fatalf("next with an orphan = %v, want RECOVER with the verify command", orphan)
	}
}

func TestNextJSONShapeAndPriority(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	writeVerify(t, root, passingVerify)
	createGoal(t, binary, root, "queue", "Queue", true)
	first := addWork(t, binary, root, "queue", "specs/stories/a.md")
	second := addWork(t, binary, root, "queue", "specs/stories/b.md")

	wantKeys := []string{"action", "goal_id", "instruction", "reason", "status", "story_ref", "waiting", "work_id"}
	assertShape := func(next map[string]any) {
		t.Helper()
		for _, key := range wantKeys {
			if _, ok := next[key]; !ok {
				t.Fatalf("next --json lacks %q: %v", key, next)
			}
		}
		if len(next) != len(wantKeys) {
			t.Fatalf("next --json has unexpected keys: %v", next)
		}
		if _, ok := next["waiting"].([]any); !ok {
			t.Fatalf("waiting is %#v, want a list even when empty", next["waiting"])
		}
	}

	// (3) START the first READY node.
	before := readState(t, root)
	next := jsonOf(t, binary, root, "next", "--json")
	assertShape(next)
	if next["action"] != "START" || next["work_id"] != first || next["goal_id"] != "queue" || next["status"] != "READY" ||
		next["story_ref"] != "specs/stories/a.md" || next["instruction"] != "forgepilot start "+first || next["reason"] == "" {
		t.Fatalf("next = %v", next)
	}
	if readState(t, root) != before {
		t.Fatal("next wrote state")
	}

	// (1) RESUME beats starting the other READY node.
	mustRun(t, binary, root, "start", first)
	next = jsonOf(t, binary, root, "next", "--json")
	assertShape(next)
	if next["action"] != "RESUME" || next["work_id"] != first || next["status"] != "RUNNING" {
		t.Fatalf("next = %v", next)
	}

	// (3) With the first in REVIEW the second is offered; (4) once it has an open
	// Gate everything left is waiting on people, and both are listed.
	mustRun(t, binary, root, "verify", first)
	next = jsonOf(t, binary, root, "next", "--json")
	if next["action"] != "START" || next["work_id"] != second {
		t.Fatalf("next with %s in REVIEW = %v", first, next)
	}
	mustRun(t, binary, root, "gate", "open", "--work", second, "--question", "which?", "--option", "a", "--option", "b")
	next = jsonOf(t, binary, root, "next", "--json")
	assertShape(next)
	waiting := next["waiting"].([]any)
	if next["action"] != "WAIT" || len(waiting) != 2 {
		t.Fatalf("next = %v, want WAIT listing the REVIEW and the Gate", next)
	}
	review, gate := waiting[0].(map[string]any), waiting[1].(map[string]any)
	if review["kind"] != "REVIEW" || review["work_id"] != first || gate["kind"] != "GATE" || gate["work_id"] != second || gate["gate_id"] == "" {
		t.Fatalf("waiting = %v", waiting)
	}
	before = readState(t, root)
	text, err := command(binary, root, "next")
	if err != nil || !strings.Contains(text, "Waiting: "+first) || !strings.Contains(text, "Waiting: "+second) {
		t.Fatalf("next text = %q, %v", text, err)
	}
	if readState(t, root) != before {
		t.Fatal("next wrote state")
	}

	// (5) A cancelled Goal ends the conversation.
	mustRun(t, binary, root, "goal", "cancel", "queue", "--reason", "done with it")
	next = jsonOf(t, binary, root, "next", "--json")
	assertShape(next)
	if next["action"] != "GOAL_CANCELLED" || next["goal_id"] != "queue" {
		t.Fatalf("next = %v", next)
	}

	for _, arguments := range [][]string{{"next", "--bogus"}, {"next", "--json", "x"}} {
		if output, err := command(binary, root, arguments...); err == nil || !strings.Contains(output, "usage: forgepilot next") {
			t.Errorf("%v = %q, %v; want a usage failure", arguments, output, err)
		}
	}
}

func TestStatusExplainsWhyUnfinishedWorkCannotAdvance(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	writeVerify(t, root, passingVerify)
	createGoal(t, binary, root, "queue", "Queue", true)
	first := addWork(t, binary, root, "queue", "specs/stories/a.md")
	second := addWork(t, binary, root, "queue", "specs/stories/b.md", first)
	mustRun(t, binary, root, "start", first)
	mustRun(t, binary, root, "verify", first)
	mustRun(t, binary, root, "gate", "open", "--work", first, "--question", "ok?", "--option", "y", "--option", "n")

	output, err := command(binary, root, "status")
	if err != nil {
		t.Fatalf("status = %q, %v", output, err)
	}
	for _, want := range []string{
		"Cannot advance: waiting for review approve",
		"Cannot advance: open Gate GATE-001",
		"Cannot advance: depends on " + first + ", which is REVIEW",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("status lacks %q:\n%s", want, output)
		}
	}

	summary, err := command(binary, root, "status", "--work", second)
	if err != nil || !strings.Contains(summary, second+" PENDING") || !strings.Contains(summary, "Cannot advance: depends on "+first) {
		t.Fatalf("status --work = %q, %v", summary, err)
	}

	scoped := jsonOf(t, binary, root, "status", "--goal", "queue", "--work", second, "--json")
	items := scoped["goals"].([]any)[0].(map[string]any)["work_items"].([]any)
	if len(items) != 1 {
		t.Fatalf("--work did not narrow the JSON to one item: %v", items)
	}
	reasons := items[0].(map[string]any)["cannot_advance"].([]any)
	if len(reasons) != 1 || reasons[0].(map[string]any)["kind"] != "DEPENDENCY" || reasons[0].(map[string]any)["ref"] != first {
		t.Fatalf("cannot_advance = %v", reasons)
	}
	if _, ok := scoped["next"].(map[string]any)["action"]; !ok {
		t.Fatalf("status --json lacks the next action: %v", scoped)
	}

	if output, err := command(binary, root, "status", "--goal", "nope"); err == nil || !strings.Contains(output, `unknown goal "nope"`) {
		t.Errorf("unknown goal = %q, %v", output, err)
	}
}
