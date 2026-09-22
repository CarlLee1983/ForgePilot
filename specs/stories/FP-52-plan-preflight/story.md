# Story: FP-52 reviewed Goal Plan pure preflight

## Goal

Operators can inspect a supplied reviewed Goal Plan before adoption and receive a
versioned JSON projection of its structural and binding facts, without changing
ForgePilot state or executing anything.

## Context

ADR-0035 assigns PraxisBound ownership of the Goal Plan Manifest and Plan
Coverage Review. ForgePilot may validate their structure, raw-byte/digest
bindings, and complete Goal registration; it must not infer requirement
coverage from Markdown or manifest-set equality.

## Classification

* Security sensitive: no
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

## Scope

### In Scope

* `goal preflight --request --json` as a pure, versioned projection of one
  supplied reviewed plan.
* Complete-DAG validation for chain, diamond, and fan-in topology; Plan Node
  Reference uniqueness; edge validity and acyclicity; declared Story source
  digest and coverage-approval binding; and exact Goal Work Item/node mapping.
* Explicit `observed`, `unprobed`, or `unavailable` facts in the JSON result.

### Out of Scope

* Persisting a manifest, node mapping, authorization, ledger, Run Record, or
  Evidence; those begin in FP-53.
* Re-evaluating PraxisBound requirement semantics, parsing Story Markdown, or
  treating registration equality as Plan Coverage Review.
* Git, runtime, canonical-check, recovery, or any subprocess probe.

## Inputs

* A Goal identifier, workspace, Goal Plan Manifest, and binding Plan Coverage
  Review supplied by the caller.
* Directly readable ForgePilot state and local artifact bytes.

## Outputs

* One versioned JSON preflight projection containing diagnostics and each fact's
  observation status; no state or filesystem mutation.

## Rules

* R1: The manifest must cover the full registered Work Item DAG; every Plan
  Node Reference maps one-to-one to a Work Item, while multiple nodes may cite
  one Story.
* R2: Missing/duplicate nodes, nonexistent or mismatched edges, cycles, source
  digest drift, and coverage-approval drift fail closed.
* R3: Only directly readable bytes and state may be reported observed. Candidate
  freshness, worker liveness, runtime availability, and authoritative next
  action are unprobed or unavailable, never claimed current.
* R4: This path writes no file/state and launches no subprocess, including Git,
  runtime version, canonical-check, or recovery commands.

## Expected Errors

* Malformed input, absent artifacts, or any invalid binding produces a
  fail-closed JSON diagnostic with no partial adoption.

## Dependencies

* ADR-0035 and GitHub issue #52; FP-53 consumes the validated projection.

## Constraints

* Preserve `internal/work` as the sole owner of Work Item lifecycle and typed
  next actions; this projection is neither a Gate, Evidence, nor a PASS.

## Guidance

Relevant:

* decision: ADR-0035 ownership split and pure-inspection boundary.

Not applicable:

* requirement-coverage inference.
