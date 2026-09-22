# Long-running Runner MVP

## 這一版要做的事

人類完成 Goal、Story、Work Item 與依賴的前置規劃之後，`forgepilot run --goal <goal-id>` 依 ForgePilot 的判定自動取得下一個合法動作、必要時啟動一個新的 coding agent session 實作指定的 Work Item、跑正式 verification、重新讀取狀態，再繼續下一項；因 Gate、預算或異常而停止，或在全部 current verification 條件滿足時自動完成 Goal。`goal create --review-policy goal` 是唯一 Goal-level 自動完成模式，不提供人工終審選項。

責任分工是這一版唯一的設計主張：**Runner 負責執行，ForgePilot 負責判定，PraxisBound 負責工程驗證規範。**細節記在 [ADR-0019](../../adr/0019-runner-executes-forgepilot-decides.md)。

## 範圍

做進 ForgePilot，單一 workspace、單一 Runner、單一指定 Goal、循序執行。只接受 ACTIVE、非空、review policy 為 `GOAL` 的 Goal。只支援 snapshot verification，不自動 commit。一個真實 Codex CLI adapter，加一個不依賴網路的 fake subprocess adapter。每次實作或 repair attempt 都建立新的 Agent session。Goal 條件滿足時由 typed transaction 自動完成，Work Item 維持 VERIFIED，不產生 Human Review。

不包含：多 Agent 平行執行、多 Goal 自動切換、daemon、排程、Web UI、A2A、MCP、遠端執行、自動 merge／release、Goal 最終人工核准指令。不修改 PraxisBound，不新增通用 workflow framework。

## 分層

| 元件 | 責任 |
|---|---|
| `internal/cli` | 參數、進度呈現、停止原因、退出碼 |
| `internal/app` | Goal-scoped typed 查詢、start、reconcile、verification、recovery，CLI 與 Runner 共用 |
| `internal/runner` | 執行迴圈、session 邊界、預算、停止條件、執行紀錄 |
| `internal/agent` | 啟動 coding CLI、交接內容、結果解析、程序控制 |
| 既有 `work`／`repository`／`storage` | 合法狀態轉移、Git facts、交易與原子保存 |

`internal/work` 維持純狀態機——沒有 filesystem、Git、subprocess 或 agent runtime interface。Git 操作仍集中於 `internal/repository`。verification orchestration 只有一份，從 `internal/cli/verify.go` 抽到 `internal/app`，CLI 與 Runner 都呼叫它，不複製。

Agent runtime（Codex 等 coding CLI）與 Verification toolchain（Candidate checkout 所要求的 Go／Node／Python）是兩件事：agent adapter 不進入既有 runtime resolution。

## CLI 契約

