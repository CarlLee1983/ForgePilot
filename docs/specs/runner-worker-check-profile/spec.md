# Agent Session Check Profile

## Decision status

這份契約已完成 dogfood evidence inventory、三個獨立 interface 方案比較與 Sol/high architecture
review。Human 已明確接受 [ADR-0028](../../adr/0028-agent-session-checks-are-diagnostic.md)，
implementation 已依本文件以 TDD 完成；canonical／race gates 與獨立 Standards／Spec review 皆通過。

本 slice 只處理 Runner 啟動的 Agent Session 如何分配 focused checks、正式 Verification、
repository-wide／race gates 與 sandbox failure。它不新增 verification command、Evidence type、
state／Run Record schema、repository manifest、CLI flag、永久 cache 或 release/onboarding 工作。

## Evidence

dogfood run `.forgepilot/runs/run-20260916t043145-8b371d/` 有 10 個 Agent Sessions。
WI-003、WI-004、WI-005、WI-006、WI-007 與 WI-008 的 worker 都在 focused checks 之外嘗試
repository-wide gates；多個 session 因 `/bin/ps: operation not permitted`、process-recovery tests
或 sandboxed cache 失敗而無法完成 broad/race commands。外層 Runner 隨後仍為所有 Work Item
取得正式 PASS Evidence。這證明 worker environment failure 與 Candidate engineering verdict 是兩個
不同事實，也證明目前 briefing 雖說 Agent Result 不是 PASS，卻沒有把 check ownership 說清楚。

## Outcome

每份 Runner worker handoff 都帶一個 required **Agent Session Check Profile**：

- Agent Session 擁有與當前 Story、變更與風險直接相符的最低有用 focused checks。
- Agent Session 的 command output 與 Agent Result 都只是診斷材料，不是 PASS 或 Evidence。
- `implementation_finished` 後，Runner 仍只透過 `internal/app.Verify` 在 immutable Candidate 上執行
  canonical `make verify`；只有該 Verification Run 能建立 Verification Evidence。
- repository rules 另列、但不在 `make verify` 內的 repository-wide／race gates，由明確的
  integration/final owner 在 Human final acceptance 前執行，不是每個 Runner worker 的完成儀式。
- sandbox denial 表示該 session 沒有執行能力；它不是 code FAIL、PASS 或要求提高權限的理由。

`Verification` 已是 domain term，因此產品 interface、CONTEXT 與 handoff 一律使用
**Agent Session Check Profile**，不使用「worker verification profile」。

## Interface and seam

profile ownership 放在既有 `internal/agent.Handoff` module。它已擁有 briefing 的 required／optional
ordering、byte budget、prohibitions 與 result contract；Runner 只把 typed action 與 runtime facts
映射成 pure values，不拼接 policy prose。

```go
package agent

type SessionCheckKind string

const (
	SessionCheckImplementation SessionCheckKind = "implementation"
	SessionCheckRepair         SessionCheckKind = "repair"
)

type SessionSandbox string

const (
	SandboxWorkspaceWrite          SessionSandbox = "workspace-write"
	SandboxNotConfiguredByForgePilot SessionSandbox = "not_configured_by_forgepilot"
)

type SessionEnvironment struct {
	Sandbox SessionSandbox
}

type SessionCheckProfile struct {
	Kind        SessionCheckKind
	Environment SessionEnvironment
}

type Handoff struct {
	// existing fields...
	CheckProfile SessionCheckProfile
	// FailureLogPath is required for repair. FailureExcerpt remains optional.
}

type Runtime interface {
	Name() string
	Executable() (string, error)
	Version() (string, error)
	SessionEnvironment() SessionEnvironment
	Plan(Request) (Plan, error)
}
```

`Handoff.Render(limit)` remains the sole rendering entry point. No new verifier, command planner, Story parser,
filesystem port or profile-provider interface is introduced. `SessionEnvironment` extends a real seam:
Codex and fake are existing adapters whose launch capabilities differ.

`Runtime.Name()` remains the single runtime identity. `SessionEnvironment` describes only the sandbox setting
ForgePilot adds, and does not repeat that name. The Codex adapter uses the same `SandboxWorkspaceWrite` typed
constant both in its `Plan` argument and its environment description; an adapter contract test locks the launch
argument and handoff wording together. `SandboxNotConfiguredByForgePilot` means only that ForgePilot adds no
equivalent setting—it does not claim the executable or operating system is unrestricted.

### Invariants and ordering

