package forgepilot_test

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// This file guards one property of requireAbsent: it observes, and it never
// changes what it observed. The helper is used to assert that an opt-in guard
// prevented a write, and it is handed paths it did not create — so a version
// that deleted what it found would destroy a developer's own data on the way to
// reporting a failure, and would erase the evidence of the failure as well.
//
// Every case below runs the real requireAbsent. Testing a copy of the decision,
// or grepping the source for os.RemoveAll, would both stay green against a
// helper that had grown a deletion somewhere else.

// requireAbsentTargetFlag hands the helper entry point the single path it is to
// observe. It is a flag and not an environment variable, because a flag is not
// inherited: nothing in a developer's shell can push
// TestRequireAbsentHelperProcess into its expected-to-fail mode during an
// ordinary `go test` run, and no inherited entry can outrank what the parent
// passed. Only a parent case that has built its own data sets it.
var requireAbsentTargetFlag = flag.String("forgepilot.require-absent-target", "",
	"absolute path for "+requireAbsentHelperName+" to observe; set only by the requireAbsent regression cases")

// requireAbsentHelperName is spelled once and anchored at both ends wherever it
// is used as a -test.run pattern. A pattern that stopped matching would leave
// the child exiting 0 with nothing run, which must not read as a pass.
const requireAbsentHelperName = "TestRequireAbsentHelperProcess"

// TestRequireAbsentHelperProcess is not a case of its own. It is the entry point
// the cases below execute in a subprocess, so that requireAbsent's t.Fatalf can
// be observed as a failure instead of ending the test that is checking it.
func TestRequireAbsentHelperProcess(t *testing.T) {
	target := *requireAbsentTargetFlag
	if target == "" {
		t.Skip("-forgepilot.require-absent-target is unset: this entry point runs only as a child of the requireAbsent cases")
	}
	requireAbsent(t, target)
}

// TestRequireAbsentPassesForAMissingPath is the baseline the other cases are read
// against: the helper accepts an absent path, and accepting it creates nothing.
func TestRequireAbsentPassesForAMissingPath(t *testing.T) {
	binary := compileRootTestBinary(t)
	owned := t.TempDir()
	target := filepath.Join(owned, "absent")

	outcome := runRequireAbsentHelper(t, binary, target)
	requireHelperPassed(t, outcome)

	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("%s exists after a passing observation (%v); the helper created what it was asked to look for", target, err)
	}
	if after := snapshotTree(t, owned); len(after) != 1 {
		t.Fatalf("the owned directory is no longer empty: %v", after)
	}
}

// TestRequireAbsentKeepsAnExistingFile is the case the original helper failed:
// it removed the file and then reported it. The contents are compared byte for
// byte, so a helper that truncated or rewrote the file in place is caught too.
func TestRequireAbsentKeepsAnExistingFile(t *testing.T) {
	binary := compileRootTestBinary(t)
	owned := t.TempDir()
	target := filepath.Join(owned, "smoke-opt-in-must-not-create-this")
	write(t, target, "data the test did not create and must not destroy\n")

	before := snapshotTree(t, owned)
	outcome := runRequireAbsentHelper(t, binary, target)
	requireHelperFailedWithFound(t, outcome, target)
	requireTreeUnchanged(t, owned, before)
}

// TestRequireAbsentKeepsAnExistingDirectory checks the shape a recursive delete
// destroys most: a directory with a file beside a nested subdirectory. The
// snapshot covers every entry's contents, so an emptied-out directory that still
// exists does not pass for an untouched one.
func TestRequireAbsentKeepsAnExistingDirectory(t *testing.T) {
	binary := compileRootTestBinary(t)
	owned := t.TempDir()
	target := filepath.Join(owned, "evidence")
	write(t, filepath.Join(target, "sentinel.txt"), "sentinel\n")
	write(t, filepath.Join(target, "nested", "deeper", "record.json"), "{\"kept\":true}\n")

	before := snapshotTree(t, owned)
	outcome := runRequireAbsentHelper(t, binary, target)
	requireHelperFailedWithFound(t, outcome, target)
	requireTreeUnchanged(t, owned, before)
}

