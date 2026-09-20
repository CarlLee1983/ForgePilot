# Acceptance Criteria

## Happy Path

* [ ] AC-001: Implementation and repair launches resolve only the authorized provider, executable,
  model, effort, permissions, and immutable engine generation.
* [ ] AC-002: An authorization or supervised job acquires its opaque generation-retention marker before
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
| `AC-001` | test | `go test ./internal/agent ./internal/app` | `approved pinned profile fixture` | `launch receives exactly authorized worker and engine values` |
| `AC-002` | test | `go test ./internal/app ./internal/storage` | `active authorization fixture` | `marker write precedes engine reference and release follows ownership cleanup` |
| `AC-003` | test | `scripts/forgepilot-bootstrap_test.sh` | `active and uncertain marker fixtures` | `prune and uninstall retain referenced generations` |
| `AC-004` | test | `go test ./internal/app ./internal/runner` | `paused revision fixture with historical run` | `revision requires all preconditions and leaves old run binding unchanged` |
| `AC-005` | test | `go test ./internal/agent ./internal/app` | `one-field drift fixtures` | `fail-closed result occurs before worker process launch` |
| `AC-006` | test | `go test ./internal/app ./internal/storage` | `missing and corrupt marker fixtures` | `engine use and deletion are both blocked` |
| `AC-007` | test | `go test ./internal/agent ./internal/storage` | `credential sentinel profile fixture` | `persisted and emitted records omit credential values` |

## Security Fixture Matrix

| Source field | Payload | Expected result | Persisted locations | Verification |
| --- | --- | --- | --- | --- |
| `authorization.worker_profile` | `codex:/opt/codex:fixed-model:medium:workspace-write` | preserve | `authorization version` | `go test ./internal/agent ./internal/app` |
| `authorization.engine_generation` | `<full-source-commit, sha256-payload-digest>` | preserve | `authorization version and run binding` | `go test ./internal/app ./internal/runner` |
| `runtime.resolved_worker_identity` | `/opt/codex@1.2.0` | reject | `launch boundary` | `go test ./internal/agent ./internal/app` |
| `retention.reference_id` | `256-bit random reference` | redact | `SHA-256-only generation retention marker` | `go test ./internal/app ./internal/storage` |

## Verification Notes

Focused tests must assert no process starts on drift. FP-61 owns repository-wide full and race gates.
