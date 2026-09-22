# Story: FP-60 expose one pure progress projection and read-only consumers

## Goal

Preflight, status, dry-run, durable handoff, and the first read-only TUI present the same typed,
versioned, optimistic execution projection without doing work or pretending stale facts are current.

## Context

ADR-0035 requires one app-owned inspection source for the full DAG and execution boundaries. Inspection
does not write, take ownership, recover, probe runtime, run Git, or start a subprocess. It shows
observable facts with provenance and time; externally refreshed or unprobed facts are historical or
unknown. It is not a second Work Item lifecycle or a frozen future action plan.

## Classification

* Security sensitive: no
* Baseline conformance: yes
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
* Reason: `error-projection`
* Reason: `concurrency`

## Scope

### In Scope

* One versioned typed projection for complete DAG, dependencies, current action/attempts, Gates, caps,
  consumption, stop reasons, paths, worker observations, and machine/fresh/Human completion boundaries.
* CLI JSON preflight/status, dry-run, durable handoff, and read-only TUI consumers of that projection.
* Optimistic consistent reads, explicit missing paths, provenance/timestamps, and no-side-effect tests.

### Out of Scope

* Launching, recovery, runtime/Git probes, state mutation, TUI control operations, or new lifecycle
  authority.

## Inputs

* Persisted plan, coverage approval, registration, authorization, ledger, run history, and directly
  readable local paths.

## Outputs

* A versioned read-only projection and consumers with identical semantic fields and explicit observation
  freshness.

## Rules

* R1: Consumers use app projection, not independently reconstructed progress or next-action logic.
* R2: Inspection writes nothing and starts no subprocess, including runtime version, Git, canonical
  verification, lock, or recovery activity.
* R3: A changing/unprobed fact reports source and observation time or `unknown`; missing paths remain
  visible as missing.

## Expected Errors

* Inconsistent concurrent snapshot, unsupported projection version, or unreadable required state returns
  a typed inspection failure without side effects.

## Dependencies

* GitHub issues #53, #56, and #59; ADR-0035.

## Constraints

* Read-only TUI owns no control action; closing any observer cannot stop execution.
* Machine PASS, fresh PASS, and Human acceptance remain distinct fields and claims.

## Guidance

Relevant:

* decision: `ADR-0035` — single pure inspection source and no-subprocess boundary

Not applicable:

* persisted Work Item lifecycle projection

## Superseded Behavior

* `internal/runner/runner.go` dry-run runtime `Version()` probe — dry-run becomes pure inspection and
  must not execute the worker executable.