見 [development-plan.md](../../development-plan.md#long-running-runner-mvp) 的契約表與退出碼表；該表是 flag 命名的唯一依據。

## 執行規則

**Typed query。** Runner 呼叫 `State.ActionableNextForGoal(goalID, repository)`，不解析任何 CLI 文字。Goal-scoped 與全域查詢共用同一份 priority／legality 實作，既有全域 `next` 行為不得改變。

**Action 對應。**

| Action | 行為 |
|---|---|
| `START` | 既有 `StartWithRepository`，成功後開新 session 實作 |
| `RESUME` | 重新讀取現況，開新 session 接續 |
| `REPAIR` | 開新 session，附帶有限失敗摘要 |
| `RECONCILE` | 既有 `ReconcileGoalReadiness`，再重新查詢 |
| `REVERIFY` | 正式 verification，不重啟整段實作 |
| `COMPLETE_GOAL` | GOAL policy 在鎖內重驗 Candidate／Evidence／Gate，寫入 Goal completion evidence 後完成，退出碼 0 |
| `WAIT_GATE`／`WAIT_GOAL`／`WAIT_HUMAN_REVIEW` | 記錄原因並停止，不解除阻擋，退出碼 2 |
| `NONE` | 以 `GoalStall` 分類具體原因，不視為完成 |

每個動作前後重新讀取 state 與必要 repository facts。`NONE` 至少區分 live verification、orphan verification、依賴未滿足、被阻擋，以及其他無法前進的情況。

**Scope change。** run 開始時記錄 Goal 的 scope fingerprint（每件 Work Item 的 ID、StoryRef 與排序後的依賴）。每次迴圈重算並比對，不同即停止並回報 scope changed。

**Verification 權威。** 正式 verification 沿用既有 snapshot capture、detached worktree、toolchain resolution 與 Evidence 保存。Agent exit code 0、Agent 宣稱完成、verification 命令 exit code 0 都不是 PASS。模型、認證、log 或 toolchain 問題分類為 refusal 或 operational error，不製造假的工程 FAIL。Runner 不直接寫 VERIFIED／DONE。

**Candidate freshness 與完成。** 沿用既有 dependency freshness 與排序。必要 prerequisite 已 stale 時先補足驗證。每張工作需要自己的合法 Evidence。GOAL policy 的 `GoalSummary` projection 只在所有 Work Item 最新 PASS 都匹配 current Candidate 且沒有 OPEN Gate 時給出完成 action；typed transaction 會再次核對 exact IDs，寫入 aggregate evidence 後完成 Goal，不改 VERIFIED 為 DONE，也不等待 Goal-level Human Review。

## Agent session

每張工作、每次 repair 開新 session；不使用 `resume --last`。以 executable 與 argument array 啟動，prompt 走 stdin，workspace 路徑明確指定，採明確的 workspace 寫入權限，不取消 sandbox、不自動升權。

交接內容：Goal、Work Item、本次 action；Story 路徑與驗收要求；`AGENTS.md` 與必要 ADR 參照；依賴工作與目前 Candidate 摘要；前次 attempt 的有限摘要；必要的 verification 失敗摘錄；本次禁止操作與停止條件。有大小上限，超限時提供檔案參照與有界摘錄，不靜默刪除驗收條件。

worker 只處理指定的 Work Item，不自行選下一張、不推進 lifecycle、不核准 review、不解除 Gate、不完成 Goal、不改 ForgePilot state、不降低驗收條件，也不 commit、push、merge、release。

結構化結果區分 `implementation_finished`、`needs_human`、`execution_failed`，包含摘要與必要的未完成事項／人工問題，並驗證型別與必要欄位。exit code 0 但缺少合法結果是 runtime protocol error。`needs_human` 必須停止。

## 執行保護

**Workspace ownership。** 涵蓋整段 Runner 的 workspace lock，使用 canonical workspace identity（symlink 別名不能啟動第二個 Runner）。不在模型或測試執行期間持有 state transaction lock。這個 lock 只協調 Runner。

**中斷與恢復。** 程序 ownership 與 fail-closed 判定見 [ADR-0020](../../adr/0020-worker-ownership-is-fail-closed.md)。恢復時重新讀取最新 domain state；orphan verification 走既有 exclusive lock 與 reclaim 流程。

**無進展偵測。** 以 Work Item、action、Candidate、verification 結果／freshness、Gate、Goal 等語意事實判斷。新的 Evidence ID、timestamp、log 量或 attempt 編號不算進展。

**執行紀錄。** `.forgepilot/runs/<run-id>/`，記錄 run id、workspace identity、Goal 與啟動範圍、runtime executable／version、執行設定與 deadline、step／attempt／預算、程序 ownership、Evidence IDs、停止原因與交接摘要。這是 execution history，不是第二份 lifecycle。原子保存，保存失敗即停止。單次輸出、單 run 與 Runner artifacts 總容量有具體上限，測試可注入較小值。不自動刪除總檢需要的 artifacts。Runner artifacts 落在既有 `.forgepilot/` ignore 範圍內，不改變 Candidate digest。不保存完整環境變數、token 或認證檔。

## 驗收

見 [tickets](issues/)。預設測試不依賴真實模型、帳號或網路；除 unit tests 外必須以 fake Agent subprocess 驗證程序、鎖與 crash recovery。真實 Codex smoke test 採 opt-in，不進預設 CI。
