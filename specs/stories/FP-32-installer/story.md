# Story: FP-32 固定版本 installer 的信任查驗與版本化安裝

## Goal

沒有 Go 的 macOS 開發者能先檢閱一份固定版本的 shell installer，再讓它依自己的 Mac 架構取得
指定資產、完成來源與 macOS trust 驗證，並只在全部查驗通過後把目前入口原子切換到使用者自己
擁有的版本化安裝目錄；任何一個查驗失敗都不得切換入口，也不得留下無法辨識來源的 binary。

## Context

ForgePilot 目前只能以 `go install` 或原始碼建置取得。#31 已經產出一批未簽署試用資產、
SHA-256 manifest 與 provenance，作為這個 installer 的輸入來源；#30 之後會固定正式簽署者身分。
這個 installer 是 parent spec #28 描述的「消費者首次使用」路徑中負責下載與安裝的那一段：
它讀取已公布的資產與 checksum，驗證 Developer ID 簽章、預期簽署者與 notarization，然後把
binary 安裝到使用者自己的版本化目錄。

[ADR-0025](../../../docs/adr/0025-formal-macos-release-trust.md) 把正式導入的信任門檻定在
Developer ID 簽署、與預先固定的簽署者身分相符、以及 notarization 上，並要求 installer 在
digest、簽章、簽署者或查驗失敗時保留既有可用版本。這份 Story 的核心需求正是把這個信任門檻
做成可重跑、fail closed 的查驗與安裝流程，不是單純的下載腳本。

[ADR-0024](../../../docs/adr/0024-onboarding-stays-outside-the-offline-cli.md) 把分發程序放在
離線治理 CLI 之外，所以這個 installer 不屬於 `internal/cli`、`internal/app` 或
`internal/repository`，不使核心治理命令取得下載、打包或網路責任，也不因為安裝而變動
ForgePilot 的 state 或 Git repository。

對應的 tracker 票是 GitHub issue #32，parent spec 是 #28，前置票是 #30（預期簽署者身分）
與 #31（試用資產與 provenance）。

## Classification

* Security sensitive: yes
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

* Level: high
* Reason: `trust-verification`
* Reason: `entrypoint-switch`

## Scope

### In Scope

* 一個固定版本的 shell installer，可在執行前被人先讀完再決定是否執行。
* 使用 #31 定義的架構推導規則（僅依 Mac 架構決定下載目標的部分，與 #36 的正式簽署候選版本
  共用同一規則）推導要下載的資產，並拒絕非 macOS 15+ 或非 arm64／amd64 的平台。
* 核對下載資產的通道標記（試用版／正式版）與 installer 實際請求的通道相符，不符時 fail
  closed，不進行後續查驗或安裝。
* 下載固定版本資產後，依序核對公布的 SHA-256、Developer ID 簽章、預期簽署者要求（Team ID／
  明確簽署身分）與 notarization assessment。
* digest 不符、簽章無效、簽署者不符或未設定、notarization 被拒絕或下載中斷時全部 fail
  closed：不安裝、不切換入口、保留既有可用版本。
* 安裝到使用者擁有的版本化目錄（例如依版本分目錄），目前入口只在所有查驗與安裝步驟完成後
  才原子切換。
* 相同版本重跑安全，且產生一致的安裝結果。
* PATH 尚未包含安裝目錄時，回報可直接使用的絕對 binary 路徑與人工 PATH 設定指引。
* 使用者路徑（下載、核對、安裝）全程不依賴 `gh`；使用者可只用 macOS 內建工具核對已公布的
  SHA-256。
* 以最外層行為（實際執行 installer 觀察安裝結果）測試成功、每一種查驗失敗與重跑。

### Out of Scope

* 固定正式簽署者身分本身（Team ID／明確簽署要求的決定與配置）——屬於 #30。
* 未簽署試用資產、SHA-256 manifest 與 provenance 的建置——屬於 #31；這個 installer 只消費
  #31 產出的資產、checksum 與命名格式，不重新定義它們。
* 短 prompt 導入文件、agent 引導 Story 檢閱與 `init → goal create → work add → status`
  的逐步執行——屬於 #33。
