# Story: FP-33 source-built onboarding procedure and short prompt

## Goal

A developer can introduce ForgePilot from one reviewed, full immutable source
commit SHA without treating an unsigned prebuilt binary as formal onboarding.
The procedure exposes the two human approval boundaries before it changes a
machine or target repository.

## Context

[ADR-0030](../../../docs/adr/0030-source-built-onboarding-without-apple-developer.md)
is the governing decision: the maintainer will not join the Apple Developer
Program. Formal onboarding therefore uses installed Go to build a fixed source
commit. This Story owns the common procedure, short prompt, and a no-side-effect
action-plan renderer. It does not implement adapters, real-agent acceptance, or
native dual-architecture support evidence.

## Scope

### In scope

* Inspection-only target-repository and Candidate checks before any write.
* A full 40-character SHA action plan that literally discloses source fetch,
  build, verification, staging paths, atomic entrypoint switch, and later
  repository commands.
* The first approval before source fetch/build/verify/entrypoint change and a
  second approval after Story review before `init`, `goal create`, `work add`,
  and `status`.
* Existing state reuse, valid Story reuse or draft-and-review guidance, and
  `verify --snapshot` guidance for uncommitted work.

### Out of scope

* Installing Go, package managers, shell-profile changes, prebuilt downloads,
  signing, notarization, Gatekeeper bypasses, Release publication, core CLI
  networking, adapter installation (#34), disposable-repository execution
  acceptance (#35), real-agent acceptance (#39), and native support evidence
  (#38).

## Rules

* The plan accepts only a lowercase full 40-hex source commit SHA and prints no
  command with side effects as part of its own execution.
* An absent or unsuitable Go toolchain stops the procedure; it is never
  installed by the agent.
* The target Candidate precheck examines the actual COMMIT or SNAPSHOT, not the
  main worktree. An ignored untracked Makefile is absent from a SNAPSHOT.
* The source build uses `go install` with `GOBIN` in its versioned staging
  directory and runs `make verify` in its checked-out exact source revision
  and changes the entrypoint only atomically after success.
* No lifecycle decision, commit, migration, review approval, Gate resolution,
  or publication is performed for the developer.

## Verification

`scripts/onboarding/onboarding_test.sh` is the public document/action-plan
seam. It uses a disposable local Git source commit, rejects invalid identities,
asserts both approval boundaries and literal commands, and proves plan rendering
does not mutate source. Run `make verify` and `go test -race -count=1 ./...`
for integration gates.
