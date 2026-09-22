# ADR-0036：以明確 policy 讓 Goal 可在驗證後自動完成

## Status

accepted; the explicit HUMAN Goal-final-review option is superseded by ADR-0037

## Context

`review_policy=GOAL` 原本只把例行 Human Review 移到 Goal 邊界；所有 Work Item
完成 current Candidate 驗證後，Runner 仍停在 `AWAITING_GOAL_REVIEW`。這會讓以
`forgepilot goal` 建立的長任務在所有工程條件都已通過後仍無法連續執行。產品現在把
「current Candidate 的 PASS 即完成」作為 GOAL policy 的預設；需要人工終審的例外仍
可明確選擇 `HUMAN`。完成不能只依賴上一輪 projection，否則會留下 Candidate／Gate
競態。

## Decision

新增持久化 `completion_policy`：

- `VERIFIED` 是 `review_policy=GOAL` 的 CLI 預設：完成 current Candidate 驗證後，
  Runner 可以無人值守地跨過 Goal 邊界。需要人工終審的產品明確傳入
  `--completion-policy human`，保留 `AWAITING_GOAL_REVIEW`。
- `VERIFIED` 只能和 `review_policy=GOAL` 一起建立；`goal create --review-policy goal`
  與 `--completion-policy verified` 都選用它。
- `HUMAN` 是明確的人工終審例外：使用
  `goal create --review-policy goal --completion-policy human`。WORK_ITEM policy
  一律維持既有 Human Review。

`VERIFIED` Goal 的 `next` projection 產生型別化 `COMPLETE_GOAL` action。Runner 不
解析文字，也不把 `WAIT_GOAL_REVIEW` 重新解釋成完成；它呼叫 application service，
由單一 `storage.Update` callback 重新讀取 current Candidate facts，要求 Goal
ACTIVE、Work Item 非空且全部為 `VERIFIED`、每件最新 Verification 是 current
Candidate 的 PASS、沒有 OPEN Gate，而且 Verification Evidence ID 集合與 action
投影完全相同。任何漂移都 fail closed。

成功交易會追加不可變的 `GoalCompletionEvidence`（`GC-*`），保存 Goal、repository、
觀察到的 revision／snapshot digest、completion policy、basis 與精確的 Verification
Evidence IDs，然後把 Goal 設為 `COMPLETED`。Work Item 保持 `VERIFIED`，不把它們
重寫成 `DONE`；舊的 `goal complete` 仍只允許 WORK_ITEM Goal 的人工路徑。

## Consequences

- Existing schema-v10 GOAL records gain the new default during explicit migration;
  already-v11 `HUMAN` records remain an explicit human boundary. Changing policy is
  durable and included in Runner's execution scope.
- A committed completion is terminal. New work or a new Gate cannot be attached to a
  completed Goal; later repository changes do not reopen it.
- Run records stop with `GOAL_COMPLETED` and carry the aggregate completion ID followed
  by its exact Verification Evidence IDs. A save/restart race is idempotent because a
  retry returns the existing aggregate record instead of appending another one.
- A pre-v11 run record has no completion-policy field; after state migration its first
  resume adopts and persists the Goal's migrated policy. If completion committed before
  the prior run could save its stop record, resume clears the purpose-tagged, no-process
  pending marker for the final facts read only when the committed Goal and aggregate
  evidence prove completion, then repairs that record before readiness or runtime
  preflight. Other pending executions retain the existing fail-closed recovery rules.
- Schema v11 adds the completion policy, the aggregate evidence collection and its ID
  counter. Migration is one-way with the existing `state.json.v<n>.bak` backup and
  manual rollback by restoring that backup.

## Alternatives rejected

- A `--force` or run-only flag would not be durable policy and could drift when a run
  resumes; the default is persisted on the Goal and old GOAL state is upgraded during
  the explicit schema migration.
- Completing in `Runner.finish()` from a read-only summary has a TOCTOU gap; completing
  directly in every verification settlement couples unrelated `verify` commands to Goal
  completion. The typed action plus locked application transaction keeps one owner for
  each responsibility.

**Falsified if:** `internal/work/completion.go`, `internal/storage/migrate.go`, or
`internal/app/service.go` no longer require the exact current Verification Evidence set
and current Candidate facts before writing Goal completion.
