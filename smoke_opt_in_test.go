package forgepilot_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This file guards the one test that spends model quota. It costs nothing to
// run: no case here starts a model, and the case that checks the real guard
// runs the compiled test binary with a Codex spy shadowing whatever CLI the
// developer has installed, so a regression in the guard turns this file red
// instead of turning it into an unbudgeted round.

// smokeSpyLogVariable tells the stand-in Codex where to record that it was
// asked to run. A recorded invocation is the only unambiguous evidence that a
// guard let something through, so it is written to a file rather than inferred
// from output.
const smokeSpyLogVariable = "FORGEPILOT_TEST_CODEX_SPY_LOG"

// smokeTestName is the test whose guard is under test. It is spelled out once,
// anchored, and reused: a -run pattern that stopped matching would make every
// assertion below vacuously true.
const smokeTestName = "TestCodexSmokeDrivesDependentWorkToTheGoalReviewBoundary"

// TestSmokeOptInAcceptsOnlyTheExactValueOne states the opt-in contract value by
// value. It reads through os.Getenv rather than calling smokeOptedIn with a
// literal, because "unset" and "set to the empty string" are the same argument
// to the function and two different things to a person: one is a machine that
// was never asked, the other is a variable someone emptied on purpose.
func TestSmokeOptInAcceptsOnlyTheExactValueOne(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		set   bool
		value string
		want  bool
	}{
		{name: "unset"},
		{name: "empty string", set: true, value: ""},
		{name: "zero", set: true, value: "0"},
		{name: "false", set: true, value: "false"},
		{name: "FALSE", set: true, value: "FALSE"},
		{name: "off", set: true, value: "off"},
		{name: "no", set: true, value: "no"},
		{name: "true", set: true, value: "true"},
		{name: "yes", set: true, value: "yes"},
		{name: "two", set: true, value: "2"},
		{name: "one with surrounding whitespace", set: true, value: " 1 "},
		{name: "one with a trailing newline", set: true, value: "1\n"},
		{name: "exactly one", set: true, value: "1", want: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// t.Setenv restores whatever the caller's environment held, so a
			// developer who really is opted in neither has that honoured here nor
			// loses it afterwards. Unsetting goes through it for the same reason.
			t.Setenv(SmokeVariable, testCase.value)
			if !testCase.set {
				if err := os.Unsetenv(SmokeVariable); err != nil {
					t.Fatal(err)
				}
			}
			if got := smokeOptedIn(os.Getenv(SmokeVariable)); got != testCase.want {
				t.Fatalf("%s=%q (set: %t): opted in = %t, want %t",
					SmokeVariable, testCase.value, testCase.set, got, testCase.want)
			}
		})
	}
}

