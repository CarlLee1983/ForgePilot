# 08 — 恢復判準只有一份、清理不被吞掉、停止原因不被改名

[07](07-recovery-hardening.md) 之後的第三輪 dogfood 修正。同樣不重新設計 Runner、不加新能力。
07 把「未確認的清理是持久化事實」做進 `Record.Pending`，這一輪處理的是**同一條規則在其他呼叫路徑上不成立**：
判準有兩份而且分岔了、清理錯誤在幾個出口被吞掉、預算在錯的時刻開始、停止原因在一條路徑上被改名。

責任分工不變（[ADR-0019](../../../adr/0019-runner-executes-forgepilot-decides.md)）、
ownership 仍 fail-closed（[ADR-0020](../../../adr/0020-worker-ownership-is-fail-closed.md)）、
期限與清理寬限仍是兩個量（[ADR-0021](../../../adr/0021-execution-limits-are-bounded-and-named.md)）、
恢復阻擋仍活在 workspace 而不是一次 run（[ADR-0022](../../../adr/0022-pending-cleanup-outlives-the-process.md)）。
沒有新的架構決定，所以沒有新的 ADR——四份既有決定的**實作**在這一輪才在所有路徑上成立。

## 四個缺口

**A — Worker 與 Pending 的恢復判準分岔了。** `judgePending` 要求「leader 不在了」還要加上
「`kill(-pgid, 0)` 回報群組為空」才放行；`recover()` 處理 `Record.Worker` 時，`agent.Gone` 與
`agent.Unrelated` 直接印「the worker is gone」並清掉紀錄，從不問群組。這不只是舊紀錄的相容路徑：
現行 agent 啟動流程先存 `Worker.Identity`，只有在清理失敗時才寫 `Pending`，所以兩者之間的 crash
留下的就是「有 Worker、沒有 Pending」。leader 已退出而它 fork 的 `make verify` 還在寫 workspace 時，
三個入口——resume、同 Goal 新 run、同 workspace 另一個 Goal——全部放行。

同一段還有第二個問題：`settleWorkspace` 判斷某個舊 run 是否阻擋時讀的是 `existing.Stop`，
而那可能是上一個程序寫下的歷史 `RECOVERY_BLOCKED`。群組後來真的消失時，這一次恢復其實成功了，
卻仍然被舊 `Stop` 擋下，要人再跑一次同樣的指令才會過。

**B — 清理階段把未確認的程序吞掉。** `RemoveWorktree` 遇到任何 Git 錯誤就 `os.RemoveAll(path)`：
`ErrNotSettled` 因此變成 nil，而且順手刪掉恢復紀錄指向的那個 checkout——正好是還有東西在寫的那個。
`AddWorktree` 的前置清理更直接，`_, _ = git(...)` 整個丟掉。`app.Verify` 這一側，runtime preflight
與 canonical preflight 失敗後的 `_ = repository.RemoveWorktree(...)`、驗證結束的 deferred 清理，
以及驗證後 facts／readiness refresh 的失敗，都只是 warning 或什麼都不是：Runner 因此拿不到
任何可持久化的東西，一個 PASS 之後留下未確認程序的 run 會直接繼續下一張工作。

**C — 清理預算在開工前就開始計時。** `runVerification` 以
`reclaimOrphan(cleanup.context(), ...)` 呼叫回收，而參數求值會**先**打開 cleanup window。
沒有 orphan 時也一樣：snapshot、checkout、preflight 與正式驗證全部在這個 window 裡跑掉，
等到真正要善後時 deadline 早已過期。實測的後果比「預算變少」更嚴重——過期的 context 讓
`process.Start` 在啟動前就返回，於是 checkout 根本沒有被移除，也沒有任何人被告知。
有 orphan 時則是另一個問題：開工前的回收與收工後的善後共用同一份 deadline。

**D — 步驟之間的 Git 查詢被取消時停止原因被改名。** `readFacts()` 在沒有 `ErrNotSettled` 的取消情況下
解除 Pending 後直接回傳 `readErr`。上層對它的處理各不相同：loop 的 decision 讀當成一般操作失敗（退出碼 1，
連 stop reason 都沒有），`start` 與 `reconcile` 記成 `STALLED`。一個 Ctrl-C 因此有三個名字，
沒有一個是退出碼所依據的那個。

## 交付

- `internal/runner/recovery.go`：`settleExecution` 是恢復唯一的安全判準，`judgePending` 與
  `recover()` 的 Worker 分支共用它；完整 identity 的情形直接重用 `agent.TerminateOwned`，
  不再各寫一份 Gone／Unrelated／Unknown 規則。
