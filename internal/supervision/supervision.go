// Package supervision owns user-scoped macOS LaunchAgent installation and its
// durable execution-control record. Runner admission remains the authority for
// authorization, worker identity, workspace ownership, and Pending cleanup.
package supervision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const schemaVersion = 1

type Phase string

const (
	PhaseReady   Phase = "READY"
	PhaseRunning Phase = "RUNNING"
	PhasePaused  Phase = "PAUSED"
	PhaseBlocked Phase = "RECOVERY_BLOCKED"
)

type Event struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail,omitempty"`
}

// Job contains control and process-launch identity, not Goal or Work Item state.
// Credentials and caller environment are never serialized or passed to launchd.
type Job struct {
	SchemaVersion       int       `json:"schema_version"`
	ID                  string    `json:"id"`
	Workspace           string    `json:"workspace"`
	GoalID              string    `json:"goal_id"`
	Executable          string    `json:"executable"`
	AuthorizationDigest string    `json:"authorization_digest"`
	Deadline            time.Time `json:"deadline"`
	Phase               Phase     `json:"phase"`
	Events              []Event   `json:"events"`
}

type Spec struct {
	Workspace           string
	GoalID              string
	Executable          string
	AuthorizationDigest string
	Deadline            time.Time
}

type Status struct {
	Job       Job  `json:"job"`
	Installed bool `json:"installed"`
	Loaded    bool `json:"loaded"`
}

// Manager is scoped to the current macOS user. Home is explicit for isolated
// tests; production callers pass os.UserHomeDir() and use New.
type Manager struct {
	home      string
	launchctl string
}

func New(home string) (*Manager, error) {
	if runtime.GOOS != "darwin" {
		return nil, errors.New("macOS LaunchAgent supervision is only supported on macOS")
	}
	if !cleanAbsolute(home) {
		return nil, errors.New("home must be an absolute clean path")
	}
	return &Manager{home: home, launchctl: "/bin/launchctl"}, nil
}

func ID(workspace, goalID string) (string, error) {
	if !cleanAbsolute(workspace) || goalID == "" || strings.TrimSpace(goalID) != goalID || strings.ContainsAny(goalID, "/\\\x00\n\r") {
		return "", errors.New("workspace must be absolute and goal ID must be a safe nonempty name")
	}
	sum := sha256.Sum256([]byte(workspace + "\x00" + goalID))
	return hex.EncodeToString(sum[:16]), nil
}

func (m *Manager) JobPath(id string) (string, error) {
	if !validID(id) {
		return "", errors.New("invalid supervision job ID")
	}
	return filepath.Join(m.home, "Library", "Application Support", "ForgePilot", "supervision", id+".json"), nil
}

func (m *Manager) plistPath(id string) string {
	return filepath.Join(m.home, "Library", "LaunchAgents", "com.forgepilot.supervision."+id+".plist")
}

func label(id string) string { return "com.forgepilot.supervision." + id }

// Install writes the durable job and LaunchAgent before asking launchd to load
// it. A retry with identical identity is safe; a changed binding requires an
// explicit uninstall and new authorization decision by the caller.
func (m *Manager) Install(spec Spec, now time.Time) (Job, error) {
	if err := validateSpec(spec, now); err != nil {
		return Job{}, err
	}
	id, _ := ID(spec.Workspace, spec.GoalID)
	var job Job
	err := m.lock(id, func() error {
		path, _ := m.JobPath(id)
		existing, err := readJob(path)
		if err == nil {
			if existing.Workspace != spec.Workspace || existing.GoalID != spec.GoalID || existing.Executable != spec.Executable || existing.AuthorizationDigest != spec.AuthorizationDigest || !existing.Deadline.Equal(spec.Deadline) {
				return errors.New("supervised job is bound to a different authorization or engine; uninstall explicitly before replacing it")
			}
			job = existing
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		} else {
			job = Job{SchemaVersion: schemaVersion, ID: id, Workspace: spec.Workspace, GoalID: spec.GoalID, Executable: spec.Executable, AuthorizationDigest: spec.AuthorizationDigest, Deadline: spec.Deadline.UTC(), Phase: PhaseReady, Events: []Event{{At: now.UTC(), Kind: "installed"}}}
			if err := writeJSON(path, job); err != nil {
				return err
			}
		}
		plist := m.plistPath(id)
		if err := writeFile(plist, []byte(plistXML(job, path)), 0600); err != nil {
			return err
		}
		if loaded, err := m.launchOutput("print", fmt.Sprintf("gui/%d/%s", os.Getuid(), label(id))); err == nil {
			if !matchesLoadedJob(string(loaded), job, plist, path) {
				return errors.New("loaded LaunchAgent does not match the pinned supervision job")
			}
			return nil
		}
		return m.launch("bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), plist)
	})
	return job, err
}

func (m *Manager) Load(id string) (Job, error) {
	path, err := m.JobPath(id)
	if err != nil {
		return Job{}, err
	}
	job, err := readJob(path)
	if err == nil && job.ID != id {
		return Job{}, errors.New("supervision job identity does not match its path")
	}
	return job, err
}

// LoadPath accepts only the exact path generated for a job in this user's
// supervision directory. It is the safe entry point for the LaunchAgent's
// --job argument.
func (m *Manager) LoadPath(path string) (Job, error) {
	if !cleanAbsolute(path) || filepath.Dir(path) != filepath.Join(m.home, "Library", "Application Support", "ForgePilot", "supervision") {
		return Job{}, errors.New("job path is outside the user supervision directory")
	}
	id := strings.TrimSuffix(filepath.Base(path), ".json")
	expected, err := m.JobPath(id)
	if err != nil || expected != path {
		return Job{}, errors.New("invalid job path")
	}
	return m.Load(id)
}

func (m *Manager) Status(id string) (Status, error) {
	job, err := m.Load(id)
	if err != nil {
		return Status{}, err
	}
	_, err = os.Lstat(m.plistPath(id))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Status{}, err
	}
	installed := err == nil
	loaded := false
	if installed {
		loaded = m.launch("print", fmt.Sprintf("gui/%d/%s", os.Getuid(), label(id))) == nil
	}
	return Status{Job: job, Installed: installed, Loaded: loaded}, nil
}

