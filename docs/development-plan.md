# Development plan

## 計畫狀態

M1–M4 已全部完成。本文件供後續開發拆分工作、驗收與交接；產品規則見 [architecture.md](architecture.md)。

開發時如採用 ForgeFlowV2，工程 requirements 與 acceptance criteria 由正式 Story 承載，Work Item 只 reference Story。本文件不另定 Story schema，也不自動產生 Story。

## Milestones

| 階段 | 範圍 | Exit criteria |
|---|---|---|
| M1 — Durable Engineering Queue | 六個 CLI 指令、Goal／Work Item、依賴、basic transition、local JSON | 可選取、開始工作，且跨 process 保存一致 |
| M2 — Verification Evidence | Revision resolver、`make verify` runner、PASS／FAIL Evidence、stale detection | Evidence 綁定確切受測 revision，新 revision 不沿用舊 PASS |
| M3 — Human Gate & Review | Gate、Decision、Human Review、DONE 與依賴解鎖 | 合法完成 A 後 B 可執行；未 review 不可 DONE |
| M4 — PR exact-head review integration | PR number＋HEAD SHA review target | 新 HEAD 必須重新取得適用 review Evidence |

M1 通過後才開始 M2；M2 通過後才開始 M3。MVP 完成範圍為 M1–M3；M4 為後續階段。各階段不得提前加入 database、Web UI、daemon、scheduler framework、agent runtime、plugin framework 或 network API。

## M1 驗收衝突的處理

原始成功流程要求 M1 完成 A lifecycle 再解鎖 B，但 M1 同時排除 verification 與 Human Review；完整 lifecycle 因而不可能在 M1 合法到達 DONE。

採用以下分段驗收：

- M1 真實 CLI 流程驗收到 A RUNNING、B PENDING 與 process restart。
- M1 以測試內建立的 dependency fixture 驗證「A 已為 DONE 時，B 可 READY」。Fixture 不提供產品指令或修改使用者 state 的捷徑。
- M3 才執行真實的 A → verify → review → DONE → B READY 端到端驗收。**已完成**：見 `integration_test.go` 的 `TestEndToEndQueueAdvances`，以獨立 process 對真實 Git repository 跑完整條流程。

不加入臨時 `complete`、`set-status` 或測試專用 approve，也不把 RUNNING 宣稱為完成。

## M1 開工前定案（已完成）

1. 固定初始支援 OS 與 filesystem 範圍，選定可跨 process 自動釋放的鎖機制；不宣稱未驗證的跨平台支援。
2. 固定 Go toolchain 版本與實際 module path；不提交 placeholder module identity。

M1 採 macOS 本機檔案系統與 OS `flock` 程序鎖，Go 1.25.5，module `github.com/carl/forgepilot`。不需要為 M1 預先解決 M2／M3 的全部開放問題。

## M1 依賴順序與交付切片

M1 已依序完成下列切片。

### 1. 工程基礎（完成）

- [x] 建立 Go module 與 `cmd/forgepilot` 入口。
- [x] 建立 root `make verify`：格式檢查、`go vet`、`go test ./...` 與 CLI build。
- [x] 格式檢查不得靜默改寫工作目錄。
- [x] 更新 README，清楚區分已實作與規劃功能。

驗收：最小 CLI 可建置，canonical verification 可執行。尚不建立 M2／M3 的空 package 或假介面。

### 2. Domain rules（完成）

- [x] 建立 Goal／Work Item 與 M1 可用狀態。
- [x] 集中 transition policy，拒絕非法轉移。
- [x] 實作依賴驗證、initial readiness 與 next-work selection。
- [x] 提供可控制的時間與 deterministic ID 排序。
- [x] 加入 domain unit tests。

驗收：規則可在不碰 filesystem、Git 或 subprocess 的測試中驗證。

### 3. Durable storage 與建立流程（完成）

- [x] 實作 schema validation、鎖與原子 read-modify-write。
- [x] 實作 `init`、`goal create`、`work add`。
- [x] Story reference 驗證與 `.gitignore` 保存。
- [x] 驗證 init 可安全重試，並行新增不遺失更新或重複配發 ID。

