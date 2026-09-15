package forgepilot_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/process"
	"github.com/CarlLee1983/ForgePilot/internal/runner"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
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
	record, err := runner.Start(runnerOptions(fixture.root, "queue", &output))
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
		if code != 2 || !strings.Contains(out, "RECOVERY_BLOCKED") {
			t.Fatalf("%s: exit = %d, want 2 with RECOVERY_BLOCKED\n%s", attempt.name, code, out)
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

	// Once the group this test owns is confirmed gone, a legitimate start opens
	// the workspace again — without anybody editing a record by hand.
	stopGroup(t, stillRunning)
	out, _ := fixture.runForge(t, agent, "run", "resume", record.RunID)
	if strings.Contains(out, "RECOVERY_BLOCKED") {
		t.Fatalf("a confirmed-empty group still blocked the workspace\n%s", out)
	}
	if pending := unresolvedPending(t, loadRunRecord(t, fixture.root, record.RunID)); len(pending) != 0 {
		t.Fatalf("the confirmed group is still recorded as unresolved: %v", pending)
	}
}

// unresolvedPending reads back the entries a stored record still cannot account
// for. It reads the persisted JSON rather than the in-memory record, because the
// question is what the next process finds.
func unresolvedPending(t *testing.T, stored map[string]any) []map[string]any {
	t.Helper()
	entries, _ := stored["pending"].([]any)
	var unresolved []map[string]any
	for _, entry := range entries {
		pending, ok := entry.(map[string]any)
		if ok && pending["unresolved"] == true {
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
