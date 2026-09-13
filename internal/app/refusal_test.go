package app

import (
	"errors"
	"fmt"
	"testing"
)

// A refusal must stay recognisable after being wrapped, and an ordinary
// operational failure must never be mistaken for one. The whole point of the
// distinction is that a refusal names a condition outside the code — a Gate, a
// stopped Goal, an unsatisfiable Runtime Contract — while an operational
// failure is ForgePilot's own problem. Treating either as a verification result
// would invent a FAIL the repository never produced.
func TestRefusalSurvivesWrappingAndDoesNotSwallowOtherFailures(t *testing.T) {
	cause := errors.New("work item \"WI-001\" is blocked by GATE-001")
	refusal := refuse(cause)
	if !IsRefusal(refusal) {
		t.Fatal("a refusal is not recognised as one")
	}
	if refusal.Error() != cause.Error() {
		t.Fatalf("refusal message = %q, want the original verbatim", refusal.Error())
	}
	if !errors.Is(refusal, cause) {
		t.Fatal("a refusal hides the error it was built from")
	}
	if !IsRefusal(fmt.Errorf("verify WI-001: %w", refusal)) {
		t.Fatal("a wrapped refusal is not recognised as one")
	}

	if refuse(nil) != nil {
		t.Fatal("refusing nothing produced an error")
	}
	operational := fmt.Errorf("git rev-parse HEAD: %w", errors.New("not a repository"))
	if IsRefusal(operational) {
		t.Fatal("an operational failure was classified as a refusal")
	}
	if IsRefusal(ErrVerificationTimedOut) {
		t.Fatal("a timeout was classified as a refusal; it is neither that nor a FAIL")
	}
}
