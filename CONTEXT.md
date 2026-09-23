# ForgePilot

ForgePilot 管理工程工作的可執行性、進度與決策證據；工程要求本身由 PraxisBound 定義。

## Language

**Distribution Bootstrap**：在使用者環境中安裝 ForgePilot CLI 與一個可被指定 Agent 發現的 skill adapter 的分發流程；它不讀取、改寫或初始化任何受管理 repository。
_Avoid_：Repository Onboarding、silent install、核心 CLI command

**Repository Onboarding**：在一個目標 Git repository 導入 ForgePilot 的受控流程，包含 Candidate preflight、Story review，以及經明確授權後的 state 初始化與第一個 Goal／Work Item 建立。
_Avoid_：Distribution Bootstrap、skill installation、automatic project setup

**Bootstrap Transaction**：Distribution Bootstrap 對一個固定 ForgePilot source commit 的可回復安裝交易；CLI entrypoint 與 Agent skill 經同一個 managed current pointer 在新版本完成 staging、build 與驗證後一起切換，失敗時既有版本仍是唯一可用版本。
_Avoid_：Repository Onboarding transaction、partial upgrade、best-effort installation

**Bootstrap Source**：Bootstrap Transaction 以絕對本機 Git `--source` 與完整 40-character commit SHA 指定、由 installer 在 staging 取得 detached checkout 的 ForgePilot source identity；執行 installer 的工作樹不構成被安裝版本的身分。
_Avoid_：installer checkout、floating branch、latest release alias

**Bootstrap Generation**：由一個 Bootstrap Source 建立、經驗證後不可變的安裝結果；其 identity 同時包含完整 source commit 與 staged payload digest，不能只用版本顯示字串代替。
_Avoid_：release label、尚未驗證的 build、可覆寫的安裝目錄

**Generation Retention Reference**：對一個 Bootstrap Generation 仍被授權工作引用的 opaque claim；它不表達或保存 target repository identity。
_Avoid_：repository registry、run log、可推測的 owner name

**Bootstrap Approval**：使用者對一份展示了 Bootstrap Transaction 來源、版本、路徑與效果的 action plan 所作的明確同意；它只授權使用者目錄的 CLI／skill 安裝，不授權任何 Repository Onboarding 寫入。
_Avoid_：Repository Onboarding approval、implicit consent、blanket authorization

**Bootstrap Manifest**：Bootstrap 管理的使用者目錄中、只描述已管理版本與 current／previous CLI／skill 指標的安全狀態檔；它不保存 target repository、ForgePilot state、環境值、憑證或 command output。
_Avoid_：ForgePilot state、installation log、target repository registry

**Goal**：需要跨多次工程工作推進的長期目標。
_Avoid_：Story、Work Item

**Goal Review Policy**：Goal 持久化的例行審查政策；`WORK_ITEM`（預設）保留逐件工作的人工作業審查，`GOAL` 由機器驗證結果推進依賴，且在整個 Goal 的完成條件成立時自動完成。
_Avoid_：skip review、單次指令的權限、Work Item 的可選屬性

**Completion Policy**：由 Review Policy 推導並保存在 state 的相容性欄位；`VERIFIED` 對應 `GOAL`，在所有 Work Item 有 current PASS 且無 OPEN Gate 時以 aggregate completion evidence 原子完成 Goal；`HUMAN` 對應 `WORK_ITEM`。它不是使用者可選的 Goal 終審模式；舊 `AWAITING_GOAL_REVIEW` 僅供歷史 run record 顯示。
_Avoid_：把 VERIFIED Work Item 改成 DONE、Agent 自我宣告、未綁定 Candidate 的完成

**Work Item**：歸屬一個 Goal、參照一個 PraxisBound Story 的工程工作單位，具有自己的狀態與依賴。
_Avoid_：Story、Task（作為另一種獨立工作物件）

