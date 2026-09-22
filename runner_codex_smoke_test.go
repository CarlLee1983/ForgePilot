package forgepilot_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/runner"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// SmokeVariable opts in to driving the real Codex CLI. It is off by default and
// must stay off in CI: a green run of the fake subprocess adapter is evidence
// about ForgePilot's process, lock and recovery handling, and nothing at all
// about an unattended run against a real model.
const SmokeVariable = "FORGEPILOT_CODEX_SMOKE"

// smokeOptedIn reports whether a value of SmokeVariable opts in to spending
// model quota. The comparison is exact, and that is the whole contract: "0",
// "false", "off", "no", "true", "yes", "2" and a "1" with whitespace around it
// are every one of them off. The guard this backs used to read "non-empty means
// on", which turned each of those spellings — including the ones a person types
// to mean off — into a request to drive a real model. Trimming first would fix
// the least dangerous half of that and keep the rest, so it is not done either.
func smokeOptedIn(value string) bool { return value == "1" }

// TestCodexSmokeDrivesDependentWorkToGoalCompletion is the only test
// that talks to a model. It needs a locally installed and already-authenticated
// Codex; ForgePilot never installs or logs in on anyone's behalf. Its three
// Stories deliberately form a small vertical slice, so a PASS after each
// session comes from the disposable repository's own make verify rather than
// from an agent's completion claim.
func TestCodexSmokeDrivesDependentWorkToGoalCompletion(t *testing.T) {
	if !smokeOptedIn(os.Getenv(SmokeVariable)) {
		t.Skipf("set %s=1 to drive the real Codex CLI", SmokeVariable)
	}
	fixture := newRunnerFixture(t, "01-normalize.md", "02-cli.md", "03-errors.md")
	// The evidence export is opened before anything is started and torn down
	// before the fixture is, so a round that ends badly still leaves its inputs
	// and results behind. A round costs model quota and cannot be replayed.
	round := &smokeRound{TimeZone: time.Now().Format("MST-07:00")}
	openSmokeExport(t, fixture, round)

	writeCodexSmokeRepository(t, fixture.root)
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "smoke",
		[]string{"specs/stories/01-normalize.md"},
		[]string{"specs/stories/02-cli.md", "WI-001"},
		[]string{"specs/stories/03-errors.md", "WI-002"})

	// The budget is stated in full at the call site rather than left to the
	// product defaults: this round is a controlled one, and the limits it ran
	// under are part of what the evidence has to say.
	arguments := []string{"run", "--goal", "smoke", "--runtime", "codex", "--snapshot",
		"--max-steps", "18", "--max-attempts-per-work", "2",
		"--agent-timeout", "10m", "--max-duration", "45m", "--verify-timeout", "5m"}
	round.Command = append([]string{"forgepilot"}, arguments...)
	round.StartedAt = time.Now()
	output, code := fixture.runForge(t, "", arguments...)
	round.FinishedAt = time.Now()
	round.RunnerOutput, round.RunnerExit = output, code
	t.Logf("codex smoke output:\n%s", output)

	if runs, err := storage.ListRuns(fixture.root); err == nil && len(runs) > 0 {
		round.RunID = runs[len(runs)-1]
	}
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(output, "GOAL_COMPLETED") {
		t.Fatal("the smoke run did not complete the Goal after verification")
	}
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.WorkItems) != 3 {
		t.Fatalf("work items = %d, want 3", len(state.WorkItems))
	}
	for _, item := range state.WorkItems {
		// DONE is checked first, and on its own. It belongs to WORK_ITEM review;
		// Goal completion must leave these items VERIFIED.
		if item.Status == work.Done {
			t.Fatalf("%s is DONE; Goal completion must not rewrite Work Item lifecycle", item.ID)
		}
		if item.Status != work.Verified {
			t.Fatalf("%s status = %s, want VERIFIED", item.ID, item.Status)
		}
		evidence, ok := state.LatestVerification(item.ID)
		if !ok || evidence.Result != work.Pass || evidence.CandidateKind != work.SnapshotCandidate {
			t.Fatalf("%s evidence = %#v, want a SNAPSHOT PASS", item.ID, evidence)
		}
		t.Logf("%s: VERIFIED on %s candidate %s (%s), evidence %s",
			item.ID, evidence.CandidateKind, shortCandidate(evidence), evidence.CandidateDigest, evidence.ID)
	}

	// Goal completion writes aggregate completion evidence, never Human Review
	// Evidence; the Work Items remain VERIFIED.
	for _, evidence := range state.Evidence {
		if evidence.Type == work.ReviewEvidence {
			t.Fatalf("a review was recorded without a person: %#v", evidence)
		}
	}
	for _, goal := range state.Goals {
		if goal.ID == "smoke" && goal.Status != work.GoalCompleted {
			t.Fatalf("goal smoke is %s, want COMPLETED", goal.Status)
		}
	}

	runID := round.RunID
	if runID == "" {
		t.Fatal("no run was recorded")
	}
	record, err := runner.LoadRecord(fixture.root, runID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Stop == nil || record.Stop.Reason != runner.StopGoalCompleted {
		t.Fatalf("run %s stop = %#v, want GOAL_COMPLETED", runID, record.Stop)
	}
	if record.Stop.Reason.ExitCode() != code {
		t.Fatalf("exit = %d but %s is documented as %d", code, record.Stop.Reason, record.Stop.Reason.ExitCode())
	}
	if len(record.Stop.EvidenceIDs) != 4 || record.Stop.EvidenceIDs[0] != "GC-001" {
		t.Fatalf("run %s completion Evidence = %#v, want aggregate proof and three current PASS records", runID, record.Stop.EvidenceIDs)
	}
	// Nothing may still be recorded as executing. A worker or an unresolved
	// pending execution would mean the run ended while something it started was
	// unaccounted for, which no stop reason may hide.
	if record.Worker != nil {
		t.Fatalf("run %s still records a live worker: %#v", runID, record.Worker)
	}
	if pending := record.UnresolvedPending(); len(pending) != 0 {
		t.Fatalf("run %s still has unresolved pending executions: %#v", runID, pending)
	}
	// state.Gates is the complete history, closed ones included, so "none
	// outstanding" has to be asked of each Gate's status rather than of the
	// slice's length.
	for _, gate := range state.Gates {
		if gate.Status == work.GateOpen {
			t.Fatalf("gate %s on %s is still open: %q", gate.ID, gate.WorkItemID, gate.Question)
		}
	}

	// Each Work Item got its own session, with its own briefing and its own
	// structured result on disk.
	for _, id := range []string{"WI-001", "WI-002", "WI-003"} {
		attempts := record.Attempts[id]
		if attempts < 1 {
			t.Fatalf("run %s started no session for %s", runID, id)
		}
		for number := 1; number <= attempts; number++ {
			for _, name := range []string{"result.json", "session.log", "handoff.md"} {
				artifact := filepath.Join(fixture.root, ".forgepilot", "runs", runID,
					fmt.Sprintf("%s-attempt-%d", strings.ToLower(id), number), name)
				if _, err := os.Stat(artifact); err != nil {
					t.Fatalf("run %s has no %s artifact for %s attempt %d: %v", runID, name, id, number, err)
				}
			}
		}
	}

	// Goal completion is recomputed here, before the fixture is torn down, through
	// the same typed query the product uses. Finding the words in the run's
	// console output is not the same claim.
	summary, err := app.GoalReadiness(context.Background(), fixture.root, "smoke")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Completion != work.GoalDoneCompletion {
		t.Fatalf("recomputed goal completion = %s, want %s", summary.Completion, work.GoalDoneCompletion)
	}
	if strings.Join(summary.VerificationEvidenceIDs, ",") != strings.Join(record.Stop.EvidenceIDs[1:], ",") {
		t.Fatalf("recomputed Evidence %v does not match the run's %v",
			summary.VerificationEvidenceIDs, record.Stop.EvidenceIDs[1:])
	}

	// Checked last, and labelled, because it is a different kind of claim from
	// everything above: those are ForgePilot's contract, this is an expectation
	// about the model. A second attempt is within the budget this round was
	// given, so the Runner reaching GOAL_COMPLETED from one still
	// satisfies the contract — but the expectation is not quietly dropped
	// either. It fails the test, and the message says which of the two failed,
	// so a red run is never ambiguous about what went wrong.
	for _, id := range []string{"WI-001", "WI-002", "WI-003"} {
		if record.Attempts[id] != 1 {
			t.Errorf("SMOKE EXPECTATION FAILED (the Runner contract above held): %s took %d attempts, "+
				"the test expected one; the Runner reached %s regardless",
				id, record.Attempts[id], record.Stop.Reason)
		}
	}
	t.Logf("codex smoke run id: %s; evidence: %v; session artifacts: %s",
		runID, record.Stop.EvidenceIDs, filepath.Join(fixture.root, ".forgepilot", "runs", runID))
}

