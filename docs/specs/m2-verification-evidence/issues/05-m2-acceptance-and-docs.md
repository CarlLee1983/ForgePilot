# 05: M2 驗收與文件

**What to build:** 使用者讀 README 時看到的是 ForgePilot 現在真正能做的事——驗證與 Evidence 已經可用，而 DONE、Gate 與人工審查仍清楚標示為尚未實作。整個 M2 的驗收矩陣被實際執行過一遍，而不是靠先前的紀錄推論。

**Blocked by:** 03, 04

**Status:** ready-for-agent

- [ ] README 反映 M2 實際能力，規劃中的功能仍清楚標示為未實作
- [ ] development-plan 的 M2 驗收項目逐項實際確認後才勾選
- [ ] architecture 與 CONTEXT 的敘述與實際行為一致
- [ ] `go test -race ./...` 與 `make verify` 於本次實際執行並記錄環境與命令，不沿用先前的紀錄
- [ ] 檢查整體 diff 無未授權的範圍擴張：沒有 DONE、沒有 Gate、沒有 `--json`、沒有 Evidence 查詢指令、沒有驗證逾時
- [ ] 依交付格式回報實際結果，不把未執行的檢查標記為 PASS
