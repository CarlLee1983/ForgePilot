package runner

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
)

func TestExactAuthorizationResumeUsesDurableBoundedStopReason(t *testing.T) {
	now := time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)
	base := Record{
		ExecutionAuthorizationDigest: "sha256:authorization",
		Deadline:                     now.Add(time.Hour),
		Budget:                       Budget{MaxSteps: 1, MaxAttemptsPerWork: 1},
		Steps:                        0,
		Attempts:                     map[string]int{"WI-001": 1},
		HumanWaits:                   map[string]int{"WI-001": 1},
	}
	base.Stop = &Stop{Reason: StopNeedsHuman, At: now}
	if !exactAuthorizationResumeReusable(base, base.ExecutionAuthorizationDigest, now) {
		t.Fatal("a confirmed human wait was treated as an exhausted exact-run contract")
	}
	for _, reason := range []StopReason{StopMaxSteps, StopMaxAttempts, StopMaxDuration} {
		base.Stop = &Stop{Reason: reason, At: now}
		if exactAuthorizationResumeReusable(base, base.ExecutionAuthorizationDigest, now.Add(-2*time.Hour)) {
			t.Fatalf("%s became exact-resumable after a clock rollback", reason)
		}
	}
}

func TestResumeAuthorizationCreatesSuccessorAfterAnchorMaxStepsWithCurrentAuthorization(t *testing.T) {
	root, runtimeCommand, _, now, _ := newUnresolvedExecutionRunnerFixture(t)
	identity := resolveExecutionIdentityForRunnerTest(t, root, now)
	options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
	options.Budget.MaxSteps = 1
	options.Budget.MaxDuration = time.Minute

	anchorRunner, err := newRunner(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := anchorRunner.loop(); err != nil {
		t.Fatal(err)
	}
	anchor := *anchorRunner.record
	if anchor.Stop == nil || anchor.Stop.Reason != StopMaxSteps {
		t.Fatalf("anchor stop = %#v; want MAX_STEPS", anchor.Stop)
	}
	anchorBefore, err := LoadRecord(root, anchor.RunID)
	if err != nil {
		t.Fatal(err)
	}

	// The Goal authorization is unchanged and the anchor is still within its
	// deadline. Authorization-level continuation must nevertheless create a
	// fresh run because the old run exhausted its own step contract.
	options.Now = func() time.Time { return now.Add(time.Second) }
	successor, err := ResumeAuthorization(options, anchor.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if successor.RunID == anchor.RunID {
		t.Fatalf("same-authorization exhausted anchor was resumed exactly: %#v", successor)
	}
	if successor.ExecutionAuthorizationDigest != anchor.ExecutionAuthorizationDigest || successor.Budget != anchor.Budget ||
		!successor.Deadline.After(anchor.Deadline) {
		t.Fatalf("successor did not preserve authorization and receive a fresh run contract: anchor=%#v successor=%#v", anchor, successor)
	}
	anchorAfter, err := LoadRecord(root, anchor.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !equalJSON(t, anchorBefore, anchorAfter) {
		t.Fatal("authorization-level continuation mutated the exhausted anchor Run Record")
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, ok := state.GoalByID(anchor.GoalID)
	if !ok || goal.Execution == nil || goal.Execution.Ledger.RunsConsumed != 2 {
		t.Fatalf("same-authorization continuation did not consume one additional run unit: %#v", goal)
	}
}

func TestResumeAuthorizationCreatesSuccessorAfterAnchorDeadline(t *testing.T) {
	root, runtimeCommand, _, now, _ := newUnresolvedExecutionRunnerFixture(t)
	identity := resolveExecutionIdentityForRunnerTest(t, root, now)
	options := chargedRunnerTestOptions(root, runtimeCommand, now, identity)
	options.Budget.MaxSteps = 1
	options.Budget.MaxDuration = time.Minute

	anchorRunner, err := newRunner(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := anchorRunner.loop(); err != nil {
		t.Fatal(err)
	}
	anchor := *anchorRunner.record
	if anchor.Stop == nil || anchor.Stop.Reason != StopMaxSteps {
		t.Fatalf("anchor stop = %#v; want MAX_STEPS", anchor.Stop)
	}
	anchorBefore, err := LoadRecord(root, anchor.RunID)
	if err != nil {
		t.Fatal(err)
	}
	runDirectory, err := storage.RunDirectory(root, anchor.RunID)
	if err != nil {
		t.Fatal(err)
	}
	anchorBytes, err := os.ReadFile(filepath.Join(runDirectory, recordName))
	if err != nil {
		t.Fatal(err)
	}

	options.Now = func() time.Time { return now.Add(2 * time.Minute) }
	successor, err := ResumeAuthorization(options, anchor.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if successor.RunID == anchor.RunID {
		t.Fatalf("expired anchor was resumed exactly: %#v", successor)
	}
	if successor.ExecutionAuthorizationDigest != anchor.ExecutionAuthorizationDigest {
		t.Fatalf("successor changed authorization digest: anchor=%q successor=%q", anchor.ExecutionAuthorizationDigest, successor.ExecutionAuthorizationDigest)
	}
	if successor.Budget != anchor.Budget || !successor.Deadline.After(anchor.Deadline) {
		t.Fatalf("successor did not receive a fresh run contract: anchor=%#v successor=%#v", anchor, successor)
	}

	anchorAfter, err := LoadRecord(root, anchor.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !equalJSON(t, anchorBefore, anchorAfter) {
		t.Fatal("authorization-level continuation mutated the anchor Run Record")
	}
	anchorAfterBytes, err := os.ReadFile(filepath.Join(runDirectory, recordName))
	if err != nil {
		t.Fatal(err)
	}
	if string(anchorBytes) != string(anchorAfterBytes) {
		t.Fatal("authorization-level continuation rewrote the anchor artifact")
	}
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	goal, ok := state.GoalByID(anchor.GoalID)
	if !ok || goal.Execution == nil || goal.Execution.Ledger.RunsConsumed != 2 {
		t.Fatalf("successor did not consume one additional cumulative run unit: %#v", goal)
	}
}
