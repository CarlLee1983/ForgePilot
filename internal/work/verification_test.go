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
	pass, err := state.RecordVerification("WI-001", "abc123", "make verify", 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if pass.ID != "EV-001" || pass.Result != Pass || pass.Type != VerificationEvidence {
		t.Fatalf("evidence = %#v", pass)
	}
	if pass.Revision != "abc123" || pass.Command != "make verify" || pass.ExitCode != 0 {
		t.Fatalf("evidence lost the run's identity: %#v", pass)
	}
	if pass.WorkItemID != "WI-001" || pass.StoryRef != "specs/stories/a" || pass.Repository != "/repo" {
		t.Fatalf("evidence lost its bindings: %#v", pass)
	}
	if state.WorkItems[0].Status != Review {
		t.Fatalf("PASS left work as %s, want REVIEW", state.WorkItems[0].Status)
	}

	fail, err := state.RecordVerification("WI-001", "def456", "make verify", 2, now)
	if err != nil {
		t.Fatal(err)
	}
	if fail.ID != "EV-002" || fail.Result != Fail || fail.ExitCode != 2 {
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
	orphaned := state
	orphaned.Evidence = []Evidence{{ID: "EV-001", Type: VerificationEvidence, WorkItemID: "WI-404", Revision: "abc", Result: Pass}}
	if err := orphaned.Validate(); err == nil {
		t.Fatal("accepted evidence for an unknown work item")
	}
	unresulted := state
	unresulted.Evidence = []Evidence{{ID: "EV-001", Type: VerificationEvidence, WorkItemID: "WI-001", Revision: "abc", Result: "MAYBE"}}
	if err := unresulted.Validate(); err == nil {
		t.Fatal("accepted evidence with an unknown result")
	}
}