驗收：命令成功後的新 process 可讀回完整資料；失敗不截斷或清空既有 state。

### 4. Queue 操作（完成）

- [x] 實作 `next`、`start`、`status`。
- [x] `next`／`status` 無寫入副作用。
- [x] `start` 在交易內重查條件，重複 start 回傳明確錯誤。
- [x] `status` 支援零件、一件或多件 RUNNING 工作。

驗收：A 可被選取與開始；依賴 A 的 B 保持 PENDING。

### 5. 整合驗收與交接

- [x] Temporary repository fixture 與獨立 process CLI integration tests。
- [x] 通過下方 M1 驗收矩陣與 `make verify`。
- [x] 檢查最終 diff、文件一致性與未授權的範圍擴張。
- [x] 依交付格式回報實際結果，不把未執行檢查標成 PASS。

## M1 CLI contract

所有指令操作最近的 `.forgepilot/` state root。未 init、未知 ID、無效 reference、非法 transition、損毀 state 或保存失敗需有清楚錯誤，不得 panic 或靜默成功。

| 指令 | 輸入與成功結果 |
|---|---|
| `forgepilot init` | 在 repository root 建立 state；已存在時不覆寫；補齊 ignore entry |
| `forgepilot goal create --id <id> --title <title> [--description <text>]` | 建立 ACTIVE Goal；repository 綁定 state root；省略 description 時保存空字串 |
| `forgepilot work add --goal <goal-id> --story <path> [--depends-on <work-id>]` | 驗證後配發 Work Item ID，回傳 ID、Story 與 PENDING／READY；多個依賴可重複傳 flag |
| `forgepilot next` | 跨本機 ACTIVE Goals 選最早 READY 工作，顯示 Goal、Work Item ID、Story 與選取理由 |
| `forgepilot start <work-id>` | READY → RUNNING；無隱含 Agent spawning 或 claim lease |
| `forgepilot status` | 顯示 Goals、Work Items、所有 Current Work 與 Next Work |

成功查詢但無 actionable work 是正常結果：顯示無 READY 工作，exit code 0。非法操作與 I/O 失敗使用非零 exit code，diagnostic 寫至 stderr；M1 不需要細分大型 exit-code taxonomy。

M1 不提供 `--json`、任意 status mutation 或 identity framework。Open Gates 與 Last Verification 欄位可以先省略；若呈現，必須標示「尚未支援」，不能顯示為已驗證沒有 Gate／Evidence。

## 預計結構

```text
cmd/forgepilot/main.go
internal/cli/
internal/work/
internal/repository/
internal/storage/
docs/architecture.md
docs/development-plan.md
docs/adr/
CONTEXT.md
README.md
Makefile
go.mod
.gitignore
```

Test files 隨對應 package 放置。Integration fixtures 優先在 temporary directory 建立，不提前建立永久範例專案、Gate／Evidence 目錄或介面層。

## M1 驗收

### Unit 與 storage

| Boundary | 必須驗證的行為 |
|---|---|
| Goal | 建立 ACTIVE、拒絕重複 ID；非 ACTIVE Goal 不進 selection |
| Transition | READY → RUNNING 合法；PENDING → RUNNING、重複 start、跳到 DONE 被拒絕 |
| Dependency | 未知、自我、重複、跨 Goal、循環資料被拒絕；未完成依賴阻擋；全部 DONE 可解鎖 |
| Selection | 最早 READY；同 timestamp 固定順序；無候選；多 Goal；不修改 snapshot |
| Story reference | 存在的檔案／目錄可參照；缺失、路徑穿越與 symlink 逃逸被拒絕 |
| Persistence | 保存／載入、safe init retry、JSON 損毀與較新 schema 拒讀，不清空資料 |
| Write integrity | 兩個 process 新增均保留且 ID 唯一；注入寫入失敗不留下截斷 state；程序終止後鎖可再取得 |

測試使用 Go 既有測試能力，優先行為測試，不為每個 getter 建立測試。M1 不製造 Goal 完成、Gate 或 Evidence 的假實作來滿足尚未啟用的測試項目。

