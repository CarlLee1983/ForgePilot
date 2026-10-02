package forgepilot_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// statusesOf reads the durable status of every Work Item and of the Goal, so an
// assertion is about what a separate process would find rather than about what
// a command printed.
func statusesOf(t *testing.T, root, goalID string) (map[string]string, work.GoalStatus) {
	t.Helper()
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, item := range state.WorkItems {
		statuses[item.ID] = state.DisplayStatus(item.ID)
	}
	goal, ok := state.GoalByID(goalID)
	if !ok {
		t.Fatalf("goal %q not found", goalID)
	}
	return statuses, goal.Status
}

func wantStatuses(t *testing.T, root, goalID string, want map[string]string, wantGoal work.GoalStatus) {
	t.Helper()
	got, goal := statusesOf(t, root, goalID)
	for id, status := range want {
		if got[id] != status {
			t.Errorf("%s is %s, want %s", id, got[id], status)
		}
	}
	if goal != wantGoal {
		t.Errorf("goal %s is %s, want %s", goalID, goal, wantGoal)
	}
}

// Without an Approval Requirement a PASS is the whole completion decision: the
// work is DONE, its dependent is unlocked in the same transaction, and the last
// PASS completes the Goal. The first PASS names an older revision than the
// second, and the Goal completes anyway (ADR-0040).
func TestPassCompletesWorkUnlocksDownstreamAndCompletesTheGoal(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	writeVerify(t, root, passingVerify)
	mustRun(t, binary, root, "goal", "import", writePlanText(t, planText("chain", false,
		"first specs/stories/a.md",
		"second specs/stories/b.md first",
	)))

	mustRun(t, binary, root, "start", "first")
	output, err := command(binary, root, "verify", "first")
	if err != nil {
		t.Fatalf("verify first = %v: %s", err, output)
	}
	for _, want := range []string{"first DONE", "second READY"} {
		if !strings.Contains(output, want) {
			t.Fatalf("verify output %q lacks %q", output, want)
		}
	}
	if strings.Contains(output, "COMPLETED") {
		t.Fatalf("the Goal completed with work remaining: %s", output)
	}
	// What a separate process reads back: DONE and the unlock landed together,
	// and the Goal is still open.
	wantStatuses(t, root, "chain", map[string]string{"first": "DONE", "second": "READY"}, work.GoalActive)
	if output, err := command(binary, root, "next"); err != nil || !strings.Contains(output, "Next: second") || !strings.Contains(output, "Action: forgepilot start second") {
		t.Fatalf("next after the first PASS = %q, %v", output, err)
	}

	// HEAD moves before the last item passes: the first PASS is now at an older
	// revision, and DONE work is never stale.
	if err := os.WriteFile(filepath.Join(root, "later.txt"), []byte("later\n"), 0644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, root, "move HEAD")
	if output, err := command(binary, root, "status"); err != nil || strings.Contains(output, "stale") {
		t.Fatalf("DONE work reported stale: %q, %v", output, err)
	}

	mustRun(t, binary, root, "start", "second")
	output, err = command(binary, root, "verify", "second")
	if err != nil {
		t.Fatalf("verify second = %v: %s", err, output)
	}
	for _, want := range []string{"second DONE", "Goal chain COMPLETED"} {
		if !strings.Contains(output, want) {
			t.Fatalf("verify output %q lacks %q", output, want)
		}
	}
	wantStatuses(t, root, "chain", map[string]string{"first": "DONE", "second": "DONE"}, work.GoalCompleted)

	if output, err := command(binary, root, "next"); err != nil || !strings.Contains(output, "Goal chain is completed") {
		t.Fatalf("next after the Goal completed = %q, %v", output, err)
	}
	if output, err := command(binary, root, "status"); err != nil || !strings.Contains(output, "Goal chain COMPLETED") {
		t.Fatalf("status after the Goal completed = %q, %v", output, err)
	}
	// DONE is terminal: there is no way back in.
	for _, arguments := range [][]string{{"start", "first"}, {"verify", "second"}, {"goal", "cancel", "chain", "--reason", "late"}} {
		if output, err := command(binary, root, arguments...); err == nil {
			t.Fatalf("%v succeeded against a completed Goal: %s", arguments, output)
		}
	}
}

