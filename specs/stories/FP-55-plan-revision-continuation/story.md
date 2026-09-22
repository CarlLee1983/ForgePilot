# Story: FP-55 revise authorized plans and continue within bounds

## Goal

An operator can explicitly authorize an additive reviewed-plan revision and
continue only through ADR-0035's exact rollover and verification-boundary rules,
without rewriting historical contracts or resetting consumption.

## Context

ADR-0035 permits same-Goal additions and re-reviewed contract updates while
preserving nodes, Story references, edges, Evidence, and cumulative accounting.
Deletion, replacement, or dependency rewrites require a new Goal.

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
* Boundary: `AuthorizationRevision`
* Boundary: `VerificationBoundary`
* Contract: `revision preserves history and cumulative charges while new actions recheck current bindings`
* Owner: `AuthorizationRevision = ForgePilot execution-governance`
* Owner: `VerificationBoundary = internal/app`

## Risk

* Level: high
* Reason: `concurrency`

## Concurrency

* Contended resource: `Goal revision authorization and action admission`
* Linearization point: `atomic revision publication followed by per-action recheck`
* Conflict outcome: `drift stops new action while lawfully started verification retains evidence`
* Evidence AC: `AC-004`

## Scope

### In Scope

* `execution revise` preview/diff and explicit authorization of additive reviewed
  changes, preserving historical plan/run/Evidence/accounting facts.
* Exact automatic rollover eligibility; authorization-level explicit continuation;
  plan/authorization rechecks after session completion and at verification.

### Out of Scope

* Topology replacement inside a Goal, Evidence inheritance into a new Goal, or
  a general reopen mechanism.
* Stop intent and external/human wait declarations (FP-56).

## Inputs

* Current authorization/ledger, prior manifest, proposed reviewed revision, run
  stop reason, and current plan/profile bindings.

## Outputs

* An explicit revision or continuation decision with immutable historical
  contracts and honest verification Evidence retained.

## Rules

* R1: Revision preserves every old node, Story reference, edge, Evidence, and
  consumed accounting; it may add nodes or update re-reviewed contract content.
* R2: Delete/replace node or alter dependency topology requires a new Goal and
  does not reopen DONE or inherit old Evidence.
* R3: Automatic rollover is limited to confirmed cleanup after `MAX_STEPS` or
  `MAX_DURATION`, with current valid binding and sufficient total caps; timeout,
  attempts/no-progress/capacity/wait/drift/expiry/recovery-blocked never roll.
* R4: Exact-run resume retains its run contract. Explicit authorization resume
  may create a new run only after required rechecks, never changing the old run.
* R5: Before every action, including session-to-verification, current bindings
  are rechecked. Lawfully started verification retains its immutable honest
  Evidence; later drift stops continuation/rollover.

## Expected Errors

* Stale revision, prohibited topology change, drift, or ineligible rollover
  refuses continuation without altering historical contracts or consumption.

## Dependencies

* FP-54 and ADR-0035; FP-56 supplies durable pause/resume control.

## Constraints

* VERIFIED/machine PASS remains distinct from DONE and Human final acceptance;
  no second Work Item lifecycle is introduced.