**External Work Reference**：呼叫方在建立 Work Item 時提供、只作同一 Goal 內安全重試的不可變冪等鍵。它不代表 Story identity、需求內容、PR、revision 或 lifecycle；未帶此鍵的舊 Work Item 不會被事後推測或認領。
_Avoid_：Story ID 的替代品、可修改 metadata、跨 Goal 全域 ID、完成／審查條件

**PraxisBound Story**：由 PraxisBound 管理的工程契約，包含需求、acceptance criteria 與工程指引。ForgePilot 自身借用 PraxisBound 的 Story 目錄格式（`specs/stories/<story-id>/` 下的 `story.md` 與 `acceptance.md`），但不交出治理所有權；`specs/stories/m5-*.md` 是 M5 當時手寫的兩份，保留為刻意的歷史偏離。取捨見 [ADR-0013](docs/adr/0013-forgepilot-self-adoption-of-forgeflow.md)。
_Avoid_：ForgePilot requirement、Work Item 的需求副本

**Story Readiness Contract**：由 PraxisBound 擁有、隨單一 PraxisBound Story 提供的版本化 machine-readable 宣告；它列出 ForgePilot 可讀取的 inputs、outputs、criterion operations、future identities 與 decision follow-up refs，但不把 Story schema 或核准權交給 ForgePilot。取捨見 [ADR-0029](docs/adr/0029-story-readiness-contract-is-upstream-owned.md)。
_Avoid_：Work Item requirement、ForgePilot-owned Story schema、Story Markdown inference

**Story Source Digest**：Story Readiness Contract 對其命名的 `story.md` 或 `acceptance.md` 原始 bytes 所宣告的 SHA-256 identity。PraxisBound 產生它；ForgePilot 只重算 bytes 並比對，不解析 Markdown。
_Avoid_：Markdown semantic hash、ForgePilot-owned criterion coverage、可由 prose 推論的宣告

**Goal Plan Manifest**（[已定案](docs/adr/0035-supervised-goal-execution-with-bounded-rollover.md)）：PraxisBound 對一份 Goal 計畫所宣告的完整工作節點、依賴與 Story 契約集合；ForgePilot 透過 read-only preflight 核對其結構、綁定與登錄是否完整且一致。它不是原始需求沒有漏拆的證明，也不代表後續授權與執行能力已完成。
_Avoid_：Run Record、未來 action 清單、需求覆蓋證明

**Plan Node Reference**（[已定案，待實作](docs/adr/0035-supervised-goal-execution-with-bounded-rollover.md)）：Goal Plan Manifest 中一個工作節點的識別，在同一 Goal 內與一張 Work Item 明確對應；不同節點可引用同一個 Story。
_Avoid_：Story path、External Work Reference、Work Item lifecycle

**Plan Coverage Review**（[已定案，待實作](docs/adr/0035-supervised-goal-execution-with-bounded-rollover.md)）：PraxisBound 對原始需求與 Goal 計畫之間覆蓋關係所提供、經明確人工核准且綁定受審來源與計畫版本的審查結論；它與 ForgePilot 核對計畫是否完整登錄是不同的責任。
_Avoid_：manifest 集合相等、Verification PASS、Human final acceptance

**Whole-DAG Story Readiness Review**：ForgePilot 在一個 Goal 上對所有 Story Readiness Contracts、Work Item dependency DAG 與本機 artifact facts 所做的 read-only preflight；它回報規劃缺陷，既不是 Work Item Readiness projection、Gate、Evidence，也不授權 lifecycle transition。
_Avoid_：Verification、Agent judgment、second state machine、PASS

**Delivered Input**：由 prerequisite Story Readiness Contract 宣告、被 downstream Story contract 消費的 output；它是規劃關係，不是已被實作或取得 Verification PASS 的主張。
_Avoid_：Evidence、verified artifact、implicit prose dependency

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

