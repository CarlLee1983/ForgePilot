# ForgePilot source-built onboarding procedure

> **Current procedure:** this document describes the current coupled
> source-built walkthrough. ADR-0033 defines a Bootstrap that will install a
> same-version CLI and Codex skill before Repository Onboarding. Its repository
> script currently contains only incomplete `status` and `retention-v1`
> development paths; it is not a supported installer. See the
> [implementation status](bootstrap.md) and do not use it to install ForgePilot.

Formal onboarding builds ForgePilot from a developer-supplied, full 40-character
commit SHA. It is not a prebuilt-binary, signing, or platform-trust promise.
The procedure is outside ForgePilot's offline governance CLI.

The supported host is Apple Silicon macOS (`Darwin arm64`) only. The planner
checks that boundary during inspection and refuses Intel Macs or other hosts
before it prints an executable onboarding plan. Cross-compilation and unsigned
`amd64` maintainer trial assets are not support evidence.

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
be used. A local source repository must be an absolute path; URL and scp-style
repository references are also accepted. Printing this plan is inspection-only;
it must not perform an action.

## FIRST EXPLICIT APPROVAL: obtain and verify ForgePilot

Show the plan, including source fetch, detached checkout, local build, `make
verify`, and the atomic entrypoint switch. Wait for the developer's FIRST
EXPLICIT APPROVAL before any one of those actions.

Use only an already-installed compatible Go toolchain. If Go is missing or does
not satisfy the project, stop and explain the prerequisite or installation
options; do not install Go, a package manager, or alter a shell profile. Compare
the reported toolchain with the `go` directive at the exact reviewed ForgePilot
`source_commit`; do not read it from ambient worktree content.
Disable Git lazy fetch and replacement objects for that exact-commit read.
An incompatible version stops at `go-prerequisite`, before staging.

Build the exact source commit with `GOBIN` set to a user-owned versioned staging
directory and `go install ./cmd/forgepilot`, run the disclosed `make verify`
there, and confirm the staged CLI starts. Only then
perform the displayed atomic entrypoint switch. A failure leaves the existing
entrypoint unchanged.

On any source-action failure, stop before later actions. A durable or shared
diagnostic uses only the safe fields `source_commit`, `failed_action`, `cause`,
`exit_status`, `entrypoint_preserved`, `target_state_preserved`, and
`temporary_entrypoint_present`, plus the fixed `raw_command_output=omitted`
marker. Omit raw stdout/stderr, environment values, credential-bearing source
URLs, absolute local paths, and shell state. Local command output may be used
transiently for diagnosis, but redact it before retaining or sharing the
result. If `entrypoint-link` succeeded and `entrypoint-switch` failed, the old entrypoint
remains authoritative; report the leftover `.new` path only through
`temporary_entrypoint_present` and require explicitly reviewed cleanup before
replanning.

## Reuse state, review Story, then ask again

After the target preflight, read and reuse that ForgePilot state if
`.forgepilot/` already exists. Do not rerun `init` or overwrite existing Goals
or Work Items.

When a valid PraxisBound Story already exists, use its path. Otherwise draft
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

## Action-plan format and offline acceptance

The same plan can be consumed without evaluating shell text:
append `--format actions` to the documented planner arguments. The stream starts
with `forgepilot-onboarding-plan-v1` followed by NUL. Every record then contains
NUL-terminated approval phase, action ID, working directory, effect, decimal
argument count, and that many argv elements. Environment overrides are explicit
`env` argv elements. Consumers must reject unknown versions, truncated records,
unexpected commands or paths, and duplicate IDs before executing anything.
The default human-readable plan and this stream share one action list. Neither
format executes the printed actions or grants approval.

The phases are `source`, `story`, and `repository`. The `review-story` record is
a human checkpoint: its sole argument is the Story path, not an executable.
Missing Story directories produce this checkpoint and no repository commands.
Authorize draft writes separately, review the draft, then re-run the plan.
Existing state follows the same Story check before the read-only `status` plan.
Story directories and the two leaf files must not be symlinks; leaves must be
regular files. Source staging and the entrypoint parent must be outside the
target repository so the first approval cannot perform second-phase writes.
Derived staging directories and the staged executable cannot be symlinks, the
temporary entrypoint must be absent, and the entrypoint cannot be a directory.
Target commands use the approved entrypoint's absolute path.

After source installation has already succeeded in the current walkthrough,
replanning for Story review resumes only the `story` and `repository` phases.
Retain the successful source actions and their exact source/commit/staging/
entrypoint identity; do not repeat them or infer success from a directory merely
existing. If that identity or the installed entrypoint changes, stop and obtain
a fresh source plan and approval. Existing staging directories are readable by
the planner so this resume is possible; repeating a full source install into an
occupied checkout stops at clone without replacing the current entrypoint.

Candidate inspection is conservative and static. COMMIT accepts only a literal
full SHA matching HEAD. SNAPSHOT checks HEAD tracking and current files without
using the real index. An uncertain transformation (attributes, filters, line
normalization, sparse checkout) requires human Candidate inspection; the planner
never runs a clean filter or Makefile to guess its result. It does not fetch
missing objects. This advisory inspection does not replace ForgePilot's canonical
check in the eventual detached Candidate checkout. Re-plan if inspected files,
HEAD, paths, or Story change before execution.

`onboarding_acceptance_test.go` installs both actual adapters under temporary
homes and consumes these actions with fake source tools and a built ForgePilot
CLI. It covers both approvals, failure stops, Story review, existing state, and
final status. The fake agent is deterministic; it does not establish that a
real model follows the prose correctly.

`TestOnboardingCodexAcceptance` and `TestOnboardingClaudeAcceptance` are guarded
by `FORGEPILOT_ONBOARDING_CODEX_ACCEPTANCE=1` and
`FORGEPILOT_ONBOARDING_CLAUDE_ACCEPTANCE=1`, respectively. Even when enabled,
FP-35 stops at a generated executable spy. It never launches a real model or
imports login credentials; actual first-use model acceptance belongs to #39.
Only after exact opt-in may `FORGEPILOT_SMOKE_ARTIFACT_DIR` select an existing,
absolute directory outside the temporary fixture. Reports omit raw agent output
and all environment values, refuse to overwrite existing files, and explicitly
record that no ForgePilot verification ran. Export configuration alone enables
nothing. The existing Runner `FORGEPILOT_CODEX_SMOKE` contract is unchanged.
