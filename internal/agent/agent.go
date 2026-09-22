// Package agent starts a local coding CLI for one Work Item and reads back a
// structured result. It is the Agent runtime boundary and nothing else: the
// Verification toolchain a Candidate checkout requires is resolved separately
// by internal/repository, and the two must not be merged.
//
// The runtime this package launches may itself contact a model service. That is
// the one exception to ForgePilot's offline boundary, and it exists only while
// a person is running `forgepilot run`. See
// docs/adr/0018-runner-may-launch-a-local-coding-cli.md.
package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Outcome is the closed set of things a session may report. There is no
// "succeeded": implementation_finished says an implementation attempt ended,
// and whether the work passes is decided afterwards by the canonical check on
// the Candidate — never by the agent and never by its exit code.
type Outcome string

const (
	ImplementationFinished Outcome = "implementation_finished"
	NeedsHuman             Outcome = "needs_human"
	ExecutionFailed        Outcome = "execution_failed"
)

// Question is what a session must supply when it stops for a person. Options
// are carried through as the agent stated them; nothing here invents choices,
// answers on a person's behalf, or resolves anything.
type Question struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
	Context  string   `json:"context,omitempty"`
	// ExternalFact names the one offline-unverifiable fact a person may later
	// self-declare. It is deliberately distinct from options: a fact wait is
	// not a Gate and ForgePilot must not fabricate one for it.
	ExternalFact string `json:"external_fact,omitempty"`
}

