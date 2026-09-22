package work

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestGoalReviewPolicyDefaultsAndValidates(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	state := NewState()
	if err := state.AddGoal("default", "Default", "", "/repo", now); err != nil {
		t.Fatal(err)
	}
	if got := state.Goals[0].ReviewPolicy; got != ReviewPerWorkItem {
		t.Fatalf("default review policy = %q, want %q", got, ReviewPerWorkItem)
	}
	if err := state.AddGoalWithReviewPolicy("goal", "Goal", "", "/repo", ReviewPerGoal, now); err != nil {
		t.Fatal(err)
	}
	if got := state.Goals[1].ReviewPolicy; got != ReviewPerGoal {
		t.Fatalf("explicit review policy = %q, want %q", got, ReviewPerGoal)
	}
	if got := state.Goals[1].CompletionPolicy; got != CompletionVerified {
		t.Fatalf("GOAL completion policy = %q, want %q", got, CompletionVerified)
	}
	if err := state.AddGoalWithPolicies("manual", "Manual", "", "/repo", ReviewPerGoal, CompletionHuman, now); err == nil {
		t.Fatal("created a GOAL-policy Goal with manual final review")
	}
	if err := state.AddGoalWithReviewPolicy("invalid", "Invalid", "", "/repo", ReviewPolicy("BYPASS"), now); err == nil {
		t.Fatal("created a Goal with an invalid review policy")
	}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
	invalidPolicy := state
	invalidPolicy.Goals = append([]Goal(nil), state.Goals...)
	invalidPolicy.Goals[0].ReviewPolicy = ""
	if err := invalidPolicy.Validate(); err == nil {
		t.Fatal("validated a Goal without a durable review policy")
	}
	wrongStatus := NewState()
	if err := wrongStatus.AddGoal("work-item", "Work Item", "", "/repo", now); err != nil {
		t.Fatal(err)
	}
	if _, err := wrongStatus.AddWork("work-item", "specs/stories/one", nil, now); err != nil {
		t.Fatal(err)
	}
	wrongStatus.WorkItems[0].Status = Verified
	if err := wrongStatus.Validate(); err == nil {
		t.Fatal("validated VERIFIED work under WORK_ITEM review policy")
	}

	for _, impossible := range []struct {
		name   string
		status Status
	}{
		{name: "review", status: Review},
		{name: "done", status: Done},
	} {
		t.Run("goal policy rejects "+impossible.name, func(t *testing.T) {
			invalid := state
			invalid.Goals = append([]Goal(nil), state.Goals...)
			invalid.WorkItems = []Item{{ID: "WI-001", GoalID: "goal", StoryRef: "specs/stories/one", Status: impossible.status}}
			invalid.NextWorkID = 2
			if err := invalid.Validate(); err == nil {
				t.Fatalf("validated GOAL-policy work in %s", impossible.status)
			}
		})
	}

	completed := state
	completed.Goals = append([]Goal(nil), state.Goals...)
	completed.Goals[1].Status = GoalCompleted
	if err := completed.Validate(); err == nil {
		t.Fatal("validated a completed GOAL-policy Goal without completion evidence")
	}

	withReviewEvidence := state
	withReviewEvidence.Goals = append([]Goal(nil), state.Goals...)
	withReviewEvidence.WorkItems = []Item{{ID: "WI-001", GoalID: "goal", StoryRef: "specs/stories/one", Status: Verified}}
	withReviewEvidence.NextWorkID = 2
	withReviewEvidence.Evidence = []Evidence{{
		ID: "EV-001", Type: ReviewEvidence, Repository: "/repo", WorkItemID: "WI-001", StoryRef: "specs/stories/one",
		Revision: "1111111111111111111111111111111111111111", CandidateKind: CommitCandidate,
		Result: Approved, Reviewer: "human@example.com", CreatedAt: now,
	}}
	withReviewEvidence.NextEvidenceID = 2
	if err := withReviewEvidence.Validate(); err == nil {
		t.Fatal("validated Work Item review evidence under GOAL review policy")
	}
}

