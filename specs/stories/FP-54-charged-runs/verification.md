# FP-54 Verification

## Result

**PARTIAL** — 2026-09-22. Verification applies to the shared dirty working
tree observed during this session; it is not attached to the unchanged HEAD
revision. No commit, push, Runner lifecycle start, or Goal lifecycle write was
performed.

## Checks

* focused charged recovery tests: pass —
  `GOCACHE=/private/tmp/forgepilot-fp54.uc6gyy/gocache GOMODCACHE=/Users/carl/go/pkg/mod go test ./internal/runner -run '^(TestChargedHumanDispositionSurvivesCrashAndReconcilesOnce|TestLegacyRunIntentDoesNotAdoptAuthorizationAddedAfterCrash|TestChargedWorkerIdentityCrashRecoversWithoutDuplicateLaunch|TestStateDigestFailureKeepsTheAttemptWithoutAPhantomWorker)$' -count=1 -timeout=180s`
* affected packages: pass — `go test ./internal/work ./internal/app -count=1`
* full repository gate: pass on the current tree —
  `GOCACHE=/private/tmp/forgepilot-fp54.uc6gyy/gocache GOMODCACHE=/Users/carl/go/pkg/mod make verify`
* formatting and whitespace: pass — `gofmt -d` and `git diff --check`
* race gate: incomplete — `go test -race -count=1 ./...` ran for 10 minutes
  and timed out in the existing root integration test
  `TestRecoveryNeitherSignalsNorTrustsAReusedPid`; all listed internal
  packages completed and no race report was emitted.

## Evidence

* `AC-001`: partial — direct start and exact resume use the same current
authorization and durable RUN/ACTION/STEP receipts; subprocess fixtures
prove charge-before-launch and exact replay. Production runtime identity
resolution remains a later FP-58 boundary, so the test seam is not evidence
of native macOS identity coverage.
* `AC-002`: pass for covered charged paths — cumulative RUN, STEP, and
per-node technical-attempt accounting remains ledger-owned across exact
resumes and confirmed `needs_human` dispositions; inflated local
`HumanWaits` counts now fail closed.
* `AC-003`: pass for covered fixtures — unresolved identity, forged receipts,
stale legacy authorization added after a `PENDING_CLASSIFICATION` crash,
missing state, and charged preparation failures refuse before worker launch
without a free retry.
* `AC-004`: pass for covered ordering boundaries — real subprocess fixtures
cover pre-charge, post-charge, Run Record, step receipt, pending ownership,
worker identity, `needs_human` result recording, and ledger disposition
boundaries; recovery preserves ownership or stops fail closed.
* `AC-005`: partial — manual/non-run contracts and exact-run budget/deadline
preservation pass their focused and full-gate tests; the required current
tree race gate timed out in an unrelated integration fixture.

## Residual Risks

* The shared checkout contains unrelated concurrent product and documentation
  changes; this verification does not certify those changes independently.
* Native macOS process identity acceptance remains outside the completed
  evidence, and the repository race gate needs a separate timeout diagnosis.
* FP-55 plan revision is intentionally out of FP-54 scope; charged Runner
  admission still requires one current authorization revision and refuses
  inconsistent multi-revision charged state.

## Worktree and authorization

FP-54 grants plan, modify, and migration authority. No dependency, migration
execution, commit, push, deployment, or lifecycle action was performed.
