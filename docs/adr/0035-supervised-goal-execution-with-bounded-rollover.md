---
status: accepted
---

# 完整計畫下的有界背景執行

長任務需要在執行前核對上游完整計畫，並在主 Agent Session 關閉、單次 run 到期或需要
人工交接後，仍能在同一份可追溯授權下安全接續。採用 PraxisBound 擁有的計畫與覆蓋核准、
ForgePilot 擁有的有界執行授權／程序管理，以及 CLI、主 Session、唯讀 TUI 共用的進度投影。
Work Item lifecycle 與合法動作仍只有 domain 一份權威。

**文件狀態：** 2026-09-19 的 `grill-with-docs` 四輪 Q1–Q24 均已獲使用者「照建議」確認，
完整共識摘要也已獲使用者明確確認。決策樹已無待選分支，本 ADR 為 accepted。
以下是已定案、尚未實作的設計契約，不是現有功能或驗收通過的主張。
尚未進入 spec、tickets 或程式碼實作，也未修改 PraxisBound。

## 上游完整計畫與覆蓋核准

PraxisBound 提供 Goal Plan Manifest 與 Plan Coverage Review。沿用既有的 requirement →
Story → acceptance 覆蓋索引，另取得明確上游 Human approval，綁定受審來源與 manifest digest；
來源改變即須重審。身分沿用自述方式，不新增認證。ForgePilot 只驗結構、綁定、核准結論
有效性及登錄一致性，不解析 Story Markdown 或重新審查需求語意。
集合相等只能證明沒有漏登錄上游宣告的節點，不能證明原始需求沒有漏拆。
覆蓋核准、Execution Authorization 與 Human final acceptance 是三個不同的決定。

Plan Node Reference 在同一 Goal 內與一張 Work Item 一對一；多個節點可以引用同一 Story，
但 inputs／outputs、future identities 等 readiness 衝突仍按既有規則檢查。
`external_ref` 保持冪等鍵語意，不改作 Story 或節點 identity。
manifest 與 Goal 的完整 Work Item 集合及依賴必須相符，不能只核對準備執行的子圖。

同 Goal 的計畫修訂保留所有既有節點、Story reference 與依賴，可以新增節點或經重審更新
契約內容；先停止、展示差異並明確修訂授權，保留全部歷史與累計消耗。
同 Goal 修訂後，既有 Evidence 是否仍可使用由既有 freshness 規則判定。
刪除、替換節點或改依賴時，首版改建新 Goal；新 Goal 不直接繼承舊 Evidence，DONE 不重開。
條件式 follow-up 因此必須在決策後以已核准的新計畫版本新增；若需要改寫既有拓撲，
適用新 Goal 的邊界，不能在 Runner 私下忽略某些 Work Item。
freeze 的是計畫／Story 契約與節點／依賴，不是正常演進的 Candidate。

既有 Goal 導入 manifest 時，必須提供完整且明確的節點對應，核對全部 Story／依賴事實
成功後一次保存；不能由 Story path 猜測對應。這項映射不改寫工作狀態或既有 Evidence。

## Execution Authorization 與總帳

每個 Goal 固定一條 Execution Authorization 歷史。各版本綁定 workspace、Goal、計畫版本、
Worker Profile、ForgePilot 引擎版本、總額度與固定到期時間；修訂只新增版本，累計消耗
跨 run、主 Session 與授權修訂保留。追加額度、延長期限、換計畫／profile／引擎版本
必須明列差異並取得使用者授權，不能建立平行總帳把消耗歸零。
授權不授予 Human Decision、Human acceptance、merge／release 或額外工程範圍。

每次授權顯式提供有限的總 steps、每個節點累計 technical attempts、總 run 數、
crash recovery 次數與到期時間，並保留 artifact 容量限制；沒有隱含的跨 run 產品預設。
主 Session 可提出數值建議，但數值必須展示在授權內容。等待人、睡眠、登出不延長期限。
既有單 run 預設仍是單次執行限制；預算估計只作提示，不保證整個 DAG 能完成。

