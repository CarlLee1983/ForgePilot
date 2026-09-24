# Acceptance Criteria

## Happy Path

* [x] AC-001: Implementation and repair launches resolve only the authorized provider, executable,
  model, effort, permissions, and immutable engine generation.
* [x] AC-002: An authorization or supervised job acquires its opaque generation-retention marker before
  referencing the generation and releases it only when no active ownership remains.

## Business Rules

* [x] AC-003: Prune and uninstall preserve generations with active or uncertain markers.
* [x] AC-004: An engine revision is accepted only after persisted pause intent, confirmed cleanup,
  compatibility success, and explicit authorization revision; existing runs keep their old binding.

## Failure Cases

* [x] AC-005: Executable, provider, model, effort, permission, profile, or engine drift rejects before
  launch and never silently uses a runtime default.
* [x] AC-006: Missing, corrupt, or uncertain retention acquisition rejects engine use and does not
  delete the referenced generation.

## Regression Requirements

* [x] AC-007: ForgePilot does not read or copy runtime credentials into typed authorization, Run
  control fields, or retention markers. Worker-controlled output and derived text are explicitly
  untrusted; credentials printed by a worker may appear in session logs and downstream artifacts.

## Acceptance Evidence

| AC | Method | Evidence | Fixture / precondition | Expected observation |
| --- | --- | --- | --- | --- |
| `AC-001` | test | `internal/agent/agent_test.go`, `internal/app/charged_run_test.go`, `internal/runner/charged_recovery_test.go` | `approved pinned profile and failing make verify fixture` | `implementation and repair launch separate real subprocesses with the same pinned model, effort, sandbox, authorization digest, and engine tuple` |
| `AC-002` | test | `go test ./internal/app ./internal/storage` | `active authorization fixture` | `marker write precedes engine reference and release follows ownership cleanup` |
| `AC-003` | test | `scripts/forgepilot-bootstrap_test.sh` | `active marker, preexisting removal staging, exact-commit uninstall, two-candidate prune, and interrupted removal fixtures` | `retained/current/previous generations survive approved removals; matching fresh approval resumes only recorded candidates` |
| `AC-004` | test | `internal/runner/execution_engine_revision_integration_test.go`, `internal/app/execution_engine_revision_test.go` | `persisted pause, two charged historical Run Records with ledger receipts, real cleanup auditor` | `unsettled historical ownership rejects revision; clean audit publishes a revised authorization while old authorization and Run bindings stay unchanged` |
| `AC-005` | test | `internal/agent/agent_test.go`, `internal/app/engine_binding_test.go`, `internal/runner/charged_recovery_test.go` | `one-field real-subprocess charged Runner drift matrix` | `provider, executable path, model, effort, sandbox permission, engine source commit, and payload digest each reject before the worker sentinel is written` |
| `AC-006` | test | `go test ./internal/app ./internal/storage` and `scripts/forgepilot-bootstrap_test.sh` | `missing and corrupt marker fixtures with earlier valid removal approvals` | `acquisition and engine use fail closed; approved prune and uninstall refuse deletion without a valid retention store` |
| `AC-007` | test | `internal/runner/charged_recovery_test.go`, `scripts/forgepilot-bootstrap_test.sh` | `silent and credential-printing real subprocesses; retention acquisition` | `runtime sentinel is absent from typed authorization, Run control record, handoff, and retention marker; a printed sentinel enters session.log as documented` |

## Security Fixture Matrix

| Source field | Payload | Expected result | Persisted locations | Verification |
| --- | --- | --- | --- | --- |
| `authorization.worker_profile` | `codex:/opt/codex:fixed-model:medium:workspace-write` | preserve | `authorization version` | `go test ./internal/agent ./internal/app` |
| `authorization.engine_generation` | `<full-source-commit, sha256-payload-digest>` | preserve | `authorization version and run binding` | `go test ./internal/app ./internal/runner` |
| `runtime.resolved_worker_identity` | `/opt/codex@1.2.0` | reject | `launch boundary` | `go test ./internal/agent ./internal/app` |
| `retention.reference_id` | `deterministic, domain-separated 256-bit owner reference` | redact | `generation retention marker filename holds only its SHA-256 hash` | `go test ./internal/app ./internal/storage` |

## Verification Notes

Focused tests must assert no process starts on drift. FP-61 owns repository-wide full and race gates.
