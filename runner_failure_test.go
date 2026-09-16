package forgepilot_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/CarlLee1983/ForgePilot/internal/repository"
	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

// A runtime that exits 0 without ever writing a usable result must not be read
// as completion. Exit code zero is what a well-behaved CLI reports, but a
// session that says nothing has told ForgePilot nothing, and treating silence
// as success would let a broken or misconfigured runtime "finish" work that was
// never touched.
func TestAgentCleanExitWithoutAResultIsNotCompletion(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, `printf 'done\n' > "$workspace/$lower.txt"`)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot",
		"--max-attempts-per-work", "2")
	if code == 0 {
		t.Fatalf("exit = 0 for a session that returned no result\n%s", output)
	}
	if !strings.Contains(output, "protocol error") && !strings.Contains(output, "no usable result") &&
		!strings.Contains(output, "no result") {
		t.Fatalf("output does not explain the protocol failure:\n%s", output)
	}

	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range state.WorkItems {
		if item.Status == work.Verified {
			t.Fatalf("%s reached VERIFIED although its session never reported a result", item.ID)
		}
	}
}

// The canonical check is what decides pass or fail, not the agent's own claim
// of "finished". This proves a real FAIL from the repository's own `make
// verify` drives a bounded repair attempt rather than being swallowed or
// mistaken for a pass.
func TestVerificationFailureLeadsToBoundedRepairThenPass(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, `if [ "$attempt" = "1" ]; then
  printf 'broken\n' > "$workspace/$lower.txt"
else
  printf 'done\n' > "$workspace/$lower.txt"
fi
printf '{"outcome":"implementation_finished","summary":"attempt %s for %s"}' "$attempt" "$item" > "$result"
`)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, output)
	}
	if sessions := fixture.sessions(t); len(sessions) != 2 || sessions[0] != "WI-001" || sessions[1] != "WI-001" {
		t.Fatalf("sessions = %v, want two sessions for the same work item", sessions)
	}

	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	var results []work.Result
	for _, evidence := range state.Evidence {
		if evidence.WorkItemID == "WI-001" && evidence.Type == work.VerificationEvidence {
			results = append(results, evidence.Result)
		}
	}
	if len(results) < 2 || results[0] != work.Fail {
		t.Fatalf("verification Evidence = %v, want a FAIL before a PASS", results)
	}
	if results[len(results)-1] != work.Pass {
		t.Fatalf("verification Evidence = %v, want the last entry to be PASS", results)
	}
}

// A session that cannot run at all — missing credentials, missing tooling —
// is the agent's problem, not the repository's. Verifying anyway would
// manufacture an engineering FAIL for code that was never even attempted, so
// execution_failed must stop the run without recording any Verification
// Evidence for the item.
func TestAgentExecutionFailedStopsWithoutInventingAVerificationFailure(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, `printf '{"outcome":"execution_failed","summary":"no credentials","error":"not logged in"}' > "$result"`)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	if code != 2 {
		t.Fatalf("exit = %d, want 2\n%s", code, output)
	}
	if !strings.Contains(output, "not logged in") {
		t.Fatalf("output does not mention the agent's error:\n%s", output)
	}

	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, evidence := range state.Evidence {
		if evidence.WorkItemID == "WI-001" && evidence.Type == work.VerificationEvidence {
			t.Fatalf("a credentials failure was recorded as Verification Evidence: %#v", evidence)
		}
	}
}

// needs_human must become an actionable Gate a person can resolve, not just a
// stop. This proves the question and the agent's own options survive into
// state exactly, and that stopping for a person never quietly marks the item
// VERIFIED or resolves the question on its own.
func TestNeedsHumanStopsAndRecordsAGate(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, `printf '{"outcome":"needs_human","summary":"ambiguous","needs_human":{"question":"which store?","options":["postgres","sqlite"]}}' > "$result"`)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	if code != 2 {
		t.Fatalf("exit = %d, want 2\n%s", code, output)
	}
	if !strings.Contains(output, "which store?") {
		t.Fatalf("output does not name the question:\n%s", output)
	}

	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	gates := state.GatesFor("WI-001")
	if len(gates) != 1 {
		t.Fatalf("gates = %v, want exactly one", gates)
	}
	gate := gates[0]
	if gate.Status != work.GateOpen {
		t.Fatalf("gate status = %s, want OPEN", gate.Status)
	}
	if len(gate.Options) != 2 || gate.Options[0] != "postgres" || gate.Options[1] != "sqlite" {
		t.Fatalf("gate options = %v, want the agent's exact two options", gate.Options)
	}
	if gate.Choice != "" || gate.DecidedBy != "" {
		t.Fatalf("gate was resolved without a person: %#v", gate)
	}
	for _, item := range state.WorkItems {
		if item.ID == "WI-001" && item.Status == work.Verified {
			t.Fatal("WI-001 reached VERIFIED while a Gate is still open")
		}
	}
}

