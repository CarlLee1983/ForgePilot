package work

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestAdoptInitialExecutionStoresCompleteBindingWithoutChangingLifecycle(t *testing.T) {
	now := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	state, execution := initialExecutionFixture(t, now)
	beforeGoals := append([]Goal(nil), state.Goals...)
	beforeItems := append([]Item(nil), state.WorkItems...)

	if err := state.AdoptInitialExecution(execution); err != nil {
		t.Fatal(err)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("adopted state is invalid: %v", err)
	}
	if len(state.Goals) != 1 || state.Goals[0].Execution == nil {
		t.Fatalf("Goal execution aggregate = %#v", state.Goals)
	}
	goalWithoutExecution := state.Goals[0]
	goalWithoutExecution.Execution = nil
	if !reflect.DeepEqual(goalWithoutExecution, beforeGoals[0]) {
		t.Fatalf("adoption changed Goal lifecycle or other fields: before %#v, after %#v", beforeGoals[0], goalWithoutExecution)
	}
	if !reflect.DeepEqual(state.WorkItems, beforeItems) {
		t.Fatal("adoption changed Work Item lifecycle or registration")
	}
	aggregate := state.Goals[0].Execution
	if len(aggregate.PlanBindings) != 1 || len(aggregate.PlanBindings[0].Nodes) != len(beforeItems) {
		t.Fatalf("adopted bindings = %#v", aggregate.PlanBindings)
	}
	if len(aggregate.Authorizations) != 1 || aggregate.Authorizations[0].Revision != 1 ||
		aggregate.Ledger.RunsConsumed != 0 || aggregate.Ledger.StepsConsumed != 0 || aggregate.Ledger.RecoveriesConsumed != 0 {
		t.Fatalf("initial authorization/ledger = %#v / %#v", aggregate.Authorizations, aggregate.Ledger)
	}
	for _, attempts := range aggregate.Ledger.NodeAttempts {
		if attempts.TechnicalAttempts != 0 {
			t.Fatalf("initial attempts = %#v", aggregate.Ledger.NodeAttempts)
		}
	}

	// The state owns its durable aggregate; a caller retaining request slices
	// cannot mutate the already-adopted mapping after the transaction.
	execution.PlanBindings[0].Nodes[0].WorkItemID = "WI-999"
	if state.Goals[0].Execution.PlanBindings[0].Nodes[0].WorkItemID == "WI-999" {
		t.Fatal("persisted aggregate aliases caller-owned slices")
	}
}

