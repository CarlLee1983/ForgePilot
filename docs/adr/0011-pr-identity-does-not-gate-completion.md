# PR identity 不參與完成判定

M4 讓 Human Review Evidence 可以攜帶 PR reference，但 DONE 的四項條件一字不改，`Stale` 的定義也一字不改。PR reference 是識別資料，沒有任何規則讀它。

理由是同一個 commit SHA 就是同一份程式碼——它在哪個 PR 底下被審查，不改變被審查的內容。development-plan 對 M4 的 exit criteria 是「新 HEAD 必須重新取得適用 review Evidence」，那由既有的 SHA 比對天然成立，PR number 在其中不需要扮演任何角色。

要讓 PR 參與判定，得先有一個「這件工作應該對應哪個 PR」的宣告可供比對，也就是 Work Item 上一個可變的 PR 欄位。那正是 [ADR-0003](0003-no-work-item-target-revision.md) 拒絕的形狀，理由相同：它會立刻產生「誰負責讓它保持正確」的問題，而 PR 關掉重開、改推到另一個 PR 都是常見的事。

此條特別記錄，是因為 Evidence 上會有一個沒有任何規則讀取的欄位，日後讀者很可能把它當成未接完的線而「補上」。不接是決定，不是遺漏。

**Consequences:** 同一個 SHA 上、標著不同 PR 的兩筆 review，彼此互相取代（取最新一筆），不會因為 PR 不同而被視為兩條獨立的審查軌跡。標錯 PR 不會讓一次核准失效——它是一筆記錯的識別資料，不是一次無效的審查。

**Falsified if:** `internal/work/evidence.go` 的 `CompletionBlockers` 或 `Stale` 開始讀 Evidence 的 PR 欄位，或 `internal/work/work.go` 的 Item 出現 PR 欄位。屆時必須先回答 ADR-0003 當初回答過的那個問題：誰負責讓那個欄位保持正確。
