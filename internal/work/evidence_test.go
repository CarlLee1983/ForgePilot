package work

import (
	"encoding/json"
	"slices"
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

func TestRuntimeValidationAllowsLegacyVerificationButRejectsReviewOrBlankMetadata(t *testing.T) {
	zero := 0
	verification := Evidence{ID: "EV-001", Type: VerificationEvidence, Repository: "/repo",
		WorkItemID: "WI-001", StoryRef: "specs/stories/a", Revision: "abc123", CandidateKind: CommitCandidate,
		Command: "make verify", ExitCode: &zero, Result: Pass, CreatedAt: time.Now().UTC()}
	items := map[string]Item{"WI-001": {ID: "WI-001"}}
	if err := validateEvidence([]Evidence{verification}, 2, items); err != nil {
		t.Fatalf("legacy verification without runtime = %v", err)
	}
	unsupported := verification
	unsupported.Runtime = map[string]string{"os": "14.0"}
	if err := validateEvidence([]Evidence{unsupported}, 2, items); err == nil {
		t.Fatal("accepted unsupported runtime metadata")
	}
	blankValue := verification
	blankValue.Runtime = map[string]string{"go": "\t"}
	if err := validateEvidence([]Evidence{blankValue}, 2, items); err == nil {
		t.Fatal("accepted runtime with blank value")
	}
	prerelease := verification
	prerelease.Runtime = map[string]string{"go": "1.25rc1", "node": "24.0.0-rc.1+build.2", "python": "3.13.0rc1", "rust": "1.86.0-nightly"}
	if err := validateEvidence([]Evidence{prerelease}, 2, items); err != nil {
		t.Fatalf("rejected normalized prerelease runtime: %v", err)
	}
	review := Evidence{ID: "EV-001", Type: ReviewEvidence, Repository: "/repo", WorkItemID: "WI-001",
		StoryRef: "specs/stories/a", Revision: "abc123", CandidateKind: CommitCandidate, Result: Approved,
		Reviewer: "carl@example.com", Runtime: map[string]string{"go": "1.25.5"}, CreatedAt: time.Now().UTC()}
	if err := validateEvidence([]Evidence{review}, 2, items); err == nil {
		t.Fatal("accepted review evidence carrying runtime")
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

	if _, err := state.RecordReview("WI-001", revision, Approved, "", "", "", now); err == nil {
		t.Fatal("recorded a review with no reviewer")
	}
	if _, err := state.RecordReview("WI-001", "", Approved, "carl@example.com", "", "", now); err == nil {
		t.Fatal("recorded a review with no revision")
	}
	if _, err := state.RecordReview("WI-001", revision, Rejected, "carl@example.com", "", "", now); err == nil {
		t.Fatal("recorded a rejection with no reason")
	}
	if _, err := state.RecordReview("WI-001", revision, Pass, "carl@example.com", "", "", now); err == nil {
		t.Fatal("recorded a verification result as a review")
	}
	if _, err := state.RecordReview("WI-002", revision, Approved, "carl@example.com", "", "", now); err == nil {
		t.Fatal("reviewed work that is not in REVIEW")
	}
	if len(state.Evidence) != 1 {
		t.Fatalf("a refused review left evidence: %#v", state.Evidence)
	}

	evidence, err := state.RecordReview("WI-001", revision, Approved, "carl@example.com", "reads correct", "", now)
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
	evidence, err := state.RecordReview("WI-001", revision, Rejected, "carl@example.com", "the error path is unhandled", "", now)
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

// approveAt drives a Work Item that is already in REVIEW through an approval at
// the given revision.
func approveAt(t *testing.T, state *State, id, revision string, now time.Time) {
	t.Helper()
	if _, err := state.RecordReview(id, revision, Approved, "carl@example.com", "", "", now); err != nil {
		t.Fatal(err)
	}
}

func TestApprovalCompletesWorkAndUnlocksDependents(t *testing.T) {
	state, now, revision := reviewFixture(t)
	// A third item that depends on both, so unlocking has something to refuse.
	if _, err := state.AddWork("g", "specs/stories/c", []string{"WI-001", "WI-002"}, now); err != nil {
		t.Fatal(err)
	}

	approveAt(t, &state, "WI-001", revision, now)
	if got := state.WorkItemStatus("WI-001"); got != Done {
		t.Fatalf("status after approval = %s, want DONE", got)
	}
	if blockers := state.CompletionBlockers("WI-001"); len(blockers) != 0 {
		t.Fatalf("completed work still reports blockers: %v", blockers)
	}
	// Every dependency of WI-002 is now DONE, so it is unlocked in the same
	// transaction. WI-003 still waits on WI-002 and must not be.
	if got := state.WorkItemStatus("WI-002"); got != Ready {
		t.Fatalf("status of the unlocked dependent = %s, want READY", got)
	}
	if got := state.WorkItemStatus("WI-003"); got != Pending {
		t.Fatalf("status of the still-blocked dependent = %s, want PENDING", got)
	}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}

	// DONE is terminal: nothing reviews, verifies or restarts it again.
	if _, err := state.RecordReview("WI-001", revision, Rejected, "carl@example.com", "changed my mind", "", now); err == nil {
		t.Fatal("reviewed completed work")
	}
	if err := state.Verifiable("WI-001"); err == nil {
		t.Fatal("verified completed work")
	}
	if err := state.Start("WI-001", now); err == nil {
		t.Fatal("restarted completed work")
	}
}

func TestCompletionRequiresAPassAtTheApprovedRevision(t *testing.T) {
	state, now, revision := reviewFixture(t)
	other := "2222222222222222222222222222222222222222"

	// Approving at a revision the PASS does not cover records the review but
	// completes nothing, and says why.
	approveAt(t, &state, "WI-001", other, now)
	if got := state.WorkItemStatus("WI-001"); got != Review {
		t.Fatalf("status = %s, want REVIEW when the approval names another revision", got)
	}
	blockers := state.CompletionBlockers("WI-001")
	if len(blockers) == 0 {
		t.Fatal("no reason given for work that was approved but not completed")
	}
	if !strings.Contains(strings.Join(blockers, "; "), "revision") {
		t.Fatalf("blockers %v do not explain the revision mismatch", blockers)
	}
	if got := state.WorkItemStatus("WI-002"); got != Pending {
		t.Fatalf("a dependent was unlocked without a completion: %s", got)
	}

	// Verifying that revision and approving it again does complete the work.
	if err := state.BeginVerification("WI-001", other, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerification("WI-001", other, "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
	approveAt(t, &state, "WI-001", other, now)
	if got := state.WorkItemStatus("WI-001"); got != Done {
		t.Fatalf("status = %s, want DONE", got)
	}
	_ = revision
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
	if _, err := state.RecordReview("WI-001", revision, Approved, "carl@example.com", "", "", now); err == nil {
		t.Fatal("approved work whose latest verification failed")
	}

	// A newer REJECTED on the same revision beats the older APPROVED. Getting
	// back to REVIEW takes a fresh PASS, and the rejection then holds.
	if err := state.BeginVerification("WI-001", revision, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerification("WI-001", revision, "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordReview("WI-001", revision, Rejected, "carl@example.com", "found a leak", "", now); err != nil {
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

func TestOpenGatesAndInactiveGoalsBlockCompletion(t *testing.T) {
	state, now, revision := reviewFixture(t)
	if _, err := state.OpenGate("WI-001", "Which cache?", []string{"redis", "in-process"}, "", now); err != nil {
		t.Fatal(err)
	}
	approveAt(t, &state, "WI-001", revision, now)
	if got := state.WorkItemStatus("WI-001"); got != Review {
		t.Fatalf("status = %s, want REVIEW while a gate is open", got)
	}
	if !strings.Contains(strings.Join(state.CompletionBlockers("WI-001"), "; "), "gate") {
		t.Fatalf("blockers %v do not name the open gate", state.CompletionBlockers("WI-001"))
	}
	if err := state.ResolveGate("GATE-001", "redis", "", "carl@example.com", now); err != nil {
		t.Fatal(err)
	}

	// A Goal that is not ACTIVE blocks reaching DONE, but not recording what
	// already happened.
	state.Goals[0].Status = GoalBlocked
	approveAt(t, &state, "WI-001", revision, now)
	if got := state.WorkItemStatus("WI-001"); got != Review {
		t.Fatalf("status = %s, want REVIEW while the goal is not active", got)
	}
	if !strings.Contains(strings.Join(state.CompletionBlockers("WI-001"), "; "), "goal") {
		t.Fatalf("blockers %v do not name the inactive goal", state.CompletionBlockers("WI-001"))
	}

	state.Goals[0].Status = GoalActive
	approveAt(t, &state, "WI-001", revision, now)
	if got := state.WorkItemStatus("WI-001"); got != Done {
		t.Fatalf("status = %s, want DONE once every condition holds", got)
	}
}

// TestCompletedWorkIsNeverStale keeps a warning that implies no action from
// accumulating until the user starts ignoring every warning.
func TestCompletedWorkIsNeverStale(t *testing.T) {
	state, now, revision := reviewFixture(t)
	approveAt(t, &state, "WI-001", revision, now)
	if state.Stale("WI-001", "3333333333333333333333333333333333333333") {
		t.Fatal("completed work was marked stale against a later revision")
	}
	latest, ok := state.LatestVerification("WI-001")
	if !ok || latest.Revision != revision {
		t.Fatalf("completed work no longer shows the revision it finished on: %#v", latest)
	}
}

// TestV4EvidenceCarriesEmptyPRReference pins the field schema v4 adds without
// yet writing to it. PR Reference belongs to Human Review alone: Verification
// Evidence serialises it empty and is refused if it carries one, for the same
// reason a verification may not carry a reviewer — one kind of Evidence must
// never be readable as the other.
func TestV4EvidenceCarriesEmptyPRReference(t *testing.T) {
	zero := 0
	verification := Evidence{ID: "EV-001", Type: VerificationEvidence, Repository: "/repo",
		WorkItemID: "WI-001", StoryRef: "specs/stories/a", Revision: "abc123", CandidateKind: CommitCandidate,
		Command: "make verify", ExitCode: &zero, Result: Pass, CreatedAt: time.Now().UTC()}
	encoded, err := json.Marshal(verification)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"pr":""`) {
		t.Fatalf("evidence %s lacks an empty pr field", encoded)
	}

	items := map[string]Item{"WI-001": {ID: "WI-001"}}
	if err := validateEvidence([]Evidence{verification}, 2, items); err != nil {
		t.Fatal(err)
	}
	withPR := verification
	withPR.PR = "carl/forgepilot#123"
	if err := validateEvidence([]Evidence{withPR}, 2, items); err == nil {
		t.Fatal("accepted verification evidence carrying a PR reference")
	}
}

// TestReviewRecordsThePullRequestItHappenedOn covers the optional PR Reference:
// present it is stored beside the exact revision, absent the review is exactly
// what M3 recorded.
func TestReviewRecordsThePullRequestItHappenedOn(t *testing.T) {
	state, now, revision := reviewFixture(t)
	evidence, err := state.RecordReview("WI-001", revision, Approved, "carl@example.com", "", "carl/forgepilot#123", now)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.PR != "carl/forgepilot#123" || evidence.Revision != revision {
		t.Fatalf("review did not bind the PR to the revision: %#v", evidence)
	}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}

	bare, _, bareRevision := reviewFixture(t)
	plain, err := bare.RecordReview("WI-001", bareRevision, Approved, "carl@example.com", "", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if plain.PR != "" {
		t.Fatalf("a review without --pr carries one anyway: %#v", plain)
	}
}

// TestPRReferenceMustBeOwnerNameNumber pins the single accepted form. Only one
// form is accepted so that two records pointing at the same pull request cannot
// be written differently, which would make them incomparable.
func TestPRReferenceMustBeOwnerNameNumber(t *testing.T) {
	// Case is preserved and accepted as written: a user copying the name off the
	// pull request page must not be refused. That two spellings of one pull
	// request stay distinguishable is the known cost.
	for _, reference := range []string{"carl/forgepilot#1", "carl/forgepilot#123", "a-b.c_d/e-f.g_h#9", "Carl/ForgePilot#7"} {
		state, now, revision := reviewFixture(t)
		if _, err := state.RecordReview("WI-001", revision, Approved, "carl@example.com", "", reference, now); err != nil {
			t.Fatalf("rejected %q: %v", reference, err)
		}
	}
	rejected := []string{
		"https://github.com/CarlLee1983/ForgePilot/pull/123",
		"github.com/CarlLee1983/ForgePilot#123",
		"carl/forgepilot/123",
		"carl/forgepilot",
		"#123",
		"carl#123",
		"/forgepilot#123",
		"carl/#123",
		"carl/forgepilot#0",
		"carl/forgepilot#007",
		"carl/forgepilot#-1",
		"carl/forgepilot#12a",
		" carl/forgepilot#123",
		"carl/forgepilot#123 ",
		"carl/forge pilot#123",
		"carl/forgepilot#123#4",
		"carl/forgepilot#1\ncarl/evil#2",
		"carl/forgepilot#1\n",
		"../..#1",
		".github/.#1",
		"-carl/forgepilot#1",
		"carl/-forgepilot#1",
		"carl/forgepilot#" + strings.Repeat("1", 300),
		strings.Repeat("a", 300) + "/b#1",
	}
	for _, reference := range rejected {
		state, now, revision := reviewFixture(t)
		if _, err := state.RecordReview("WI-001", revision, Approved, "carl@example.com", "", reference, now); err == nil {
			t.Fatalf("accepted %q", reference)
		}
		// A malformed reference is invalid input, not a review with a bad
		// outcome: nothing may be written.
		if len(state.Evidence) != 1 {
			t.Fatalf("a refused review still appended evidence for %q: %#v", reference, state.Evidence)
		}
	}
}

// TestLoadedEvidenceWithAMalformedPRIsRejected keeps the rule in the domain
// rather than at the CLI: a hand-edited state.json must not smuggle a value the
// command line would have refused.
func TestLoadedEvidenceWithAMalformedPRIsRejected(t *testing.T) {
	items := map[string]Item{"WI-001": {ID: "WI-001"}}
	review := Evidence{ID: "EV-001", Type: ReviewEvidence, Repository: "/repo",
		WorkItemID: "WI-001", StoryRef: "specs/stories/a", Revision: "abc123", CandidateKind: CommitCandidate,
		Result: Approved, Reviewer: "carl@example.com", CreatedAt: time.Now().UTC()}
	if err := validateEvidence([]Evidence{review}, 2, items); err != nil {
		t.Fatal(err)
	}
	good := review
	good.PR = "carl/forgepilot#123"
	if err := validateEvidence([]Evidence{good}, 2, items); err != nil {
		t.Fatal(err)
	}
	bad := review
	bad.PR = "https://github.com/CarlLee1983/ForgePilot/pull/123"
	if err := validateEvidence([]Evidence{bad}, 2, items); err == nil {
		t.Fatal("accepted a review carrying a malformed PR reference")
	}
}

// TestPRReferenceChangesNothingAboutCompletionOrStaleness is the executable form
// of ADR-0011. The same commit is the same code whatever pull request it was
// read under, so the completion conditions and the stale rule must give the same
// answer with a PR Reference as without one — and two reviews of one revision
// must still replace each other rather than form separate tracks per PR.
func TestPRReferenceChangesNothingAboutCompletionOrStaleness(t *testing.T) {
	withPR, now, revision := reviewFixture(t)
	without, _, _ := reviewFixture(t)

	if got, want := withPR.CompletionBlockers("WI-001"), without.CompletionBlockers("WI-001"); !slices.Equal(got, want) {
		t.Fatalf("blockers differ before review: %v vs %v", got, want)
	}
	if withPR.Stale("WI-001", "other") != without.Stale("WI-001", "other") {
		t.Fatal("staleness differs before review")
	}

	if _, err := withPR.RecordReview("WI-001", revision, Approved, "carl@example.com", "", "carl/forgepilot#123", now); err != nil {
		t.Fatal(err)
	}
	if _, err := without.RecordReview("WI-001", revision, Approved, "carl@example.com", "", "", now); err != nil {
		t.Fatal(err)
	}
	if withPR.WorkItemStatus("WI-001") != Done || without.WorkItemStatus("WI-001") != Done {
		t.Fatalf("completion differs: %s vs %s", withPR.WorkItemStatus("WI-001"), without.WorkItemStatus("WI-001"))
	}
	if withPR.Stale("WI-001", "other") != without.Stale("WI-001", "other") {
		t.Fatal("staleness differs after review")
	}

	if got, want := withPR.CompletionBlockers("WI-001"), without.CompletionBlockers("WI-001"); !slices.Equal(got, want) {
		t.Fatalf("blockers differ after review: %v vs %v", got, want)
	}

	// A later judgement on the same revision wins even when it names a different
	// pull request: the PR does not open a second, parallel review track. The
	// route runs through a rejection because DONE has no reopen (ADR-0006).
	replaced, _, replacedRevision := reviewFixture(t)
	if _, err := replaced.RecordReview("WI-001", replacedRevision, Rejected, "carl@example.com", "not yet", "carl/forgepilot#123", now); err != nil {
		t.Fatal(err)
	}
	if err := replaced.BeginVerification("WI-001", replacedRevision, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := replaced.RecordVerification("WI-001", replacedRevision, "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
	if _, err := replaced.RecordReview("WI-001", replacedRevision, Approved, "carl@example.com", "", "carl/forgepilot#456", now); err != nil {
		t.Fatal(err)
	}
	latest, ok := replaced.LatestReview("WI-001")
	if !ok || latest.PR != "carl/forgepilot#456" || latest.Result != Approved {
		t.Fatalf("the later review on the same revision did not win: %#v", latest)
	}
	if replaced.WorkItemStatus("WI-001") != Done {
		t.Fatalf("a review naming a different PR blocked completion: %s", replaced.WorkItemStatus("WI-001"))
	}
}