// Uninstall first tells launchd to stop future launches. It retains the job
// record as audit evidence and marks it paused before removing the plist.
func (m *Manager) Uninstall(id string, now time.Time) error {
	if now.IsZero() {
		return errors.New("uninstall time is required")
	}
	return m.lock(id, func() error {
		job, err := m.Load(id)
		if err != nil {
			return err
		}
		job.Phase = PhasePaused
		job.Events = appendEvent(job.Events, Event{At: now.UTC(), Kind: "uninstall_requested"})
		path, _ := m.JobPath(id)
		if err := writeJSON(path, job); err != nil {
			return err
		}
		plist := m.plistPath(id)
		if info, err := os.Lstat(plist); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		} else if !info.Mode().IsRegular() {
			return errors.New("unsafe LaunchAgent plist path")
		}
		if err := m.launch("bootout", fmt.Sprintf("gui/%d", os.Getuid()), plist); err != nil {
			return err
		}
		return os.Remove(plist)
	})
}

// RecordEvent persists a lifecycle observation without granting permission to
// launch. A caller must still perform all Runner admission checks afterward.
func (m *Manager) RecordEvent(id, kind, detail string, now time.Time) (Job, error) {
	if now.IsZero() || !allowedEvent(kind) || len(detail) > 1024 {
		return Job{}, errors.New("invalid supervision event")
	}
	var job Job
	err := m.lock(id, func() error {
		var err error
		job, err = m.Load(id)
		if err != nil {
			return err
		}
		job.Events = appendEvent(job.Events, Event{At: now.UTC(), Kind: kind, Detail: detail})
		path, _ := m.JobPath(id)
		return writeJSON(path, job)
	})
	return job, err
}

// SetPhase records a Runner-observed outcome. It cannot authorize execution.
func (m *Manager) SetPhase(id string, phase Phase, detail string, now time.Time) (Job, error) {
	if now.IsZero() || len(detail) > 1024 || phase != PhaseReady && phase != PhaseRunning && phase != PhasePaused && phase != PhaseBlocked {
		return Job{}, errors.New("invalid supervision phase")
	}
	var job Job
	err := m.lock(id, func() error {
		var err error
		job, err = m.Load(id)
		if err != nil {
			return err
		}
		if job.Phase == PhasePaused && phase != PhasePaused {
			return errors.New("paused job requires explicit resume")
		}
		if job.Phase == PhaseBlocked && phase != PhaseBlocked {
			return errors.New("recovery-blocked job requires explicit repair")
		}
		job.Phase = phase
		job.Events = appendEvent(job.Events, Event{At: now.UTC(), Kind: "phase_changed", Detail: detail})
		path, _ := m.JobPath(id)
		return writeJSON(path, job)
	})
	return job, err
}

// Resume only updates the supervisor's observation after the caller has
// explicitly authorized resume through the runner/app control boundary. It
// neither clears runner pause intent nor launches a process.
func (m *Manager) Resume(id string, now time.Time) (Job, error) {
	if now.IsZero() {
		return Job{}, errors.New("resume time is required")
	}
	var job Job
	err := m.lock(id, func() error {
		var err error
		job, err = m.Load(id)
		if err != nil {
			return err
		}
		if job.Phase != PhasePaused && job.Phase != PhaseBlocked {
			return errors.New("supervised job is not paused or recovery-blocked")
		}
		job.Phase = PhaseReady
		job.Events = appendEvent(job.Events, Event{At: now.UTC(), Kind: "resumed"})
		path, _ := m.JobPath(id)
		return writeJSON(path, job)
	})
	return job, err
}

func (m *Manager) launch(args ...string) error {
	_, err := m.launchOutput(args...)
	return err
}