// A failing check returns the work to RUNNING under either policy, and a Goal
// with other work still open does not complete when one item does.
func TestFailKeepsWorkRunningAndAGoalWithOpenWorkDoesNotComplete(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	writeVerify(t, root, failingVerify)
	mustRun(t, binary, root, "goal", "import", writePlanText(t, planText("pair", false,
		"left specs/stories/a.md",
		"right specs/stories/b.md",
	)))
	mustRun(t, binary, root, "start", "left")
	if output, err := command(binary, root, "verify", "left"); err != nil || !strings.Contains(output, "FAIL") || !strings.Contains(output, "left RUNNING") {
		t.Fatalf("verify of a failing check = %q, %v", output, err)
	}
	wantStatuses(t, root, "pair", map[string]string{"left": "RUNNING", "right": "READY"}, work.GoalActive)

	writeVerify(t, root, passingVerify)
	mustRun(t, binary, root, "verify", "left")
	wantStatuses(t, root, "pair", map[string]string{"left": "DONE", "right": "READY"}, work.GoalActive)
	mustRun(t, binary, root, "start", "right")
	mustRun(t, binary, root, "verify", "right")
	wantStatuses(t, root, "pair", map[string]string{"left": "DONE", "right": "DONE"}, work.GoalCompleted)
}

// With an Approval Requirement a PASS waits in REVIEW. Approval completes the
// work and unlocks downstream; rejection sends it back to RUNNING; and a REVIEW
// whose Candidate went stale cannot be approved until it is verified again.
func TestApprovalRequirementRoutesPassThroughReview(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	writeVerify(t, root, passingVerify)
	mustRun(t, binary, root, "goal", "import", writePlanText(t, planText("careful", true,
		"first specs/stories/a.md",
		"second specs/stories/b.md first",
	)))

	mustRun(t, binary, root, "start", "first")
	output, err := command(binary, root, "verify", "first")
	if err != nil || !strings.Contains(output, "first REVIEW") || strings.Contains(output, "second READY") {
		t.Fatalf("verify first = %q, %v; want a PASS that waits in REVIEW and unlocks nothing", output, err)
	}
	wantStatuses(t, root, "careful", map[string]string{"first": "REVIEW", "second": "PENDING"}, work.GoalActive)
	if output, err := command(binary, root, "next"); err != nil || !strings.Contains(output, "Waiting: first") || !strings.Contains(output, "human review required") {
		t.Fatalf("next while REVIEW = %q, %v", output, err)
	}

	output, err = command(binary, root, "review", "approve", "first", "--note", "ok")
	if err != nil || !strings.Contains(output, "first DONE") || !strings.Contains(output, "second READY") {
		t.Fatalf("approve first = %q, %v", output, err)
	}
	wantStatuses(t, root, "careful", map[string]string{"first": "DONE", "second": "READY"}, work.GoalActive)

	// Rejection returns the work to RUNNING; only a fresh PASS gets it back to REVIEW.
	mustRun(t, binary, root, "start", "second")
	mustRun(t, binary, root, "verify", "second")
	if output, err := command(binary, root, "review", "reject", "second"); err == nil {
		t.Fatalf("rejected without a reason: %s", output)
	}
	output, err = command(binary, root, "review", "reject", "second", "--reason", "needs a test")
	if err != nil || !strings.Contains(output, "second RUNNING") {
		t.Fatalf("reject second = %q, %v", output, err)
	}
	wantStatuses(t, root, "careful", map[string]string{"second": "RUNNING"}, work.GoalActive)
	if output, err := command(binary, root, "review", "approve", "second"); err == nil {
		t.Fatalf("approved work that is RUNNING: %s", output)
	}
	mustRun(t, binary, root, "verify", "second")
	wantStatuses(t, root, "careful", map[string]string{"second": "REVIEW"}, work.GoalActive)

	// HEAD moves: the REVIEW is stale, approval is refused and says why, and next
	// recommends verifying again.
	if err := os.WriteFile(filepath.Join(root, "later.txt"), []byte("later\n"), 0644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, root, "move HEAD")
	output, err = command(binary, root, "review", "approve", "second")
	if err == nil || !strings.Contains(output, "stale") {
		t.Fatalf("approve of a stale REVIEW = %q, %v; want a refusal that says stale", output, err)
	}
	wantStatuses(t, root, "careful", map[string]string{"second": "REVIEW"}, work.GoalActive)
	state, loadErr := storage.Load(root)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	for _, evidence := range state.Evidence {
		if evidence.Type == work.ReviewEvidence && evidence.WorkItemID == "second" && evidence.Result == work.Approved {
			t.Fatalf("the refused approval was recorded anyway: %#v", evidence)
		}
	}
	if output, err := command(binary, root, "next"); err != nil || !strings.Contains(output, "Action: forgepilot verify second") || !strings.Contains(output, "stale") {
		t.Fatalf("next for a stale REVIEW = %q, %v", output, err)
	}

	mustRun(t, binary, root, "verify", "second")
	output, err = command(binary, root, "review", "approve", "second")
	if err != nil || !strings.Contains(output, "second DONE") || !strings.Contains(output, "Goal careful COMPLETED") {
		t.Fatalf("approve after re-verifying = %q, %v", output, err)
	}
	wantStatuses(t, root, "careful", map[string]string{"first": "DONE", "second": "DONE"}, work.GoalCompleted)
}

