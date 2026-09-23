package forgepilot_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/process"
	"github.com/CarlLee1983/ForgePilot/internal/runner"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// A verification whose workspace facts read leaves a process group nobody could
// confirm empty, and whose own transaction then fails to replace state.json.
// Two failures, and only one of them is about storage.
//
// The recovery blocking has to survive the other one. Before this it did not:
// internal/app returned the transaction's error before it recorded the group, so
// the Runner saw a result with no Cleanup, resolved the pending it had written
// before the verification started, and reported an ordinary operational failure
// — leaving the next CLI process to find a workspace that looked clear while a
// group was still running in it.
//
// The initial double failure is driven in-process because both seams are
// internal and deliberately unreachable from the CLI. What has to hold in a real
// operating-system process — that every door back into this workspace stays shut
// until the group is confirmed gone — is then asserted through the real binary.
// See docs/adr/0022-pending-cleanup-outlives-the-process.md.

// errRunStateSaveFailed is the operational failure the state replacement reports.
var errRunStateSaveFailed = errors.New("injected: the run's state could not be replaced")

// failFactsRefreshThenStateSave makes the verification's own post-check facts
// read report an unconfirmed group, and arms — from inside that failure, so it
// can land nowhere else — a one-shot failure of the state replacement that same
// transaction is about to make.
//
// `rev-parse --absolute-git-dir` is only run by InspectSnapshot, and only after
// the canonical check has announced that it ran is the first such read the one
// inside app.Verify: the between-steps readiness reads before it need no
// snapshot digest, because nothing is VERIFIED yet.
//
// The group named is one this test started and still owns. Naming the Git
// child's own group would name a group that really is empty, and the next
// recovery would clear it correctly — so there would be nothing left to assert.
func failFactsRefreshThenStateSave(t *testing.T, root, marker string, pgid int) func() (int, int) {
	t.Helper()
	var mutex sync.Mutex
	var refreshHits, saveHits int
	var disarmSave func()
	t.Cleanup(func() {
		if disarmSave != nil {
			disarmSave()
		}
	})
	restore := process.InjectCleanupFailure(func(_ int, argv []string, _ time.Duration) error {
		if !strings.Contains(strings.Join(argv, " "), "--absolute-git-dir") {
			return nil
		}
		if _, err := os.Stat(marker); err != nil {
			return nil
		}
		mutex.Lock()
		if refreshHits > 0 {
			mutex.Unlock()
			return nil
		}
		refreshHits++
		// Unlocked before the storage seam is armed: the failure that seam runs
		// takes this mutex, so holding it across the arming would be the two locks
		// in opposite orders. Unreachable today — both happen on one goroutine, in
		// order — but the order is what a later reader would copy.
		mutex.Unlock()
		disarmSave = storage.InjectStateSaveFailure(root, func() error {
			mutex.Lock()
			defer mutex.Unlock()
			saveHits++
			return errRunStateSaveFailed
		})
		return &process.NotSettled{PGID: pgid, Reason: "injected: the group could not be confirmed"}
	})
	t.Cleanup(restore)
	return func() (int, int) {
		mutex.Lock()
		defer mutex.Unlock()
		restore()
		return refreshHits, saveHits
	}
}

