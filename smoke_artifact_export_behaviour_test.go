package forgepilot_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/agent"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
)

// The export helper is what makes a real smoke round readable after its
// disposable fixture is gone, so it is tested without a model. Every property
// below is one the expensive round depends on and cannot re-run cheaply.

func readManifest(t *testing.T, export *artifactExport) artifactManifest {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(export.dir, "manifest.json"))
	if err != nil {
		t.Fatalf("no manifest was written: %v", err)
	}
	var manifest artifactManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestArtifactExportRecordsWhatItSavedAndWhereItCameFrom(t *testing.T) {
	source := t.TempDir()
	run := filepath.Join(source, "runs", "run-20260915t101010-abcdef", "wi-002-attempt-1")
	write(t, filepath.Join(run, "result.json"), `{"outcome":"implementation_finished"}`)
	write(t, filepath.Join(run, "session.log"), "session output\n")
	write(t, filepath.Join(source, "runs", "run-20260915t101010-abcdef", "run.json"), `{"run_id":"x"}`)

	export, err := openArtifactExport(t.TempDir(), "smoke", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	export.copyTree("runs", filepath.Join(source, "runs"), nil)
	export.write("environment.json", []byte(`{"go":"go1.25.5"}`))
	if err := export.close(time.Now()); err != nil {
		t.Fatalf("close = %v, want a complete export", err)
	}

	manifest := readManifest(t, export)
	if !manifest.Complete {
		t.Fatalf("manifest = %#v, want complete", manifest)
	}
	byPath := map[string]artifactEntry{}
	for _, entry := range manifest.Files {
		byPath[entry.Path] = entry
	}
	if len(byPath) != 4 {
		t.Fatalf("manifest files = %#v, want four", manifest.Files)
	}
	result := byPath["runs/run-20260915t101010-abcdef/wi-002-attempt-1/result.json"]
	if result.WorkItemID != "WI-002" || result.Attempt != 1 || result.RunID != "run-20260915t101010-abcdef" {
		t.Fatalf("result.json entry = %#v, want it tied to WI-002 attempt 1", result)
	}
	// The manifest exists so a later reader can check a file against its digest,
	// so the digest is recomputed from the exported bytes rather than merely
	// checked for being sixty-four characters long.
	exported, err := os.ReadFile(filepath.Join(export.dir, filepath.FromSlash(result.Path)))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(exported)
	if result.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("result.json digest = %q, want %x", result.SHA256, digest)
	}
	copied, err := os.ReadFile(filepath.Join(export.dir, "runs", "run-20260915t101010-abcdef", "wi-002-attempt-1", "session.log"))
	if err != nil || string(copied) != "session output\n" {
		t.Fatalf("session.log = %q, %v; the bytes were not preserved", copied, err)
	}
}

func TestArtifactExportKeepsWhatItHasWhenSomethingIsMissing(t *testing.T) {
	source := t.TempDir()
	write(t, filepath.Join(source, "runs", "run-a", "run.json"), `{"run_id":"run-a"}`)

	export, err := openArtifactExport(t.TempDir(), "smoke", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	export.copyTree("runs", filepath.Join(source, "runs"), nil)
	export.copyFile("logs/verification.log", filepath.Join(source, "never-written.log"))
	// A FAIL or an early stop must still leave an index behind, so an absent
	// source is not an export failure.
	if err := export.close(time.Now()); err != nil {
		t.Fatalf("close = %v; a missing source is a gap, not an export failure", err)
	}

	manifest := readManifest(t, export)
	if manifest.Complete {
		t.Fatal("a manifest with a gap must not report itself complete")
	}
	if len(manifest.Files) != 1 || manifest.Files[0].Path != "runs/run-a/run.json" {
		t.Fatalf("files = %#v, want the run record that did exist", manifest.Files)
	}
	if len(manifest.Missing) != 1 || manifest.Missing[0].Path != "logs/verification.log" ||
		!strings.Contains(manifest.Missing[0].Reason, "never produced") {
		t.Fatalf("missing = %#v, want the absent log named with a reason", manifest.Missing)
	}
}

func TestArtifactExportReportsAFailedCopyInsteadOfSwallowingIt(t *testing.T) {
	source := t.TempDir()
	write(t, filepath.Join(source, "run.json"), `{"run_id":"run-a"}`)
	base := t.TempDir()
	export, err := openArtifactExport(base, "smoke", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	export.copyFile("run.json", filepath.Join(source, "run.json"))
	// A destination that cannot be written is the failure this asserts on: an
	// export that reported success here would leave a round believing its
	// evidence was saved.
	if err := os.Chmod(export.dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(export.dir, 0o700) })
	export.write("environment.json", []byte(`{"go":"go1.25.5"}`))

	err = export.close(time.Now())
	if err == nil {
		t.Fatal("close = nil; a failed write must be reported")
	}
	if !strings.Contains(err.Error(), "environment.json") {
		t.Fatalf("close = %v, want the failing path named", err)
	}
}

func TestArtifactExportGivesEachRoundItsOwnDirectory(t *testing.T) {
	base := t.TempDir()
	at := time.Date(2026, 9, 15, 10, 10, 10, 0, time.UTC)
	first, err := openArtifactExport(base, "smoke", at)
	if err != nil {
		t.Fatal(err)
	}
	// The same second is the case a timestamp alone does not survive, and
	// overwriting the previous round's evidence is the failure being prevented.
	second, err := openArtifactExport(base, "smoke", at)
	if err != nil {
		t.Fatal(err)
	}
	if first.dir == second.dir {
		t.Fatalf("both rounds wrote to %s", first.dir)
	}
	first.write("environment.json", []byte("first"))
	second.write("environment.json", []byte("second"))
	if err := first.close(at); err != nil {
		t.Fatal(err)
	}
	if err := second.close(at); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(first.dir, "environment.json"))
	if err != nil || string(contents) != "first" {
		t.Fatalf("the first round's evidence = %q, %v", contents, err)
	}
}

