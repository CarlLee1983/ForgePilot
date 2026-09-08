# Verification 輸出保存在 state 之外，以 run 為鍵

一次 `forgepilot verify` 的原始輸出寫進 `.forgepilot/logs/<work-id>-<short-sha>-<started-at>.log`，邊執行邊串流；`current_run` 比照既有的 `WorktreePath` 多存一個 `LogPath`（schema v5）。Evidence 上不新增任何指向這份輸出的欄位。

輸出不進 state snapshot，是因為 [ADR-0001](0001-evidence-in-state-snapshot.md) 的失效條件寫的正是「單次 `state.json` 寫入的體積或延遲成為實際問題」。dbcli 一輪 `make verify` 的輸出是數百 KB，而每次寫入都要重寫整份歷史——把輸出塞進 snapshot 等於主動去踩自己寫下的失效條件，而不是等它自然發生。

Evidence 上不加欄位，則是為了避開 [ADR-0003](0003-no-work-item-target-revision.md) 與 [ADR-0011](0011-pr-identity-does-not-gate-completion.md) 已經拒絕過兩次的形狀：一個沒有任何規則讀取、卻要有人負責讓它保持正確的路徑欄位。以 run 為鍵之後路徑不必被指向也找得到——Evidence 自帶 Work Item ID 與完整 SHA，`ls .forgepilot/logs/<work-id>-<short-sha>-*` 就是它的輸出。`LogPath` 存在 `current_run` 上是不同的東西：它跟著一次執行生滅，理由與 `WorktreePath` 完全相同——回收孤兒時要知道上一輪實際寫到哪，而不是拿今天的命名規則去猜昨天那一輪。

同一個理由也回答了一個相關但不同的問題：要不要在 Evidence 上加欄位表達「這次執行是事後補跑，還是邊做邊跑」。ForgePilot 觀測得到的只有 revision 與時間；要它判斷使用者是不是事後補跑，等於要它替使用者的意圖背書，而它沒有任何管道取得那個意圖。這個欄位的形狀——沒有規則讀取它，卻要有人負責讓它保持正確——正是 ADR-0003、ADR-0011 與本篇拒絕過的同一種形狀，所以答案一致：不加。

備選是把 Evidence ID 當檔名，run 結束後更名。放棄它的理由是那讓一份檔案的正確名字依賴一次事後更名成功，而 `internal/cli/verify.go` 對收尾動作的承諾恰恰是「不得否決結果」；INTERRUPTED 的更名還要等到下一次 verify 的回收才發生，沒有下一次就永遠停在暫名。

輸出不自動清理，也不提供清理指令：`.forgepilot/` 已被忽略，成長由使用者處理。刪除是一條要有人負責的規則，在有人抱怨體積之前不值得發明。

**Consequences:** 非 PASS 時 `verify` 不再把全文印進 stdout，改為在執行開始前印出 log 路徑；INTERRUPTED 的執行因此第一次留得下截斷的輸出，回收孤兒時一併報出它的位置。log 是診斷材料，不是結論——結論只在 Evidence。

**Falsified if:** `internal/work/evidence.go` 的 `Evidence` 出現指向輸出的欄位，或 `internal/work/work.go` 的 `Run` 失去 `LogPath` 而改以命名規則推導，或 `internal/repository/verification.go` 不再串流而把整份輸出留在記憶體。前兩者要先回答 ADR-0003 當初回答過的問題：誰負責讓那個欄位保持正確。