// Review is a boundary a Goal opts into. Without an Approval Requirement there
// is nothing to approve or reject, and the refusal says why.
func TestReviewIsRefusedWhereTheGoalRequiresNoApproval(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	writeVerify(t, root, passingVerify)
	mustRun(t, binary, root, "goal", "import", writePlanText(t, planText("fast", false,
		"a specs/stories/a.md",
		"b specs/stories/b.md",
	)))
	mustRun(t, binary, root, "start", "a")

	for _, arguments := range [][]string{
		{"review", "approve", "a"},
		{"review", "reject", "a", "--reason", "no"},
	} {
		output, err := command(binary, root, arguments...)
		if err == nil || !strings.Contains(output, "does not require approval") {
			t.Fatalf("%v = %q, %v; want a refusal that says the Goal requires no approval", arguments, output, err)
		}
	}
	// The same refusal for DONE work: a completed item is not reviewable either.
	mustRun(t, binary, root, "verify", "a")
	if output, err := command(binary, root, "review", "approve", "a"); err == nil || !strings.Contains(output, "does not require approval") {
		t.Fatalf("approve of DONE work = %q, %v", output, err)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, evidence := range state.Evidence {
		if evidence.Type == work.ReviewEvidence {
			t.Fatalf("a refused review left Evidence: %#v", evidence)
		}
	}
}

// Approval is held back by an open Gate rather than recorded and left
// incomplete: the same command works once the Gate closes.
func TestApprovalIsRefusedWhileAGateIsOpenAndWorksOnceItCloses(t *testing.T) {
	root, binary := fixture(t)
	revision := reviewable(t, binary, root)
	mustRun(t, binary, root, "gate", "open", "--work", "WI-001", "--question", "Ship it?", "--option", "yes", "--option", "no")

	output, err := command(binary, root, "review", "approve", "WI-001")
	if err == nil || !strings.Contains(output, "GATE-001") {
		t.Fatalf("approve under an open gate = %q, %v; want a refusal naming the gate", output, err)
	}
	wantStatuses(t, root, "queue", map[string]string{"WI-001": "REVIEW", "WI-002": "PENDING"}, work.GoalActive)

	mustRun(t, binary, root, "gate", "resolve", "GATE-001", "--option", "yes")
	output, err = command(binary, root, "review", "approve", "WI-001")
	if err != nil || !strings.Contains(output, "WI-001 DONE") || !strings.Contains(output, revision[:12]) {
		t.Fatalf("approve after the gate closed = %q, %v", output, err)
	}
	wantStatuses(t, root, "queue", map[string]string{"WI-001": "DONE", "WI-002": "READY"}, work.GoalActive)
}

// Cancelling a Goal while a run is in flight still records what the run found,
// but a PASS under a cancelled Goal completes nothing.
func TestCancellingMidRunStillRecordsTheEvidenceWithoutCompleting(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	createGoal(t, binary, root, "queue", "Queue", false)
	addWork(t, binary, root, "queue", "specs/stories/a.md")
	mustRun(t, binary, root, "start", "WI-001")
	revision := writeVerify(t, root, "verify:\n\t@sleep 2\n")

	running := startVerify(t, binary, root, "WI-001")
	mustRun(t, binary, root, "goal", "cancel", "queue", "--reason", "the direction is wrong")
	if err := running.Wait(); err != nil {
		t.Fatalf("verification did not finish after the goal was cancelled: %v", err)
	}

	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	latest, ok := state.LatestVerification("WI-001")
	if !ok {
		t.Fatal("a run underway when the goal was cancelled lost its evidence")
	}
	if latest.Result != work.Pass || latest.Revision != revision {
		t.Fatalf("evidence = %#v, want PASS at %s", latest, revision)
	}
	wantStatuses(t, root, "queue", map[string]string{"WI-001": "RUNNING"}, work.GoalCancelled)
}

// A Goal has exactly one way to stop other than finishing, and it needs a
// reason. The BLOCKED state, and the commands that entered and left it, are gone.
func TestGoalCancelNeedsAReasonAndTheOtherGoalCommandsAreGone(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	createGoal(t, binary, root, "queue", "Queue", true)
	addWork(t, binary, root, "queue", "specs/stories/a.md")

	if output, err := command(binary, root, "goal", "cancel", "queue"); err == nil || !strings.Contains(output, "--reason") {
		t.Fatalf("cancel without a reason = %q, %v", output, err)
	}
	if output, err := command(binary, root, "goal", "cancel", "missing", "--reason", "x"); err == nil || !strings.Contains(output, "unknown goal") {
		t.Fatalf("cancel of an unknown goal = %q, %v", output, err)
	}
	if output, err := command(binary, root, "goal", "cancel", "queue", "--reason", "no longer needed"); err != nil || !strings.Contains(output, "CANCELLED") {
		t.Fatalf("cancel = %q, %v", output, err)
	}
	wantStatuses(t, root, "queue", map[string]string{"WI-001": "NOT_STARTED"}, work.GoalCancelled)
	if output, err := command(binary, root, "goal", "cancel", "queue", "--reason", "again"); err == nil {
		t.Fatalf("cancelled a cancelled goal: %s", output)
	}
}

// What the lifecycle used to have and no longer does: not as hidden commands,
// but as commands and flags the CLI has never heard of.
func TestRemovedLifecycleSurfaceIsUnknownAndAbsentFromHelp(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	createGoal(t, binary, root, "queue", "Queue", true)
	addWork(t, binary, root, "queue", "specs/stories/a.md")

	for _, arguments := range [][]string{
		{"goal", "block", "queue", "--reason", "x"},
		{"goal", "unblock", "queue"},
		{"goal", "complete", "queue"},
		{"review", "request", "WI-001"},
		{"review", "approve", "WI-001", "--pr", "carl/forgepilot#1"},
		{"review", "reject", "WI-001", "--reason", "x", "--pr", "carl/forgepilot#1"},
		{"goal", "import", "--review-policy", "GOAL", "plan.json"},
	} {
		output, err := command(binary, root, arguments...)
		if err == nil {
			t.Errorf("%v succeeded: %s", arguments, output)
			continue
		}
		if !strings.Contains(output, "unknown") && !strings.Contains(output, "usage") {
			t.Errorf("%v failed without saying the command or flag is unknown: %s", arguments, output)
		}
	}
	// The --pr refusals above prove nothing if the flag was merely rejected for
	// another reason: with the flag removed the same command must be valid.
	if output, err := command(binary, root, "review", "approve", "WI-001", "--pr", "carl/forgepilot#1"); err == nil || !strings.Contains(output, "unknown flag --pr") {
		t.Errorf("review approve --pr = %q, %v; want an unknown-flag refusal", output, err)
	}

	help, err := command(binary, root, "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"goal block", "unblock", "goal complete", "review request", "--pr", "VERIFIED", "review-policy", "policy"} {
		if strings.Contains(help, removed) {
			t.Errorf("help still mentions %q:\n%s", removed, help)
		}
	}
	for _, kept := range []string{"goal cancel", "review approve", "review reject", "goal import"} {
		if !strings.Contains(help, kept) {
			t.Errorf("help lost %q:\n%s", kept, help)
		}
	}
}