func TestArtifactExportRefusesAPathThatWouldLeaveItsDirectory(t *testing.T) {
	base := t.TempDir()
	export, err := openArtifactExport(base, "smoke", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	export.write(filepath.Join("..", "escaped.json"), []byte("no"))
	if err := export.close(time.Now()); err == nil {
		t.Fatal("close = nil; an escaping path must be refused and reported")
	}
	if _, err := os.Stat(filepath.Join(base, "escaped.json")); err == nil {
		t.Fatal("a file was written outside the round's directory")
	}
}

func TestArtifactExportNeedsAnAbsoluteDestination(t *testing.T) {
	if _, err := openArtifactExport("relative/path", "smoke", time.Now()); err == nil {
		t.Fatal("a relative destination must be refused: it would resolve against the test's working directory")
	}
	if _, err := openArtifactExport("", "smoke", time.Now()); err == nil {
		t.Fatal("an empty destination must be refused")
	}
}

// TestSmokeEvidenceOutlivesTheFixtureItCameFrom rehearses the whole export
// against a fake-runtime run. It is the only cheap way to find out whether the
// expensive round's evidence would actually survive, and it runs in the default
// suite because the failure it catches — an export wired up wrongly — is
// otherwise discovered only after the quota is spent.
func TestSmokeEvidenceOutlivesTheFixtureItCameFrom(t *testing.T) {
	fixture := newRunnerFixture(t)
	// The rehearsal fixture carries the same shape the smoke fixture does, so a
	// file the export looks for is absent here only when it would be absent
	// there too.
	write(t, filepath.Join(fixture.root, "AGENTS.md"), "# Rehearsal fixture\n")
	write(t, filepath.Join(fixture.root, "go.mod"), "module example.com/forgepilot-export-rehearsal\n\ngo 1.25.5\n")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "evidence", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	round := &smokeRound{TimeZone: time.Now().Format("MST-07:00")}
	round.StartedAt = time.Now()
	output, code := fixture.runForge(t, agent, "run", "--goal", "evidence", "--runtime", "fake", "--snapshot")
	round.FinishedAt = time.Now()
	round.RunnerOutput, round.RunnerExit = output, code
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, output)
	}
	round.RunID = lastRun(t, fixture.root)

	export, err := openArtifactExport(t.TempDir(), "rehearsal", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	collectSmokeEvidence(t, export, fixture, round)
	if err := export.close(time.Now()); err != nil {
		t.Fatalf("the rehearsal export failed: %v", err)
	}

	manifest := readManifest(t, export)
	saved := map[string]artifactEntry{}
	for _, entry := range manifest.Files {
		saved[entry.Path] = entry
	}
	// The identity, the input the session was given, the result it returned, the
	// durable state the assertions read, and the objects the Candidate is made
	// of. Losing any one of them makes the round unreadable afterwards.
	required := []string{
		"environment.json",
		"runner-output.txt",
		"inputs/specs/stories/a.md",
		"forgepilot/state.json",
		"forgepilot/runs/" + round.RunID + "/run.json",
		"forgepilot/runs/" + round.RunID + "/wi-001-attempt-1/result.json",
		"forgepilot/runs/" + round.RunID + "/wi-001-attempt-1/handoff.md",
		"fixture.bundle",
		"fixture-refs.txt",
	}
	for _, path := range required {
		if _, ok := saved[path]; !ok {
			t.Errorf("the export has no %s; manifest holds %d files and %#v gaps", path, len(manifest.Files), manifest.Missing)
		}
	}
	if entry := saved["forgepilot/runs/"+round.RunID+"/wi-001-attempt-1/result.json"]; entry.WorkItemID != "WI-001" || entry.Attempt != 1 {
		t.Errorf("result.json entry = %#v, want it tied to WI-001 attempt 1", entry)
	}
	// A lock file is deliberately not copied, and the manifest says so rather
	// than leaving its absence to be guessed at.
	explained := false
	for _, excluded := range manifest.Excluded {
		if strings.HasPrefix(excluded.Path, "forgepilot/locks") {
			explained = true
		}
	}
	if !explained {
		t.Errorf("exclusions = %#v, want the skipped lock directory named", manifest.Excluded)
	}
	// A deliberate exclusion is not a gap, so it must not make a whole export
	// report itself as missing evidence.
	if !manifest.Complete {
		t.Errorf("manifest is incomplete: missing %#v, failures %#v", manifest.Missing, manifest.Failures)
	}

	// The Candidate must be recoverable from the export alone, which is the
	// difference between recording a snapshot SHA and keeping what it points at.
	var environment smokeEnvironment
	if err := json.Unmarshal(mustReadExport(t, export, "environment.json"), &environment); err != nil {
		t.Fatal(err)
	}
	if environment.ForgePilotCommit == "" || environment.Go == "" || environment.Round.RunID != round.RunID {
		t.Errorf("environment.json = %#v, want the source commit, the toolchain and the run it describes", environment)
	}
	restored := t.TempDir()
	clone := exec.Command("git", "clone", "--bare", filepath.Join(export.dir, "fixture.bundle"), filepath.Join(restored, "repo.git"))
	if out, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("the exported bundle does not restore: %v: %s", err, out)
	}
	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	evidence, ok := state.LatestVerification("WI-001")
	if !ok {
		t.Fatal("no verification evidence to check the bundle against")
	}
	show := exec.Command("git", "-C", filepath.Join(restored, "repo.git"), "cat-file", "-t", evidence.Revision)
	if out, err := show.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "commit" {
		t.Errorf("the verified candidate %s is not in the exported bundle: %s %v", evidence.Revision, out, err)
	}
}