啟動工作前先記入消耗。crash 導致無法確認是否執行時，不自動退還；已確認的合法
`needs_human` 仍依既有規則不計 technical attempt。授權紀錄缺失、損毀或不一致即停止，
不能從 Run Record／logs 猜回消耗。授權與總帳的持久化位置、原子更新與預先扣帳的
crash 一致性由 spec 具體化，但不能把 run history 變成另一份額度權威。

## 自動 rollover 與明確續接

| 入口／情境 | 契約 |
|---|---|
| 自動 rollover | 僅限單 run 的 MaxSteps／MaxDuration 耗盡（停止原因 MAX_STEPS／MAX_DURATION）；清理已確認、授權仍有效、總額度足夠且計畫／profile 等綁定一致才可建立下一個 run |
| 授權層級的明確續接 | 解除等待並通過檢查後，能合法續用舊 run 就續用；舊 run 到期，或已明確追加額度／修訂計畫或 profile，需要時建立新 run |
| `run resume <run-id>` | 對已綁定 Execution Authorization 的 run 保持 exact-run 語意，不延長該 run 期限，不改寫其預算；不符合原 run 契約時拒絕並引導至授權層級入口。cutover 前未綁定授權的 legacy run 適用下節的切換規則 |
| Verification FAIL | 仍走既有有限 repair 流程，不藉由新 run 繞過累計限制 |

attempts 耗盡、agent／verification 的單次執行 timeout（AgentTimeout／VerifyTimeout，
停止原因 AGENT_TIMEOUT／VERIFY_TIMEOUT）、無進展、容量不足、人工等待、契約／profile 改變、
授權到期及 recovery blocked 都不是自動 rollover 的理由。明確的人工接續與自動接續
不可混同：例如先前因 `needs_human` 暫停、如今舊 run 已到期，使用者解決問題後的
授權層 resume 可以在原授權仍有效時開新 run；追加 attempts 後的明確續接也是同理。
它們不修改舊 run 的期限、計數與歷史停止原因，也不增加自動 rollover 的例外。
未解除的恢復阻擋始終禁止新工作；總授權到期不因換 run 而延長。

## 既有 run 的切換與所有入口

新版本所有 Runner 啟動／續跑入口，包括直接 `run`、exact-run resume、授權層續接
與 supervisor restart，都核對同一份 Goal 授權，不能用直接開新 run 繞過總額度。
手動 `work`、`verify`、`review`、Gate／Goal 治理維持既有契約，不全面套用此執行授權。

此處的 legacy run 專指 cutover 前建立、尚未綁定 Execution Authorization 的 run，
不包含切換後已綁定授權、可按原契約 resume 的 run。legacy run 沒有可靠的跨 run 總帳。
先完成必要程序清理與完整 manifest 對應，再由人明確
核准切換；新額度從切換點起算，歷史消耗明確標示未納入新總帳，不推測重建。
同一 Goal 的 Work Item、Evidence 與 run 歷史保留；cutover 前的 legacy run 僅作歷史，不再直接 resume。
後續從目前 domain state 取得下一個合法動作，不能因為切換而重新實作已確認的工作。

## Worker Profile 與固定引擎版本

Main Agent Session 是使用者自行選擇的外部調派入口；ForgePilot 不自行啟動另一個調派模型。
每個授權版本固定一份 implementation／repair 共用的 Worker Profile；首版只有既有
Codex worker，model、effort、權限與 executable 選擇均須顯式提供，綁定解析後的路徑／版本。
resume 或新 run 發現不一致即停止並要求修訂；不沿用聊天模型，不默默換用 runtime 預設或
其他 provider。憑證由 runtime 管理，不複製進授權紀錄。Claude／Gemini 等 worker 另列需求。

