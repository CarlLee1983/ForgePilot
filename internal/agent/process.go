package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/process"
)

// Liveness is what can be established about a recorded worker process. It is
// deliberately four-valued: "cannot tell" is a distinct answer from "gone", and
// conflating them is how a recovery starts a second writer.
type Liveness string

const (
	// Gone means no process holds that pid any more.
	Gone Liveness = "GONE"
	// Ours means the pid is live and its identity matches what was recorded.
	Ours Liveness = "OURS"
	// Unrelated means the pid is live but belongs to something else, so our
	// worker has exited and that process must not be signalled.
	Unrelated Liveness = "UNRELATED"
	// Unknown means the question could not be answered. Callers fail closed.
	Unknown Liveness = "UNKNOWN"
)

// ProcessIdentity is everything needed to recognise a worker process later. A
// pid alone cannot do it — pids are reused — so the start time and executable
// observed at launch are recorded beside it. See
// docs/adr/0020-worker-ownership-is-fail-closed.md.
type ProcessIdentity struct {
	PID  int `json:"pid"`
	PGID int `json:"pgid"`
	// Executable is what ForgePilot launched, for reporting.
	Executable string `json:"executable"`
	// ObservedStart and ObservedCommand are read back from the operating system
	// immediately after launch rather than constructed here. Comparing against
	// what the OS itself reported is what makes the later check meaningful: a
	// value derived from our own argv would match a reused pid running an
	// unrelated program whenever the two happened to look alike.
	ObservedStart   string    `json:"observed_start"`
	ObservedCommand string    `json:"observed_command"`
	RecordedAt      time.Time `json:"recorded_at"`
}

// Recorded reports whether an identity carries enough to be checked at all.
func (identity ProcessIdentity) Recorded() bool {
	return identity.PID > 0 && identity.PGID > 0 && identity.ObservedStart != "" && identity.ObservedCommand != ""
}

// Inspect asks the operating system what is at the recorded pid now. An
// unreadable or unparseable answer is Unknown, never Gone: the whole point is
// that the caller refuses to act when it cannot tell.
func Inspect(identity ProcessIdentity) (Liveness, error) {
	if !identity.Recorded() {
		return Unknown, fmt.Errorf("incomplete process record (pid %d, pgid %d)", identity.PID, identity.PGID)
	}
	start, command, err := inspectProcess(identity.PID)
	if errors.Is(err, errNoSuchProcess) {
		return Gone, nil
	}
	if err != nil {
		return Unknown, err
	}
	if start == identity.ObservedStart && command == identity.ObservedCommand {
		return Ours, nil
	}
	return Unrelated, nil
}

var errNoSuchProcess = errors.New("no such process")

// inspectProcess reads one process's start time and executable. `ps` is used
// rather than a bare kill(pid, 0) because a live pid alone proves nothing about
// whose process it is.
func inspectProcess(pid int) (string, string, error) {
	command := exec.Command("ps", "-o", "lstart=,args=", "-p", fmt.Sprint(pid))
	output, err := command.Output()
	text := strings.TrimSpace(string(output))
	if err != nil {
		var exit *exec.ExitError
		// ps exits non-zero with no output when the pid simply is not there.
		if errors.As(err, &exit) && text == "" {
			return "", "", errNoSuchProcess
		}
		return "", "", fmt.Errorf("inspect process %d: %w", pid, err)
	}
	if text == "" {
		return "", "", errNoSuchProcess
	}
	// lstart is a fixed-width 5-field time; everything after it is the command.
	fields := strings.Fields(text)
	if len(fields) < 6 {
		return "", "", fmt.Errorf("inspect process %d: unreadable ps output %q", pid, text)
	}
	return strings.Join(fields[:5], " "), strings.Join(fields[5:], " "), nil
}