- `internal/runner/runner.go`：`recover()` 回報「這一次是否拒絕」，`settleWorkspace` 用它而不是
  讀 `record.Stop`；`readFacts` 在解除 Pending 之後、回傳一般錯誤之前，先以本次執行已成立的
  stop cause 記下停止原因。
- `internal/repository/verification.go`：`RemoveWorktree` 的 filesystem fallback 只留給它原本要處理的
  一般失敗，`ErrNotSettled` 原樣回傳；`AddWorktree` 的前置清理同樣不再吞掉它。
- `internal/app/verify.go`：兩份 cleanup window（回收 orphan 一份、收工善後一份），各自在自己的階段
  第一次用到時才打開；每一個清理出口（reclaim、runtime preflight、canonical preflight、deferred 移除、
  facts refresh）把未確認的群組記進 `VerifyResult.Cleanup` 與 `Unresolved`。
- `internal/process`：`Budget.Remaining()`；內部故障注入邊界同時收到「正在收尾哪一個受管理指令」
  與「這個階段還剩多少預算」，測試因此能指名階段而不是撞上先跑到的那一個。argv 只在經過
  `process.Start` 的停止路徑上有值——`internal/agent` 的長駐 Session 自己持有 handle、直接呼叫
  `Settle`，那條路徑收到的是空的。
- `internal/cli/verify.go`：未確認清理的警告改為從 `Unresolved[].Kind` 讀出實際階段，
  不再寫死「canonical check 的程序群組」——現在它也可能來自回收、preflight、收工清理或 facts refresh。
- `internal/runner`：`recover`／`recoverPending`／`readFacts` 移進 `recovery.go`，
  `runner.go` 從 1229 行降到 1097（專案上限 800，這一輪讓它往下走而不是往上）。
- 測試：見下方驗收。`internal/app/cleanup_test.go` 兩個 subtest 改為真的進入不同階段並實際斷言
  `wantKind`、PGID 與 checkout 位置。

## 不在範圍

重新設計 Runner、第二套 lifecycle、Work Item DONE／Gate／Goal 最終人工審查規則、
自動 approve／commit／push／merge／tag、daemon／scheduler／多 Goal 並行／新 runtime／A2A／MCP／UI、
`--force` 或任何自動刪除 `run.json`／Worker／Pending 的恢復捷徑、關閉 Git hooks／filters、
獨立 `forgepilot verify` 的預設執行期限或它是否讀 run record 的契約。

## 驗收

### A — 恢復判準只有一份

- [x] leader 已退出、同 PGID 子程序仍存活、紀錄只有 Worker 沒有 Pending：resume、同 Goal 新 run、
      同 workspace 另一個 Goal 三者都 `RECOVERY_BLOCKED`、退出碼 2，且不啟動任何 session。
- [x] 被阻擋的嘗試不增加 steps／attempts，不改寫既有 deadline，Worker 紀錄保留。
- [x] 群組確認消失後，同一次合法啟動即解除阻擋並清掉 Worker，不需要人再跑一次。
- [x] PID 被重用（identity 不符）時阻擋，且不對該程序群組送出任何 signal。
- [x] 歷史 `RECOVERY_BLOCKED` 的 `Stop` 不會讓一個已經可確認的 workspace 繼續被拒。
- [x] 舊的「pid 與群組都確實不存在」的 Worker 紀錄仍照舊恢復。

### B — 清理不被吞掉

- [x] `RemoveWorktree` 在 Git 回報 `ErrNotSettled` 時原樣回傳，不追加 `os.RemoveAll`；
      目錄仍存在的可控失敗案例中目錄未被刪除。
- [x] Git 已經完成刪除才回報未確認時，不假裝可以回滾，但也不回報成功。
- [x] `AddWorktree` 的前置清理同樣不吞掉 `ErrNotSettled`。
- [x] 一般清理失敗（Git 不認得的殘留目錄）仍照舊被清掉，沒有因為修正而永久阻擋。
- [x] 驗證產生 PASS 之後的 deferred 清理未確認時：Evidence 不變、`VerifyResult.Cleanup` 與
      `Unresolved`（kind、PGID、checkout 位置）帶回呼叫端，不只印 warning。
- [x] Runner 收到之後保存為 unresolved `Pending` 並停在 `RECOVERY_BLOCKED`；
      以新的 CLI 程序 resume／新 run／另一個 Goal 皆被阻擋；群組確認消失後解除。