// shortCandidate names the immutable revision a piece of Evidence is bound to.
func shortCandidate(evidence work.Evidence) string {
	if len(evidence.Revision) > 12 {
		return evidence.Revision[:12]
	}
	return evidence.Revision
}

func writeCodexSmokeRepository(t *testing.T, root string) {
	t.Helper()
	write(t, filepath.Join(root, "go.mod"), "module example.com/forgepilot-runner-smoke\n\ngo 1.25.5\n")
	write(t, filepath.Join(root, "Makefile"), "verify:\n\tgo test ./...\n")
	write(t, filepath.Join(root, "AGENTS.md"), "# Smoke fixture\n\nUse the Go standard library only. Do not commit.\n")
	writeReadinessStory(t, root, "01-normalize.md", []byte(`# Normalize function

Implement package `+"`transform`"+` with `+"`Normalize(input string) (string, error)`"+`.

Acceptance criteria:
- It trims leading and trailing whitespace.
- It collapses each run of internal whitespace to one ASCII space.
- It rejects input that is empty after trimming.
- Add table-driven tests for normal, mixed-whitespace, and blank input.
- `+"`make verify`"+` must pass.
`), []byte("# acceptance 01-normalize.md\n"))
	writeReadinessStory(t, root, "02-cli.md", []byte(`# Normalize CLI

Add `+"`cmd/normalize`"+` as a command-line interface over `+"`transform.Normalize`"+`.

Acceptance criteria:
- One input argument is normalized and printed with exactly one trailing newline.
- Keep command behavior testable without spawning a subprocess.
- Add tests for a successful normalization through the command path.
- `+"`make verify`"+` must pass.
`), []byte("# acceptance 02-cli.md\n"))
	writeReadinessStory(t, root, "03-errors.md", []byte(`# CLI error handling

Finish the normalize command's error behavior.

Acceptance criteria:
- Missing input and blank input produce a non-zero command result.
- The error is written to stderr, not stdout.
- Add tests that cover both errors through the command's testable path.
- Preserve the successful behavior from the preceding Story.
- `+"`make verify`"+` must pass.
`), []byte("# acceptance 03-errors.md\n"))
	commitAll(t, root, "seed three-work-item Codex smoke fixture")
}