// ObserveIdentity records a just-started process so it can be recognised after
// a crash. It is called immediately after Start, which is the narrowest the
// window between launching and being able to recover can be made.
func ObserveIdentity(pid int, executable string, now time.Time) ProcessIdentity {
	identity := ProcessIdentity{PID: pid, PGID: pid, Executable: executable, RecordedAt: now.UTC()}
	if start, command, err := inspectProcess(pid); err == nil {
		identity.ObservedStart, identity.ObservedCommand = start, command
	}
	return identity
}

// TerminateOwned stops a recorded worker's whole process group. It refuses
// unless the identity was confirmed as ours: signalling a pid that has been
// reused would kill an unrelated program, which is worse than not recovering.
// The stop is confirmed rather than assumed: a group that survived both signals
// is reported, because "we sent SIGKILL" is not the same statement as "nothing
// is writing this workspace".
func TerminateOwned(identity ProcessIdentity) error {
	liveness, err := Inspect(identity)
	if err != nil {
		return err
	}
	switch liveness {
	case Gone, Unrelated:
		// A leader that has exited says nothing about the rest of its group: the
		// children it forked keep the pgid it had and go on writing this
		// workspace. An empty group is the only confirmation available, and when
		// the group is not empty this pid is exactly the case where signalling
		// would be a guess — the recorded identity no longer matches what holds
		// it, so the group under that id may not be ours to kill.
		// See docs/adr/0020-worker-ownership-is-fail-closed.md.
		if process.Gone(identity.PGID) {
			return nil
		}
		return &process.NotSettled{PGID: identity.PGID, Reason: fmt.Sprintf(
			"process group %d still has members although pid %d is no longer the worker; it cannot be signalled without guessing whose it is",
			identity.PGID, identity.PID)}
	case Unknown:
		return fmt.Errorf("cannot confirm whether pid %d is still the worker", identity.PID)
	}
	return process.Stop(identity.PGID, process.NewBudget())
}

// bounded stops writing once limit bytes have been accepted, leaving one note
// saying so. It never returns an error: a full log must not kill a session that
// is otherwise doing its job.
type bounded struct {
	writer    io.Writer
	remaining int64
	noted     bool
}

func boundedWriter(writer io.Writer, limit int64) io.Writer {
	if limit <= 0 {
		return writer
	}
	return &bounded{writer: writer, remaining: limit}
}

func (writer *bounded) Write(contents []byte) (int, error) {
	if writer.remaining <= 0 {
		if !writer.noted {
			writer.noted = true
			_, _ = io.WriteString(writer.writer, "\n[forgepilot: session output truncated at its configured limit]\n")
		}
		return len(contents), nil
	}
	accepted := contents
	if int64(len(accepted)) > writer.remaining {
		accepted = accepted[:writer.remaining]
	}
	written, err := writer.writer.Write(accepted)
	writer.remaining -= int64(written)
	if err != nil {
		return written, err
	}
	return len(contents), nil
}

// Session is one running agent process.
type Session struct {
	Identity   ProcessIdentity
	Plan       Plan
	OutputPath string

	command *exec.Cmd
	output  *os.File
	drain   func(*process.Budget)
	done    chan error
}

// Started reports the process identity a caller must persist before doing
// anything else, so a crash straight afterwards still leaves a recoverable
// record of what was launched.
func (session *Session) Started() ProcessIdentity { return session.Identity }

// Discard releases a session a caller decided not to wait for. Wait owns the
// ordinary path; this exists for the one caller that stops a session it has
// just launched — because it could not record the identity — and then returns
// without ever calling Wait. Without it the output pipe's copy goroutine and
// the log file are never released.
//
// It does not stop anything: the caller has already decided what to do about
// the process. It only lets go of what this handle holds.
func (session *Session) Discard() {
	session.drain(process.NewBudget())
	session.output.Close()
}

