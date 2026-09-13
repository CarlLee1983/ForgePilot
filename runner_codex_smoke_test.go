package forgepilot_test

import (
	"os"
	"strings"
	"testing"
)

// SmokeVariable opts in to driving the real Codex CLI. It is off by default and
// must stay off in CI: a green run of the fake subprocess adapter is evidence
// about ForgePilot's process, lock and recovery handling, and nothing at all
// about an unattended run against a real model.
const SmokeVariable = "FORGEPILOT_CODEX_SMOKE"

// TestCodexSmokeDrivesOneWorkItem is the only test that talks to a model. It
// needs a locally installed and already-authenticated Codex; ForgePilot never
// installs or logs in on anyone's behalf.
func TestCodexSmokeDrivesOneWorkItem(t *testing.T) {
	if os.Getenv(SmokeVariable) == "" {
		t.Skipf("set %s=1 to drive the real Codex CLI", SmokeVariable)
	}
	fixture := newRunnerFixture(t, "smoke.md")
	write(t, fixture.root+"/specs/stories/smoke.md",
		"# Smoke story\n\nAcceptance: the repository root contains a file named `wi-001.txt` whose only line is `done`.\n")
	commitAll(t, fixture.root, "smoke story")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "smoke", []string{"specs/stories/smoke.md"})

	output, code := fixture.runForge(t, "", "run", "--goal", "smoke", "--runtime", "codex", "--snapshot",
		"--max-steps", "12", "--max-attempts-per-work", "2", "--agent-timeout", "10m")
	t.Logf("codex smoke output:\n%s", output)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(output, "AWAITING_GOAL_REVIEW") {
		t.Fatal("the smoke run did not reach the goal review boundary")
	}
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
