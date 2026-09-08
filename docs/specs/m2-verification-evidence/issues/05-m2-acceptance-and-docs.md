# 05: M2 驗收與文件

**What to build:** 使用者讀 README 時看到的是 ForgePilot 現在真正能做的事——驗證與 Evidence 已經可用，而 DONE、Gate 與人工審查仍清楚標示為尚未實作。整個 M2 的驗收矩陣被實際執行過一遍，而不是靠先前的紀錄推論。

**Blocked by:** 03, 04

**Status:** done

- [x] README 反映 M2 實際能力，規劃中的功能仍清楚標示為未實作
- [x] development-plan 的 M2 驗收項目逐項實際確認後才勾選
- [x] architecture 與 CONTEXT 的敘述與實際行為一致
- [x] `go test -race ./...` 與 `make verify` 於本次實際執行並記錄環境與命令，不沿用先前的紀錄
- [x] 檢查整體 diff 無未授權的範圍擴張：沒有 DONE、沒有 Gate、沒有 `--json`、沒有 Evidence 查詢指令、沒有驗證逾時
- [x] 依交付格式回報實際結果，不把未執行的檢查標記為 PASS

## 驗收紀錄

這張票的 checkbox 在 M2 當時未被勾選，而 development-plan 的 M2 Exit checklist 已宣告完成——紀錄與宣告不一致。以下是 2026-09-08 於 M4 完成後補做的實際確認，以及兩項無法照原字面重跑的說明。

**環境**：macOS 26.5.1、arm64、Go 1.25.5 darwin/arm64、`/Users/carl/Dev/CMG/ForgePilot`、commit `9e7a16f`、工作樹乾淨。

**實跑命令與結果**：`go clean -testcache` 後 `go test -race ./...` 全數 ok（root 35.175s、repository 2.744s、storage 2.753s、work 2.349s）；`make verify` 全數 ok。兩者皆為本次執行，未沿用先前紀錄。

**M2 驗收矩陣的對應測試**（皆在上述執行中通過）：`TestVerifyRecordsEvidenceAgainstTheCommittedRevision`、`TestVerifyRunsOutsideTheMainWorktree`、`TestVerifyIsVisibleConcurrentAndRecoversFromInterruption`、`TestStatusReportsEvidenceAndStaleness`、`TestVerifyRefusesWhenTheRevisionHasNoCanonicalCheck`、`TestRefusedVerifyWritesOnlyTheRunThatEnded`、`TestInterruptedEvidenceHasNoExitCode`、`TestVerifyRecoversFromLeftoverWorktrees`、`TestMigrateCommandUpgradesLegacyState`、`TestMigrateUpgradesLegacyStateAndKeepsBackup`、`TestMigrateRefusesToDiscardWhatAStepWouldCreate`。

**範圍擴張檢查**：`--json` 在非測試程式碼中零出現；`internal/cli/` 只有 `cli.go`、`gate.go`、`review.go`、`verify.go`，沒有 Evidence 查詢指令；`internal/` 非測試程式碼中沒有任何驗證逾時。DONE 與 Gate 現在確實存在，但由 M3 授權——這一項當時要防的是 M2 提前實作，M3 之後已無法照原字面重跑，改以「M3 之外沒有其他擴張」確認。

**文件一致性**：README 現為 M4 版本，指令列表與 `internal/cli/cli.go` 的 dispatch 相符（`migrate`／`goal`／`work`／`next`／`start`／`verify`／`gate`／`review`／`status`），沒有殘留把已實作功能標示為未實作的敘述。「規劃中的功能仍標示為未實作」在 M2 當時指 DONE、Gate 與人工審查，這些已於 M3-07 與 M4 的驗收中逐項確認，該項語意由後續里程碑承接。
