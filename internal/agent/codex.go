package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Codex launches the Codex CLI's non-interactive `exec` mode. Command is the
// executable to run; empty means `codex` as found on PATH.
type Codex struct{ Command string }

func (codex Codex) Name() string { return "codex" }

func (codex Codex) SessionEnvironment() SessionEnvironment {
	return SessionEnvironment{Sandbox: SandboxWorkspaceWrite}
}

// Executable resolves the CLI without installing or configuring anything. A
// missing Codex is a stop condition, not something to fix automatically.
func (codex Codex) Executable() (string, error) {
	command := codex.Command
	if command == "" {
		command = "codex"
	}
	resolved, err := exec.LookPath(command)
	if err != nil {
		return "", fmt.Errorf("codex runtime is not available: %w", err)
	}
	return resolved, nil
}

func (codex Codex) Version() (string, error) {
	executable, err := codex.Executable()
	if err != nil {
		return "", err
	}
	output, err := exec.Command(executable, "--version").Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", fmt.Errorf("codex --version failed: %s", strings.TrimSpace(string(exit.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// Plan builds one new Codex session.
//
// Four choices are deliberate. The prompt arrives on stdin rather than in argv,
// so a Story's text can never become part of a command line. `--cd` names the
// workspace explicitly instead of relying on the inherited directory.
// `--sandbox workspace-write` grants exactly the access implementing a Work Item
// needs; the bypass flags are never used, and a session that lacks permission
// stops rather than escalating. `--output-schema` plus `--output-last-message`
// make the structured result a file ForgePilot reads, not prose it parses.
//
// There is no `resume`: every attempt is a new session, so a failed attempt's
// conversation cannot quietly become the context for the next Work Item. See
// docs/specs/runner-mvp/spec.md.
func (codex Codex) Plan(request Request) (Plan, error) {
	if err := validateCodexProfile(request.Model, request.Effort); err != nil {
		return Plan{}, err
	}
	executable, err := codex.Executable()
	if err != nil {
		return Plan{}, err
	}
	schemaPath := filepath.Join(request.ArtifactDir, "result-schema.json")
	if err := os.MkdirAll(request.ArtifactDir, 0755); err != nil {
		return Plan{}, fmt.Errorf("create session directory: %w", err)
	}
	if err := os.WriteFile(schemaPath, []byte(ResultSchema), 0600); err != nil {
		return Plan{}, fmt.Errorf("write result schema: %w", err)
	}
	resultPath := filepath.Join(request.ArtifactDir, "result.json")
	return Plan{
		Executable: executable,
		Args: []string{
			"exec",
			"--model", request.Model,
			// Codex has a dedicated model flag. Effort is a config key, whose
			// value is TOML rather than JSON or a shell fragment. Worker Profile
			// validation currently permits only medium, so this is a fixed TOML
			// literal and never interpolates untrusted text.
			"--config", `model_reasoning_effort="medium"`,
			"--cd", request.Workspace,
			"--sandbox", string(codex.SessionEnvironment().Sandbox),
			"--skip-git-repo-check",
			"--output-schema", schemaPath,
			"--output-last-message", resultPath,
			"--color", "never",
			"-",
		},
		ResultPath:  resultPath,
		HandoffPath: filepath.Join(request.ArtifactDir, "handoff.md"),
	}, nil
}

func validateCodexProfile(model, effort string) error {
	if strings.TrimSpace(model) == "" || model != strings.TrimSpace(model) || strings.ContainsAny(model, "\x00\r\n") {
		return errors.New("Codex Worker Profile must provide one clean model")
	}
	if effort != "medium" {
		return errors.New("Codex Worker Profile must provide supported medium effort")
	}
	return nil
}

// ResultSchema is the JSON Schema a runtime is given for its final message. It
// mirrors Result exactly; ParseResult validates independently, because a schema
// the runtime was handed is a request, not a guarantee.
const ResultSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["outcome", "summary", "unfinished", "needs_human", "error"],
  "properties": {
    "outcome": {
      "type": "string",
      "enum": ["implementation_finished", "needs_human", "execution_failed"]
    },
    "summary": { "type": "string" },
    "unfinished": { "type": ["array", "null"], "items": { "type": "string" } },
    "needs_human": {
      "type": ["object", "null"],
      "additionalProperties": false,
      "required": ["question", "options", "context"],
      "properties": {
        "question": { "type": "string" },
        "options": { "type": ["array", "null"], "items": { "type": "string" } },
        "context": { "type": ["string", "null"] }
      }
    },
    "error": { "type": ["string", "null"] }
  }
}
`
