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

type staticBootstrapGenerationResolver struct {
	resolved ResolvedBootstrapGeneration
	calls    int
	err      error
}

func (resolver *staticBootstrapGenerationResolver) Resolve(context.Context) (ResolvedBootstrapGeneration, error) {
	resolver.calls++
	return resolver.resolved, resolver.err
}

func TestEnsureExecutionLaunchIdentityReacquiresBeforeAndAfterAuthorizationBinding(t *testing.T) {
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
	authorization := before.Goals[0].Execution.Authorizations[0]
	generation := work.ExecutionEngineGeneration{SourceCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PayloadSHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	helper := filepath.Join(t.TempDir(), "forgepilot-bootstrap")
	script := `#!/bin/sh
[ "$1" = retention-v1 ] && [ "$2" = acquire ] && [ "$3" = --generation ] && [ "$5" = --payload-digest ] && [ "$7" = --reference ] || exit 9
printf '{"protocol_version":1,"result":"acquired","generation_id":"%s","payload_digest":"%s"}\n' "$4" "$6"
`
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	resolver := &staticBootstrapGenerationResolver{resolved: ResolvedBootstrapGeneration{Generation: generation, HelperPath: helper}}
	identity := RunnerIdentity{Runtime: authorization.WorkerProfile.Runtime, ExecutablePath: authorization.WorkerProfile.ExecutablePath,
		Version: "codex 1.2.3", Model: authorization.WorkerProfile.Model, Effort: authorization.WorkerProfile.Effort,
		Sandbox: authorization.WorkerProfile.Sandbox}
	if _, _, err := ReacquireExecutionLaunchIdentity(t.Context(), fixture.root, before.Goals[0].ID, authorization.Digest,
		identity, resolver, time.Now().UTC()); err == nil || resolver.calls != 0 {
		t.Fatalf("unbound exact resume = %v, resolver calls=%d; want refusal before discovery", err, resolver.calls)
	}

	bound, admitted, err := EnsureExecutionLaunchIdentity(t.Context(), fixture.root, before.Goals[0].ID, authorization.Digest,
		identity, resolver, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 1 || admitted.EngineGeneration == nil || *admitted.EngineGeneration != generation {
		t.Fatalf("admission resolver or identity = calls %d, identity %#v", resolver.calls, admitted)
	}
	if bound.Digest == authorization.Digest {
		t.Fatal("initial admission did not reseal its authorization")
	}

	stable, err := storage.Load(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnsureExecutionLaunchIdentity(t.Context(), fixture.root, before.Goals[0].ID, bound.Digest,
		identity, resolver, time.Now().UTC()); err != nil {
		t.Fatalf("exact retry = %v", err)
	}
	if resolver.calls != 2 || !bytes.Equal(marshalState(t, stable), marshalState(t, mustLoadExecutionState(t, fixture.root))) {
		t.Fatalf("exact retry calls=%d or changed state", resolver.calls)
	}
	if _, _, err := EnsureExecutionLaunchIdentity(t.Context(), fixture.root, before.Goals[0].ID, authorization.Digest,
		identity, resolver, time.Now().UTC()); err == nil || resolver.calls != 2 {
		t.Fatalf("stale admission = %v, resolver calls=%d; want refusal before discovery", err, resolver.calls)
	}
}

func mustLoadExecutionState(t *testing.T, root string) work.State {
	t.Helper()
	state, err := storage.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return state
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

func TestBootstrapRetentionRejectsDuplicateJSONKeys(t *testing.T) {
	generation := work.ExecutionEngineGeneration{SourceCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PayloadSHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	reference := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	helper := filepath.Join(t.TempDir(), "forgepilot-bootstrap")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' '{"protocol_version":1,"result":"acquired","result":"acquired","generation_id":"%s","payload_digest":"%s"}'
`, generation.SourceCommit, generation.PayloadSHA256)
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := (BootstrapRetention{HelperPath: helper}).Acquire(t.Context(), generation, reference); err == nil {
		t.Fatal("retention protocol accepted duplicate JSON keys")
	}
}

func TestBootstrapGenerationResolverAcceptsOnlyItsProcessGenerationHelper(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".local", "share", "forgepilot")
	generation := work.ExecutionEngineGeneration{SourceCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PayloadSHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	managedExecutable := filepath.Join(root, "versions", generation.SourceCommit, "bin", "forgepilot")
	managedHelper := filepath.Join(root, "versions", generation.SourceCommit, "libexec", "forgepilot-bootstrap")
	if err := os.MkdirAll(filepath.Dir(managedExecutable), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(managedHelper), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedExecutable, []byte("managed CLI"), 0700); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/bin/sh
[ "$1" = generation-v1 ] && [ "$2" = current ] || exit 9
printf '%%s\n' '{"protocol_version":1,"generation_id":"%s","payload_digest":"%s","forgepilot_path":"%s","helper_path":"%s"}'
`, generation.SourceCommit, generation.PayloadSHA256, managedExecutable, managedHelper)
	if err := os.WriteFile(managedHelper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("versions/"+generation.SourceCommit, filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "current", "libexec", "forgepilot-bootstrap"), filepath.Join(home, ".local", "bin", "forgepilot-bootstrap")); err != nil {
		t.Fatal(err)
	}

	image := BootstrapProcessImage{executablePath: managedExecutable, homePath: home, managedRoot: root}
	resolved, err := (BootstrapGenerationResolver{ProcessImage: image}).Resolve(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Generation != generation || resolved.HelperPath != managedHelper {
		t.Fatalf("resolved generation = %#v", resolved)
	}

	fabricatedHelper := filepath.Join(root, "unmanaged", "forgepilot-bootstrap")
	if err := os.MkdirAll(filepath.Dir(fabricatedHelper), 0700); err != nil {
		t.Fatal(err)
	}
	fabricatedMarker := filepath.Join(root, "fabricated-helper-ran")
	if err := os.WriteFile(fabricatedHelper, []byte("#!/bin/sh\ntouch "+fabricatedMarker+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := (BootstrapGenerationResolver{ProcessImage: image}).Resolve(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fabricatedMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("resolver invoked a fabricated helper")
	}

	managedMarker := filepath.Join(root, "unsafe-managed-helper-ran")
	if err := os.WriteFile(managedHelper, []byte("#!/bin/sh\ntouch "+managedMarker+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(managedHelper, filepath.Join(root, "helper-hard-link")); err != nil {
		t.Fatal(err)
	}
	if _, err := (BootstrapGenerationResolver{ProcessImage: image}).Resolve(t.Context()); err == nil {
		t.Fatal("resolver accepted a hard-linked managed helper")
	}
	if _, err := os.Stat(managedMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("resolver executed an unsafe managed helper")
	}
}

func TestBootstrapGenerationResolverRefusesAChangedCurrentGenerationAfterProcessStart(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".local", "share", "forgepilot")
	oldGeneration := work.ExecutionEngineGeneration{SourceCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PayloadSHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	newGeneration := work.ExecutionEngineGeneration{SourceCommit: "cccccccccccccccccccccccccccccccccccccccccccc",
		PayloadSHA256: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}
	oldExecutable := filepath.Join(root, "versions", oldGeneration.SourceCommit, "bin", "forgepilot")
	oldHelper := filepath.Join(root, "versions", oldGeneration.SourceCommit, "libexec", "forgepilot-bootstrap")
	newExecutable := filepath.Join(root, "versions", newGeneration.SourceCommit, "bin", "forgepilot")
	newHelper := filepath.Join(root, "versions", newGeneration.SourceCommit, "libexec", "forgepilot-bootstrap")
	for _, path := range []string{oldExecutable, newExecutable} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("managed CLI"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(newHelper), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newHelper, []byte("managed helper"), 0700); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/bin/sh
[ "$1" = generation-v1 ] && [ "$2" = current ] || exit 9
printf '%%s\n' '{"protocol_version":1,"generation_id":"%s","payload_digest":"%s","forgepilot_path":"%s","helper_path":"%s"}'
`, newGeneration.SourceCommit, newGeneration.PayloadSHA256, newExecutable, newHelper)
	if err := os.MkdirAll(filepath.Dir(oldHelper), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldHelper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("versions/"+newGeneration.SourceCommit, filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "current", "libexec", "forgepilot-bootstrap"), filepath.Join(home, ".local", "bin", "forgepilot-bootstrap")); err != nil {
		t.Fatal(err)
	}

	_, err := (BootstrapGenerationResolver{ProcessImage: BootstrapProcessImage{executablePath: oldExecutable, homePath: home, managedRoot: root}}).Resolve(t.Context())
	if err == nil {
		t.Fatal("resolver accepted the generation selected after its process started")
	}
}

func TestBootstrapGenerationResolverRejectsDuplicateJSONKeys(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".local", "share", "forgepilot")
	generation := work.ExecutionEngineGeneration{SourceCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PayloadSHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	executable := filepath.Join(root, "versions", generation.SourceCommit, "bin", "forgepilot")
	helper := filepath.Join(root, "versions", generation.SourceCommit, "libexec", "forgepilot-bootstrap")
	for _, path := range []string{executable, helper} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("managed"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' '{"protocol_version":1,"protocol_version":1,"generation_id":"%s","payload_digest":"%s","forgepilot_path":"%s","helper_path":"%s"}'
`, generation.SourceCommit, generation.PayloadSHA256, executable, helper)
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("versions/"+generation.SourceCommit, filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "current", "libexec", "forgepilot-bootstrap"), filepath.Join(home, ".local", "bin", "forgepilot-bootstrap")); err != nil {
		t.Fatal(err)
	}
	if _, err := (BootstrapGenerationResolver{ProcessImage: BootstrapProcessImage{executablePath: executable, homePath: home, managedRoot: root}}).Resolve(t.Context()); err == nil {
		t.Fatal("resolver accepted duplicate JSON keys")
	}
}