**Verification Run**：一次 canonical Verification execution。它以 durable run ID 識別；PASS 可以在同一個 Goal 中為多張符合資格的 Work Item 各留下 Evidence，但 FAIL／INTERRUPTED 只屬於觸發執行的 anchor。進行中的 run 仍是短暫狀態，可能因中斷而不留下成功或失敗的結論。
_Avoid_：Evidence、VERIFYING 狀態本身

**Runtime Contract**：Candidate 自己透過版本檔或 ecosystem manifest 宣告的 runtime／toolchain 要求；ForgePilot 只解析並選用本機已安裝的符合版本，不安裝或改動使用者全域環境。
_Avoid_：caller shell 當下的 PATH、ForgePilot 自行推測的 primary language、dependency installation

**Resolved Runtime**：某次 Verification Run 開始前已驗證並固定的 subprocess environment，以及其中各 runtime 的實際版本；它隨 run 進入 Verification Evidence。
_Avoid_：永久 shell 設定、完整 PATH、只記宣告版本而未驗證實際 executable

**Stale**：既有 Verification Evidence 所綁定的 Candidate 已不符合目前 workspace；commit candidate 比較 HEAD，snapshot candidate 比較自動計算的 digest。DONE 的工作不標示 stale——它已在某個確切 Candidate 上完成，重驗的提示對它不對應任何行動。
_Avoid_：失效、FAIL、需要重做

**Revision Identity**：Candidate 的 immutable Git revision；一般 candidate 是既有 commit SHA，snapshot candidate 是由 ForgePilot 保留的 snapshot commit SHA。PR Reference 附加於審查紀錄上作為識別，不構成版本身分的一部分。
_Avoid_：最新版本、branch name、PR number 單獨作為版本身分

**PR Reference**：一筆 Human Review 所聲明的 pull request 出處，形式為 `owner/name#number`。它是使用者輸入的識別字串；ForgePilot 只驗格式，不查證該 PR 存在或其 HEAD 為何。
_Avoid_：經過查證的 PR 狀態、merge authorization、review target 本身

**Readiness**：Work Item 持久化的 `PENDING`／`READY` 值，表達「依賴在上次計算時是否已依 Goal 的 progression policy 滿足」。它是可由目前 state 與 repository facts 重算的 projection，只是被保存下來；Gate 或 Candidate 移動使它退回 PENDING 後，條件恢復不會自動改寫它，要由 `reconcile` 明確重算。READY 不表示可以忽略該工作自己的 Gate。
_Avoid_：可以開始工作的保證、Gate 已解除、第二份狀態來源

**Actionable Work**：屬於可執行 Goal、處於 READY、依賴已依 Goal 的 progression policy 滿足（WORK_ITEM 為 DONE；GOAL 可為 fresh VERIFIED）且沒有未解決 Gate 的工作。
_Avoid_：RUNNING 工作、所有未完成工作

**Human Review**：對特定 revision 所記錄的工程審查結果，為 APPROVED 或 REJECTED。
_Avoid_：Verification PASS、merge authorization、Human Decision

**VERIFIED**：採 `GOAL` Review Policy 的 Work Item 已在某個 immutable Candidate 上取得機器 PASS 的狀態；它可依 policy 滿足下游依賴，但不是 Human acceptance、DONE 或 Goal 完成，Candidate 後來 stale 時仍必須重驗。
_Avoid_：APPROVED、DONE、跳過審查

**Goal Completion Evidence**：`GOAL` policy 的 current-verification 條件成立、Goal 自動完成時，在同一次 state transaction 保存的不可變 aggregate record；它綁定 Goal、repository、Candidate facts 與精確的 latest Verification Evidence IDs。這是唯一能證明自動 completion transaction 已提交、供 Runner final-facts recovery 使用的 provenance，不是另一個 Work Item lifecycle。
_Avoid_：Goal 的可變進度快取、Human Review Evidence、只保存一句完成宣告