// A Runner drives only GOAL review policy. Under WORK_ITEM policy a person
// reviews every Work Item, so an unattended run would stop at the first one
// anyway — refusing up front says why instead of appearing to work.
func TestRunRefusesGoalsItMayNotDrive(t *testing.T) {
	fixture := newRunnerFixture(t)
	mustRun(t, fixture.binary, fixture.root, "init")
	mustRun(t, fixture.binary, fixture.root, "goal", "create", "--id", "perwork", "--title", "Per work item")
	mustRun(t, fixture.binary, fixture.root, "work", "add", "--goal", "perwork", "--story", "specs/stories/a.md")
	mustRun(t, fixture.binary, fixture.root, "goal", "create", "--id", "empty", "--title", "Empty", "--review-policy", "goal")
	agent := fixture.fakeAgent(t, implementsCleanly)

	for name, expectation := range map[string]struct{ goal, want string }{
		"work-item policy": {"perwork", "review policy"},
		"empty goal":       {"empty", "no work items"},
		"unknown goal":     {"missing", "unknown goal"},
	} {
		output, code := fixture.runForge(t, agent, "run", "--goal", expectation.goal, "--runtime", "fake", "--snapshot")
		if code != 1 || !strings.Contains(output, expectation.want) {
			t.Fatalf("%s: exit = %d, output = %s", name, code, output)
		}
		if fixture.sessions(t) != nil {
			t.Fatalf("%s started a session", name)
		}
	}
}