### CLI integration

建立 temporary Git repository，內含兩個 `specs/stories/` fixture。M1 不解析 Story schema，也不要求受管理 fixture 的 Makefile；M2 擴充 fixture 的 Makefile 與 commit。

每個命令都以獨立 CLI process 執行：

```text
init
→ goal create
→ work add A                  => WI-001 READY
→ work add B depends WI-001   => WI-002 PENDING
→ next                       => WI-001 / Story A
→ start WI-001                => RUNNING
→ 新 process: status          => A RUNNING、B PENDING
→ 新 process: next            => 無 READY 工作，成功退出
```

另驗證不存在的 Story、重複 Goal ID、未知 dependency、重複 start 與損毀 state 的錯誤；確認診斷不破壞資料。依賴解鎖以測試內 fixture 驗證，不手改實際工作 state。

### M1 Exit checklist

- [x] 六個指令都能穩定執行，且 scope 未越過 M1。
- [x] Domain、storage、CLI integration 的必測行為均通過。
- [x] 新 process 讀回狀態一致。
- [x] `make verify` 通過，記錄實際環境與命令。
- [x] README 與 architecture 反映實際行為，待定事項仍清楚標示。
- [x] 無未 review 即 DONE 的產品路徑。

## 後續階段的開工與驗收重點

### M2

