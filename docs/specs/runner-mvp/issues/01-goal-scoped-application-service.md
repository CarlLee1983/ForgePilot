# 01 — Goal-scoped typed application service

ADR、spec 與 CLI 契約落地，並抽出 CLI 與 Runner 共用的 application service。

## 交付

- `internal/work`：`ActionableNextForGoal(goalID, repository)` 與 `GoalStall(goalID, repository)`；與全域 `ActionableNext` 共用同一份 priority／legality 實作。
- `internal/app`：`Next`、`Start`、`Reconcile`、`GoalReadiness` 的 typed 包裝，以及從 `internal/cli/verify.go` 搬過來的 verification orchestration。
- `internal/cli/verify.go` 改為薄殼，輸出文字與退出碼完全不變。

## 驗收

- [x] 同一 repository 有兩個 Goal 時，goal-scoped query 只回傳指定 Goal 的動作；另一個 Goal 排在前面也不會造成假停滯。
- [x] 篩選 Goal 不影響依賴判斷所需的完整 state。
- [x] 全域 `next` 的既有輸出與排序完全不變（既有測試不修改即通過）。
- [x] `GoalStall` 分辨 VERIFYING、依賴未滿足、被 Gate／Goal 阻擋、空 Goal 與未知情況。
- [x] `verify` 的 stdout 文字、警告與退出碼與搬移前逐字相同。
