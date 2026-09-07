# 03: `status` 呈現與 M4 端到端驗收

**What to build:** 存下來的 PR Reference 有讀出來的地方。`status` 在顯示最新一筆 Human Review 時一併顯示它；那筆審查沒有 PR 就什麼都不印，也不出現任何提示字樣——缺 PR 是合法狀態，不是待辦事項。

以及證明整條路真的通：在真實 Git repository 上，以獨立 process 跑完 PR HEAD 上的驗證、核准、完成，然後讓 HEAD 改變，確認舊的核准保留為歷史但不適用，必須重新驗證與重新核准。這條同時驗到 M4 的三個硬約束欄位與「HEAD 一變就是新的 review target」。

**Blocked by:** 02

**驗收條件的修正**：原本寫成「同一件工作在 HEAD 改變後重新 verify 與 approve」，那與 ADR-0006 抵觸——DONE 是終態，沒有 reopen。HEAD 改變後仍需重新審查的主張，只對尚未完成的工作成立，因此改由被解鎖的下游那件工作承擔。

**Status:** done

- [x] `status` 顯示最新一筆 Human Review 的 PR Reference；該筆沒有 PR 時不印任何東西、不帶警告或提示語氣
- [x] 端到端流程以獨立 process 跑通：某個 HEAD 上 verify PASS → `review approve --pr` → DONE 並解鎖下游，Evidence 同時保存 PR Reference 與完整 SHA
- [x] 同一流程接著在下游那件工作上新增 commit 使 HEAD 改變，舊的 PASS 與 APPROVED 保留為歷史但不適用，工作無法憑它們完成
- [x] 重新 verify 與重新 approve 之後那件工作才完成，新的兩筆 Evidence 記的是新的 HEAD，先前的紀錄一筆未失
- [x] README 與 architecture 反映實際行為，M4 不再標示為未實作
- [x] development-plan 補上 M4 exit checklist 並逐項勾選
- [x] `make verify` 與 `go test -race -count=1 ./...` 實跑通過，記錄環境、commit 與工作目錄狀態
