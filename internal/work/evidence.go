package work

import "time"

// EvidenceType discriminates the kinds of Evidence a Work Item accumulates. Only
// verification exists in M2, but the field is written from the first release so
// that immutable history stays readable when Human Review Evidence arrives.
type EvidenceType string

const VerificationEvidence EvidenceType = "verification"

// Result is the outcome of a Verification Run. Interrupted means no result was
// produced; it must never be reported as a failure.
type Result string

const (
	Pass        Result = "PASS"
	Fail        Result = "FAIL"
	Interrupted Result = "INTERRUPTED"
)

// Evidence is an immutable record binding one outcome to one exact revision.
type Evidence struct {
	ID         string       `json:"id"`
	Type       EvidenceType `json:"type"`
	Repository string       `json:"repository"`
	WorkItemID string       `json:"work_item_id"`
	StoryRef   string       `json:"story_ref"`
	Revision   string       `json:"revision"`
	Command    string       `json:"command"`
	ExitCode   int          `json:"exit_code"`
	Result     Result       `json:"result"`
	CreatedAt  time.Time    `json:"created_at"`
}