* GitHub Release 的 Candidate workflow、簽署與 notarization 的 CI 執行——屬於 #36。
* Apple Developer ID／notary 憑證所在的受保護 CI release environment 本身的配置，以及對
  真正簽署且已送件 notarize 資產的端到端驗證——屬於 #37。
* 在原生 Apple Silicon 與 Intel 機器上的新安裝、首次啟動與 `init → goal create → work add →
  status` 原生驗收——屬於 #38。
* 建立 GitHub draft、上傳資產與正式 publish——屬於 #40 與 #41。
* npm／npx wrapper、Homebrew formula、Linux、Windows 或 macOS 12–14 的支援宣稱。
* 修改 `internal/cli`、`internal/app`、`internal/repository` 或 `internal/work` 的任何行為。
* 為 ForgePilot 引入 Go 標準函式庫以外的相依。
* 自動更新既有安裝、遷移 `.forgepilot/` state，或修改使用者的 shell profile。

## Inputs

* 目標版本的固定資產下載位置與其公布的 SHA-256（由 #31 的輸出提供格式，含架構推導規則與
  通道標記兩層命名）。
* 該版本預期簽署者的 Team ID／明確簽署身分（由 #30 提供的固定值，或該值尚未設定的狀態）。
* 執行時的 Mac 架構與 macOS 版本。
* 使用者現有的安裝目錄狀態（若已安裝過先前版本）。

## Outputs

* 一個安裝到使用者擁有的版本化目錄中的 ForgePilot binary。
* 查驗結果紀錄：digest、簽章、簽署者與 notarization 各自的通過或失敗原因。
* 目前入口的原子切換結果，或在任一查驗失敗時維持既有入口不變的紀錄。
* PATH 尚未設定時的絕對路徑與設定指引輸出。

## Rules

* R1: Installer 只接受 macOS 15+ 的 arm64 或 amd64；其他平台或架構在下載前即拒絕並說明原因。
* R2: 下載到的資產通道標記（試用版／正式版）必須與 installer 實際請求的通道相符；不符即
  fail closed，不進行 digest 以下的查驗。
* R3: 下載的資產必須先通過公布的 SHA-256 核對，核對前不得執行或安裝該資產。
* R4: 通過 digest 核對後才驗證 Developer ID 簽章；簽章無效即 fail closed。
* R5: 簽章驗證必須核對預期簽署者身分（Team ID／明確簽署要求），而非接受任意有效的
  Developer ID；簽署者不符即 fail closed。
* R6: 預期簽署者身分尚未設定時（例如 #30 尚未提供 Team ID／簽署要求），installer 在驗證
  簽章前即 fail closed，並說明必須先完成 #30，不得以預設或空白身分繼續。
* R7: 通過簽章與簽署者核對後才驗證 notarization assessment；查驗被拒絕即 fail closed。
* R8: digest、通道標記、簽章、簽署者或 notarization 任一項失敗時，既有可用版本必須維持
  可用，且不得切換目前入口。
* R9: 目前入口只在資產下載、全部查驗與安裝寫入都完成後才原子切換；不存在切換到一半的中間
  狀態。
* R10: 安裝目錄必須由使用者擁有，安裝與切換過程不得使用 `sudo`、不得使用 curl pipe shell、
  不得修改 shell profile。
* R11: 相同版本重跑必須安全完成，且不因重跑而破壞既有可用入口或產生不一致的安裝結果。
* R12: Installer 不得寫入受管理 repository、不得寫入 `.forgepilot/` state、不得執行
  `migrate`、不得讀取或處理 agent 登入憑證。
* R13: PATH 未涵蓋安裝目錄時，installer 必須輸出可直接使用的絕對 binary 路徑與人工 PATH
  設定指引，而不是安靜地失敗或假設 PATH 已設定。
* R14: 使用者路徑（下載、核對、安裝、PATH 指引）全程不得呼叫 `gh`；使用者必須能只用 macOS
  內建工具（例如 `shasum`）核對已公布的 SHA-256。

## Expected Errors