func (m *Manager) launchOutput(args ...string) ([]byte, error) {
	out, err := exec.Command(m.launchctl, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("launchctl %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// launchctl print is a diagnostic format rather than a stable API. Unknown
// renderings fail closed so an existing label cannot silently retain stale
// executable arguments after an idempotent install.
func matchesLoadedJob(output string, job Job, plist, jobPath string) bool {
	lines := strings.Split(output, "\n")
	fields := map[string]string{}
	var arguments []string
	readingArguments := false
	for _, line := range lines {
		if readingArguments {
			if line == "\t}" {
				readingArguments = false
				continue
			}
			if !strings.HasPrefix(line, "\t\t") {
				return false
			}
			arguments = append(arguments, strings.TrimPrefix(line, "\t\t"))
			continue
		}
		if line == "\targuments = {" {
			readingArguments = true
			continue
		}
		for _, key := range []string{"path", "program", "working directory"} {
			prefix := "\t" + key + " = "
			if strings.HasPrefix(line, prefix) {
				if fields[key] != "" {
					return false
				}
				fields[key] = strings.TrimPrefix(line, prefix)
			}
		}
	}
	want := []string{job.Executable, "execution", "supervise", "run", "--job", jobPath}
	if readingArguments || len(arguments) != len(want) || fields["path"] != plist || fields["program"] != job.Executable || fields["working directory"] != job.Workspace {
		return false
	}
	for index, argument := range want {
		if arguments[index] != argument {
			return false
		}
	}
	return true
}

func validateSpec(s Spec, now time.Time) error {
	if _, err := ID(s.Workspace, s.GoalID); err != nil {
		return err
	}
	if !cleanAbsolute(s.Executable) || s.AuthorizationDigest == "" || strings.TrimSpace(s.AuthorizationDigest) != s.AuthorizationDigest || strings.ContainsAny(s.AuthorizationDigest, "\x00\n\r") || now.IsZero() || !now.Before(s.Deadline) {
		return errors.New("invalid pinned executable, authorization, deadline, or recovery limit")
	}
	info, err := os.Lstat(s.Executable)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return errors.New("pinned executable must be an executable regular file, not a symlink")
	}
	if err := canonicalWorkspace(s.Workspace); err != nil {
		return err
	}
	return nil
}

func canonicalWorkspace(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("workspace must be a directory")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	if resolved != path {
		return errors.New("workspace must use its canonical path")
	}
	return nil
}

func cleanAbsolute(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsRune(path, 0)
}
func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}
func allowedEvent(kind string) bool {
	switch kind {
	case "restart", "login", "wake", "launch", "stop", "observer_closed", "recovery_blocked":
		return true
	}
	return false
}
func appendEvent(events []Event, event Event) []Event {
	events = append(events, event)
	if len(events) > 128 {
		events = events[len(events)-128:]
	}
	return events
}

func readJob(path string) (Job, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Job{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return Job{}, errors.New("unsafe supervision job record")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Job{}, err
	}
	var job Job
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&job); err != nil {
		return Job{}, err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return Job{}, errors.New("trailing supervision job data")
	}
	if err := validateJob(job); err != nil {
		return Job{}, err
	}
	return job, nil
}

func validateJob(j Job) error {
	id, err := ID(j.Workspace, j.GoalID)
	if err != nil || j.SchemaVersion != schemaVersion || j.ID != id || !cleanAbsolute(j.Executable) || j.AuthorizationDigest == "" || j.Deadline.IsZero() || j.Phase != PhaseReady && j.Phase != PhaseRunning && j.Phase != PhasePaused && j.Phase != PhaseBlocked {
		return errors.New("invalid supervision job record")
	}
	if err := canonicalWorkspace(j.Workspace); err != nil {
		return fmt.Errorf("supervised workspace: %w", err)
	}
	if len(j.Events) > 128 {
		return errors.New("supervision event history exceeds limit")
	}
	for _, event := range j.Events {
		if event.At.IsZero() || len(event.Kind) > 64 || len(event.Detail) > 1024 {
			return errors.New("invalid supervision event")
		}
	}
	return nil
}

func (m *Manager) lock(id string, fn func() error) error {
	path, err := m.JobPath(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	lockPath := path + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func writeJSON(path string, job Job) error {
	if err := validateJob(job); err != nil {
		return err
	}
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(path, append(data, '\n'), 0600)
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return errors.New("refusing non-regular supervision path")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".forgepilot-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func xml(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;")
	return r.Replace(s)
}

func plistXML(job Job, path string) string {
	return "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\"><dict>\n" +
		"<key>Label</key><string>" + xml(label(job.ID)) + "</string>\n" +
		"<key>ProgramArguments</key><array><string>" + xml(job.Executable) + "</string><string>execution</string><string>supervise</string><string>run</string><string>--job</string><string>" + xml(path) + "</string></array>\n" +
		"<key>WorkingDirectory</key><string>" + xml(job.Workspace) + "</string>\n" +
		"<key>RunAtLoad</key><true/>\n<key>StartInterval</key><integer>60</integer>\n" +
		"<key>LimitLoadToSessionType</key><string>Aqua</string>\n</dict></plist>\n"
}
