package control

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/work"
)

func TestReadRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	root := controlTestRoot(t)
	path := controlPath(root)
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"revision":0,"waits":[],"external_declarations":[],"unknown":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(root); err == nil {
		t.Fatal("Read accepted an unknown field")
	}
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"revision":0,"waits":[],"external_declarations":[]} {}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(root); err == nil {
		t.Fatal("Read accepted trailing JSON")
	}
}

func TestReadUpgradesV1SidecarWithoutInventingEngineRevisionIntent(t *testing.T) {
	root := controlTestRoot(t)
	path := controlPath(root)
	contents := []byte(`{"schema_version":1,"revision":0,"waits":[],"external_declarations":[]}`)
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}
	state, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if state.SchemaVersion != SchemaVersion || state.Pause != nil {
		t.Fatalf("v1 control upgrade = %#v", state)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(contents) {
		t.Fatal("read rewrote the legacy control sidecar")
	}
}

func TestReadRejectsV1SidecarThatClaimsV2EngineRevisionIntent(t *testing.T) {
	root := controlTestRoot(t)
	contents := []byte(`{"schema_version":1,"revision":1,"pause":{"goal_id":"g","run_id":"r","reason":"stop","requested_by":"operator","requested_at":"2026-09-23T00:00:00Z","engine_revision":{"authorization_digest":"sha256:authorization","engine_generation":{"source_commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","payload_sha256":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},"revision":1},"waits":[],"external_declarations":[]}`)
	if err := os.WriteFile(controlPath(root), contents, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(root); err == nil {
		t.Fatal("v1 control sidecar carrying v2 engine revision intent was accepted")
	}
}

func TestEngineRevisionPauseRequiresExactDurableBinding(t *testing.T) {
	state := NewState()
	pause := testPause()
	pause.EngineRevision = &EngineRevisionPause{AuthorizationDigest: "sha256:authorization",
		EngineGeneration: work.ExecutionEngineGeneration{SourceCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			PayloadSHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}
	if err := state.SetPause(pause); err != nil {
		t.Fatalf("set engine revision pause: %v", err)
	}
	state.Revision = 1
	if err := state.Validate(); err != nil {
		t.Fatalf("validate engine revision pause: %v", err)
	}
	invalid := *pause.EngineRevision
	invalid.AuthorizationDigest = ""
	state.Pause.EngineRevision = &invalid
	if err := state.Validate(); err == nil {
		t.Fatal("engine revision pause without authorization digest was accepted")
	}
}

func TestUpdateIsAtomicOnRejectedOperation(t *testing.T) {
	root := controlTestRoot(t)
	if err := Update(root, func(state *State) error {
		return state.SetPause(testPause())
	}); err != nil {
		t.Fatalf("initial Update: %v", err)
	}
	before, err := os.ReadFile(controlPath(root))
	if err != nil {
		t.Fatal(err)
	}
	err = Update(root, func(state *State) error {
		state.Waits = append(state.Waits, Wait{ID: "bad"})
		return nil
	})
	if err == nil {
		t.Fatal("invalid Update succeeded")
	}
	after, err := os.ReadFile(controlPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("rejected Update changed the sidecar")
	}
	info, err := os.Stat(controlPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("sidecar mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestWithLockSerializesControlTransactions(t *testing.T) {
	root := controlTestRoot(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() { firstDone <- WithLock(root, func() error { close(entered); <-release; return nil }) }()
	<-entered
	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() { secondDone <- WithLock(root, func() error { close(secondEntered); return nil }) }()
	select {
	case <-secondEntered:
		t.Fatal("second control lock entered before first released")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first lock: %v", err)
	}
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second lock: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second control lock did not proceed")
	}
}

func TestValidationRejectsInvalidControlFacts(t *testing.T) {
	state := NewState()
	state.Pause = &Pause{GoalID: "g", RunID: "r", Reason: "stop", RequestedBy: "user", RequestedAt: time.Now().UTC(), Revision: 1}
	if err := state.Validate(); err == nil {
		t.Fatal("pause revision ahead of state accepted")
	}
	state.Revision = 1
	state.Waits = []Wait{{ID: "w", Kind: WaitKind("wrong"), GoalID: "g", RunID: "r", NodeID: "n", PlanDigest: "sha256:p", AuthorizationRevision: 1, AuthorizationDigest: "sha256:a", Question: "?", Context: "context", ExpectedFact: "fact"}}
	if err := state.Validate(); err == nil {
		t.Fatal("invalid wait kind accepted")
	}
}

func TestExternalDeclarationIsAppendOnlyAndIdempotent(t *testing.T) {
	root := controlTestRoot(t)
	wait := testExternalWait()
	declaration := testDeclaration()
	if err := Update(root, func(state *State) error {
		if err := state.AppendWait(wait); err != nil {
			return err
		}
		return state.AppendExternalDeclaration(declaration)
	}); err != nil {
		t.Fatalf("append declaration: %v", err)
	}
	if err := Update(root, func(state *State) error { return state.AppendExternalDeclaration(declaration) }); err != nil {
		t.Fatalf("idempotent append: %v", err)
	}
	state, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.ExternalDeclarations) != 1 {
		t.Fatalf("declarations = %#v", state.ExternalDeclarations)
	}
	changed := declaration
	changed.FactName = "different"
	if err := Update(root, func(state *State) error { return state.AppendExternalDeclaration(changed) }); err == nil {
		t.Fatal("conflicting idempotent declaration succeeded")
	}
	if err := Update(root, func(state *State) error { state.ExternalDeclarations = nil; return nil }); err == nil {
		t.Fatal("declaration removal succeeded")
	}
}

func TestWaitIsAppendOnlyAndPauseReferencesItsExactRun(t *testing.T) {
	root := controlTestRoot(t)
	wait := testExternalWait()
	if err := Update(root, func(state *State) error {
		if err := state.AppendWait(wait); err != nil {
			return err
		}
		return state.SetPause(Pause{GoalID: wait.GoalID, RunID: wait.RunID, Reason: "needs_human", RequestedBy: "ForgePilot", RequestedAt: time.Now().UTC(), WaitID: wait.ID})
	}); err != nil {
		t.Fatalf("write wait pause: %v", err)
	}
	if err := Update(root, func(state *State) error { state.Waits = nil; return nil }); err == nil {
		t.Fatal("wait removal succeeded")
	}
	if err := Update(root, func(state *State) error {
		return state.SetPause(Pause{GoalID: wait.GoalID, RunID: "other", Reason: "needs_human", RequestedBy: "ForgePilot", RequestedAt: time.Now().UTC(), WaitID: wait.ID})
	}); err == nil {
		t.Fatal("pause with mismatched wait run succeeded")
	}
}

func TestExternalDeclarationMustExactlyBindExternalWait(t *testing.T) {
	state := NewState()
	state.Waits = []Wait{testExternalWait()}
	declaration := testDeclaration()
	declaration.AuthorizationDigest = "sha256:other"
	if err := state.AppendExternalDeclaration(declaration); err == nil {
		t.Fatal("mismatched declaration binding accepted")
	}
}

func controlTestRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, stateDirectory), 0700); err != nil {
		t.Fatal(err)
	}
	return root
}

func testPause() Pause {
	return Pause{GoalID: "G-001", RunID: "run-001", Reason: "requested", RequestedBy: "Carl", RequestedAt: time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)}
}

func testExternalWait() Wait {
	return Wait{ID: "wait-001", Kind: WaitExternal, GoalID: "G-001", RunID: "run-001", NodeID: "node-001", PlanDigest: "sha256:plan", AuthorizationRevision: 1, AuthorizationDigest: "sha256:authorization", Question: "What happened?", Context: "deployment", ExpectedFact: "production-deployment"}
}

func testDeclaration() ExternalDeclaration {
	return ExternalDeclaration{ID: "declaration-001", FactName: "production-deployment", WaitID: "wait-001", NodeID: "node-001", PlanDigest: "sha256:plan", AuthorizationRevision: 1, AuthorizationDigest: "sha256:authorization", DeclaredBy: "Carl", DeclaredAt: time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)}
}
