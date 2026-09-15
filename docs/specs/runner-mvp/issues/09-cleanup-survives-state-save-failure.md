# 09 — 已觀察到的未確認程序不因存檔失敗而消失

[08](08-recovery-closure.md) 之後的第四輪 dogfood 修正，範圍是**一條複合失敗路徑**。
不重新設計 Runner、storage 或 lifecycle，不擴充任何執行控制能力，沒有新的架構決定，
因此沒有新的 ADR——[ADR-0022](../../../adr/0022-pending-cleanup-outlives-the-process.md)
的既有不變量在這條路徑上本來就應該成立，只是實作把它漏掉了。

不變量講一次就夠：**已觀察到的未確認程序，不得因另一個操作失敗而消失。**
Evidence 有沒有成功保存，與程序有沒有被確認停止，是兩個不同的事實。

## 缺口

`internal/app/verify.go` 的 `runVerification()`，canonical check 產生結果之後：

1. `storage.Update` 的 callback 呼叫 `CandidateFacts` 讀 workspace facts。
2. 那次讀取回報 `process.ErrNotSettled`——一個沒被確認停空的 Git 程序群組——
   但那個事實此刻只存在於 callback 外的 `factsErr` 區域變數裡。
3. callback 返回後，`storage.Update` 在 `state.Validate()` 或原子替換失敗。
4. 舊的寫法是 `if err := storage.Update(...); err != nil { return result, err }`：
   直接返回，**跳過**下面那段把 `factsErr` 記進 `VerifyResult.Cleanup` 與 `Unresolved` 的程式碼。
5. `internal/runner` 的 `verify()` 因此收到 `result.Cleanup == nil`，
   於是解除它在驗證開始前寫下的 placeholder Pending、保存、再把錯誤當一般操作失敗往上丟。

結果：`state.json` 存檔失敗、`run.json` 卻照常保存的那個窗口裡，
下一次啟動必須遵守的恢復阻擋被清掉了。08 的驗收只覆蓋了 transaction **成功**的情況
（`facts refresh 回傳 ErrNotSettled 時一併帶回`），所以這條路徑一直是綠的。

## 修正

`internal/app/verify.go`：取得 `updateErr` 而不是就地返回，順序改成

```
storage.Update(...)               // 原有交易，不動
  ↓
factsErr 裡的未確認程序 → result.note(UnresolvedGit, root, …)   // 兩條路徑都做，只做一次
  ↓
updateErr != nil → return result, updateErr                     // 原始錯誤原樣返回
  ↓
交易成功才設定 Evidence／HasEvidence／Status／RefreshWarning
```

`result.note` 在提前返回**之前**成立，所以既有的 deferred 清理讀到 `result.Cleanup != nil`，
走「留著 checkout」那條分支，不把恢復紀錄指向的目錄當普通善後刪掉。
`updateErr` 不被包裝，`errors.Is`／`errors.As` 仍認得原始存檔失敗。
記憶體裡的 Evidence 不被宣稱已保存：`HasEvidence` 留在 false，不補造 PASS／FAIL，
也不把存檔失敗轉成工程 FAIL；`Reclaimed` 與先前已保存的 Evidence 完全不動。

`internal/runner` 不改。它既有的「Cleanup 優先於一般操作錯誤」分支本來就對，
placeholder Pending 換成具體群組的 Pending 也本來就在同一次原子替換裡完成——
問題自始至終是 app 沒把事實傳上去。

## 測試接縫

`internal/storage/inject.go`：`InjectStateSaveFailure(root, fail)`，形狀比照
`internal/process/inject.go`，理由也一樣——這個失敗不是真實硬體可以在指定時刻被要求產生的。

- 只對指定 workspace 的 `.forgepilot/state.json` 生效，比對完整目的地路徑；
  `.forgepilot/runs/<run-id>/run.json` 走同一個 `writeFileAtomically` 但目的地不同，不受影響。
- 命中點在 `os.Rename` 之前：暫存檔已寫出但未替換，defer 會移除它，
  所以「這次 Evidence 尚未發布」是可以斷言的事實。
- one-shot：命中即自行解除，失敗之後的寫入（恢復紀錄、下一個程序）照常。
- internal package、傳函式而非讀環境變數、沒有 CLI flag 或設定可以到達；
  正常執行時是一次對空字串的比對。
- 共用同一個接縫的測試不並行。

不用填滿磁碟、改 `.forgepilot` 權限或刪除 `state.json` 模擬：那些會連 run record 一起破壞，
之後的 CLI 只是因為資料毀損而拒絕，證明不了這一輪要修的東西。

