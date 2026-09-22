# Acceptance Criteria

## Happy Path

* [ ] AC-001: CLI JSON preflight/status, dry-run, durable handoff, and read-only TUI consume one
  versioned projection for the complete DAG, dependencies, action/attempts, Gates, caps, consumption,
  stops, paths, and completion boundaries.
* [ ] AC-002: The projection distinguishes machine PASS, fresh PASS, and Human acceptance and exposes
  known worker facts with their source and observed time.

## Business Rules

* [ ] AC-003: Repeated observations start no subprocess and write no state, locks, logs, Git objects,
  recovery data, or other files.
* [ ] AC-004: Unprobed or changing external facts are `unknown` or historical with provenance/time;
  missing paths are explicitly visible as missing.
* [ ] AC-005: Concurrent reads never mix revisions, and closing an observer never stops execution.

## Failure Cases

* [ ] AC-006: Unsupported projection schema, unreadable required state, or inconsistent read retry
  exhaustion returns a typed read-only error with no side effect.

## Regression Requirements

* [ ] AC-007: Existing dry-run no longer calls runtime `Version()` or any executable, Git, canonical
  check, ownership, or recovery operation.

## Acceptance Evidence

| AC | Method | Evidence | Fixture / precondition | Expected observation |
| --- | --- | --- | --- | --- |
| `AC-001` | test | `go test ./internal/app ./internal/cli ./internal/runner` | `complete DAG projection fixture` | `all consumers serialize the same versioned semantic projection` |
| `AC-002` | test | `go test ./internal/app ./internal/cli` | `machine fresh and human decision fixtures` | `three completion states and observation provenance remain distinct` |
| `AC-003` | test | `go test ./internal/app ./internal/cli ./internal/runner` | `subprocess and filesystem sentinel fixture` | `repeated reads create no process or write` |
| `AC-004` | test | `go test ./internal/app ./internal/cli` | `unprobed changing and missing path fixtures` | `output reports unknown historical and missing values explicitly` |
| `AC-005` | test | `go test ./internal/app ./internal/runner -race` | `concurrent readers and active-run fixture` | `each observation is one revision and observer close does not stop run` |
| `AC-006` | test | `go test ./internal/app ./internal/cli` | `unsupported unreadable and changing-state fixtures` | `typed failure occurs without process or write` |
| `AC-007` | test | `go test ./internal/runner ./internal/agent` | `dry-run executable invocation sentinel` | `dry-run returns projection without invoking Version or executable` |

## Verification Notes

The focused race test proves projection concurrency. Repository-wide full and race gates are FP-61
closure evidence.
