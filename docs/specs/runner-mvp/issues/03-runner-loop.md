# 03 — Runner loop、snapshot verification 串接與停止判定

## 交付

- `internal/runner`：執行迴圈、action 對應、scope fingerprint、停止原因型別。
- `internal/cli/run.go`：`run`、`run --dry-run`、`run status`、`run resume`，以及 0／1／2／3 退出碼。

## 驗收

- [x] A → B → C 相依工作循序完成，每次 attempt 新 session，最後停在等待總檢而非 DONE。
- [x] 缺少 `--snapshot` 時拒絕，不自動 commit，也不改用 HEAD verification。
- [x] `--dry-run` 不呼叫模型、不 start／reconcile／verify、不新增 run record、不建立 snapshot。
- [x] `NONE` 不被當成完成；WAIT_* 停止且不解除阻擋。
- [x] 外部改動 Goal 的 Work Item 集合、StoryRef 或依賴時停止並回報 scope changed。
- [x] 達成等待總檢時保存引用的 Evidence IDs，退出碼 0，state 中沒有任何 DONE。
