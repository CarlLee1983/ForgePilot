# Story: FP-36 手動啟動的 Release Candidate workflow 契約

## Goal

維護者能以一個明確的完整 commit SHA 人工啟動一支 Release Candidate workflow，取得
darwin/arm64 與 darwin/amd64 兩份候選資產、對應 provenance 與 Actions candidate
artifact，同時確定這支 workflow 沒有能力自行把任何東西發佈出去——沒有 push-tag 觸發、
沒有建立 draft、沒有上傳 Release asset、也沒有建立或搬動 tag 的權限。

## Context

[ADR-0025](../../../docs/adr/0025-formal-macos-release-trust.md) 把正式 macOS Release
的信任門檻定在 Developer ID 簽署、與預先固定的預期簽署者相符、notarization，以及兩種
架構的原生驗收上；簽署與 notarization 必須在維護者持有的受保護 CI environment 執行，
agent 不得複製 ambient credentials。這份 Story 落地的是那個受保護簽署鏈前面的 workflow
契約本身——它定義輸入、job 邊界、fail-closed 行為與輸出範圍，而不是第一次真實簽署。

[FP-31](../FP-31-trial-assets/story.md) 已經接受一個可重跑的建置入口，從單一完整 commit
產生 darwin/arm64 與 darwin/amd64 兩份 unsigned trial 資產，並定義了資產命名、SHA-256
manifest 與 provenance 的形狀；其資產命名分成兩層——依主機架構挑選檔案的**架構推導
規則**，以及標示「這是 unsigned trial」的**通道標記**。這份 Story 只重用前者：候選
資產沿用相同的架構推導規則，但不得帶有 trial 通道標記，因為它們是走向正式簽署的
candidate，不是 trial 資產。這份 Story 的 workflow 在此之上，多加一層 job 邊界：候選
資產的簽署與 notarization 步驟只能在受保護 environment 裡執行，而觸發整支 workflow 的
只有人工 `workflow_dispatch`，不存在任何 push-tag 或其他自動路徑能讓它跑起來。

因為維護者尚未在此 repository 配置真實的 Apple Developer ID 憑證、notary API key 與受
保護 environment 的 reviewer 名單，這張票不能、也不需要以成功 signed 與 notarized 的
候選資產作為完成條件。它只固定這支 workflow 未來要讀取的公開變數與 secret 名稱、
reviewer 邊界，以及 fail-closed 的控制流，並用不需要真實 Apple credentials 的
credential-free fixtures 與靜態檢查驗證這條控制流。第一次真實簽署與 notarization 執行，
留給 [ADR-0025](../../../docs/adr/0025-formal-macos-release-trust.md) 所描述、且已配置
好受保護信任環境的後續票（GitHub issue #37）驗收。

對應的 tracker 票是 GitHub issue #36，parent spec 是 #28，前置條件是 #32（installer）。

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
* Reason: `credential-leak`
* Reason: `unauthorized-publish`

## Scope

### In Scope

* 一支只能以 GitHub Actions `workflow_dispatch` 人工啟動、且要求輸入一個完整
  40 字元 hex commit SHA 的 workflow 契約；不存在 push-tag 或其他事件觸發路徑。
* 從同一個輸入 commit SHA 建置 darwin/arm64 與 darwin/amd64 兩份候選資產，沿用
  FP-31 已驗收的架構推導命名規則、SHA-256 manifest 與 provenance 形狀，但不得
  帶有 FP-31 的 trial 通道標記。
* 一個把簽署與 notarization 步驟限制在受保護 GitHub environment 執行的 job 邊界，
  並固定該 environment 之後要配置的公開變數名稱、secret 名稱與 reviewer 邊界。
* Pull request 觸發的 job 不得取得受保護 environment 的變數或 secret 值。
* 對每個候選資產重新驗證 SHA-256、Mach-O 架構、Developer ID 簽章、預期簽署者
  （值由 #30 固定，此處只固定其變數名稱）與 notarization acceptance 的步驟契約。
* 缺少受保護輸入、簽署者不符、notarization 失敗，或任一架構建置失敗時的
  fail-closed 行為。
* Workflow 唯一輸出範圍：本次 run 專屬的 GitHub Actions candidate artifact
  （候選資產、manifest、provenance）；不建立 GitHub draft、不上傳 Release asset、
  不建立或搬動 Git tag，也沒有任何 publish 呼叫。
* Provenance 契約記錄 commit SHA、workflow run id/URL、簽署者身分、架構與每個
  資產的 digest，並明確禁止把私密認證資料寫進 log 或 artifact。
* 用不需要真實 Apple credentials 的 credential-free fixtures 與靜態檢查驗證上述
  控制流，並確保測試輸出不會被標記為正式 Candidate。

