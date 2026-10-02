package forgepilot_test

import (
	"strings"
	"testing"
)

func TestRemovedCommandsAreUnknownAndAbsentFromHelp(t *testing.T) {
	root, binary := fixture(t)
	mustRun(t, binary, root, "init")
	for _, arguments := range [][]string{
		{"run", "--goal", "queue", "--runtime", "fake", "--snapshot"},
		{"execution", "plan", "--request", "x", "--json"},
		{"goal", "preflight", "--request", "x", "--json"},
		{"goal", "create", "--id", "queue", "--title", "Queue"},
		{"work", "add", "--goal", "queue", "--story", "specs/stories/a.md"},
		{"work", "list", "--goal", "queue", "--json"},
		{"migrate"},
	} {
		output, err := command(binary, root, arguments...)
		if err == nil || !strings.Contains(output, "unknown") {
			t.Errorf("%v = %v, want an unknown-command failure\n%s", arguments, err, output)
		}
	}
	help, err := command(binary, root, "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(help, "\n") {
		for _, removed := range []string{"  run ", "execution", "preflight", "goal create", "work add", "work list", "migrate", "external-ref"} {
			if strings.Contains(line, removed) {
				t.Errorf("help still mentions %q: %s", removed, line)
			}
		}
	}
	if !strings.Contains(help, "goal import") {
		t.Errorf("help does not mention goal import:\n%s", help)
	}
}