- zero／unknown check kind or sandbox value is invalid; handoff rendering fails before session launch.
- `RESUME` maps to `SessionCheckImplementation`; `REPAIR` maps to `SessionCheckRepair`.
- repair requires the latest formal FAIL's unambiguous `FailureLogPath`. The path is required context and cannot
  be trimmed; the bounded failure excerpt remains optional.
- implementation profile carrying formal failure context is invalid.
- the profile section is required context, after project rules and before prohibitions／result contract; it is
  never silently truncated. If required context exceeds the byte budget, session creation fails.
- Runtime facts describe only what ForgePilot actually configures. Codex says `workspace-write`; fake says
  `not_configured_by_forgepilot` and makes no broader permission claim.
- Agent Result remains the closed set `implementation_finished`, `needs_human`, `execution_failed`.
  No PASS、VERIFIED、Evidence or check-attestation field is added.
- `implementation_finished` still calls exactly the existing `runner.verify -> internal/app.Verify` path.
- profile is not persisted in state or a new Run Record field; the rendered `handoff.md` is already the
  session-specific execution-history artifact.

### Rendered ownership contract

The required profile states, in substance:

1. Run the smallest stable checks that cover changed behavior and applicable acceptance criteria. Expand only
   when a dependency, regression risk or formal failure requires it, and report exact commands/outcomes.
2. Do not run repository-wide gates merely to declare the Agent Session complete. Runner later owns formal
   canonical `make verify`; only its Candidate Verification can create PASS Evidence.
3. Project rules may name non-canonical broad/race gates. Their integration/final owner must run them before
   Human final acceptance. They are neither waived nor falsely listed as unfinished session implementation.
4. A runtime sandbox may allow workspace writes while denying process inspection, caches, network, credentials
   or system executables. A denied command is “not run”; do not escalate, infer PASS, or infer a code defect.

Repair adds:

1. The latest formal Verification Log is the primary source, not an earlier session summary.
2. Reproduce at the narrowest stable seam, fix the owning cause and run the focused regression.
3. A repository-wide command may be used to isolate the failure, but never as the session's PASS claim.

## Repository expression and enforcement

This P0 deliberately adds no repository manifest.

- Story acceptance criteria and existing project instructions express slice-specific focused checks. When no
  exact command is named, the worker selects the lowest stable test seam and reports what it ran.
- `Makefile` continues to express the one canonical command: `make verify`.
- project instructions express non-canonical repository-wide／race gates and their integration/final owner.
  ForgePilot's root `AGENTS.md` must gain the following scoped semantics at implementation time:
  - only inside an Agent Session launched by `forgepilot run`, the check profile is an explicit exception to the
    general “re-run both gates before work” instruction;
  - the session runs focused Story／changed-slice checks and does not run `make verify` or
    `go test -race -count=1 ./...` merely to satisfy a repository-wide Story AC;
  - those Story AC obligations are **transferred, not waived**: Runner owns canonical `make verify`, and the
    human-directed primary integration/final workflow owns `go test -race -count=1 ./...` after Runner reaches
    `GOAL_COMPLETED` and before Human final acceptance;
  - outside a Runner-launched Agent Session, the existing pre-work and final requirements remain unchanged.
- The primary integration/final workflow must report the exact non-canonical command and exit result in its final
  review handoff. That report is observable acceptance evidence for the repository workflow, not ForgePilot
  Verification Evidence or a new lifecycle precondition.
- If another repository has higher-priority instructions requiring a broad command in every worker, those
  instructions still win. ForgePilot does not secretly override repository authority.

Only the trust boundary is enforced: Agent output cannot create PASS, and formal Verification has one
orchestrator. Focused selection and final-owner compliance are instruction-only. P0 does not parse session logs,
execute a second check pipeline or store final-gate Evidence. Making a non-canonical gate lifecycle-blocking
would require a separate accepted design; it cannot be smuggled into Agent Result or Runner history.

## Failure classification

| Observation | Classification | Runner effect |
|---|---|---|
| focused check exposes a code defect the worker can repair | implementation/repair remains in progress | worker fixes and reruns focused regression |
| focused check is blocked by the declared session environment | diagnostic capability gap | report exact denial; never PASS; `execution_failed` only if work itself cannot proceed |
| intentionally deferred canonical or final gate was not run by worker | ownership handoff, not unfinished code | `implementation_finished` remains valid; later owner must execute its gate |
| Agent Result is malformed or claims an unknown outcome | runtime protocol error | existing retry/stop behavior |
| formal canonical Verification FAILS | engineering FAIL Evidence on anchor | next typed action is repair; new session receives required log path |
| formal check cannot start because of Gate/runtime/tooling/precondition | Verification refusal/operational stop | no manufactured FAIL Evidence |
| final-owner gate later fails | final acceptance blocker outside P0 Evidence | repair via the owning workflow; never reinterpret an earlier PASS |

