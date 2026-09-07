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
		WorkItemID: "WI-001", StoryRef: "specs/stories/a", Revision: "abc123",
		Command: "make verify", ExitCode: &zero, Result: Pass, CreatedAt: time.Now().UTC()}
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

// reviewFixture returns state with WI-001 verified PASS and sitting in REVIEW,
// plus WI-002 depending on it.
func reviewFixture(t *testing.T) (State, time.Time, string) {
	t.Helper()
	now := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	revision := "1111111111111111111111111111111111111111"
	state := NewState()
	if err := state.AddGoal("g", "Goal", "", "/repo", now); err != nil {
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
	if err := state.BeginVerification(first.ID, revision, "/tmp/worktree", now); err != nil {
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
