# 07 — 取消到得了 Git、等待有上限、清理阻擋跨程序存活

[06](06-execution-control.md) 之後的第二輪 dogfood 修正。同樣不重新設計 Runner、不加新能力，
補上 06 交付後仍然開著的三個缺口。責任分工不變（[ADR-0019](../../../adr/0019-runner-executes-forgepilot-decides.md)）、
ownership 仍 fail-closed（[ADR-0020](../../../adr/0020-worker-ownership-is-fail-closed.md)）、
期限與清理寬限仍是兩個量（[ADR-0021](../../../adr/0021-execution-limits-are-bounded-and-named.md)）。
新的決定記在 [ADR-0022](../../../adr/0022-pending-cleanup-outlives-the-process.md)。

## 三個缺口

**A — 取消到不了執行中的 Git 子程序。** 06 把 `make -n verify`、runtime probe 與 canonical check
都接上了 context，`internal/repository` 的 Git 呼叫卻沒有：`git()` 用 `exec.Command` 加
`CombinedOutput()`，沒有 context、沒有程序群組、沒有上限。`CaptureSnapshot`、`PruneWorktrees`、
`AddWorktree` 以及它們背後的 `Head`、`EnsureClean`、`read-tree`／`add`／`write-tree` 因此全部在
取消路徑之外。一個 post-checkout hook 或一個卡住的 clean/smudge filter，就足以讓 Ctrl-C 之後的
Runner 停在「正在建立 worktree」上，而 `app/verify.go` 在這些呼叫之前只檢查 `ctx.Err()`——那是
啟動前的檢查，對已經開始執行的程序沒有作用。

**B — SIGKILL 之後的等待沒有上限。** `process.StopLeader` 送出 SIGKILL 之後是 `return <-finished`。
`finished` 不到，這個呼叫就永遠不返回。它在 `Session.Wait` 與 `process.Start` 兩條路徑上，
而且回傳值在 `Session.Wait` 被 `_ =` 丟掉。`CleanupGrace` 這個常數因此描述的是一條實際上
可以永遠不返回的路徑。

**C — 清理失敗只活一個程序的長度。** 未確認的清理寫進 `Stop.Reason = RECOVERY_BLOCKED`，
但下一次啟動的判準是 `settleAbandonedWorkers`，它只看 `Worker != nil`。canonical check 的程序群組、
runtime preflight 的 probe、Git 留下的東西都沒有 `Worker`，於是重啟 CLI、換 run ID 或在同一個
workspace 換一個 Goal，都會直接開始新的業務程序。`resume` 還會主動清掉 `Stop`。

## 交付

- `internal/repository`：Git 走與其他受管理程序相同的啟動、終止與確認路徑，並接受 context；
  保留 Git 專用語意（合併輸出、exit code、environment override、private index、snapshot ref）。
  呼叫鏈上每個 Runner 會用到的 helper 提供 `InContext` 形式。
- `internal/process`：`StopLeader` 在 SIGKILL 之後有上限，無法確認時回傳 `ErrNotSettled`；
  一次被叫停的執行只發一份清理預算，沿清理路徑遞減，不每層重領。
- `internal/agent`：`Session.Wait` 不再吞掉 `StopLeader` 的結果。
- `internal/app`：`VerifyResult` 在 `ctx.Err()` 成立時仍攜帶已觀察到的清理失敗，並帶回
  未確認程序群組的 typed 描述；未確認時不刪除恢復所需的 worktree。
- `internal/runner`：`Record.Pending` 持久化未解決的執行；`Start` 與 `Resume` 在 workspace lock
  內共用同一份 workspace 掃描；`Stop` 與 `Pending` 分開。
- 文件：ADR-0022 與 ADR index、architecture 的執行期限／ownership／recovery 邊界、
  development-plan 驗收矩陣、舊 run record 的相容與人工恢復說明。

## 不在範圍

