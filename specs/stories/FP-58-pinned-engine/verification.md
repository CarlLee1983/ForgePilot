# FP-58 Verification

## Result

**PARTIAL** — 2026-09-23, at committed checkpoint `ea03e0f` (FP-58 product
implementation `32b7a68` plus v2 Runner fixture repair). All required
focused checks pass. AC-002 has complete acceptance evidence; the other ACs
remain open in whole or in part for the reasons below. This record does not change the ForgePilot
Goal or Work Item lifecycle and does not authorize a real-model smoke run.

## Checks

* `go test ./internal/agent ./internal/app -count=1` — PASS, exit 0.
* `go test ./internal/app ./internal/storage -count=1` — PASS, exit 0.
* `go test ./internal/app ./internal/runner -count=1` — PASS, exit 0.
* `sh -n scripts/forgepilot-bootstrap` — PASS, exit 0.
* `sh scripts/forgepilot-bootstrap_test.sh` — PASS, exit 0 (`retention protocol PASS`).
* The repository integration gates had already passed on the same committed
  source before this documentation-only update: `make verify` and
  `go test -race -count=1 ./...`, both exit 0. FP-61 owns their formal final
  acceptance; the focused commands above are FP-58's required checks.

## Acceptance evidence

* **AC-001 — PARTIAL.** `internal/agent/agent_test.go` checks the authorized Codex
  model, effort, and sandbox in the launch plan. `internal/app/charged_run_test.go`
  binds the launch to the current authorization. Real-subprocess charged Runner
  tests in `internal/runner/charged_recovery_test.go` assert the requested
  profile reaches an implementation worker and rejects mismatch before another
  launch. Repair routes through the same production implementation function,
  but no test launches a repair worker and asserts its full pinned binding.
* **AC-002 — PASS.** `internal/app/engine_binding_test.go` and
  `execution_pinned_test.go` cover acquire-before-binding, distinct opaque
  authorization and Run owners, and failed acquisition. The Runner retention
  closure tests cover durable closure before release, retry after failure,
  immutable historical bindings, and refusal while cleanup is uncertain.
* **AC-003 — PENDING.** `scripts/forgepilot-bootstrap_test.sh` proves `plan
  --prune` excludes retained generations and refuses malformed retention
  state. `scripts/forgepilot-bootstrap` still rejects `prune` and `uninstall`
  execution as unavailable, so actual preservation during those operations
  has no executable acceptance evidence. FP-57 records the same lifecycle gap.
* **AC-004 — PARTIAL.** `internal/app/execution_engine_revision_test.go` covers
  matching persisted pause, audit invocation, legacy-sidecar refusal,
  acquire-before-publication, state-save failure, and unchanged old
  authorization. `internal/runner/cleanup_audit_test.go` separately rejects
  forged, missing, malformed, or unsettled historical Run ownership. The
  engine revision fixture supplies an audit proof without a historical Run;
  the combined revision-plus-historical-Run path remains untested.
* **AC-005 — PARTIAL.** `internal/app/charged_run_test.go`,
  `engine_binding_test.go`, and `internal/runner/charged_recovery_test.go`
  cover selected profile and engine tuple drift; the assertions require refusal
  before a new worker session starts. The charged Runner mismatch fixture
  changes model and effort together. A before-launch matrix changing provider,
  executable, model, effort, and permission one at a time is still missing.
* **AC-006 — PARTIAL.** The app tests refuse binding when retention acquisition
  fails; the Bootstrap shell test refuses acquisition and prune planning for
  malformed or missing retention state. Actual deletion is unavailable, so
  the no-deletion half of this AC cannot yet be observed.
* **AC-007 — PENDING.** No credential-sentinel fixture checks authorization,
  Run Record, handoff, session log, and marker bytes together. Existing tests
  prove raw *retention references* are omitted, which is a separate fact.
  ForgePilot streams worker stdout/stderr into `session.log`; a worker that
  prints a credential can therefore put it in a log. The literal AC needs an
  explicit boundary decision and a corresponding executable test.

## Remaining work

FP-57's prune and uninstall lifecycle must exist before AC-003 and the
deletion portion of AC-006 can be tested. AC-001 needs a repair launch binding
assertion; AC-004 needs an engine revision test with a real historical Run;
AC-005 needs one-field drift refusals. AC-007 needs a scoped guarantee for
ForgePilot-authored data versus arbitrary worker output, followed by a
credential-sentinel test. Until those conditions are met, FP-58 is not fully
accepted. No migration, dependency, external write, or `.forgepilot/` lifecycle
operation was performed for this record.
