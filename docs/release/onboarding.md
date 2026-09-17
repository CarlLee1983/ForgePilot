# ForgePilot source-built onboarding procedure

Formal onboarding builds ForgePilot from a developer-supplied, full 40-character
commit SHA. It is not a prebuilt-binary, signing, or platform-trust promise.
The procedure is outside ForgePilot's offline governance CLI.

## Inspection-only phase

Before a source fetch, build, `make verify`, entrypoint change, or repository
write, inspect only. Confirm the target directory is a Git repository and ask
whether later verification uses a COMMIT Candidate or a SNAPSHOT Candidate.

For a COMMIT Candidate, resolve and display its full SHA, require it to match
the target's current `HEAD`, and inspect that commit's `Makefile`; a Makefile
visible only in the current working tree does not pass.
Re-run the plan if HEAD changes before normal verification.
For a SNAPSHOT Candidate, an
ignored, untracked Makefile is not included, so stop rather than assume it has
`make verify`. If Candidate content cannot be determined, stop and say why.

Use `scripts/onboarding/source-built-plan.sh` to print the exact commands,
absolute paths, source repository, and full 40-character commit SHA that will
be used. Printing this plan is inspection-only; it must not perform an action.

## FIRST EXPLICIT APPROVAL: obtain and verify ForgePilot

Show the plan, including source fetch, detached checkout, local build, `make
verify`, and the atomic entrypoint switch. Wait for the developer's FIRST
EXPLICIT APPROVAL before any one of those actions.

Use only an already-installed compatible Go toolchain. If Go is missing or does
not satisfy the project, stop and explain the prerequisite or installation
options; do not install Go, a package manager, or alter a shell profile.

Build the exact source commit with `GOBIN` set to a user-owned versioned staging
directory and `go install ./cmd/forgepilot`, run the disclosed `make verify`
there, and confirm the staged CLI starts. Only then
perform the displayed atomic entrypoint switch. A failure leaves the existing
entrypoint unchanged.

## Reuse state, review Story, then ask again

After the target preflight, read and reuse that ForgePilot state if
`.forgepilot/` already exists. Do not rerun `init` or overwrite existing Goals
or Work Items.

When a valid ForgeFlow Story already exists, use its path. Otherwise draft
`specs/stories/<story-id>/story.md` and `acceptance.md` with intent, scope,
observable acceptance criteria, `make verify`, assumptions, and relevant
guidance. Present it for human review. Do not run `forgepilot work add` until
the developer has approved the Story.

## SECOND EXPLICIT APPROVAL: target-repository writes

Then show `forgepilot init`, `forgepilot goal create`, `forgepilot work add`,
and `forgepilot status`, with their target-repository paths and effects. Wait
for the SECOND EXPLICIT APPROVAL before these repository writes. Omit
`--review-policy` to retain the default `WORK_ITEM` policy. If the Story or
implementation remains uncommitted, later verification is `forgepilot verify <work-id> --snapshot`; never create a WIP commit only to verify.
For a COMMIT Candidate, the developer must commit the intended change and keep the worktree clean before normal verification; this procedure does not commit it.

This procedure never commits on the developer’s behalf, migrates state,
approves a review, resolves a Gate, or publishes. Before the relevant explicit
approval, it does not fetch source, invoke a repository-defined target, or
write to the target repository; the inspection-only Git queries above are the
sole exception.
