package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FakeCommandVariable names the executable the fake runtime launches when
// `--runtime-command` is not given.
const FakeCommandVariable = "FORGEPILOT_FAKE_AGENT"

// Fake launches a local executable that stands in for a coding CLI. It exists
// so the default test suite can exercise real processes, real process groups,
// real locks and real crash recovery without a model, an account or a network.
//
// It is a runtime, not a mock: the contract it obeys — new process per session,
// handoff on stdin and as a file, structured result written to the named path —
// is the same contract Codex obeys, so a test that passes here is testing the
// production path rather than a substitute for it.
type Fake struct{ Command string }

func (fake Fake) Name() string { return "fake" }

func (fake Fake) Executable() (string, error) {
	command := fake.Command
	if command == "" {
		command = os.Getenv(FakeCommandVariable)
	}
	if command == "" {
		return "", fmt.Errorf("the fake runtime needs --runtime-command or %s to name an executable", FakeCommandVariable)
	}
	if strings.ContainsRune(command, os.PathSeparator) {
		absolute, err := filepath.Abs(command)
		if err != nil {
			return "", err
		}
		info, err := os.Stat(absolute)
		if err != nil {
			return "", fmt.Errorf("fake runtime is not available: %w", err)
		}
		if info.IsDir() || info.Mode()&0111 == 0 {
			return "", fmt.Errorf("fake runtime %s is not executable", absolute)
		}
		return absolute, nil
	}
	resolved, err := exec.LookPath(command)
	if err != nil {
		return "", fmt.Errorf("fake runtime is not available: %w", err)
	}
	return resolved, nil
}

func (fake Fake) Version() (string, error) {
	executable, err := fake.Executable()
	if err != nil {
		return "", err
	}
	output, err := exec.Command(executable, "--version").Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			// A stand-in that does not answer --version is still usable; naming it
			// by path is honest about what was actually run.
			return "fake(" + executable + ")", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func (fake Fake) Plan(request Request) (Plan, error) {
	executable, err := fake.Executable()
	if err != nil {
		return Plan{}, err
	}
	resultPath := filepath.Join(request.ArtifactDir, "result.json")
	handoffPath := filepath.Join(request.ArtifactDir, "handoff.md")
	return Plan{
		Executable:  executable,
		Args:        []string{"--workspace", request.Workspace, "--result", resultPath, "--handoff", handoffPath},
		ResultPath:  resultPath,
		HandoffPath: handoffPath,
	}, nil
}

// Resolve maps a `--runtime` name to a Runtime. Unknown names are refused
// rather than defaulted: silently running a different agent than the one asked
// for is the kind of surprise a long unattended run cannot afford.
func Resolve(name, command string) (Runtime, error) {
	switch name {
	case "codex":
		return Codex{Command: command}, nil
	case "fake":
		return Fake{Command: command}, nil
	case "":
		return nil, errors.New("--runtime is required")
	default:
		return nil, fmt.Errorf("unknown runtime %q; use codex or fake", name)
	}
}
