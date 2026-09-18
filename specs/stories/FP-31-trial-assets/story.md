# Story: FP-31 可檢閱的雙架構試用資產

## Goal

維護者能從一個明確的完整 commit，以單一可重跑的入口產生 darwin/arm64 與 darwin/amd64 的
ForgePilot 試用資產、每個資產的 SHA-256 與 provenance，並且任何讀到這批輸出的人都能立刻看出
它們是 unsigned trial，不是正式免 Go 發佈。

## Context

ForgePilot 目前只能以 `go install` 或原始碼建置取得，最新的 GitHub Release 沒有任何 binary 資產。
後續的 installer（#32）與 Release Candidate workflow（#36）都需要一批命名穩定、digest 可核對、
來源可追溯的資產作為輸入；在簽署與 notarization 的前置資源備妥之前，這批資產只能是試用性質。

[ADR-0025](../../../docs/adr/0025-formal-macos-release-trust.md) 把正式導入的信任門檻定在
Developer ID 簽署、預期簽署者、notarization 與原生驗收上，並明確要求未簽署試用流程不得
被稱為正式導入。[ADR-0032](../../../docs/adr/0032-formal-macos-onboarding-is-apple-silicon-only.md)
後來把正式支援面限定為 Apple Silicon；本 Story 的雙架構輸出仍只是 maintainer trial，不使 Intel
成為受支援平台。這份 Story 產出的正是「未簽署試用」層級的資產，因此標示責任是它的核心需求，
不是附帶說明。

[ADR-0024](../../../docs/adr/0024-onboarding-stays-outside-the-offline-cli.md) 把分發程序放在
離線治理 CLI 之外，所以這個建置入口不屬於 `internal/cli`、`internal/app` 或 `internal/repository`，
也不讓核心命令取得下載或打包責任。

對應的 tracker 票是 GitHub issue #31，parent spec 是 #28。

## Classification

* Security sensitive: no
* Baseline conformance: no
* Task mode: execution

## Authority

* plan: yes
* modify: yes
* add_dependency: no
* migration: no
* commit: no
* push: no
* deploy: no

## Risk

* Level: medium
* Reason: `unsigned-artifact-mislabel`

## Scope

### In Scope

* 一個可重跑的建置入口，從單一完整 commit 產生 darwin/arm64 與 darwin/amd64 兩份 ForgePilot binary 資產。
* 穩定的資產命名規則，拆成可被 installer 依架構單獨推導的「架構推導規則」，以及與之分離的「通道標記」。
* 每個資產的 SHA-256，以及包含 commit SHA、版本與目標架構的 provenance 輸出。
* 對每個產出資產自動檢查 Mach-O 架構符合宣告的目標架構。
* 對每個產出資產取得基本的 CLI 啟動證據。
* 在資產名稱的通道標記、provenance 輸出與對應文件三處標示 unsigned trial；架構推導規則本身不編碼通道。

### Out of Scope

* Developer ID 簽署、notarization、預期簽署者驗證——屬於 #36 與 #37。
* Installer 的下載、驗證與安裝行為——屬於 #32。
* 建立 GitHub Release、draft、上傳資產或 publish——屬於 #40 與 #41。
* 正式支援平台的原生安裝驗收——屬於 #38；ADR-0032 後只包含 Apple Silicon。
* 修改 `internal/cli`、`internal/app`、`internal/repository` 或 `internal/work` 的任何行為。
* 為 ForgePilot 引入 Go 標準函式庫以外的相依。
* #36 的正式簽署 Release Candidate 重用本 Story 的架構推導規則，但不重用、也不繼承 unsigned trial 通道標記——通道標記的取捨屬於 #36 的範圍。

## Inputs

* 一個明確指定的完整 commit SHA。
* 該 commit 的 ForgePilot 原始碼樹。
* 目標架構清單：`darwin/arm64` 與 `darwin/amd64`。

## Outputs

* 兩個 macOS binary 資產，名稱由「架構推導規則」與「unsigned trial 通道標記」兩個獨立成分組成。
* 一份 SHA-256 manifest，涵蓋每一個產出資產。
* 一份 provenance 輸出，含 commit SHA、版本與每個資產的目標架構。
* 架構檢查與 CLI 啟動檢查的結果。

## Rules

* R1: 兩個資產必須由同一個輸入 commit SHA 建置；入口不得在未指定 commit 時以目前工作樹充數。
* R2: 資產命名分為兩層，且兩層不得互相編碼對方：(a) 架構推導規則——installer 得以由 Mac 架構單獨推導出要下載的資產，此規則與正式簽署 Release Candidate（#36）共用並原樣重用；(b) 通道標記——與架構推導規則分離的獨立命名成分，標示發佈通道。試用資產的通道標記必須是 unsigned trial；正式簽署候選不得帶有此通道標記。
* R3: SHA-256 manifest 必須涵蓋每一個產出資產，且與實際 bytes 相符。
* R4: provenance 必須同時記錄 commit SHA、版本與目標架構；缺任何一項即為失敗。
* R5: 每個資產的實際 Mach-O 架構必須與其宣告的目標架構相符，不符即為失敗。
* R6: 每個資產必須提供基本 CLI 啟動證據；只有建置成功不足以通過。
* R7: 相同輸入 commit 重跑入口必須安全，且產生一致的資產命名與 provenance。
* R8: 輸出與文件不得宣稱 Developer ID 簽章、notarization、原生驗收或正式支援。
* R9: 此入口不得寫入 `.forgepilot/` state、不得執行 `migrate`、不得建立 Git tag 或 commit。

## Expected Errors

* 未提供完整 commit SHA 時，入口在建置任何資產之前失敗並說明缺少的輸入。
* 任一目標架構建置失敗時，整體入口失敗，不得只輸出單一架構的資產與 manifest。
* 產出資產的 Mach-O 架構與宣告不符時失敗，並指出實際讀到的架構。
* CLI 啟動檢查失敗時失敗，且不將該資產列入 manifest 為可用。
* 產生 SHA-256 或 provenance 失敗時，不得留下一份宣稱完整但缺項的 manifest。

## Dependencies

* GitHub issue #29（已接受的分發與導入 ADR 進入版本庫）必須先落地。
* [ADR-0024](../../../docs/adr/0024-onboarding-stays-outside-the-offline-cli.md) 與
  [ADR-0025](../../../docs/adr/0025-formal-macos-release-trust.md)。

## Constraints

* ForgePilot 的 Go 程式碼只用標準函式庫；這個入口不得為它加入新的 Go 相依。
* 建置入口位於核心治理 CLI 之外，不得使 `internal/cli`、`internal/app` 或 `internal/repository` 取得下載、打包或網路責任。
* 此入口只產生 arm64 與 amd64 unsigned trial；只有 arm64 屬於 ADR-0032 的預定正式支援面，
  amd64 資產不得被用來宣稱 Intel 支援，也不宣稱任何 deployment target 相容性。
* Repository 的 canonical check 是 `make verify`，另跑 `go test -race -count=1 ./...`。

## Guidance

Relevant:

* decision: `ADR-0024` — 分發程序在離線治理 CLI 之外
* decision: `ADR-0025` — 正式 Release 的信任門檻與「未簽署試用不得稱為正式導入」
* practice: `AGENTS.md` 的分層規則——`internal/repository` 是唯一可以碰 Git 的地方

Not applicable:

* ForgePilot 的 Work Item 狀態機與 Evidence 規則：這份 Story 不觸及 `internal/work`。