// Start launches one new session. The child is placed in its own process group
// so the whole tree can be stopped later, the handoff is delivered on stdin and
// as a file, and all console output is streamed to a file rather than held in
// memory: a session that is killed still leaves what it had produced.
func Start(runtime Runtime, request Request, now time.Time) (*Session, error) {
	plan, err := runtime.Plan(request)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(request.ArtifactDir, 0755); err != nil {
		return nil, fmt.Errorf("create session directory: %w", err)
	}
	if plan.HandoffPath != "" {
		if err := os.WriteFile(plan.HandoffPath, []byte(request.Handoff), 0600); err != nil {
			return nil, fmt.Errorf("write handoff: %w", err)
		}
	}
	outputPath := filepath.Join(request.ArtifactDir, "session.log")
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return nil, fmt.Errorf("create session log: %w", err)
	}
	// A runaway session must not be able to fill the disk through its console
	// output. The cap is announced in the log itself, so nobody reading it later
	// mistakes a truncated log for a short one.
	console := boundedWriter(output, request.MaxOutputBytes)
	command := exec.Command(plan.Executable, plan.Args...)
	command.Dir = request.Workspace
	command.Stdin = strings.NewReader(request.Handoff)
	if len(plan.Environment) > 0 {
		command.Env = append(os.Environ(), plan.Environment...)
	}
	// The bound is applied on this side of a descriptor we own. Handing os/exec
	// an io.Writer would make it create the pipe and the copy goroutine, and
	// Wait would then not return until every descriptor the session passed on
	// had been closed — a background grandchild holding it open is how a Ctrl-C
	// comes to hang forever. See internal/process.
	drain, err := process.Attach(command, console)
	if err != nil {
		output.Close()
		return nil, err
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		drain(nil)
		output.Close()
		return nil, fmt.Errorf("start %s: %w", plan.Executable, err)
	}
	session := &Session{
		Identity:   ObserveIdentity(command.Process.Pid, plan.Executable, now),
		Plan:       plan,
		OutputPath: outputPath,
		command:    command,
		output:     output,
		drain:      drain,
		done:       make(chan error, 1),
	}
	go func() { session.done <- command.Wait() }()
	return session, nil
}

// Wait blocks until the session ends or ctx does. The caller owns every limit
// that can end it early — its own timeout, the run's total deadline, a signal —
// so this reports only that the session was stopped and hands back the context's
// cause; deciding which limit it was belongs to whoever set them, not here.
//
// The whole process group is settled before returning on every path, including
// a clean exit: whatever the session forked is still ours, still writing this
// workspace, and — for the Runner — now past the digest taken around the
// session.
func (session *Session) Wait(ctx context.Context) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// The same sequence process.Start uses, from the same place: one budget for
	// the whole stop, the leader reaped before the group is confirmed, and the
	// leader's own verdict kept rather than discarded — which is what the `_ =`
	// that used to be here threw away, turning "no wait result ever arrived"
	// into silence.
	waitErr, completed, cleanup := process.Settle(ctx, session.Identity.PGID, session.done, session.drain)
	session.output.Close()
	if !completed {
		return Result{}, &StoppedError{Cause: context.Cause(ctx), Cleanup: cleanup}
	}
	result, err := session.collect(waitErr)
	if cleanup != nil {
		// Both survive: why the session ended and whether anything it started is
		// still running are two questions, and the caller acts on both.
		return result, errors.Join(err, cleanup)
	}
	return result, err
}

// collect reads the structured result. The exit code is diagnostic only: a
// clean exit with no usable result is a protocol error, never a completion.
func (session *Session) collect(waitErr error) (Result, error) {
	result, err := ParseResult(session.Plan.ResultPath)
	if err == nil {
		return result, nil
	}
	if waitErr != nil {
		var exit *exec.ExitError
		if errors.As(waitErr, &exit) {
			return Result{}, &ProtocolError{Detail: fmt.Sprintf("runtime exited with code %d and left no usable result: %v", exit.ExitCode(), err)}
		}
		return Result{}, fmt.Errorf("run %s: %w", session.Plan.Executable, waitErr)
	}
	return Result{}, err
}
