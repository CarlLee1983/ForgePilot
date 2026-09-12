package work

import (
	"testing"
	"time"
)

func verifiableState(t *testing.T) (State, time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	state := NewState()
	if err := state.AddGoal("g", "Goal", "", "/repo", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.AddWork("g", "specs/stories/a", nil, now); err != nil {
		t.Fatal(err)
	}
	if err := state.Start("WI-001", now); err != nil {
		t.Fatal(err)
	}
	return state, now
}

func TestVerifiableRejectsWorkThatCannotBeVerified(t *testing.T) {
	state, _ := verifiableState(t)
	if err := state.Verifiable("WI-404"); err == nil {
		t.Fatal("accepted an unknown work item")
	}
	if err := state.Verifiable("WI-001"); err != nil {
		t.Fatalf("rejected RUNNING work: %v", err)
	}
	state.WorkItems[0].Status = Pending
	if err := state.Verifiable("WI-001"); err == nil {
		t.Fatal("accepted PENDING work")
	}
	state.WorkItems[0].Status = Review
	if err := state.Verifiable("WI-001"); err != nil {
		t.Fatalf("rejected REVIEW work: %v", err)
	}
	state.WorkItems[0].Status = Running
	state.Goals[0].Status = GoalBlocked
	if err := state.Verifiable("WI-001"); err == nil {
		t.Fatal("accepted work under a non-active goal")
	}
}

func TestRecordVerificationAppendsEvidenceAndMovesWork(t *testing.T) {
	state, now := verifiableState(t)
	if _, err := state.RecordVerification("WI-001", "abc123", "make verify", 0, now); err == nil {
		t.Fatal("recorded evidence for work that never entered a verification run")
	}
	if err := state.BeginVerification("WI-001", "abc123", "/tmp/wt", "", now); err != nil {
		t.Fatal(err)
	}
	if state.WorkItems[0].Status != Verifying || state.WorkItems[0].CurrentRun == nil {
		t.Fatalf("begin left %#v", state.WorkItems[0])
	}
	pass, err := state.RecordVerification("WI-001", "abc123", "make verify", 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if pass.ID != "EV-001" || pass.Result != Pass || pass.Type != VerificationEvidence {
		t.Fatalf("evidence = %#v", pass)
	}
	if pass.Revision != "abc123" || pass.Command != "make verify" || pass.ExitCode == nil || *pass.ExitCode != 0 {
		t.Fatalf("evidence lost the run's identity: %#v", pass)
	}
	if pass.WorkItemID != "WI-001" || pass.StoryRef != "specs/stories/a" || pass.Repository != "/repo" {
		t.Fatalf("evidence lost its bindings: %#v", pass)
	}
	if state.WorkItems[0].Status != Review {
		t.Fatalf("PASS left work as %s, want REVIEW", state.WorkItems[0].Status)
	}

	if err := state.BeginVerification("WI-001", "def456", "/tmp/wt", "", now); err != nil {
		t.Fatal(err)
	}
	fail, err := state.RecordVerification("WI-001", "def456", "make verify", 2, now)
	if err != nil {
		t.Fatal(err)
	}
	if fail.ID != "EV-002" || fail.Result != Fail || fail.ExitCode == nil || *fail.ExitCode != 2 {
		t.Fatalf("evidence = %#v", fail)
	}
	if state.WorkItems[0].Status != Running {
		t.Fatalf("FAIL left work as %s, want RUNNING", state.WorkItems[0].Status)
	}
	if len(state.Evidence) != 2 || state.Evidence[0].ID != "EV-001" || state.Evidence[0].Result != Pass {
		t.Fatalf("evidence was overwritten: %#v", state.Evidence)
	}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsInconsistentEvidence(t *testing.T) {
	state, now := verifiableState(t)
	if err := state.BeginVerification("WI-001", "abc123", "/tmp/wt", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerification("WI-001", "abc123", "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
	duplicated := state
	duplicated.Evidence = append(append([]Evidence(nil), state.Evidence...), state.Evidence[0])
	if err := duplicated.Validate(); err == nil {
		t.Fatal("accepted duplicate evidence IDs")
	}
	reused := state
	reused.NextEvidenceID = 1
	if err := reused.Validate(); err == nil {
		t.Fatal("accepted a next_evidence_id that would reuse an ID")
	}
	zero := 0
	orphaned := state
	orphaned.Evidence = []Evidence{{ID: "EV-001", Type: VerificationEvidence, WorkItemID: "WI-404", Revision: "abc", CandidateKind: CommitCandidate, Result: Pass, ExitCode: &zero}}
	if err := orphaned.Validate(); err == nil {
		t.Fatal("accepted evidence for an unknown work item")
	}
	unresulted := state
	unresulted.Evidence = []Evidence{{ID: "EV-001", Type: VerificationEvidence, WorkItemID: "WI-001", Revision: "abc", CandidateKind: CommitCandidate, Result: "MAYBE", ExitCode: &zero}}
	if err := unresulted.Validate(); err == nil {
		t.Fatal("accepted evidence with an unknown result")
	}
}

func TestLatestVerificationAndStaleness(t *testing.T) {
	state, now := verifiableState(t)
	if _, ok := state.LatestVerification("WI-001"); ok {
		t.Fatal("reported verification for work that has never been verified")
	}
	if state.Stale("WI-001", "abc123") {
		t.Fatal("never-verified work reported as stale rather than unverified")
	}
	if err := state.BeginVerification("WI-001", "abc123", "/tmp/wt", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerification("WI-001", "abc123", "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
	latest, ok := state.LatestVerification("WI-001")
	if !ok || latest.ID != "EV-001" {
		t.Fatalf("latest = %#v, %v", latest, ok)
	}
	if state.Stale("WI-001", "abc123") {
		t.Fatal("evidence for the current revision reported as stale")
	}
	if !state.Stale("WI-001", "def456") {
		t.Fatal("evidence for an older revision not reported as stale")
	}

	// A newer record for another Work Item must not become this one's latest.
	if _, err := state.AddWork("g", "specs/stories/b", nil, now); err != nil {
		t.Fatal(err)
	}
	if err := state.Start("WI-002", now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginVerification("WI-002", "def456", "/tmp/wt", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerification("WI-002", "def456", "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
	latest, _ = state.LatestVerification("WI-001")
	if latest.ID != "EV-001" {
		t.Fatalf("latest for WI-001 = %s, want EV-001", latest.ID)
	}
	if !state.Stale("WI-001", "def456") {
		t.Fatal("staleness leaked across work items")
	}
}

func TestReclaimRunRecordsAnInterruptionWithoutAnExitCode(t *testing.T) {
	state, now := verifiableState(t)
	if _, _, _, found, err := state.ReclaimRun("WI-001", "make verify", now); err != nil || found {
		t.Fatalf("reclaimed a run that was never started: %v, %v", found, err)
	}
	if err := state.BeginVerification("WI-001", "abc123", "/tmp/wt-abc", "", now); err != nil {
		t.Fatal(err)
	}
	evidence, abandoned, logFile, found, err := state.ReclaimRun("WI-001", "make verify", now)
	if err != nil || !found {
		t.Fatalf("ReclaimRun = %v, %v", found, err)
	}
	if evidence.Result != Interrupted {
		t.Fatalf("an abandoned run was recorded as %s", evidence.Result)
	}
	if evidence.ExitCode != nil {
		t.Fatalf("INTERRUPTED evidence carries exit code %d", *evidence.ExitCode)
	}
	if evidence.Revision != "abc123" {
		t.Fatalf("INTERRUPTED evidence lost the interrupted run's revision: %#v", evidence)
	}
	if abandoned != "/tmp/wt-abc" {
		t.Fatalf("abandoned worktree = %q, want the path state recorded", abandoned)
	}
	if logFile != "" {
		t.Fatalf("log path = %q, want empty since none was given to BeginVerification", logFile)
	}
	if state.WorkItems[0].Status != Running || state.WorkItems[0].CurrentRun != nil {
		t.Fatalf("reclaim left %#v", state.WorkItems[0])
	}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestOrphanReclaimSeparatesRecordingFromStarting draws the line the M3 spec
// draws: an interrupted run is a fact that already happened, so it is recorded
// regardless of what is blocking the Work Item, while starting a *new* run is
// subject to every block. Collapsing the two either loses the fact or opens a
// way past a Gate.
func TestOrphanReclaimSeparatesRecordingFromStarting(t *testing.T) {
	now := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	revision := "1111111111111111111111111111111111111111"
	state := NewState()
	if err := state.AddGoal("g", "Goal", "", "/repo", now); err != nil {
		t.Fatal(err)
	}
	item, err := state.AddWork("g", "specs/stories/a", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Start(item.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginVerification(item.ID, revision, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	// The runner dies here, leaving an orphan, and then a question is raised.
	if _, err := state.OpenGate(item.ID, "Which cache?", []string{"redis", "in-process"}, "", now); err != nil {
		t.Fatal(err)
	}

	// A new run must not start: an open Gate blocks verification, and an
	// abandoned run is not a licence to ignore it.
	if err := state.CanBeginVerification(item.ID); err == nil {
		t.Fatal("an orphaned run let verification start past an open gate")
	}
	// The fact that a run was interrupted is still recorded.
	evidence, _, _, found, err := state.ReclaimRun(item.ID, "make verify", now)
	if err != nil || !found {
		t.Fatalf("ReclaimRun = %#v, %v, %v", evidence, found, err)
	}
	if evidence.Result != Interrupted || evidence.Revision != revision {
		t.Fatalf("evidence = %#v", evidence)
	}
	if got := state.WorkItemStatus(item.ID); got != Running {
		t.Fatalf("status after reclaim = %s, want RUNNING", got)
	}

	// The same holds when the block is an inactive Goal rather than a Gate.
	if err := state.ResolveGate("GATE-001", "redis", "", "carl@example.com", now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginVerification(item.ID, revision, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if err := state.BlockGoal("g", "the direction is wrong", now); err != nil {
		t.Fatal(err)
	}
	if err := state.CanBeginVerification(item.ID); err == nil {
		t.Fatal("an orphaned run let verification start under a blocked goal")
	}
	if _, _, _, found, err := state.ReclaimRun(item.ID, "make verify", now); err != nil || !found {
		t.Fatalf("a blocked goal discarded an interrupted run: %v, %v", found, err)
	}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestLogPathSurvivesTheRunAndVanishesWithIt covers LogPath as a pure value:
// BeginVerification writes it into current_run the same way it writes
// WorktreePath, and it disappears whenever the rest of the Run does — on a
// PASS/FAIL result and on reclaiming an orphan alike. Nothing here touches the
// filesystem; the value is only ever passed in and read back.
func TestLogPathSurvivesTheRunAndVanishesWithIt(t *testing.T) {
	state, now := verifiableState(t)
	if err := state.BeginVerification("WI-001", "abc123", "/tmp/wt", "/forgepilot/logs/WI-001-abc123-1.log", now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItems[0].CurrentRun.LogPath; got != "/forgepilot/logs/WI-001-abc123-1.log" {
		t.Fatalf("LogPath = %q, want the path passed to BeginVerification", got)
	}
	if _, err := state.RecordVerification("WI-001", "abc123", "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
	if state.WorkItems[0].CurrentRun != nil {
		t.Fatalf("LogPath outlived its run: %#v", state.WorkItems[0].CurrentRun)
	}

	if err := state.BeginVerification("WI-001", "def456", "/tmp/wt", "/forgepilot/logs/WI-001-def456-2.log", now); err != nil {
		t.Fatal(err)
	}
	evidence, _, reclaimedLogPath, found, err := state.ReclaimRun("WI-001", "make verify", now)
	if err != nil || !found {
		t.Fatalf("ReclaimRun = %#v, %v, %v", evidence, found, err)
	}
	if reclaimedLogPath != "/forgepilot/logs/WI-001-def456-2.log" {
		t.Fatalf("ReclaimRun log path = %q, want the path state recorded", reclaimedLogPath)
	}
	if state.WorkItems[0].CurrentRun != nil {
		t.Fatalf("LogPath outlived an interrupted run: %#v", state.WorkItems[0].CurrentRun)
	}

	// A run may also begin with no log path recorded — that is the honest state
	// for a run whose output was never streamed anywhere, not an error.
	if err := state.BeginVerification("WI-001", "ghi789", "/tmp/wt", "", now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItems[0].CurrentRun.LogPath; got != "" {
		t.Fatalf("LogPath = %q, want empty when none was given", got)
	}
}
