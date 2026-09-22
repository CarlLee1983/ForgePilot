# Acceptance Criteria

## Happy Path

* [x] AC-001: Valid chain, diamond, and fan-in reviewed plans each return one versioned JSON projection containing full topology, source/digest bindings, coverage-approval binding, and Goal registration compatibility.

## Business Rules

* [x] AC-002: The projection labels directly read facts as observed and candidate freshness, worker/runtime state, and domain next action as unprobed or unavailable; it never reports those as live facts.

## Failure Cases

* [x] AC-003: Missing or duplicate nodes, bad edges, cycles, source drift, approval drift, or mapping mismatch fail closed with diagnostics and no mutation.
* [x] AC-004: Instrumented invocation proves preflight writes no files or state and starts no Git, runtime, canonical-check, recovery, or other subprocess.

## Regression Requirements

* [x] AC-005: The implementation does not parse Story Markdown, claim Plan Coverage Review from manifest equality, or add a Work Item lifecycle authority outside `internal/work`.

## Acceptance Evidence

| AC | Method | Evidence | Fixture / precondition | Expected observation |
| --- | --- | --- | --- | --- |
| `AC-001` | test | `internal/app/preflight_test.go` | `chain-diamond-fanin-fixtures` | `versioned-json-projections` |
| `AC-002` | test | `internal/app/preflight_test.go` | `unprobed-fact-fixture` | `explicit-observation-statuses` |
| `AC-003` | test | `internal/app/preflight_test.go` | `invalid-plan-binding-fixtures` | `fail-closed-without-mutation` |
| `AC-004` | test | `internal/cli/preflight_test.go` | `write-and-subprocess-spies` | `zero-writes-and-zero-launches` |
| `AC-005` | test | `internal/app/preflight_test.go` | `ownership-regression-fixture` | `no-semantic-inference-or-lifecycle-write` |

## Verification Notes

Run the focused preflight tests and the repository documentation/change-surface
checks required by the development-plan matrix; full integration gates remain
the primary workflow's responsibility.
