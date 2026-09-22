# Story: FP-59 supervise pinned jobs across macOS session lifecycle

## Goal

A pinned authorized job remains observable and safely recoverable when a terminal, Main Agent Session,
or read-only UI closes, while respecting macOS login, reboot, sleep/wake, deadline, and ownership limits.

## Context

ADR-0035 makes background control separate from Work Item lifecycle. Before every post-restart, login,
or wake launch, supervision rechecks authorization, profile, engine, deadline, workspace ownership, and
unresolved Pending execution. It never promises execution while logged out or asleep; uncertain workers
and cleanup are fail-closed, and recovery charges are bounded accounting events rather than free retries.

## Classification

* Security sensitive: yes
* Baseline conformance: no
* Task mode: execution

## Authority

* plan: yes
* modify: yes
* add_dependency: no
* migration: no
* commit: no
* push: no
* deploy: no

## Risk

* Level: high
* Reason: `concurrency`
* Reason: `process-ownership`

## Scope

### In Scope

* User-scoped supervised-job persistence, stop/launch race handling, bounded recovery accounting, and
  macOS restart/login/wake preflight.
* Real-subprocess process-group and recovery tests, plus documented native macOS evidence.

### Out of Scope

* Daemon execution while logged out or asleep, new Work Item states, unbounded automatic retry, engine
  profile definition, read-only TUI implementation, and credential management.

## Inputs

* Pinned authorization and engine binding, durable control intent, Pending execution ownership facts,
  deadline, and macOS lifecycle event.

## Outputs

* Durable paused/running/recovery-blocked supervision record, bounded recovery charge, and fail-closed
  launch decision.

## Rules

* R1: Closing an observer does not stop execution; explicit stop persists intent before cancellation.
* R2: Every post-lifecycle launch revalidates authorization, binding, deadline, ownership, and Pending
  execution; an unresolved result blocks a new writer.
* R3: Recovery is finite and charged before execution; it cannot erase a stop, deadline, or uncertainty.

## Expected Errors

* Unknown worker identity, cleanup failure, expired authorization, or unresolved Pending execution blocks
  recovery and launch.
* Stop/launch races resolve to durable intent and never permit two writers.

## Dependencies

* GitHub issues #56 and #58; ADR-0020 and ADR-0035.

## Constraints

* Use real subprocesses for process-group and recovery behavior; fake runtimes alone are insufficient.
* Do not claim uninterrupted execution across logout, reboot, or sleep.

## Guidance

Relevant:

* decision: `ADR-0020` — fail-closed process identity and cleanup
* decision: `ADR-0035` — supervision, lifecycle, and bounded recovery

Not applicable:

* redefining Goal or Work Item lifecycle

## Trust Boundary Fields

* `supervision.intent` — durable user stop or resume instruction.
* `worker.identity` — pid, start time, and command evidence used for ownership verification.
* `macos.lifecycle_event` — restart, login, wake, stop, or launch trigger.
* `authorization.deadline` — fixed expiry checked before any new launch.