Worker 版本與 ForgePilot 引擎版本是不同的綁定。有效授權固定使用不可變的 ForgePilot
版本，supervisor 重啟不能跟隨分發的 `current` 指標悄悄換引擎。
切換引擎須先暫停、確認清理與資料相容性，再明確修訂授權。
安裝升級不自動更新既有背景工作；其引用的舊版本必須保留。
分發清理程序只讀使用者目錄內的版本保留標記，不建立目標 repository 清單，也不掃描
目標 repository。具體保留標記與安裝／升級交易在 spec 中定義。

## 背景執行、停止與恢復

| 事件 | 行為 |
|---|---|
| 關閉 Main Agent Session、終端或 TUI | 執行繼續；退出觀察介面不代表停止 |
| 程序意外 crash | 在明確的有限次數內，先確認 recovery 再接續 |
| 登出／重開機 | 保存進度，僅在下次登入且原授權仍有效時接續；不要求登出期間或登入前執行 |
| 睡眠／喚醒 | 不保證睡眠期間執行，喚醒後重查期限 |
| 使用者停止 | 先持久化暫停意圖，再取消當前工作並確認程序清理；保留 Evidence、工作修改及日誌 |
| 人工／外部等待、計畫變更、總額度耗盡或 recovery blocked | 保持停止；程序重啟不解除暫停或阻擋 |

使用者停止後必須明確 resume。CLI 擁有控制操作，首版 TUI 唯讀。
supervisor 保存的是執行控制意圖與程序 ownership，不是 Goal／Work Item 的新 lifecycle。
既有 workspace ownership、pending cleanup 與 fail-closed 程序識別持續適用。

授權帳本遺失時，只接受與 Goal 最後已知授權及扣帳版本相符的完整備份，不能使用較早
備份降低已知消耗。沒有相符備份就停止該 Goal 的 Runner、保留歷史；繼續工程工作需
明確建立新 Goal、Work Items 與授權，不能直接繼承舊 Evidence。
首版不提供清空總帳、重設授權或強制繞過。

## 唯讀 inspection、進度與接手

新增 Goal 層 preflight JSON 入口。CLI、Main Agent Session、首版唯讀 TUI 共用 app 的
判定來源，既有 dry-run 也改用同一來源，不讓各介面自行計算合法動作或保存第二份進度。
顯示完整 DAG、依賴、目前位置、當前 action／attempt、已知 worker 資訊、Gate、預算與
停止原因，以及 workspace、Story、handoff、session log、verification log 的完整路徑。
machine PASS、fresh PASS 與 Human acceptance 必須分開呈現。

Inspection 不寫入、不啟動 subprocess，只檢查計畫、核准、登錄、授權與可直接讀取的事實。
未實測的能力標為 `unprobed`；需要外部查詢才能重新確認的 Candidate freshness、程序存活、
runtime 與權威 next action，只能顯示未知或註明最後觀察時間／來源，不冒充即時結論。
正式啟動取得 ownership、完成必要 recovery、核對授權，再以受管理程序執行 probes 與工作。
TUI 刷新不觸發 runtime probe、Git 程序、canonical check 或 recovery。

新 Main Agent Session 透過持久化 handoff 與公開介面接手，不依賴原聊天脈絡保存進度。
完整路線是已核准計畫與目前可觀察事實的投影，不是不可變的未來 action 清單。
欄位 schema、版本、命令命名、讀取一致性、路徑遺失與 UI 呈現由 spec 具體化。

## 人工與外部交接

已知 human／external 交接點顯示在路線上，不阻止可合法執行的工作開始。
真正遇到 `needs_human` 時停止 Runner；Gate 仍按既有條件成立，不能為單純等資料硬造選項。
外部資料等待保存為執行暫停原因，不新增 Work Item 狀態；提供資料後明確 resume 並重查條件。

