# ForgePilot

**English** | [繁體中文](README.zh-TW.md) | [日本語](README.ja.md)

Website: [https://carllee1983.github.io/ForgePilot/en/](https://carllee1983.github.io/ForgePilot/en/)

ForgePilot tells your engineering agents what work is actionable next.

Warrant bounds that work to human-approved Stories and proves completion with your repository's own verification.

ForgePilot does not replace Warrant or your coding agent.

```text
Human
 ↓
ForgePilot
 ↓
Warrant Story
 ↓
Agent
 ↓
make verify
 ↓
Evidence
 ↓
ForgePilot
```

ForgePilot is a passive DAG ledger: once the work has been broken down, an external coding Agent follows `forgepilot next` and works through every node of a DAG in topological order. ForgePilot never launches an Agent. It decides the next legal action, stores Verification Evidence bound to an exact Candidate, and unlocks downstream work in the same transaction that completes a node, so engineering work can carry over across Agents and sessions.

## Current status

The convergence described in [ADR-0040](docs/adr/0040-forgepilot-is-a-passive-dag-ledger.md) is fully implemented. The product looks like this:

- A **Goal Plan** (JSON) creates a Goal and its whole DAG in one `forgepilot goal import`; node IDs are Work Item IDs. To add work, edit the plan file and import it again — only new nodes are accepted.
- The Agent runs a loop: `next` returns the single legal next step, and the Agent runs `start`, implements, then `verify`. PENDING and READY are computed from dependencies at read time and never stored; a workspace holds at most one RUNNING or VERIFYING item at a time.
- `verify` runs the managed project's own `make verify` in an isolated detached worktree against an exact Candidate (the committed HEAD, or an uncommitted working tree pinned with `--snapshot`), and stores PASS, FAIL or INTERRUPTED as Evidence bound to that Candidate. **PASS means DONE**: downstream work unlocks in the same transaction, and the Goal completes automatically when its last item becomes DONE.
- A Goal can require **Approval** at import time (`require_approval`): PASS moves the item to REVIEW first, a human's `review approve` makes it DONE, and `review reject` sends it back to RUNNING.
- When a human needs to decide something, the Agent opens a **Gate** that blocks the item; an Agent in a later session learns from `next` or `status` that it is waiting on a human. `status` explains, for every unfinished item, why it cannot move forward.
- Fully offline: no network requests, and no processes other than Git and `make verify`.

There is no Runner, supervised execution, Bootstrap or Story readiness review, and no `work add`, `reconcile`, `migrate`, `--pr` or Review Policy — ADR-0040 removed them, and their historical specs remain under `docs/specs/`.

The initially supported platform is the local file system on macOS, with Go 1.25.5. State is protected by a process lock and atomic replacement; no other platform is claimed yet.

## Documentation

The project documents below are written in Traditional Chinese.

| Document | Purpose |
|---|---|
| [Domain vocabulary](CONTEXT.md) | Shared core terms, so Story and Work Item are never confused |
| [Architecture](docs/architecture.md) | Responsibility boundaries, data model, lifecycle, `next` rules and persistence |
| [Development plan](docs/development-plan.md) | CLI contract, Goal Plan format and the change-surface verification matrix (followed by historical milestone records) |
| [Decision records](docs/adr/README.md) | Hard-to-reverse decisions and the conditions that would falsify them |
| [Diagrams](docs/diagrams/README.md) | The state machine, layers, transaction boundaries and the `verify` / `review approve` sequence |
| [Project tour](docs/show-me-forgepilot.html) | One page covering the problem, core concepts and key decisions |
| [AGENTS.md](AGENTS.md) | What an Agent taking over this repo needs to know first: boundaries, pitfalls and working method |

## Installation

Requires Go 1.25.5 or later. ForgePilot uses only the standard library and has no external dependencies.

```bash
go install github.com/CarlLee1983/ForgePilot/cmd/forgepilot@<tag>
```

Replace `<tag>` with the release tag to install, for example `v0.4.0` — the first post-convergence release and the product this document describes. Tags up to and including `v0.3.1` are the pre-convergence product, which still contains the Runner and the old schema.

Skills are installed by copying: copy the repository's `skills/<agent>/forgepilot/` directory into the Agent's skill directory — `~/.claude/skills/` for Claude Code (`skills/claude-code/`), `~/.agents/skills/` for Codex (`skills/codex/`). The skill teaches the Agent to advance the whole DAG with `next → start → implement → verify`, and to open a Gate and stop when something needs a human decision.

## Usage

From the root of a repository that already has human-approved [Warrant](https://github.com/CarlLee1983/Warrant) Stories (`specs/stories/<slug>.md`):

```bash
forgepilot init
git add .gitignore && git commit -m "chore: ignore ForgePilot state"   # init adds the .forgepilot/ ignore entry
forgepilot goal import plans/dbcli-dba.json
forgepilot next
forgepilot start DBCLI-001
forgepilot status
```

A Goal and its whole dependency DAG are created from one Goal Plan (JSON). Node IDs are Work Item IDs, and commands such as `start` and `verify` take them:

```json
{
  "goal": { "id": "dbcli-dba", "title": "DBA Workflow Support", "require_approval": false },
  "nodes": [
    { "id": "DBCLI-001", "story": "specs/stories/DBCLI-001.md", "depends_on": [] },
    { "id": "DBCLI-002", "story": "specs/stories/DBCLI-002.md", "depends_on": ["DBCLI-001"] }
  ]
}
```

An ID starts with an alphanumeric character, may continue with alphanumerics, `.`, `_` and `-`, is at most 64 characters, contains no `..`, and does not end in `.` or `.lock`; node IDs are unique across the whole state. `story` must exist and live under the repository's `specs/stories/`. If any check on the plan fails (a cycle, an unknown, self or duplicate dependency, a duplicate node, a Story path that is missing or outside `specs/stories/`, an unknown JSON field), nothing is written, and the error names the node and field. Node order is the order in which `next` recommends items when several are READY at once. For the full command and plan format contract, see [the CLI contract in docs/development-plan.md](docs/development-plan.md#cli-契約).

To add work, edit the plan file and import it into the same Goal again. Only new nodes are accepted (they may depend on existing ones); the plan must list every existing node, and their `story`, `depends_on` and the Goal's attributes must match the originals. An identical plan is a successful no-op, and a COMPLETED or CANCELLED Goal is always rejected. If a newly added node's Story is not yet committed, `goal import` prints a hint after succeeding: verify the working tree with `forgepilot verify <work-id> --snapshot`, or commit first and use commit-mode verification.

At this point the stored state is `DBCLI-001 = RUNNING` and `DBCLI-002 = NOT_STARTED` (shown as PENDING by `status`). After the CLI restarts, `next` recommends `resume implementation` for `DBCLI-001` rather than starting another READY item. When an OPEN Gate or a pending Human Review leaves nothing else to do, `next` states explicitly what it is waiting for; it never runs a recommendation on the Agent's behalf. Agent loops use `forgepilot next --json`, where every field is always present (an absent value is an empty string, and `waiting` is an empty array).

### Verification and completion

Once the Agent has finished implementing, verify the committed revision:

```bash
forgepilot verify DBCLI-001
```

ForgePilot checks that the working tree is clean, resolves the current HEAD, creates a detached worktree for that commit under `.forgepilot/worktrees/`, and runs the `make verify` your project defines with the caller's environment — that check pins its own toolchain; ForgePilot does not resolve runtime declarations. It then stores the Evidence and prints the log path (`.forgepilot/logs/`). On PASS, in a Goal that does not require Approval, `DBCLI-001` becomes DONE directly, the same transaction turns the items depending on it READY, and the Goal becomes COMPLETED when its last item is DONE; in a Goal that requires Approval, the item moves to REVIEW instead. FAIL or INTERRUPTED always returns the item to RUNNING so the Agent can read the log and fix it. One verify leaves Evidence only for the item that triggered it, and DONE is terminal — later commits do not reopen it.

Because commit-mode verification runs in an isolated checkout, **your `make verify` must work on a fresh checkout** — projects that need a `.env`, locally installed dependencies or an existing build cache will fail. This is the same requirement CI imposes. `verify` without flags refuses to run when the working tree is dirty (including untracked files), because a commit cannot describe uncommitted content. To verify that content, use snapshot mode:

```bash
forgepilot verify DBCLI-001 --snapshot
```

ForgePilot builds a local immutable snapshot commit through a private Git index, capturing tracked staged and unstaged changes, tracked deletions and non-ignored untracked files; ignored runtime artifacts are left out of the snapshot (unless already tracked). The current branch, HEAD, real index, staging state and working files are identical before and after capture. The snapshot is kept under `refs/forgepilot/snapshots/`; no branch or tag is created, and no network request is made.

An interrupted verification (Ctrl-C, closing the terminal, a reboot) never leaves a fake result: the next `verify` records that run as INTERRUPTED and returns the item to RUNNING, and until then `next` reports `RECOVER`.

### When a human has to decide

When engineering raises a question that neither ForgePilot nor the Agent has the authority to settle — an architectural trade-off, a scope change, a choice with security impact, an ambiguous spec — record it as a Gate instead of letting the Agent pick an option and keep writing:

```bash
forgepilot gate open --work DBCLI-001 \
  --question "Backfill existing data?" --option "Backfill" --option "Don't backfill" \
  --reason "The spec doesn't say how existing data is handled"
```

List at least two options — a single option is not a question. While the Gate is open, `DBCLI-001` can be neither started nor verified and `next` will not recommend it, but its state does not change: a RUNNING item stays RUNNING, because blocking is a separate dimension.

```bash
forgepilot gate resolve GATE-001 --option "Don't backfill" --note "There is no existing data yet"
forgepilot gate cancel GATE-002 --reason "This was the wrong question"
```

`resolve` accepts only one of the listed options. If none of them is right, `cancel` with a reason and open a Gate that asks the right question; `cancel` also lifts the block, but it leaves an immutable record shown in `status`, so a withdrawal cannot happen silently. The decider's identity defaults to Git's `user.email` and can be overridden with `--by` — it is a **self-declared** identity that ForgePilot does not authenticate.

### Human review

When a Goal declares `"require_approval": true` in its Goal Plan, each item enters REVIEW after PASS:

```bash
forgepilot review approve DBCLI-001 --note "Solves the right problem"
forgepilot review reject DBCLI-001 --reason "Error paths are not handled"
```

A rejection sends the item back to RUNNING for the Agent to keep fixing. ForgePilot **has no completion command**: `review approve` checks the completion conditions in the same transaction that records the review — the latest Verification is PASS and still matches the current Candidate, the item has no unresolved Gate, and its Goal is ACTIVE — and only when all hold does the item become DONE and unlock downstream work. If HEAD (or the snapshot's working tree) changes after PASS, approve is rejected and asks for a new `verify`; when a REVIEW item is stale, `next` also recommends re-verifying. For a Goal that does not require Approval, `review` is always rejected.

DONE is terminal, with no reopen; to redo work, add a new node to the plan file and import it, so the reason for the redo has a place to be recorded. To abandon a whole Goal, use `forgepilot goal cancel <goal-id> --reason <text>`.

### Older state

State is read only at schema 19, and there is no upgrade command. State written by older versions is refused with a message explaining that the schema is a clean break and this version does not read old state. To carry unfinished work forward, write a Goal Plan for each Goal, move the old `.forgepilot/` to an archive location, run `forgepilot init`, then run `forgepilot goal import` for each plan.

## Scope

A local CLI, Goal Plan import, the DAG and readiness computed at read time, Gates, Evidence, rule-bound state transitions, a deterministic `next`, Story references and `make verify` integration.

Out of scope: a web UI, cloud services, database services, daemons, schedulers, a Runner that launches coding agents, parallel multi-agent execution, token quotas, a generic workflow DSL, a plugin framework, a network API, remote execution, messaging-platform integrations, and research or ML workflows.

ForgePilot does not generate Stories, break down work, judge PASS with an LLM, decide architecture, or automatically merge, release or perform production writes.

## Development verification

`make verify` at the repository root is ForgePilot's own canonical verification command: it checks formatting and runs `go vet`, the tests and the CLI build. Integration and final acceptance additionally run `go test -race -count=1 ./...`.