func TestAdoptInitialExecutionRejectsInvalidAggregateWithoutMutation(t *testing.T) {
	now := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*GoalExecution)
	}{
		{name: "incomplete mapping", mutate: func(execution *GoalExecution) { execution.PlanBindings[0].Nodes = execution.PlanBindings[0].Nodes[:1] }},
		{name: "duplicate Work Item mapping", mutate: func(execution *GoalExecution) {
			execution.PlanBindings[0].Nodes[1].WorkItemID = execution.PlanBindings[0].Nodes[0].WorkItemID
		}},
		{name: "Story mismatch", mutate: func(execution *GoalExecution) { execution.PlanBindings[0].Nodes[0].StoryRef = "specs/stories/other" }},
		{name: "topology mismatch", mutate: func(execution *GoalExecution) { execution.PlanBindings[0].Nodes[1].DependsOn = nil }},
		{name: "zero cap", mutate: func(execution *GoalExecution) { execution.Authorizations[0].Caps.MaxRuns = 0 }},
		{name: "invalid artifact order", mutate: func(execution *GoalExecution) {
			execution.Authorizations[0].Caps.Artifacts.MaxWriteBytes = 20
			execution.Authorizations[0].Caps.Artifacts.MaxRunBytes = 10
		}},
		{name: "invalid Worker Profile", mutate: func(execution *GoalExecution) { execution.Authorizations[0].WorkerProfile.Model = "" }},
		{name: "unobserved Worker identity", mutate: func(execution *GoalExecution) {
			execution.Authorizations[0].WorkerIdentity = &ResolvedWorkerIdentity{
				ExecutablePath:   execution.Authorizations[0].WorkerProfile.ExecutablePath,
				ExecutableSHA256: execution.Authorizations[0].WorkerProfile.ExecutableSHA256,
				ReportedVersion:  "self-declared-version", ObservedAt: now,
			}
		}},
		{name: "unobserved engine generation", mutate: func(execution *GoalExecution) {
			execution.Authorizations[0].EngineGeneration = &ExecutionEngineGeneration{
				SourceCommit: "self-declared-commit", PayloadSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			}
		}},
		{name: "expiry outside maximum", mutate: func(execution *GoalExecution) {
			execution.Authorizations[0].ExpiresAt = execution.Authorizations[0].AuthorizedAt.Add(15 * 24 * time.Hour)
		}},
		{name: "nonzero initial ledger", mutate: func(execution *GoalExecution) { execution.Ledger.StepsConsumed = 1 }},
		{name: "missing node ledger", mutate: func(execution *GoalExecution) { execution.Ledger.NodeAttempts = execution.Ledger.NodeAttempts[:1] }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, execution := initialExecutionFixture(t, now)
			test.mutate(&execution)
			before := state
			if err := state.AdoptInitialExecution(execution); err == nil {
				t.Fatal("accepted invalid initial execution aggregate")
			}
			if !reflect.DeepEqual(state, before) {
				t.Fatalf("failed adoption mutated state:\nbefore: %#v\nafter:  %#v", before, state)
			}
		})
	}
}

func TestAdoptInitialExecutionRefusesAnExistingAuthorization(t *testing.T) {
	now := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	state, execution := initialExecutionFixture(t, now)
	if err := state.AdoptInitialExecution(execution); err != nil {
		t.Fatal(err)
	}
	before := state
	if err := state.AdoptInitialExecution(execution); err == nil {
		t.Fatal("adopted a second initial authorization")
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatal("rejected duplicate adoption mutated state")
	}
}