本機檔案／digest 條件由 ForgePilot 重查。無法離線查證的條件使用
External Fulfillment Declaration：具名自述確認綁定計畫與授權版本、節點、條件及等待項目，
供明確 resume 使用。它不是 Verification Evidence、Gate resolution、Human final acceptance
或已查證的外部事實；計畫／授權版本改變不自動沿用舊確認。

## Verification 與完成邊界

每個 action 前重新檢查計畫與授權，包含 session 結束到 snapshot／verification 的邊界。
已合法開始的 verification 仍按既有防護保存 immutable Candidate 的真實結果，不擴張
既有 verdict guard 來丟棄誠實結果。結束後 scope 改變即停止，禁止繼續或 rollover；
Evidence 保留，但不代表新版計畫已完成。

Runner 的終點仍是等待 Goal final review。VERIFIED／machine PASS 不是 DONE 或
Human acceptance；Goal approve／reject／completion、Goal Evidence 不在這次交付範圍。
既有 DONE 終態與 domain typed next action 權威不變。

## 與既有 ADR 的關係

| ADR | 保留或擴充的邊界 |
|---|---|
| 0005、0013、0029 | 保留自述身分、上游 Story 語意所有權與 raw-byte digest 核對；擴充 Goal manifest、覆蓋核准與外部條件契約，不由 Markdown 推論 |
| 0016、0019 | 保留 domain lifecycle／合法動作與 Human final review；新增授權／執行控制，不保存另一份 Work Item 進度；dry-run 改採純 inspection 的能力界線 |
| 0020、0021、0022 | 保留程序 ownership、pending cleanup 與原 run 限制；增加跨 run 總額度及獨立的明確授權續接入口，不延長 exact-run resume |
| 0031 | external ref 仍是冪等鍵；新增獨立節點對應，既有 Goal 必須明確映射，不推測認領 |
| 0033 | 擴充使用者目錄內的引擎版本保留／切換規則；Bootstrap 仍不取得目標 repository 清單或讀寫其內容 |

## 後續規格化與驗收

決策樹與整體共同理解均已確認；後續 spec 具體化 schema 與交易。
後續 spec 必須涵蓋上游 artifact export、atomic manifest mapping、授權／扣帳與 run 建立的
crash 一致性、程序 supervision／有限重啟、引擎版本保留／升級、CLI／JSON、TUI／handoff、
legacy cutover、backup recovery 與相容性。CLI 名稱／flags 先進 development-plan 契約表；
本文件不創造已可呼叫的命令。

驗收至少包含 chain／diamond／fan-in、漏節點與依賴不符、來源／核准 digest 漂移、
session→verification 邊界、驗證中 scope 變動保留 Evidence、總額度跨 run／修訂不重置、
人工／外部暫停、logout／reboot／crash、不可確認的 worker、舊 run 切換、引擎升級、
相符／過舊／遺失備份，以及所有 freshness／完成主張的界線。
必須用真正 subprocess 驗證程序控制；fake 不代表真實 Codex 無人值守驗收通過。
依 repository 驗證矩陣執行各切片檢查，integration／final acceptance 保留 full／race gates。

## 訪談時的現況證據

PraxisBound 本機 checkout 在 `/Users/carl/Dev/CMG/ForgeFlowV2`。
`packages/core/src/review/manifest.ts` 與 `types.ts` 已有 `batch.json` 的 sources、requirements
與 dependencies；`review/index.ts` 建立覆蓋 trace、source digests 與 diagnostics；
`packages/cli/src/review.ts` 提供 `praxisbound review index <manifest> --json`。
這些不是既有的 ForgePilot Goal Plan Manifest 或 Human approval；export 與整合仍待實作。

`internal/app/readiness.go` 只看已登錄 Work Items；`internal/runner/runner.go` 的 loop 雖重查
readiness／scope，`afterSession` 卻直接進入 verify，缺少同一份契約重查。
Run Record 沒有跨 run 總帳，也不提供主 Session 關閉後存活的保證。
`run status --json` 並非完整 DAG 進度契約。