**Legacy Goal Completion Provenance**：v11 `GOAL/HUMAN` 已是 `COMPLETED` 時，v12 migration 隨 Goal 保存的來源 schema 與 policy 標記；它只保留舊 lifecycle 事實，不宣稱 current Candidate、Verification PASS 或 Gate 狀態，也不能代替 Goal Completion Evidence。
_Avoid_：自動完成證明、Runner crash-recovery 證據、推測舊終審者或完成時間

**Runner**：由使用者明確啟動、對單一 Goal 依 ForgePilot 判定循序執行的本機執行迴圈。它保存的是 execution history——step、attempt、預算、程序 ownership、停止原因——不保存 Work Item 的 lifecycle，每一步都重新向 domain 取得下一個合法動作。
_Avoid_：daemon、排程器、第二套工作狀態機、自動核准者

**Agent Session**：Runner 為一張 Work Item 的一次 attempt 所啟動的一個全新 coding CLI 程序。每次實作或修復都是新的 session，不延續前一次對話。
_Avoid_：長對話、跨 Work Item 的脈絡、Verification Run

**Main Agent Session**（[已定案，待實作](docs/adr/0035-supervised-goal-execution-with-bounded-rollover.md)）：使用者下達執行意圖、查看進度與接手工作的外部 Agent 對話；它透過 ForgePilot 的公開介面調派工作，不擁有 Work Item lifecycle 或合法動作判定權。
_Avoid_：Runner 啟動的 Agent Session、程序 supervisor、持久化進度來源

**Execution Authorization**（[ADR-0035](docs/adr/0035-supervised-goal-execution-with-bounded-rollover.md)）：使用者對指定 workspace 與 Goal 給予的有界執行授權；每個可供 Runner launch 的版本綁定計畫、已解析的 Worker Profile、ForgePilot 引擎 generation、總額度與固定到期時間，累計消耗跨 run 與授權修訂保留。FP-53 revision one 只記錄明確要求的 profile 與額度，未宣稱已解析 worker／engine identity，因此不可 launch；FP-58 必須以 evidence-bearing transition 驗證並綁定這些 identity。它不授予 Human Decision、Human acceptance 或額外工程範圍。
_Avoid_：Run Record、單次 run 預算、無限續跑、經過認證的身分

**Engine Retention Owner**（[ADR-0039](docs/adr/0039-per-owner-engine-generation-retention.md)）：對一個 immutable ForgePilot engine generation 仍可啟動或恢復執行的明確持有人；current Execution Authorization 與一個尚未 durable-close 的 supervised Run 各自是不同 owner。owner 以 opaque Bootstrap retention marker 保護 generation，不保存 repository 或 holder identity 到 Bootstrap。
_Avoid_：Bootstrap generation 本身、Run Record、可由歷史 Authorization 自動推測的 owner、reference token

**Engine Compatibility Check**（[ADR-0039](docs/adr/0039-per-owner-engine-generation-retention.md)）：在 engine revision 前，candidate process 對目前的 execution state、control sidecar 與相關 Run Record 所做的 read-only、fail-closed 可讀性與 identity 檢查；它證明 candidate 可以接手，不是 canonical Verification、migration 或 Agent capability claim。
_Avoid_：make verify、bootstrap install、runtime default、best-effort repair

**Worker Profile**（[ADR-0035](docs/adr/0035-supervised-goal-execution-with-bounded-rollover.md)）：一個 Execution Authorization 版本內實作與修復共用、綁定解析後 executable 身分的 Agent runtime、model、effort 與權限選擇；resume 與跨 run 續接沿用同一份選擇。
_Avoid_：Agent Session Check Profile、Verification Runtime Contract、調派模型

**Goal Plan Binding**（[ADR-0035](docs/adr/0035-supervised-goal-execution-with-bounded-rollover.md)）：經覆蓋核准的完整 Goal Plan 與 ForgePilot Goal 的明確一對一連結；每個 Plan Node Reference 對應同 Goal 內恰好一張 Work Item，並保留計畫拓撲與來源 digest。
_Avoid_：由 Story 路徑推測 mapping、External Work Reference、部分子圖 adoption