// Result is the structured hand-back from one session. It is untrusted content:
// a summary to show a person and to quote into the next handoff, never an
// authorization and never Evidence.
type Result struct {
	Outcome    Outcome   `json:"outcome"`
	Summary    string    `json:"summary"`
	Unfinished []string  `json:"unfinished,omitempty"`
	Question   *Question `json:"needs_human,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// ProtocolError reports a session that ended without a usable result — missing
// file, malformed JSON, unknown outcome, missing required field. Exit code zero
// does not rescue it: a runtime that claims success while saying nothing has
// told us nothing, and treating that as completion is exactly the false success
// this type exists to prevent.
type ProtocolError struct{ Detail string }

func (err *ProtocolError) Error() string {
	return "agent runtime protocol error: " + err.Detail
}

// IsProtocolError reports whether a session failed to speak the result contract.
func IsProtocolError(err error) bool {
	var protocol *ProtocolError
	return errors.As(err, &protocol)
}

// ErrStopped reports a session stopped because the caller's context ended,
// which is what a SIGINT, a session timeout or an expired run deadline all look
// like from here. It is deliberately distinct from a runtime failure: the
// session was working, and the person resuming later needs to know it was
// interrupted rather than broken. Which limit ended it is the caller's to say —
// only the caller set them — and StoppedError carries the cause back so it can.
var ErrStopped = errors.New("agent session stopped on request")

// StoppedError is a stopped session together with what the caller's context
// gave as the reason, and whether the session's process group could be
// confirmed empty afterwards.
type StoppedError struct {
	Cause   error
	Cleanup error
}

func (err *StoppedError) Error() string {
	message := ErrStopped.Error()
	if err.Cause != nil {
		message += ": " + err.Cause.Error()
	}
	if err.Cleanup != nil {
		message += "; " + err.Cleanup.Error()
	}
	return message
}

func (err *StoppedError) Is(target error) bool { return target == ErrStopped }

// Both are exposed to errors.Is: a caller asks "was it stopped" and "could its
// process group be confirmed empty" separately, and the second question has an
// answer on the completed path too.
func (err *StoppedError) Unwrap() []error { return []error{err.Cause, err.Cleanup} }

// MaxResultBytes caps the structured result a session may hand back. The
// summary is meant to be read by a person and quoted into the next handoff;
// anything larger is a log, and logs live in the session's own artifacts.
const MaxResultBytes = 64 * 1024

// ParseResult decodes and validates one session's structured result. It is
// strict on purpose: unknown fields are rejected the same way state decoding
// rejects them, because a result the next version silently reinterprets is
// worse than one that is refused now.
func ParseResult(path string) (Result, error) {
	file, err := os.Open(path)
	if err != nil {
		return Result{}, &ProtocolError{Detail: fmt.Sprintf("no result at %s: %v", path, err)}
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, MaxResultBytes+1))
	if err != nil {
		return Result{}, &ProtocolError{Detail: fmt.Sprintf("read result %s: %v", path, err)}
	}
	if len(contents) > MaxResultBytes {
		return Result{}, &ProtocolError{Detail: fmt.Sprintf("result exceeds %d bytes", MaxResultBytes)}
	}
	return DecodeResult(contents)
}

// DecodeResult validates an in-memory result payload.
func DecodeResult(contents []byte) (Result, error) {
	trimmed := bytes.TrimSpace(contents)
	if len(trimmed) == 0 {
		return Result{}, &ProtocolError{Detail: "result is empty"}
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	var result Result
	if err := decoder.Decode(&result); err != nil {
		return Result{}, &ProtocolError{Detail: fmt.Sprintf("decode result: %v", err)}
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Result{}, &ProtocolError{Detail: "result contains extra JSON values"}
	}
	return result, result.validate()
}

func (result Result) validate() error {
	switch result.Outcome {
	case ImplementationFinished, NeedsHuman, ExecutionFailed:
	case "":
		return &ProtocolError{Detail: "result has no outcome"}
	default:
		return &ProtocolError{Detail: fmt.Sprintf("unknown outcome %q", result.Outcome)}
	}
	if strings.TrimSpace(result.Summary) == "" {
		return &ProtocolError{Detail: fmt.Sprintf("%s result has no summary", result.Outcome)}
	}
	switch result.Outcome {
	case NeedsHuman:
		if result.Question == nil || strings.TrimSpace(result.Question.Question) == "" {
			return &ProtocolError{Detail: "needs_human result states no question"}
		}
		if result.Question.ExternalFact != "" {
			if strings.TrimSpace(result.Question.ExternalFact) == "" || strings.ContainsAny(result.Question.ExternalFact, "\r\n") {
				return &ProtocolError{Detail: "needs_human external fact is invalid"}
			}
			if len(result.Question.Options) != 0 {
				return &ProtocolError{Detail: "needs_human external fact cannot carry Gate options"}
			}
		}
	case ExecutionFailed:
		if strings.TrimSpace(result.Error) == "" {
			return &ProtocolError{Detail: "execution_failed result states no error"}
		}
	case ImplementationFinished:
		if result.Question != nil {
			return &ProtocolError{Detail: "implementation_finished result carries a human question"}
		}
	}
	return nil
}

// Request is one session's inputs. Every field is explicit: nothing about the
// session is inherited from the caller's shell beyond the runtime's own
// configuration, and no credential ever passes through here.
type Request struct {
	Workspace   string
	ArtifactDir string
	Handoff     string
	// Model and Effort come from the exact Worker Profile returned by the final
	// authorization transaction before session launch. Runtimes must never
	// substitute their own defaults for these selections.
	Model   string
	Effort  string
	Timeout time.Duration
	// MaxOutputBytes bounds the session's console log. Zero means unbounded,
	// which the Runner never uses.
	MaxOutputBytes int64
}

// Plan is how a Runtime says to start one new session. Executable and Args stay
// separate — a shell string would let a Story's text become a command — and the
// handoff reaches the process through stdin and a file, never through argv.
type Plan struct {
	Executable string
	Args       []string
	// ResultPath is where the session is told to write its structured result.
	ResultPath string
	// HandoffPath is the file copy of the handoff, also passed on stdin.
	HandoffPath string
	// Environment lists extra `KEY=value` entries for the child. It never
	// carries credentials: the runtime owns its own authentication.
	Environment []string
}

// Runtime is one coding CLI ForgePilot knows how to launch. It describes how to
// start a session; it does not interpret engineering outcomes, and nothing it
// returns is ever treated as Evidence.
type Runtime interface {
	// Name is the identifier accepted by `--runtime`.
	Name() string
	// Executable is the resolved path the runtime will launch.
	Executable() (string, error)
	// Version reports the runtime's own version, for the run record.
	Version() (string, error)
	// SessionEnvironment describes the sandbox setting ForgePilot adds when it
	// launches this runtime.
	SessionEnvironment() SessionEnvironment
	// Plan describes one new session. Every call is a new session: no runtime
	// may continue an earlier conversation.
	Plan(request Request) (Plan, error)
}