## Acceptance matrix

| Scenario | Observable result |
|---|---|
| implementation handoff | required profile points to project rules and says focused checks are session-owned, Runner owns formal canonical, and project-named supplemental gates belong to the integration/final owner; product prose does not hard-code repository-specific commands |
| repair handoff after canonical FAIL | profile is repair; full formal log path is present and untruncated; wording starts from that failure and asks for a focused reproducer |
| repair without a unique formal FAIL log | rendering/session preparation fails before runtime launch; no guessed path and no Agent Session |
| implementation profile carries failure context | rendering refuses the inconsistent briefing |
| tight handoff budget | profile and repair log path are retained; optional attempts/excerpt may be marked truncated; impossible required content is refused |
| Codex runtime | handoff says ForgePilot launches `workspace-write`, makes no promise for `/bin/ps`/network/credentials, and Codex Plan still contains the matching flag |
| fake runtime | handoff does not claim Codex sandbox or unrestricted OS access; it says ForgePilot adds no equivalent sandbox |
| sandbox-denial wording | rendered profile instructs that a denied command is “not run”/diagnostic limitation and forbids inferring PASS or code FAIL; model compliance is not claimed as deterministic |
| worker claims focused/broad/race success in summary | claim remains untrusted text; no Evidence is created from it |
| `needs_human` or `execution_failed` | formal Verification does not start, matching existing behavior |
| `implementation_finished` | Runner invokes the existing `internal/app.Verify` path exactly once; no parallel verifier or direct lifecycle write |
| canonical PASS | existing Candidate Verification Evidence is the only PASS; state/schema semantics unchanged |
| canonical FAIL then retry | next typed action produces a fresh repair session, not a resumed conversation, with latest failure context |
| existing run is resumed after upgrade | next handoff derives a fresh profile from current typed action/runtime; old artifacts are not rewritten |
| Run Record/state inspection | no profile field, command attestation or new lifecycle truth appears |
| project instruction and Story audit | root `AGENTS.md` explicitly transfers, without waiving, repository-wide Story AC ownership for Runner sessions; focused Story AC remain session-owned; outside Runner both existing gates remain required |
| terminology/document consistency | `CONTEXT.md` defines Agent Session Check Profile without redefining Verification; architecture records required profile ordering, required repair log path, instruction-only limits and final-owner responsibility |
| repository final acceptance for this slice | primary integration/final workflow reports `make verify` and `go test -race -count=1 ./...` exit 0; the report is repository acceptance, not ForgePilot Evidence or a lifecycle condition |

## Post-implementation dogfood observation

Run one representative real-Codex session and inspect its artifacts to learn whether the worker actually favors
focused checks and classifies sandbox denial as instructed. This is a cost/behavior observation, not deterministic
acceptance: formal safety depends on Runner-owned Candidate Verification even when the model ignores the profile.

## Compatibility and rollback

- No CLI syntax, state schema, Run Record schema, Agent Result JSON, Candidate, Evidence or Verification Run
  semantics change.
- `internal/app.Verify`, canonical `make verify` and Candidate fan-out remain unchanged.
- Runtime interface gains one pure environment-description method; every current adapter and test runtime must
  implement it. No dependency is added; standard library only remains true.
- Existing runs resume without migration. Historical handoff artifacts remain historical.
- Rollback removes the typed profile, runtime environment method, required rendering section, scoped project-rule
  text and tests. No state or Evidence rollback is needed because formal safety never depends on the instruction.

## Rejected shapes

- **Versioned repository command manifest:** flexible, but adds schema/version/selector/path/argv validation for
  instruction-only commands before a second plan source exists. Reconsider only with multiple repositories whose
  focused checks cannot be expressed in Story/project contracts.
- **Runner executes profile commands:** duplicates process ownership, timeout, logging and verdict orchestration,
  and risks creating a second Verification path.
- **Raw Markdown assembled in Runner:** leaks ownership wording, sandbox facts and byte-priority rules across the
  handoff seam, making the module shallow.
- **Runtime adapter appends its own profile prose:** engineering ownership does not vary by coding CLI and the
  handoff artifact could diverge from stdin/fake behavior.
- **Parse worker logs to forbid broad commands:** brittle enforcement of untrusted prose/process output; it does
  not prove the command did or did not run and provides no engineering verdict.
