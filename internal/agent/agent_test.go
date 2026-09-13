package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
			if _, err := session.Wait(30*time.Second, nil); !IsProtocolError(err) {
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
	result, err := session.Wait(30*time.Second, nil)
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
	if _, err := session.Wait(2*time.Second, nil); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("Wait err = %v, want a timeout", err)
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

func TestInspectDistinguishesGoneFromOursFromUnrelated(t *testing.T) {
	script := "#!/bin/sh\nsleep 30\n"
	request := Request{Workspace: t.TempDir(), ArtifactDir: t.TempDir(), Handoff: "x"}
	session, err := Start(Fake{Command: writeScript(t, script)}, request, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	identity := session.Identity
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
		if err := TerminateOwned(reused); err != nil {
			t.Fatalf("%s: TerminateOwned = %v", name, err)
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
	if _, err := session.Wait(10*time.Second, nil); err == nil {
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
		Dependencies:   []Dependency{{ID: "WI-006", Status: "VERIFIED", StoryRef: "specs/stories/six.md", Evidence: "EV-012"}},
		Attempts:       []Attempt{{Number: 1, Outcome: "implementation_finished", Summary: strings.Repeat("prior context ", 400)}},
		FailureExcerpt: strings.Repeat("FAIL line\n", 400), FailureLogPath: ".forgepilot/logs/WI-007.log",
	}

	full := handoff.Render(DefaultHandoffBytes)
	for _, required := range []string{"WI-007", "specs/stories/seven.md", "must not", "implementation_finished", "AGENTS.md"} {
		if !strings.Contains(full, required) {
			t.Fatalf("full handoff lost %q", required)
		}
	}

	bounded := handoff.Render(3000)
	if len(bounded) > 3000 {
		t.Fatalf("bounded handoff is %d bytes", len(bounded))
	}
	for _, required := range []string{"specs/stories/seven.md", "acceptance criteria are the requirements", "must not", "execution_failed"} {
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
