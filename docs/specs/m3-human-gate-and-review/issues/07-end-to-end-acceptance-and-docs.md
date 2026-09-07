# 07: 端到端驗收與文件

**What to build:** 完成 M1 分段驗收時明確延後至此的那條流程——在真實的 Git repository 中，以獨立的 CLI process 依序跑完 `A start → verify → approve → DONE → B READY`。M1 當時只能用測試內的 fixture 證明「A 已為 DONE 時 B 可 READY」，因為產品裡沒有合法到達 DONE 的路徑；這張票補上真的那一條。

同時讓 README 反映 ForgePilot 現在真正能做的事：MVP 的 M1–M3 已完整，而 PR review target 仍未實作。

**Blocked by:** 05, 06

**Status:** done

- [x] 以獨立 process 完成真實的 A start → verify → approve → DONE → B READY 流程
- [x] 同一流程中驗證 Gate 會阻擋推進、解除後恢復
- [x] README 反映 M1–M3 的實際能力，PR review target 仍清楚標示為未實作
- [x] development-plan 的 M3 驗收項目逐項實際確認後才勾選
- [x] architecture 與 CONTEXT 的敘述與實際行為一致，特別是狀態集合與 Gate 不佔用狀態欄
- [x] `go test -race ./...` 與 `make verify` 於本次實際執行並記錄環境與命令，不沿用先前的紀錄
- [x] 檢查整體 diff 無未授權的範圍擴張：沒有完成指令、沒有 reopen、沒有 `--json`、沒有 Gate 查詢指令、沒有身分認證、沒有 PR 整合
- [x] 依交付格式回報實際結果，不把未執行的檢查標記為 PASS