// TestRequireAbsentKeepsASymbolicLinkToAnExistingTarget checks the link is
// reported and that neither the link nor what it points at is followed into a
// deletion. The target lives in the same owned temporary space, so nothing
// outside it is ever at risk.
func TestRequireAbsentKeepsASymbolicLinkToAnExistingTarget(t *testing.T) {
	binary := compileRootTestBinary(t)
	owned := t.TempDir()
	linkTarget := filepath.Join(owned, "target")
	write(t, filepath.Join(linkTarget, "kept.txt"), "kept\n")
	link := filepath.Join(owned, "link")
	if err := os.Symlink(linkTarget, link); err != nil {
		t.Fatal(err)
	}

	before := snapshotTree(t, owned)
	outcome := runRequireAbsentHelper(t, binary, link)
	requireHelperFailedWithFound(t, outcome, link)
	requireTreeUnchanged(t, owned, before)

	// Stated separately from the snapshot: the snapshot would also be satisfied
	// by a helper that had replaced the link with an identical one.
	resolved, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("the symbolic link is no longer readable: %v", err)
	}
	if resolved != linkTarget {
		t.Fatalf("the link now points at %q, not %q", resolved, linkTarget)
	}
}

// TestRequireAbsentReportsADanglingSymbolicLink is the case os.Stat gets wrong.
// Stat follows the link, finds nothing and answers "does not exist", so a helper
// built on it passes while an entry is sitting there. The link was created by
// something; a guard that let it through has had an effect, and the helper must
// say so.
func TestRequireAbsentReportsADanglingSymbolicLink(t *testing.T) {
	binary := compileRootTestBinary(t)
	owned := t.TempDir()
	missing := filepath.Join(owned, "target-that-does-not-exist")
	link := filepath.Join(owned, "dangling")
	if err := os.Symlink(missing, link); err != nil {
		t.Fatal(err)
	}

	before := snapshotTree(t, owned)
	outcome := runRequireAbsentHelper(t, binary, link)
	requireHelperFailedWithFound(t, outcome, link)
	requireTreeUnchanged(t, owned, before)

	if _, err := os.Lstat(missing); !os.IsNotExist(err) {
		t.Fatalf("%s exists after the observation (%v); the helper resolved the link and created its target", missing, err)
	}
}

// helperOutcome is what a child run reported: whether the named test passed, and
// the output it is read from.
type helperOutcome struct {
	passed bool
	output string
}

// runRequireAbsentHelper runs the compiled test binary with the helper entry
// point selected and target as its subject, and establishes that the child
// actually reached that test.
//
// Three things it refuses to treat as a verdict: a child that did not run the
// test at all, one that skipped it, and one that printed neither a PASS nor a
// FAIL line for it. Without those, a compile failure or a mistyped -test.run
// pattern would exit non-zero and read exactly like requireAbsent doing its job.
func runRequireAbsentHelper(t *testing.T, binary, target string) helperOutcome {
	t.Helper()

	// The child is bounded twice for the same reason the smoke guard's children
	// are: a directly executed test binary has no default timeout, and a hang
	// must turn this test red rather than wait for the parent to panic.
	const timeout = 60 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	command := exec.CommandContext(ctx, binary,
		"-test.run", "^"+requireAbsentHelperName+"$", "-test.v",
		"-test.timeout", timeout.String(),
		"-forgepilot.require-absent-target", target)
	command.Dir = projectRoot(t)
	// Nothing here starts a model, and the environment says so explicitly: the
	// smoke settings are stripped by inheritedEnvironment and the opt-in is then
	// pinned to a value the guard rejects. The explicit entries come last
	// because exec.Cmd deduplicates Env and keeps the final occurrence, so a
	// stray inherited value must not be the one that survives.
	command.Env = append(inheritedEnvironment(),
		"PATH="+os.Getenv("PATH"),
		SmokeVariable+"=0")

	raw, err := command.CombinedOutput()
	output := string(raw)
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !asExitError(err, &exit) {
			t.Fatalf("run the %s child: %v\n%s", requireAbsentHelperName, err, output)
		}
		code = exit.ExitCode()
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("%s did not finish within %s; it was killed:\n%s", requireAbsentHelperName, timeout, output)
	}

	if !strings.Contains(output, "=== RUN   "+requireAbsentHelperName) {
		t.Fatalf("%s never ran, so the child's exit code says nothing about requireAbsent:\n%s",
			requireAbsentHelperName, output)
	}
	if strings.Contains(output, "--- SKIP: "+requireAbsentHelperName) {
		t.Fatalf("%s skipped; it did not reach requireAbsent:\n%s", requireAbsentHelperName, output)
	}
	passed := strings.Contains(output, "--- PASS: "+requireAbsentHelperName)
	failed := strings.Contains(output, "--- FAIL: "+requireAbsentHelperName)
	if passed == failed {
		t.Fatalf("%s reported neither exactly one PASS nor exactly one FAIL:\n%s", requireAbsentHelperName, output)
	}
	if passed != (code == 0) {
		t.Fatalf("%s reported passed=%t but the child exited %d:\n%s", requireAbsentHelperName, passed, code, output)
	}
	return helperOutcome{passed: passed, output: output}
}

