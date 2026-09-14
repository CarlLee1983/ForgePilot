package agent

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// macOS `ps` does not fail when it cannot read a process's arguments: it prints
// the accounting name in parentheses instead, and exits zero. That line parses
// as a perfectly ordinary command, which is what makes it dangerous — a worker
// observed during that window is recorded as running "(sh)", and every later
// check then finds a command that does not match and calls the live worker
// somebody else's. See docs/adr/0020-worker-ownership-is-fail-closed.md.
func TestParseProcessLineRefusesArgumentsPsCouldNotRead(t *testing.T) {
	for name, line := range map[string]string{
		"short name":    "Mon Sep 14 14:41:09 2026 (sh)",
		"long name":     "Mon Sep 14 14:41:09 2026 (forgepilot)",
		"padded date":   "Mon Sep  1 14:41:09 2026 (sh)",
		"name with gap": "Mon Sep 14 14:41:09 2026 (Google Chrome He)",
		"no command":    "Mon Sep 14 14:41:09 2026",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parseProcessLine(line); !errors.Is(err, errArgumentsUnavailable) {
				t.Fatalf("parseProcessLine(%q) err = %v, want errArgumentsUnavailable", line, err)
			}
		})
	}
}

func TestParseProcessLineReadsStartAndCommand(t *testing.T) {
	start, command, err := parseProcessLine("Mon Sep  1 14:41:09 2026 /bin/sh /tmp/script.sh --workspace /tmp/w")
	if err != nil {
		t.Fatal(err)
	}
	if start != "Mon Sep 1 14:41:09 2026" {
		t.Fatalf("start = %q", start)
	}
	if command != "/bin/sh /tmp/script.sh --workspace /tmp/w" {
		t.Fatalf("command = %q", command)
	}
	if _, _, err := parseProcessLine("Mon Sep 1 2026"); err == nil || errors.Is(err, errArgumentsUnavailable) {
		t.Fatalf("parseProcessLine of a truncated line = %v, want an unreadable-output error", err)
	}
}

// The window ps renders the accounting name in is the kernel's, around exec and
// around exit, and it is short — so an unreadable answer is re-read rather than
// reported. It is re-read a bounded number of times: a process that stays
// unreadable must end as an answer, not as a wait.
func TestInspectWithRereadsArgumentsThatWereNotAvailableYet(t *testing.T) {
	const line = "Mon Sep 14 14:41:09 2026 /bin/sh /tmp/script.sh"

	calls := 0
	unreadableOnce := func(int) (string, string, error) {
		calls++
		if calls == 1 {
			return "", "", errArgumentsUnavailable
		}
		return parseProcessLine(line)
	}
	start, command, err := inspectWith(unreadableOnce, 1)
	if err != nil || start == "" || command != "/bin/sh /tmp/script.sh" {
		t.Fatalf("inspectWith = %q, %q, %v", start, command, err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want the read to have been repeated exactly once", calls)
	}

	calls = 0
	alwaysUnreadable := func(int) (string, string, error) {
		calls++
		return "", "", errArgumentsUnavailable
	}
	if _, _, err := inspectWith(alwaysUnreadable, 1); !errors.Is(err, errArgumentsUnavailable) {
		t.Fatalf("inspectWith err = %v, want errArgumentsUnavailable", err)
	}
	if calls != psAttempts {
		t.Fatalf("calls = %d, want %d", calls, psAttempts)
	}

	// Every other answer is the answer. A pid that is not there must not be
	// waited on, and a broken ps must not be retried into a timeout.
	for name, stop := range map[string]error{"gone": errNoSuchProcess, "broken ps": errors.New("ps: command not found")} {
		t.Run(name, func(t *testing.T) {
			calls = 0
			_, _, err := inspectWith(func(int) (string, string, error) { calls++; return "", "", stop }, 1)
			if !errors.Is(err, stop) || calls != 1 {
				t.Fatalf("inspectWith = %v after %d calls, want %v after 1", err, calls, stop)
			}
		})
	}
}

// A record written before the parenthesised form was recognised is still on
// disk. It can never match what ps reports now, so it must not be compared at
// all — but the pid being gone is a fact about the machine rather than about
// the record, and a run left behind by an older version still has to be able to
// clean itself up.
func TestInspectRefusesAnIdentityRecordedFromUnreadableArguments(t *testing.T) {
	live := ProcessIdentity{
		PID: os.Getpid(), PGID: os.Getpid(), Executable: "agent.test",
		ObservedStart: "Mon Sep 14 14:41:09 2026", ObservedCommand: "(sh)",
	}
	liveness, err := Inspect(live)
	if liveness != Unknown || err == nil {
		t.Fatalf("Inspect of a live pid = %v, %v, want Unknown and an error", liveness, err)
	}
	if !strings.Contains(err.Error(), "(sh)") {
		t.Fatalf("error does not name the unreadable command: %v", err)
	}

	gone := live
	gone.PID, gone.PGID = unusedPID(t), unusedPID(t)
	if liveness, err := Inspect(gone); liveness != Gone || err != nil {
		t.Fatalf("Inspect of a dead pid = %v, %v, want Gone", liveness, err)
	}
}

// unusedPID returns a pid that no process holds, so a test can ask about one
// that is definitely gone.
func unusedPID(t *testing.T) int {
	t.Helper()
	for pid := 1 << 20; pid < 1<<20+4096; pid++ {
		if _, _, err := readProcess(pid); errors.Is(err, errNoSuchProcess) {
			return pid
		}
	}
	t.Fatal("no unused pid found")
	return 0
}