func TestReviseExecutionAppendsAnAdditivePlanWithoutResettingHistoryOrConsumption(t *testing.T) {
	now := time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)
	state, execution := initialExecutionFixture(t, now)
	if err := state.AdoptInitialExecution(execution); err != nil {
		t.Fatal(err)
	}
	if _, err := state.PrepareExecutionReservation("goal", ExecutionReservation{
		ID: "run-001:step:001", Kind: ExecutionReservationStep, RunID: "run-001", CreatedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	history := cloneGoalExecution(*state.Goals[0].Execution)
	third, err := state.AddWork("goal", "specs/stories/third", []string{"WI-002"}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	digest := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	binding := cloneGoalExecution(*state.Goals[0].Execution).PlanBindings[0]
	binding.Revision = 2
	binding.RequestSHA256 = "sha256:" + digest
	binding.PlanRevision = 2
	binding.ManifestSHA256 = digest
	binding.CoverageReview.SHA256 = digest
	binding.Declaration.SHA256 = digest
	binding.Nodes = append(binding.Nodes, ExecutionPlanNodeBinding{
		PlanNodeRef: "node-third", StoryRef: third.StoryRef,
		ReadinessContract: ExecutionArtifactBinding{Path: "specs/stories/third/readiness.json", SHA256: digest},
		DependsOn:         []string{"node-second"}, WorkItemID: third.ID,
	})
	binding.AdoptedAt = now.Add(3 * time.Minute)
	binding.Digest = ""
	authorization := cloneGoalExecution(*state.Goals[0].Execution).Authorizations[0]
	authorization.Revision = 2
	authorization.RequestSHA256 = binding.RequestSHA256
	authorization.ApprovalToken = binding.RequestSHA256
	authorization.AuthorizedAt = binding.AdoptedAt
	authorization.ExpiresAt = binding.AdoptedAt.Add(14 * 24 * time.Hour)
	authorization.PlanBindingDigest = ""
	authorization.Digest = ""

	if err := state.ReviseExecution("goal", binding, authorization); err != nil {
		t.Fatal(err)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("revised state is invalid: %v", err)
	}
	got := state.Goals[0].Execution
	if len(got.PlanBindings) != 2 || len(got.Authorizations) != 2 || got.PlanBindings[1].Revision != 2 || got.Authorizations[1].Revision != 2 {
		t.Fatalf("revision history = %#v", got)
	}
	if !reflect.DeepEqual(got.PlanBindings[0], history.PlanBindings[0]) || !reflect.DeepEqual(got.Authorizations[0], history.Authorizations[0]) {
		t.Fatal("revision rewrote historic binding or authorization")
	}
	if got.Ledger.StepsConsumed != history.Ledger.StepsConsumed || !reflect.DeepEqual(got.Ledger.Reservations, history.Ledger.Reservations) {
		t.Fatalf("revision reset or rewrote cumulative accounting: %#v", got.Ledger)
	}
	if got.Ledger.AuthorizationRevision != 2 || len(got.Ledger.NodeAttempts) != 3 || got.Ledger.NodeAttempts[2] != (ExecutionNodeAttempts{PlanNodeRef: "node-third"}) {
		t.Fatalf("current ledger did not retain accounting and add the new node: %#v", got.Ledger)
	}
}

func TestReviseExecutionMatchesRetainedNodesByReferenceRatherThanManifestPosition(t *testing.T) {
	now := time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)
	state, execution := initialExecutionFixture(t, now)
	if err := state.AdoptInitialExecution(execution); err != nil {
		t.Fatal(err)
	}
	added, err := state.AddWork("goal", "specs/stories/inserted", nil, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	binding := cloneGoalExecution(*state.Goals[0].Execution).PlanBindings[0]
	binding.Revision, binding.PlanRevision = 2, 2
	binding.RequestSHA256 = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	binding.ManifestSHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	binding.CoverageReview.SHA256 = binding.ManifestSHA256
	binding.Declaration.SHA256 = binding.ManifestSHA256
	binding.Nodes = append([]ExecutionPlanNodeBinding{{
		PlanNodeRef: "node-inserted", StoryRef: added.StoryRef,
		ReadinessContract: ExecutionArtifactBinding{Path: "specs/stories/inserted/readiness.json", SHA256: binding.ManifestSHA256},
		WorkItemID:        added.ID,
	}}, binding.Nodes...)
	binding.AdoptedAt = now.Add(2 * time.Minute)
	authorization := cloneGoalExecution(*state.Goals[0].Execution).Authorizations[0]
	authorization.Revision, authorization.RequestSHA256, authorization.ApprovalToken = 2, binding.RequestSHA256, binding.RequestSHA256
	authorization.AuthorizedAt, authorization.ExpiresAt = binding.AdoptedAt, binding.AdoptedAt.Add(14*24*time.Hour)
	authorization.PlanBindingDigest, authorization.Digest = "", ""

	if err := state.ReviseExecution("goal", binding, authorization); err != nil {
		t.Fatalf("revision with a lexically earlier additive node: %v", err)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("revision with reordered manifest is invalid: %v", err)
	}
}

func TestReviseExecutionRejectsCapsBelowPreservedConsumption(t *testing.T) {
	now := time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)
	state, execution := initialExecutionFixture(t, now)
	if err := state.AdoptInitialExecution(execution); err != nil {
		t.Fatal(err)
	}
	for ordinal := 1; ordinal <= 2; ordinal++ {
		if _, err := state.PrepareExecutionReservation("goal", ExecutionReservation{
			ID: fmt.Sprintf("run-001:step:%d", ordinal), Kind: ExecutionReservationStep, RunID: "run-001", CreatedAt: now.Add(time.Duration(ordinal) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	binding := cloneGoalExecution(*state.Goals[0].Execution).PlanBindings[0]
	binding.Revision, binding.PlanRevision = 2, 2
	binding.RequestSHA256 = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	binding.ManifestSHA256, binding.CoverageReview.SHA256, binding.Declaration.SHA256 = binding.RequestSHA256[len("sha256:"):], binding.RequestSHA256[len("sha256:"):], binding.RequestSHA256[len("sha256:"):]
	binding.AdoptedAt = now.Add(3 * time.Minute)
	authorization := cloneGoalExecution(*state.Goals[0].Execution).Authorizations[0]
	authorization.Revision, authorization.RequestSHA256, authorization.ApprovalToken = 2, binding.RequestSHA256, binding.RequestSHA256
	authorization.AuthorizedAt, authorization.ExpiresAt = binding.AdoptedAt, binding.AdoptedAt.Add(14*24*time.Hour)
	authorization.Caps.MaxSteps = 1
	authorization.PlanBindingDigest, authorization.Digest = "", ""
	before := cloneGoalExecution(*state.Goals[0].Execution)

	if err := state.ReviseExecution("goal", binding, authorization); err == nil {
		t.Fatal("accepted a revision whose step cap is below preserved consumption")
	}
	if !reflect.DeepEqual(*state.Goals[0].Execution, before) {
		t.Fatal("rejected lower cap revision mutated execution history or ledger")
	}
}

func initialExecutionFixture(t *testing.T, now time.Time) (State, GoalExecution) {
	t.Helper()
	state := NewState()
	if err := state.AddGoal("goal", "Goal", "", "/repo", now); err != nil {
		t.Fatal(err)
	}
	first, err := state.AddWork("goal", "specs/stories/first", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := state.AddWork("goal", "specs/stories/second", []string{first.ID}, now)
	if err != nil {
		t.Fatal(err)
	}
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	requestDigest := "sha256:" + digest
	binding := GoalPlanBinding{
		Revision: 1, RequestSHA256: requestDigest, PlanID: "plan-1", PlanRevision: 1,
		ManifestSHA256: digest, CoverageReviewID: "review-1", CoverageReview: ExecutionArtifactBinding{Path: "coverage-review.json", SHA256: digest},
		Declaration: ExecutionArtifactBinding{Path: "specs/plans/plan.json", SHA256: digest},
		Nodes: []ExecutionPlanNodeBinding{
			{PlanNodeRef: "node-first", StoryRef: first.StoryRef, ReadinessContract: ExecutionArtifactBinding{Path: "specs/stories/first/readiness.json", SHA256: digest}, WorkItemID: first.ID},
			{PlanNodeRef: "node-second", StoryRef: second.StoryRef, ReadinessContract: ExecutionArtifactBinding{Path: "specs/stories/second/readiness.json", SHA256: digest}, DependsOn: []string{"node-first"}, WorkItemID: second.ID},
		},
		AdoptedAt: now,
	}
	execution := GoalExecution{
		GoalID: "goal", Workspace: "/repo", PlanBindings: []GoalPlanBinding{binding},
		Authorizations: []ExecutionAuthorization{{
			Revision: 1, RequestSHA256: requestDigest, ApprovalToken: requestDigest, Approver: "operator",
			GoalID: "goal", Workspace: "/repo",
			AuthorizedAt: now, ExpiresAt: now.Add(14 * 24 * time.Hour),
			Caps: ExecutionCaps{MaxSteps: 500, MaxTechnicalAttemptsPerNode: 5, MaxRuns: 20, MaxRecoveries: 2,
				Artifacts: ExecutionArtifactLimits{MaxHandoffBytes: 64 * 1024, MaxWriteBytes: 1 << 20, MaxRunBytes: 16 << 20, MaxTotalBytes: 128 << 20}},
			WorkerProfile: WorkerProfile{Runtime: "codex", ExecutablePath: "/usr/local/bin/codex", ExecutableSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Model: "model-v1", Effort: "medium", Sandbox: "workspace-write"},
		}},
		Ledger: ExecutionLedger{Revision: 1, AuthorizationRevision: 1, NodeAttempts: []ExecutionNodeAttempts{
			{PlanNodeRef: "node-first"}, {PlanNodeRef: "node-second"},
		}},
	}
	return state, execution
}