- [x] 驗證後 facts／readiness refresh 回傳 `ErrNotSettled` 時一併帶回，且因為它成立在 PASS **之後**，
      接著的 deferred 清理改走「留著 checkout」那條路。
      **邊界（這一輪只覆蓋 transaction 成功的情況）**：上面這條的測試走的是「facts refresh 回報未確認、
      而記錄 Evidence 的那次 `storage.Update` 成功」。同一個 `factsErr` 在 transaction **失敗**時被
      跳過不記——舊碼在 `storage.Update` 回傳錯誤時直接 return，`result.note` 從未執行——
      因此存檔失敗但 run record 仍可保存的窗口裡，恢復阻擋會整個消失。
      這條複合路徑由 [09](09-cleanup-survives-state-save-failure.md) 修正並驗收。

### C — 清理預算的生命週期

- [x] 沒有 orphan、業務工作時間超過 `CleanupGrace`：真正進入善後時仍有 > 0 的有效預算，
      且 checkout 確實被移除。
- [x] 有 orphan：開工前回收用掉的 window 不影響收工善後的 window。
- [x] 同一清理階段中連續數個受管理指令共用一份遞減的預算，不每層重領。
- [x] 沒有使用無期限 context，沒有新增使用者可調的 bypass，沒有放大 grace。

### D — 停止原因

- [x] 步驟之間的 Candidate facts 查詢中送 SIGINT：`INTERRUPTED`、退出碼 130。
- [x] 同上送 SIGTERM：`TERMINATED`、退出碼 143。
- [x] 同上讓 run deadline 到期：`MAX_DURATION`、退出碼 3。
- [x] 三者都不產生 `STALLED`，也不退化成沒有 stop reason 的退出碼 1。
- [x] 三者都不啟動新的 session，且被確認清除的 facts 讀不留下 `Pending`。
- [x] 一般 Git 失敗（filter 直接非零退出）仍是操作失敗，不被歸類成停止。

### E — 測試可信度

- [x] `TestACancellationDoesNotSwallowAnUnconfirmedCleanup` 的兩個 subtest 真的分別進入
      canonical preflight 與 runtime preflight；把其中一個的 `wantKind` 對調會失敗。
- [x] 斷言涵蓋 `Unresolved.Kind`、PGID > 0、Location 等於本次 checkout，以及原始中斷原因仍可讀。
- [x] 沒有偽造工程 FAIL。
- [x] 子程序測試使用握手與明確 timeout；只對測試自己建立、ownership 可確認的程序群組送 signal。

### F — 既有行為

- [x] `make verify` 與 `go test -race -count=1 ./...` 全綠（見交付報告的平台與時間）。
- [x] 正常 PASS、工程 FAIL、有限 repair、Gate 與 Goal review 不退化，最終仍停在 `AWAITING_GOAL_REVIEW`。
- [x] 獨立 `forgepilot verify` 的契約不變。
- [ ] 真實 Codex smoke（`FORGEPILOT_CODEX_SMOKE=1`）。**NOT RUN**——opt-in，未取得額度授權。
      後續：[ticket 11](11-real-codex-smoke-acceptance.md) 於 `2deaf14` 實跑一輪並留下證據；
      那是本輪之後的獨立驗收，本輪當時的 NOT RUN 是歷史事實。

## 這一輪的代價

`make verify` 變慢約 50 秒，全部落在 `internal/app`（1.9s → 53s）。來源是
`cleanup_window_test.go` 的兩個測試各跑一次超過 `process.CleanupGrace`（22 秒）的業務工作——
那是「業務時間長過一個 cleanup window」這個關係唯一誠實的表達方式，除非把 `CleanupGrace`
做成可注入的，而那是把測試需求變成生產介面。這筆代價寫在這裡，免得下一個人讀成效能退化。

## 未關閉的邊界

- 07 的兩項未驗收項目（準備完成到 `agent.Start` 之間的取消、存檔失敗的注入）仍未驗收，本輪沒有動它們。
  後者的接縫與其中一條路徑的驗收在 [09](09-cleanup-survives-state-save-failure.md)；
  07 其餘的 crash window 至今仍未驗收。
- `reclaimOrphan` 仍會移除它回收的舊 worktree；這一輪讓那個移除**回報**未確認的群組並阻擋後續執行，
  但沒有改變「獨立 `forgepilot verify` 不讀 run record」的契約，ADR-0022 Consequences 描述的暴露面依舊。