* 平台或架構不受支援時，installer 在下載任何資產之前失敗並說明不支援的平台或架構。
* 下載到的資產通道標記與實際請求的通道不符時失敗並說明不符的通道，不繼續 digest 核對。
* 下載中斷或資產不完整時失敗，不進入查驗或安裝步驟，且不切換既有入口。
* SHA-256 與公布值不符時失敗並回報不符，不繼續驗證簽章。
* Developer ID 簽章無效時失敗並回報簽章驗證失敗，既有入口不變。
* 簽署者與預期身分不符時失敗並回報不符的簽署者，既有入口不變。
* 預期簽署者身分尚未設定時失敗並說明必須先完成 #30，不驗證簽章，既有入口不變。
* Notarization assessment 被拒絕時失敗並回報查驗結果，既有入口不變。
* 安裝目錄寫入失敗（例如權限不足）時失敗，不進行入口切換，且不留下部分寫入的版本化目錄。

## Dependencies

* GitHub issue #31（未簽署試用資產、SHA-256 manifest 與 provenance）提供本 Story 消費的
  資產與 checksum 格式；該格式的命名分兩層——由 Mac 架構決定檔名的架構推導規則（與 #36 的
  正式簽署候選版本共用不變）與只有試用資產攜帶的通道標記——這個 installer 只消費架構推導
  規則決定下載目標，並核對通道標記與請求的通道相符。正式簽署資產由 #30／#36／#37 之後接上
  同一介面。
* GitHub issue #30（預期簽署者身分固定）必須先有明確的 Team ID／簽署要求可供這個 installer
  查驗；在該身分尚未固定前，這個 Story 以可設定的預期簽署者值開發與測試，不寫死佔位身分，
  且把「尚未設定」視為與「不符」不同的 fail closed 錯誤。
* GitHub issue #37（Apple Developer ID／notary 憑證所在的受保護 CI release environment）：
  唯有搭配 #37 產出的真正簽署且已送件 notarize 的資產，才能端到端驗證 notarization
  assessment 真正回傳 accepted；真正的 accepted 判定不是這張票的完成條件，這張票驗證的是
  digest → 簽章 → 預期簽署者 → notarization 的查驗順序與 fail closed 控制流程本身。
* [ADR-0024](../../../docs/adr/0024-onboarding-stays-outside-the-offline-cli.md) 與
  [ADR-0025](../../../docs/adr/0025-formal-macos-release-trust.md)。

## Constraints

* ForgePilot 的 Go 程式碼只用標準函式庫；這個 installer 不得為它加入新的 Go 相依。
* Installer 位於核心治理 CLI 之外，不得使 `internal/cli`、`internal/app` 或
  `internal/repository` 取得下載、打包或網路責任。
* 不使用 `sudo`，不使用 curl pipe shell，不修改 shell profile、受管理 repository 或
  `.forgepilot/` state，不執行 `migrate`，不讀取或處理 agent 憑證。
* Repository 的 canonical check 是 `make verify`，另跑 `go test -race -count=1 ./...`。

## Guidance

Relevant:

* decision: `ADR-0024` — 分發程序在離線治理 CLI 之外
* decision: `ADR-0025` — 正式 Release 的信任門檻，installer 查驗失敗需保留既有可用版本
* practice: Apple code-signing requirements（TN3127）——定義與查驗預期簽署者的依據

Not applicable:

* ForgePilot 的 Work Item 狀態機與 Evidence 規則：這份 Story 不觸及 `internal/work`。

## Trust Boundary Fields

* `commit_or_asset_location` — 使用者或呼叫端在執行 installer 時提供的版本／位置輸入
* `asset.channel` — 由下載資產命名中的通道標記讀出的來源通道（試用版／正式版），未核對前
  視為不可信，須與 installer 實際請求的通道相符
* `expected_signer` — 由 #30 固定的 Team ID／明確簽署身分，installer 於執行時讀入的查驗
  依據；未設定時視為需要 fail closed 的狀態，不得以預設值替代
* `asset.sha256` — 下載回應中資產 bytes 對應宣稱的 SHA-256，未經核對前視為不可信
* `developer_id.signer` — 由系統簽章查驗（`codesign`）讀出的簽署者識別資訊
* `notarization.assessment` — 由系統查驗（`spctl --assess`）讀出的外部判定結果
* `entrypoint.state` — 執行時讀取的既有安裝目錄與目前入口路徑狀態，用於決定是否安全切換
