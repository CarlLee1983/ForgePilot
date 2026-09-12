# ForgePilot

ForgePilot 管理工程工作的可執行性、進度與決策證據；工程要求本身由 ForgeFlowV2 定義。

## Language

**Goal**：需要跨多次工程工作推進的長期目標。
_Avoid_：Story、Work Item

**Work Item**：歸屬一個 Goal、參照一個 ForgeFlow Story 的工程工作單位，具有自己的狀態與依賴。
_Avoid_：Story、Task（作為另一種獨立工作物件）

**ForgeFlow Story**：由 ForgeFlowV2 管理的工程契約，包含需求、acceptance criteria 與工程指引。ForgePilot 自身尚未接上 ForgeFlowV2——`specs/stories/` 底下目前是為滿足路徑檢查而手寫的臨時契約，取捨見 [ADR-0013](docs/adr/0013-forgepilot-self-adoption-of-forgeflow.md)。
_Avoid_：ForgePilot requirement、Work Item 的需求副本

**Gate**：依附於單一 Work Item、需要明確 Human Decision 才能解除的工程決策關卡；提出時必須列出至少兩個選項。未解除的 Gate 阻擋該工作推進，但不改變它的狀態。
_Avoid_：自動核准、驗證失敗、Goal 層級的阻擋

**Human Decision**：對某個 Gate 所記錄的選擇——選定其列出的選項之一，連同自述的決策者身分。
_Avoid_：Agent 推測、默認同意、自由作答、經過認證的身分

**Candidate**：Verification 與 Human Review 所共同判斷的 immutable code identity；可以是既有 commit，也可以是從 working tree 固定下來的 snapshot。它不是 Work Item 的狀態或可變 target。
_Avoid_：branch、working tree 本身、WIP commit、第二套 lifecycle

**Evidence**：針對特定工作與 Candidate 保存的驗證或審查紀錄；可能成功、失敗，或因中斷而未產生結果。它綁定的是一個 immutable revision 與一個時間，不表達工作發生的先後。
_Avoid_：完成宣告、Agent 自評、工作的時序、補跑與當下執行的區別

**Verification**：對某個確切 Candidate 執行 repository 自己定義的 canonical 檢查，其結果構成 Evidence。
_Avoid_：Agent 自我測試、臨時指定的 shell command

**Verification Log**：一次 Verification Run 的原始輸出，以該次執行為鍵保存在 state 之外。它是事後診斷用的材料，不是結論——結論只在 Evidence 裡。
_Avoid_：Evidence、驗證結果、完成宣告

**Verification Run**：一次進行中的 Verification。它是短暫的，可能因中斷而結束並且不留下成功或失敗的結論。
_Avoid_：Evidence、VERIFYING 狀態本身

**Stale**：既有 Verification Evidence 所綁定的 Candidate 已不符合目前 workspace；commit candidate 比較 HEAD，snapshot candidate 比較自動計算的 digest。DONE 的工作不標示 stale——它已在某個確切 Candidate 上完成，重驗的提示對它不對應任何行動。
_Avoid_：失效、FAIL、需要重做

**Revision Identity**：Candidate 的 immutable Git revision；一般 candidate 是既有 commit SHA，snapshot candidate 是由 ForgePilot 保留的 snapshot commit SHA。PR Reference 附加於審查紀錄上作為識別，不構成版本身分的一部分。
_Avoid_：最新版本、branch name、PR number 單獨作為版本身分

**PR Reference**：一筆 Human Review 所聲明的 pull request 出處，形式為 `owner/name#number`。它是使用者輸入的識別字串；ForgePilot 只驗格式，不查證該 PR 存在或其 HEAD 為何。
_Avoid_：經過查證的 PR 狀態、merge authorization、review target 本身

**Actionable Work**：屬於可執行 Goal、處於 READY、依賴已完成且沒有未解決 Gate 的工作。
_Avoid_：RUNNING 工作、所有未完成工作

**Human Review**：對特定 revision 所記錄的工程審查結果，為 APPROVED 或 REJECTED。
_Avoid_：Verification PASS、merge authorization、Human Decision

**DONE**：工作在某個確切 revision 上滿足驗證與人工審查條件後的終態。它記錄的是已經發生的事，後續的 commit 不會使其失效，也不會使其重開。
_Avoid_：Agent 停止執行、RUNNING、已 merge、已 release、可重開的狀態