func TestValidateRequiresVerifiedWorkToHaveLatestPassEvidence(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 30, 0, 0, time.UTC)
	state := NewState()
	if err := state.AddGoalWithReviewPolicy("goal", "Goal", "", "/repo", ReviewPerGoal, now); err != nil {
		t.Fatal(err)
	}
	item, err := state.AddWork("goal", "specs/stories/one", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	state.WorkItems[0].Status = Verified
	if err := state.Validate(); err == nil {
		t.Fatal("validated VERIFIED work without Verification Evidence")
	}

	revision := "1111111111111111111111111111111111111111"
	exitCode := 0
	state.Evidence = []Evidence{{
		ID: "EV-001", Type: VerificationEvidence, Repository: "/repo", WorkItemID: item.ID, StoryRef: item.StoryRef,
		Revision: revision, CandidateKind: CommitCandidate, Command: "make verify", ExitCode: &exitCode, Result: Pass, VerificationRunID: "VR-001", CreatedAt: now,
	}}
	state.NextEvidenceID = 2
	state.NextVerificationRunID = 2
	if err := state.Validate(); err != nil {
		t.Fatalf("valid VERIFIED state: %v", err)
	}
	failCode := 1
	state.Evidence = append(state.Evidence, Evidence{
		ID: "EV-002", Type: VerificationEvidence, Repository: "/repo", WorkItemID: item.ID, StoryRef: item.StoryRef,
		Revision: revision, CandidateKind: CommitCandidate, Command: "make verify", ExitCode: &failCode, Result: Fail, VerificationRunID: "VR-002", CreatedAt: now,
	})
	state.NextEvidenceID = 3
	state.NextVerificationRunID = 3
	if err := state.Validate(); err == nil {
		t.Fatal("validated VERIFIED work whose latest Verification is FAIL")
	}
}

