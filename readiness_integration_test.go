package forgepilot_test

// Readiness recovery and GOAL-policy re-verification ordering. These share the
// fixtures in integration_test.go; they live apart because they are one subject
// — a queue whose persisted readiness fell behind the facts, and the schedule
// that keeps new work from waiting behind re-verification it does not need.

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// --- Readiness reconciliation and GOAL-policy reverification ordering ---------

// goalQueue builds a GOAL-policy Goal with the given stories chained so that each
// Work Item depends on the one before it, and commits a passing canonical check.
func goalQueue(t *testing.T, binary, root string, stories ...string) {
	t.Helper()
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue", "--review-policy", "goal")
	writeVerify(t, root, passingVerify)
	for i, story := range stories {
		arguments := []string{"work", "add", "--goal", "queue", "--story", story}
		if i > 0 {
			arguments = append(arguments, "--depends-on", fmt.Sprintf("WI-%03d", i))
		}
		mustRun(t, binary, root, arguments...)
	}
}

func workStatus(t *testing.T, root, id string) work.Status {
	t.Helper()
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return state.WorkItemStatus(id)
}

func verificationEvidence(t *testing.T, root string) []work.Evidence {
	t.Helper()
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	var records []work.Evidence
	for _, evidence := range state.Evidence {
		if evidence.Type == work.VerificationEvidence {
			records = append(records, evidence)
		}
	}
	return records
}

// verificationRuns counts the Verification Runs that actually executed. Every run
// opens its own log before any Evidence exists, so the log directory is the
// independent record of how many times the canonical check was driven.
func verificationRuns(t *testing.T, root string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, ".forgepilot", "logs"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	return len(entries)
}

// TestReadinessIsRecoveredByAnExplicitReconcileCommand walks the queue through a
// Gate and a moved Candidate and back again. Persisted readiness outlives the
// condition that withdrew it, so recovering it must be a command a user can run
// rather than something they have to guess at.
func TestReadinessIsRecoveredByAnExplicitReconcileCommand(t *testing.T) {
	root, binary := fixture(t)
	goalQueue(t, binary, root, "specs/stories/a.md", "specs/stories/b.md")
	revisionA, err := headRevision(root)
	if err != nil {
		t.Fatal(err)
	}
	mustRun(t, binary, root, "start", "WI-001")
	mustRun(t, binary, root, "verify", "WI-001")
	if got := workStatus(t, root, "WI-002"); got != work.Ready {
		t.Fatalf("WI-002 after PASS = %s, want READY", got)
	}

	mustRun(t, binary, root, "gate", "open", "--work", "WI-001", "--question", "Which way?", "--option", "left", "--option", "right")
	if got := workStatus(t, root, "WI-002"); got != work.Pending {
		t.Fatalf("WI-002 behind an open Gate = %s, want PENDING", got)
	}

	// Move the repository to B, then answer the Gate. The block is gone but the
	// prerequisite's PASS no longer names the current Candidate, so readiness
	// stays withdrawn.
	if err := os.WriteFile(filepath.Join(root, "later.txt"), []byte("later\n"), 0644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, root, "move the Goal candidate to B")
	mustRun(t, binary, root, "gate", "resolve", "GATE-001", "--option", "left", "--by", "carl")
	if got := workStatus(t, root, "WI-002"); got != work.Pending {
		t.Fatalf("WI-002 at a stale prerequisite = %s, want PENDING", got)
	}
	if output, err := command(binary, root, "reconcile", "--goal", "queue"); err != nil || !strings.Contains(output, "readiness unchanged") {
		t.Fatalf("reconcile at a stale prerequisite = %q, %v", output, err)
	}

	// Back on A the prerequisite's PASS matches again, with no new verification.
	gitCommand(t, root, "reset", "--hard", revisionA)
	if got := workStatus(t, root, "WI-002"); got != work.Pending {
		t.Fatalf("WI-002 before reconcile = %s, want PENDING", got)
	}
	if output, err := command(binary, root, "start", "WI-002"); err == nil || !strings.Contains(output, "not READY") {
		t.Fatalf("start before reconcile = %q, %v", output, err)
	}
	output, err := command(binary, root, "next")
	if err != nil {
		t.Fatalf("next before reconcile = %q, %v", output, err)
	}
	for _, want := range []string{"Next: WI-002", "State: PENDING", "Action: forgepilot reconcile --goal queue"} {
		if !strings.Contains(output, want) {
			t.Fatalf("next output %q does not contain %q", output, want)
		}
	}

	output, err = command(binary, root, "reconcile", "--goal", "queue")
	if err != nil {
		t.Fatalf("reconcile = %q, %v", output, err)
	}
	for _, want := range []string{"Goal queue reconciled", "WI-002 PENDING -> READY"} {
		if !strings.Contains(output, want) {
			t.Fatalf("reconcile output %q does not contain %q", output, want)
		}
	}
	if got := workStatus(t, root, "WI-002"); got != work.Ready {
		t.Fatalf("WI-002 after reconcile = %s, want READY", got)
	}
	output, err = command(binary, root, "next")
	if err != nil || !strings.Contains(output, "Action: forgepilot start WI-002") {
		t.Fatalf("next after reconcile = %q, %v", output, err)
	}
	mustRun(t, binary, root, "start", "WI-002")

	// The whole recovery added no Verification Evidence and ran no new check.
	if records := verificationEvidence(t, root); len(records) != 1 || records[0].Result != work.Pass || records[0].Revision != revisionA {
		t.Fatalf("verification evidence after recovery = %#v", records)
	}
	if runs := verificationRuns(t, root); runs != 1 {
		t.Fatalf("verification runs after recovery = %d, want 1", runs)
	}
}

