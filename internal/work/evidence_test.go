package work

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestV3EvidenceCarriesEmptyReviewFields pins the containers schema v3 adds
// without yet writing to them: Verification Evidence must serialise the review
// fields as empty, and must be refused if it carries one.
func TestV3EvidenceCarriesEmptyReviewFields(t *testing.T) {
	zero := 0
	verification := Evidence{ID: "EV-001", Type: VerificationEvidence, Repository: "/repo",
		WorkItemID: "WI-001", StoryRef: "specs/stories/a", Revision: "abc123", CandidateKind: CommitCandidate,
		Command: "make verify", ExitCode: &zero, Result: Pass, VerificationRunID: "VR-001", CreatedAt: time.Now().UTC()}
	encoded, err := json.Marshal(verification)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"reviewer":""`, `"note":""`} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("evidence %s lacks %s", encoded, field)
		}
	}

	items := map[string]Item{"WI-001": {ID: "WI-001"}}
	if err := validateEvidence([]Evidence{verification}, 2, items); err != nil {
		t.Fatal(err)
	}
	withReviewer := verification
	withReviewer.Reviewer = "carl@example.com"
	if err := validateEvidence([]Evidence{withReviewer}, 2, items); err == nil {
		t.Fatal("accepted verification evidence carrying a reviewer")
	}
	withNote := verification
	withNote.Note = "looks fine"
	if err := validateEvidence([]Evidence{withNote}, 2, items); err == nil {
		t.Fatal("accepted verification evidence carrying a review note")
	}
}

// The v19 shape has no PR on Evidence and no policy fields on Goal: a Goal says
// only whether it requires approval.
func TestV19ShapeCarriesNoPRReviewPolicyOrCompletionProvenance(t *testing.T) {
	zero := 0
	evidence, err := json.Marshal(Evidence{ExitCode: &zero})
	if err != nil {
		t.Fatal(err)
	}
	goal, err := json.Marshal(Goal{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := json.Marshal(NewState())
	if err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{`"pr"`, `"review_policy"`, `"completion_policy"`, `"goal_completion_evidence"`, `"next_goal_completion_evidence_id"`} {
		for name, encoded := range map[string][]byte{"evidence": evidence, "goal": goal, "state": state} {
			if strings.Contains(string(encoded), removed) {
				t.Errorf("%s still serialises %s: %s", name, removed, encoded)
			}
		}
	}
	if !strings.Contains(string(goal), `"require_approval":false`) {
		t.Errorf("goal does not serialise its Approval Requirement: %s", goal)
	}
}

func TestStateValidationRejectsFalseEvidenceRepositoryOrStoryAssociation(t *testing.T) {
	now := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	state := NewState()
	if err := state.AddGoalRequiringApproval("g", "Goal", "", "/repo", now); err != nil {
		t.Fatal(err)
	}
	item, err := state.AddWork("g", "specs/stories/a", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Start(item.ID, now); err != nil {
		t.Fatal(err)
	}
	revision := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := state.BeginVerification(item.ID, revision, "/tmp/worktree", "/tmp/log", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerification(item.ID, revision, "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
	state.Evidence[0].Repository = "/other"
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "repository") {
		t.Fatalf("repository mismatch validation = %v", err)
	}
	state.Evidence[0].Repository = "/repo"
	state.Evidence[0].StoryRef = "specs/stories/other"
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "story reference") {
		t.Fatalf("story mismatch validation = %v", err)
	}
}

// A Verification Run ID keys that run's log, so one ID may belong to exactly one
// run: two Work Items sharing it, or one run being both active and settled,
// would share a log.
func TestVerificationRunIDsAreUsedExactlyOnce(t *testing.T) {
	now := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	zero := 0
	build := func() State {
		return State{
			SchemaVersion: SchemaVersion, NextEvidenceID: 3, NextGateID: 1, NextVerificationRunID: 3,
			Goals: []Goal{{ID: "g", Title: "Goal", Repository: "/repo", Status: GoalActive}},
			WorkItems: []Item{
				{ID: "WI-001", GoalID: "g", StoryRef: "specs/stories/a", Status: Done},
				{ID: "WI-002", GoalID: "g", StoryRef: "specs/stories/b", Status: Done},
			},
			Evidence: []Evidence{
				{ID: "EV-001", Type: VerificationEvidence, Repository: "/repo", WorkItemID: "WI-001", StoryRef: "specs/stories/a", Revision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CandidateKind: CommitCandidate, Command: "make verify", ExitCode: &zero, Result: Pass, VerificationRunID: "VR-001", CreatedAt: now},
				{ID: "EV-002", Type: VerificationEvidence, Repository: "/repo", WorkItemID: "WI-002", StoryRef: "specs/stories/b", Revision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CandidateKind: CommitCandidate, Command: "make verify", ExitCode: &zero, Result: Pass, VerificationRunID: "VR-002", CreatedAt: now},
			},
		}
	}
	if err := build().Validate(); err != nil {
		t.Fatalf("distinct run IDs = %v", err)
	}
	t.Run("two work items share a run", func(t *testing.T) {
		state := build()
		state.Evidence[1].VerificationRunID = "VR-001"
		if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "used more than once") {
			t.Fatalf("validation = %v, want a shared-run refusal", err)
		}
	})
	t.Run("active and settled", func(t *testing.T) {
		state := build()
		state.WorkItems[0].Status = Verifying
		state.WorkItems[0].CurrentRun = &Run{VerificationRunID: "VR-001", Revision: state.Evidence[0].Revision, CandidateKind: CommitCandidate}
		if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "used more than once") {
			t.Fatalf("validation = %v, want active/settled refusal", err)
		}
	})
	t.Run("former legacy run IDs are no longer a thing", func(t *testing.T) {
		state := build()
		state.Evidence[0].VerificationRunID = "LVR-001"
		if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "invalid verification run ID") {
			t.Fatalf("validation = %v, want an invalid run ID refusal", err)
		}
	})
	t.Run("counter reuse", func(t *testing.T) {
		state := build()
		state.NextVerificationRunID = 2
		if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "would reuse") {
			t.Fatalf("validation = %v, want counter refusal", err)
		}
	})
}

// reviewFixture returns state under a Goal that requires approval, with WI-001
// passed into REVIEW and WI-002 depending on it.
func reviewFixture(t *testing.T) (State, time.Time, string) {
	t.Helper()
	now := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	revision := "1111111111111111111111111111111111111111"
	state := NewState()
	if err := state.AddGoalRequiringApproval("g", "Goal", "", "/repo", now); err != nil {
		t.Fatal(err)
	}
	first, err := state.AddWork("g", "specs/stories/a", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.AddWork("g", "specs/stories/b", []string{first.ID}, now); err != nil {
		t.Fatal(err)
	}
	if err := state.Start(first.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginVerification(first.ID, revision, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerification(first.ID, revision, "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
	if state.WorkItemStatus(first.ID) != Review {
		t.Fatalf("fixture did not reach REVIEW: %s", state.WorkItemStatus(first.ID))
	}
	return state, now, revision
}

func TestRecordReviewBindsAJudgementToAnExactRevision(t *testing.T) {
	state, now, revision := reviewFixture(t)

	if _, err := state.RecordReview("WI-001", revision, Approved, "", "", now); err == nil {
		t.Fatal("recorded a review with no reviewer")
	}
	if _, err := state.RecordReview("WI-001", "", Approved, "carl@example.com", "", now); err == nil {
		t.Fatal("recorded a review with no revision")
	}
	if _, err := state.RecordReview("WI-001", revision, Rejected, "carl@example.com", "", now); err == nil {
		t.Fatal("recorded a rejection with no reason")
	}
	if _, err := state.RecordReview("WI-001", revision, Pass, "carl@example.com", "", now); err == nil {
		t.Fatal("recorded a verification result as a review")
	}
	if _, err := state.RecordReview("WI-002", revision, Approved, "carl@example.com", "", now); err == nil {
		t.Fatal("reviewed work that is not in REVIEW")
	}
	if len(state.Evidence) != 1 {
		t.Fatalf("a refused review left evidence: %#v", state.Evidence)
	}

	evidence, err := state.RecordReview("WI-001", revision, Approved, "carl@example.com", "reads correct", now)
	if err != nil {
		t.Fatal(err)
	}
	// Review and verification share one ID sequence, so a Work Item's history
	// reads as a single timeline.
	if evidence.ID != "EV-002" || evidence.Type != ReviewEvidence {
		t.Fatalf("evidence = %#v", evidence)
	}
	if evidence.Repository != "/repo" || evidence.StoryRef != "specs/stories/a" || evidence.Revision != revision {
		t.Fatalf("review evidence is not bound to the work it reviewed: %#v", evidence)
	}
	if evidence.Reviewer != "carl@example.com" || evidence.Note != "reads correct" {
		t.Fatalf("review evidence lost the reviewer or note: %#v", evidence)
	}
	if evidence.ExitCode != nil || evidence.Command != "" {
		t.Fatalf("review evidence carries verification fields: %#v", evidence)
	}
	if state.Evidence[0].Result != Pass {
		t.Fatal("the earlier verification evidence was overwritten")
	}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRejectionReturnsWorkToRunning(t *testing.T) {
	state, now, revision := reviewFixture(t)
	evidence, err := state.RecordReview("WI-001", revision, Rejected, "carl@example.com", "the error path is unhandled", now)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Result != Rejected || evidence.Note != "the error path is unhandled" {
		t.Fatalf("evidence = %#v", evidence)
	}
	if got := state.WorkItemStatus("WI-001"); got != Running {
		t.Fatalf("status after REJECTED = %s, want RUNNING", got)
	}
	latest, ok := state.LatestReview("WI-001")
	if !ok || latest.ID != evidence.ID {
		t.Fatalf("LatestReview = %#v, %v", latest, ok)
	}
	// The rejection did not disturb the verification history.
	verification, ok := state.LatestVerification("WI-001")
	if !ok || verification.Result != Pass {
		t.Fatalf("LatestVerification = %#v, %v", verification, ok)
	}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestApprovalCompletesWorkAndUnlocksDependents(t *testing.T) {
	state, now, revision := reviewFixture(t)
	// A third item that depends on both, so unlocking has something to refuse.
	if _, err := state.AddWork("g", "specs/stories/c", []string{"WI-001", "WI-002"}, now); err != nil {
		t.Fatal(err)
	}

	if _, err := state.RecordReview("WI-001", revision, Approved, "carl@example.com", "", now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus("WI-001"); got != Done {
		t.Fatalf("status after approval = %s, want DONE", got)
	}
	// Every dependency of WI-002 is now DONE, so it is unlocked in the same
	// transaction. WI-003 still waits on WI-002 and must not be.
	if got := state.DisplayStatus("WI-002"); got != "READY" {
		t.Fatalf("status of the unlocked dependent = %s, want READY", got)
	}
	if got := state.DisplayStatus("WI-003"); got != "PENDING" {
		t.Fatalf("status of the still-blocked dependent = %s, want PENDING", got)
	}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}

	// DONE is terminal: nothing reviews, verifies or restarts it again.
	if _, err := state.RecordReview("WI-001", revision, Rejected, "carl@example.com", "changed my mind", now); err == nil {
		t.Fatal("reviewed completed work")
	}
	if err := state.Verifiable("WI-001"); err == nil {
		t.Fatal("verified completed work")
	}
	if err := state.Start("WI-001", now); err == nil {
		t.Fatal("restarted completed work")
	}
}

// TestTheLatestResultWinsOnTheSameRevision is what makes re-running a check
// worth doing: a later FAIL or REJECTED overrides the earlier good result rather
// than being outvoted by it.
func TestTheLatestResultWinsOnTheSameRevision(t *testing.T) {
	state, now, revision := reviewFixture(t)

	// A newer FAIL on the same revision beats the older PASS: the FAIL returns
	// the work to RUNNING, so there is nothing to approve.
	if err := state.BeginVerification("WI-001", revision, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerification("WI-001", revision, "make verify", 1, now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus("WI-001"); got != Running {
		t.Fatalf("status after FAIL = %s, want RUNNING", got)
	}
	if _, err := state.RecordReview("WI-001", revision, Approved, "carl@example.com", "", now); err == nil {
		t.Fatal("approved work whose latest verification failed")
	}

	// A newer REJECTED on the same revision beats the older PASS. Getting back to
	// REVIEW takes a fresh PASS, and the rejection then holds.
	if err := state.BeginVerification("WI-001", revision, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerification("WI-001", revision, "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus("WI-001"); got != Review {
		t.Fatalf("status after the fresh PASS = %s, want REVIEW", got)
	}
	if _, err := state.RecordReview("WI-001", revision, Rejected, "carl@example.com", "found a leak", now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus("WI-001"); got != Running {
		t.Fatalf("status after REJECTED = %s, want RUNNING", got)
	}
	latest, ok := state.LatestReview("WI-001")
	if !ok || latest.Result != Rejected {
		t.Fatalf("LatestReview = %#v, %v, want the newer REJECTED", latest, ok)
	}
}

// A Goal that is no longer ACTIVE has nothing left to approve.
func TestReviewIsRefusedUnderACancelledGoal(t *testing.T) {
	state, now, revision := reviewFixture(t)
	if err := state.CancelGoal("g", "dropped", now); err != nil {
		t.Fatal(err)
	}
	for _, result := range []Result{Approved, Rejected} {
		if _, err := state.RecordReview("WI-001", revision, result, "carl@example.com", "because", now); err == nil || !strings.Contains(err.Error(), "active goal") {
			t.Fatalf("%s under a cancelled goal = %v", result, err)
		}
	}
}

// TestCompletedWorkIsNeverStale keeps a warning that implies no action from
// accumulating until the user starts ignoring every warning.
func TestCompletedWorkIsNeverStale(t *testing.T) {
	state, now, revision := reviewFixture(t)
	if _, err := state.RecordReview("WI-001", revision, Approved, "carl@example.com", "", now); err != nil {
		t.Fatal(err)
	}
	if state.Stale("WI-001", "3333333333333333333333333333333333333333") {
		t.Fatal("completed work was marked stale against a later revision")
	}
	latest, ok := state.LatestVerification("WI-001")
	if !ok || latest.Revision != revision {
		t.Fatalf("completed work no longer shows the revision it finished on: %#v", latest)
	}
}