// TestRealCodexSmokeConsultsTheOptInBeforeAnySideEffect asks the question the
// value matrix above cannot: is the real entry point behind that contract?
//
// It runs the compiled test binary, one case per environment, and checks three
// separate claims each time. The target test reports SKIP — an overall exit code
// of 0 would also be satisfied by a test that never ran at all. No Codex was
// invoked. And nothing was written where the evidence export would have written,
// which is what places the guard before the side effects rather than among them.
//
// The last case inverts all three. Without it every assertion here would be
// satisfied by a smoke test that had been replaced with an unconditional
// t.Skip — the guard would be gone, the round would never run again, and nothing
// would turn red. It is also the only case in which the Codex spy is shown to
// record anything, so "no Codex was invoked" stops being a claim about a
// mechanism nothing ever exercised.
func TestRealCodexSmokeConsultsTheOptInBeforeAnySideEffect(t *testing.T) {
	binary := compileRootTestBinary(t)
	spyDirectory := writeCodexSpy(t)

	for _, testCase := range []struct {
		name string
		// optIn is the value of SmokeVariable; unset leaves it out entirely.
		optIn string
		set   bool
		// artifact selects the FORGEPILOT_SMOKE_ARTIFACT_DIR shape: "" leaves it
		// unset, "absolute" names a fresh path that must stay uncreated, and
		// "relative" names a path the export helper rejects outright.
		artifact string
		// wantInvoked inverts every assertion: the guard is expected to let the
		// round begin, and the spy is expected to record the runtime probe.
		wantInvoked bool
		// timeout bounds the child. A guard regression that blocks — on stdin, on
		// authentication — must fail this test rather than hang until the parent
		// go test panics and orphans the process group it started.
		timeout time.Duration
	}{
		{name: "opt-in disabled with zero", optIn: "0", set: true},
		{name: "opt-in disabled with false", optIn: "false", set: true},
		{name: "opt-in unset but evidence requested", artifact: "absolute"},
		{name: "opt-in spelled with whitespace", optIn: " 1 ", set: true, artifact: "absolute"},
		// The discriminating case. The export refuses a relative destination, so
		// under the old "non-empty means on" guard this environment reached
		// openSmokeExport and failed there. A SKIP is only possible when the
		// opt-in is decided before the export is opened.
		{name: "opt-in disabled with an invalid evidence path", optIn: "0", set: true, artifact: "relative"},
		// The positive control. No evidence directory is asked for, so the round
		// leaves nothing outside its own fixture; the spy answers the runtime
		// version probe by refusing, so the Runner stops before a session is
		// planned and no quota is spent. It is guarded by a pre-flight that
		// resolves `codex` the way the child will.
		{name: "exact opt-in reaches the runtime", optIn: "1", set: true, wantInvoked: true, timeout: 5 * time.Minute},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			spyLog := filepath.Join(t.TempDir(), "codex-invocations.log")
			// The destination is named but never created: the assertion afterwards
			// is that it still does not exist.
			evidence := filepath.Join(t.TempDir(), "evidence")
			relative, relativeTarget := privateRelativeArtifactPath(t, projectRoot(t))

			environment := []string{
				// PATH puts the spy first, so even a guard that let the round start
				// could not reach a real, authenticated Codex.
				"PATH=" + spyDirectory + string(os.PathListSeparator) + os.Getenv("PATH"),
				smokeSpyLogVariable + "=" + spyLog,
			}
			environment = append(environment, inheritedEnvironment()...)
			if testCase.set {
				environment = append(environment, SmokeVariable+"="+testCase.optIn)
			}
			switch testCase.artifact {
			case "absolute":
				environment = append(environment, SmokeArtifactVariable+"="+evidence)
			case "relative":
				environment = append(environment, SmokeArtifactVariable+"="+relative)
			}

			if testCase.wantInvoked {
				requireShadowedCodex(t, environment, spyDirectory)
			}

			timeout := testCase.timeout
			if timeout == 0 {
				timeout = 2 * time.Minute
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			// The child gets its own deadline as well: a compiled test binary run
			// directly has no default timeout at all, so without this the only
			// thing bounding a hung round would be the context kill.
			command := exec.CommandContext(ctx, binary,
				"-test.run", "^"+smokeTestName+"$", "-test.v",
				"-test.timeout", timeout.String())
			command.Dir = projectRoot(t)
			command.Env = environment
			raw, err := command.CombinedOutput()
			output := string(raw)
			code := 0
			if err != nil {
				var exit *exec.ExitError
				if !asExitError(err, &exit) {
					t.Fatalf("%v\n%s", err, output)
				}
				code = exit.ExitCode()
			}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				t.Fatalf("the guarded test did not finish within %s; it was killed:\n%s", timeout, output)
			}

			skipped := strings.Contains(output, "--- SKIP: "+smokeTestName)
			spy, spyErr := os.ReadFile(spyLog)
			if spyErr != nil && !os.IsNotExist(spyErr) {
				t.Fatalf("the Codex spy log could not be read: %v", spyErr)
			}
			invoked := spyErr == nil

			if testCase.wantInvoked {
				// The round began, so it must have reached the runtime and then
				// stopped on the spy's refusal. A SKIP here would mean the guard
				// refuses its own documented opt-in — the failure mode every other
				// case in this table is blind to.
				if skipped {
					t.Fatalf("%s skipped with %s=%q; the opt-in it documents no longer starts it:\n%s",
						smokeTestName, SmokeVariable, testCase.optIn, output)
				}
				if !invoked {
					t.Fatalf("%s ran but never asked for Codex, so the spy assertions in the other cases "+
						"check a mechanism nothing exercises:\n%s", smokeTestName, output)
				}
				if !strings.Contains(string(spy), "codex") {
					t.Fatalf("the spy log does not record a Codex invocation: %q", spy)
				}
				if code == 0 {
					t.Fatalf("the round reported success against a Codex spy that refuses to run:\n%s", output)
				}
				return
			}

			if code != 0 {
				t.Fatalf("the guarded test exited %d instead of skipping:\n%s", code, output)
			}
			if !skipped {
				t.Fatalf("%s did not report SKIP; exit code 0 alone does not say it ran and declined:\n%s",
					smokeTestName, output)
			}
			if invoked {
				t.Fatalf("Codex was invoked without an exact opt-in: %s", spy)
			}
			requireAbsent(t, evidence)
			// A relative destination is resolved against the child's working
			// directory, which is the checkout. The path it resolves to is the
			// one checked here, and it sits in this subtest's own temporary
			// space rather than in the checkout, so the assertion observes only
			// ground this test owns.
			requireAbsent(t, relativeTarget)
		})
	}
}

// requireAbsentFoundMessage is the fragment requireAbsent reports when the path
// is occupied. It is a constant so the regression tests in
// require_absent_test.go can recognise the real helper's failure rather than
// accept any non-zero exit — a compile error, a timeout or a test that never
// ran would otherwise pass for a correct refusal.
const requireAbsentFoundMessage = "expected it not to exist, but found an existing entry"