// A Gate needs at least two real options for a person to choose between; a
// single unstated option is not a choice. This proves ForgePilot never
// fabricates a second option just to have something to record.
func TestNeedsHumanWithoutOptionsStopsWithoutFabricatingAGate(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, `if [ "$attempt" -eq 1 ]; then
  printf '{"outcome":"needs_human","summary":"ambiguous","needs_human":{"question":"what now?"}}' > "$result"
else
  printf 'done\n' > "$workspace/$lower.txt"
  printf '{"outcome":"implementation_finished","summary":"implemented after human handling"}' > "$result"
fi`)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot",
		"--max-attempts-per-work", "1")
	if code != 2 {
		t.Fatalf("exit = %d, want 2\n%s", code, output)
	}

	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if gates := state.GatesFor("WI-001"); len(gates) != 0 {
		t.Fatalf("gates = %v, want none: ForgePilot must not invent options", gates)
	}
	runID := lastRun(t, fixture.root)
	output, code = fixture.runForge(t, agent, "run", "resume", runID)
	if code != 0 || !strings.Contains(output, "AWAITING_GOAL_REVIEW") {
		t.Fatalf("resume exit = %d: a valid human wait consumed the technical retry\n%s", code, output)
	}
	record := loadRunRecord(t, fixture.root, runID)
	if record["attempts"].(map[string]any)["WI-001"] != float64(2) ||
		record["human_waits"].(map[string]any)["WI-001"] != float64(1) {
		t.Fatalf("human wait accounting = attempts %v, waits %v", record["attempts"], record["human_waits"])
	}
}

// A run record from before human-wait accounting has no trustworthy way to
// distinguish an old question from a technical attempt. Resume must therefore
// charge every persisted attempt conservatively, while preserving the original
// deadline and step budget instead of treating the old shape as a new run.
func TestLegacyRunRecordResumesWithAllOldAttemptsChargedAsTechnical(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, `if [ "$attempt" -eq 1 ]; then
  printf '{"outcome":"needs_human","summary":"ambiguous","needs_human":{"question":"which store?","options":["postgres","sqlite"]}}' > "$result"
else
  printf 'done\n' > "$workspace/$lower.txt"
  printf '{"outcome":"implementation_finished","summary":"should not launch"}' > "$result"
fi`)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot",
		"--max-attempts-per-work", "1")
	if code != 2 || !strings.Contains(output, "AGENT_NEEDS_HUMAN") {
		t.Fatalf("first run exit = %d\n%s", code, output)
	}
	runID := lastRun(t, fixture.root)
	legacy := loadRunRecord(t, fixture.root, runID)
	delete(legacy, "human_waits")
	writeRunRecord(t, fixture.root, runID, legacy)
	deadline, steps, attempts := legacy["deadline"], legacy["steps"], legacy["attempts"]
	mustRun(t, fixture.binary, fixture.root, "gate", "resolve", "GATE-001", "--option", "postgres",
		"--by", fixtureIdentity)

	output, code = fixture.runForge(t, agent, "run", "resume", runID)
	if code != 3 || !strings.Contains(output, "MAX_ATTEMPTS") {
		t.Fatalf("legacy resume exit = %d, want MAX_ATTEMPTS\n%s", code, output)
	}
	if sessions := fixture.sessions(t); len(sessions) != 1 {
		t.Fatalf("legacy resume launched another session: %v", sessions)
	}
	after := loadRunRecord(t, fixture.root, runID)
	if after["deadline"] != deadline || after["steps"] != steps {
		t.Fatalf("legacy resume reset budget: deadline %v -> %v, steps %v -> %v",
			deadline, after["deadline"], steps, after["steps"])
	}
	if got := after["attempts"]; !reflect.DeepEqual(got, attempts) {
		t.Fatalf("legacy attempts changed on refused resume: %v -> %v", attempts, got)
	}
}

