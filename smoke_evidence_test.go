package forgepilot_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/runner"
)

// This file is the evidence half of the real Codex smoke round. The round costs
// model quota and cannot be replayed, so everything it used and everything it
// produced is copied out of the disposable fixture before that fixture is
// deleted. Nothing here runs unless FORGEPILOT_SMOKE_ARTIFACT_DIR is set.

// smokeRound is what one round of the real smoke test knows about itself. It is
// filled in as the round proceeds so that a round which stops early still
// exports the part it reached.
type smokeRound struct {
	Command      []string  `json:"command"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
	TimeZone     string    `json:"time_zone"`
	RunnerExit   int       `json:"runner_exit_code"`
	RunnerOutput string    `json:"-"`
	RunID        string    `json:"run_id,omitempty"`
}

// openSmokeExport prepares this round's evidence directory and arranges for the
// export to happen before the fixture is deleted.
//
// The ordering is the whole point. Go runs t.Cleanup functions last-registered
// first, and the fixture's temporary directories registered their own removal
// when they were created, so a cleanup registered here necessarily runs before
// them. By the time it runs the Runner process has already exited — runForge
// waits for it — so no writer is still in the fixture when it is read.
//
// It returns nil when the variable is unset, which is what keeps ordinary test
// runs from writing anywhere outside their own temporary directories.
func openSmokeExport(t *testing.T, fixture runnerFixture, round *smokeRound) *artifactExport {
	t.Helper()
	base := os.Getenv(SmokeArtifactVariable)
	if base == "" {
		t.Logf("set %s to an absolute path outside the fixture to keep this round's evidence", SmokeArtifactVariable)
		return nil
	}
	export, err := openArtifactExport(base, "codex-smoke", time.Now())
	if err != nil {
		t.Fatalf("the evidence export could not be opened, so the round was not started: %v", err)
	}
	t.Logf("codex smoke evidence: %s", export.dir)
	t.Cleanup(func() {
		collectSmokeEvidence(t, export, fixture, round)
		if err := export.close(time.Now()); err != nil {
			// An export failure is reported as a test failure: a round that says it
			// passed while its evidence was lost is the outcome this guards against.
			t.Errorf("the evidence export did not complete: %v", err)
		}
		t.Logf("codex smoke evidence written to %s", export.dir)
	})
	return export
}

// collectSmokeEvidence copies the round's identity, inputs and results out of
// the fixture. Every section records a gap rather than stopping, because a
// round that failed halfway is exactly when the partial evidence matters.
func collectSmokeEvidence(t *testing.T, export *artifactExport, fixture runnerFixture, round *smokeRound) {
	t.Helper()
	export.write("environment.json", encodeEvidence(t, collectEnvironment(t, fixture, round)))
	if patch := gitCapture(projectRoot(t), "diff", "HEAD"); patch != "" {
		export.write("forgepilot-working-tree.patch", []byte(patch))
	}
	export.write("runner-output.txt", []byte(round.RunnerOutput))

	// The inputs: the Stories the sessions were given, the fixture's own AGENTS.md
	// and the canonical check that produced every PASS. A Story path is not an
	// input; the text the model actually read is.
	export.copyTree("inputs/specs", filepath.Join(fixture.root, "specs"), nil)
	export.copyFile("inputs/AGENTS.md", filepath.Join(fixture.root, "AGENTS.md"))
	export.copyFile("inputs/Makefile", filepath.Join(fixture.root, "Makefile"))
	export.copyFile("inputs/go.mod", filepath.Join(fixture.root, "go.mod"))

	// The results: every run artifact (handoff, result.json, session.log, the run
	// record and its journal), every verification log, and the final state the
	// assertions were made against.
	forgepilot := filepath.Join(fixture.root, ".forgepilot")
	export.copyTree("forgepilot", forgepilot, func(within string) string {
		if within == "locks" || strings.HasPrefix(within, "locks/") {
			return "a lock file carries no evidence and is not copied"
		}
		if within == "worktrees" || strings.HasPrefix(within, "worktrees/") {
			return "a verification worktree is a checkout of the snapshot already carried by fixture.bundle"
		}
		return ""
	})

	// The Candidate content itself. Snapshot Evidence names a commit that lives
	// only in the fixture's object database, so recording the SHA without the
	// objects would leave nothing to re-verify against.
	staging, err := os.MkdirTemp("", "forgepilot-smoke-bundle")
	if err != nil {
		export.gap("fixture.bundle", "no staging directory for the bundle: "+err.Error())
		staging = ""
	}
	defer func() {
		if staging != "" {
			_ = os.RemoveAll(staging)
		}
	}()
	bundle := filepath.Join(staging, "fixture.bundle")
	if output, err := exec.Command("git", "-C", fixture.root, "bundle", "create", bundle, "--all").CombinedOutput(); err != nil {
		export.gap("fixture.bundle", fmt.Sprintf("git bundle failed: %v: %s", err, strings.TrimSpace(string(output))))
	} else {
		export.copyFile("fixture.bundle", bundle)
	}
	export.write("fixture-refs.txt", []byte(gitCapture(fixture.root, "show-ref")))
	export.write("fixture-log.txt", []byte(gitCapture(fixture.root, "log", "--all", "--oneline", "--decorate")))

	// The final working tree, so the code the last PASS was taken on is readable
	// without unpacking the bundle.
	export.copyTree("fixture-worktree", fixture.root, func(within string) string {
		switch {
		case within == ".git" || strings.HasPrefix(within, ".git/"):
			return "the Git directory is carried by fixture.bundle instead"
		case within == ".forgepilot" || strings.HasPrefix(within, ".forgepilot/"):
			return "exported under forgepilot/"
		}
		return ""
	})

	export.write("outer-capture.md", []byte(outerCaptureNote))
}

// outerCaptureNote records the one thing this export cannot capture about
// itself, rather than leaving its absence to be discovered later.
const outerCaptureNote = `# What this directory does not contain

The ` + "`go test`" + ` console output and its exit code are produced by the process
that runs this test, so they cannot be written from inside it. Capture them
alongside this directory and preserve the real exit code:

    go test -count=1 -v -timeout=60m -run '^TestCodexSmokeDrivesDependentWorkToTheGoalReviewBoundary$' . > go-test.log 2>&1
    echo $? > go-test-exit-code.txt

Piping through ` + "`tee`" + ` reports tee's exit code, not the test's.
`

// smokeEnvironment records who ran this round, on what, and with which
// settings. Configured values and observed values are kept apart on purpose: a
// model name read out of a config file is what was asked for, and only a
// session log can say what answered.
type smokeEnvironment struct {
	ForgePilotCommit   string            `json:"forgepilot_commit"`
	ForgePilotDirty    bool              `json:"forgepilot_working_tree_modified"`
	ForgePilotStatus   string            `json:"forgepilot_working_tree_status"`
	OS                 string            `json:"macos_version"`
	Architecture       string            `json:"cpu_architecture"`
	Go                 string            `json:"go_version"`
	Git                string            `json:"git_version"`
	Codex              string            `json:"codex_cli_version"`
	Round              *smokeRound       `json:"round"`
	RunnerBudget       map[string]any    `json:"runner_budget,omitempty"`
	RuntimeName        string            `json:"runtime_name,omitempty"`
	RuntimeExecutable  string            `json:"runtime_executable,omitempty"`
	RuntimeVersion     string            `json:"runtime_version_recorded_by_the_run,omitempty"`
	ConfiguredSettings map[string]string `json:"codex_settings_requested_by_configuration"`
	AdapterSandbox     string            `json:"codex_sandbox_requested_by_the_adapter"`
	ObservedModel      string            `json:"model_identity_reported_by_the_session_logs"`
}

func collectEnvironment(t *testing.T, fixture runnerFixture, round *smokeRound) smokeEnvironment {
	t.Helper()
	status := gitCapture(projectRoot(t), "status", "--porcelain")
	environment := smokeEnvironment{
		ForgePilotCommit: strings.TrimSpace(gitCapture(projectRoot(t), "rev-parse", "HEAD")),
		ForgePilotDirty:  strings.TrimSpace(status) != "",
		ForgePilotStatus: status,
		OS:               capture("sw_vers", "-productVersion"),
		Architecture:     capture("uname", "-m"),
		Go:               capture("go", "version"),
		Git:              capture("git", "--version"),
		Codex:            capture("codex", "--version"),
		Round:            round,
		// The adapter passes this explicitly; see internal/agent/codex.go.
		AdapterSandbox:     "workspace-write (--sandbox workspace-write, no bypass flags)",
		ConfiguredSettings: codexConfiguredSettings(),
		ObservedModel:      observedModel(fixture.root, round.RunID),
	}
	if round.RunID != "" {
		if record, err := runner.LoadRecord(fixture.root, round.RunID); err == nil {
			environment.RuntimeName = record.RuntimeName
			environment.RuntimeExecutable = record.RuntimeExecutable
			environment.RuntimeVersion = record.RuntimeVersion
			budget, _ := json.Marshal(record.Budget)
			_ = json.Unmarshal(budget, &environment.RunnerBudget)
		}
	}
	return environment
}

// codexSettingKeys is the whitelist of settings worth recording. It is a
// whitelist rather than a copy of the file because a Codex configuration can
// hold MCP server definitions and other private material, and none of that
// belongs in an evidence bundle.
var codexSettingKeys = map[string]bool{
	"model": true, "model_reasoning_effort": true, "model_provider": true,
	"approval_policy": true, "sandbox_mode": true, "service_tier": true,
}

func codexConfiguredSettings() map[string]string {
	settings := map[string]string{}
	home, err := os.UserHomeDir()
	if err != nil {
		settings["error"] = err.Error()
		return settings
	}
	contents, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		settings["error"] = "unknown: " + err.Error()
		return settings
	}
	for _, line := range strings.Split(string(contents), "\n") {
		line = strings.TrimSpace(line)
		// Only the top-level table is read; a section header ends it.
		if strings.HasPrefix(line, "[") {
			break
		}
		key, value, found := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !found || !codexSettingKeys[key] {
			continue
		}
		settings[key] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	for key := range codexSettingKeys {
		if _, ok := settings[key]; !ok {
			settings[key] = "unknown"
		}
	}
	return settings
}

// modelLine finds a model identity a session log reported about itself. A CLI
// version does not imply a model, so anything not printed by the run is
// reported as unknown rather than inferred.
var modelLine = regexp.MustCompile(`(?im)^\s*(?:model|reasoning)\s*[:=]\s*(.+)$`)

func observedModel(root, runID string) string {
	if runID == "" {
		return "unknown: no run was recorded"
	}
	matches := map[string]bool{}
	directory := filepath.Join(root, ".forgepilot", "runs", runID)
	_ = filepath.Walk(directory, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Base(path) != "session.log" {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, match := range modelLine.FindAllStringSubmatch(string(contents), -1) {
			matches[strings.TrimSpace(match[1])] = true
		}
		return nil
	})
	if len(matches) == 0 {
		return "unknown: no session log reported a model identity"
	}
	reported := make([]string, 0, len(matches))
	for value := range matches {
		reported = append(reported, value)
	}
	sort.Strings(reported)
	return strings.Join(reported, "; ")
}

func encodeEvidence(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(encoded, '\n')
}

// capture runs a command for its version string. A failure is recorded as text
// rather than failing the round: an unreadable version is a gap in the evidence,
// not a reason to throw the evidence away.
func capture(name string, arguments ...string) string {
	output, err := exec.Command(name, arguments...).CombinedOutput()
	if err != nil {
		return "unknown: " + err.Error()
	}
	return strings.TrimSpace(string(output))
}

func gitCapture(root string, arguments ...string) string {
	output, err := exec.Command("git", append([]string{"-C", root}, arguments...)...).CombinedOutput()
	if err != nil {
		return "unknown: " + err.Error() + ": " + strings.TrimSpace(string(output))
	}
	return string(output)
}