### Out of Scope

* darwin/arm64 與 darwin/amd64 unsigned trial 資產的建置入口本身——已由 #31 驗收，
  這份 Story 只是重用其架構推導命名規則與 manifest 契約，不重用其 trial 通道標記。
* Installer 的下載、驗證與安裝行為——屬於 #32。
* 預期簽署者身分（Team ID／Developer ID 明確簽署要求）的實際值與決策——屬於 #30；
  這份 Story 只固定消費該值所用的變數名稱。
* 受保護 CI release environment 的真實配置、Apple Developer ID 憑證與 notary
  API key 的實際落地，以及第一次真實 signed／notarized 執行——屬於 #37。
* 在原生 Apple Silicon 與 Intel 機器上的安裝與啟動驗收——屬於 #38。
* 建立 GitHub Release draft——屬於 #40。
* 正式 publish、tag 與資產的不可變發佈——屬於 #41。
* 修改 `internal/cli`、`internal/app`、`internal/repository` 或 `internal/work`
  的任何行為。
* 為 ForgePilot 引入 Go 標準函式庫以外的相依。

## Inputs

* `workflow_dispatch` 的必要輸入：一個明確指定的完整 40 字元 hex commit SHA。
* 該 commit 的 ForgePilot 原始碼樹，以及 FP-31 已驗收的建置入口。
* 受保護 environment 之後將配置的公開變數與 secret（此 Story 只消費固定名稱，
  不要求或假設任何真實值）。
* 目標架構清單：`darwin/arm64` 與 `darwin/amd64`。

## Outputs

* 一份 workflow 契約文件／定義，宣告觸發條件、job 邊界、輸入輸出與 fail-closed
  行為（實際 workflow 檔案的落地與其受保護簽署步驟的真實執行留給後續實作與 #37）。
* 兩份候選資產，附 SHA-256 manifest 與重新驗證後的架構、簽章、notarization 結果
  紀錄，僅以 GitHub Actions candidate artifact 形式輸出。
* 一份 provenance 輸出，記錄 commit SHA、workflow run id/URL、簽署者身分、架構
  與每個資產的 digest。
* Credential-free fixtures 與靜態檢查的驗證結果，明確標示為控制流驗證而非正式
  Candidate。

## Provisional Configuration Names

以下名稱只是本 Story 為了讓 workflow 契約可以固定引用點而暫定的名稱，**尚未與
#30、#37 確認**；#37 在配置受保護 environment 時可以沿用，也可以重新命名——
在那之前不得視為已定案的介面：

* 公開變數（暫定）：`APPLE_SIGNING_TEAM_ID`、`APPLE_SIGNING_IDENTITY`——
  預期簽署者身分本身的值由 #30 決定，此處只暫定其變數名稱。
* Secret 名稱（暫定）：`APPLE_DEVELOPER_ID_CERTIFICATE_P12`、
  `APPLE_NOTARIZATION_API_KEY`——只允許以名稱出現在 workflow 契約與 log
  中，其值不得出現在任何 log 或 artifact。
* Reviewer 邊界（暫定）：受保護 environment 要求至少一位在維護者 reviewer
  清單中的人工核准，該清單的實際成員由 #37 配置受保護 environment 時決定。

## Rules

* R1: Workflow 只能以 `workflow_dispatch` 人工啟動，且必須要求一個完整 40 字元
  hex commit SHA 輸入；不存在任何 push-tag 或其他自動觸發路徑。
* R2: darwin/arm64 與 darwin/amd64 兩份候選資產必須來自同一個輸入 commit SHA，
  並沿用 FP-31 已驗收的架構推導命名規則、manifest 與 provenance 形狀；候選資產
  的檔名不得帶有 FP-31 的 trial 通道標記。
* R3: 簽署與 notarization 步驟只能在受保護 GitHub environment 中執行；
  `pull_request` 事件觸發的 job 不得引用該 environment，也不得取得其變數或
  secret 值。
* R4: Workflow 契約必須固定受保護 environment 之後要配置的公開變數名稱、secret
  名稱與 reviewer 邊界（見 `## Provisional Configuration Names`），供 #37
  直接消費，本身不要求這些名稱已有真實值，且這些名稱在與 #30、#37 確認前
  一律視為暫定，可被取代。
* R5: 每個候選資產在被視為有效候選之前，必須重新驗證 SHA-256、Mach-O 架構、
  Developer ID 簽章、與 #30 固定的預期簽署者相符，以及 notarization acceptance。
* R6: 缺少受保護輸入、簽署者不符、notarization 失敗，或任一架構建置失敗時，
  整支 workflow run 失敗，不得留下部分完成、被當作有效候選的產出。