**Execution Ledger**（[ADR-0035](docs/adr/0035-supervised-goal-execution-with-bounded-rollover.md)）：跨 run 與授權修訂累計執行消耗的唯一權威；無法確認是否執行的消耗不會自動退還。
_Avoid_：Run Record、可重設的單次預算、從 log 推算的總額

**Artifact-byte Reservation**：Execution Ledger 內以穩定 ID append 的、在 Agent 輸出前先扣除的 artifact 容量消耗。它是跨 run／revision 的事實，必須同 ledger、witness 與 approval-token freshness 一起驗證；v15 前的已授權歷史不推測這項消耗，而是標為 unknown，停止後續執行直到明確重新授權。
_Avoid_：由 `.forgepilot/runs` 或 log 回推、可退還的暫存額度、Run Record 欄位

**Goal Execution Witness**：連結 Goal Plan Binding、目前 Execution Authorization 與 Execution Ledger 的完整性證據；它不另行擁有預算或 lifecycle。
_Avoid_：第二份總帳、Human acceptance、Work Item status

**Approval Token**：依據精確 request／artifact bytes 與當時 Goal registration 產生的 preview freshness digest；相符時表示被檢視的輸入未變，不證明 approver 身分或授權資格。
_Avoid_：credential、簽章、authenticated identity

**External Fulfillment Declaration**（[已定案，待實作](docs/adr/0035-supervised-goal-execution-with-bounded-rollover.md)）：對 ForgePilot 無法離線查證的外部條件所作、綁定計畫與授權版本、節點、條件及等待項目的具名自述確認，供明確 resume 使用。
_Avoid_：Verification Evidence、Gate resolution、Human final acceptance、經查證的外部事實

**Agent Session Check Profile**：Runner handoff 對該次 Agent Session 的檢查責任說明；session 做 focused diagnostics，Runner 另行擁有正式 Candidate Verification，project instructions 指定的 integration/final owner 再負責 canonical 以外的 gates。它是 instruction-only，不是 Evidence 或另一套 Verification。
_Avoid_：Worker Verification、PASS claim、check attestation、repository command manifest

**Agent Result**：一次 Agent Session 交回的結構化結果，為 `implementation_finished`、`needs_human` 或 `execution_failed`。它是未受信任的模型輸出，只作為摘要與停止理由；`implementation_finished` 只表示這次實作結束。
_Avoid_：PASS、Evidence、完成宣告、授權

**Run Record**：某一次 Runner 執行的持久化 execution history，存在 `.forgepilot/runs/<run-id>/`。恢復時以 ForgePilot 最新 domain state 為準，Run Record 只提供預算、程序 ownership 與其 immutable authorization／engine binding 的核對材料；它不擁有 Goal ledger 或 Work Item lifecycle。

**Pending Execution**：Run Record 裡一筆「Runner 啟動了某個外部程序，但還沒能確認它停下來」的紀錄——agent session、canonical check、runtime preflight 或 Git 子程序都算。它在程序啟動之前寫下，確認清理完成之後才移除，因此跨程序存活：重啟 CLI、換 run ID 或在同一個 workspace 換一個 Goal 都讀得到它。它與 **Stop Reason** 是兩件事——後者說「上一次為什麼結束」，`resume` 會清掉；恢復阻擋不會。
_Avoid_：第二份 lifecycle、進度來源、Verification Log

**Workspace Ownership**：同一個 workspace 同時只能有一個 Runner 的協調機制，以 canonical path 上的鎖表達；symlink 別名視為同一個 workspace。它只協調 Runner，不宣稱能阻止其他程序修改檔案。
_Avoid_：Verification Run 的 per-work lock、state 交易鎖、Git 鎖

**DONE**：工作在某個確切 revision 上滿足驗證與人工審查條件後的終態。它記錄的是已經發生的事，後續的 commit 不會使其失效，也不會使其重開。
_Avoid_：Agent 停止執行、RUNNING、已 merge、已 release、可重開的狀態
