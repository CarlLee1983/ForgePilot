# Story: FP-54 enforce authorization on charged foreground runs

## Goal

Every direct-run and exact-run-resume entry starts work only under the same
current authorization and durable pre-charge, so caps remain truthful across
runs and crashes.

## Context

ADR-0035 requires consumption before launch and makes the authorization ledger,
not Run Record/log inference, the cross-run budget authority. Manual work,
verification, review, and governance keep their existing contracts.

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
* Boundary: `ExecutionLedger`
* Boundary: `RunnerAdmission`
* Contract: `durable authorization charge precedes every worker launch across all run entries`
* Owner: `ExecutionLedger = ForgePilot execution-governance`
* Owner: `RunnerAdmission = internal/app`

## Risk

* Level: high
* Reason: `concurrency`

## Concurrency

* Contended resource: `Goal authorization ledger and pending run ownership`
* Linearization point: `durable charged preparation before worker launch`
* Conflict outcome: `inconsistent or unconfirmed preparation refuses free retry`
* Evidence AC: `AC-003`

## Scope

### In Scope

* Authorization admission for direct `run` and exact `run resume <run-id>`.
* Durable idempotent run/action reservations before implementation or repair
  worker launch, cross-run caps for runs/steps/per-node technical attempts, and
  crash-consistent linkage among ledger, Goal witness, Run Record, and Pending.

### Out of Scope

* Plan adoption/revision, authorization-level resume, automatic rollover, and
  stop/wait controls.
* Applying authorization to manual work, verification, review, or governance.

## Inputs

* Current Goal authorization, ledger, Worker Profile, Run Record/Pending facts,
  and requested direct-run or exact-run-resume action.

## Outputs

* Either a durably charged runnable preparation or a refusal before worker
  launch, with existing runner ownership and lifecycle boundaries preserved.

## Rules

* R1: A run and every implementation/repair action reserve durably and
  idempotently before launch; creating/resuming a run cannot reset total caps.
* R2: Missing, expired, profile-mismatched, or accounting-inconsistent
  authorization refuses before launch. Unconfirmed crash consumption is not
  refunded or retried for free.
* R3: Ledger, witness, Run Record, and Pending ordering fails closed; Run Record
  never becomes a substitute budget authority.
* R4: Existing exact-run resume remains exact-run: it cannot extend that run's
  deadline or rewrite its budget.

## Expected Errors

* Invalid authorization or charged-preparation inconsistency prevents worker
  launch and retains enough durable state to prevent an uncharged retry.

## Dependencies

* FP-53 and ADR-0035; FP-55 extends continuation rules.

## Constraints

* `internal/runner` records execution history and requests typed actions; it
  does not own Work Item lifecycle or calculate an independent action list.