// requireAbsent fails when a path the guard should have prevented exists. It is
// a read-only assertion: it observes and reports, and it removes nothing. The
// helper cannot know whether what it found belongs to this test, so deleting it
// would risk destroying data the test never created — and would also erase the
// very evidence its failure message is about.
//
// The observation goes through os.Lstat rather than os.Stat, so a symbolic link
// counts as an existing entry in its own right, including one whose target is
// gone: a dangling link is something that was created, and a guard that let it
// through has still had an effect.
//
// A stat error that is not "no such file" is reported as undecided rather than
// as evidence of a leak: claiming the export ran because a stat returned EACCES
// would be an assertion saying more than it checked. The message likewise states
// only what was seen. It does not claim the entry came from the smoke export,
// because nothing here establishes that.
func requireAbsent(t *testing.T, path string) {
	t.Helper()
	switch _, err := os.Lstat(path); {
	case err == nil:
		t.Fatalf("%s: %s; it has been left untouched", path, requireAbsentFoundMessage)
	case os.IsNotExist(err):
	default:
		t.Fatalf("whether %s exists could not be decided: %v", path, err)
	}
}

// privateRelativeArtifactPath produces the relative evidence destination for the
// discriminating case, together with the absolute path that destination names.
//
// The case needs a path the export helper refuses, which means a relative one,
// and the child resolves it against the checkout it runs in. Spelling it as a
// fixed name in the checkout made the test reach for a path it did not own: a
// developer with something of that name had their own data inspected. Anchoring
// the relative path at this subtest's own temporary directory keeps the refusal
// being tested and moves the observed location onto ground the test created.
//
// Both halves are returned together so the environment the child receives and
// the path afterwards asserted to be absent cannot drift apart.
//
// Both ends are resolved through filepath.EvalSymlinks before the relative path
// is computed. filepath.Rel is lexical, and its ".." prefix assumes the depth it
// counted is the depth the kernel will walk; os.Getwd may return the logical
// path a shell arrived by, so a checkout reached through a symbolic link would
// make the child resolve the relative path somewhere other than the absolute
// path asserted here. Resolving first makes the two the same place by
// construction.
func privateRelativeArtifactPath(t *testing.T, workingDirectory string) (string, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(workingDirectory)
	if err != nil {
		t.Fatalf("resolve the working directory %s: %v", workingDirectory, err)
	}
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve this subtest's temporary directory: %v", err)
	}
	target := filepath.Join(parent, "smoke-opt-in-must-not-create-this")
	relative, err := filepath.Rel(root, target)
	if err != nil {
		t.Fatalf("express %s relative to %s: %v", target, root, err)
	}
	if filepath.IsAbs(relative) {
		t.Fatalf("%s is absolute; the export would no longer refuse it and the case would stop discriminating", relative)
	}
	return relative, target
}

// requireShadowedCodex resolves `codex` the way the child will, and refuses to
// run the positive control unless it lands on the spy. The control deliberately
// lets the round start, so this is the one assertion whose failure has to happen
// before the child does, not after.
func requireShadowedCodex(t *testing.T, environment []string, spyDirectory string) {
	t.Helper()
	probe := exec.Command("/bin/sh", "-c", "command -v codex")
	probe.Env = environment
	output, err := probe.CombinedOutput()
	resolved := strings.TrimSpace(string(output))
	if err != nil || resolved != filepath.Join(spyDirectory, "codex") {
		t.Fatalf("codex resolves to %q (%v), not the spy in %s; refusing to start a round that could reach a model",
			resolved, err, spyDirectory)
	}
}

// inheritedEnvironment passes the caller's environment through with every smoke
// setting stripped. A developer who is legitimately opted in must not have that
// leak into a subprocess whose entire purpose is to observe the guard saying no.
func inheritedEnvironment() []string {
	stripped := map[string]bool{
		SmokeVariable:         true,
		SmokeArtifactVariable: true,
		smokeSpyLogVariable:   true,
		"PATH":                true,
		// The fake runtime is configured by environment too, and a stray value
		// would change what the guarded test would have done had it run.
		"FORGEPILOT_FAKE_AGENT": true,
	}
	var environment []string
	for _, entry := range os.Environ() {
		if name, _, found := strings.Cut(entry, "="); found && stripped[name] {
			continue
		}
		environment = append(environment, entry)
	}
	return environment
}

// compileRootTestBinary builds this package's test binary so one case can be run
// with an environment of its own. Compiling once keeps the five cases from
// paying for five builds.
func compileRootTestBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "forgepilot-root.test")
	build := exec.Command("go", "test", "-c", "-o", binary, ".")
	build.Dir = projectRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compile this package's test binary: %v\n%s", err, output)
	}
	return binary
}

// writeCodexSpy installs a stand-in `codex` earlier on PATH than any real one.
// It records the call and refuses to do anything else: a spy that fell back to
// the real CLI would turn a failing assertion into a bill.
func writeCodexSpy(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "codex")
	write(t, path, `#!/bin/sh
printf 'codex %s\n' "$*" >> "$`+smokeSpyLogVariable+`"
echo "the Codex spy refuses to run: this test must never reach a model" >&2
exit 91
`)
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	return directory
}
