package forgepilot_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// When the repository facts needed to judge staleness cannot be read, status
// must not report the Evidence as fresh. It says staleness is unknown, and so
// does the next action it embeds, which agrees with `next` failing on its own.
func TestStatusSaysStalenessIsUnknownWhenGitFactsCannotBeRead(t *testing.T) {
	root, binary := fixture(t)
	reviewable(t, binary, root)

	gitDir, hidden := filepath.Join(root, ".git"), filepath.Join(root, ".git-hidden")
	if err := os.Rename(gitDir, hidden); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(hidden, gitDir)

	if output, err := command(binary, root, "next"); err == nil {
		t.Fatalf("next without Git facts = %q, want an error", output)
	}
	text, err := command(binary, root, "status")
	if err != nil || !strings.Contains(text, "staleness unknown:") {
		t.Fatalf("status = %q, %v; want the unknown staleness marked", text, err)
	}
	if strings.Contains(text, "Next: WI") || !strings.Contains(text, "Next: unknown") {
		t.Fatalf("status embeds a next action it cannot decide: %q", text)
	}

	status := jsonOf(t, binary, root, "status", "--json")
	item := status["goals"].([]any)[0].(map[string]any)["work_items"].([]any)[0].(map[string]any)
	if stale, present := item["verification_stale"]; !present || stale != nil {
		t.Fatalf("verification_stale = %v (present %v), want null", stale, present)
	}
	if reason, _ := item["stale_unknown_reason"].(string); reason == "" {
		t.Fatalf("stale_unknown_reason is empty: %v", item)
	}
	if next := status["next"].(map[string]any); next["action"] != "UNKNOWN" || next["reason"] == "" {
		t.Fatalf("embedded next = %v, want UNKNOWN with a reason", next)
	}
}

func TestReviewRejectIsRefusedWhileAnotherWorkItemHoldsTheWorkspace(t *testing.T) {
	root, binary := fixture(t)
	reviewable(t, binary, root)
	second := addWork(t, binary, root, "queue", "specs/stories/b.md")
	mustRun(t, binary, root, "start", second)

	output, err := command(binary, root, "review", "reject", "WI-001", "--reason", "redo")
	if err == nil || !strings.Contains(output, second) || !strings.Contains(output, "RUNNING") {
		t.Fatalf("reject while %s runs = %q, %v; want a refusal naming it", second, output, err)
	}
	status, err := command(binary, root, "status", "--work", "WI-001")
	if err != nil || !strings.Contains(status, "Cannot advance: review reject and re-verify are refused while "+second) || !strings.Contains(status, "goal cancel queue") {
		t.Fatalf("status --work = %q, %v; want the blocked actions and the way out", status, err)
	}

	// Passing the occupant verifies it into REVIEW, which frees the slot.
	mustRun(t, binary, root, "verify", second)
	if output, err := command(binary, root, "review", "reject", "WI-001", "--reason", "redo"); err != nil {
		t.Fatalf("reject after the slot was freed = %q, %v", output, err)
	}
}

func TestStatusWorkJSONIsScopedToItsGoal(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	createGoal(t, binary, root, "one", "One", false)
	createGoal(t, binary, root, "two", "Two", false)
	addWork(t, binary, root, "one", "specs/stories/a.md")
	second := addWork(t, binary, root, "two", "specs/stories/b.md")

	status := jsonOf(t, binary, root, "status", "--work", second, "--json")
	goals := status["goals"].([]any)
	if len(goals) != 1 || goals[0].(map[string]any)["id"] != "two" {
		t.Fatalf("goals = %v, want only the Goal of %s", goals, second)
	}
}

// With nothing for an agent to do the plain status lists what is being waited
// on, exactly as `next` does, instead of a bare "Next: none".
func TestStatusListsWhatItWaitsOn(t *testing.T) {
	root, binary := fixture(t)
	reviewable(t, binary, root)

	text, err := command(binary, root, "status")
	if err != nil || !strings.Contains(text, "Waiting: WI-001") || !strings.Contains(text, "Reason: human review required") || strings.Contains(text, "Next: none") {
		t.Fatalf("status = %q, %v; want the waiting list", text, err)
	}
}
