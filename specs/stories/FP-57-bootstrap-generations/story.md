# Story: FP-57 immutable managed engine generations

## Goal

The source-built Bootstrap installs a complete ForgePilot generation transactionally, switches only
after verification, and retains opaque references without learning anything about target repositories.

## Context

ADR-0035 requires an immutable, version-pinned ForgePilot engine for every authorized background
job. Bootstrap owns user-home generation installation and retention; it must neither create a target
repository registry nor inspect a target repository to decide what to retain.

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
* Reason: `versioned-surface`
* Reason: `retention-overflow`

## Scope

### In Scope

* Source-built Bootstrap staging, integrity verification, atomic current-pointer switch, and recovery
  for managed CLI, helper, and skill generations in the user-owned install root.
* The versioned `retention-v1` acquire/release protocol and safe prune/uninstall decisions.
* Tests proving failures retain the preceding working generation and proving retention never requires a
  target-repository registry or scan.

### Out of Scope

* Execution Authorization, Worker Profile selection, Runner launch, supervision, and UI projection.
* Network download, credential handling, release publication, or modification of target repositories.

## Inputs

* A locally built ForgePilot CLI, matching Bootstrap helper, Codex skill/procedure payload, and their
  canonical payload digest.
* User-home managed install root, full source commit, exact generation tuple, and 256-bit opaque retention reference.

## Outputs

* Verified immutable generation directory, atomically selected current pointer, recoverable transaction
  record, and opaque retention markers.

## Rules

* R1: A generation is identified by its full lowercase source commit plus canonical payload digest;
  verify the complete staged CLI/helper/skill payload before changing `current`.
* R2: `retention-v1` acquires and releases exact generation tuples under the shared Bootstrap lock;
  references are opaque and never identify a target repository or holder.
* R3: Prune/uninstall keeps current, previous, actively retained, or uncertain generations; each
  removal still requires fresh Bootstrap Approval.
* R4: Any failure before pointer commit leaves the prior current selected. A post-commit failure leaves
  an authoritative non-idle transaction and both generations available for approved recovery.

## Expected Errors

* Invalid, incomplete, or mismatched staged content rejects before pointer switch.
* Missing, malformed, or uncertain retention state blocks destructive removal.

## Dependencies

* GitHub issue #50; ADR-0033 and ADR-0035.

## Constraints

* Keep Bootstrap source-built and local-only; do not add a repository registry, target-repository scan,
  credentials, or network client.
* Do not delete a generation until retention state is conclusively releasable.

## Guidance

Relevant:

* decision: `ADR-0033` — Bootstrap separates distribution from repository onboarding
* decision: `ADR-0035` — referenced engine generations are retained by user-home opaque markers

Not applicable:

* Work Item lifecycle mutation

## Trust Boundary Fields

* `bootstrap.generation_id` — full lowercase 40-character source commit used for the managed version path.
* `bootstrap.payload_digest` — canonical SHA-256 over the staged CLI, helper, skill, and procedure before activation.
* `retention.reference_id` — 256-bit opaque bearer value hashed before Bootstrap persistence; never a repository or holder identity.
