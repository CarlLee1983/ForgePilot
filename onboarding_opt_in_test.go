package forgepilot_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const onboardingCodexOptIn = "FORGEPILOT_ONBOARDING_CODEX_ACCEPTANCE"
const onboardingClaudeOptIn = "FORGEPILOT_ONBOARDING_CLAUDE_ACCEPTANCE"
const onboardingSpyLog = "FORGEPILOT_ONBOARDING_TEST_SPY_LOG"

// FP-35 ends at a generated executable spy, even when opted in. Actual model
// onboarding is #39; these tests never resolve an ambient Codex or Claude CLI.
func TestOnboardingCodexAcceptance(t *testing.T) {
	if os.Getenv(onboardingCodexOptIn) != "1" {
		t.Skip("requires exact onboarding Codex opt-in 1")
	}
	onboardingAcceptanceProbe(t, "codex")
}
func TestOnboardingClaudeAcceptance(t *testing.T) {
	if os.Getenv(onboardingClaudeOptIn) != "1" {
		t.Skip("requires exact onboarding Claude opt-in 1")
	}
	onboardingAcceptanceProbe(t, "claude")
}

type onboardingAgentReport struct {
	Kind            string `json:"kind"`
	Adapter         string `json:"adapter"`
	AgentExitCode   int    `json:"agent_exit_code"`
	AgentOutput     string `json:"agent_output"`
	VerificationRun bool   `json:"verification_run"`
}

func onboardingAcceptanceProbe(t *testing.T, platform string) {
	t.Helper()
	root := t.TempDir()
	// No inherited environment or login files enter this subprocess. The only
	// executable is created here, under the test's own temporary directory.
	spy := filepath.Join(root, platform)
	script := `#!/bin/sh
set -eu
if [ -n "$SPY_LOG" ]; then printf '%s\n' "$1" >> "$SPY_LOG"; fi
case "$1" in
 --login-status) printf 'fixture login only\n';;
 --dispatch) printf '{"claim":"PASS: onboarding complete","credential":"synthetic-secret"}\n';;
 *) exit 91;;
esac
`
	onboardingWrite(t, root, platform, script)
	if err := os.Chmod(spy, 0700); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=/usr/bin:/bin", "HOME=" + root, "CODEX_HOME=" + filepath.Join(root, "codex-home"), "CLAUDE_CONFIG_DIR=" + filepath.Join(root, "claude-home"), "SPY_LOG=" + os.Getenv(onboardingSpyLog)}
	for _, arg := range []string{"--login-status", "--dispatch"} {
		c := exec.Command(spy, arg)
		c.Env = env
		c.Dir = root
		raw, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("generated %s spy failed: %v", platform, err)
		}
		if arg == "--dispatch" {
			var output struct{ Claim, Credential string }
			if err := json.Unmarshal(raw, &output); err != nil {
				t.Fatal("invalid generated spy output")
			}
			report := onboardingAgentReport{Kind: "agent-output-only", Adapter: platform, AgentExitCode: 0, AgentOutput: "[OMITTED]", VerificationRun: false}
			if destination := os.Getenv(SmokeArtifactVariable); destination != "" {
				if err := exportOnboardingReport(root, destination, report); err != nil {
					t.Fatal(err)
				}
			}
			t.Log("generated agent spy reached; no ForgePilot verification was run")
		}
	}
}

// Reports intentionally omit raw agent text and all environment fields. Root
// handles bind writes to the checked destination; exclusive creation cannot
// overwrite an earlier run or follow a pre-existing leaf symlink.
func exportOnboardingReport(fixture, destination string, report onboardingAgentReport) error {
	root, err := openOnboardingExport(fixture, destination, nil)
	if err != nil {
		return err
	}
	defer root.Close()
	// Allowlisted constants prevent arbitrary adapter/output values entering disk.
	if report.Kind != "agent-output-only" || (report.Adapter != "codex" && report.Adapter != "claude") || report.AgentOutput != "[OMITTED]" || report.VerificationRun {
		return fmt.Errorf("invalid sanitized report")
	}
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	name := "onboarding-" + report.Adapter + ".json"
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = root.Remove(name)
		return fmt.Errorf("write sanitized report: %v / %v", writeErr, closeErr)
	}
	return nil
}

// afterResolve is a test-only scheduling seam for a filesystem rename race.
func openOnboardingExport(fixture, destination string, afterResolve func()) (*os.Root, error) {
	if !filepath.IsAbs(destination) {
		return nil, fmt.Errorf("export must be absolute")
	}
	// Bind first. Later path resolution is accepted only if it still names this
	// opened directory; a replacement cannot redirect any eventual write.
	root, err := os.OpenRoot(destination)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			root.Close()
		}
	}()
	fixture, err = filepath.EvalSymlinks(fixture)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(destination)
	if err != nil {
		return nil, err
	}
	if afterResolve != nil {
		afterResolve()
	}
	live, err := os.Stat(destination)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(live, opened) {
		return nil, fmt.Errorf("export directory changed")
	}
	if resolved == fixture || strings.HasPrefix(resolved, fixture+string(filepath.Separator)) {
		return nil, fmt.Errorf("export must be outside fixture")
	}
	keep = true
	return root, nil
}

