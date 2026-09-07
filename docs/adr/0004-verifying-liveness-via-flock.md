# 以 flock 而非 pid 判斷 Verification Run 是否存活

Verification 可能執行數分鐘，不能全程持有 state lock，否則 `status` 會被擋死。因此驗證發生在鎖外，前後各一次受鎖交易，狀態會落地為 VERIFYING，程序崩潰便留下孤兒。判斷孤兒的存活性採用 M1 已有的機制：Verification Run 期間額外持有 `locks/verify-<work-id>` 的 flock，OS 在程序死亡時自動釋放，後續任何命令只要嘗試取得該鎖即可可靠判定跑者是否還在。

直覺做法是在 state 內記錄 pid，但 pid 會被作業系統重用，會把新的無關程序誤判為仍在執行的驗證。flock 沒有這個問題，並且順帶排除了同一件工作被並行驗證兩次。

發現孤兒時不推斷結果：append 一筆 result 為 INTERRUPTED 的 Evidence 並退回 RUNNING。INTERRUPTED 不是 FAIL——把「沒有結果」記成失敗與偽造 exit code 是同一種錯誤。回收由下一次 `verify` 的開頭交易執行，`status` 與 `next` 維持純讀，只呈現「跑者已消失」而不改寫 state。

**Falsified if:** `internal/storage/storage.go` 的 flock 機制不再是程序死亡即釋放——例如支援 flock 語意不同的平台或網路檔案系統。屆時存活性判斷必須連同 M1 的 state lock 一起重新設計。
