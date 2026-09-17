package forgepilot_test

import (
	"context"
	"github.com/CarlLee1983/ForgePilot/internal/repository"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func onboardingGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", root}, args...)...)
	c.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + root, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func onboardingWrite(t *testing.T, root, path, contents string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func onboardingRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	onboardingGit(t, root, "init", "-q")
	onboardingGit(t, root, "config", "user.email", "onboarding@example.invalid")
	onboardingGit(t, root, "config", "user.name", "Onboarding fixture")
	onboardingWrite(t, root, "Makefile", "verify:\n\t@true\n")
	onboardingWrite(t, root, ".gitignore", ".forgepilot/\n")
	onboardingWrite(t, root, "specs/stories/first/story.md", "# First Story\n")
	onboardingWrite(t, root, "specs/stories/first/acceptance.md", "Run make verify.\n")
	onboardingGit(t, root, "add", ".")
	onboardingGit(t, root, "commit", "-qm", "fixture")
	return root
}

func onboardingPlan(t *testing.T, root, kind, candidate string, extra ...string) (string, error) {
	t.Helper()
	args := []string{filepath.Join(projectRoot(t), "scripts/onboarding/source-built-plan.sh"),
		"--source", projectRoot(t), "--commit", strings.Repeat("a", 40),
		"--stage-root", t.TempDir(), "--entrypoint", filepath.Join(t.TempDir(), "forgepilot"),
		"--target", root, "--candidate-kind", kind, "--candidate", candidate,
		"--goal-id", "first", "--goal-title", "First goal", "--story", "specs/stories/first"}
	c := exec.Command("/bin/sh", append(args, extra...)...)
	c.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + root, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	out, err := c.CombinedOutput()
	return string(out), err
}

func TestOnboardingPlanRequiresLiteralHeadSHA(t *testing.T) {
	root := onboardingRepository(t)
	if out, err := onboardingPlan(t, root, "COMMIT", "HEAD"); err == nil {
		t.Fatalf("accepted symbolic Candidate: %s", out)
	}
	head := onboardingGit(t, root, "rev-parse", "HEAD")
	if out, err := onboardingPlan(t, root, "COMMIT", head); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}

func TestOnboardingPlanSnapshotUsesHeadSeedRatherThanRealIndex(t *testing.T) {
	for _, tracked := range []bool{true, false} {
		t.Run(map[bool]string{true: "head tracked real index deleted", false: "ignored forced into real index"}[tracked], func(t *testing.T) {
			root := onboardingRepository(t)
			if !tracked {
				onboardingGit(t, root, "rm", "Makefile")
				onboardingGit(t, root, "commit", "-qm", "without Makefile")
				onboardingWrite(t, root, "Makefile", "verify:\n\t@true\n")
			}
			onboardingWrite(t, root, ".gitignore", ".forgepilot/\nMakefile\n")
			if tracked {
				onboardingGit(t, root, "rm", "--cached", "Makefile")
			} else {
				onboardingGit(t, root, "add", "-f", "Makefile")
			}
			before := onboardingGit(t, root, "status", "--porcelain=v1")
			out, err := onboardingPlan(t, root, "SNAPSHOT", "current")
			if (err == nil) != tracked {
				t.Fatalf("HEAD-tracked=%t: %v: %s", tracked, err, out)
			}
			if after := onboardingGit(t, root, "status", "--porcelain=v1"); after != before {
				t.Fatal("plan changed real index/worktree")
			}
			// Independent oracle: the actual product snapshot owns the
			// HEAD-seeded private-index composition (benign fixture only).
			snapshot, err := repository.CaptureSnapshot(context.Background(), root, "WI-001", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			captured := onboardingGit(t, root, "ls-tree", snapshot.Revision, "--", "Makefile")
			if (captured != "") != tracked {
				t.Fatalf("snapshot disagrees with plan: %q", captured)
			}

		})
	}
}

func TestOnboardingPlanInspectsStaticRegularMakefile(t *testing.T) {
	for _, mode := range []string{"symlink", "conditional", "define", "assignment", "shell"} {
		t.Run(mode, func(t *testing.T) {
			root := onboardingRepository(t)
			sentinel := filepath.Join(t.TempDir(), "executed")
			content := map[string]string{"conditional": "ifeq (a,b)\nverify:\nendif\n", "define": "define recipe\nverify:\nendef\n", "assignment": "verify:=variable\n", "shell": "X := $(shell touch " + sentinel + ")\nverify:\n\t@true\n"}[mode]
			if mode == "symlink" {
				outside := filepath.Join(t.TempDir(), "Makefile")
				onboardingWrite(t, filepath.Dir(outside), "Makefile", "verify:\n\t@true\n")
				if err := os.Remove(filepath.Join(root, "Makefile")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "Makefile")); err != nil {
					t.Fatal(err)
				}
			} else {
				onboardingWrite(t, root, "Makefile", content)
			}
			out, err := onboardingPlan(t, root, "SNAPSHOT", "current")
			if (err == nil) != (mode == "shell") {
				t.Fatalf("%s: %v: %s", mode, err, out)
			}
			requireAbsent(t, sentinel)
		})
	}
}

func TestOnboardingPlanExposesExactActions(t *testing.T) {
	root := onboardingRepository(t)
	out, err := onboardingPlan(t, root, "SNAPSHOT", "current", "--format", "actions")
	if err != nil {
		t.Fatalf("action plan: %v: %s", err, out)
	}
	if !strings.HasPrefix(out, "forgepilot-onboarding-plan-v1\x00") {
		t.Fatalf("missing action protocol: %q", out)
	}
	if !strings.Contains(out, "--title\x00First goal\x00") {
		t.Fatal("title is not an exact argv element")
	}
}

func TestOnboardingPlanRejectsTransformingSnapshotAndNestedInstall(t *testing.T) {
	for _, mode := range []string{"filter", "autocrlf", "nested-stage", "nested-entry", "symlink-stage"} {
		t.Run(mode, func(t *testing.T) {
			root := onboardingRepository(t)
			sentinel := filepath.Join(t.TempDir(), "filter-ran")
			var extra []string
			switch mode {
			case "filter":
				onboardingWrite(t, root, ".gitattributes", "Makefile filter=unsafe\n")
				onboardingGit(t, root, "config", "filter.unsafe.clean", "touch "+sentinel)
			case "autocrlf":
				onboardingGit(t, root, "config", "core.autocrlf", "true")
			case "nested-stage":
				extra = []string{"--stage-root", filepath.Join(root, "stage")}
			case "nested-entry":
				extra = []string{"--entrypoint", filepath.Join(root, "bin/forgepilot")}
			case "symlink-stage":
				p := filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(root, p); err != nil {
					t.Fatal(err)
				}
				extra = []string{"--stage-root", filepath.Join(p, "new")}
			}
			if out, err := onboardingPlan(t, root, "SNAPSHOT", "current", extra...); err == nil {
				t.Fatalf("accepted %s: %s", mode, out)
			}
			requireAbsent(t, sentinel)
		})
	}
}

func TestOnboardingPlanStopsForMissingOrUnsafeStoryBeforeStateReuse(t *testing.T) {
	root := onboardingRepository(t)
	if err := os.RemoveAll(filepath.Join(root, "specs/stories/first")); err != nil {
		t.Fatal(err)
	}
	onboardingWrite(t, root, ".forgepilot/state.json", "existing state sentinel")
	out, err := onboardingPlan(t, root, "SNAPSHOT", "current", "--format", "actions")
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	actions, err := readOnboardingActions(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range actions {
		if a.Phase == "repository" {
			t.Fatalf("missing Story bypassed review: %#v", a)
		}
	}
	outside := t.TempDir()
	onboardingWrite(t, outside, "story.md", "outside")
	onboardingWrite(t, root, "specs/stories/first/acceptance.md", "local")
	if err := os.Symlink(filepath.Join(outside, "story.md"), filepath.Join(root, "specs/stories/first/story.md")); err != nil {
		t.Fatal(err)
	}
	if out, err := onboardingPlan(t, root, "SNAPSHOT", "current"); err == nil {
		t.Fatalf("accepted Story symlink: %s", out)
	}
}

func TestOnboardingPlanRejectsMissingGitUnknownKindAndStaleCommit(t *testing.T) {
	if out, err := onboardingPlan(t, t.TempDir(), "SNAPSHOT", "current"); err == nil {
		t.Fatalf("non-Git target accepted: %s", out)
	}
	root := onboardingRepository(t)
	old := onboardingGit(t, root, "rev-parse", "HEAD")
	onboardingGit(t, root, "commit", "--allow-empty", "-qm", "later")
	for _, test := range []struct{ kind, candidate string }{{"COMMIT", old}, {"unknown", old}, {"COMMIT", old[:12]}} {
		if out, err := onboardingPlan(t, root, test.kind, test.candidate); err == nil {
			t.Fatalf("invalid Candidate accepted: %s", out)
		}
	}
}

func TestOnboardingPlanDoesNotMissHeadAttributesDeletedFromRealIndex(t *testing.T) {
	root := onboardingRepository(t)
	onboardingWrite(t, root, ".gitattributes", "Makefile filter=custom\n")
	onboardingGit(t, root, "add", ".gitattributes")
	onboardingGit(t, root, "commit", "-qm", "attributes")
	onboardingGit(t, root, "rm", ".gitattributes")
	if out, err := onboardingPlan(t, root, "SNAPSHOT", "current"); err == nil {
		t.Fatalf("HEAD attribute fallback ignored: %s", out)
	}
}

func TestOnboardingPlanPreservesExactArgumentBoundaries(t *testing.T) {
	root := onboardingRepository(t)
	title := "developer's goal; $(touch unwanted)\nnext line"
	out, err := onboardingPlan(t, root, "SNAPSHOT", "current", "--format", "actions", "--goal-title", title)
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	actions, err := readOnboardingActions(out)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range actions {
		if a.ID == "goal-create" {
			found = true
			if len(a.Args) != 7 || a.Args[6] != title {
				t.Fatalf("title argv changed: %#v", a.Args)
			}
		}
	}
	if !found {
		t.Fatal("no goal action")
	}
	requireAbsent(t, filepath.Join(root, "unwanted"))
}

func TestOnboardingPlanRejectsInstallationPathAmbiguity(t *testing.T) {
	for _, mode := range []string{"colon-relative-entry", "stage-bin-symlink", "stage-symlink", "temporary-directory", "temporary-symlink", "entrypoint-directory"} {
		t.Run(mode, func(t *testing.T) {
			root := onboardingRepository(t)
			stage := t.TempDir()
			entry := filepath.Join(t.TempDir(), "forgepilot")
			switch mode {
			case "colon-relative-entry":
				stage = filepath.Join(stage, "colon:", "next")
				entry = "relative/forgepilot"
			case "stage-bin-symlink":
				if err := os.Mkdir(filepath.Join(stage, strings.Repeat("a", 40)), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(root, filepath.Join(stage, strings.Repeat("a", 40), "bin")); err != nil {
					t.Fatal(err)
				}
			case "stage-symlink":
				if err := os.Symlink(root, filepath.Join(stage, strings.Repeat("a", 40))); err != nil {
					t.Fatal(err)
				}
			case "temporary-directory":
				if err := os.Mkdir(entry+".new", 0700); err != nil {
					t.Fatal(err)
				}
			case "temporary-symlink":
				if err := os.Symlink(root, entry+".new"); err != nil {
					t.Fatal(err)
				}
			case "entrypoint-directory":
				if err := os.Mkdir(entry, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if out, err := onboardingPlan(t, root, "SNAPSHOT", "current", "--stage-root", stage, "--entrypoint", entry); err == nil {
				t.Fatalf("accepted unsafe installation: %s", out)
			}
		})
	}
}

func TestOnboardingPlanRejectsTagObjectAndNonRuleMakeText(t *testing.T) {
	root := onboardingRepository(t)
	onboardingGit(t, root, "tag", "-am", "fixture tag", "candidate")
	tag := onboardingGit(t, root, "rev-parse", "candidate")
	if out, err := onboardingPlan(t, root, "COMMIT", tag); err == nil {
		t.Fatalf("accepted tag object: %s", out)
	}
	for _, makefile := range []string{"verify: X=1\n", "TEXT = \\\nverify:\n", "verify:::=not-a-rule\n", "verify:::\n"} {
		onboardingWrite(t, root, "Makefile", makefile)
		if out, err := onboardingPlan(t, root, "SNAPSHOT", "current"); err == nil {
			t.Fatalf("accepted non-rule %q: %s", makefile, out)
		}
	}
}

func TestOnboardingPlanBindsTargetCommandsToApprovedEntrypoint(t *testing.T) {
	root := onboardingRepository(t)
	entry := filepath.Join(t.TempDir(), "approved forgepilot")
	out, err := onboardingPlan(t, root, "SNAPSHOT", "current", "--format", "actions", "--entrypoint", entry)
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	actions, err := readOnboardingActions(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range actions {
		if a.Phase == "repository" && a.Args[0] != entry {
			t.Fatalf("unbound repository command: %#v", a)
		}
	}
}

func TestOnboardingPlanRejectsRelativeSource(t *testing.T) {
	root := onboardingRepository(t)
	if out, err := onboardingPlan(t, root, "SNAPSHOT", "current", "--source", "."); err == nil {
		t.Fatalf("source changes meaning at clone cwd: %s", out)
	}
}