// TestReconcileFailsClosedAndStaysInsideOneGoal covers every way reconciliation
// must refuse, and proves a refusal writes nothing.
func TestReconcileFailsClosedAndStaysInsideOneGoal(t *testing.T) {
	t.Run("prerequisite is stale or gated", func(t *testing.T) {
		root, binary := fixture(t)
		goalQueue(t, binary, root, "specs/stories/a.md", "specs/stories/b.md")
		mustRun(t, binary, root, "start", "WI-001")
		mustRun(t, binary, root, "verify", "WI-001")
		if err := os.WriteFile(filepath.Join(root, "later.txt"), []byte("later\n"), 0644); err != nil {
			t.Fatal(err)
		}
		commitAll(t, root, "move the Goal candidate")
		// A stale prerequisite: WI-002 was READY, and reconciling withdraws it.
		output, err := command(binary, root, "reconcile", "--goal", "queue")
		if err != nil || !strings.Contains(output, "WI-002 READY -> PENDING") {
			t.Fatalf("reconcile at a stale prerequisite = %q, %v", output, err)
		}
		if output, err := command(binary, root, "start", "WI-002"); err == nil {
			t.Fatalf("started work behind a stale prerequisite: %q", output)
		}
		// An open Gate on the prerequisite keeps it withdrawn even once the
		// Candidate matches again.
		mustRun(t, binary, root, "verify", "WI-001")
		mustRun(t, binary, root, "gate", "open", "--work", "WI-001", "--question", "Which way?", "--option", "left", "--option", "right")
		if got := workStatus(t, root, "WI-001"); got != work.Verified {
			t.Fatalf("WI-001 = %s, want VERIFIED", got)
		}
		if output, err := command(binary, root, "reconcile", "--goal", "queue"); err != nil || !strings.Contains(output, "readiness unchanged") {
			t.Fatalf("reconcile behind an open Gate = %q, %v", output, err)
		}
		if got := workStatus(t, root, "WI-002"); got != work.Pending {
			t.Fatalf("WI-002 behind an open Gate = %s, want PENDING", got)
		}
	})

	t.Run("the work item carries its own gate", func(t *testing.T) {
		root, binary := fixture(t)
		goalQueue(t, binary, root, "specs/stories/a.md", "specs/stories/b.md")
		mustRun(t, binary, root, "start", "WI-001")
		mustRun(t, binary, root, "verify", "WI-001")
		mustRun(t, binary, root, "gate", "open", "--work", "WI-002", "--question", "Which way?", "--option", "left", "--option", "right")
		// Blocking is a condition, not a status (ADR-0007): readiness may be
		// recomputed, but the Gate still refuses to let the work start, and next
		// does not present it as actionable.
		if output, err := command(binary, root, "reconcile", "--goal", "queue"); err != nil {
			t.Fatalf("reconcile = %q, %v", output, err)
		}
		if output, err := command(binary, root, "start", "WI-002"); err == nil || !strings.Contains(output, "GATE-001") {
			t.Fatalf("start through an open Gate = %q, %v", output, err)
		}
		output, err := command(binary, root, "next")
		if err != nil || strings.Contains(output, "reconcile") || strings.Contains(output, "start WI-002") {
			t.Fatalf("next with a gated work item = %q, %v", output, err)
		}
	})

	t.Run("goals that may not be reconciled", func(t *testing.T) {
		for _, refusal := range []struct {
			name    string
			stop    []string
			goalID  string
			message string
		}{
			{name: "unknown", goalID: "missing", message: `unknown goal "missing"`},
			{name: "blocked", stop: []string{"goal", "block", "queue", "--reason", "wrong direction"}, goalID: "queue", message: "it is BLOCKED"},
			{name: "cancelled", stop: []string{"goal", "cancel", "queue", "--reason", "abandoned"}, goalID: "queue", message: "it is CANCELLED"},
		} {
			t.Run(refusal.name, func(t *testing.T) {
				root, binary := fixture(t)
				goalQueue(t, binary, root, "specs/stories/a.md", "specs/stories/b.md")
				mustRun(t, binary, root, "start", "WI-001")
				mustRun(t, binary, root, "verify", "WI-001")
				if err := os.WriteFile(filepath.Join(root, "later.txt"), []byte("later\n"), 0644); err != nil {
					t.Fatal(err)
				}
				commitAll(t, root, "move the Goal candidate")
				if refusal.stop != nil {
					mustRun(t, binary, root, refusal.stop...)
				}
				before := readState(t, root)
				output, err := command(binary, root, "reconcile", "--goal", refusal.goalID)
				if err == nil || !strings.Contains(output, refusal.message) {
					t.Fatalf("reconcile %s goal = %q, %v", refusal.name, output, err)
				}
				if after := readState(t, root); after != before {
					t.Fatalf("refused reconciliation rewrote state:\n%s", after)
				}
			})
		}
	})

	t.Run("a completed goal", func(t *testing.T) {
		root, binary := fixture(t)
		mustRun(t, binary, root, "init")
		mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
		writeVerify(t, root, passingVerify)
		mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
		mustRun(t, binary, root, "start", "WI-001")
		mustRun(t, binary, root, "verify", "WI-001")
		mustRun(t, binary, root, "review", "request", "WI-001")
		mustRun(t, binary, root, "review", "approve", "WI-001", "--by", "carl")
		mustRun(t, binary, root, "goal", "complete", "queue")
		before := readState(t, root)
		if output, err := command(binary, root, "reconcile", "--goal", "queue"); err == nil || !strings.Contains(output, "it is COMPLETED") {
			t.Fatalf("reconcile a COMPLETED goal = %q, %v", output, err)
		}
		if after := readState(t, root); after != before {
			t.Fatalf("refused reconciliation rewrote state:\n%s", after)
		}
	})

	t.Run("required repository facts cannot be read", func(t *testing.T) {
		root, binary := fixture(t)
		goalQueue(t, binary, root, "specs/stories/a.md", "specs/stories/b.md")
		mustRun(t, binary, root, "start", "WI-001")
		mustRun(t, binary, root, "verify", "WI-001")
		if err := os.WriteFile(filepath.Join(root, "later.txt"), []byte("later\n"), 0644); err != nil {
			t.Fatal(err)
		}
		commitAll(t, root, "move the Goal candidate")
		mustRun(t, binary, root, "reconcile", "--goal", "queue")
		if got := workStatus(t, root, "WI-002"); got != work.Pending {
			t.Fatalf("WI-002 = %s, want PENDING", got)
		}
		before := readState(t, root)
		// Without a readable repository the prerequisite's freshness is unknown,
		// and unknown must never be read as fresh.
		if err := os.Rename(filepath.Join(root, ".git"), filepath.Join(root, ".git.hidden")); err != nil {
			t.Fatal(err)
		}
		output, err := command(binary, root, "reconcile", "--goal", "queue")
		if err == nil || !strings.Contains(output, "resolve current Candidate") {
			t.Fatalf("reconcile without repository facts = %q, %v", output, err)
		}
		if after := readState(t, root); after != before {
			t.Fatalf("a failed fact lookup left a partial update:\n%s", after)
		}
		if err := os.Rename(filepath.Join(root, ".git.hidden"), filepath.Join(root, ".git")); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("another goal is left alone", func(t *testing.T) {
		root, binary := fixture(t)
		goalQueue(t, binary, root, "specs/stories/a.md", "specs/stories/b.md")
		mustRun(t, binary, root, "goal", "create", "--id", "other", "--title", "Other", "--review-policy", "goal")
		mustRun(t, binary, root, "work", "add", "--goal", "other", "--story", "specs/stories/c.md")
		mustRun(t, binary, root, "start", "WI-001")
		mustRun(t, binary, root, "verify", "WI-001")
		mustRun(t, binary, root, "start", "WI-003")
		mustRun(t, binary, root, "verify", "WI-003")
		// Both Goals are on the same Candidate; withdraw the first Goal's readiness
		// by hand so that reconciling it has something to do.
		if err := os.WriteFile(filepath.Join(root, "later.txt"), []byte("later\n"), 0644); err != nil {
			t.Fatal(err)
		}
		commitAll(t, root, "move the Goal candidate")
		mustRun(t, binary, root, "reconcile", "--goal", "queue")
		otherBefore := workItem(t, root, "WI-003")
		gitCommand(t, root, "rm", "-q", "later.txt")
		commitAll(t, root, "return to the earlier content")
		output, err := command(binary, root, "reconcile", "--goal", "queue")
		if err != nil {
			t.Fatalf("reconcile = %q, %v", output, err)
		}
		if got := workItem(t, root, "WI-003"); got.Status != otherBefore.Status || !got.UpdatedAt.Equal(otherBefore.UpdatedAt) {
			t.Fatalf("reconciling queue changed WI-003: %#v, was %#v", got, otherBefore)
		}
	})
}

func readState(t *testing.T, root string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(root, ".forgepilot", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func workItem(t *testing.T, root, id string) work.Item {
	t.Helper()
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range state.WorkItems {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("unknown work item %q", id)
	return work.Item{}
}

// TestConsecutiveGoalWorkDefersHistoricReverificationToTheBoundary is the
// scheduling change itself: three chained Work Items, each landing on its own
// revision. If every stale VERIFIED outranked startable work, the queue would
// re-verify all of its history between consecutive items.
func TestConsecutiveGoalWorkDefersHistoricReverificationToTheBoundary(t *testing.T) {
	root, binary := fixture(t)
	goalQueue(t, binary, root, "specs/stories/a.md", "specs/stories/b.md", "specs/stories/c.md")

	var actions []string
	step := func(id string) string {
		t.Helper()
		mustRun(t, binary, root, "start", id)
		// Each Work Item actually changes the product, so the Candidate moves.
		if err := os.WriteFile(filepath.Join(root, id+".go"), []byte("package "+strings.ToLower(strings.ReplaceAll(id, "-", ""))+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		revision := commitAll(t, root, "implement "+id)
		mustRun(t, binary, root, "verify", id)
		actions = append(actions, "VERIFY "+id)
		return revision
	}
	nextAction := func() string {
		t.Helper()
		output, err := command(binary, root, "next")
		if err != nil {
			t.Fatalf("next = %q, %v", output, err)
		}
		for _, line := range strings.Split(output, "\n") {
			if strings.HasPrefix(line, "Action: ") {
				return strings.TrimPrefix(line, "Action: ")
			}
		}
		if strings.Contains(output, "Reason: goal final review required") {
			return "WAIT_GOAL_REVIEW"
		}
		t.Fatalf("next produced no action: %q", output)
		return ""
	}

	step("WI-001")
	if got := nextAction(); got != "forgepilot start WI-002" {
		t.Fatalf("after WI-001 PASS: %q", got)
	}
	actions = append(actions, "START WI-002")
	revisionB := step("WI-002")
	// WI-001's PASS is stale at B, yet WI-003 is legally startable: the new work
	// comes first and the owed re-verification waits.
	if got := nextAction(); got != "forgepilot start WI-003" {
		t.Fatalf("after WI-002 PASS: %q", got)
	}
	actions = append(actions, "START WI-003")
	revisionC := step("WI-003")
	if revisionB == revisionC {
		t.Fatal("WI-002 and WI-003 landed on the same revision")
	}

	// WI-003's repository-wide PASS also refreshes both stale VERIFIED peers in
	// one shared run, so the Goal reaches its review boundary immediately.
	if got := nextAction(); got != "WAIT_GOAL_REVIEW" {
		t.Fatalf("after WI-003 PASS: %q", got)
	}
	actions = append(actions, "WAIT_GOAL_REVIEW")

	want := []string{"VERIFY WI-001", "START WI-002", "VERIFY WI-002", "START WI-003",
		"VERIFY WI-003", "WAIT_GOAL_REVIEW"}
	if !reflect.DeepEqual(actions, want) {
		t.Fatalf("action sequence = %v, want %v", actions, want)
	}

	// Three Verification Runs produced six Work Item-specific Evidence records:
	// the second refreshed one stale peer and the last refreshed both.
	if runs := verificationRuns(t, root); runs != 3 {
		t.Fatalf("verification runs = %d, want 3", runs)
	}
	records := verificationEvidence(t, root)
	if len(records) != 6 {
		t.Fatalf("verification evidence = %d, want 6", len(records))
	}
	atC := 0
	for _, record := range records {
		if record.Result != work.Pass {
			t.Fatalf("evidence %s is %s", record.ID, record.Result)
		}
		if record.Revision == revisionC {
			atC++
		}
	}
	// The boundary demands a current PASS for every Work Item: three of the six
	// Evidence records name C, and none of the earlier Evidence was rewritten.
	if atC != 3 {
		t.Fatalf("%d evidence records name the final revision, want 3", atC)
	}
	output, err := command(binary, root, "status")
	if err != nil || !strings.Contains(output, "Goal review: awaiting goal final review") {
		t.Fatalf("status at the boundary = %q, %v", output, err)
	}
}

// TestConsecutiveSnapshotWorkDefersReverificationWithEvolvingDigests repeats the
// same schedule for SNAPSHOT candidates, where freshness follows a workspace
// digest rather than HEAD. Each step changes the workspace, so no two runs share
// a snapshot.
func TestConsecutiveSnapshotWorkDefersReverificationWithEvolvingDigests(t *testing.T) {
	root, binary := fixture(t)
	goalQueue(t, binary, root, "specs/stories/a.md", "specs/stories/b.md", "specs/stories/c.md")
	digests := map[string]bool{}
	step := func(id, contents string) {
		t.Helper()
		mustRun(t, binary, root, "start", id)
		if err := os.WriteFile(filepath.Join(root, "workspace.txt"), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
		mustRun(t, binary, root, "verify", id, "--snapshot")
		state, err := storage.Load(root)
		if err != nil {
			t.Fatal(err)
		}
		latest, ok := state.LatestVerification(id)
		if !ok || latest.CandidateKind != work.SnapshotCandidate {
			t.Fatalf("%s latest verification = %#v", id, latest)
		}
		digests[latest.CandidateDigest] = true
	}
	nextAction := func() string {
		t.Helper()
		output, err := command(binary, root, "next")
		if err != nil {
			t.Fatalf("next = %q, %v", output, err)
		}
		for _, line := range strings.Split(output, "\n") {
			if strings.HasPrefix(line, "Action: ") {
				return strings.TrimPrefix(line, "Action: ")
			}
		}
		if strings.Contains(output, "Reason: goal final review required") {
			return "WAIT_GOAL_REVIEW"
		}
		t.Fatalf("next produced no action: %q", output)
		return ""
	}

	step("WI-001", "first\n")
	if got := nextAction(); got != "forgepilot start WI-002" {
		t.Fatalf("after WI-001: %q", got)
	}
	step("WI-002", "second\n")
	if got := nextAction(); got != "forgepilot start WI-003" {
		t.Fatalf("after WI-002: %q", got)
	}
	step("WI-003", "third\n")
	if got := nextAction(); got != "WAIT_GOAL_REVIEW" {
		t.Fatalf("after WI-003: %q", got)
	}
	// Three distinct workspaces, one per Work Item: no step re-verified the
	// snapshot a previous step had already checked. The final run fans its current
	// digest out to the two stale peers.
	if len(digests) != 3 {
		t.Fatalf("distinct snapshot digests = %d, want 3", len(digests))
	}
	if runs := verificationRuns(t, root); runs != 3 {
		t.Fatalf("verification runs = %d, want 3", runs)
	}
}

// TestMultiplePrerequisitesEachHaveToHold proves the downstream item needs every
// prerequisite to be fresh and unblocked, not just one of them.
func TestMultiplePrerequisitesEachHaveToHold(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue", "--review-policy", "goal")
	writeVerify(t, root, passingVerify)
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/b.md")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/c.md",
		"--depends-on", "WI-001", "--depends-on", "WI-002")
	mustRun(t, binary, root, "start", "WI-001")
	mustRun(t, binary, root, "verify", "WI-001")
	// One prerequisite has passed; the other has not, so nothing downstream moves.
	if got := workStatus(t, root, "WI-003"); got != work.Pending {
		t.Fatalf("WI-003 with one prerequisite = %s, want PENDING", got)
	}
	if output, err := command(binary, root, "reconcile", "--goal", "queue"); err != nil || !strings.Contains(output, "readiness unchanged") {
		t.Fatalf("reconcile with one prerequisite = %q, %v", output, err)
	}
	mustRun(t, binary, root, "start", "WI-002")
	mustRun(t, binary, root, "verify", "WI-002")
	if got := workStatus(t, root, "WI-003"); got != work.Ready {
		t.Fatalf("WI-003 with both prerequisites = %s, want READY", got)
	}

	// A Gate on either prerequisite withdraws it again, and reconciling cannot
	// put it back while the question stands.
	mustRun(t, binary, root, "gate", "open", "--work", "WI-001", "--question", "Which way?", "--option", "left", "--option", "right")
	if got := workStatus(t, root, "WI-003"); got != work.Pending {
		t.Fatalf("WI-003 behind a gated prerequisite = %s, want PENDING", got)
	}
	if output, err := command(binary, root, "reconcile", "--goal", "queue"); err != nil || !strings.Contains(output, "readiness unchanged") {
		t.Fatalf("reconcile behind a gated prerequisite = %q, %v", output, err)
	}
	mustRun(t, binary, root, "gate", "resolve", "GATE-001", "--option", "left", "--by", "carl")
	if got := workStatus(t, root, "WI-003"); got != work.Ready {
		t.Fatalf("WI-003 after the Gate closed = %s, want READY", got)
	}

	// Moving the Candidate makes both prerequisites stale at once; a single fresh
	// PASS is not enough to start the downstream work.
	if err := os.WriteFile(filepath.Join(root, "later.txt"), []byte("later\n"), 0644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, root, "move the Goal candidate")
	if output, err := command(binary, root, "start", "WI-003"); err == nil || !strings.Contains(output, "stale dependency") {
		t.Fatalf("start behind stale prerequisites = %q, %v", output, err)
	}
	if output, err := command(binary, root, "reconcile", "--goal", "queue"); err != nil || !strings.Contains(output, "WI-003 READY -> PENDING") {
		t.Fatalf("reconcile behind stale prerequisites = %q, %v", output, err)
	}
	// One explicit verification is enough here because the other prerequisite is
	// an eligible stale VERIFIED peer and receives the same canonical PASS.
	mustRun(t, binary, root, "verify", "WI-001")
	if got := workStatus(t, root, "WI-003"); got != work.Ready {
		t.Fatalf("WI-003 after shared prerequisite verification = %s, want READY", got)
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	first, firstOK := state.LatestVerification("WI-001")
	second, secondOK := state.LatestVerification("WI-002")
	if !firstOK || !secondOK || first.VerificationRunID != second.VerificationRunID {
		t.Fatalf("prerequisite runs = %#v, %#v; want one shared run", first, second)
	}
}

// TestWorkItemPolicyIsUnchangedByReadinessReconciliation keeps the default
// boundary where it was: a PASS still needs a person, and reconcile is not a way
// past that.
func TestWorkItemPolicyIsUnchangedByReadinessReconciliation(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	mustRun(t, binary, root, "goal", "create", "--id", "queue", "--title", "Queue")
	writeVerify(t, root, passingVerify)
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/a.md")
	mustRun(t, binary, root, "work", "add", "--goal", "queue", "--story", "specs/stories/b.md", "--depends-on", "WI-001")
	mustRun(t, binary, root, "start", "WI-001")
	mustRun(t, binary, root, "verify", "WI-001")
	if got := workStatus(t, root, "WI-001"); got != work.Running {
		t.Fatalf("WI-001 after PASS = %s, want RUNNING", got)
	}
	if got := workStatus(t, root, "WI-002"); got != work.Pending {
		t.Fatalf("WI-002 before approval = %s, want PENDING", got)
	}
	// No human approval yet, so no amount of reconciling unlocks the dependent.
	if output, err := command(binary, root, "reconcile", "--goal", "queue"); err != nil || !strings.Contains(output, "readiness unchanged") {
		t.Fatalf("reconcile before approval = %q, %v", output, err)
	}
	if got := workStatus(t, root, "WI-002"); got != work.Pending {
		t.Fatalf("WI-002 after reconcile = %s, want PENDING", got)
	}
	if output, err := command(binary, root, "start", "WI-002"); err == nil {
		t.Fatalf("started work behind an unapproved dependency: %q", output)
	}

	mustRun(t, binary, root, "review", "request", "WI-001")
	// A stale REVIEW still outranks other work: next sends the agent back to it.
	if err := os.WriteFile(filepath.Join(root, "later.txt"), []byte("later\n"), 0644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, root, "move HEAD under a REVIEW work item")
	output, err := command(binary, root, "next")
	if err != nil || !strings.Contains(output, "Action: forgepilot verify WI-001") {
		t.Fatalf("next with a stale REVIEW = %q, %v", output, err)
	}
	mustRun(t, binary, root, "verify", "WI-001")
	mustRun(t, binary, root, "review", "request", "WI-001")
	mustRun(t, binary, root, "review", "approve", "WI-001", "--by", "carl")
	if got := workStatus(t, root, "WI-001"); got != work.Done {
		t.Fatalf("WI-001 after approval = %s, want DONE", got)
	}
	if got := workStatus(t, root, "WI-002"); got != work.Ready {
		t.Fatalf("WI-002 after approval = %s, want READY", got)
	}
}

// TestNextStaysAQueryAndReconcileIsIdempotent pins the two purity contracts that
// make the recommendation safe to follow blindly.
func TestNextStaysAQueryAndReconcileIsIdempotent(t *testing.T) {
	root, binary := fixture(t)
	goalQueue(t, binary, root, "specs/stories/a.md", "specs/stories/b.md")
	revisionA, err := headRevision(root)
	if err != nil {
		t.Fatal(err)
	}
	mustRun(t, binary, root, "start", "WI-001")
	mustRun(t, binary, root, "verify", "WI-001")
	mustRun(t, binary, root, "gate", "open", "--work", "WI-001", "--question", "Which way?", "--option", "left", "--option", "right")
	if err := os.WriteFile(filepath.Join(root, "later.txt"), []byte("later\n"), 0644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, root, "move the Goal candidate")
	mustRun(t, binary, root, "gate", "resolve", "GATE-001", "--option", "left", "--by", "carl")
	gitCommand(t, root, "reset", "--hard", revisionA)

	stateBefore := readState(t, root)
	surfaceBefore := gitWorkspaceSurface(t, root)
	first, err := command(binary, root, "next")
	if err != nil {
		t.Fatalf("next = %q, %v", first, err)
	}
	second, err := command(binary, root, "next")
	if err != nil {
		t.Fatalf("next = %q, %v", second, err)
	}
	if first != second {
		t.Fatalf("next is not stable:\n%s\n%s", first, second)
	}
	if !strings.Contains(first, "Action: forgepilot reconcile --goal queue") {
		t.Fatalf("next = %q", first)
	}
	if after := readState(t, root); after != stateBefore {
		t.Fatal("next wrote state")
	}
	if after := gitWorkspaceSurface(t, root); after != surfaceBefore {
		t.Fatalf("next disturbed the repository:\n%s\n%s", surfaceBefore, after)
	}

	if output, err := command(binary, root, "reconcile", "--goal", "queue"); err != nil || !strings.Contains(output, "WI-002 PENDING -> READY") {
		t.Fatalf("reconcile = %q, %v", output, err)
	}
	afterFirst := readState(t, root)
	output, err := command(binary, root, "reconcile", "--goal", "queue")
	if err != nil || !strings.Contains(output, "Goal queue readiness unchanged") {
		t.Fatalf("second reconcile = %q, %v", output, err)
	}
	if after := readState(t, root); after != afterFirst {
		t.Fatalf("a second reconciliation rewrote state:\n%s\n%s", afterFirst, after)
	}
	// The recommendation moved on rather than repeating itself: a next that keeps
	// recommending a reconcile which reports nothing is the spin being ruled out.
	output, err = command(binary, root, "next")
	if err != nil || !strings.Contains(output, "Action: forgepilot start WI-002") {
		t.Fatalf("next after reconcile = %q, %v", output, err)
	}
	if records := verificationEvidence(t, root); len(records) != 1 {
		t.Fatalf("verification evidence = %d, want 1", len(records))
	}
}