func TestRunnerPreservesPendingWhenStateSaveFails(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md", "b.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"}, []string{"specs/stories/b.md"})
	fixture.seedGoal(t, "second", []string{"specs/stories/b.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)
	t.Setenv("FORGEPILOT_FAKE_AGENT", agent)

	// The check announces that it has run, which is what separates the facts read
	// inside the verification from the readiness reads between steps.
	marker := filepath.Join(t.TempDir(), "check-ran")
	fixture.replaceCanonicalCheck(t, ": > "+marker+"\nexit 0\n")

	stillRunning := liveGroup(t)
	observe := failFactsRefreshThenStateSave(t, fixture.root, marker, stillRunning)

	var output bytes.Buffer
	record, err := runner.Start(runnerOptions(fixture, "queue", &output))
	refreshHits, saveHits := observe()
	if err != nil {
		t.Fatalf("the run failed operationally: %v\n%s", err, output.String())
	}
	if refreshHits != 1 || saveHits != 1 {
		t.Fatalf("the two failures did not both fire once: refresh = %d, save = %d\n%s",
			refreshHits, saveHits, output.String())
	}
	if record.Stop == nil || record.Stop.Reason != runner.StopRecoveryBlocked {
		t.Fatalf("stop = %#v, want RECOVERY_BLOCKED\n%s", record.Stop, output.String())
	}
	// Why it stopped still names the storage failure: a cleanup failure must not
	// erase the other thing that went wrong.
	if !strings.Contains(record.Stop.Detail, errRunStateSaveFailed.Error()) {
		t.Fatalf("the original state save failure is not in the stop detail: %s", record.Stop.Detail)
	}

	// The blocking is durable, in the field a resume does not clear, and it names
	// the group the failure actually reported.
	stored := loadRunRecord(t, fixture.root, record.RunID)
	pending := unresolvedPending(t, stored)
	if len(pending) != 1 {
		t.Fatalf("want exactly one unresolved pending execution, got %v\n%s", stored["pending"], output.String())
	}
	entry := pending[0]
	if entry["kind"] != runner.KindGit {
		t.Fatalf("the pending execution is a %v, want %s", entry["kind"], runner.KindGit)
	}
	if entry["location"] != fixture.root {
		t.Fatalf("the pending execution points at %v, not the workspace %s", entry["location"], fixture.root)
	}
	identity, _ := entry["identity"].(map[string]any)
	if identity == nil || identity["pgid"] != float64(stillRunning) {
		t.Fatalf("the pending execution names group %v, want %d", identity["pgid"], stillRunning)
	}
	// Nothing was invented for the transaction that failed: no Evidence claimed,
	// and no next work item started.
	// Every Evidence id this run claims must exist in state. The transaction that
	// failed built one in memory and it must not be among them — an empty list is
	// the expected answer here, and asserting the count is what keeps this from
	// being a loop that passes by never running.
	if len(record.EvidenceIDs) != 0 {
		t.Fatalf("the run listed Evidence for a transaction that never landed: %v", record.EvidenceIDs)
	}
	for _, id := range record.EvidenceIDs {
		if evidence := loadEvidenceID(t, fixture.root, id); evidence == "" {
			t.Fatalf("the run claims Evidence %s that was never saved", id)
		}
	}
	sessionsBefore := fixture.sessions(t)
	if len(sessionsBefore) != 1 {
		t.Fatalf("sessions = %v, want only the first work item's\n%s", sessionsBefore, output.String())
	}
	stepsBefore, deadlineBefore := stored["steps"], stored["deadline"]
	// The same record through the typed loader, so the budget assertions after
	// the resume compare fields rather than JSON keys: a renamed or dropped field
	// must not read as "nothing was spent".
	blocked, err := runner.LoadRecord(fixture.root, record.RunID)
	if err != nil {
		t.Fatal(err)
	}

	// Every door back into this workspace, in a real process of its own.
	for _, attempt := range []struct {
		name      string
		arguments []string
	}{
		{name: "resume the same run", arguments: []string{"run", "resume", record.RunID}},
		{name: "start a new run", arguments: []string{"run", "--goal", "queue", "--runtime", "fake", "--snapshot"}},
		{name: "another goal in the same workspace", arguments: []string{"run", "--goal", "second", "--runtime", "fake", "--snapshot"}},
	} {
		out, code := fixture.runForge(t, agent, attempt.arguments...)
		if code != runner.StopRecoveryBlocked.ExitCode() || !strings.Contains(out, string(runner.StopRecoveryBlocked)) {
			t.Fatalf("%s: exit = %d, want %d with RECOVERY_BLOCKED\n%s",
				attempt.name, code, runner.StopRecoveryBlocked.ExitCode(), out)
		}
		// Blocked on the group, not on a workspace somebody broke: a state file
		// that no longer parsed, or a runtime that could not be found, would also
		// refuse — and would prove nothing about this.
		if !strings.Contains(out, runner.KindGit+" for ") || !strings.Contains(out, "process group") {
			t.Fatalf("%s: the refusal does not name the unresolved Git group\n%s", attempt.name, out)
		}
		if sessions := fixture.sessions(t); len(sessions) != len(sessionsBefore) {
			t.Fatalf("%s started a session behind a blocked recovery: %v", attempt.name, sessions)
		}
		after := loadRunRecord(t, fixture.root, record.RunID)
		if after["steps"] != stepsBefore || after["deadline"] != deadlineBefore {
			t.Fatalf("%s spent the blocked run's budget: steps %v -> %v, deadline %v -> %v",
				attempt.name, stepsBefore, after["steps"], deadlineBefore, after["deadline"])
		}
		if len(unresolvedPending(t, after)) != 1 {
			t.Fatalf("%s cleared the recovery blocking: %v", attempt.name, after["pending"])
		}
	}

	// Once the group this test owns is confirmed gone, a legitimate resume opens
	// the workspace again — without anybody editing a record by hand — and then
	// carries the same run all the way to Goal completion.
	// "No longer blocked" is not the claim being made here: the claim is that the
	// machine finished its half of the work, so the exit code and the persisted
	// stop reason are what is asserted, not the absence of a word in the output.
	stopGroup(t, stillRunning)
	out, code := fixture.runForge(t, agent, "run", "resume", record.RunID)
	if code != runner.ExitGoalCompleted {
		t.Fatalf("resume exit = %d, want %d (GOAL_COMPLETED)\n%s", code, runner.ExitGoalCompleted, out)
	}
	resumed, err := runner.LoadRecord(fixture.root, record.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Stop == nil || resumed.Stop.Reason != runner.StopGoalCompleted {
		t.Fatalf("the stored stop is %#v, want %s\n%s", resumed.Stop, runner.StopGoalCompleted, out)
	}
	// The same run, not a fresh one that happened to finish the work.
	if resumed.RunID != record.RunID {
		t.Fatalf("the resumed record is run %s, not %s", resumed.RunID, record.RunID)
	}
	if pending := resumed.UnresolvedPending(); len(pending) != 0 {
		t.Fatalf("the confirmed group is still recorded as unresolved: %v", pending)
	}
	if resumed.Worker != nil {
		t.Fatalf("the finished run still names a live worker: %#v", resumed.Worker)
	}

	// The second Work Item got a session of its own, and it was briefed as
	// itself. A session count alone would pass for a second attempt at the first
	// item, which is a different run.
	sessionsAfter := fixture.sessions(t)
	if len(sessionsAfter) <= len(sessionsBefore) {
		t.Fatalf("sessions = %v, want the second work item to have been started too", sessionsAfter)
	}
	started := 0
	for _, item := range sessionsAfter {
		if item == "WI-002" {
			started++
		}
	}
	if started != 1 {
		t.Fatalf("WI-002 ran %d sessions, want exactly one: %v", started, sessionsAfter)
	}
	// The story reference is the load-bearing half: the fixture agent takes the
	// Work Item id out of this same briefing, so a briefing that names the item
	// says little on its own, while the Story it points at is the engineering
	// contract the session was actually handed.
	briefing := fixture.handoff(t, "WI-002")
	for _, required := range []string{"# ForgePilot work item WI-002", "`specs/stories/b.md`"} {
		if !strings.Contains(briefing, required) {
			t.Fatalf("WI-002's briefing does not contain %q:\n%s", required, briefing)
		}
	}
	if strings.Contains(briefing, "specs/stories/a.md") {
		t.Fatalf("WI-002 was briefed on the first work item's story:\n%s", briefing)
	}

	// The budget continued; it was not reset. A resume that restarted the clock
	// or the step count would turn every limit into a suggestion, and the
	// reclaim, reconcile and re-verification this recovery legitimately spends
	// steps on are exactly what would hide underneath a reset. What the total
	// should be is deliberately not asserted: those are lawful steps whose count
	// is an implementation detail.
	if !resumed.Deadline.Equal(blocked.Deadline) {
		t.Fatalf("the resume moved the deadline: %s -> %s", blocked.Deadline, resumed.Deadline)
	}
	if resumed.Steps <= blocked.Steps {
		t.Fatalf("steps went %d -> %d; the resume did no work, or it reset the count", blocked.Steps, resumed.Steps)
	}
	if resumed.Steps > resumed.Budget.MaxSteps {
		t.Fatalf("steps %d exceed the run's own budget of %d", resumed.Steps, resumed.Budget.MaxSteps)
	}
	if blocked.Attempts["WI-001"] < 1 || resumed.Attempts["WI-001"] < blocked.Attempts["WI-001"] {
		t.Fatalf("the first work item's attempts went %v -> %v", blocked.Attempts, resumed.Attempts)
	}
	if resumed.Attempts["WI-002"] < 1 {
		t.Fatalf("the second work item recorded no attempt: %v", resumed.Attempts)
	}

	// The Goal is completed from two VERIFIED work items with real PASS Evidence;
	// no Work Item was marked DONE and no Human Review was fabricated.
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	passes := map[string]string{}
	for _, id := range []string{"WI-001", "WI-002"} {
		item, found := itemByID(state, id)
		if !found {
			t.Fatalf("%s is missing from state", id)
		}
		if item.Status == work.Done {
			t.Fatalf("%s reached DONE; the runner must never complete work", id)
		}
		if item.Status != work.Verified {
			t.Fatalf("%s is %s, want VERIFIED", id, item.Status)
		}
		latest, ok := state.LatestVerification(id)
		if !ok || latest.Result != work.Pass {
			t.Fatalf("%s latest verification = %#v, want a PASS", id, latest)
		}
		passes[id] = latest.ID
	}
	// The original save failure is not allowed to have become an engineering
	// verdict, and no human judgement was invented anywhere. The verification the
	// failed transaction abandoned is closed out as INTERRUPTED — no result was
	// produced, which is not the same statement as "the code failed".
	interrupted := 0
	for _, evidence := range state.Evidence {
		if evidence.Type == work.ReviewEvidence {
			t.Fatalf("the runner recorded a human review: %#v", evidence)
		}
		if evidence.Type != work.VerificationEvidence {
			continue
		}
		if evidence.Result == work.Fail {
			t.Fatalf("a failure that was never an engineering result was recorded as FAIL: %#v", evidence)
		}
		if evidence.Result == work.Interrupted && evidence.WorkItemID == "WI-001" {
			interrupted++
		}
	}
	if interrupted != 1 {
		t.Fatalf("the abandoned verification of WI-001 was closed out %d times as INTERRUPTED, want once", interrupted)
	}
	goal, found := goalByID(state, "queue")
	if !found {
		t.Fatal("goal queue is missing from state")
	}
	if goal.Status != work.GoalCompleted {
		t.Fatalf("goal queue is %s; the runner should complete a verified Goal", goal.Status)
	}

	// The projection and durable aggregate record agree on the completion proof.
	// Only the Goal this run drove: the second Goal exists to prove cross-goal
	// blocking and was never meant to finish.
	summary, err := app.GoalReadiness(context.Background(), fixture.root, "queue")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Completion != work.GoalDoneCompletion {
		t.Fatalf("goal queue is %q, want %q", summary.Completion, work.GoalDoneCompletion)
	}
	// Every Evidence id the run finally stands on is the projection's own answer,
	// so each one is durable, current against the Candidate, and the latest PASS
	// of the Work Item it belongs to. Other Evidence from the reclaimed and
	// re-run verification is expected and not counted.
	claimed := resumed.Stop.EvidenceIDs
	completion, hasCompletion := state.GoalCompletionEvidenceFor("queue")
	if !hasCompletion || len(claimed) == 0 || claimed[0] != completion.ID {
		t.Fatalf("the run does not cite its committed Goal completion: stop=%#v completion=%#v", resumed.Stop, completion)
	}
	// Not an independent second opinion — the Runner copies this list from the
	// same projection. What it does say is that the list still holds when
	// recomputed after the run ended: Evidence that had gone stale against the
	// current Candidate would drop out of the projection and not out of the
	// record. The order is itemsByCreation, which is deterministic.
	if !reflect.DeepEqual(claimed[1:], summary.VerificationEvidenceIDs) {
		t.Fatalf("the run cites %v, the projection now cites %v", claimed[1:], summary.VerificationEvidenceIDs)
	}
	cited := map[string]bool{}
	for _, id := range claimed[1:] {
		evidence, found := evidenceByID(state, id)
		if !found {
			t.Fatalf("the run cites Evidence %s that is not in state", id)
		}
		if evidence.Type != work.VerificationEvidence || evidence.Result != work.Pass {
			t.Fatalf("cited Evidence %s is %s/%s, want a verification PASS", id, evidence.Type, evidence.Result)
		}
		if passes[evidence.WorkItemID] != id {
			t.Fatalf("cited Evidence %s belongs to %s, whose latest PASS is %s",
				id, evidence.WorkItemID, passes[evidence.WorkItemID])
		}
		cited[evidence.WorkItemID] = true
	}
	if !cited["WI-001"] || !cited["WI-002"] {
		t.Fatalf("the cited Evidence does not cover both work items: %v", claimed)
	}
}

// A run that gets past the same recovery block but whose remaining work cannot
// run is the other half of the same story: "the cleanup cleared" and "the work
// succeeded" are separate facts, and only the second one reaches a review
// boundary. The agent reports a legitimate execution_failed — an environment it
// could not run in — which is never an engineering verdict about the code.
func TestRunnerRecoveryDoesNotClaimFinalReviewAfterAgentFailure(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md", "b.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"}, []string{"specs/stories/b.md"})
	agent := fixture.fakeAgent(t, failsOnTheSecondWorkItem)
	t.Setenv("FORGEPILOT_FAKE_AGENT", agent)

	marker := filepath.Join(t.TempDir(), "check-ran")
	fixture.replaceCanonicalCheck(t, ": > "+marker+"\nexit 0\n")

	stillRunning := liveGroup(t)
	observe := failFactsRefreshThenStateSave(t, fixture.root, marker, stillRunning)

	var output bytes.Buffer
	record, err := runner.Start(runnerOptions(fixture, "queue", &output))
	refreshHits, saveHits := observe()
	if err != nil {
		t.Fatalf("the run failed operationally: %v\n%s", err, output.String())
	}
	if refreshHits != 1 || saveHits != 1 {
		t.Fatalf("the two failures did not both fire once: refresh = %d, save = %d\n%s",
			refreshHits, saveHits, output.String())
	}
	if record.Stop == nil || record.Stop.Reason != runner.StopRecoveryBlocked {
		t.Fatalf("stop = %#v, want RECOVERY_BLOCKED\n%s", record.Stop, output.String())
	}
	if len(unresolvedPending(t, loadRunRecord(t, fixture.root, record.RunID))) != 1 {
		t.Fatalf("the blocked run recorded no unresolved pending execution\n%s", output.String())
	}

	stopGroup(t, stillRunning)
	out, code := fixture.runForge(t, agent, "run", "resume", record.RunID)

	// The premise: the block really did lift. Without this the rest would pass
	// for a run that never got as far as the failing session.
	resumed := loadRunRecord(t, fixture.root, record.RunID)
	if len(unresolvedPending(t, resumed)) != 0 {
		t.Fatalf("the confirmed group is still recorded as unresolved: %v\n%s", resumed["pending"], out)
	}
	stop, _ := resumed["stop"].(map[string]any)
	if stop == nil || stop["reason"] == string(runner.StopRecoveryBlocked) {
		t.Fatalf("the resume is still blocked on recovery: %v\n%s", resumed["stop"], out)
	}
	if !containsString(fixture.sessions(t), "WI-002") {
		t.Fatalf("the second work item never got a session: %v\n%s", fixture.sessions(t), out)
	}

	// And the conclusion: an unblocked recovery is not a finished run.
	if stop["reason"] != string(runner.StopAgentExecutionFailed) {
		t.Fatalf("stop = %v, want %s", resumed["stop"], runner.StopAgentExecutionFailed)
	}
	if code != runner.StopAgentExecutionFailed.ExitCode() {
		t.Fatalf("resume exit = %d, want %d\n%s", code, runner.StopAgentExecutionFailed.ExitCode(), out)
	}
	if strings.Contains(out, string(runner.StopGoalCompleted)) {
		t.Fatalf("the run claimed Goal completion it never reached:\n%s", out)
	}

	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	item, found := itemByID(state, "WI-002")
	if !found {
		t.Fatal("WI-002 is missing from state")
	}
	if item.Status == work.Verified || item.Status == work.Done {
		t.Fatalf("WI-002 is %s after a session that could not run", item.Status)
	}
	if latest, ok := state.LatestVerification("WI-002"); ok {
		t.Fatalf("a session that never ran produced Verification Evidence: %#v", latest)
	}
	// A failure path is where a "just finish it" shortcut would be cheapest to
	// add, so Goal completion remains explicitly unclaimed here.
	for _, evidence := range state.Evidence {
		if evidence.Type == work.ReviewEvidence {
			t.Fatalf("the runner recorded a human review: %#v", evidence)
		}
	}
	for _, other := range state.WorkItems {
		if other.Status == work.Done {
			t.Fatalf("%s reached DONE; the runner must never complete work", other.ID)
		}
	}
	goal, found := goalByID(state, "queue")
	if !found {
		t.Fatal("goal queue is missing from state")
	}
	if goal.Status != work.GoalActive {
		t.Fatalf("goal queue is %s; the runner must not complete a Goal", goal.Status)
	}
	summary, err := app.GoalReadiness(context.Background(), fixture.root, "queue")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Completion == work.GoalReadyToComplete || summary.Completion == work.GoalDoneCompletion {
		t.Fatalf("goal queue is complete or ready with %s unfinished", item.ID)
	}
}

// failsOnTheSecondWorkItem implements the first work item exactly as an
// ordinary session does, and reports a legitimate execution_failed for the
// second: an environment the agent could not run in, which is the agent's
// problem and never a verdict about the repository. No product flag, no
// environment switch and no injection framework — the runtime already carries
// the outcome, and the briefing already names the item.
const failsOnTheSecondWorkItem = `if [ "$item" = "WI-002" ]; then
  printf '{"outcome":"execution_failed","summary":"no credentials","error":"not logged in"}' > "$result"
else
` + implementsCleanly + `fi
`

func itemByID(state work.State, id string) (work.Item, bool) {
	for _, item := range state.WorkItems {
		if item.ID == id {
			return item, true
		}
	}
	return work.Item{}, false
}

func goalByID(state work.State, id string) (work.Goal, bool) {
	for _, goal := range state.Goals {
		if goal.ID == id {
			return goal, true
		}
	}
	return work.Goal{}, false
}

func evidenceByID(state work.State, id string) (work.Evidence, bool) {
	for _, evidence := range state.Evidence {
		if evidence.ID == id {
			return evidence, true
		}
	}
	return work.Evidence{}, false
}

func containsString(list []string, want string) bool {
	for _, entry := range list {
		if entry == want {
			return true
		}
	}
	return false
}

// unresolvedPending reads back the entries a stored record still cannot account
// for. It reads the persisted JSON rather than the in-memory record, because the
// question is what the next process finds.
func unresolvedPending(t *testing.T, stored map[string]any) []map[string]any {
	t.Helper()
	raw, present := stored["pending"]
	if !present || raw == nil {
		return nil
	}
	entries, ok := raw.([]any)
	if !ok {
		t.Fatalf("the record's pending executions are not a list: %v", raw)
	}
	var unresolved []map[string]any
	for _, entry := range entries {
		pending, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("a pending execution is not an object: %v", entry)
		}
		if pending["unresolved"] == true {
			unresolved = append(unresolved, pending)
		}
	}
	return unresolved
}

// loadEvidenceID reports the recorded Evidence with this id, or "" when state
// holds none: a run may not list Evidence a failed transaction never published.
func loadEvidenceID(t *testing.T, root, id string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(root, ".forgepilot", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Evidence []struct {
			ID string `json:"id"`
		} `json:"evidence"`
	}
	if err := json.Unmarshal(contents, &state); err != nil {
		t.Fatal(err)
	}
	for _, evidence := range state.Evidence {
		if evidence.ID == id {
			return evidence.ID
		}
	}
	return ""
}
