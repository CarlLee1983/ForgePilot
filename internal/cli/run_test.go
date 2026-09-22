package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/app"
	"github.com/CarlLee1983/ForgePilot/internal/readiness"
	"github.com/CarlLee1983/ForgePilot/internal/runner"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
)

// AC-005: execution authorization only admits unattended Runner work. The
// manual lifecycle, verification, review, and Gate contracts remain available
// to a Goal that has never adopted an execution authorization.
func TestUnauthorisedGoalKeepsManualVerificationReviewAndGovernanceContracts(t *testing.T) {
	root := chargedRunCLIFixture(t)

	if stdout, stderr, code := executeCLI(t, root, "start", "WI-001"); code != 0 || stderr != "" || !strings.Contains(stdout, "WI-001 RUNNING") {
		t.Fatalf("start = exit %d, stdout=%q, stderr=%q", code, stdout, stderr)
	}
	if stdout, stderr, code := executeCLI(t, root, "verify", "WI-001"); code != 0 || stderr != "" || !strings.Contains(stdout, "PASS") {
		t.Fatalf("verify = exit %d, stdout=%q, stderr=%q", code, stdout, stderr)
	}
	if stdout, stderr, code := executeCLI(t, root, "review", "request", "WI-001"); code != 0 || stderr != "" || !strings.Contains(stdout, "WI-001 REVIEW") {
		t.Fatalf("review request = exit %d, stdout=%q, stderr=%q", code, stdout, stderr)
	}
	if stdout, stderr, code := executeCLI(t, root, "gate", "open", "--work", "WI-001", "--question", "Proceed?", "--option", "yes", "--option", "no"); code != 0 || stderr != "" || !strings.Contains(stdout, "GATE-001 OPEN") {
		t.Fatalf("gate open = exit %d, stdout=%q, stderr=%q", code, stdout, stderr)
	}

	stateBytes, err := os.ReadFile(filepath.Join(root, ".forgepilot", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stateBytes), `"execution"`) {
		t.Fatalf("unaffected commands created execution authorization: %s", stateBytes)
	}
}

// AC-005: exact resume must continue the recorded run as-is. A rejected resume
// is especially useful here: the admission refusal must not rewrite the stored
// deadline or budget while it reports the missing authorization.
func TestUnauthorisedExactRunResumePreservesRecordedDeadlineAndBudget(t *testing.T) {
	root := chargedRunCLIFixture(t)
	fakeAgent := filepath.Join(root, "fake-agent")
	if err := os.WriteFile(fakeAgent, []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo fake-agent-1.0; exit 0; fi\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FORGEPILOT_FAKE_AGENT", fakeAgent)
	scope, err := app.CurrentGoalScope(root, "goal")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	record := runner.Record{
		RunID: "run-20260921t000000-abcdef", Workspace: root, GoalID: "goal", GoalTitle: "Goal", Scope: scope,
		RuntimeName: "fake", Snapshot: true,
		ExecutionAuthorizationDigest: "sha256:" + strings.Repeat("a", 64),
		RunReservationID:             "run-20260921t000000-abcdef:run",
		Budget: runner.Budget{MaxSteps: 7, MaxAttemptsPerWork: 2, MaxDuration: 90 * time.Minute,
			AgentTimeout: 11 * time.Minute, VerifyTimeout: 13 * time.Minute, MaxHandoffBytes: 12345},
		Limits:    storage.ArtifactLimits{MaxWriteBytes: 1024, MaxRunBytes: 4096, MaxTotalBytes: 16384},
		StartedAt: deadline.Add(-90 * time.Minute), Deadline: deadline,
		Attempts: map[string]int{}, HumanWaits: map[string]int{},
		Stop: &runner.Stop{Reason: runner.StopInterrupted, At: deadline.Add(-time.Minute)},
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteRunArtifact(root, record.RunID, "run.json", encoded, storage.ArtifactLimits{}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, ".forgepilot", "runs", record.RunID, "run.json"))
	if err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := executeCLI(t, root, "run", "resume", record.RunID)
	if code != 1 || stdout != "" || !strings.Contains(strings.ToLower(stderr), "execution authorization") {
		t.Fatalf("resume = exit %d, stdout=%q, stderr=%q", code, stdout, stderr)
	}

	after, err := os.ReadFile(filepath.Join(root, ".forgepilot", "runs", record.RunID, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("unauthorised exact resume rewrote its record: before=%s after=%s", before, after)
	}
	var persisted runner.Record
	if err := json.Unmarshal(after, &persisted); err != nil {
		t.Fatal(err)
	}
	if !persisted.Deadline.Equal(record.Deadline) || persisted.Budget != record.Budget {
		t.Fatalf("resume changed deadline or budget: got deadline=%s budget=%#v, want deadline=%s budget=%#v", persisted.Deadline, persisted.Budget, record.Deadline, record.Budget)
	}
}

func chargedRunCLIFixture(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, arguments := range [][]string{{"init", "-q"}, {"config", "user.email", "fixture@example.com"}, {"config", "user.name", "Fixture"}} {
		command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
		}
	}
	writeChargedRunCLIStory(t, root)
	for path, contents := range map[string]string{
		"Makefile":   "verify:\n\t@true\n",
		".gitignore": ".forgepilot/\n",
	} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, arguments := range [][]string{{"add", "-A"}, {"commit", "-qm", "fixture"}} {
		command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
		}
	}
	if stdout, stderr, code := executeCLI(t, root, "init"); code != 0 || stderr != "" || !strings.Contains(stdout, "Initialized ForgePilot") {
		t.Fatalf("init = exit %d, stdout=%q, stderr=%q", code, stdout, stderr)
	}
	if stdout, stderr, code := executeCLI(t, root, "goal", "create", "--id", "goal", "--title", "Goal"); code != 0 || stderr != "" || !strings.Contains(stdout, "Goal goal created") {
		t.Fatalf("goal create = exit %d, stdout=%q, stderr=%q", code, stdout, stderr)
	}
	if stdout, stderr, code := executeCLI(t, root, "work", "add", "--goal", "goal", "--story", "specs/stories/charged"); code != 0 || stderr != "" || !strings.Contains(stdout, "WI-001") {
		t.Fatalf("work add = exit %d, stdout=%q, stderr=%q", code, stdout, stderr)
	}
	return root
}

func writeChargedRunCLIStory(t *testing.T, root string) {
	t.Helper()
	directory := filepath.Join(root, "specs", "stories", "charged")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	story, acceptance := []byte("# Story\n"), []byte("# Acceptance\n")
	sidecar := `{"schema_version":1,"story_ref":"specs/stories/charged","story_md_digest":"` + readiness.Digest(story) + `","acceptance_md_digest":"` + readiness.Digest(acceptance) + `"}`
	for name, contents := range map[string][]byte{"story.md": story, "acceptance.md": acceptance, "readiness.json": []byte(sidecar)} {
		if err := os.WriteFile(filepath.Join(directory, name), contents, 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func executeCLI(t *testing.T, root string, arguments ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, err bytes.Buffer
	returnOut := Execute(arguments, root, &out, &err)
	return out.String(), err.String(), returnOut
}
