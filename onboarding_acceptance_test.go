package forgepilot_test

import (
	"errors"
	"fmt"
	"go/version"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// The fake agent exercises the historical zero-install #33 action planner with
// copied adapters. Codex's managed-generation path is covered separately by
// Bootstrap tests; this harness does not establish native Codex behavior.
type onboardingAction struct {
	Phase, ID, Directory, Effect string
	Args                         []string
}

func readOnboardingActions(raw string) ([]onboardingAction, error) {
	fields := strings.Split(raw, "\x00")
	if len(fields) < 2 || fields[0] != "forgepilot-onboarding-plan-v1" || fields[len(fields)-1] != "" {
		return nil, errors.New("invalid action protocol")
	}
	fields = fields[1 : len(fields)-1]
	var actions []onboardingAction
	for len(fields) > 0 {
		if len(fields) < 5 {
			return nil, errors.New("truncated action")
		}
		n, err := strconv.Atoi(fields[4])
		if err != nil || n < 1 || n > len(fields)-5 {
			return nil, errors.New("invalid argv count")
		}
		actions = append(actions, onboardingAction{fields[0], fields[1], fields[2], fields[3], append([]string(nil), fields[5:5+n]...)})
		fields = fields[5+n:]
	}
	return actions, nil
}

type onboardingHarness struct {
	Root, Home, Stage, Entry, Binary, Tools, Source, Commit string
	Actions                                                 []onboardingAction
	Executed                                                []string
	Status, Story                                           string
	installedIdentity                                       string
	SourceApproved, RepositoryApproved, DraftApproved       bool
}

func newOnboardingHarness(t *testing.T, binary, platform string) *onboardingHarness {
	t.Helper()
	source := projectRoot(t)
	h := &onboardingHarness{Root: onboardingRepository(t), Home: t.TempDir(), Stage: t.TempDir(), Tools: t.TempDir(), Binary: binary, Source: source, Commit: onboardingGit(t, source, "rev-parse", "HEAD")}
	h.Entry = filepath.Join(h.Home, "bin/forgepilot")
	platformDir := map[string]string{"codex": ".agents", "claude-code": ".claude"}[platform]
	adapter := filepath.Join(h.Source, "skills", platform, "forgepilot-onboarding/SKILL.md")
	contents, err := os.ReadFile(adapter)
	if err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(h.Home, platformDir, "skills/forgepilot-onboarding/SKILL.md")
	onboardingWrite(t, h.Home, filepath.Join(platformDir, "skills/forgepilot-onboarding/SKILL.md"), string(contents))
	// The existing anti-drift checker validates the installed bytes, not copies
	// of the adapter enum. Removing/changing a source adapter makes this fail.
	check := exec.Command("/bin/sh", filepath.Join(h.Source, "scripts/skills/check_adapters_test.sh"))
	check.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + h.Home, "ADAPTER_MUTATION=1"}
	key := "CODEX_ADAPTER"
	if platform == "claude-code" {
		key = "CLAUDE_ADAPTER"
	}
	check.Env = append(check.Env, key+"="+installed)
	if out, err := check.CombinedOutput(); err != nil {
		t.Fatalf("installed adapter: %v: %s", err, out)
	}
	installedBytes, err := os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}
	contract := "Shared temporary source contract: local #33 checkout at FORGEPILOT_ONBOARDING_SOURCE; common procedure docs/release/onboarding.md; temporary only, not a published immutable identity."
	if platform == "codex" {
		for _, required := range []string{"generation-v1 current", "docs/release/onboarding.md", "Repository Onboarding Approval"} {
			if !strings.Contains(string(installedBytes), required) {
				t.Fatalf("installed managed Codex adapter lost %q", required)
			}
		}
	} else if !strings.Contains(string(installedBytes), contract) {
		t.Fatal("installed Claude adapter lost the zero-install procedure reference")
	}
	procedure, err := os.ReadFile(filepath.Join(h.Source, "docs/release/onboarding.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(procedure), "scripts/onboarding/source-built-plan.sh") {
		t.Fatal("common procedure lost the public plan seam")
	}
	for name, script := range map[string]string{
		"git":  `case "$1" in clone) mkdir -p "$5";; fetch|checkout) :;; *) exit 91;; esac`,
		"go":   `case "$1" in version) printf 'go version go1.25.5 darwin/arm64\n';; install) cp "$FP_BUILT_BINARY" "$GOBIN/forgepilot";; *) exit 92;; esac`,
		"make": `[ "$1" = verify ] || exit 93`,
	} {
		onboardingWrite(t, h.Tools, name, "#!/bin/sh\nset -eu\nprintf '%s\\000' "+name+" \"$@\" >> \"$FP_TOOL_LOG\"\n"+script+"\n")
		if err := os.Chmod(filepath.Join(h.Tools, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func (h *onboardingHarness) plan(t *testing.T) error {
	t.Helper()
	out, err := onboardingPlan(t, h.Root, "SNAPSHOT", "current", "--format", "actions", "--stage-root", h.Stage, "--entrypoint", h.Entry, "--source", h.Source, "--commit", h.Commit)
	if err != nil {
		return fmt.Errorf("plan: %w: %s", err, out)
	}
	h.Actions, err = readOnboardingActions(out)
	if err != nil {
		return err
	}
	return nil
}

// This independent contract table rejects drift before execution. The executor
// still consumes the received argv; it never executes this expected table.
func (h *onboardingHarness) validateActions() error {
	root, err := filepath.EvalSymlinks(h.Root)
	if err != nil {
		return err
	}
	stage := filepath.Join(h.Stage, h.Commit)
	source := filepath.Join(stage, "source")
	stagedBinary := filepath.Join(stage, "bin/forgepilot")
	type contract struct {
		phase, id, cwd, effect string
		args                   []string
	}
	expected := []contract{
		{"source", "go-prerequisite", root, "requires an already installed compatible Go; stop if unavailable", []string{"env", "GOTOOLCHAIN=local", "go", "version"}},
		{"source", "stage-directories", root, "creates user-owned staging and entrypoint parent directories", []string{"mkdir", "-p", filepath.Join(stage, "bin"), filepath.Dir(h.Entry)}},
		{"source", "source-fetch", root, "source fetch into the versioned staging directory", []string{"git", "clone", "--no-checkout", "--", h.Source, source}},
		{"source", "source-commit", source, "fetches the exact source commit", []string{"git", "fetch", "origin", h.Commit}},
		{"source", "source-checkout", source, "checks out the exact source commit detached", []string{"git", "checkout", "--detach", h.Commit}},
		{"source", "build", source, "builds with GOBIN in versioned staging; no toolchain downloads", []string{"env", "GOBIN=" + filepath.Join(stage, "bin"), "GOTOOLCHAIN=local", "go", "install", "./cmd/forgepilot"}},
		{"source", "verification", source, "runs the approved source canonical check", []string{"env", "GOTOOLCHAIN=local", "make", "verify"}},
		{"source", "startup", source, "confirms the staged CLI starts", []string{stagedBinary, "--help"}},
		{"source", "entrypoint-link", root, "prepares an atomic entrypoint switch; refuses an existing temporary path", []string{"ln", "-s", stagedBinary, h.Entry + ".new"}},
		{"source", "entrypoint-switch", root, "atomic entrypoint switch after build, verification and startup succeed", []string{"mv", "-fh", h.Entry + ".new", h.Entry}},
		{"story", "review-story", root, "reuse contained regular Story files; otherwise authorize a draft and stop for review before work add", []string{"specs/stories/first"}},
	}
	valid, err := onboardingStory(h.Root, "specs/stories/first")
	if err != nil {
		return err
	}
	if valid {
		_, err := os.Lstat(filepath.Join(h.Root, ".forgepilot"))
		statusEffect := "reuse existing state; reads the resulting state"
		if os.IsNotExist(err) {
			statusEffect = "reads the resulting state"
			expected = append(expected,
				contract{"repository", "init", root, "creates ForgePilot state", []string{h.Entry, "init"}},
				contract{"repository", "goal-create", root, "creates the Goal", []string{h.Entry, "goal", "create", "--id", "first", "--title", "First goal"}},
				contract{"repository", "work-add", root, "creates the Work Item after Story review", []string{h.Entry, "work", "add", "--goal", "first", "--story", "specs/stories/first"}})
		} else if err != nil {
			return err
		}
		expected = append(expected, contract{"repository", "status", root, statusEffect, []string{h.Entry, "status"}})
	}
	if len(h.Actions) != len(expected) {
		return errors.New("unexpected action count")
	}
	for i, a := range h.Actions {
		want := expected[i]
		if a.Phase != want.phase || a.ID != want.id || a.Directory != want.cwd || a.Effect != want.effect || !reflect.DeepEqual(a.Args, want.args) {
			return fmt.Errorf("unexpected action contract at %d (%s)", i, a.ID)
		}
	}
	return nil
}

var errOnboardingDeclined = errors.New("developer declined authorization")
var errOnboardingReview = errors.New("Story draft requires human review")

type onboardingPathSnapshot struct {
	exists bool
	mode   os.FileMode
	value  string
}

func snapshotOnboardingPath(path string) (onboardingPathSnapshot, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return onboardingPathSnapshot{}, nil
	}
	if err != nil {
		return onboardingPathSnapshot{}, err
	}
	snapshot := onboardingPathSnapshot{exists: true, mode: info.Mode()}
	if info.Mode()&os.ModeSymlink != 0 {
		snapshot.value, err = os.Readlink(path)
		return snapshot, err
	}
	if info.Mode().IsRegular() {
		contents, readErr := os.ReadFile(path)
		snapshot.value = string(contents)
		return snapshot, readErr
	}
	return snapshot, nil
}

type onboardingFailureDiagnostic struct {
	SourceCommit, Action, Cause, ExitStatus                 string
	EntrypointPreserved, TargetStatePreserved, TemporaryNew bool
}

func (d onboardingFailureDiagnostic) Error() string {
	return fmt.Sprintf("source_commit=%s failed_action=%s cause=%s exit_status=%s entrypoint_preserved=%t target_state_preserved=%t temporary_entrypoint_present=%t raw_command_output=omitted", d.SourceCommit, d.Action, d.Cause, d.ExitStatus, d.EntrypointPreserved, d.TargetStatePreserved, d.TemporaryNew)
}

func onboardingExitStatus(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return strconv.Itoa(exitErr.ExitCode())
	}
	return "unavailable"
}

func (h *onboardingHarness) failureDiagnostic(action, cause, exitStatus string, entrypointBefore, targetStateBefore onboardingPathSnapshot) error {
	entrypointAfter, entrypointErr := snapshotOnboardingPath(h.Entry)
	targetStateAfter, targetStateErr := snapshotOnboardingPath(filepath.Join(h.Root, ".forgepilot/state.json"))
	_, temporaryErr := os.Lstat(h.Entry + ".new")
	return onboardingFailureDiagnostic{
		SourceCommit:         h.Commit,
		Action:               action,
		Cause:                cause,
		ExitStatus:           exitStatus,
		EntrypointPreserved:  entrypointErr == nil && entrypointAfter == entrypointBefore,
		TargetStatePreserved: targetStateErr == nil && targetStateAfter == targetStateBefore,
		TemporaryNew:         temporaryErr == nil,
	}
}

func (h *onboardingHarness) compatibleGo(raw string) bool {
	fields := strings.Fields(raw)
	if len(fields) < 3 || fields[0] != "go" || fields[1] != "version" || !version.IsValid(fields[2]) {
		return false
	}
	gitShow := exec.Command("git", "-C", h.Source, "show", h.Commit+":go.mod")
	gitShow.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + h.Home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1"}
	contents, err := gitShow.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(contents), "\n") {
		goDirective := strings.Fields(line)
		if len(goDirective) == 2 && goDirective[0] == "go" {
			required := "go" + goDirective[1]
			return version.IsValid(required) && version.Compare(fields[2], required) >= 0
		}
	}
	return false
}

func (h *onboardingHarness) run(t *testing.T) error {
	t.Helper()
	if err := h.plan(t); err != nil {
		return err
	}
	return h.executeActions(t, false)
}

func (h *onboardingHarness) sourceIdentity() string {
	return strings.Join([]string{h.Source, h.Commit, h.Stage, h.Entry}, "\x00")
}

func (h *onboardingHarness) runTarget(t *testing.T) error {
	if h.installedIdentity != h.sourceIdentity() {
		return errors.New("source installation has not completed for this plan")
	}
	target, err := os.Readlink(h.Entry)
	if err != nil || target != filepath.Join(h.Stage, h.Commit, "bin/forgepilot") {
		return errors.New("approved entrypoint changed before resume")
	}
	return h.executeActions(t, true)
}

func (h *onboardingHarness) executeActions(t *testing.T, targetOnly bool) error {
	t.Helper()
	if err := h.validateActions(); err != nil {
		return err
	}
	entrypointBefore, err := snapshotOnboardingPath(h.Entry)
	if err != nil {
		return err
	}
	targetStateBefore, err := snapshotOnboardingPath(filepath.Join(h.Root, ".forgepilot/state.json"))
	if err != nil {
		return err
	}
	for _, a := range h.Actions {
		if targetOnly && a.Phase == "source" {
			continue
		}
		if a.Effect == "" {
			return errors.New("action lacks effects")
		}
		switch a.Phase {
		case "source":
			if !h.SourceApproved {
				return errOnboardingDeclined
			}
		case "story":
			if len(a.Args) != 1 {
				return errors.New("invalid Story action")
			}
			h.Story = a.Args[0]
			valid, err := onboardingStory(h.Root, h.Story)
			if err != nil {
				return err
			}
			if !valid {
				if !h.DraftApproved {
					return errOnboardingDeclined
				}
				if err := onboardingDraft(h.Root, h.Story); err != nil {
					return err
				}
				return errOnboardingReview
			}
			continue
		case "repository":
			if !h.RepositoryApproved {
				return errOnboardingDeclined
			}
		default:
			return fmt.Errorf("unknown approval phase %q", a.Phase)
		}
		c := exec.Command(a.Args[0], a.Args[1:]...)
		// Go's LookPath uses the parent PATH; resolve known tools explicitly instead.
		if !filepath.IsAbs(a.Args[0]) {
			if _, err := os.Stat(filepath.Join(h.Tools, a.Args[0])); err == nil {
				c.Path = filepath.Join(h.Tools, a.Args[0])
			} else {
				c.Path = filepath.Join("/bin", a.Args[0])
				if _, err := os.Stat(c.Path); err != nil {
					c.Path = filepath.Join("/usr/bin", a.Args[0])
				}
			}
		}
		c.Dir = a.Directory
		c.Env = []string{"PATH=" + h.Tools + ":/usr/bin:/bin", "HOME=" + h.Home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "FP_BUILT_BINARY=" + h.Binary, "FP_TOOL_LOG=" + filepath.Join(h.Home, "tool-invocations")}
		raw, err := c.CombinedOutput()
		h.Executed = append(h.Executed, a.ID)
		if err != nil {
			cause := "command_failed"
			if a.ID == "go-prerequisite" {
				cause = "go_unavailable"
			}
			return h.failureDiagnostic(a.ID, cause, onboardingExitStatus(err), entrypointBefore, targetStateBefore)
		}
		if a.ID == "go-prerequisite" && !h.compatibleGo(string(raw)) {
			return h.failureDiagnostic(a.ID, "go_incompatible", "0", entrypointBefore, targetStateBefore)
		}
		if a.ID == "entrypoint-switch" {
			h.installedIdentity = h.sourceIdentity()
		}
		if a.ID == "status" {
			h.Status = string(raw)
		}
	}
	return nil
}

// Every component is non-symlink and every leaf is a contained regular file.
// Missing files request a draft; unsafe or partial existing paths fail closed.
func onboardingStory(root, story string) (bool, error) {
	if !strings.HasPrefix(story, "specs/stories/") || filepath.IsAbs(story) || filepath.Clean(story) != story {
		return false, errors.New("invalid Story path")
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return false, err
	}
	defer r.Close()
	parts := strings.Split(story, "/")
	for i := range parts {
		info, err := r.Lstat(filepath.Join(parts[:i+1]...))
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false, errors.New("unsafe Story directory")
		}
	}
	for _, name := range []string{"story.md", "acceptance.md"} {
		info, err := r.Lstat(filepath.Join(story, name))
		if err != nil {
			return false, err
		}
		if !info.Mode().IsRegular() {
			return false, errors.New("Story leaf must be a regular file")
		}
		f, err := r.Open(filepath.Join(story, name))
		if err != nil {
			return false, err
		}
		f.Close()
	}
	return true, nil
}

