# 05: 完成與依賴原子解鎖

**What to build:** 這是整條佇列第一次真的會前進。`review approve` 在記錄審查的同一次交易內檢查四項條件——同一 revision 的最新 Verification 為 PASS、最新 Human Review 為 APPROVED、該工作無未解除 Gate、其 Goal 為 ACTIVE——全部滿足就進入 DONE，並在同一次交易內把因此滿足依賴的下游工作從 PENDING 轉為 READY。

產品裡不存在完成指令。DONE 只能是條件被滿足後的結果，不是一個可以下達的命令。

判定一律取同一 revision 上最新的一筆：較新的 FAIL 勝過較舊的 PASS，較新的 REJECTED 勝過較舊的 APPROVED。這讓「重跑驗證確認 PASS 是否穩定」這個能力有真實效果，而不是反過來變成漏洞。

**Blocked by:** 03, 04

**Status:** ready-for-agent

- [ ] 四項條件全部滿足時，approve 於同一交易內讓工作進入 DONE
- [ ] 未驗證、或最新 Verification 非 PASS 時不完成
- [ ] approve 當下的 HEAD 與最新 PASS 的 revision 不同時，審查仍被記錄但不完成，且 `status` 說明未完成的原因
- [ ] 同一 revision 上較新的 FAIL 勝過較舊的 PASS；較新的 REJECTED 勝過較舊的 APPROVED
- [ ] 有未解除 Gate 時不完成
- [ ] Goal 非 ACTIVE 時不完成
- [ ] 完成後，全部依賴皆為 DONE 的下游工作在同一交易內轉為 READY
- [ ] 仍有其他未完成依賴的下游工作不被解鎖
- [ ] 新 process 讀回時只會看到解鎖前或解鎖後，不存在「A 已 DONE 但 B 仍 PENDING」的中間狀態
- [ ] 產品中不存在任何能直接寫入 DONE 的指令或路徑，也不存在 reopen
- [ ] DONE 的工作不被標示 stale，但仍顯示其完成時的 revision
- [ ] `make verify` 通過
