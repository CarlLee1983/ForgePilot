# ForgePilot

ForgePilot 管理工程工作的可執行性、進度與決策證據；工程要求本身由 ForgeFlowV2 定義。

## Language

**Goal**：需要跨多次工程工作推進的長期目標。
_Avoid_：Story、Work Item

**Work Item**：歸屬一個 Goal、參照一個 ForgeFlow Story 的工程工作單位，具有自己的狀態與依賴。
_Avoid_：Story、Task（作為另一種獨立工作物件）

**ForgeFlow Story**：由 ForgeFlowV2 管理的工程契約，包含需求、acceptance criteria 與工程指引。
_Avoid_：ForgePilot requirement、Work Item 的需求副本

**Gate**：需要明確 Human Decision 才能解除的工程決策關卡。
_Avoid_：自動核准、驗證失敗

**Human Decision**：人對 Gate 所記錄的明確選擇或判斷。
_Avoid_：Agent 推測、默認同意

**Evidence**：針對特定工作與 revision 保存的驗證或審查紀錄，可能成功，也可能失敗。
_Avoid_：完成宣告、Agent 自評

**Revision Identity**：Evidence 所對應的精確工程版本身分；完成與審查涉及 repository、Story 與 commit，PR review 另涉及 PR 與其 HEAD。
_Avoid_：最新版本、branch name

**Actionable Work**：屬於可執行 Goal、處於 READY、依賴已完成且沒有未解決 Gate 的工作。
_Avoid_：RUNNING 工作、所有未完成工作

**Human Review**：人對特定 revision 所記錄的工程審查結果。
_Avoid_：Verification PASS、merge authorization

**DONE**：工作已滿足適用的驗證與人工審查條件後的完成狀態。
_Avoid_：Agent 停止執行、RUNNING、已 merge、已 release