func TestOnboardingOptInSubprocessOrdering(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, platform := range []struct{ name, variable, test string }{
		{"codex", onboardingCodexOptIn, "TestOnboardingCodexAcceptance"},
		{"claude", onboardingClaudeOptIn, "TestOnboardingClaudeAcceptance"},
	} {
		for _, value := range []string{"<unset>", "", "0", "false", "FALSE", "off", "no", "true", "yes", "2", " 1", "1 ", " 1 ", "1\n", "1"} {
			t.Run(platform.name+"/"+fmt.Sprintf("%q", value), func(t *testing.T) {
				private := t.TempDir()
				spyLog := filepath.Join(private, "spy.log")
				// A premature t.TempDir is an error for disabled cases, even if its
				// cleanup would otherwise hide that the fixture had been created.
				temporary := filepath.Join(private, "missing-tmp")
				destination := filepath.Join(private, "missing-export")
				enabled := value == "1"
				if enabled {
					temporary = t.TempDir()
					destination = t.TempDir()
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				c := exec.CommandContext(ctx, binary, "-test.run", "^"+platform.test+"$", "-test.v", "-test.timeout", "20s")
				c.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + private, "TMPDIR=" + temporary, onboardingSpyLog + "=" + spyLog, SmokeArtifactVariable + "=" + destination}
				if value != "<unset>" {
					c.Env = append(c.Env, platform.variable+"="+value)
				}
				raw, err := c.CombinedOutput()
				out := string(raw)
				if err != nil {
					t.Fatalf("guarded subprocess: %v: %s", err, out)
				}
				if !strings.Contains(out, "=== RUN   "+platform.test) {
					t.Fatalf("entry point never ran: %s", out)
				}
				if !enabled {
					if !strings.Contains(out, "--- SKIP: "+platform.test) {
						t.Fatalf("entry point did not skip: %s", out)
					}
					requireAbsent(t, spyLog)
					requireAbsent(t, temporary)
					requireAbsent(t, destination)
				} else {
					if strings.Contains(out, "--- SKIP:") {
						t.Fatalf("positive control skipped: %s", out)
					}
					calls, err := os.ReadFile(spyLog)
					if err != nil || string(calls) != "--login-status\n--dispatch\n" {
						t.Fatalf("spy unreachable: %q %v", calls, err)
					}
					evidence, err := os.ReadFile(filepath.Join(destination, "onboarding-"+platform.name+".json"))
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(string(evidence), "synthetic-secret") || strings.Contains(string(evidence), "PASS") {
						t.Fatalf("unsanitized agent claim: %s", evidence)
					}
					if !strings.Contains(string(evidence), `"verification_run":false`) {
						t.Fatal("agent report claims verification")
					}
					entries, err := os.ReadDir(temporary)
					if err != nil || len(entries) != 0 {
						t.Fatalf("disposable fixture survived subprocess: %v %v", entries, err)
					}
				}
			})
		}
	}
}

func TestOnboardingEvidenceContainmentAndClaimSeparation(t *testing.T) {
	fixture := t.TempDir()
	outside := t.TempDir()
	report := onboardingAgentReport{"agent-output-only", "codex", 0, "[OMITTED]", false}
	t.Setenv("AWS_SECRET_ACCESS_KEY", "synthetic-secret")
	if err := exportOnboardingReport(fixture, fixture, report); err == nil {
		t.Fatal("export inside fixture accepted")
	}
	link := filepath.Join(outside, "alias")
	if err := os.Symlink(fixture, link); err != nil {
		t.Fatal(err)
	}
	if err := exportOnboardingReport(fixture, link, report); err == nil {
		t.Fatal("export symlink into fixture accepted")
	}
	if err := exportOnboardingReport(fixture, "relative", report); err == nil {
		t.Fatal("relative export accepted")
	}
	if err := exportOnboardingReport(fixture, outside, report); err != nil {
		t.Fatal(err)
	}
	if err := exportOnboardingReport(fixture, outside, report); err == nil {
		t.Fatal("export overwrote an existing report")
	}
	report.AgentOutput = "PASS synthetic-secret"
	if err := exportOnboardingReport(fixture, t.TempDir(), report); err == nil {
		t.Fatal("raw model text accepted")
	}
	raw, err := os.ReadFile(filepath.Join(outside, "onboarding-codex.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "synthetic-secret") || strings.Contains(string(raw), "PASS") {
		t.Fatalf("agent claim leaked: %s", raw)
	}
}

func TestOnboardingOptInsDoNotEnableRunnerSmoke(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	private := t.TempDir()
	log := filepath.Join(private, "spy.log")
	onboardingWrite(t, private, "codex", "#!/bin/sh\nprintf invoked > \"$SPY_LOG\"\nexit 1\n")
	if err := os.Chmod(filepath.Join(private, "codex"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, binary, "-test.run", "^"+smokeTestName+"$", "-test.v", "-test.timeout", "20s")
	c.Env = []string{"PATH=" + private + ":/usr/bin:/bin", "HOME=" + private, "TMPDIR=" + filepath.Join(private, "missing-tmp"), "SPY_LOG=" + log, onboardingCodexOptIn + "=1", onboardingClaudeOptIn + "=1", SmokeArtifactVariable + "=" + filepath.Join(private, "missing-export")}
	raw, err := c.CombinedOutput()
	if err != nil || !strings.Contains(string(raw), "--- SKIP: "+smokeTestName) {
		t.Fatalf("onboarding opt-ins affected Runner smoke: %v: %s", err, raw)
	}
	requireAbsent(t, log)
	requireAbsent(t, filepath.Join(private, "missing-tmp"))
	requireAbsent(t, filepath.Join(private, "missing-export"))
}

func TestOnboardingExportRejectsDestinationSwapAfterResolution(t *testing.T) {
	fixture := t.TempDir()
	parent := t.TempDir()
	destination := filepath.Join(parent, "export")
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := openOnboardingExport(fixture, destination, func() {
		if err := os.Rename(destination, filepath.Join(parent, "original")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(fixture, destination); err != nil {
			t.Fatal(err)
		}
	})
	if root != nil {
		defer root.Close()
	}
	if err == nil {
		t.Fatal("export bound to fixture through post-resolution directory swap")
	}
	requireAbsent(t, filepath.Join(fixture, "onboarding-codex.json"))
}
