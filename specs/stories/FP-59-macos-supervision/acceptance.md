# Acceptance Criteria

## Happy Path

* [ ] AC-001: After restart, login, or wake, a user-scoped supervisor rechecks authorization, profile,
  engine, deadline, workspace ownership, and unresolved Pending execution before any launch.
* [ ] AC-002: Terminal, Main Agent Session, and read-only UI closure leave an authorized running job
  running; explicit stop persists pause intent before process cancellation and cleanup.

## Business Rules

* [ ] AC-003: Recovery attempts are bounded and charged before work; confirmed `needs_human` retains
  its existing non-technical-attempt rule.
* [ ] AC-004: Sleep, logout, and reboot preserve state for a later eligible recovery but do not claim
  execution continued while asleep or logged out.

## Failure Cases

* [ ] AC-005: Uncertain worker identity, unresolved cleanup, ownership conflict, expired deadline, or
  unresolved Pending execution blocks new writers and reports recovery blocked.
* [ ] AC-006: Concurrent stop and launch cannot produce two writers or clear persisted stop intent.

## Regression Requirements

* [ ] AC-007: Process-group and recovery behavior is exercised with real subprocesses, not only mocks.
* [ ] AC-008: Native macOS login, reboot, and sleep/wake observations are recorded with their limits.

## Acceptance Evidence

| AC | Method | Evidence | Fixture / precondition | Expected observation |
| --- | --- | --- | --- | --- |
| `AC-001` | test | `go test ./internal/runner ./internal/agent` | `restart login and wake event fixtures` | `all required preflight facts are checked before launch` |
| `AC-002` | test | `go test ./internal/runner` | `observer close and explicit stop fixtures` | `observer closure preserves run; stop persists before cancellation` |
| `AC-003` | test | `go test ./internal/runner ./internal/storage` | `bounded recovery ledger fixture` | `recovery is precharged and finite while needs_human is not technical attempt` |
| `AC-004` | human | `docs/operations/macos-supervision-acceptance.md` | `native macOS login reboot sleep/wake session` | `documented observations make no uninterrupted-execution claim` |
| `AC-005` | test | `go test ./internal/runner ./internal/agent` | `unknown worker cleanup ownership deadline and pending fixtures` | `new writer is blocked fail-closed` |
| `AC-006` | test | `go test ./internal/runner -race` | `concurrent stop and launch real-subprocess fixture` | `one durable intent and no concurrent writer` |
| `AC-007` | test | `go test ./internal/runner` | `real process-group and crash-recovery fixture` | `subprocess ownership and cleanup assertions pass` |
| `AC-008` | human | `docs/operations/macos-supervision-acceptance.md` | `Apple Silicon macOS host` | `login reboot and wake outcomes and limitations are recorded` |

## Security Fixture Matrix

| Source field | Payload | Expected result | Persisted locations | Verification |
| --- | --- | --- | --- | --- |
| `supervision.intent` | `stop` | preserve | `supervision control record` | `go test ./internal/runner` |
| `worker.identity` | `12345:1700000000:/opt/codex` | reject | `launch boundary` | `go test ./internal/runner ./internal/agent` |
| `macos.lifecycle_event` | `wake` | preserve | `supervision event record` | `go test ./internal/runner` |
| `authorization.deadline` | `2026-09-20T00:00:00Z` | reject | `launch boundary` | `go test ./internal/runner` |

## Verification Notes

The `-race` command in AC-006 is focused evidence; FP-61 runs the repository-wide race gate.
