# 沒有完成指令：approve 在條件滿足時直接完成工作

`review approve` 記錄 APPROVED 的 Human Review Evidence；若同一 revision 的最新 Verification 為 PASS 且該 Work Item 沒有未解除的 Gate，就在同一次交易內進入 DONE 並解鎖下游依賴。ForgePilot 不提供 `done`、`complete` 或任何等價的指令。

一個獨立的完成指令會是「什麼都不做、只是宣告完成」的東西，而那離架構明文排除的「任意 state setter、complete」只有一步之遙——一旦存在，條件不足時人就會開始想辦法讓它成功。讓 DONE 只能是條件被滿足後的結果，而不是一個可以下達的命令，是這個產品「不提供繞過驗證或審查的完成捷徑」在指令層的具體形式。

**Consequences:** approve 當下的 HEAD 與最新 PASS 的 revision 對不上時，審查仍被記錄——先審後驗是正當流程——但不會進入 DONE。這會造成「我 approve 了卻沒完成」的困惑，因此 `status` 必須說出沒有進入 DONE 的原因，不能只是靜靜地停著。

**Falsified if:** `internal/cli` 出現任何能直接寫入 DONE 的路徑。那不是實作細節的變動，而是這個決定被推翻。