func requireHelperPassed(t *testing.T, outcome helperOutcome) {
	t.Helper()
	if !outcome.passed {
		t.Fatalf("requireAbsent rejected a path that does not exist:\n%s", outcome.output)
	}
}

// requireHelperFailedWithFound checks the child failed for the stated reason.
// Matching the message keeps an unrelated failure — a panic, a different fatal —
// from being read as requireAbsent correctly refusing.
func requireHelperFailedWithFound(t *testing.T, outcome helperOutcome, target string) {
	t.Helper()
	if outcome.passed {
		t.Fatalf("requireAbsent accepted %s, which exists:\n%s", target, outcome.output)
	}
	if !strings.Contains(outcome.output, requireAbsentFoundMessage) {
		t.Fatalf("requireAbsent failed, but not by reporting an existing entry (%q):\n%s",
			requireAbsentFoundMessage, outcome.output)
	}
	if !strings.Contains(outcome.output, target) {
		t.Fatalf("the failure does not name %s, so it may be about some other path:\n%s", target, outcome.output)
	}
}

// snapshotTree records a whole tree as comparable values: every entry's relative
// path against its kind, permissions and payload. Directories and symbolic links
// are described rather than followed, so a link is compared as a link.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			resolved, err := os.Readlink(path)
			if err != nil {
				return err
			}
			snapshot[relative] = fmt.Sprintf("symlink -> %s", resolved)
		case entry.IsDir():
			snapshot[relative] = fmt.Sprintf("dir %04o", info.Mode().Perm())
		default:
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			snapshot[relative] = fmt.Sprintf("file %04o %q", info.Mode().Perm(), contents)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return snapshot
}

// requireTreeUnchanged compares the tree against an earlier snapshot and names
// what differs. It runs before the parent's t.TempDir cleanup, so the data whose
// retention it asserts is still the data the child saw.
func requireTreeUnchanged(t *testing.T, root string, before map[string]string) {
	t.Helper()
	after := snapshotTree(t, root)
	if reflect.DeepEqual(before, after) {
		return
	}
	var differences []string
	for path, was := range before {
		switch now, present := after[path]; {
		case !present:
			differences = append(differences, fmt.Sprintf("%s was removed (was %s)", path, was))
		case now != was:
			differences = append(differences, fmt.Sprintf("%s changed from %s to %s", path, was, now))
		}
	}
	for path, now := range after {
		if _, present := before[path]; !present {
			differences = append(differences, fmt.Sprintf("%s was created (%s)", path, now))
		}
	}
	t.Fatalf("requireAbsent modified the data it was asked to observe under %s:\n%s",
		root, strings.Join(differences, "\n"))
}