## 驗收

- [x] `TestRefreshCleanupSurvivesStateSaveFailure`（`internal/app`）：
      無 orphan 的 fixture，canonical check 正常完成後，只在後續 workspace facts refresh 的
      Git 階段注入 `ErrNotSettled`，並**從那個注入裡**武裝 state 存檔失敗——
      因此它只可能落在同一次交易的原子替換上。兩個故障都有命中紀錄，順序被斷言。
- [x] 返回的 err 仍 `errors.Is` 得到原始存檔錯誤；`result.Cleanup` 是 `process.ErrNotSettled`；
      `Unresolved` 恰好一筆，Kind `GIT`、PGID 等於注入時觀察到的群組、Location 等於 facts 實際
      查詢的 workspace root。
- [x] `HasEvidence` 為 false、`Status` 為空、state 仍可解析且沒有新的 PASS／FAIL、
      工作仍在 `VERIFYING`、checkout 沒有被追加刪除。
- [x] `TestRunnerPreservesPendingWhenStateSaveFails`（跨 CLI 程序）：
      兩張可依序執行的任務、既有 fake runtime、同樣的雙故障，未確認錯誤指向測試自己建立且仍存活的
      程序群組。當次 Runner 停在 `RECOVERY_BLOCKED`，停止資訊含原始存檔錯誤，
      `run.json` 的 unresolved Pending 之 Kind／PGID／Location 與安排相符，不啟動下一張任務。
- [x] 撤除注入但保留群組存活後，以真正的新 CLI 程序執行 resume 原 run、同 Goal 新 run、
      同 workspace 另一個 Goal：三者皆 `RECOVERY_BLOCKED`、退出碼 2，
      輸出指名未解決的 `GIT` 群組，不新增 session，不增加 steps、不改寫 deadline，不清除 Pending。
- [x] 只停止測試自己建立、ownership 可確認的群組；確認消失後同一次合法 resume 即解除阻擋，
      Pending 清空，不需要人編輯紀錄。
- [x] 對照案例：沒有未確認程序＋存檔失敗 → 一般操作錯誤，沒有虛構的 Cleanup／Pending／PASS／FAIL，
      checkout 照常被清掉（`TestStateSaveFailureAloneIsAnOrdinaryFailure`）。
      它武裝存檔失敗的方式與主測試同一個機制——從一次**成功**的 facts read 裡武裝——
      所以它確實走到被修改的那段（`updateErr != nil && factsErr == nil`）；
      若在命令開始前就武裝，會停在 `beginRun` 而永遠碰不到那段，coverage 為 0；
      有未確認程序＋存檔成功 → Evidence 照常保存並可讀回，Cleanup 仍帶回
      （`TestRefreshCleanupTravelsWithSavedEvidence`）；
      沒有未確認程序＋存檔成功 → PASS 保存、checkout 清掉、Goal 人工審查邊界不變
      （`TestAnUndisturbedVerificationStillPassesAndTidiesUp`）。
- [x] `make verify` 與 `go test -race -count=1 ./...`；本輪雙故障與 recovery 測試另跑
      `-race -count=3`。見交付報告。
- [ ] 真實 Codex smoke（`FORGEPILOT_CODEX_SMOKE=1`）。**NOT RUN**——opt-in，本輪未取得額度授權；
      fake runtime 的綠燈不是真實模型驗收。

## 後續

解除阻擋之後同一個 run 繼續執行到 Goal 最終人工審查的那一段，
由 [10](10-recovery-to-final-review.md) 補上驗收（含負向對照）。
本票的驗收範圍不因此改變。

## 不在範圍

重新設計 Runner／storage／lifecycle、擴充 runtime／daemon／scheduler／A2A／MCP／UI、
更動 DONE／Gate／Goal 最終人工審查規則、更改獨立 `verify` 是否讀 run record 的契約、
`--force`／刪除 `run.json`／自動清空恢復資訊、關閉 hooks 與 filters。

## 未關閉的邊界

- 07 的兩項未驗收項目之一（存檔失敗的注入）由這一輪提供接縫並在**這條**路徑上驗收；
  07 其餘的 crash window 仍未驗收，本輪沒有動它們。
- 這一輪只證明 rename 之前失敗的情況。rename 之後的 sync 失敗是另一種形狀：
  存檔函式回報錯誤不代表磁碟完全沒變，那種情況仍須保留 Cleanup，但不在這一輪重設計儲存交易。
- `reclaimOrphan` 仍會移除它回收的舊 worktree（見 ADR-0022 Consequences），本輪未觸及。
