# 目前的正式導入從固定 source version 本機建置

**Status relation:** [ADR-0032](0032-formal-macos-onboarding-is-apple-silicon-only.md)
把正式 macOS onboarding 支援面限定為 Apple Silicon，取代本 ADR 原本的 Intel／Apple Silicon
雙架構驗收前提；[ADR-0033](0033-source-built-bootstrap-separates-distribution-from-onboarding.md)
取代 source acquisition 與 target onboarding 必須同一 walkthrough 的部分。固定 source、信任與
授權邊界不變。

維護者不申請 Apple Developer Program，因此 ForgePilot 目前不能承諾 Developer ID、notarization
或 Gatekeeper 無警告啟動的預編譯 macOS binary。checksum、GitHub immutable Release 與 provenance
可以識別 source 或 asset bytes，卻不能代替 Apple 對執行檔的身分與 notarization 信任。把 unsigned
Mach-O 包成 installer，或教使用者移除 quarantine，會把繞過平台安全機制變成產品支援的一部分；
這不是可接受的導入承諾。

正式導入改為使用者明確授權的 **source-built** path。onboarding procedure 的第一階段只可用
inspection-only commands 檢查目標 Git repository、預計 Candidate，以及 `make verify` 是否存在；
不得執行 repository-defined target 或其他可能寫入的 command。它接著逐條展示預計 command、作用
路徑與效果：Go／package-manager 操作（如有）、以一個完整 immutable commit SHA 取得 source、
本機 build、`make verify`、entrypoint 原子切換，以及稍後的 repository writes。使用者確認後，
procedure 才以已安裝且符合版本的 Go 對該完整 commit SHA 執行 `go install`，在使用者目錄的
versioned staging location 驗證 CLI 可啟動並執行已展示的 verification，最後才原子切換使用者
擁有的 entrypoint。缺少 Go 時，agent 只能說明前提或提出安裝選項並等待新的授權；它不自行安裝、
下載或改變 shell profile。共通 procedure 與可選 Codex／Claude adapters 都只呼叫這一條 path；
Story 經人檢閱後，agent 必須再次展示 `init → goal create → work add → status` 的 repository
寫入及其效果，並取得第二次明確授權後才執行。

這保留「使用者貼一段 prompt 到目標專案的 AI agent」作為入口，卻不讓 prompt 靜默改動系統或
repository。ForgePilot 核心治理 CLI 繼續完全離線；下載和本機建置只屬於使用者明確啟動的分發程序。
未簽署雙架構 binary 如有必要，只能是 maintainer trial，清楚標示為非正式導入、不可作為一般使用者
文件或 agent 的預設動作。

ADR-0025 繼續定義**未來**若恢復支援 signed prebuilt macOS binary 時必須滿足的門檻；本 ADR
取代其「目前正式 onboarding 必須由 no-Go signed binary 開始」的前提。source identity／integrity
與 macOS execution trust 是兩項不同的主張，文件不得把前者誤寫成後者。

**Consequences:** 正式 onboarding 的前提是 Go，而不是 Apple signing identity。需在乾淨的原生
Apple Silicon Mac 驗收固定 source commit 的 source-built path，才可宣稱支援；planner 在
inspection-only 階段拒絕其他 host，不可用跨編譯或 unsigned asset 作替代。使用者可選的
source-based Homebrew formula 是未來便利層，不得發布 bottle 或宣稱 Homebrew 消除 Gatekeeper
friction，除非另有驗收與決定。

**Falsified if:** 一般使用者文件或 agent 把 unsigned prebuilt binary 當成正式安裝入口、指示移除
quarantine／繞過 Gatekeeper、宣稱沒有 Developer ID 仍有 notarization 或 signer trust；核心
`internal/cli`／`internal/app`／`internal/repository` 為安裝 ForgePilot 發出網路請求；或 source-built
procedure 在安裝、Go／package-manager 操作或 repository 寫入前不等待使用者的明確授權。任一項發生，
此導入與信任邊界必須重新決定。
