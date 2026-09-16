package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/process"
)

func TestDecodeResultAcceptsOnlyTheThreeOutcomes(t *testing.T) {
	valid := []string{
		`{"outcome":"implementation_finished","summary":"did the thing"}`,
		`{"outcome":"implementation_finished","summary":"partly","unfinished":["tests for the error path"]}`,
		`{"outcome":"needs_human","summary":"stuck","needs_human":{"question":"which store?","options":["a","b"]}}`,
		`{"outcome":"execution_failed","summary":"no toolchain","error":"go: command not found"}`,
	}
	for _, payload := range valid {
		if _, err := DecodeResult([]byte(payload)); err != nil {
			t.Fatalf("DecodeResult(%s) = %v", payload, err)
		}
	}

	invalid := map[string]string{
		"empty":                   ``,
		"unknown outcome":         `{"outcome":"done","summary":"s"}`,
		"missing outcome":         `{"summary":"s"}`,
		"missing summary":         `{"outcome":"implementation_finished"}`,
		"blank summary":           `{"outcome":"implementation_finished","summary":"   "}`,
		"needs_human no question": `{"outcome":"needs_human","summary":"s"}`,
		"failed without error":    `{"outcome":"execution_failed","summary":"s"}`,
		"finished with question":  `{"outcome":"implementation_finished","summary":"s","needs_human":{"question":"q"}}`,
		"unknown field":           `{"outcome":"implementation_finished","summary":"s","verified":true}`,
		"trailing value":          `{"outcome":"implementation_finished","summary":"s"} {"outcome":"needs_human"}`,
		"not an object":           `"implementation_finished"`,
	}
	for name, payload := range invalid {
		if _, err := DecodeResult([]byte(payload)); !IsProtocolError(err) {
			t.Fatalf("%s: DecodeResult err = %v, want protocol error", name, err)
		}
	}
}