* R7: Workflow 唯一被允許的輸出是本次 run 專屬的 Actions candidate artifact；
  它沒有建立 GitHub draft、上傳 Release asset、建立或搬動 Git tag，或呼叫任何
  publish 端點的權限。
* R8: Provenance 必須同時記錄 commit SHA、workflow run id/URL、簽署者身分、
  架構與每個資產的 digest；缺任何一項即為失敗。
* R9: 任何 log 或 artifact 都不得包含私密認證資料（憑證檔案內容、密碼、
  notarization API key 或其他 secret 值）；只允許記錄 secret 的名稱與是否成功
  取得，不記錄其值。
* R10: 這張票的完成條件不得以真實 Apple credentials 或成功 signed／notarized
  候選資產為依據；控制流以 credential-free fixtures 與靜態檢查驗證，其輸出不得
  被標記或宣稱為正式 Candidate。

## Expected Errors

* 未以 `workflow_dispatch` 啟動，或未提供完整 40 字元 hex commit SHA 時，
  workflow 在啟動任何 job 之前失敗並說明缺少或格式錯誤的輸入。
* `pull_request` 觸發的 job 嘗試引用受保護 environment 或其變數／secret 時，
  失敗並且不得取得任何值。
* 候選資產的簽署者與 #30 固定的預期簽署者不符時，整支 run 失敗並指出實際讀到
  的簽署者，不得標記該資產為有效候選。
* Notarization 檢查失敗（未通過或未能完成 assessment）時，整支 run 失敗，
  不得產生標示為候選的 artifact。
* 任一目標架構建置或重新驗證失敗時，整支 run 失敗，不得只留下單一架構的
  candidate artifact。
* Provenance 缺少 commit、workflow run、簽署者、架構或 digest 任一欄位時，
  整支 run 失敗，不得留下宣稱完整但缺項的 provenance。

## Dependencies

* GitHub issue #31（darwin/arm64 與 darwin/amd64 unsigned trial 資產建置入口）
  已接受，其架構推導命名規則與 manifest 契約被本 Story 重用；其 trial 通道
  標記不被重用。
* GitHub issue #32（installer）作為此 workflow 的前置阻擋票。
* GitHub issue #30（預期簽署者身分）固定本 Story 引用的簽署者變數所指向的值。
* GitHub issue #37（受保護信任環境與第一次真實簽署／notarization 執行）驗收
  這份 workflow 契約的第一次真實運作。
* [ADR-0024](../../../docs/adr/0024-onboarding-stays-outside-the-offline-cli.md)
  與 [ADR-0025](../../../docs/adr/0025-formal-macos-release-trust.md)。

## Constraints

* ForgePilot 的 Go 程式碼只用標準函式庫且維持不變；這份 Story 不得為它加入新的
  Go 相依，也不得修改既有 Go 原始碼。
* Repository 的 canonical check 是 `make verify`，另跑
  `go test -race -count=1 ./...`。
* 簽署與 notarization 步驟只能宣告在受保護 GitHub environment 中執行；此 Story
  不得配置或假設任何真實 Apple Developer ID 憑證或 notary API key 已經存在。
* Workflow 契約不得包含任何自動 publish、自動建立 draft 或自動搬動 tag 的路徑。

## Guidance

Relevant:

* decision: `ADR-0025` — 正式 Release 的信任門檻，以及簽署／notarization 只能在
  受保護 CI environment 執行、agent 不持有憑證值
* decision: `ADR-0024` — 分發程序在離線治理 CLI 之外
* practice: FP-31 的架構推導命名規則、manifest 與 provenance 契約，作為本 Story
  重用的既有形狀（trial 通道標記不重用）

Not applicable:

* ForgePilot 的 Work Item 狀態機與 Evidence 規則：這份 Story 不觸及 `internal/work`。

## Trust Boundary Fields

* `workflow_dispatch.inputs.commit_sha` — 人工啟動時由維護者輸入的完整 commit SHA，
  進入 provenance 與資產命名。
* `candidate_provenance.signer_identity` — 從簽署驗證步驟衍生，寫入 provenance
  的簽署者欄位。
* `candidate_provenance.workflow_run_url` — GitHub Actions 產生的 run 識別，
  寫入 provenance。
* `candidate_provenance.asset_digest` — 從候選資產 bytes 重新計算的 SHA-256，
  寫入 provenance 與 manifest。
* `secrets.*`（受保護 environment 的 secret 值，例如簽署憑證與 notarization
  API key）——只允許以名稱出現在 workflow 契約與 log 中，其值不得出現在任何
  log 或 artifact。
* `job.event_name`（例如 `pull_request` 或 `workflow_dispatch`）——用來判斷該
  job 是否有資格引用受保護 environment 的邊界條件。