// A Human Decision is durable domain context, not an attempt summary. The
// session resumed after the decision and every downstream session must receive
// the exact resolved Gate from current state. Waiting for that decision is not
// a technical retry, even when the configured technical budget is one.
func TestResolvedGateDecisionFlowsIntoResumeAndDownstreamHandoffs(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md", "b.md", "c.md", "d.md", "sibling.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue",
		[]string{"specs/stories/a.md"},
		[]string{"specs/stories/b.md", "WI-001"},
		[]string{"specs/stories/c.md", "WI-001"},
		[]string{"specs/stories/d.md", "WI-002", "WI-003"},
		[]string{"specs/stories/sibling.md"})
	agent := fixture.fakeAgent(t, `if [ "$item" = "WI-001" ] && [ "$attempt" -eq 1 ]; then
  printf '{"outcome":"needs_human","summary":"store choice required","needs_human":{"question":"which store?","options":["postgres","sqlite"]}}' > "$result"
else
  printf 'done\n' > "$workspace/$lower.txt"
  printf '{"outcome":"implementation_finished","summary":"implemented %s"}' "$item" > "$result"
fi
`)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot",
		"--max-attempts-per-work", "1", "--max-steps", "50")
	if code != 2 || !strings.Contains(output, "AGENT_NEEDS_HUMAN") {
		t.Fatalf("first run exit = %d\n%s", code, output)
	}
	runID := lastRun(t, fixture.root)
	mustRun(t, fixture.binary, fixture.root, "gate", "resolve", "GATE-001", "--option", "postgres",
		"--note", "keep operations simple", "--by", fixtureIdentity)
	// A resolved sibling Gate is durable too, but it is outside WI-001's
	// prerequisite closure and must not leak into these handoffs.
	mustRun(t, fixture.binary, fixture.root, "gate", "open", "--work", "WI-005",
		"--question", "unrelated deployment?", "--option", "blue", "--option", "green")
	mustRun(t, fixture.binary, fixture.root, "gate", "resolve", "GATE-002", "--option", "blue",
		"--note", "sibling only", "--by", fixtureIdentity)
	// A downstream decision is relevant only when that Work Item itself runs.
	mustRun(t, fixture.binary, fixture.root, "gate", "open", "--work", "WI-004",
		"--question", "downstream layout?", "--option", "compact", "--option", "expanded")
	mustRun(t, fixture.binary, fixture.root, "gate", "resolve", "GATE-003", "--option", "compact",
		"--note", "owned by WI-004", "--by", fixtureIdentity)
	// CANCELLED is a withdrawn question, not a selected decision.
	mustRun(t, fixture.binary, fixture.root, "gate", "open", "--work", "WI-001",
		"--question", "obsolete question?", "--option", "yes", "--option", "no")
	mustRun(t, fixture.binary, fixture.root, "gate", "cancel", "GATE-004", "--reason", "question withdrawn",
		"--by", fixtureIdentity)
	mustRun(t, fixture.binary, fixture.root, "gate", "open", "--work", "WI-001",
		"--question", "which migration mode?", "--option", "online", "--option", "offline")
	mustRun(t, fixture.binary, fixture.root, "gate", "resolve", "GATE-005", "--option", "online",
		"--by", fixtureIdentity)

	output, code = fixture.runForge(t, agent, "run", "resume", runID)
	if code != 0 || !strings.Contains(output, "AWAITING_GOAL_REVIEW") {
		t.Fatalf("resume exit = %d\n%s", code, output)
	}
	for _, itemID := range []string{"WI-001", "WI-002", "WI-003", "WI-004"} {
		briefing := fixture.handoff(t, itemID)
		for _, wanted := range []string{"GATE-001", "WI-001", "which store?", "postgres", "keep operations simple"} {
			if !strings.Contains(briefing, wanted) {
				t.Fatalf("%s handoff lost %q:\n%s", itemID, wanted, briefing)
			}
		}
		for _, unrelated := range []string{"GATE-002", "unrelated deployment?", "sibling only", "GATE-004", "obsolete question?", "question withdrawn"} {
			if strings.Contains(briefing, unrelated) {
				t.Fatalf("%s handoff leaked unrelated decision %q:\n%s", itemID, unrelated, briefing)
			}
		}
		if itemID != "WI-004" {
			for _, downstream := range []string{"GATE-003", "downstream layout?", "owned by WI-004"} {
				if strings.Contains(briefing, downstream) {
					t.Fatalf("%s handoff leaked downstream decision %q:\n%s", itemID, downstream, briefing)
				}
			}
		}
	}
	if briefing := fixture.handoff(t, "WI-004"); strings.Count(briefing, "GATE-001") != 1 {
		t.Fatalf("diamond dependencies duplicated the resolved Gate:\n%s", briefing)
	} else {
		first, downstream, later := strings.Index(briefing, "GATE-001"), strings.Index(briefing, "GATE-003"), strings.Index(briefing, "GATE-005")
		if first < 0 || downstream < first || later < downstream {
			t.Fatalf("resolved Gates lost durable opening order:\n%s", briefing)
		}
		if !strings.Contains(briefing, "GATE-005 on WI-001\n  Question: which migration mode?\n  Selected choice: online\n  Resolution note: ") ||
			strings.Contains(briefing, "GATE-005 on WI-001\n  Question: which migration mode?\n  Selected choice: online\n  Resolution note: none") {
			t.Fatalf("empty resolution note was not rendered exactly:\n%s", briefing)
		}
	}
	record := loadRunRecord(t, fixture.root, runID)
	attempts := record["attempts"].(map[string]any)
	if attempts["WI-001"] != float64(2) {
		t.Fatalf("WI-001 attempts = %v, want two launched sessions", attempts["WI-001"])
	}
	humanWaits := record["human_waits"].(map[string]any)
	if humanWaits["WI-001"] != float64(1) {
		t.Fatalf("WI-001 human waits = %v, want one", humanWaits["WI-001"])
	}
}