重新設計 Runner、新平台承諾、第二套 lifecycle、第二份 verification orchestration、
把 Git 呼叫搬出 `internal/repository`、`internal/work` 加入 filesystem／subprocess／測試 interface、
更動 Work Item 狀態集合／DONE 規則／Gate 規則／Goal 最終人工審查、daemon／scheduler／多 Goal 並行／
runtime／A2A／MCP／UI、自動 commit／push／merge、獨立 `forgepilot verify` 的預設執行期限、
以關閉 hooks／停用 filters／略過 canonical check 迴避問題。

## 期限歸屬

| 階段 | 受哪一種期限約束 |
|---|---|
| 啟動準備（handoff、capacity、digest、Git 前置） | 本次執行有效期限 `min(run deadline, step deadline)` |
| 執行（agent session、canonical check、Git 子程序） | 同上 |
| 必要清理 | `process.CleanupGrace`，一次執行一份總預算，不隨層數重領 |

`resume` 不重置 deadline、steps、attempts 或容量設定。獨立 `forgepilot verify` 不新增預設執行期限。

## 驗收

### A — 取消到得了 Git

- [x] post-checkout hook 握手後送 SIGINT：hook 與受管理子程序停止，退出碼 130。
- [x] 同上送 SIGTERM：退出碼 143。
- [x] 同上讓 run deadline 到期：`MAX_DURATION`、退出碼 3。
- [x] 上述三種情況都不啟動正式 verification，也不開始下一張工作。
- [x] snapshot capture 被取消時，使用者 HEAD、branch、real index、staging state 與工作檔案未被破壞。
- [x] 未確認停止前，不刪除恢復所需的 worktree 與 ownership 紀錄。（Runner 路徑；`reclaimOrphan` 回收舊 worktree 的既有行為未改，見 ADR-0022 的 Consequences）
- [ ] 準備完成到 `agent.Start` 之間被取消時，不啟動 Agent。（已實作；沒有可靠的握手點能在整合層穩定命中這個窗口，未寫測試）

### B — 等待有上限

- [x] SIGTERM 後等待、SIGKILL 後等待、群組清理確認與輸出收集各有上限與可說明的返回條件。
- [x] wait 結果不到達時，SIGKILL 之後仍在有界時間內回傳未確認結果，不產生 PASS。
- [x] 延後到達的 wait 結果不覆寫已保存的停止原因或 Evidence；不重複 `command.Wait`。
- [x] 正常 exit 0、正常非零退出、背景子程序、繼承輸出描述元的子程序都不退化。
- [x] `CleanupGrace` 的值與文件符合真實控制流程。

### C — 清理阻擋跨程序

- [x] verification、preflight 或 Git 未能確認清理時，寫下持久化的未解決執行紀錄。
- [x] 新的 CLI 程序 resume 原 run、啟動新 run、同 workspace 換另一個可執行 Goal，三者都先阻擋。
- [x] 被阻擋的嘗試不消耗新的 steps／attempts，既有 run 的 deadline 不被改寫。
- [x] `resume` 清除 `Stop` 不清除 `Pending`。
- [x] ownership 不完整、PID 重用、leader 消失但群組仍在時不得放行。
      （這一條當時只在 `Pending` 路徑成立；`Record.Worker` 路徑的同一判準到 [08](08-recovery-closure.md) 才補上）
- [ ] 啟動前存檔失敗不啟動程序；啟動後存檔失敗嘗試有界停止並保留 pending 訊息。（已實作；需要注入 storage 寫入失敗，本輪未加該注入邊界）
- [x] runtime preflight／`make -n verify`／canonical check 因 `ctx.Err()` 提前返回時，清理失敗不遺失。
- [x] 已保存的 PASS／FAIL 不被清理失敗覆寫；被中斷且無結果者仍走 reclaim 保存 INTERRUPTED。
- [x] 舊 `Worker` 紀錄仍能恢復；舊的正常紀錄仍可讀；舊 blocked 紀錄缺乏可確認資訊時不自動放行；
      毀損的恢復資料不視為「沒有待清理工作」。

### D — 既有行為

- [x] 正常 PASS、工程 FAIL、有限 repair、snapshot freshness、Gate 與 Goal review 不退化。
- [x] 最終仍停在 `AWAITING_GOAL_REVIEW`，不自動 DONE／approve。
- [x] 獨立 `forgepilot verify` 契約不變。