`internal/cli/run.go` 的既有單 run 預設是 100 steps、每件工作 3 次 technical attempts、
8 小時、agent／verification 各 30 分鐘；artifact 上限為每次寫入 1 MiB、每 run 16 MiB、
總量 128 MiB。這些不是新授權的預設。`internal/cli/cli.go` 有 `goal cancel`，
`internal/cli/gate.go` 有 `gate cancel`；`internal/cli/cli.go` 的 work 子命令只有 add／list，
搭配 `internal/app/work.go`，沒有 Work Item 層的刪除、取消、改指 Story 或改依賴公開命令。
`internal/runner/runner.go` 的 `DryRun` 呼叫 runtime `Version()`；
`internal/agent/codex.go` 的 `Codex.Version()` 透過
`exec.Command(executable, "--version").Output()` 啟動已解析的 Codex executable。
目前的 state-read-only 與本 ADR 的 no-subprocess inspection 不同。
以上是當次程式碼查核，不是新功能已驗收的證據。

## 決策追溯

| 訪談題目 | 對應契約 |
|---|---|
| Q1、Q6–Q8、Q13 | 上游計畫／覆蓋核准、節點映射與計畫修訂 |
| Q3、Q10、Q14–Q15、Q21 | 授權歷史、累計額度、rollover 與明確續接 |
| Q2、Q9、Q16 | 背景存活、持久化暫停與程序恢復 |
| Q5、Q12、Q17、Q23 | 外部調派、Worker Profile 與固定 ForgePilot 引擎 |
| Q4、Q18 | 共用唯讀進度、preflight 與 probes 的能力界線 |
| Q11、Q19 | 人工／外部交接與具名自述確認 |
| Q20 | Verification 前後的計畫檢查與 Evidence 保留 |
| Q22、Q24 | 舊 run 切換與總帳遺失恢復 |

**Consequences:** 範圍包含上游契約、持久化授權、程序 supervision、分發版本保留與使用介面，
大於擴充 dry-run。Runner 的新授權前提、cutover 前的 legacy run 不再直接 resume，以及 dry-run 不再執行
runtime probe 都是可觀察的相容性變更；必須在 spec／migration／操作文件與驗收中處理。
本輪只完成設計文件，沒有修改或清理既有 release／Story 工作樹變更。

**邊界路徑註記：** TUI 與 supervisor 的實作路徑尚未定案；下列相關條件先記為設計邊界，
後續 spec 決定實作位置時須在本節補入實際路徑，不能以角色名稱當成已有的程式碼邊界。
Bootstrap 的規劃入口為 `scripts/forgepilot-bootstrap`，由
[source-built Bootstrap spec](../specs/source-built-bootstrap.md) 指定，目前尚未實作。

**Falsified if:**

- `internal/runner/runner.go` 保存 Work Item lifecycle 或自行計算合法動作。
- `internal/app/readiness.go` 把 manifest 集合相等當成需求覆蓋證明。
- `internal/cli/cli.go`、`internal/cli/run.go` 或待定路徑的 TUI 直接改寫治理 state。
- `internal/cli/run.go`／`internal/runner/runner.go` 的任何 Runner 啟動或續跑入口繞過授權，
  或因換 run／版本而重置消耗。
- `internal/cli/run.go` 的 dry-run，或新 Goal preflight／TUI 共用的純 inspection 路徑，
  寫入狀態或檔案、啟動任何 subprocess；包含 runtime 版本 probe、Git 與 canonical check。
- 待定路徑的 supervisor 清掉持久化暫停或無聲切換 ForgePilot 引擎版本，
  或 `internal/agent/codex.go` 的 worker 啟動繞過已核准 Worker Profile。
- 規劃中的 `scripts/forgepilot-bootstrap` 為版本保留而建立目標 repository 清單、
  掃描其內容，或刪除仍受有效授權引用的引擎版本。

任一項都必須重新決定責任與信任邊界。