// Without a bound, a session that never converges would loop forever. This
// proves the attempts-per-work limit actually stops the run rather than being
// a number that is merely recorded.
func TestMaxAttemptsPerWorkIsBounded(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, `printf 'broken\n' > "$workspace/$lower.txt"
printf '{"outcome":"implementation_finished","summary":"attempt %s"}' "$attempt" > "$result"
`)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot",
		"--max-attempts-per-work", "2", "--max-steps", "50")
	if code != 3 {
		t.Fatalf("exit = %d, want 3\n%s", code, output)
	}
	if !strings.Contains(output, "MAX_ATTEMPTS") && !strings.Contains(output, "NO_PROGRESS") {
		t.Fatalf("output does not name a bounded stop reason:\n%s", output)
	}
	if sessions := fixture.sessions(t); len(sessions) != 2 {
		t.Fatalf("sessions = %v, want exactly two technical attempts", sessions)
	}
}

// A step budget must stop a run even mid-Goal, and stopping there must not be
// dressed up as reaching the review boundary: nothing has actually been
// verified yet when the budget runs out this early.
func TestMaxStepsStopsTheRun(t *testing.T) {
	fixture := newRunnerFixture(t)
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue",
		[]string{"specs/stories/a.md"},
		[]string{"specs/stories/b.md", "WI-001"},
		[]string{"specs/stories/c.md", "WI-002"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot",
		"--max-steps", "2")
	if code != 3 {
		t.Fatalf("exit = %d, want 3\n%s", code, output)
	}
	if !strings.Contains(output, "MAX_STEPS") {
		t.Fatalf("output does not name MAX_STEPS:\n%s", output)
	}
	if strings.Contains(output, "AWAITING_GOAL_REVIEW") {
		t.Fatalf("a step-limited stop was reported as the review boundary:\n%s", output)
	}
}

// The Runner writes its own artifacts under .forgepilot/runs/, a directory
// that must already be inside the same ignore rules a Candidate snapshot
// relies on. If it were not, a run's own bookkeeping would change what the
// next verification checks out.
func TestRunnerArtifactsDoNotChangeTheCandidateDigest(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, output)
	}

	before, err := repository.InspectSnapshot(context.Background(), fixture.root)
	if err != nil {
		t.Fatal(err)
	}

	runID := lastRun(t, fixture.root)
	if err := storage.WriteRunArtifact(fixture.root, runID, "extra.log", []byte("more runner bookkeeping\n"),
		storage.ArtifactLimits{}); err != nil {
		t.Fatal(err)
	}

	after, err := repository.InspectSnapshot(context.Background(), fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if after.Digest != before.Digest {
		t.Fatalf("digest changed after writing a runner artifact: before %s, after %s", before.Digest, after.Digest)
	}
}

