package forgepilot_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// SmokeVariable opts in to driving the real Codex CLI. It is off by default and
// must stay off in CI: a green run of the fake subprocess adapter is evidence
// about ForgePilot's process, lock and recovery handling, and nothing at all
// about an unattended run against a real model.
const SmokeVariable = "FORGEPILOT_CODEX_SMOKE"

// TestCodexSmokeDrivesDependentWorkToTheGoalReviewBoundary is the only test
// that talks to a model. It needs a locally installed and already-authenticated
// Codex; ForgePilot never installs or logs in on anyone's behalf. Its three
// Stories deliberately form a small vertical slice, so a PASS after each
// session comes from the disposable repository's own make verify rather than
// from an agent's completion claim.
func TestCodexSmokeDrivesDependentWorkToTheGoalReviewBoundary(t *testing.T) {
	if os.Getenv(SmokeVariable) == "" {
		t.Skipf("set %s=1 to drive the real Codex CLI", SmokeVariable)
	}
	fixture := newRunnerFixture(t, "01-normalize.md", "02-cli.md", "03-errors.md")
	writeCodexSmokeRepository(t, fixture.root)
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "smoke",
		[]string{"specs/stories/01-normalize.md"},
		[]string{"specs/stories/02-cli.md", "WI-001"},
		[]string{"specs/stories/03-errors.md", "WI-002"})

	output, code := fixture.runForge(t, "", "run", "--goal", "smoke", "--runtime", "codex", "--snapshot",
		"--max-steps", "18", "--max-attempts-per-work", "2", "--agent-timeout", "10m")
	t.Logf("codex smoke output:\n%s", output)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(output, "AWAITING_GOAL_REVIEW") {
		t.Fatal("the smoke run did not reach the goal review boundary")
	}
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.WorkItems) != 3 {
		t.Fatalf("work items = %d, want 3", len(state.WorkItems))
	}
	for _, item := range state.WorkItems {
		if item.Status != work.Verified {
			t.Fatalf("%s status = %s, want VERIFIED", item.ID, item.Status)
		}
		evidence, ok := state.LatestVerification(item.ID)
		if !ok || evidence.Result != work.Pass || evidence.CandidateKind != work.SnapshotCandidate {
			t.Fatalf("%s evidence = %#v, want a SNAPSHOT PASS", item.ID, evidence)
		}
	}
	runID := lastRun(t, fixture.root)
	record := loadRunRecord(t, fixture.root, runID)
	attempts, ok := record["attempts"].(map[string]any)
	if !ok || len(attempts) != 3 {
		t.Fatalf("run %s attempts = %#v, want one attempt for each Work Item", runID, record["attempts"])
	}
	for _, id := range []string{"WI-001", "WI-002", "WI-003"} {
		if attempts[id] != float64(1) {
			t.Fatalf("run %s attempt for %s = %#v, want 1", runID, id, attempts[id])
		}
		for _, name := range []string{"result.json", "session.log"} {
			artifact := filepath.Join(fixture.root, ".forgepilot", "runs", runID, strings.ToLower(id)+"-attempt-1", name)
			if _, err := os.Stat(artifact); err != nil {
				t.Fatalf("run %s has no %s artifact for %s: %v", runID, name, id, err)
			}
		}
	}
	stop, ok := record["stop"].(map[string]any)
	if !ok || stop["reason"] != "AWAITING_GOAL_REVIEW" {
		t.Fatalf("run %s stop = %#v", runID, record["stop"])
	}
	evidenceIDs, ok := stop["evidence_ids"].([]any)
	if !ok || len(evidenceIDs) != 3 {
		t.Fatalf("run %s final-review Evidence = %#v, want three current PASS records", runID, stop["evidence_ids"])
	}
	t.Logf("codex smoke run id: %s; evidence: %v; session artifacts: %s", runID, stop["evidence_ids"], filepath.Join(fixture.root, ".forgepilot", "runs", runID))
}

func writeCodexSmokeRepository(t *testing.T, root string) {
	t.Helper()
	write(t, filepath.Join(root, "go.mod"), "module example.com/forgepilot-runner-smoke\n\ngo 1.25.5\n")
	write(t, filepath.Join(root, "Makefile"), "verify:\n\tgo test ./...\n")
	write(t, filepath.Join(root, "AGENTS.md"), "# Smoke fixture\n\nUse the Go standard library only. Do not commit.\n")
	write(t, filepath.Join(root, "specs", "stories", "01-normalize.md"), `# Normalize function

Implement package `+"`transform`"+` with `+"`Normalize(input string) (string, error)`"+`.

Acceptance criteria:
- It trims leading and trailing whitespace.
- It collapses each run of internal whitespace to one ASCII space.
- It rejects input that is empty after trimming.
- Add table-driven tests for normal, mixed-whitespace, and blank input.
- `+"`make verify`"+` must pass.
`)
	write(t, filepath.Join(root, "specs", "stories", "02-cli.md"), `# Normalize CLI

Add `+"`cmd/normalize`"+` as a command-line interface over `+"`transform.Normalize`"+`.

Acceptance criteria:
- One input argument is normalized and printed with exactly one trailing newline.
- Keep command behavior testable without spawning a subprocess.
- Add tests for a successful normalization through the command path.
- `+"`make verify`"+` must pass.
`)
	write(t, filepath.Join(root, "specs", "stories", "03-errors.md"), `# CLI error handling

Finish the normalize command's error behavior.

Acceptance criteria:
- Missing input and blank input produce a non-zero command result.
- The error is written to stderr, not stdout.
- Add tests that cover both errors through the command's testable path.
- Preserve the successful behavior from the preceding Story.
- `+"`make verify`"+` must pass.
`)
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