開工前定案事項已全部完成，記錄於 [architecture.md](architecture.md#m2-開工前定案已完成) 與 `docs/adr/0001`–`0004`。剩下的是實作。

M2 新增兩個指令，不新增其他：

| 指令 | 輸入與成功結果 |
|---|---|
| `forgepilot verify <work-id>` | 於隔離 worktree 執行受管理專案的 `make verify`，append Evidence；PASS → REVIEW，FAIL → RUNNING |
| `forgepilot migrate` | 備份後將 v1 state 升級為 v2；已是 v2 時回報並成功結束 |

`status` 擴充為顯示每件工作最新一筆 Evidence 的 result 與 SHA，以及是否 stale。不提供 `evidence` 查詢指令，不提供 `--json`。

`verify` 的前置條件：Goal 為 ACTIVE、Work Item 為 RUNNING 或 REVIEW、repo 有 HEAD、工作樹乾淨、受管理專案有 `make verify` target。對同一 SHA 重跑不受限制。開頭交易先回收孤兒 VERIFYING。

交付切片依序為：schema v2 與 `migrate`；`internal/repository` 的 revision resolver 與 worktree 隔離；canonical runner 與 Evidence append；`verify` 指令與 transition；`status` 的 stale 呈現。

Integration fixture 加入 Makefile 與真實 commit。驗收：PASS → REVIEW、FAIL → RUNNING、新 commit 不沿用舊 PASS、髒工作樹被拒、缺少 `make verify` 被拒且不留 Evidence、程序中斷回收為 INTERRUPTED 且不產生假 PASS、隔離 worktree 看不到主樹未提交內容、`git worktree` 殘骸被 prune 清除。另以既有 M1 state fixture 驗證 v1 被拒讀、`migrate` 的備份與重複執行安全，以及備份檔已存在時的拒絕。M2 仍不提供 DONE。

### M2 Exit checklist

- [x] `verify` 與 `migrate` 可穩定執行，且 scope 未越過 M2。
- [x] PASS → REVIEW、FAIL → RUNNING；Evidence 綁定完整 commit SHA 且只累積不覆寫。
- [x] 髒工作樹（含未追蹤檔案）與缺少 `make verify` 均被拒絕且不留 Evidence。
- [x] 驗證在隔離的 detached worktree 執行，並以測試證明它看不到主工作樹的未提交內容。
- [x] 驗證進行中 `status` 與 `next` 仍可執行；同一 Work Item 不可並行驗證，不同 Work Item 可以。
- [x] 程序中斷回收為 INTERRUPTED 並退回 RUNNING，不產生假 PASS 或假 FAIL。
- [x] 新 commit 後舊 PASS 標示為 stale 且不觸發任何 transition；純讀指令不寫入 state。
- [x] v1 state 被拒讀並指示 migrate；`migrate` 備份、重複執行安全、備份已存在時拒絕、升級不遺失資料。
- [x] `make verify` 與 `go test -race ./...` 通過，記錄實際環境與命令。
- [x] README 與 architecture 反映實際行為，未實作事項仍清楚標示。
- [x] 無未 review 即 DONE 的產品路徑。

### M3

開工前定案事項已全部完成，記錄於 [architecture.md](architecture.md#m3-開工前定案已完成) 與 `docs/adr/0005`–`0008`。實作已完成，切片與驗收見 [specs/m3-human-gate-and-review/](specs/m3-human-gate-and-review/) 底下的七張 ticket。

其中兩項原本列為必須定案的問題是被消滅而非回答：移除 `WAITING_HUMAN` 之後不存在「恢復規則」，移除 Work Item 的 `BLOCKED` 之後不存在「BLOCKED recovery」。

M3 新增九個指令：

| 指令 | 輸入與成功結果 |
|---|---|
| `forgepilot gate open --work <id> --question <text> --option <text> --option <text> [--reason <text>]` | 配發 Gate ID，該工作即被阻擋；至少兩個 `--option` |
| `forgepilot gate resolve <gate-id> --option <text> [--note <text>] [--by <identity>]` | 保存 Decision、自述決策者與時間；Gate 轉為 RESOLVED |
| `forgepilot gate cancel <gate-id> --reason <text> [--by <identity>]` | Gate 轉為 CANCELLED 並解除阻擋 |
| `forgepilot review approve <work-id> [--note <text>] [--by <identity>]` | append APPROVED Evidence；條件滿足則同交易進入 DONE 並解鎖下游 |
| `forgepilot review reject <work-id> --reason <text> [--by <identity>]` | append REJECTED Evidence，工作退回 RUNNING |
| `forgepilot goal block <goal-id> --reason <text>` / `unblock <goal-id>` | 切換 Goal 的 ACTIVE／BLOCKED |
| `forgepilot goal complete <goal-id>` | 全部 Work Item 皆 DONE 時才允許；人手動宣告 |
| `forgepilot goal cancel <goal-id> --reason <text>` | Goal 轉為 CANCELLED |

`status` 擴充為顯示每件工作的 OPEN Gate 數量、最新一筆 Human Review，以及 approve 之後未進入 DONE 時的原因。不提供 `done`、`complete <work-id>` 或任何等價指令（[ADR-0008](adr/0008-approval-completes-work.md)），不提供 Gate 查詢指令，不提供 `--json`。

Schema 升至 v3：新增 Gate 集合與其 ID 配發計數，Evidence 加入 review 專用欄位。沿用 M2 的升級契約——不自動升級，由 `migrate` 備份為 `state.json.v2.bak` 後升級，備份已存在時拒絕。

交付切片依序為：schema v3 與 v2→v3 升級；Gate 的開啟、解除、取消與阻擋語意；Human Review Evidence 與 reject；approve 至 DONE 與依賴原子解鎖；Goal lifecycle；`status` 擴充與端到端驗收。

驗收：未解 Gate 不可推進；多 Gate 需全部關閉才解除；cancel 解除阻擋且留下理由；resolve 只接受列出的選項；不同 revision 的 PASS 與 APPROVED 不可組合；同一 revision 上較新的 FAIL 或 REJECTED 勝過較舊的 PASS 或 APPROVED；未 review 不可 DONE；approve 後依賴在同一交易內解鎖；DONE 無任何 reopen 路徑；DONE 工作不標示 stale；Goal 非 ACTIVE 時活躍工作維持原狀但無法推進，而進行中的 Verification Run 跑完仍記錄 Evidence；`goal complete` 在尚有非 DONE 工作時被拒。另完成真實的 A → verify → approve → DONE → B READY 端到端流程，這是 M1 分段驗收時延後至此的項目。

### M3 Exit checklist

- [x] 未解除 Gate 阻擋 `start` 與 `verify`，且不被 `next` 選中；開關 Gate 不改變 Work Item 的狀態。卡在 VERIFYING 的孤兒不再是繞過 Gate 的路徑（`eb2f7fd`）。
- [x] 一件工作可同時掛多個 Gate，全部關閉才解除阻擋；`resolve` 只接受列出的選項，`cancel` 要求理由並在 `status` 中可見。
- [x] Gate 進入 RESOLVED 或 CANCELLED 後不可再變更；決策連同自述決策者與時間保存，身分明確標示為聲明而非認證。
- [x] Human Review 綁定完整 commit SHA，與 Verification 共用同一容器與 ID 序列；REJECTED 要求理由並退回 RUNNING；髒工作樹被拒。
- [x] 未驗證或未審查不可 DONE；不同 revision 的 PASS 與 APPROVED 不可組合；同一 revision 上較新的 FAIL 或 REJECTED 勝過較舊的 PASS 或 APPROVED。
- [x] 有未解除 Gate 或 Goal 非 ACTIVE 時不完成；approve 與 PASS revision 不符時仍記錄審查、不完成，且 `status` 說明原因——包括最後一個阻擋在 approve 之後才被解除、條件已全部成立卻仍停在 REVIEW 的情況（`eb2f7fd`）。
- [x] 完成後全部依賴皆 DONE 的下游在同一交易內轉為 READY，仍有其他未完成依賴者不被解鎖；新 process 讀回不存在中間狀態。
- [x] 產品中不存在完成指令、reopen、Gate 查詢指令、`--json` 或身分認證；DONE 工作不標示 stale 但仍顯示完成時的 revision。
- [x] Goal 非 ACTIVE 時活躍工作維持原狀但無法推進，進行中的 Verification Run 跑完仍記錄 Evidence；`goal complete` 在尚有非 DONE 工作時被拒。
- [x] v2 state 被拒讀並指示 migrate；`migrate` 逐版升級、備份、重複執行安全、備份已存在時拒絕、升級不遺失資料。
- [x] 真實的 A start → verify → approve → DONE → B READY 端到端流程以獨立 process 跑通，同一流程涵蓋 Gate 的阻擋與解除。
- [x] `make verify` 與 `go test -race -count=1 ./...` 於 macOS 26.5.1 arm64、go1.25.5 實跑通過，commit `34a2536`，工作目錄乾淨。
- [x] README、architecture 與 CONTEXT 反映實際行為，PR review target 仍清楚標示為未實作。

### M4

開工前定案事項已全部完成，記錄於 [architecture.md](architecture.md#m4-開工前定案已完成) 與 `docs/adr/0010`–`0011`。實作規格見 [specs/m4-pr-exact-head-review/](specs/m4-pr-exact-head-review/)。

原本列為必須定案的「PR metadata 來源、授權與 read-only integration 邊界」三問，答案是同一個：不從外部取得。ForgePilot 不主動發出網路請求（[ADR-0010](adr/0010-no-outbound-network-requests.md)），PR Reference 是使用者輸入的識別字串，因此沒有來源可談、沒有授權要處理，也沒有 integration 邊界要劃。

Evidence 包含 repository、PR number 與 exact HEAD SHA 的硬約束由 PR Reference（`owner/name#number`）與既有的完整 commit SHA 欄位共同滿足。「HEAD 改變即為新 target」由既有的 SHA 比對成立，PR 不參與判定（[ADR-0011](adr/0011-pr-identity-does-not-gate-completion.md)）。不加入 automatic merge 或 release。

M4 不新增指令，只擴充兩個既有指令：

| 指令 | 輸入與成功結果 |
|---|---|
| `forgepilot review approve <work-id> [--pr <owner/name#number>] [--note <text>] [--by <identity>]` | 同 M3，Evidence 額外記錄 PR Reference |
| `forgepilot review reject <work-id> --reason <text> [--pr <owner/name#number>] [--by <identity>]` | 同 M3，Evidence 額外記錄 PR Reference |

`--pr` 為選填。格式非法時拒絕整個指令，不 append 任何 Evidence。verification Evidence 不得攜帶 PR Reference。`status` 顯示最新一筆 Human Review 時一併顯示其 PR Reference（若有）。不提供 PR 查詢指令、不提供 `--json`、不提供任何會發出網路請求的能力。

Schema 升至 v4：Evidence 加入選填的 `pr`。沿用既有升級契約——不自動升級，由 `migrate` 備份為 `state.json.v3.bak` 後升級，備份已存在時拒絕；即使升級步驟不動資料，儀式照走。

交付切片依序為：schema v4 與 v3→v4 升級；PR Reference 的格式規則與 Evidence 驗證；`review approve`／`reject` 的 `--pr`；`status` 呈現與端到端驗收。

驗收：在 PR 的 HEAD 上 verify PASS 後 `review approve --pr` 進入 DONE，Evidence 同時保存 PR Reference 與完整 SHA；新增 commit 使 HEAD 改變後，舊的 PASS／APPROVED 保留為歷史但不套用，必須重新 verify 與重新 approve，新的那筆記的是新的 HEAD；`--pr` 格式非法時整個指令被拒且不寫入任何 Evidence；verification Evidence 帶 PR Reference 時被 validation 拒讀；v3 state 被拒讀並指示升級，升級後資料完整。

### M4 Exit checklist

- [x] Evidence 新增選填的 `pr` 欄位，形式限定為 `owner/name#number`，每段以英數開頭、number 拒絕 0 與前導零、總長上限 255；URL、換行夾帶、`../..` 與其他寫法一律被拒。
- [x] `review approve` 與 `review reject` 皆接受選填的 `--pr`；不帶時行為與 M3 完全相同。
- [x] `--pr` 帶了空值或格式非法時整個指令以非零 exit code 失敗，重新讀回 state 時 Evidence 未增長、Work Item 未移動。
- [x] 格式規則位於 `internal/work`，`validateEvidence` 對載入的 state 一併把關；手改的 `state.json` 夾帶非法值會被拒讀。
- [x] verification Evidence 攜帶 PR Reference 時被驗證拒絕，與既有的 reviewer／note 分流同一套規則。
- [x] 帶 PR 與不帶 PR 的完成判定與 stale 判定結果逐項相同；同一 revision 上標不同 PR 的兩筆 review 仍互相取代（ADR-0011）。
- [x] 產品不發出任何網路請求，不讀 token，不查證 PR 是否存在（ADR-0010）。
- [x] `status` 顯示最新一筆 Human Review 的 PR Reference；沒有 PR 時不印任何相關字樣、不帶提示語氣。
- [x] 端到端流程以獨立 process 跑通：PR HEAD 上 verify PASS → `review approve --pr` → DONE → 解鎖下游；HEAD 改變後舊的 PASS／APPROVED 保留為歷史但不適用，重新 verify 與 approve 後才完成，新的兩筆記的是新的 HEAD。
- [x] v3 state 被拒讀並指示 migrate；`migrate` 備份為 `state.json.v3.bak` 後升級、重複執行安全、備份已存在時拒絕、升級不遺失 Goal、Work Item、Evidence 或 Gate。
- [x] 兩處版本號 fixture 調到 v4。
- [x] README、architecture 與 CONTEXT 反映實際行為。
- [x] `make verify` 與 `go test -race -count=1 ./...` 於 macOS 26.5.1 arm64、go1.25.5 實跑通過，commit `2fc9b92` 之後的工作樹，工作目錄乾淨。

## 每階段交付格式

開發者完成後提供：

1. **Implementation Summary**：實際完成的行為與 milestone。
2. **Architecture Decisions**：本階段定案事項與理由。
3. **Files Changed**：實際變更檔案。
4. **Verification Result**：執行命令、PASS／FAIL／not run 與原因。
5. **Deferred Work**：仍未實作的後續功能。
6. **Risks / Open Questions**：未解風險、操作限制與下一階段前置決策。

只有 acceptance criteria 與 required checks 實際通過才能宣告 milestone 完成；文件或測試 fixture 不代表產品能力已實作。