// `run status` must answer two different questions with two different fields:
// what the run concluded when it stopped, and what the Goal's readiness is
// right now. The workspace can move after the run ends, and a stored
// conclusion must never be restated as if it still held.
func TestRunStatusSeparatesTheStoredResultFromCurrentReadiness(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, output)
	}
	runID := lastRun(t, fixture.root)

	text, code := fixture.runForge(t, "", "run", "status", runID)
	if code != 0 {
		t.Fatalf("run status exit = %d\n%s", code, text)
	}
	if !strings.Contains(text, "AWAITING_GOAL_REVIEW") {
		t.Fatalf("run status does not show the stored stop reason:\n%s", text)
	}
	if !strings.Contains(text, "awaiting goal final review") {
		t.Fatalf("run status does not show a separately-labelled current readiness:\n%s", text)
	}

	jsonOutput, code := fixture.runForge(t, "", "run", "status", runID, "--json")
	if code != 0 {
		t.Fatalf("run status --json exit = %d\n%s", code, jsonOutput)
	}
	var view map[string]any
	if err := json.Unmarshal([]byte(jsonOutput), &view); err != nil {
		t.Fatalf("decode run status --json: %v\n%s", err, jsonOutput)
	}
	stopReason, hasStopReason := view["stop_reason"]
	currentGoal, hasCurrentGoal := view["current_goal_readiness"]
	if !hasStopReason || !hasCurrentGoal {
		t.Fatalf("run status --json is missing distinct stop_reason/current_goal_readiness keys: %v", view)
	}
	if stopReason != "AWAITING_GOAL_REVIEW" || currentGoal != "awaiting goal final review" {
		t.Fatalf("stop_reason = %v, current_goal_readiness = %v", stopReason, currentGoal)
	}

	// Move the workspace on: the stored conclusion must stay put while the
	// current readiness reflects what changed.
	write(t, filepath.Join(fixture.root, "specs", "stories", "d.md"), "# story d\n")
	commitAll(t, fixture.root, "add another story")

	jsonAfter, code := fixture.runForge(t, "", "run", "status", runID, "--json")
	if code != 0 {
		t.Fatalf("run status --json exit = %d\n%s", code, jsonAfter)
	}
	var viewAfter map[string]any
	if err := json.Unmarshal([]byte(jsonAfter), &viewAfter); err != nil {
		t.Fatalf("decode run status --json: %v\n%s", err, jsonAfter)
	}
	if viewAfter["stop_reason"] != "AWAITING_GOAL_REVIEW" {
		t.Fatalf("stop_reason changed after the workspace moved on: %v", viewAfter["stop_reason"])
	}
	if viewAfter["current_goal_readiness"] == "awaiting goal final review" {
		t.Fatalf("current_goal_readiness still reads as awaiting review after the workspace changed: %v", viewAfter)
	}
}

// A Runtime Contract the machine cannot satisfy is a refusal, not an
// engineering failure. It must stop the run before any Verification Run begins,
// so no FAIL Evidence is ever written for a problem the repository's code had
// nothing to do with.
func TestAnUnsatisfiableToolchainStopsWithoutFakeFailEvidence(t *testing.T) {
	fixture := newRunnerFixture(t, "a.md")
	// The Candidate declares a Node version nobody has installed. It is read from
	// the isolated checkout, so it only takes effect once verification starts.
	write(t, filepath.Join(fixture.root, ".node-version"), "0.0.1-does-not-exist\n")
	commitAll(t, fixture.root, "declare an impossible runtime")
	mustRun(t, fixture.binary, fixture.root, "init")
	fixture.seedGoal(t, "queue", []string{"specs/stories/a.md"})
	agent := fixture.fakeAgent(t, implementsCleanly)

	output, code := fixture.runForge(t, agent, "run", "--goal", "queue", "--runtime", "fake", "--snapshot")
	if code != 2 {
		t.Fatalf("exit = %d\n%s", code, output)
	}
	if !strings.Contains(output, "VERIFICATION_REFUSED") {
		t.Fatalf("a toolchain problem was not classified as a refusal:\n%s", output)
	}

	state, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, evidence := range state.Evidence {
		if evidence.Result == work.Fail {
			t.Fatalf("a toolchain problem produced FAIL evidence %s", evidence.ID)
		}
	}
	for _, item := range state.WorkItems {
		if item.Status == work.Verified {
			t.Fatalf("%s reached VERIFIED without a verification run", item.ID)
		}
	}
}
