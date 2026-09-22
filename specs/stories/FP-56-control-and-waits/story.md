# Story: FP-56 persist stop control and human/external waits

## Goal

Users can safely stop and explicitly resume authorized execution, with durable
pause intent and correctly classified human/external waits that survive restart
without becoming Work Item lifecycle states.

## Context

ADR-0035 reserves execution control and worker ownership for ForgePilot while
keeping `internal/work` lifecycle authoritative. A valid `needs_human` result
is not a technical failure; unverified external facts require a bound named
self-declaration and never become Evidence or Gate resolution.

## Classification

* Security sensitive: no
* Baseline conformance: no
* Task mode: execution

## Authority

* plan: yes
* modify: yes
* add_dependency: no
* migration: yes
* commit: no
* push: no
* deploy: no

## Architecture

* Impact: high
* Boundary: `ExecutionControl`
* Boundary: `ExternalFulfillmentDeclaration`
* Contract: `pause intent precedes cancellation and waits never become lifecycle or evidence`
* Owner: `ExecutionControl = ForgePilot execution-governance`
* Owner: `ExternalFulfillmentDeclaration = ForgePilot app-orchestration`

## Risk

* Level: high
* Reason: `concurrency`

## Concurrency

* Contended resource: `persisted pause intent and worker process ownership`
* Linearization point: `durable pause record before cancellation request`
* Conflict outcome: `restart/recovery retains stop and requires explicit resume`
* Evidence AC: `AC-003`

## Scope

### In Scope

* Durable stop intent before cancellation, restart/recovery behavior, explicit
  authorized resume, and worker-cleanup preservation.
* Single validated `needs_human` classification without technical-attempt charge.
* External Fulfillment Declarations bound to named fact, wait, node, plan digest,
  and authorization revision for an explicit resume recheck.

### Out of Scope

* New `WAITING_HUMAN`/`BLOCKED` Work Item states, Gate-option fabrication, or
  implicit continuation after stop/wait/restart.
* Treating declarations as Verification Evidence, Gate resolution, Human final
  acceptance, or verified external fact.

## Inputs

* Authorized execution control request, worker ownership/recovery facts, agent
  result, and optional named external declaration.

## Outputs

* Persisted pause/wait control state and an explicit-resume decision, retaining
  existing logs, Evidence, work modifications, and ownership safeguards.

## Rules

* R1: Stop records durable pause intent before cancellation. Restart, recovery,
  logout, reboot, or supervisor restart never clear it or auto-continue.
* R2: Only a validated `needs_human` result is classified once and does not
  consume a technical attempt; malformed, missing, or crashed results retain
  their charge.
* R3: A declaration names its fact and binds exact wait/node/plan digest/
  authorization revision plus self-declared person. It expires on plan or
  authorization revision change and never resumes implicitly.
* R4: Resume rechecks current authorization, plan, pause reason, and existing
  process/recovery protection before taking any action.

## Expected Errors

* Unconfirmed worker ownership, malformed agent result, stale declaration, or
  unmet resume condition remains stopped and fails closed.

## Dependencies

* FP-55 and ADR-0035.

## Constraints

* Supervisor/control data is execution history/control only; it does not save
  Work Item lifecycle or calculate legal domain actions.
