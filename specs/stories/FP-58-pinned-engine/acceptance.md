# Acceptance Criteria

## Happy Path

* [ ] AC-001: Implementation and repair launches resolve only the authorized provider, executable,
  model, effort, permissions, and immutable engine generation.
* [x] AC-002: An authorization or supervised job acquires its opaque generation-retention marker before
  referencing the generation and releases it only when no active ownership remains.

## Business Rules

* [ ] AC-003: Prune and uninstall preserve generations with active or uncertain markers.
* [ ] AC-004: An engine revision is accepted only after persisted pause intent, confirmed cleanup,
  compatibility success, and explicit authorization revision; existing runs keep their old binding.

## Failure Cases

* [ ] AC-005: Executable, provider, model, effort, permission, profile, or engine drift rejects before
  launch and never silently uses a runtime default.
* [ ] AC-006: Missing, corrupt, or uncertain retention acquisition rejects engine use and does not
  delete the referenced generation.

## Regression Requirements

* [ ] AC-007: Worker credentials are not persisted in authorization, run state, logs, or markers.

## Acceptance Evidence

| AC | Method | Evidence | Fixture / precondition | Expected observation |
| --- | --- | --- | --- | --- |
| `AC-001` | test | `internal/agent/agent_test.go`, `internal/app/charged_run_test.go`, `internal/runner/charged_recovery_test.go` | `approved pinned profile fixture` | `implementation launch uses the pinned profile; a repair worker launch asserting the same binding remains to be tested` |
| `AC-002` | test | `go test ./internal/app ./internal/storage` | `active authorization fixture` | `marker write precedes engine reference and release follows ownership cleanup` |
| `AC-003` | test | `scripts/forgepilot-bootstrap_test.sh` | `active and uncertain marker fixtures` | `prune planning excludes retained generations; prune and uninstall execution remain unavailable, so this AC is pending` |
| `AC-004` | test | `go test ./internal/app ./internal/runner` | `paused revision fixture and separate historical-run audit fixtures` | `pause, audit, and retention boundaries pass separately; a combined engine revision with historical Run remains to be tested` |
| `AC-005` | test | `internal/agent/agent_test.go`, `internal/app/engine_binding_test.go`, `internal/runner/charged_recovery_test.go` | `existing profile and engine drift fixtures` | `current mismatch cases reject; a one-field-before-launch matrix for every listed field remains to be tested` |
| `AC-006` | test | `go test ./internal/app ./internal/storage` and `scripts/forgepilot-bootstrap_test.sh` | `missing and corrupt marker fixtures` | `acquisition and engine use fail closed; deletion cannot yet be verified because lifecycle removal is unavailable` |
| `AC-007` | review | `specs/stories/FP-58-pinned-engine/verification.md` | `runtime-owned credentials and worker output` | `ForgePilot does not place credentials in the profile, but no credential-sentinel fixture proves all listed artifacts; worker-emitted output can enter session logs, so this AC is pending` |

## Security Fixture Matrix

| Source field | Payload | Expected result | Persisted locations | Verification |
| --- | --- | --- | --- | --- |
| `authorization.worker_profile` | `codex:/opt/codex:fixed-model:medium:workspace-write` | preserve | `authorization version` | `go test ./internal/agent ./internal/app` |
| `authorization.engine_generation` | `<full-source-commit, sha256-payload-digest>` | preserve | `authorization version and run binding` | `go test ./internal/app ./internal/runner` |
| `runtime.resolved_worker_identity` | `/opt/codex@1.2.0` | reject | `launch boundary` | `go test ./internal/agent ./internal/app` |
| `retention.reference_id` | `deterministic, domain-separated 256-bit owner reference` | redact | `generation retention marker filename holds only its SHA-256 hash` | `go test ./internal/app ./internal/storage` |

## Verification Notes

Focused tests must assert no process starts on drift. FP-61 owns repository-wide full and race gates.