func onboardingDraft(root, story string) error {
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	if err = r.MkdirAll(filepath.Dir(story), 0700); err != nil {
		return err
	}
	if err = r.Mkdir(story, 0700); err != nil {
		return err
	}
	for name, contents := range map[string]string{"story.md": "# Draft: offline billing sync\nScope: local CLI. Assumption: developer reviews before work add.\nGuidance: follow repository AGENTS.md.\n", "acceptance.md": "Status shows the first Goal and Work Item. Run make verify.\n"} {
		f, err := r.OpenFile(filepath.Join(story, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = f.WriteString(contents)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func TestOnboardingOfflineAcceptance(t *testing.T) {
	_, binary := fixture(t)
	for _, platform := range []string{"codex", "claude-code"} {
		t.Run(platform, func(t *testing.T) {
			h := newOnboardingHarness(t, binary, platform)
			head := onboardingGit(t, h.Root, "rev-parse", "HEAD")
			h.SourceApproved = true
			h.RepositoryApproved = true
			if err := h.run(t); err != nil {
				t.Fatal(err)
			}

			invocations, err := os.ReadFile(filepath.Join(h.Home, "tool-invocations"))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"git\x00fetch\x00origin\x00" + h.Commit + "\x00", "git\x00checkout\x00--detach\x00" + h.Commit + "\x00", "go\x00install\x00./cmd/forgepilot\x00", "make\x00verify\x00"} {
				if !strings.Contains(string(invocations), want) {
					t.Fatalf("spy did not receive exact source action %q", want)
				}
			}
			expected := []string{"go-prerequisite", "stage-directories", "source-fetch", "source-commit", "source-checkout", "build", "verification", "startup", "entrypoint-link", "entrypoint-switch", "init", "goal-create", "work-add", "status"}
			if !reflect.DeepEqual(h.Executed, expected) {
				t.Fatalf("actions: %v", h.Executed)
			}
			for _, want := range []string{"first", "WI-001 READY", "specs/stories/first"} {
				if !strings.Contains(h.Status, want) {
					t.Fatalf("status missing %q: %s", want, h.Status)
				}
			}
			if got := onboardingGit(t, h.Root, "rev-parse", "HEAD"); got != head {
				t.Fatal("onboarding committed target changes")
			}
			state, err := os.ReadFile(filepath.Join(h.Root, ".forgepilot/state.json"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(state), `"result": "PASS"`) || strings.Contains(string(state), `"status": "DONE"`) {
				t.Fatal("onboarding created a verification/approval claim")
			}
			// Replanning existing state still resolves Story, then invokes only status.
			if err := h.plan(t); err != nil {
				t.Fatal(err)
			}
			h.Executed = nil
			// Source install is already available; run the target portion from the plan.
			if err := h.runTarget(t); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(filepath.Join(h.Root, ".forgepilot/state.json"))
			if err != nil {
				t.Fatal(err)
			}
			if string(state) != string(after) || h.Story != "specs/stories/first" || !reflect.DeepEqual(h.Executed, []string{"status"}) {
				t.Fatal("existing state/Story not reused read-only")
			}
		})
	}
}

func TestOnboardingApprovalAndFailureStops(t *testing.T) {
	_, binary := fixture(t)
	for _, platform := range []string{"codex", "claude-code"} {
		for _, scenario := range []string{"first-declined", "second-declined", "missing-go", "missing-git", "failed-build", "failed-verify", "missing-story", "existing-state-missing-story", "draft-declined"} {
			t.Run(platform+"/"+scenario, func(t *testing.T) {
				h := newOnboardingHarness(t, binary, platform)
				h.SourceApproved = true
				h.RepositoryApproved = true
				var stateBefore []byte
				switch scenario {
				case "first-declined":
					h.SourceApproved = false
				case "second-declined":
					h.RepositoryApproved = false
				case "missing-go":
					onboardingWrite(t, h.Tools, "go", "#!/bin/sh\nexit 127\n")
				case "missing-git":
					onboardingWrite(t, h.Tools, "git", "#!/bin/sh\nexit 127\n")
				case "failed-build":
					onboardingWrite(t, h.Tools, "go", "#!/bin/sh\ncase \"$1\" in\nversion) printf 'go version go1.25.5 darwin/arm64\\n';;\ninstall) exit 1;;\n*) exit 92;;\nesac\n")
				case "failed-verify":
					onboardingWrite(t, h.Tools, "make", "#!/bin/sh\nexit 1\n")
				case "existing-state-missing-story":
					mustRun(t, binary, h.Root, "init")
					var err error
					stateBefore, err = os.ReadFile(filepath.Join(h.Root, ".forgepilot/state.json"))
					if err != nil {
						t.Fatal(err)
					}
					fallthrough
				case "missing-story", "draft-declined":
					if err := os.RemoveAll(filepath.Join(h.Root, "specs/stories/first")); err != nil {
						t.Fatal(err)
					}
					h.DraftApproved = scenario != "draft-declined"
				}
				head := onboardingGit(t, h.Root, "rev-parse", "HEAD")
				err := h.run(t)
				if err == nil {
					t.Fatal("flow did not stop")
				}
				switch scenario {
				case "first-declined", "second-declined", "draft-declined":
					if !errors.Is(err, errOnboardingDeclined) {
						t.Fatal(err)
					}
				case "missing-story", "existing-state-missing-story":
					if !errors.Is(err, errOnboardingReview) {
						t.Fatal(err)
					}
					if ok, err := onboardingStory(h.Root, "specs/stories/first"); err != nil || !ok {
						t.Fatalf("draft: %t %v", ok, err)
					}
					// Explicit human review is simulated by resuming only after this stop.
					if err := h.plan(t); err != nil {
						t.Fatal(err)
					}
					if err := h.runTarget(t); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "first-declined" && len(h.Executed) != 0 {
					t.Fatalf("executed without first approval: %v", h.Executed)
				}
				if scenario == "missing-go" && !reflect.DeepEqual(h.Executed, []string{"go-prerequisite"}) {
					t.Fatalf("missing Go did not stop before writes: %v", h.Executed)
				}
				if scenario == "failed-build" || scenario == "failed-verify" || scenario == "missing-go" || scenario == "missing-git" || scenario == "first-declined" {
					requireAbsent(t, h.Entry)
				}
				if !strings.Contains(scenario, "missing-story") {
					requireAbsent(t, filepath.Join(h.Root, ".forgepilot"))
				}
				if stateBefore != nil {
					after, err := os.ReadFile(filepath.Join(h.Root, ".forgepilot/state.json"))
					if err != nil || string(after) != string(stateBefore) {
						t.Fatal("existing state changed during Story review")
					}
				}
				if onboardingGit(t, h.Root, "rev-parse", "HEAD") != head {
					t.Fatal("flow created a commit")
				}
			})
		}
	}
}

func TestOnboardingSourceFailurePreservesExistingEntrypoint(t *testing.T) {
	_, binary := fixture(t)
	h := newOnboardingHarness(t, binary, "codex")
	h.SourceApproved = true
	h.RepositoryApproved = true
	onboardingWrite(t, h.Home, "bin/forgepilot", "previous version sentinel")
	onboardingWrite(t, h.Tools, "make", "#!/bin/sh\nexit 1\n")
	if err := h.run(t); err == nil {
		t.Fatal("source verification failure did not stop")
	}
	previous, err := os.ReadFile(h.Entry)
	if err != nil || string(previous) != "previous version sentinel" {
		t.Fatal("failed installation replaced prior entrypoint")
	}
	requireAbsent(t, filepath.Join(h.Root, ".forgepilot"))
}

func TestOnboardingSourceFailureDiagnosticsAreSanitized(t *testing.T) {
	_, binary := fixture(t)
	const secret = "synthetic-source-credential"
	tests := []struct {
		name, failedAt, cause, exitStatus string
		temporaryNew                      bool
		executed                          []string
		configure                         func(*testing.T, *onboardingHarness)
	}{
		{
			name:       "missing-go",
			failedAt:   "go-prerequisite",
			cause:      "go_unavailable",
			exitStatus: "127",
			executed:   []string{"go-prerequisite"},
			configure: func(t *testing.T, h *onboardingHarness) {
				onboardingWrite(t, h.Tools, "go", "#!/bin/sh\nprintf '"+secret+"\\n' >&2\nexit 127\n")
			},
		},
		{
			name:       "incompatible-go",
			failedAt:   "go-prerequisite",
			cause:      "go_incompatible",
			exitStatus: "0",
			executed:   []string{"go-prerequisite"},
			configure: func(t *testing.T, h *onboardingHarness) {
				onboardingWrite(t, h.Tools, "go", "#!/bin/sh\nprintf 'go version go1.24.0 darwin/arm64\\n'\n")
			},
		},
		{
			name:       "failed-verification",
			failedAt:   "verification",
			cause:      "command_failed",
			exitStatus: "1",
			executed:   []string{"go-prerequisite", "stage-directories", "source-fetch", "source-commit", "source-checkout", "build", "verification"},
			configure: func(t *testing.T, h *onboardingHarness) {
				onboardingWrite(t, h.Tools, "make", "#!/bin/sh\nprintf '"+secret+"\\n' >&2\nexit 1\n")
			},
		},
		{
			name:         "failed-entrypoint-switch",
			failedAt:     "entrypoint-switch",
			cause:        "command_failed",
			exitStatus:   "74",
			temporaryNew: true,
			executed:     []string{"go-prerequisite", "stage-directories", "source-fetch", "source-commit", "source-checkout", "build", "verification", "startup", "entrypoint-link", "entrypoint-switch"},
			configure: func(t *testing.T, h *onboardingHarness) {
				onboardingWrite(t, h.Tools, "mv", "#!/bin/sh\nprintf '"+secret+"\\n' >&2\nexit 74\n")
				if err := os.Chmod(filepath.Join(h.Tools, "mv"), 0700); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newOnboardingHarness(t, binary, "codex")
			h.SourceApproved = true
			h.RepositoryApproved = true
			onboardingWrite(t, h.Home, "bin/forgepilot", "previous version sentinel")
			mustRun(t, binary, h.Root, "init")
			stateBefore, readErr := os.ReadFile(filepath.Join(h.Root, ".forgepilot/state.json"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			tc.configure(t, h)

			err := h.run(t)
			if err == nil {
				t.Fatal("source failure did not stop")
			}
			want := fmt.Sprintf("source_commit=%s failed_action=%s cause=%s exit_status=%s entrypoint_preserved=true target_state_preserved=true temporary_entrypoint_present=%t raw_command_output=omitted", h.Commit, tc.failedAt, tc.cause, tc.exitStatus, tc.temporaryNew)
			if err.Error() != want {
				t.Fatalf("diagnostic = %q, want %q", err, want)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatal("diagnostic retained raw command output")
			}
			if !reflect.DeepEqual(h.Executed, tc.executed) {
				t.Fatalf("executed actions = %v, want %v", h.Executed, tc.executed)
			}
			previous, readErr := os.ReadFile(h.Entry)
			if readErr != nil || string(previous) != "previous version sentinel" {
				t.Fatal("source failure replaced prior entrypoint")
			}
			stateAfter, readErr := os.ReadFile(filepath.Join(h.Root, ".forgepilot/state.json"))
			if readErr != nil || string(stateAfter) != string(stateBefore) {
				t.Fatal("source failure changed existing target state")
			}
		})
	}
}

func TestOnboardingGoCompatibilityIgnoresReplacementObjects(t *testing.T) {
	source := t.TempDir()
	onboardingGit(t, source, "init", "-q")
	onboardingGit(t, source, "config", "user.email", "onboarding@example.invalid")
	onboardingGit(t, source, "config", "user.name", "Onboarding fixture")
	onboardingWrite(t, source, "go.mod", "module example.invalid/source\n\ngo 1.25.5\n")
	onboardingGit(t, source, "add", "go.mod")
	onboardingGit(t, source, "commit", "-qm", "required version")
	requiredCommit := onboardingGit(t, source, "rev-parse", "HEAD")

	onboardingWrite(t, source, "go.mod", "module example.invalid/source\n\ngo 1.24.0\n")
	onboardingGit(t, source, "commit", "-qam", "replacement version")
	replacementCommit := onboardingGit(t, source, "rev-parse", "HEAD")
	onboardingGit(t, source, "replace", requiredCommit, replacementCommit)

	h := &onboardingHarness{Source: source, Commit: requiredCommit, Home: t.TempDir()}
	if h.compatibleGo("go version go1.24.0 darwin/arm64\n") {
		t.Fatal("replacement object weakened the exact source commit Go requirement")
	}
}

func TestOnboardingDraftRejectsPathsChangedAfterReview(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "specs")); err != nil {
		t.Fatal(err)
	}
	if err := onboardingDraft(root, "specs/stories/first"); err == nil {
		t.Fatal("draft escaped through changed parent")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("draft modified outside directory")
	}
}

func TestOnboardingRejectsMutatedPlanBeforeAnyExecution(t *testing.T) {
	h := newOnboardingHarness(t, "unused-built-binary", "codex")
	h.SourceApproved = true
	h.RepositoryApproved = true
	for _, mode := range []string{"extra-source", "git-commit", "relative-traversal", "wrong-cwd", "wrong-id", "duplicate", "reordered", "truncated", "empty-effects", "wrong-effects", "ambient-binary"} {
		t.Run(mode, func(t *testing.T) {
			if err := h.plan(t); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "extra-source":
				a := h.Actions[2]
				a.ID = "extra"
				h.Actions = append(h.Actions[:10], append([]onboardingAction{a}, h.Actions[10:]...)...)
			case "git-commit":
				h.Actions[2].Args = []string{"git", "commit", "-am", "not authorized"}
			case "relative-traversal":
				h.Actions[1].Args = []string{"mkdir", "-p", "../../outside"}
			case "wrong-cwd":
				h.Actions[1].Directory = t.TempDir()
			case "wrong-id":
				h.Actions[len(h.Actions)-1].ID = "migrate"
			case "duplicate":
				h.Actions[3] = h.Actions[2]
			case "reordered":
				h.Actions[0], h.Actions[1] = h.Actions[1], h.Actions[0]
			case "truncated":
				h.Actions = h.Actions[:len(h.Actions)-1]
			case "wrong-effects":
				h.Actions[0].Effect = "read-only status; no subprocess will run"
			case "empty-effects":
				h.Actions[0].Effect = ""
			case "ambient-binary":
				h.Actions[len(h.Actions)-1].Args[0] = "forgepilot"
			}
			if err := h.executeActions(t, false); err == nil {
				t.Fatal("mutated plan executed")
			}
			if len(h.Executed) != 0 {
				t.Fatalf("rejected too late: %v", h.Executed)
			}
			requireAbsent(t, h.Entry)
			requireAbsent(t, filepath.Join(h.Root, ".forgepilot"))
		})
	}
	for _, raw := range []string{"", "wrong-version\x00", "forgepilot-onboarding-plan-v1\x00source\x00", "forgepilot-onboarding-plan-v1\x00source\x00id\x00cwd\x00effect\x001\x00"} {
		if _, err := readOnboardingActions(raw); err == nil {
			t.Fatalf("malformed stream accepted: %q", raw)
		}
	}
}

func TestOnboardingCannotResumeBeforeSourceInstallation(t *testing.T) {
	h := newOnboardingHarness(t, "unused-binary", "codex")
	if err := h.plan(t); err != nil {
		t.Fatal(err)
	}
	if err := h.runTarget(t); err == nil {
		t.Fatal("skipped uncompleted source phase")
	}
	if len(h.Executed) != 0 {
		t.Fatal("resume executed without source installation")
	}
}
