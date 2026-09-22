package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CarlLee1983/ForgePilot/internal/storage"
	"github.com/CarlLee1983/ForgePilot/internal/work"
)

type recordedRetentionAcquire struct {
	generation work.ExecutionEngineGeneration
	reference  string
}

type recordingGenerationRetention struct {
	acquires []recordedRetentionAcquire
	err      error
}

func (retention *recordingGenerationRetention) Acquire(_ context.Context, generation work.ExecutionEngineGeneration, reference string) error {
	retention.acquires = append(retention.acquires, recordedRetentionAcquire{generation: generation, reference: reference})
	return retention.err
}

func TestBindExecutionLaunchIdentityRetainsBeforePinningAndRefusesDrift(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("plan = %#v, err=%v", preview, err)
	}
	if _, err := AuthorizeExecutionFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator"); err != nil {
		t.Fatal(err)
	}
	before, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	profile := before.Goals[0].Execution.Authorizations[0].WorkerProfile
	now := time.Now().UTC().Truncate(time.Second)
	identity := RunnerIdentity{Runtime: profile.Runtime, ExecutablePath: profile.ExecutablePath, Version: "codex 1.2.3",
		Model: profile.Model, Effort: profile.Effort, Sandbox: profile.Sandbox}
	generation := work.ExecutionEngineGeneration{SourceCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PayloadSHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	retention := &recordingGenerationRetention{}
	bound, err := BindExecutionLaunchIdentity(t.Context(), fixture.root, before.Goals[0].ID, identity, generation, retention, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(retention.acquires) != 1 || retention.acquires[0].generation != generation || len(retention.acquires[0].reference) != 64 {
		t.Fatalf("retention acquire = %#v", retention.acquires)
	}
	after, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	current := after.Goals[0].Execution.Authorizations[0]
	if current.WorkerIdentity == nil || current.EngineGeneration == nil || *current.EngineGeneration != generation ||
		current.WorkerIdentity.ExecutablePath != profile.ExecutablePath || current.WorkerIdentity.ReportedVersion != identity.Version {
		t.Fatalf("bound authorization = %#v", current)
	}
	if bound.Digest != current.Digest || current.Digest == before.Goals[0].Execution.Authorizations[0].Digest ||
		after.Goals[0].Execution.Witness.AuthorizationDigest != current.Digest {
		t.Fatalf("binding did not reseal the authorization and witness: %#v", after.Goals[0].Execution)
	}
	stateBytes := marshalState(t, after)
	if bytes.Contains(stateBytes, []byte(retention.acquires[0].reference)) {
		t.Fatal("repository state persisted the raw Bootstrap retention reference")
	}

	// A retry after a state-write crash reacquires the same opaque marker, then
	// recognizes the exact durable binding without changing state.
	if _, err := BindExecutionLaunchIdentity(t.Context(), fixture.root, before.Goals[0].ID, identity, generation, retention, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(retention.acquires) != 2 || retention.acquires[1].reference != retention.acquires[0].reference {
		t.Fatalf("retry did not reacquire the same retention marker: %#v", retention.acquires)
	}
	stable, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stateBytes, marshalState(t, stable)) {
		t.Fatal("idempotent identity binding changed durable state")
	}

	drift := generation
	drift.PayloadSHA256 = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if _, err := BindExecutionLaunchIdentity(t.Context(), fixture.root, before.Goals[0].ID, identity, drift, retention, now.Add(2*time.Minute)); err == nil {
		t.Fatal("engine drift was admitted")
	}
	if len(retention.acquires) != 2 {
		t.Fatal("engine drift acquired a replacement generation before refusal")
	}
	refused, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stateBytes, marshalState(t, refused)) {
		t.Fatal("engine drift mutated durable state")
	}
}

func TestBindExecutionLaunchIdentityLeavesStateUnchangedWhenRetentionFails(t *testing.T) {
	fixture := newExecutionTestFixture(t)
	preview, err := PlanExecutionFile(t.Context(), fixture.root, "execution-request.json")
	if err != nil || len(preview.Diagnostics) != 0 {
		t.Fatalf("plan = %#v, err=%v", preview, err)
	}
	if _, err := AuthorizeExecutionFile(t.Context(), fixture.root, "execution-request.json", preview.ApprovalToken, "operator"); err != nil {
		t.Fatal(err)
	}
	before, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	profile := before.Goals[0].Execution.Authorizations[0].WorkerProfile
	identity := RunnerIdentity{Runtime: profile.Runtime, ExecutablePath: profile.ExecutablePath, Version: "codex 1.2.3",
		Model: profile.Model, Effort: profile.Effort, Sandbox: profile.Sandbox}
	generation := work.ExecutionEngineGeneration{SourceCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PayloadSHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	retention := &recordingGenerationRetention{err: errors.New("helper unavailable")}
	if _, err := BindExecutionLaunchIdentity(t.Context(), fixture.root, before.Goals[0].ID, identity, generation, retention, time.Now().UTC()); err == nil {
		t.Fatal("retention failure admitted a launch identity")
	}
	after, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(marshalState(t, before), marshalState(t, after)) {
		t.Fatal("retention failure mutated execution state")
	}
}

func TestBootstrapRetentionUsesTheVersionedExactTupleProtocol(t *testing.T) {
	generation := work.ExecutionEngineGeneration{SourceCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PayloadSHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	reference := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	helper := filepath.Join(t.TempDir(), "forgepilot-bootstrap")
	script := fmt.Sprintf(`#!/bin/sh
[ "$1" = retention-v1 ] && [ "$2" = acquire ] && [ "$3" = --generation ] && [ "$4" = %s ] && [ "$5" = --payload-digest ] && [ "$6" = %s ] && [ "$7" = --reference ] && [ "$8" = %s ] || exit 9
printf '%%s\n' '{"protocol_version":1,"result":"acquired","generation_id":"%s","payload_digest":"%s"}'
`, generation.SourceCommit, generation.PayloadSHA256, reference, generation.SourceCommit, generation.PayloadSHA256)
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := (BootstrapRetention{HelperPath: helper}).Acquire(t.Context(), generation, reference); err != nil {
		t.Fatal(err)
	}
}
