package forgepilot_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runnerFixture builds a repository the Runner can actually drive: real Git, a
// real canonical check, real Story files. The canonical check passes only when
// every marker file a session left behind says "done", so a verification result
// is produced by the repository rather than declared by the test.
type runnerFixture struct {
	root    string
	binary  string
	control string
}

func newRunnerFixture(t *testing.T, stories ...string) runnerFixture {
	t.Helper()
	root, binary := fixture(t)
	if len(stories) == 0 {
		stories = []string{"a.md", "b.md", "c.md"}
	}
	for _, name := range stories {
		path := filepath.Join(root, "specs", "stories", name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
		story, acceptance := []byte("# story "+name+"\n"), []byte("# acceptance "+name+"\n")
		writeReadinessStory(t, root, name, story, acceptance)
	}
	write(t, filepath.Join(root, "Makefile"), "verify:\n\t@sh verify.sh\n")
	write(t, filepath.Join(root, "verify.sh"), canonicalCheck)
	commitAll(t, root, "seed")
	return runnerFixture{root: root, binary: binary, control: t.TempDir()}
}

func writeReadinessStory(t *testing.T, root, name string, story, acceptance []byte) {
	t.Helper()
	path := filepath.Join(root, "specs", "stories", name)
	for file, contents := range map[string][]byte{
		"story.md": story, "acceptance.md": acceptance,
		"readiness.json": []byte(fmt.Sprintf(`{"schema_version":1,"story_ref":"specs/stories/%s","story_md_digest":"%s","acceptance_md_digest":"%s","criteria":[],"inputs":[],"outputs":[],"decision_follow_ups":[]}`, name, digest(story), digest(acceptance))),
	} {
		if err := os.WriteFile(filepath.Join(path, file), contents, 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func digest(contents []byte) string {
	sum := sha256.Sum256(contents)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// canonicalCheck is the managed project's own verification. It fails when any
// marker is present but not complete, which is how a test produces a genuine
// FAIL Evidence without the test asserting one into existence.
const canonicalCheck = `set -e
for marker in wi-*.txt; do
  [ -e "$marker" ] || continue
  grep -q '^done$' "$marker" || { echo "canonical check failed: $marker is incomplete"; exit 1; }
done
echo "canonical check passed"
`

// fakeAgent writes a stand-in coding CLI. body is shell run with $workspace,
// $result, $handoff, $item (the Work Item the briefing names) and $attempt (how
// many times this script has been asked about that Work Item) already set.
func (fixture runnerFixture) fakeAgent(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-agent.sh")
	script := fmt.Sprintf(`#!/bin/sh
# The runtime is asked for its version before any session is planned. Answering
# here keeps that probe from running the session body, which is a real property
# of the contract and not a fixture convenience.
if [ "$1" = "--version" ]; then
  echo "fake-agent 1.0"
  exit 0
fi
workspace=""
result=""
handoff=""
while [ $# -gt 0 ]; do
  case "$1" in
    --workspace) workspace="$2"; shift 2;;
    --result) result="$2"; shift 2;;
    --handoff) handoff="$2"; shift 2;;
    *) shift;;
  esac
done
cat > /dev/null
item=$(grep -o 'WI-[0-9][0-9]*' "$handoff" | head -1)
lower=$(echo "$item" | tr 'A-Z' 'a-z')
control=%q
mkdir -p "$control"
attempt=$(cat "$control/$item" 2>/dev/null || echo 0)
attempt=$((attempt + 1))
printf '%%s' "$attempt" > "$control/$item"
printf '%%s\n' "$item" >> "$control/sessions"
cp "$handoff" "$control/handoff-$item.txt" || { echo "fixture: the handoff could not be recorded" >&2; exit 90; }
%s
`, fixture.control, body)
	write(t, path, script)
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

// implementsCleanly is the ordinary session: it writes the marker the canonical
// check wants and reports that its implementation attempt ended.
const implementsCleanly = `printf 'done\n' > "$workspace/$lower.txt"
printf '{"outcome":"implementation_finished","summary":"implemented %s"}' "$item" > "$result"
`

// sessions lists every Work Item a session was started for, in order. Its
// length is the number of sessions, which is what "a new session per attempt"
// means concretely.
func (fixture runnerFixture) sessions(t *testing.T) []string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(fixture.control, "sessions"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var items []string
	for _, line := range strings.Split(strings.TrimSpace(string(contents)), "\n") {
		if line != "" {
			items = append(items, line)
		}
	}
	return items
}

// handoff reports the briefing the most recent session for one Work Item was
// given. A session count says something started; this says what it was told,
// which is the part a handoff assertion is actually about. A missing briefing
// fails the test rather than reading as an empty one.
func (fixture runnerFixture) handoff(t *testing.T, itemID string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(fixture.control, "handoff-"+itemID+".txt"))
	if err != nil {
		t.Fatalf("no handoff was recorded for %s: %v", itemID, err)
	}
	return string(contents)
}

// runForge runs the CLI and reports its exit code, never failing the test for a
// non-zero one: the exit code is what most of these tests are about.
func (fixture runnerFixture) runForge(t *testing.T, agentPath string, arguments ...string) (string, int) {
	t.Helper()
	command := exec.Command(fixture.binary, arguments...)
	command.Dir = fixture.root
	command.Env = os.Environ()
	if agentPath != "" {
		command.Env = append(command.Env, "FORGEPILOT_FAKE_AGENT="+agentPath)
	}
	output, err := command.CombinedOutput()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !asExitError(err, &exit) {
			t.Fatalf("%v: %v\n%s", arguments, err, output)
		}
		code = exit.ExitCode()
	}
	return string(output), code
}

func asExitError(err error, target **exec.ExitError) bool {
	exit, ok := err.(*exec.ExitError)
	if ok {
		*target = exit
	}
	return ok
}

// seedGoal creates a GOAL-policy goal with the given dependency chain, using
// the product commands only.
func (fixture runnerFixture) seedGoal(t *testing.T, goalID string, items ...[]string) {
	t.Helper()
	mustRun(t, fixture.binary, fixture.root, "goal", "create", "--id", goalID, "--title", "Goal "+goalID, "--review-policy", "goal")
	for _, item := range items {
		arguments := []string{"work", "add", "--goal", goalID, "--story", item[0]}
		for _, dependency := range item[1:] {
			arguments = append(arguments, "--depends-on", dependency)
		}
		mustRun(t, fixture.binary, fixture.root, arguments...)
	}
}

func write(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}

func gitOutput(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return string(output)
}