// A session that exits cleanly but leaves no usable result has told us nothing.
// Reading that as completion is the false success the contract exists to stop.
func TestCleanExitWithoutAResultIsAProtocolError(t *testing.T) {
	workspace := t.TempDir()
	for name, script := range map[string]string{
		"writes nothing":  "#!/bin/sh\nexit 0\n",
		"writes prose":    "#!/bin/sh\nfor a in \"$@\"; do :; done\necho done > \"$3\"\nexit 0\n",
		"writes bad json": "#!/bin/sh\necho '{\"outcome\":\"all good\"}' > \"$4\"\nexit 0\n",
		"exits non-zero":  "#!/bin/sh\nexit 7\n",
	} {
		t.Run(name, func(t *testing.T) {
			runtime := Fake{Command: writeScript(t, script)}
			request := Request{Workspace: workspace, ArtifactDir: t.TempDir(), Handoff: "do the work"}
			session, err := Start(runtime, request, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := session.Wait(waitContext(t, 30*time.Second)); !IsProtocolError(err) {
				t.Fatalf("Wait err = %v, want protocol error", err)
			}
		})
	}
}

func TestFakeSessionDeliversTheHandoffAndReturnsItsResult(t *testing.T) {
	script := `#!/bin/sh
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
piped=$(cat)
printf '%s' "$piped" > "$workspace/from-stdin.txt"
cp "$handoff" "$workspace/from-file.txt"
printf '{"outcome":"implementation_finished","summary":"wrote two files"}' > "$result"
`
	workspace := t.TempDir()
	request := Request{Workspace: workspace, ArtifactDir: t.TempDir(), Handoff: "briefing body"}
	session, err := Start(Fake{Command: writeScript(t, script)}, request, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.Wait(waitContext(t, 30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != ImplementationFinished || result.Summary != "wrote two files" {
		t.Fatalf("result = %#v", result)
	}
	for _, name := range []string{"from-stdin.txt", "from-file.txt"} {
		contents, err := os.ReadFile(filepath.Join(workspace, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(contents) != "briefing body" {
			t.Fatalf("%s = %q", name, contents)
		}
	}
	if !session.Identity.Recorded() {
		t.Fatalf("identity = %#v", session.Identity)
	}
}

// A timeout must stop the whole process group, not just the outermost process:
// a coding CLI spawns children that keep writing the workspace otherwise.
func TestTimeoutStopsTheWholeProcessGroup(t *testing.T) {
	script := `#!/bin/sh
workspace=""
while [ $# -gt 0 ]; do
  case "$1" in
    --workspace) workspace="$2"; shift 2;;
    *) shift;;
  esac
done
( sleep 30; echo late > "$workspace/grandchild.txt" ) &
echo "$!" > "$workspace/grandchild.pid"
sleep 30
`
	workspace := t.TempDir()
	request := Request{Workspace: workspace, ArtifactDir: t.TempDir(), Handoff: "x"}
	session, err := Start(Fake{Command: writeScript(t, script)}, request, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Wait(waitContext(t, 2*time.Second)); !errors.Is(err, ErrStopped) {
		t.Fatalf("Wait err = %v, want a stopped session", err)
	}
	pid := strings.TrimSpace(readFile(t, filepath.Join(workspace, "grandchild.pid")))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(t, pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("grandchild %s survived the timeout", pid)
}

// waitContext bounds one Wait the way the Runner does: the caller owns the
// limit, and Wait only reports that the context ended.
func waitContext(t *testing.T, within time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	t.Cleanup(cancel)
	return ctx
}

func TestInspectDistinguishesGoneFromOursFromUnrelated(t *testing.T) {
	script := "#!/bin/sh\nsleep 30\n"
	request := Request{Workspace: t.TempDir(), ArtifactDir: t.TempDir(), Handoff: "x"}
	session, err := Start(Fake{Command: writeScript(t, script)}, request, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	identity := session.Identity
	if !identity.Recorded() {
		// ps never made the worker's arguments readable, so there is no identity
		// to ask questions about. That is the fail-closed answer working, not a
		// failure of what this test is about — but it is worth saying out loud
		// rather than asserting through.
		t.Cleanup(func() { _ = syscall.Kill(-session.command.Process.Pid, syscall.SIGKILL) })
		t.Skipf("ps did not report a readable command for the worker: %#v", identity)
	}
	if liveness, err := Inspect(identity); err != nil || liveness != Ours {
		t.Fatalf("live process liveness = %v, %v", liveness, err)
	}

	// A recorded start time that does not match means the pid was reused, so our
	// worker is gone and that process must not be signalled.
	for name, reused := range map[string]ProcessIdentity{
		"different start time": {PID: identity.PID, PGID: identity.PGID, Executable: identity.Executable,
			ObservedStart: "Mon Jan  1 00:00:00 2001", ObservedCommand: identity.ObservedCommand},
		"different command": {PID: identity.PID, PGID: identity.PGID, Executable: identity.Executable,
			ObservedStart: identity.ObservedStart, ObservedCommand: "/usr/bin/something-else"},
	} {
		if liveness, err := Inspect(reused); err != nil || liveness != Unrelated {
			t.Fatalf("%s: reused pid liveness = %v, %v", name, liveness, err)
		}
		// A reused pid means our worker is gone, but its group id is now shared
		// with whatever holds that pid. Signalling would be a guess and confirming
		// is impossible, so the only honest answer is that this cannot be settled.
		// See docs/adr/0022-pending-cleanup-outlives-the-process.md.
		if err := TerminateOwned(reused); !errors.Is(err, process.ErrNotSettled) {
			t.Fatalf("%s: TerminateOwned = %v, want an unsettled report", name, err)
		}
	}
	// Refusing to signal an unrelated pid must not have stopped our own worker.
	if liveness, err := Inspect(identity); err != nil || liveness != Ours {
		t.Fatalf("worker after unrelated terminate = %v, %v", liveness, err)
	}

	// An incomplete record cannot be checked at all, and must not read as Gone.
	if liveness, _ := Inspect(ProcessIdentity{PID: identity.PID}); liveness != Unknown {
		t.Fatalf("incomplete record liveness = %v", liveness)
	}
	if err := TerminateOwned(ProcessIdentity{PID: identity.PID}); err == nil {
		t.Fatal("TerminateOwned accepted an unverifiable identity")
	}

	if err := TerminateOwned(identity); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Wait(waitContext(t, 10*time.Second)); err == nil {
		t.Fatal("Wait reported success for a terminated session")
	}
	if liveness, err := Inspect(identity); err != nil || liveness == Ours {
		t.Fatalf("terminated liveness = %v, %v", liveness, err)
	}
}

func TestHandoffKeepsRequirementsAndMarksWhatItTrimmed(t *testing.T) {
	handoff := Handoff{
		GoalID: "g", GoalTitle: "Ship it", WorkItemID: "WI-007", StoryRef: "specs/stories/seven.md",
		Action: "START", ActionReason: "earliest READY work", Candidate: "SNAPSHOT abc123",
		ResolvedDecisions: []ResolvedDecision{{GateID: "GATE-003", WorkItemID: "WI-006",
			Question: "Which store?", Choice: "postgres", Note: "keep operations simple"}},
		Dependencies:   []Dependency{{ID: "WI-006", Status: "VERIFIED", StoryRef: "specs/stories/six.md", Evidence: "EV-012"}},
		Attempts:       []Attempt{{Number: 1, Outcome: "implementation_finished", Summary: strings.Repeat("prior context ", 400)}},
		FailureExcerpt: strings.Repeat("FAIL line\n", 400), FailureLogPath: ".forgepilot/logs/WI-007.log",
	}

	full, err := handoff.Render(DefaultHandoffBytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"WI-007", "specs/stories/seven.md", "must not", "implementation_finished", "AGENTS.md", "GATE-003", "Which store?", "postgres", "keep operations simple"} {
		if !strings.Contains(full, required) {
			t.Fatalf("full handoff lost %q", required)
		}
	}
	if decision, attempts := strings.Index(full, "## Resolved Human Decisions"), strings.Index(full, "## Earlier attempts"); decision < 0 || attempts < 0 || decision > attempts {
		t.Fatalf("resolved decisions did not precede untrusted attempt summaries:\n%s", full)
	}

	bounded, err := handoff.Render(3000)
	if err != nil {
		t.Fatal(err)
	}
	if len(bounded) > 3000 {
		t.Fatalf("bounded handoff is %d bytes", len(bounded))
	}
	for _, required := range []string{"specs/stories/seven.md", "acceptance criteria are the requirements", "must not", "execution_failed", "GATE-003", "Which store?", "postgres", "keep operations simple"} {
		if !strings.Contains(bounded, required) {
			t.Fatalf("bounded handoff lost %q", required)
		}
	}
	if !strings.Contains(bounded, "truncated") {
		t.Fatal("bounded handoff trimmed context silently")
	}
	if strings.Contains(bounded, ".forgepilot/logs/WI-007.log") && strings.Count(bounded, "FAIL line") > 100 {
		t.Fatal("bounded handoff inlined the whole failure log")
	}
	if _, err := handoff.Render(100); err == nil {
		t.Fatal("a handoff that could not fit required decisions and contracts was truncated instead of refused")
	}
}

func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.sh")
	if err := os.WriteFile(path, []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func processAlive(t *testing.T, pid string) bool {
	t.Helper()
	if pid == "" {
		t.Fatal("no pid recorded")
	}
	_, _, err := inspectProcess(atoi(t, pid))
	return err == nil
}

func atoi(t *testing.T, value string) int {
	t.Helper()
	result := 0
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			t.Fatalf("not a pid: %q", value)
		}
		result = result*10 + int(digit-'0')
	}
	return result
}

// A coding CLI forks compilers, language servers and watchers. When the session
// itself exits, those are still ours and still writing the workspace — and the
// Runner's digest around the session has already been taken, so anything they
// do afterwards lands outside every check there is.
func TestACleanExitStillStopsTheWholeProcessGroup(t *testing.T) {
	script := `#!/bin/sh
workspace=""
result=""
while [ $# -gt 0 ]; do
  case "$1" in
    --workspace) workspace="$2"; shift 2;;
    --result) result="$2"; shift 2;;
    *) shift;;
  esac
done
( sleep 30; echo late > "$workspace/grandchild.txt" ) &
echo "$!" > "$workspace/grandchild.pid"
printf '{"outcome":"implementation_finished","summary":"done"}' > "$result"
`
	workspace := t.TempDir()
	request := Request{Workspace: workspace, ArtifactDir: t.TempDir(), Handoff: "x"}
	session, err := Start(Fake{Command: writeScript(t, script)}, request, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Wait(waitContext(t, 30*time.Second)); err != nil {
		t.Fatalf("Wait err = %v", err)
	}
	pid := strings.TrimSpace(readFile(t, filepath.Join(workspace, "grandchild.pid")))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(t, pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("grandchild %s outlived the session that spawned it", pid)
}

// The schema is handed to a runtime that applies it strictly: every key a
// object declares must also be listed as required, and anything genuinely
// optional says so by allowing null. A schema that breaks the rule is rejected
// by the API before the model ever sees the briefing, which arrives here as a
// session that exited 1 with no result — a protocol error whose cause is ours.
func TestResultSchemaSatisfiesStrictStructuredOutput(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal([]byte(ResultSchema), &schema); err != nil {
		t.Fatalf("the schema is not valid JSON: %v", err)
	}
	var walk func(path string, node map[string]any)
	walk = func(path string, node map[string]any) {
		properties, ok := node["properties"].(map[string]any)
		if !ok {
			return
		}
		required := map[string]bool{}
		for _, name := range node["required"].([]any) {
			required[name.(string)] = true
		}
		for name, child := range properties {
			if !required[name] {
				t.Errorf("%s.%s is declared but not required", path, name)
			}
			if nested, ok := child.(map[string]any); ok {
				walk(path+"."+name, nested)
			}
		}
	}
	walk("result", schema)
}

// The strict schema requires every key, so a runtime that has nothing to put in
// one sends null. Those are the ordinary shape of a result now, not a
// malformed one.
func TestDecodeResultAcceptsTheNullsTheSchemaRequires(t *testing.T) {
	result, err := DecodeResult([]byte(`{"outcome":"implementation_finished","summary":"done","unfinished":null,"needs_human":null,"error":null}`))
	if err != nil {
		t.Fatalf("a result with the nulls the schema requires was rejected: %v", err)
	}
	if result.Outcome != ImplementationFinished || result.Summary != "done" {
		t.Fatalf("result = %+v", result)
	}
	if result.Question != nil || result.Unfinished != nil || result.Error != "" {
		t.Fatalf("nulls did not decode as absent: %+v", result)
	}

	asked, err := DecodeResult([]byte(`{"outcome":"needs_human","summary":"which store?","unfinished":null,"error":null,` +
		`"needs_human":{"question":"which store?","options":["postgres","sqlite"],"context":null}}`))
	if err != nil {
		t.Fatalf("a needs_human result with a null context was rejected: %v", err)
	}
	if asked.Question == nil || len(asked.Question.Options) != 2 {
		t.Fatalf("question = %+v", asked.Question)
	}
}

// "Why did it stop" and "is anything still running" are two questions, and the
// second has an answer on the ordinary path too. A caller that could only ask
// the first would withdraw a worker record — the one thing recovery needs —
// because the call returned.
func TestAStoppedSessionReportsWhyAndWhetherItsGroupIsSettled(t *testing.T) {
	stopped := &StoppedError{Cause: context.DeadlineExceeded, Cleanup: process.ErrNotSettled}
	if !errors.Is(stopped, ErrStopped) {
		t.Fatal("a stopped session did not read as stopped")
	}
	if !errors.Is(stopped, context.DeadlineExceeded) {
		t.Fatal("the reason the caller's context ended was lost")
	}
	if !errors.Is(stopped, process.ErrNotSettled) {
		t.Fatal("an unconfirmed process group was not reportable")
	}
	settled := &StoppedError{Cause: context.Canceled}
	if errors.Is(settled, process.ErrNotSettled) {
		t.Fatal("a settled group read as unconfirmed")
	}
}

// A leader that has exited says nothing about the rest of its group. The
// children a coding CLI forked keep its pgid and go on writing the workspace,
// so "the pid is gone" is not a confirmation — and because the recorded
// identity no longer matches whatever holds that pid, the group under it is not
// ours to signal either. The only honest answer left is that this cannot be
// settled. See docs/adr/0022-pending-cleanup-outlives-the-process.md.
func TestALeaderThatExitedDoesNotMeanItsGroupIsEmpty(t *testing.T) {
	directory := t.TempDir()
	child := filepath.Join(directory, "child.pid")
	leader := filepath.Join(directory, "leader.pid")
	// The leader forks a child into its own group and then exits, which is what
	// `make verify` and a coding CLI both do routinely.
	script := "#!/bin/sh\n( sleep 300 ) &\necho \"$!\" > " + child + "\necho $$ > " + leader + "\nexit 0\n"
	request := Request{Workspace: t.TempDir(), ArtifactDir: t.TempDir(), Handoff: "x"}
	session, err := Start(Fake{Command: writeScript(t, script)}, request, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	identity := session.Identity
	// Reap the leader without touching its group, so this test asks about a
	// group whose leader is genuinely gone.
	<-session.done
	t.Cleanup(func() { _ = syscall.Kill(-identity.PGID, syscall.SIGKILL) })

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(child); statErr == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if liveness, _ := Inspect(identity); liveness == Ours {
		t.Skip("the leader was still reported as ours; this platform reaped differently")
	}
	if err := TerminateOwned(identity); !errors.Is(err, process.ErrNotSettled) {
		t.Fatalf("TerminateOwned = %v, want an unsettled report for a populated group", err)
	}
}
