# 04: Stale 呈現與 REVIEW 重驗

**What to build:** 使用者執行 `status` 時，看得到每件工作最新一筆 Evidence 的結果與它綁定的 commit。當那個 commit 已經不是目前的 HEAD，`status` 明確標示 stale——舊的通過紀錄保留為歷史，但使用者一眼就知道它不適用於現在。這是這個階段最重要的一項防護：看起來像通過、其實驗的是舊版本，是最危險的一種錯誤。

Stale 只是呈現，不會自己改變任何狀態；新增一個 commit 不會突然把一堆工作打回去。使用者要重新取得適用的 Evidence，就對那件已經 REVIEW 的工作再跑一次 `verify`。

**Blocked by:** 02

**Status:** ready-for-agent

- [ ] `status` 顯示每件工作最新一筆 Evidence 的 result 與 commit SHA
- [ ] 最新 Evidence 的 SHA 不等於目前 HEAD 時，`status` 標示 stale
- [ ] Stale 不造成任何自動 transition
- [ ] 新增 commit 之後，先前的 PASS 保留為歷史但不被視為適用於新的 revision
- [ ] 已經 REVIEW 的 Work Item 可以再次 `verify`，且不要求它必須是 stale
- [ ] 對同一個 SHA 重複 `verify` 被允許，每次 append 一筆新的 Evidence
- [ ] 從未驗證過的工作在 `status` 顯示為尚未驗證，不得顯示為已通過或沒有問題
- [ ] `next` 與 `status` 在任何情況下都不寫入 state，包含遇到 stale 時
- [ ] `make verify` 通過
