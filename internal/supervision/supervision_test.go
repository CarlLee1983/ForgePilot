package supervision

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Manager, Spec, time.Time, string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(home, "generation", "forgepilot")
	if err := os.MkdirAll(filepath.Dir(tool), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	launchctl := filepath.Join(home, "launchctl")
	log := filepath.Join(home, "launchctl.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + log + "\"\nif [ \"$1\" = print ]; then exit 1; fi\n"
	if err := os.WriteFile(launchctl, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	manager.launchctl = launchctl
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	spec := Spec{Workspace: filepath.Join(home, "repo & <one>"), GoalID: "goal-1", Executable: tool, AuthorizationDigest: "sha256:abc", Deadline: now.Add(time.Hour)}
	if err := os.Mkdir(spec.Workspace, 0700); err != nil {
		t.Fatal(err)
	}
	return manager, spec, now, log
}

func TestInstallPersistsPinnedJobAndSafeLaunchAgent(t *testing.T) {
	m, spec, now, log := fixture(t)
	job, err := m.Install(spec, now)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := m.JobPath(job.ID)
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("job mode: %v %v", info, err)
	}
	content, err := os.ReadFile(m.plistPath(job.ID))
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("/usr/bin/plutil", "-lint", m.plistPath(job.ID)).CombinedOutput(); err != nil {
		t.Fatalf("native plist parser rejected LaunchAgent: %v: %s", err, output)
	}
	text := string(content)
	for _, want := range []string{spec.Executable, "<string>execution</string><string>supervise</string><string>run</string><string>--job</string>", path, "<key>RunAtLoad</key>", "<key>StartInterval</key><integer>60</integer>", "<key>WorkingDirectory</key><string>" + xml(spec.Workspace)} {
		if !strings.Contains(text, want) {
			t.Errorf("plist missing %q", want)
		}
	}
	if strings.Contains(text, "KeepAlive") || strings.Contains(text, spec.AuthorizationDigest) || strings.Contains(text, spec.Workspace) {
		t.Fatal("plist contains forbidden data or restart loop")
	}
	commands, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(commands), "bootstrap gui/") {
		t.Fatalf("launchctl calls: %s %v", commands, err)
	}
	if _, err := m.Install(spec, now); err != nil {
		t.Fatal("identical install must be retryable:", err)
	}
	spec.AuthorizationDigest = "other"
	if _, err := m.Install(spec, now); err == nil {
		t.Fatal("binding replacement accepted")
	}
}

func TestInstallRejectsStaleLoadedLaunchAgent(t *testing.T) {
	m, spec, now, _ := fixture(t)
	id, err := ID(spec.Workspace, spec.GoalID)
	if err != nil {
		t.Fatal(err)
	}
	jobPath, _ := m.JobPath(id)
	plist := m.plistPath(id)
	output := "gui/501/" + label(id) + " = {\n" +
		"\tpath = " + plist + "\n\tprogram = /old/forgepilot\n" +
		"\targuments = {\n\t\t/old/forgepilot\n\t\texecution\n\t\tsupervise\n\t\trun\n\t\t--job\n\t\t" + jobPath + "\n\t}\n" +
		"\tworking directory = " + spec.Workspace + "\n}\n"
	if matchesLoadedJob(output, Job{Executable: spec.Executable, Workspace: spec.Workspace}, plist, jobPath) {
		t.Fatal("stale loaded executable matched pinned job")
	}
	if err := os.WriteFile(m.launchctl, []byte("#!/bin/sh\nif [ \"$1\" = print ]; then cat <<'EOF'\n"+output+"EOF\nfi\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(spec, now); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("stale loaded label accepted: %v", err)
	}
	matching := strings.ReplaceAll(output, "/old/forgepilot", spec.Executable)
	if !matchesLoadedJob(matching, Job{Executable: spec.Executable, Workspace: spec.Workspace}, plist, jobPath) {
		t.Fatal("matching loaded service did not match pinned job")
	}
}

func TestEventAndPhasePersistAcrossManagers(t *testing.T) {
	m, spec, now, _ := fixture(t)
	job, err := m.Install(spec, now)
	if err != nil {
		t.Fatal(err)
	}
	const count = 12
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.RecordEvent(job.ID, "wake", "", now.Add(time.Minute)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	reloaded, err := New(m.home)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reloaded.Load(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != count+1 {
		t.Fatalf("lost concurrent events: %d", len(got.Events))
	}
	if _, err := m.SetPhase(job.ID, PhasePaused, "stop requested", now); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetPhase(job.ID, PhaseRunning, "", now); err == nil {
		t.Fatal("pause was cleared without explicit resume")
	}
	if _, err := m.Resume(job.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetPhase(job.ID, PhaseBlocked, "uncertain cleanup", now); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetPhase(job.ID, PhaseReady, "", now); err == nil {
		t.Fatal("recovery block was cleared without explicit resume")
	}
	if _, err := m.Resume(job.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetPhase(job.ID, PhasePaused, "stop again", now); err != nil {
		t.Fatal(err)
	}
	if err := m.Uninstall(job.ID, now); err != nil {
		t.Fatal(err)
	}
	got, err = m.Load(job.ID)
	if err != nil || got.Phase != PhasePaused {
		t.Fatalf("uninstall lost pause: %+v %v", got, err)
	}
	if _, err := os.Stat(m.plistPath(job.ID)); !os.IsNotExist(err) {
		t.Fatalf("plist remains after bootout: %v", err)
	}
}

func TestRejectsUnsafeRecordsAndPaths(t *testing.T) {
	m, spec, now, _ := fixture(t)
	job, err := m.Install(spec, now)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := m.JobPath(job.ID)
	job.SchemaVersion++
	data, _ := json.Marshal(job)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Load(job.ID); err == nil {
		t.Fatal("newer schema accepted")
	}
	if _, err := ID("relative", "goal"); err == nil {
		t.Fatal("relative workspace accepted")
	}
	if _, err := ID(spec.Workspace, "../goal"); err == nil {
		t.Fatal("unsafe goal accepted")
	}
	link := filepath.Join(m.home, "linked")
	if err := os.Symlink(spec.Executable, link); err != nil {
		t.Fatal(err)
	}
	spec.Executable = link
	if _, err := m.Install(spec, now); err == nil {
		t.Fatal("symlinked engine accepted")
	}
}
