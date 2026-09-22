# Story: FP-53 atomically adopt and authorize a reviewed Goal Plan

## Goal

An operator can preview then explicitly authorize an initial reviewed Goal Plan,
atomically binding every node to one Work Item and creating its first bounded
Execution Authorization and ledger.

## Context

ADR-0035 separates PraxisBound's manifest and coverage approval from
ForgePilot's execution authorization. Adoption must not guess a node from a
Story path or allow a partial mapping/ledger transaction.

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
* Boundary: `ExecutionAuthorization`
* Boundary: `GoalPlanBinding`
* Contract: `adoption publishes mapping authorization ledger and Goal witness in one durable transaction`
* Owner: `ExecutionAuthorization = ForgePilot execution-governance`
* Owner: `GoalPlanBinding = ForgePilot app-orchestration`

## Risk

* Level: high
* Reason: `concurrency`

## Concurrency

* Contended resource: `Goal execution authorization state`
* Linearization point: `atomic storage update publishing revision one`
* Conflict outcome: `stale or conflicting approval refuses without persistence`
* Evidence AC: `AC-004`

## Scope

### In Scope

* Pure `execution plan` preview and approval token bound to request bytes,
  artifact digests, Goal/workspace, and observed authorization state.
* `execution authorize` with explicit self-declared approver, complete explicit
  Plan Node Reference-to-Work Item mapping, revision-one caps/expiry/Worker
  Profile, ledger, and Goal witness in one transaction.

### Out of Scope

* Worker launch, charged actions, rollover, plan revision, or pause control.
* PraxisBound export format ownership or requirement-coverage review.

## Inputs

* A current FP-52-valid reviewed plan, explicit mapping, bounded caps, fixed
  expiry, Worker Profile, and approver self-declaration.

## Outputs

* A preview token or one atomically persisted initial plan binding,
  authorization revision, ledger, and Goal witness.

## Rules

* R1: The preview is pure and token binding includes exact request bytes,
  referenced digest bytes, Goal/workspace, and observed authorization state.
* R2: Authorization accepts only a current matching token and explicit
  approver; no identity authentication is introduced.
* R3: Mapping is complete and one-to-one, never inferred from Story paths or
  `external_ref`; ledger caps and Worker Profile are explicit.
* R4: Storage failure, stale input, invalid limits, or topology mismatch leaves
  no partial mapping, authorization, ledger, or witness.

## Expected Errors

* A stale token, digest/state drift, missing mapping, invalid cap/profile, or
  transaction failure refuses authorization before any durable partial state.

## Dependencies

* FP-52 and ADR-0035; FP-54 consumes this authorization.

## Constraints

* Authorization grants neither Human Decisions/final acceptance nor merge,
  release, or expanded engineering scope; `internal/work` lifecycle remains
  independent.
