# 02 — Codex adapter、fake subprocess、交接與結果驗證

## 交付

- `internal/agent`：`Runtime` 介面、`Request`／`Result`／`Outcome` 型別、結果 schema 驗證。
- Codex adapter：`codex exec` 非互動介面，executable 加 argument array，prompt 走 stdin，`--cd` 指定 workspace，`--sandbox workspace-write`，`--output-schema` 與 `--output-last-message` 取得結構化結果。
- Fake subprocess adapter：以本機 helper 程序模擬，完全不依賴網路。
- 交接內容組裝與大小上限。
- 程序群組啟動、ownership 記錄與終止。

## 驗收

- [x] 不使用 `sh -c` 拼接模型指令或 Story 內容。
- [x] 不使用 `--dangerously-bypass-approvals-and-sandbox`，不自動安裝或登入。
- [x] 每次 attempt 都是新 session，不使用 `resume --last`。
- [x] 結果缺欄位、型別錯誤或不是三個合法 outcome 之一時視為 protocol error。
- [x] exit code 0 但沒有合法結果視為 protocol error，不當成完成。
- [x] 交接超過上限時以檔案參照與有界摘錄取代，驗收條件不被靜默刪除。
- [x] 交接內容不含完整環境變數、token 或認證檔。
