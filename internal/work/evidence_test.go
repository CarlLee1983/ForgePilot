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
