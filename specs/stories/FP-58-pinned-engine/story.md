# Story: FP-58 pin Worker Profiles to retained engine generations

## Goal

Every implementation and repair launch uses exactly the authorized Worker Profile and immutable
managed ForgePilot generation tuple (full source commit plus canonical payload digest), with retention
acquired before the generation is referenced.

## Context

ADR-0035 separates the Worker Profile from the ForgePilot engine binding. Neither may drift on resume
or a new run: provider, executable, model, effort, permissions, resolved executable version, and
engine generation are explicit authorization facts. Engine changes require a paused, cleaned,
compatible, explicitly revised authorization; historical runs remain bound to their old generation.

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
* Reason: `authorization-drift`
* Reason: `retention-overflow`

## Scope

### In Scope

* Typed Worker Profile and immutable engine-generation binding in Execution Authorization and launch
  validation for implementation and repair.
* Acquire-before-reference and release lifecycle for opaque engine retention markers.
* Explicit engine-revision preconditions and preservation of prior run bindings.

### Out of Scope

* New worker providers, ambient chat-model inheritance, credentials, bootstrap installation internals,
  process supervision, and repository lifecycle changes.

## Inputs

* Approved authorization version, explicit Worker Profile, resolved worker executable identity, and
  immutable managed generation tuple: full source commit plus canonical payload digest.

## Outputs

* Launchable pinned binding or fail-closed drift result; retention marker associated with the active
  authorization or supervised job.

## Rules

* R1: Launch never substitutes a runtime default, another provider, or the calling chat model.
* R2: Acquire the exact generation tuple through `retention-v1` before any generation reference;
  release only after no authorization or supervised-job owner remains; prune/uninstall honors active
  and uncertain markers.
* R3: Engine revision requires pause, confirmed cleanup, compatibility check, and authorization revision.

## Expected Errors

* Profile, executable, model, effort, permission, or engine mismatch stops before worker launch.
* Missing or uncertain generation retention stops launch and destructive generation operations.

## Dependencies

* GitHub issues #53, #54, and #57; ADR-0035.

## Constraints

* Credentials remain runtime-managed and are never copied into authorization, run records, logs, or
  retention markers.
* Existing runs retain their original binding after an engine revision.

## Guidance

Relevant:

* decision: `ADR-0035` — explicit profiles, fixed engine, revision, and retention boundaries

Not applicable:

* provider expansion beyond the existing Codex worker

## Trust Boundary Fields

* `authorization.worker_profile` — approved provider, executable, model, effort, and permissions.
* `authorization.engine_generation` — immutable managed ForgePilot generation tuple (source commit and payload digest).
* `runtime.resolved_worker_identity` — executable path and version observed at launch validation.
* `retention.reference_id` — opaque reference acquired before engine use.