func TestFreshnessBearingRefreshControlsVerifiedDependencyPromotion(t *testing.T) {
	state, now, first, second, revision := goalReviewDependencyFixture(t)
	if _, err := state.RecordVerificationWithRepository(first.ID, revision, "make verify", 0, RepositoryState{Revision: revision}, now); err != nil {
		t.Fatal(err)
	}
	gate, err := state.OpenGate(first.ID, "Which path?", []string{"one", "two"}, "", now)
	if err != nil {
		t.Fatal(err)
	}
	stale := RepositoryState{Revision: "2222222222222222222222222222222222222222"}
	if err := state.ResolveGateWithRepository(gate.ID, "one", "", "human@example.com", stale, now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus(second.ID); got != Pending {
		t.Fatalf("stale Gate resolution promoted downstream work to %s", got)
	}
	added, err := state.AddWorkWithRepository("goal", "specs/stories/three", []string{first.ID}, stale, now)
	if err != nil {
		t.Fatal(err)
	}
	if added.Status != Pending {
		t.Fatalf("work added behind stale VERIFIED dependency = %s, want PENDING", added.Status)
	}

	state.BlockGoal("goal", "pause", now)
	if err := state.UnblockGoalWithRepository("goal", stale, now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus(second.ID); got != Pending {
		t.Fatalf("stale Goal unblock promoted downstream work to %s", got)
	}

	state.RefreshReadyWithRepository(RepositoryState{Revision: revision}, now)
	if got := state.WorkItemStatus(second.ID); got != Ready {
		t.Fatalf("fresh reconciliation left downstream work %s", got)
	}
}

func TestCancelGateRequiresFreshVerifiedDependencyToRepromote(t *testing.T) {
	state, now, first, second, revision := goalReviewDependencyFixture(t)
	if _, err := state.RecordVerificationWithRepository(first.ID, revision, "make verify", 0, RepositoryState{Revision: revision}, now); err != nil {
		t.Fatal(err)
	}
	gate, err := state.OpenGate(first.ID, "Which path?", []string{"one", "two"}, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.CancelGateWithRepository(gate.ID, "question withdrawn", "human@example.com", RepositoryState{Revision: revision}, now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus(second.ID); got != Ready {
		t.Fatalf("fresh Gate cancellation left downstream work %s", got)
	}
}

func TestWorkItemReviewPolicyKeepsReviewAndDependencyLifecycle(t *testing.T) {
	now := time.Date(2026, 9, 13, 3, 0, 0, 0, time.UTC)
	state := NewState()
	if err := state.AddGoal("goal", "Goal", "", "/repo", now); err != nil {
		t.Fatal(err)
	}
	first, err := state.AddWork("goal", "specs/stories/one", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := state.AddWork("goal", "specs/stories/two", []string{first.ID}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Start(first.ID, now); err != nil {
		t.Fatal(err)
	}
	revision := "1111111111111111111111111111111111111111"
	if err := state.BeginVerification(first.ID, revision, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerificationWithRepository(first.ID, revision, "make verify", 0, RepositoryState{Revision: revision}, now); err != nil {
		t.Fatal(err)
	}
	submitForReview(t, &state, first.ID, now)
	if state.WorkItemStatus(first.ID) != Review || state.WorkItemStatus(second.ID) != Pending {
		t.Fatalf("after PASS: first=%s second=%s", state.WorkItemStatus(first.ID), state.WorkItemStatus(second.ID))
	}
	if _, err := state.RecordReview(first.ID, revision, Approved, "human@example.com", "", "", now); err != nil {
		t.Fatal(err)
	}
	if state.WorkItemStatus(first.ID) != Done || state.WorkItemStatus(second.ID) != Ready {
		t.Fatalf("after approval: first=%s second=%s", state.WorkItemStatus(first.ID), state.WorkItemStatus(second.ID))
	}
}

func TestGoalReviewPolicyPassMachineAcceptsAndUnlocksDependency(t *testing.T) {
	now := time.Date(2026, 9, 13, 1, 0, 0, 0, time.UTC)
	state := NewState()
	if err := state.AddGoalWithReviewPolicy("goal", "Goal", "", "/repo", ReviewPerGoal, now); err != nil {
		t.Fatal(err)
	}
	first, err := state.AddWork("goal", "specs/stories/one", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := state.AddWork("goal", "specs/stories/two", []string{first.ID}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Start(first.ID, now); err != nil {
		t.Fatal(err)
	}
	revision := "1111111111111111111111111111111111111111"
	if err := state.BeginVerification(first.ID, revision, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerificationWithRepository(first.ID, revision, "make verify", 0, RepositoryState{Revision: revision}, now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus(first.ID); got != Verified {
		t.Fatalf("PASS left first work as %s, want VERIFIED", got)
	}
	if got := state.WorkItemStatus(second.ID); got != Ready {
		t.Fatalf("downstream work = %s, want READY", got)
	}
	if err := state.StartWithRepository(second.ID, RepositoryState{Revision: revision}, now); err != nil {
		t.Fatalf("could not start downstream work: %v", err)
	}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestGoalReviewPolicyGateStopsProgressionUntilResolved(t *testing.T) {
	state, now, first, second, revision := goalReviewDependencyFixture(t)
	gate, err := state.OpenGate(first.ID, "Which path?", []string{"one", "two"}, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerification(first.ID, revision, "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus(first.ID); got != Verified {
		t.Fatalf("PASS left first work as %s, want VERIFIED", got)
	}
	if got := state.WorkItemStatus(second.ID); got != Pending {
		t.Fatalf("open Gate was bypassed: downstream work = %s, want PENDING", got)
	}
	if err := state.ResolveGateWithRepository(gate.ID, "one", "", "human@example.com", RepositoryState{Revision: revision}, now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus(second.ID); got != Ready {
		t.Fatalf("resolved Gate left downstream work = %s, want READY", got)
	}
}

func TestGateOpenedAfterVerificationWithdrawsDependencyProgression(t *testing.T) {
	state, now, first, second, revision := goalReviewDependencyFixture(t)
	if _, err := state.RecordVerificationWithRepository(first.ID, revision, "make verify", 0, RepositoryState{Revision: revision}, now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus(second.ID); got != Ready {
		t.Fatalf("initial PASS left downstream work = %s, want READY", got)
	}
	if _, err := state.OpenGate(first.ID, "Which path?", []string{"one", "two"}, "", now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus(second.ID); got != Pending {
		t.Fatalf("open Gate left downstream work = %s, want PENDING", got)
	}
	if err := state.Start(second.ID, now); err == nil {
		t.Fatal("started downstream work while its VERIFIED dependency had an open Gate")
	}
}

func TestGoalReviewPolicyBlockedGoalStopsProgressionUntilUnblocked(t *testing.T) {
	state, now, first, second, revision := goalReviewDependencyFixture(t)
	if err := state.BlockGoal("goal", "waiting for direction", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerification(first.ID, revision, "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus(first.ID); got != Verified {
		t.Fatalf("PASS left first work as %s, want VERIFIED", got)
	}
	if got := state.WorkItemStatus(second.ID); got != Pending {
		t.Fatalf("blocked Goal was bypassed: downstream work = %s, want PENDING", got)
	}
	if err := state.UnblockGoalWithRepository("goal", RepositoryState{Revision: revision}, now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus(second.ID); got != Ready {
		t.Fatalf("unblocked Goal left downstream work = %s, want READY", got)
	}
}

func TestGoalReviewProjectionOffersCompletionOnlyWhenEveryPassIsFresh(t *testing.T) {
	state, now, first, second, revision := goalReviewDependencyFixture(t)
	if _, err := state.RecordVerificationWithRepository(first.ID, revision, "make verify", 0, RepositoryState{Revision: revision}, now); err != nil {
		t.Fatal(err)
	}
	if err := state.StartWithRepository(second.ID, RepositoryState{Revision: revision}, now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginVerification(second.ID, revision, "/tmp/worktree-two", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerification(second.ID, revision, "make verify", 0, now); err != nil {
		t.Fatal(err)
	}

	fresh, err := state.GoalSummary("goal", RepositoryState{Revision: revision})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Completion != GoalReadyToComplete {
		t.Fatalf("fresh Goal completion = %q, want %q", fresh.Completion, GoalReadyToComplete)
	}
	if !slices.Equal(fresh.VerificationEvidenceIDs, []string{"EV-001", "EV-002"}) {
		t.Fatalf("Goal completion evidence target = %v", fresh.VerificationEvidenceIDs)
	}
	if err := state.BeginVerification(first.ID, revision, "/tmp/worktree-one-again", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerificationWithRepository(first.ID, revision, "make verify", 0, RepositoryState{Revision: revision}, now); err != nil {
		t.Fatal(err)
	}
	refreshed, err := state.GoalSummary("goal", RepositoryState{Revision: revision})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(refreshed.VerificationEvidenceIDs, []string{"EV-003", "EV-002"}) {
		t.Fatalf("reverified Goal completion target = %v", refreshed.VerificationEvidenceIDs)
	}
	if _, err := state.AddWork("goal", "specs/stories/three", nil, now); err != nil {
		t.Fatal(err)
	}
	withNewWork, err := state.GoalSummary("goal", RepositoryState{Revision: revision})
	if err != nil {
		t.Fatal(err)
	}
	if withNewWork.Completion != GoalInProgress || len(withNewWork.VerificationEvidenceIDs) != 0 {
		t.Fatalf("new work did not invalidate Goal completion target: %#v", withNewWork)
	}
	if err := state.CompleteGoal("goal", now); err == nil {
		t.Fatal("generic completion bypassed the verified Goal transaction")
	}

	stale, err := state.GoalSummary("goal", RepositoryState{Revision: "2222222222222222222222222222222222222222"})
	if err != nil {
		t.Fatal(err)
	}
	if stale.Completion != GoalInProgress {
		t.Fatalf("stale Goal completion = %q, want %q", stale.Completion, GoalInProgress)
	}
	unknown, err := state.GoalSummary("goal", RepositoryState{})
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Completion != GoalInProgress {
		t.Fatalf("Goal without repository facts = %q, want %q", unknown.Completion, GoalInProgress)
	}
}

func TestActionableNextReverifiesAStaleVerifiedDependency(t *testing.T) {
	state, now, first, _, revision := goalReviewDependencyFixture(t)
	if _, err := state.RecordVerificationWithRepository(first.ID, revision, "make verify", 0, RepositoryState{Revision: revision}, now); err != nil {
		t.Fatal(err)
	}
	action := state.ActionableNext(RepositoryState{Revision: "2222222222222222222222222222222222222222"})
	if action.Kind != NextActionReverify || action.Item.ID != first.ID {
		t.Fatalf("action for stale VERIFIED work = %#v", action)
	}
}

func TestActionableNextOffersAutomaticGoalCompletion(t *testing.T) {
	state, now, first, second, revision := goalReviewDependencyFixture(t)
	if _, err := state.RecordVerificationWithRepository(first.ID, revision, "make verify", 0, RepositoryState{Revision: revision}, now); err != nil {
		t.Fatal(err)
	}
	if err := state.StartWithRepository(second.ID, RepositoryState{Revision: revision}, now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginVerification(second.ID, revision, "/tmp/worktree-two", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerification(second.ID, revision, "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
	action := state.ActionableNext(RepositoryState{Revision: revision})
	if action.Kind != NextActionCompleteGoal || action.Goal.ID != "goal" || action.Item.ID != "" {
		t.Fatalf("action at Goal completion boundary = %#v", action)
	}
}

func TestGoalReviewPolicyRejectsWorkItemReviewAndDirectGoalCompletion(t *testing.T) {
	state, now, first, _, revision := goalReviewDependencyFixture(t)
	if _, err := state.RecordVerification(first.ID, revision, "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
	evidenceCount := len(state.Evidence)
	if _, err := state.RecordReview(first.ID, revision, Approved, "human@example.com", "", "", now); err == nil {
		t.Fatal("recorded Work Item review under GOAL policy")
	} else if !strings.Contains(err.Error(), "GOAL review policy") {
		t.Fatalf("Work Item review error = %q", err)
	}
	if len(state.Evidence) != evidenceCount {
		t.Fatal("rejected Work Item review appended Evidence")
	}
	state.WorkItems[1].Status = Verified
	if err := state.CompleteGoal("goal", now); err == nil {
		t.Fatal("completed a GOAL-policy Goal outside the current-verification transaction")
	} else if !strings.Contains(err.Error(), "current verification") {
		t.Fatalf("Goal completion error = %q", err)
	}
}

func TestGoalReviewPolicyFailureAndInterruptionDoNotUnlockDependency(t *testing.T) {
	state, now, first, second, revision := goalReviewDependencyFixture(t)
	if _, err := state.RecordVerification(first.ID, revision, "make verify", 1, now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus(first.ID); got != Running {
		t.Fatalf("FAIL left work as %s, want RUNNING", got)
	}
	if got := state.WorkItemStatus(second.ID); got != Pending {
		t.Fatalf("FAIL unlocked downstream work: %s", got)
	}
	if err := state.BeginVerification(first.ID, revision, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if evidence, _, _, found, err := state.ReclaimRun(first.ID, "make verify", now); err != nil || !found || evidence.Result != Interrupted {
		t.Fatalf("ReclaimRun = %#v, %v, %v", evidence, found, err)
	}
	if got := state.WorkItemStatus(second.ID); got != Pending {
		t.Fatalf("INTERRUPTED unlocked downstream work: %s", got)
	}
}

func TestReverificationWithdrawsDependencySatisfactionUntilANewPass(t *testing.T) {
	state, now, first, second, revision := goalReviewDependencyFixture(t)
	if _, err := state.RecordVerificationWithRepository(first.ID, revision, "make verify", 0, RepositoryState{Revision: revision}, now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus(second.ID); got != Ready {
		t.Fatalf("initial PASS left downstream work = %s, want READY", got)
	}
	newRevision := "2222222222222222222222222222222222222222"
	if err := state.BeginVerification(first.ID, newRevision, "/tmp/worktree-new", "", now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus(second.ID); got != Pending {
		t.Fatalf("in-flight re-verification left downstream work = %s, want PENDING", got)
	}
	if _, err := state.RecordVerification(first.ID, newRevision, "make verify", 1, now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus(second.ID); got != Pending {
		t.Fatalf("failed re-verification left downstream work = %s, want PENDING", got)
	}
	if err := state.BeginVerification(first.ID, newRevision, "/tmp/worktree-new-pass", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerificationWithRepository(first.ID, newRevision, "make verify", 0, RepositoryState{Revision: newRevision}, now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus(second.ID); got != Ready {
		t.Fatalf("successful re-verification left downstream work = %s, want READY", got)
	}
}

func TestWorkSummaryDistinguishesVerifiedFromHumanAccepted(t *testing.T) {
	state, now, first, _, revision := goalReviewDependencyFixture(t)
	if _, err := state.RecordVerification(first.ID, revision, "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
	fresh, err := state.WorkSummary(first.ID, RepositoryState{Revision: revision})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Completion != CompletionVerifiedForGoalCompletion || fresh.HasReview {
		t.Fatalf("fresh VERIFIED summary = %#v", fresh)
	}
	stale, err := state.WorkSummary(first.ID, RepositoryState{Revision: "2222222222222222222222222222222222222222"})
	if err != nil {
		t.Fatal(err)
	}
	if stale.Completion != CompletionVerificationStale {
		t.Fatalf("stale VERIFIED summary = %#v", stale)
	}
}

func TestGoalReviewProjectionUsesSnapshotCandidateFreshness(t *testing.T) {
	now := time.Date(2026, 9, 13, 4, 0, 0, 0, time.UTC)
	state := NewState()
	if err := state.AddGoalWithReviewPolicy("goal", "Goal", "", "/repo", ReviewPerGoal, now); err != nil {
		t.Fatal(err)
	}
	item, err := state.AddWork("goal", "specs/stories/one", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Start(item.ID, now); err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{Kind: SnapshotCandidate,
		Revision: "2222222222222222222222222222222222222222", BaseRevision: "1111111111111111111111111111111111111111",
		Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if err := state.BeginCandidateVerification(item.ID, candidate, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerification(item.ID, candidate.Revision, "make verify", 0, now); err != nil {
		t.Fatal(err)
	}
	fresh, err := state.GoalSummary("goal", RepositoryState{SnapshotDigest: candidate.Digest})
	if err != nil || fresh.Completion != GoalReadyToComplete {
		t.Fatalf("fresh snapshot Goal summary = %#v, %v", fresh, err)
	}
	stale, err := state.GoalSummary("goal", RepositoryState{SnapshotDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"})
	if err != nil || stale.Completion != GoalInProgress {
		t.Fatalf("stale snapshot Goal summary = %#v, %v", stale, err)
	}
}

func TestSnapshotVerifiedDependencyMustBeFreshForNextAndStart(t *testing.T) {
	now := time.Date(2026, 9, 13, 5, 0, 0, 0, time.UTC)
	state := NewState()
	if err := state.AddGoalWithReviewPolicy("goal", "Goal", "", "/repo", ReviewPerGoal, now); err != nil {
		t.Fatal(err)
	}
	first, err := state.AddWork("goal", "specs/stories/one", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := state.AddWork("goal", "specs/stories/two", []string{first.ID}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Start(first.ID, now); err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{Kind: SnapshotCandidate,
		Revision: "2222222222222222222222222222222222222222", BaseRevision: "1111111111111111111111111111111111111111",
		Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if err := state.BeginCandidateVerification(first.ID, candidate, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	fresh := RepositoryState{Revision: candidate.BaseRevision, SnapshotDigest: candidate.Digest}
	if _, err := state.RecordVerificationWithRepository(first.ID, candidate.Revision, "make verify", 0, fresh, now); err != nil {
		t.Fatal(err)
	}
	stale := RepositoryState{Revision: candidate.BaseRevision, SnapshotDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	if next, ok := state.NextWithRepository(stale); ok {
		t.Fatalf("stale snapshot dependency produced next work %#v", next)
	}
	if err := state.StartWithRepository(second.ID, stale, now); err == nil {
		t.Fatal("started work behind a stale snapshot dependency")
	}
	if next, ok := state.NextWithRepository(fresh); !ok || next.ID != second.ID {
		t.Fatalf("fresh snapshot dependency next = %#v, %v", next, ok)
	}
}

func TestMixedCommitAndSnapshotDependenciesUseOneCurrentRepositoryState(t *testing.T) {
	now := time.Date(2026, 9, 13, 5, 30, 0, 0, time.UTC)
	state := NewState()
	if err := state.AddGoalWithReviewPolicy("goal", "Goal", "", "/repo", ReviewPerGoal, now); err != nil {
		t.Fatal(err)
	}
	commitItem, err := state.AddWork("goal", "specs/stories/commit", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	snapshotItem, err := state.AddWork("goal", "specs/stories/snapshot", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	downstream, err := state.AddWork("goal", "specs/stories/downstream", []string{commitItem.ID, snapshotItem.ID}, now)
	if err != nil {
		t.Fatal(err)
	}
	base := "1111111111111111111111111111111111111111"
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	current := RepositoryState{Revision: base, SnapshotDigest: digest}
	if err := state.Start(commitItem.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginVerification(commitItem.ID, base, "/tmp/commit", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerificationWithRepository(commitItem.ID, base, "make verify", 0, current, now); err != nil {
		t.Fatal(err)
	}
	if err := state.Start(snapshotItem.ID, now); err != nil {
		t.Fatal(err)
	}
	snapshot := Candidate{Kind: SnapshotCandidate, Revision: "2222222222222222222222222222222222222222", BaseRevision: base, Digest: digest}
	if err := state.BeginCandidateVerification(snapshotItem.ID, snapshot, "/tmp/snapshot", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerificationWithRepository(snapshotItem.ID, snapshot.Revision, "make verify", 0, current, now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus(downstream.ID); got != Ready {
		t.Fatalf("mixed fresh dependencies left downstream work %s", got)
	}
	state.RefreshReadyWithRepository(RepositoryState{Revision: base, SnapshotDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}, now)
	if got := state.WorkItemStatus(downstream.ID); got != Pending {
		t.Fatalf("stale snapshot among mixed dependencies left downstream work %s", got)
	}
}

func TestUnrelatedVerificationDoesNotDemoteReadyWorkBehindFreshVerifiedDependency(t *testing.T) {
	now := time.Date(2026, 9, 13, 5, 45, 0, 0, time.UTC)
	state := NewState()
	for _, id := range []string{"first-goal", "second-goal"} {
		if err := state.AddGoalWithReviewPolicy(id, id, "", "/repo", ReviewPerGoal, now); err != nil {
			t.Fatal(err)
		}
	}
	prerequisite, err := state.AddWork("first-goal", "specs/stories/prerequisite", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	downstream, err := state.AddWork("first-goal", "specs/stories/downstream", []string{prerequisite.ID}, now)
	if err != nil {
		t.Fatal(err)
	}
	revision := "1111111111111111111111111111111111111111"
	if err := state.Start(prerequisite.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginVerification(prerequisite.ID, revision, "/tmp/prerequisite", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerificationWithRepository(prerequisite.ID, revision, "make verify", 0, RepositoryState{Revision: revision}, now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus(downstream.ID); got != Ready {
		t.Fatalf("downstream work = %s before unrelated transition", got)
	}
	unrelated, err := state.AddWork("second-goal", "specs/stories/unrelated", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Start(unrelated.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginVerification(unrelated.ID, revision, "/tmp/unrelated", "", now); err != nil {
		t.Fatal(err)
	}
	if got := state.WorkItemStatus(downstream.ID); got != Ready {
		t.Fatalf("unrelated verification demoted downstream work to %s", got)
	}
}

func TestGoalReviewProjectionFailsClosedForBoundaryBlockers(t *testing.T) {
	now := time.Date(2026, 9, 13, 6, 0, 0, 0, time.UTC)
	empty := NewState()
	if err := empty.AddGoalWithReviewPolicy("goal", "Goal", "", "/repo", ReviewPerGoal, now); err != nil {
		t.Fatal(err)
	}
	if summary, err := empty.GoalSummary("goal", RepositoryState{Revision: "1111111111111111111111111111111111111111"}); err != nil || summary.Completion != GoalInProgress {
		t.Fatalf("empty Goal summary = %#v, %v", summary, err)
	}

	state := NewState()
	if err := state.AddGoalWithReviewPolicy("boundary", "Boundary", "", "/repo", ReviewPerGoal, now); err != nil {
		t.Fatal(err)
	}
	first, err := state.AddWork("boundary", "specs/stories/one", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Start(first.ID, now); err != nil {
		t.Fatal(err)
	}
	revision := "1111111111111111111111111111111111111111"
	if err := state.BeginVerification(first.ID, revision, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerificationWithRepository(first.ID, revision, "make verify", 0, RepositoryState{Revision: revision}, now); err != nil {
		t.Fatal(err)
	}
	if summary, err := state.GoalSummary("boundary", RepositoryState{Revision: revision}); err != nil || summary.Completion != GoalReadyToComplete {
		t.Fatalf("fresh boundary Goal summary = %#v, %v", summary, err)
	}
	gate, err := state.OpenGate(first.ID, "Which path?", []string{"one", "two"}, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if summary, err := state.GoalSummary("boundary", RepositoryState{Revision: revision}); err != nil || summary.Completion != GoalInProgress {
		t.Fatalf("open-Gate Goal summary = %#v, %v", summary, err)
	}
	if err := state.BlockGoal("boundary", "pause", now); err != nil {
		t.Fatal(err)
	}
	if summary, err := state.GoalSummary("boundary", RepositoryState{Revision: revision}); err != nil || summary.Completion != GoalBlockedCompletion {
		t.Fatalf("blocked Goal summary = %#v, %v", summary, err)
	}
	if err := state.ResolveGateWithRepository(gate.ID, "one", "", "human@example.com", RepositoryState{Revision: revision}, now); err != nil {
		t.Fatal(err)
	}
	if err := state.UnblockGoalWithRepository("boundary", RepositoryState{Revision: revision}, now); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginVerification(first.ID, revision, "/tmp/worktree-fail", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordVerification(first.ID, revision, "make verify", 1, now); err != nil {
		t.Fatal(err)
	}
	if summary, err := state.GoalSummary("boundary", RepositoryState{Revision: revision}); err != nil || summary.Completion != GoalInProgress {
		t.Fatalf("latest non-PASS Goal summary = %#v, %v", summary, err)
	}
}

func goalReviewDependencyFixture(t *testing.T) (State, time.Time, Item, Item, string) {
	t.Helper()
	now := time.Date(2026, 9, 13, 2, 0, 0, 0, time.UTC)
	state := NewState()
	if err := state.AddGoalWithReviewPolicy("goal", "Goal", "", "/repo", ReviewPerGoal, now); err != nil {
		t.Fatal(err)
	}
	first, err := state.AddWork("goal", "specs/stories/one", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := state.AddWork("goal", "specs/stories/two", []string{first.ID}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Start(first.ID, now); err != nil {
		t.Fatal(err)
	}
	revision := "1111111111111111111111111111111111111111"
	if err := state.BeginVerification(first.ID, revision, "/tmp/worktree", "", now); err != nil {
		t.Fatal(err)
	}
	return state, now, first, second, revision
}