func mustReadExport(t *testing.T, export *artifactExport, relative string) []byte {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(export.dir, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

// TestTheRecordedSandboxIsTheOneTheAdapterAsks binds the sandbox sentence in
// environment.json to the adapter it describes. The sentence is written by
// hand, so without this the adapter could change and the evidence would go on
// claiming the old setting — which is worse than saying nothing.
func TestTheRecordedSandboxIsTheOneTheAdapterAsks(t *testing.T) {
	// Any resolvable executable will do: Plan's argument list does not depend on
	// which CLI it found, only on the adapter's own choices.
	plan, err := (agent.Codex{Command: "git"}).Plan(agent.Request{
		Workspace: t.TempDir(), ArtifactDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments := strings.Join(plan.Args, " ")
	if !strings.Contains(arguments, "--sandbox workspace-write") {
		t.Fatalf("the adapter asks for %q; environment.json says workspace-write", arguments)
	}
	for _, bypass := range []string{"--dangerously-bypass-approvals-and-sandbox", "--full-auto", "--yolo"} {
		if strings.Contains(arguments, bypass) {
			t.Fatalf("the adapter now passes %s; environment.json claims no bypass flags", bypass)
		}
	}
	round := &smokeRound{}
	sandbox := collectEnvironment(t, runnerFixture{root: t.TempDir()}, round).AdapterSandbox
	if !strings.Contains(sandbox, "workspace-write") || !strings.Contains(sandbox, "no bypass flags") {
		t.Fatalf("recorded sandbox = %q, want it to describe what the adapter asks for", sandbox)
	}
}
