# ForgePilot

ForgePilot 是被動的 DAG 帳本：工單拆分完成後，讓外部 Agent 依拓撲順序推進工作，並保存每一步的驗證證據與人工決策；工程要求本身由 PraxisBound 定義。

本詞彙表描述 [ADR-0040](docs/adr/0040-forgepilot-is-a-passive-dag-ledger.md) 定案後的語言。收斂實作完成前，程式碼仍含已移除的詞（Runner、VERIFIED、Review Policy、Execution Authorization 等）；它們以 ADR-0040 為準，不再是領域語言。

## Language

### 工作與 DAG

**Goal**：需要跨多次工程工作推進、由一份 Goal Plan 建立的目標；全部 Work Item 成為 DONE 時自動完成。
_Avoid_：Story、Work Item

**Goal Plan**：工單拆分後交給 ForgePilot 匯入的計畫檔，宣告一個 Goal、其節點、每個節點參照的 Story 與節點間依賴。ForgePilot 只保證它構成合法 DAG、Story 路徑存在；拆得對不對是上游與人的責任。再次匯入只能新增節點。
_Avoid_：Goal Plan Manifest、需求覆蓋證明、可隨意改寫的工作清單

**Work Item**：Goal Plan 中的一個節點，以節點 ID 識別，參照一份 PraxisBound Story，具有自己的狀態與依賴。
_Avoid_：Story、Task（作為另一種獨立工作物件）、自動配發的編號

**PraxisBound Story**：由 PraxisBound 管理的工程契約，包含需求、acceptance criteria 與工程指引。ForgePilot 自身借用 PraxisBound 的 Story 目錄格式（`specs/stories/<story-id>/` 下的 `story.md` 與 `acceptance.md`），但不交出治理所有權；`specs/stories/m5-*.md` 是 M5 當時手寫的兩份，保留為刻意的歷史偏離。取捨見 [ADR-0013](docs/adr/0013-forgepilot-self-adoption-of-forgeflow.md)。
_Avoid_：ForgePilot requirement、Work Item 的需求副本

**Readiness**：Work Item 是否所有依賴都已 DONE 的判斷，PENDING 或 READY。每次讀取時由目前 state 計算，不保存。READY 不表示可以忽略該工作自己的 Gate。
_Avoid_：可以開始工作的保證、Gate 已解除、第二份狀態來源

**Actionable Work**：屬於 ACTIVE Goal、READY 且沒有未解除 Gate 的工作。同一時間最多一件工作 RUNNING。
_Avoid_：RUNNING 工作、所有未完成工作

**Agent**：依 ForgePilot 給出的下一個合法動作，在自己的迴圈中讀 Story、實作並呼叫 ForgePilot 的外部 coding agent。ForgePilot 不啟動它，也不保存它的執行歷程。
_Avoid_：Runner、Agent Session、Worker

### 人工決策

**Gate**：依附於單一 Work Item、需要明確 Human Decision 才能解除的工程決策關卡；提出時必須列出至少兩個選項。未解除的 Gate 阻擋該工作推進，但不改變它的狀態。
_Avoid_：自動核准、驗證失敗、Goal 層級的阻擋

**Human Decision**：對某個 Gate 所記錄的選擇——選定其列出的選項之一，連同自述的決策者身分。
_Avoid_：Agent 推測、默認同意、自由作答、經過認證的身分

**Approval Requirement**：Goal 建立時決定、是否要求每件工作在 Verification PASS 後再經 Human Review 才成為 DONE。預設不要求。
_Avoid_：Review Policy、Completion Policy、單件工作的可選屬性

**Human Review**：Goal 有 Approval Requirement 時，人對某個確切 Candidate 所記錄的 APPROVED 或 REJECTED。
_Avoid_：Verification PASS、merge authorization、Human Decision

### 驗證與證據

**Candidate**：Verification 與 Human Review 所共同判斷的 immutable code identity；可以是既有 commit，也可以是從 working tree 固定下來的 snapshot。它不是 Work Item 的狀態或可變 target。
_Avoid_：branch、working tree 本身、WIP commit、第二套 lifecycle

**Revision Identity**：Candidate 的 immutable Git revision；一般 candidate 是既有 commit SHA，snapshot candidate 是由 ForgePilot 保留的 snapshot commit SHA。
_Avoid_：最新版本、branch name、PR number

**Verification**：對某個確切 Candidate 執行 repository 自己定義的 canonical 檢查，其結果構成 Evidence。Toolchain 版本由該檢查自己固定，ForgePilot 不代為解析。
_Avoid_：Agent 自我測試、臨時指定的 shell command

**Verification Run**：為一件 Work Item 執行的一次 canonical Verification，以 durable run ID 識別。進行中的 run 是短暫狀態，可能因中斷而不留下成功或失敗的結論。
_Avoid_：Evidence、VERIFYING 狀態本身、替多件工作共用的執行

**Verification Log**：一次 Verification Run 的原始輸出，以該次執行為鍵保存在 state 之外。它是事後診斷用的材料，不是結論——結論只在 Evidence 裡。
_Avoid_：Evidence、驗證結果、完成宣告

**Evidence**：針對特定工作與 Candidate 保存的驗證或審查紀錄；可能成功、失敗，或因中斷而未產生結果。它綁定的是一個 immutable revision 與一個時間，不表達工作發生的先後。
_Avoid_：完成宣告、Agent 自評、工作的時序、補跑與當下執行的區別

**Stale**：等待 Human Review 的工作，其 PASS Evidence 所綁定的 Candidate 已不符合目前 workspace；commit candidate 比較 HEAD，snapshot candidate 比較自動計算的 digest。DONE 的工作不標示 stale。
_Avoid_：失效、FAIL、需要重做

**DONE**：工作在某個確切 Candidate 上取得 Verification PASS（Goal 有 Approval Requirement 時另需 APPROVED）後的終態。後續的 commit 不會使其失效，也不會使其重開；完成同一交易內解鎖下游。
_Avoid_：Agent 停止執行、RUNNING、VERIFIED、已 merge、已 release、可重開的狀態
