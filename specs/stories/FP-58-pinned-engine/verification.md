# FP-58 Verification

## Result

**FP-58 acceptance evidence complete** — 2026-09-23, working tree atop `da07983`
(product implementation `32b7a68`, v2 Runner fixture repair `ea03e0f`). AC-001
through AC-006 and the user-approved scoped AC-007 have executable evidence.
FP-57 now has source install/upgrade coverage and 35 passing completed-statement
crash boundaries. The V1 removal contract blocks partial deletion for manual
inspection; native Codex procedure acceptance subsequently passed in the
FP-57 disposable-home run on 2026-09-24. This record does
not change the ForgePilot Goal or Work Item lifecycle or authorize a real-model
smoke run.

## Checks

* Before edits, `go test ./internal/runner ./internal/app -count=1` — PASS, exit 0.
* `go test ./internal/agent ./internal/app -count=1` — PASS, exit 0.
* `go test ./internal/app ./internal/storage -count=1` — PASS, exit 0.
* `go test ./internal/app ./internal/runner -count=1` — PASS, exit 0.
* `go test ./internal/runner -run TestEngineRevisionAuditsHistoricalRunsAndPreservesTheirBindings -count=1 -v` — PASS, exit 0 after adding both charged RUN receipts.
* `go test ./internal/runner -run 'TestChargedRepairLaunchUsesPinnedWorkerProfile|TestChargedStartRejectsEachPinnedBindingDriftBeforeWorkerLaunch|TestRuntimeCredentialIsNotCopiedIntoForgePilotArtifacts' -count=1` — PASS, exit 0; the added engine payload case also passed in a subsequent focused drift run.
* On 2026-09-24, `go test ./internal/runner -run 'TestChargedRepairLaunchUsesPinnedWorkerProfile|TestChargedDirectRunAndExactResumeUseTheSameAuthorization' -count=1` — PASS, exit 0 after splitting implementation and repair argv records and checking the pinned options in each.
* On 2026-09-24, `make verify` — PASS, exit 0 after the repair argv assertion was strengthened.
* `sh -n scripts/forgepilot-bootstrap` — PASS, exit 0.
* `sh scripts/forgepilot-bootstrap_test.sh` — PASS, exit 0 after adding approved
  uninstall, multi-candidate prune, first-move recovery, and missing/corrupt
  retention refusal (`retention protocol PASS`).
* `git diff --check` — PASS, exit 0.
* `make verify` — PASS, exit 0 after the removal and documentation changes
  (format, `go vet`, `go test ./...`, CLI build, release, onboarding, and skill regressions).
* `go test -race -count=1 ./...` — PASS, exit 0 on this working tree
  (root integration package 522.520 seconds; no race report).

## Acceptance evidence

* **AC-001 — PASS.** `internal/runner/charged_recovery_test.go` drives a real
  implementation subprocess, a failing formal `make verify`, then a real repair
  subprocess. It checks the repair handoff, two launch observations, the same
  pinned model, effort, and sandbox in both argv streams, and the Run's immutable
  authorization digest and engine tuple. Existing agent and app tests cover the
  launch plan and current authorization binding.
* **AC-002 — PASS.** `internal/app/engine_binding_test.go` and
  `execution_pinned_test.go` cover acquire-before-binding, distinct opaque
  authorization and Run owners, and failed acquisition. The Runner retention
  closure tests cover durable closure before release, retry after failure,
  immutable historical bindings, and refusal while cleanup is uncertain.
* **AC-003 — PASS.** The Bootstrap shell test executes exact-commit uninstall
  and a two-candidate prune in disposable homes. It checks that current,
  previous, and actively retained generations remain installed, rejects their
  exact-commit uninstall plans, and resumes only recorded candidates after an
  interrupted first move with a fresh matching approval.
* **AC-004 — PASS.** `internal/runner/execution_engine_revision_integration_test.go`
  uses a valid Goal Plan, a persisted direct pause, two charged historical Run
  Records with ledger receipts, and the real cleanup auditor. An unsettled
  historical record refuses revision without publishing another authorization. Once settled, the revision
  succeeds; both historical Run bindings and the old authorization stay unchanged,
  and the pause intent remains durable. App and Runner unit tests cover the
  compatibility, retention, and malformed-record edges separately.
* **AC-005 — PASS.** The charged Runner fixture changes provider, executable
  path, model, effort, sandbox permission, engine source commit, and engine
  payload digest one field at a time. Each charged Start refuses before its
  real worker subprocess writes the launch sentinel. Existing agent and app
  tests cover the matching launch plan and binding validation.
* **AC-006 — PASS.** The app tests refuse binding when retention acquisition
  fails. With a previously valid prune approval, the Bootstrap shell test
  corrupts a marker and then removes the retention store in separate fixtures;
  approved prune and uninstall attempts fail before creating a removal
  transaction or deleting the candidate.
* **AC-007 — PASS under the approved source boundary.** The user approved
  limiting the guarantee to credential handling ForgePilot controls. A runtime
  credential sentinel remains absent from typed authorization state, `run.json`,
  and `handoff.md` when a real worker is silent. The Bootstrap retention test
  confirms that the same sentinel is absent from marker bytes. A second worker
  deliberately prints it and the test observes it in `session.log`. Worker
  output and derived summaries, Gates, or later handoffs remain untrusted.

## Related acceptance and limits

FP-57 has source install/upgrade and completed-statement crash injection
coverage. The V1 removal contract blocks partial deletion for manual inspection;
native Codex procedure acceptance passed in the FP-57 disposable-home run on
2026-09-24. PraxisBound's
`review readiness-digests` command refreshed the FP-58 Story and acceptance
digests on 2026-09-24. No migration,
dependency, external write, or `.forgepilot/` lifecycle operation was
performed for this record.
