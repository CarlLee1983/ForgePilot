# Development plan

## 計畫狀態

M1–M5、P0-001 Candidate Snapshot、P0-002 Work Item Status Summary、P0-003 Actionable Next、P1-004 Deterministic Runtime Resolution 與 Goal-level Review Policy 已完成。本文件供後續開發拆分工作、驗收與交接；產品規則見 [architecture.md](architecture.md)。

開發時如採用 PraxisBound，工程 requirements 與 acceptance criteria 由正式 Story 承載，Work Item 只 reference Story。本文件不另定 Story schema，也不自動產生 Story。

Dogfood Goal FP-28 的導入方向是 prompt-first、fixed-source-version 本機建置：Agent 先以
inspection-only commands 檢查與展示，逐條顯示完整 commit SHA、commands、路徑與效果；使用者
授權後才能取得 source、安裝／建置、執行 `make verify` 或切換 entrypoint。既有 Go 是正式前提，
source-built CLI 通過啟動檢查後才進入 Story 人工檢閱；預設 `WORK_ITEM` Goal／Work Item 建立前
必須另取 repository write 的明確授權。ADR-0033 已定案 Bootstrap，實作目前只有 `status`、
`generation-v1 current` 與 `retention-v1` 開發切片，尚不能安裝 ForgePilot：完成後會同版安裝 CLI 與 Codex skill、但不取得
Repository Onboarding 權限；完整 contract 在
[docs/specs/source-built-bootstrap.md](specs/source-built-bootstrap.md)。unsigned binary 只可作 maintainer
trial，不宣稱正式導入或 macOS execution trust。Apple signing、notarization 與 no-Go prebuilt release
留作未來獨立工作；具體邊界見 [architecture.md](architecture.md#distribution-and-onboarding-boundary)、
ADR-0024、ADR-0025、ADR-0030 與 ADR-0033。

## Milestones

| 階段 | 範圍 | Exit criteria |
|---|---|---|
| M1 — Durable Engineering Queue | 六個 CLI 指令、Goal／Work Item、依賴、basic transition、local JSON | 可選取、開始工作，且跨 process 保存一致 |
| M2 — Verification Evidence | Revision resolver、`make verify` runner、PASS／FAIL Evidence、stale detection | Evidence 綁定確切受測 revision，新 revision 不沿用舊 PASS |
| M3 — Human Gate & Review | Gate、Decision、Human Review、DONE 與依賴解鎖 | 合法完成 A 後 B 可執行；未 review 不可 DONE |
| M4 — PR exact-head review integration | PR number＋HEAD SHA review target | 新 HEAD 必須重新取得適用 review Evidence |
| M5 — Verification diagnosability & review clarity | review 拒絕訊息說出當下動作、verification 輸出串流成 state 之外的 log | 非 PASS 執行的輸出可從落地 log 重讀；review 與 verify 的髒工作樹拒絕訊息各自說出正在做的事 |
| P0-001 — Working Tree Candidate Snapshot | `verify --snapshot`、Candidate identity、snapshot-aware review／stale | 不製造 WIP commit 也能讓 Verification 與 Human Review 判斷完全相同的 immutable candidate |
| P0-002 — Work Item Status Summary | `status --work <id> --summary`、單一 Work Item current-state projection | Human／Agent 不必解析完整 history 就能取得驗證、review、Gate、Goal 與完成狀態 |
| P0-003 — Actionable Next | `next` 的 agent-action projection、RUNNING／stale REVIEW priority、human-only waiting | 新 Agent 可由一個純讀查詢得知下一個合法動作與原因 |
| P1-004 — Deterministic Runtime Resolution | candidate-local runtime discovery、installed toolchain resolution、runtime Evidence | `make verify` 使用 Candidate 宣告且已驗證的 runtime，不受 caller PATH 的錯誤預設版本影響 |

M1 通過後才開始 M2，依此類推至 M5；M1–M4 已完成，M5 開工前定案完成後才開始實作。各階段不得提前加入 database、Web UI、daemon、scheduler framework、agent runtime、plugin framework 或 network API。

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

M1 採 macOS 本機檔案系統與 OS `flock` 程序鎖，Go 1.25.5，module `github.com/CarlLee1983/ForgePilot`。不需要為 M1 預先解決 M2／M3 的全部開放問題。

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
| `forgepilot goal create --id <id> --title <title> [--description <text>] [--review-policy <work-item\|goal>]` | 建立 ACTIVE Goal；GOAL policy 依 current verification 與 Gate 條件自動完成；repository 綁定 state root；省略 description 時保存空字串 |
| `forgepilot work add --goal <goal-id> --story <path> [--depends-on <work-id>]` | 驗證後配發 Work Item ID，回傳 ID、Story 與 PENDING／READY；多個依賴可重複傳 flag；story 尚未提交時多印一行提示 |
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
| `forgepilot verify <work-id>` | 於隔離 worktree 執行受管理專案的 `make verify`，append Evidence；WORK_ITEM PASS／FAIL → RUNNING，GOAL PASS → VERIFIED；送審見 ADR-0023 |
| `forgepilot migrate` | 備份後將 v1 state 升級為 v2；已是 v2 時回報並成功結束 |

`status` 擴充為顯示每件工作最新一筆 Evidence 的 result 與 SHA，以及是否 stale。不提供 `evidence` 查詢指令，不提供 `--json`。

`verify` 的前置條件：Goal 為 ACTIVE、Work Item 為 RUNNING 或 REVIEW、repo 有 HEAD、工作樹乾淨、受管理專案有 `make verify` target。對同一 SHA 重跑不受限制。開頭交易先回收孤兒 VERIFYING。

交付切片依序為：schema v2 與 `migrate`；`internal/repository` 的 revision resolver 與 worktree 隔離；canonical runner 與 Evidence append；`verify` 指令與 transition；`status` 的 stale 呈現。

Integration fixture 加入 Makefile 與真實 commit。驗收：WORK_ITEM PASS／FAIL → RUNNING、明確 `review request` 才 → REVIEW、新 commit 不沿用舊 PASS、髒工作樹被拒、缺少 `make verify` 被拒且不留 Evidence、程序中斷回收為 INTERRUPTED 且不產生假 PASS、隔離 worktree 看不到主樹未提交內容、`git worktree` 殘骸被 prune 清除。另以既有 M1 state fixture 驗證 v1 被拒讀、`migrate` 的備份與重複執行安全，以及備份檔已存在時的拒絕。M2 仍不提供 DONE。

### M2 Exit checklist

- [x] `verify` 與 `migrate` 可穩定執行，且 scope 未越過 M2。
- [x] WORK_ITEM PASS／FAIL → RUNNING，只有明確 `review request` → REVIEW；Evidence 綁定完整 commit SHA 且只累積不覆寫。
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

### M5

開工前定案事項記錄於 [architecture.md](architecture.md#m5-開工前定案已完成) 與 [ADR-0012](adr/0012-verification-log-outside-state.md)（verification log）；review 拒絕訊息的修正沒有架構層級的取捨，決定直接記在下方。

M5 收斂兩件獨立的工作，同屬 M5 但彼此不共用程式碼：

1. **review 的乾淨檢查訊息說出當下在做的事**（GitHub issue #1）：`internal/repository` 的 `EnsureClean` 增加一個由呼叫端提供的動作字串（`verifying` / `reviewing`），訊息模板仍只有一份，留在 `EnsureClean` 內。判準不變——未追蹤檔案仍算髒。
2. **verification 輸出串流成 state 之外的 log**（GitHub issue #2）：理由與備選見 [ADR-0012](adr/0012-verification-log-outside-state.md)。

M5 不新增指令、不新增 flag。

M5 CLI 契約更動：

| 既有指令 | 契約變化 |
|---|---|
| `forgepilot verify <work-id>` | 執行前新增一行輸出：log 路徑（`.forgepilot/logs/<work-id>-<short-sha>-<started-at>.log`）。**移除**：非 PASS 時不再把全文印進 stdout——這是刻意的行為移除，不是退化。結果行不重複路徑；孤兒回收（`reclaimOrphan`）報告 INTERRUPTED 時附上該次執行的 log 路徑 |
| `forgepilot verify <work-id>`（拒絕時） | 髒工作樹拒絕訊息維持說「verifying」，格式不變（列出使工作樹變髒的檔案） |
| `forgepilot review approve` / `review reject` | 髒工作樹拒絕訊息改說「reviewing」；拒絕清單格式與 verify 路徑一致；判準不變，仍拒絕未追蹤檔案 |

`status` 與 `next` 不變；不提供讀取 log 的新指令，路徑印出來即可用既有工具讀取。

Schema 升至 v5：`current_run` 新增 `log path`。沿用既有升級契約——不自動升級，由 `migrate` 備份為 `state.json.v4.bak` 後升級，備份已存在時拒絕。

交付切片依序為：schema v5 與 v4→v5 升級；`EnsureClean` 的動作參數與呼叫端更新（review 訊息修正，可獨立先交付）；canonical runner 改為串流寫檔；`verify` 的呈現契約更動；孤兒回收訊息更新；端到端驗收。

### M5 驗收矩陣

**review 的乾淨檢查訊息（#1）**

| 驗收 | 對應 issue #1 |
|---|---|
| `review approve` 在工作樹不乾淨時的拒絕訊息含「reviewing」，不含「verifying」 | User story 1 |
| `verify` 在工作樹不乾淨時的拒絕訊息維持含「verifying」 | User story 2 |
| 兩條路徑的拒絕訊息同樣列出使工作樹變髒的檔案 | User story 3 |
| 判準不變：未追蹤檔案仍視為髒；不因訊息修正而放寬 | User story 4、Out of Scope |
| `EnsureClean` 的判準只有一份實作，`internal/repository` 內不分岔 | User story 5 |

**verification 輸出串流成 log（#2）**

| 驗收 | 對應 issue #2 |
|---|---|
| PASS 與 FAIL 皆留下 log，內容含該次執行的完整輸出 | User story 1、5 |
| 執行前印出 log 路徑 | User story 2 |
| 被中斷（INTERRUPTED）的執行留下截斷的 log；回收孤兒時的輸出附上該 log 路徑 | User story 3、4 |
| 非 PASS 時 stdout 不再印全文 | User story 6 |
| 由 Work Item ID 與完整 SHA 的前綴比對可找到對應的 log，不需額外索引 | User story 7 |
| 同一 revision 上重跑多次產生不同檔案，不互相覆蓋 | User story 8 |
| log 檔開不起來時 `verify` 中止並說明，不留 Evidence | User story 9 |
| `.forgepilot/logs/` 不被自動清理，不提供清理指令 | User story 10 |
| `.forgepilot/logs/` 落在既有 `.gitignore` 範圍內 | User story 11 |
| v4 state 被拒讀並指示 `forgepilot migrate` | User story 12 |
| `migrate` 完成 v4→v5 後，既有 Goal 與 Evidence 資料完整 | User story 13 |
| Evidence 上沒有指向 log 的欄位，此決定記錄於 ADR-0012 | User story 14 |
| log 路徑存在 `current_run` 的 `log path`，不以命名規則事後推導 | User story 15 |

### M5 Exit checklist

- [x] `review approve` 與 `review reject` 的髒工作樹拒絕訊息說「reviewing」；`verify` 維持說「verifying」；兩者拒絕清單格式一致；未追蹤檔案仍判定為髒（`integration_test.go` 的 `TestVerifyRecordsEvidenceAgainstTheCommittedRevision`、`TestRefusedVerifyWritesOnlyTheRunThatEnded`、`TestReviewRecordsAJudgementBesideTheVerification`；端到端實跑中 `verify` 於未 commit 的 `.gitignore` 上重現「verifying」訊息，見下方實跑紀錄）。
- [x] `EnsureClean` 只有一份判準與一份訊息模板，動作字串由呼叫端提供（`internal/repository/verification.go` 的 `EnsureClean(root, action string)`）。
- [x] `verify` 執行前印出 log 路徑；非 PASS 時 stdout 不再印全文（刻意移除，非退化）；結果行不重複路徑（`TestVerifyStreamsCanonicalOutputToALog`；端到端實跑中確認 PASS 輸出先印 `Log: ...` 再印結果行，結果行不重複路徑）。
- [x] PASS、FAIL、INTERRUPTED 皆留下對應該次執行的 log；INTERRUPTED 的 log 為截斷內容，回收孤兒時的輸出附上其路徑（`TestVerifyStreamsCanonicalOutputToALog`、`TestInterruptedRunLeavesATruncatedLogThatReclaimReports`、`TestReclaimRunRecordsAnInterruptionWithoutAnExitCode`）。
- [x] 同一 revision 重跑多次的 log 不互相覆蓋；由 Work Item ID 與完整 SHA 前綴比對可找到對應 log（`TestVerifyLogsAccumulateAcrossRepeatedRuns`；端到端實跑中 `ls .forgepilot/logs/` 看到檔名為 `WI-001-<short-sha>-<started-at>.log`）。
- [x] log 目錄開不起來時 `verify` 中止並回報，不留 Evidence（`TestVerifyAbortsWhenTheLogCannotBeCreated`）。
- [x] `.forgepilot/logs/` 不自動清理、不提供清理指令；落在既有 `.gitignore` 範圍內（產品程式碼未提供清理指令；端到端實跑中 `init` 產生的 `.gitignore` 內容為 `.forgepilot/`，涵蓋 `logs/`）。
- [x] Evidence 未新增任何指向 log 的欄位；`current_run` 新增 `log path`（`internal/work/verification_test.go` 的 `TestLogPathSurvivesTheRunAndVanishesWithIt`）。
- [x] v4 state 被拒讀並指示 migrate；`migrate` 備份為 `state.json.v4.bak` 後升級、重複執行安全、備份已存在時拒絕、升級不遺失 Goal、Work Item、Evidence 或 Gate（`internal/work/work_test.go` 的 `TestSchemaVersionErrorsDistinguishOlderFromNewer`；`integration_test.go` 的 `TestMigrateCommandUpgradesLegacyState`；`internal/storage/storage_test.go` 對 `state.json.v4.bak` 的斷言）。
- [x] 兩處版本號 fixture（`internal/storage/storage_test.go`、`internal/work/work_test.go`）調到 v5（`internal/work/work_test.go:104` 斷言 `SchemaVersion` 為 5；`internal/storage/storage_test.go:336` 以 `"schema_version": 5` 建構 fixture）。
- [x] 端到端流程以獨立 process 跑通：PASS 與 FAIL 各自留下對應 log；被 kill 的執行回收為 INTERRUPTED 且輸出附上 log 路徑；同一 revision 重跑兩次產生兩個檔案（PASS 與 stdout 呈現於下方「M5 端到端實跑紀錄」；FAIL、INTERRUPTED 與重跑不覆寫由 `TestVerifyStreamsCanonicalOutputToALog`、`TestInterruptedRunLeavesATruncatedLogThatReclaimReports`、`TestVerifyLogsAccumulateAcrossRepeatedRuns` 覆蓋）。
- [x] `make verify` 與 `go test -race -count=1 ./...` 實跑通過，記錄實際環境與命令（見下方「M5 端到端實跑紀錄」）。
- [x] README、architecture 與 CONTEXT 反映實際行為（`docs/architecture.md` 文件狀態行改為 M1–M5 已實作）。

### M5 端到端實跑紀錄

環境：macOS 26.5.1（`Darwin 25.5.0 arm64`）、`go1.25.5`、commit `8ce2c12`，實跑前 `git status --porcelain` 為空。

- `make verify`：PASS（`gofmt` 檢查、`go vet ./...`、`go test ./...`、CLI build 均成功）。
- `go test -race -count=1 ./...`：PASS，涵蓋根套件與 `internal/repository`、`internal/storage`、`internal/work`，未偵測到 race。

端到端流程於獨立 process、臨時 Git repository 上執行，binary 為本次 commit 建置：

```text
init → goal create g1 → work add WI-001 → next → start WI-001
→ verify（工作樹因未 commit 的 .gitignore 而 dirty，拒絕訊息含「verifying」）
→ commit .gitignore
→ verify（PASS，先印出 Log: <絕對路徑>，log 檔內容為 canonical 檢查完整輸出，結果行 `EV-001 PASS at <sha>` 不重複路徑）
→ review approve WI-001（`EV-002 APPROVED at <sha>`，WI-001 → DONE）
→ status（顯示 WI-001 DONE、EV-001 PASS 與 EV-002 APPROVED，log 路徑呈現未與完成流程互相干擾）
```

log 檔案落在 `.forgepilot/logs/WI-001-<short-sha>-<started-at>.log`，`.gitignore` 內容為 `.forgepilot/`，log 目錄涵蓋在內。

## M5 之後的 dogfood 修補

M5 交付完成後用 ForgePilot 自我駕駛的 dogfood（`docs/specs/m5-dogfood-friction.md`）撞到
的摩擦，拆成獨立的票修補，不併入 M5、不開新 milestone：story 路徑錯誤訊息的改善見
issue #9，`work add` 的提示改善見 issue #10，PraxisBound Story 定義與現況不符、
「是否自我套用 PraxisBound」的未決問題、以及 Evidence 不承諾工作時序這三件事的文件記錄
見 issue #12；issue #11（事後補跑與邊做邊跑在 state 裡分辨不出來）的關閉結論記在
[ADR-0012](adr/0012-verification-log-outside-state.md)。

## P0-001 — Working Tree Candidate Snapshot

CLI 契約：

| 指令 | 輸入與成功結果 |
|---|---|
| `forgepilot verify <work-id>` | 保持既有 clean workspace → HEAD → detached worktree → `make verify`，Evidence candidate kind 為 `COMMIT` |
| `forgepilot verify <work-id> --snapshot` | 從目前 working tree 建立 immutable `SNAPSHOT` candidate，輸出 Candidate、snapshot Revision、Base 與 Log，再執行相同 canonical check |
| `forgepilot review approve/reject <work-id>` | 最新 Verification 為 `COMMIT` 時保持 clean HEAD；為 `SNAPSHOT` 時重算 workspace digest，相同才把 review 綁回已驗證的 snapshot revision，不同則拒絕且不留 Evidence |
| `forgepilot status` | `COMMIT` 以 HEAD、`SNAPSHOT` 以 workspace candidate digest 判斷 stale；DONE 仍不標 stale |

`work add` 發現 Story 尚未提交時仍成功建立 Work Item，並以剛配發的 ID 提示兩條合法路徑：`forgepilot verify <work-id> --snapshot` 驗證 working tree，或先 commit 再執行既有的 commit-mode verification。

Schema 升至 v6：`current_run` 與 Evidence 增加 `candidate_kind`、`base_revision`、`candidate_digest`。`migrate` 將 v5 與更舊版本的既有 revision 明確標成 `COMMIT`，先留下 `state.json.v<n>.bak`；不查 Git、不重寫舊 Evidence 的 revision 或結果。rollback 是還原備份；已建立的 local snapshot refs 可留存。

驗收矩陣：

- Legacy clean-HEAD verification、review、stale 與 completion 行為不變。
- Snapshot 收進 tracked staged／unstaged 修改、同檔 staged＋unstaged 最終內容、tracked deletion、non-ignored untracked；ignored untracked 排除。
- capture 前後 branch、HEAD、real index、staging diff、unstaged diff、working files 相同；canonical check 在 snapshot detached worktree 執行，Evidence revision 等於該 checkout。
- snapshot ref 在命令結束後仍可解析及 checkout，不建立 branch／tag、不發網路請求。
- 同一 workspace fresh；修改、新增、刪除使 digest 改變並呈現 stale；review 拒絕變更後的 workspace，re-verify 新 snapshot 後可 review。
- PASS＋APPROVED 綁同一 snapshot revision 時仍由既有 completion invariant 進 DONE。
- 同 Work Item 的 snapshot verification 仍由既有 flock serialization；中斷後 append `INTERRUPTED` 並保存原 run candidate。
- v5 migration 保存 Goal、Work Item、Evidence、Gate 與 in-flight run，並將舊 candidate 明確標成 `COMMIT`。

Snapshot retention／GC、cloud／GitHub integration、network request、自動 commit／PR、PraxisBound readiness 與其他 CLI 擴充不在 P0-001。

## P0-002 — Work Item Status Summary

CLI 契約：

| 指令 | 輸入與成功結果 |
|---|---|
| `forgepilot status` | 保持既有完整 Goal／Work Item／Evidence／Gate history 輸出，不接受單獨的 filter。 |
| `forgepilot status --work <work-id> --summary` | 固定輸出該 Work Item 的 status、Goal 與 Story、latest Verification、latest Human Review、未解除 Gate IDs 與 completion projection。不存在的 ID 回傳 `unknown work item "<id>"`。 |

Summary 是 read-only presentation projection，不寫入 `state.json`，也不新增 lifecycle state。Verification 必須沿用既有 Candidate 規則：COMMIT 以 HEAD 比較、SNAPSHOT 以 workspace digest 比較，DONE 不標 stale；已完成 Goal 的 VERIFIED Work Item 顯示 `goal completed`，且不因 repository 後續移動回報為 Goal blocked。Review 只選最新 Human Review；Gate 只列 `OPEN`，`RESOLVED`／`CANCELLED` 不列入 blocker。`Completion:` 的 base projection 只會是 `not started`、`implementing`、`verification required`、`verification failed`、`verification stale`、`awaiting human review`、`changes requested`、`blocked by gate`、`goal blocked`、`goal completed` 或 `done`。若 APPROVED 之後才因 Gate／Goal 解除或新的 matching PASS 而滿足所有完成條件，`awaiting human review` 固定加上 ` (re-approve to complete)` action suffix；這是既有完成規則的呈現，不是新 transition。

驗收：原有 `status` 輸出保持不變；READY／RUNNING、PASS／FAIL／stale／snapshot、APPROVED／REJECTED、單一／多個 unresolved Gate、BLOCKED／COMPLETED Goal 與 DONE 都有 projection coverage；遺漏或不完整 flags 是 usage error。

## P0-003 — Actionable Next

`forgepilot next` 從「最早 READY Work Item」擴充為 read-only 的 agent-action projection。它依序選擇：可推進的 RUNNING Work Item（最新 Verification 為 FAIL 時建議修復）、可推進且 candidate stale 的 REVIEW Work Item（建議重驗）、以及既有的最早 READY Work Item（建議 `start`）。READY 的 Goal／dependency／Gate 條件與 created-at／numeric-ID 排序仍完全委派既有 selection rule。

沒有 agent 可做的工作時，fresh PASS 的 REVIEW 回報需要 Human Review；OPEN Gate 回報最早的未解除 Gate；非 ACTIVE Goal 回報 Goal 狀態。這些等待不會遮蔽其他獨立的 RUNNING、stale REVIEW 或 READY 工作。PENDING dependency 與 in-flight VERIFYING 不是 human-only recommendation。所有 action kind 都是 read-only projection，不新增 Work Item status、不寫入 `state.json`、不 claim、start、verify、resolve Gate 或建立 Agent session。

Candidate freshness 沿用 P0-001：COMMIT 比較目前 HEAD，SNAPSHOT 比較目前 workspace digest。若 stale SNAPSHOT 被選中，建議的命令是 `forgepilot verify <work-id> --snapshot`，讓建議本身也是可合法執行的下一步。

驗收：RUNNING 優先於 READY，multiple RUNNING 以 created-at／numeric ID 穩定排序，FAIL 建議 repair，commit 與 snapshot stale REVIEW 都建議 reverify，READY 保留既有排序，Gate／Goal／dependency blocker 不被當成可執行工作，human-only blocker 有明確原因，empty／all DONE 明確無工作；重複 `next` 輸出相同且 state 不變。

## P1-004 — Deterministic Runtime Resolution

`verify` 在 COMMIT／SNAPSHOT 共用的 detached checkout 內解析 Runtime Contract，再建立單一、只供該 subprocess 使用的 Resolved Runtime。支援來源與 precedence 為 `mise.toml` → `.tool-versions` → language-specific version file → ecosystem manifest：Node 的後兩層依序為 `.node-version`、`.nvmrc`、`package.json engines.node`；Go 為 `go.mod`（`toolchain` 優先於 `go` minimum）；Python 為 `.python-version`；Rust 為 `rust-toolchain.toml`、`rust-toolchain`。相容的多來源採最高 precedence，互斥的 exact declarations 拒絕而不猜測；所有已宣告 constraint 都必須由實際 executable 滿足。

resolver 只使用 caller PATH 或 mise／asdf／nvm／pyenv／rustup 的本機既有 installation／shim；選定後在 temporary bin directory 把每個 runtime command 綁到已驗證 executable，再建立 child-process PATH，避免多個 manager bin directory 互相遮蔽。不執行安裝、不 source shell profile、不修改 runtime files 或使用者全域版本，temporary directory 隨命令清理。沒有支援的 declaration 時維持原本 inherited PATH。宣告存在但沒有符合版本時，在 `current_run`、log 與結果 Evidence 建立前拒絕；因此不會留下假的 FAIL。

Schema 升至 v7：`current_run` 與 Verification Evidence 新增選填 `runtime` map，保存 resolver 實際驗證的版本；INTERRUPTED 從原 run 保留同一 metadata。v6→v7 migration 不推測舊執行環境，舊 Evidence 沒有 `runtime` 仍合法。Human Review Evidence 不攜帶 runtime。

驗收：無 declaration 維持舊流程；全部支援的 declaration source、相同／不可用版本、conflict、multiple runtimes 均有 repository tests；integration tests 證明錯誤 shell default 不進 canonical check、runtime failure 不留 FAIL Evidence、COMMIT 與 SNAPSHOT 都從 detached checkout 解析，且 Evidence 記錄 actual runtime。完整 gates 為 `make verify` 與 `go test -race -count=1 ./...`。

## Goal-level Review Policy

Goal 在建立時可持久化 `--review-policy work-item|goal`；`work-item` 是預設，完全保留既有 per-Work-Item Human Review 與 `review approve` → DONE。`goal` 不跳過 machine verification：PASS 令 Work Item 成為 `VERIFIED`，讓它在 Gate 已解除、Goal ACTIVE、Candidate fresh 的前提下滿足依賴；prerequisite 重驗或新開 OPEN Gate 會令受影響的 READY downstream 回到 PENDING，fresh PASS 或 Gate closure 後再 READY，保持 stored READY 與同一 progression predicate 一致。所有可能 promotion 的 domain API 都必須接收 CLI 在 `storage.Update` callback 內解析出的 current repository facts；缺少 facts 時 fail closed、保留 PENDING。refresh 僅限直接 dependents 或被 unblock 的 Goal，不可使無關 Goal 倒退。Goal BLOCKED 則維持既有 Work Item status；FAIL、INTERRUPTED 與 stale candidate 的語意不變。

`status` 顯示 Goal policy 與每件工作的 policy-aware projection；GOAL-policy Work Item 的 Human Review 顯示不適用。`next` 會重新驗證 stale VERIFIED Work Item，所有條件都已滿足時產生 typed `COMPLETE_GOAL` action，不等待 Goal-level Human Review。completion readiness 是 fail-closed pure projection：ACTIVE Goal 必須非空、全部 Work Item VERIFIED、每筆 latest Verification 是仍匹配目前 Candidate 的 PASS、且無 OPEN Gate；projection 保留所用 Verification Evidence IDs。自動 action 由 application 的單一 transaction 再次檢查 exact Evidence set，追加 Goal completion evidence 後完成 Goal；Work Item 保持 VERIFIED。

Schema v11 加入由 Review Policy 推導的 `completion_policy`、Goal completion evidence 與 counter；schema v12 移除 GOAL 的人工終審選項，並將舊 v11 GOAL/HUMAN 正規化為 VERIFIED。Migration 保留原 lifecycle status：原已 COMPLETED 的 Goal 在同一 state transaction 帶上獨立的 v11/HUMAN legacy provenance，不能當成 current Candidate／Verification aggregate proof；原未完成 Goal 不會被 migration 完成。完成的 GOAL/VERIFIED 必須恰有一種 provenance（automatic aggregate 或 legacy marker）。先備份 `state.json.v<n>.bak`；不提供 downgrade，rollback 是手動還原備份。v12 尚未發布，optional marker 在 v12 內加入；舊 v12 binary strict-decode 帶 marker 的 state 會拒絕讀取。CLI 不接受 `--completion-policy`；`goal complete` 對 GOAL 一律拒絕，新的完成只走帶 current Candidate facts 的 typed transaction。詳見 [ADR-0037](adr/0037-goal-completion-has-no-human-final-review.md)。

v11 HUMAN migration acceptance：`TestMigrateV11PreservesCompletedHumanGoalWithLegacyProvenance` 驗證原 status、時間與備份不變、無 GC/Candidate/Verification claims，且讀取不改寫；`TestLegacyGoalCompletionPreservesTerminalStatusWithoutClaimingVerification` 驗證 marker 的限制與 XOR；application 與 Runner recovery tests 驗證 marker 不會變成自動完成冪等結果，也不能清除未解決的 Candidate facts read。

驗收：預設及 migration 都維持 WORK_ITEM compatibility；GOAL PASS → VERIFIED 並僅作 progression；READY 在 prerequisite 重驗或 OPEN Gate 新增時回到 PENDING，fresh PASS／Gate closure 後再 READY，而 Goal BLOCKED 保持 Work Item status；Gate／Goal／failure／interruption／freshness 不可被繞過；Work Item review 和 direct `goal complete` 在 GOAL policy 都被拒；readiness 對 inactive、empty、stale、non-PASS 或 OPEN Gate fail closed，並保留 exact Verification Evidence IDs。

## Goal Plan preflight — FP-52

| 指令 | 輸入與成功結果 |
|---|---|
| `forgepilot goal preflight --request <path> --json` | 讀取 repository-relative JSON request、Goal Plan Manifest、Plan Coverage Review，以及 Manifest 綁定的 declaration、sources 與 readiness bytes；輸出 `forgepilot.goal-preflight/v1` projection、綁定事實、完整 DAG／Work Item 對應與診斷，不寫入 state 或啟動 subprocess |

Request 使用 `forgepilot.goal-preflight-request/v1`，必須明確提供 `goalId`、`manifestPath`、`coverageReviewPath`，以及 `nodeMappings` 的 Plan Node Reference/Work Item ID 對應。來源、declaration 與 readiness paths／digests 直接依 PraxisBound Manifest 驗證；declaration 按 v1 schema 驗證（最多 1 MiB、JSON depth 32），其 plan identity 與 DAG 必須符合 Manifest。Review 必須綁定 Manifest 原始 bytes、相同來源與 coverage-index identity，且明確聲明 `approved`。所有路徑都必須留在 repository 內且不得經過 symlink；缺少或多出的欄位、重複 JSON key、無效 UTF-8 與不正確的 artifact binding 都以 fail-closed JSON 診斷回報。每項 fact 明確標示 `observed`、`unprobed` 或 `unavailable`。SHA-256 以原始檔案 bytes 計算；preflight 不解析 Story Markdown、不推論需求覆蓋，也不查詢 Git、runtime、程序存活或 next action。詳見 README 的 Goal Plan preflight 使用範例與 FP-52 acceptance。

## 變更面驗證矩陣

先讀 Story／acceptance／release contract：其中明定的 checks 一律優先。未指定時，依下表選擇能直接觀察變更的最小檢查；full gate 是 integration、release、Human final acceptance，或變更本身觸及其組成時的必要條件，而不是所有文字修改的預設。

| 變更面 | 每次變更的檢查 | 升格為 full gate 的條件 |
|---|---|---|
| 非執行文件（Markdown、README、一般 HTML／CSS） | `git diff --check`；核對已改引用、指令與相對 `href`／`src`；HTML／CSS 於本機瀏覽器開啟已改頁面，確認版面與已改連結可用 | Story／acceptance 明定、release／整合交付，或同次改動也觸及其他列 |
| `docs/diagrams/` 的圖規格與產物 | 依 [圖的重新產生程序](diagrams/README.md#怎麼重新產生) render 與 visual-check | 同上 |
| Go、module metadata、Makefile 或 canonical verification 行為 | `make verify` | integration／Human final acceptance 時另跑 `go test -race -count=1 ./...`；Story 也可明定 race gate |
| `scripts/forgepilot-bootstrap` 與其 test | `sh -n scripts/forgepilot-bootstrap`、`sh scripts/forgepilot-bootstrap_test.sh` | FP-61 integration/final acceptance 另依 Story 執行原生 Apple Silicon disposable-home acceptance；不因純 shell 變更重跑 Go race gate |
| `scripts/release/`、`.github/workflows/`、`scripts/onboarding/` 或 `scripts/skills/` | 分別跑 `sh scripts/release/build_trial_assets_test.sh`、`sh scripts/release/publish_trial_assets_workflow_test.sh`、`sh scripts/onboarding/onboarding_test.sh`、`sh scripts/skills/check_adapters_test.sh` 與／或 `sh scripts/skills/short_prompt_regression_test.sh` 中受影響者 | release、跨面整合，或同次變更碰到 Go／Makefile 時跑 `make verify`；final acceptance 另跑 race gate |

報告每一項實跑命令、結果，以及沒有跑的 full gate 與理由。不得把未跑的必要 check 寫成 PASS；若必需 check 被阻擋，交付仍是 partial。這份矩陣不改變 ForgePilot 對受管理 repository 的 canonical `make verify` contract。

## 每階段交付格式

開發者完成後提供：

1. **Implementation Summary**：實際完成的行為與 milestone。
2. **Architecture Decisions**：本階段定案事項與理由。
3. **Files Changed**：實際變更檔案。
4. **Verification Result**：執行命令、PASS／FAIL／not run 與原因。
5. **Deferred Work**：仍未實作的後續功能。
6. **Risks / Open Questions**：未解風險、操作限制與下一階段前置決策。

只有 acceptance criteria 與 required checks 實際通過才能宣告 milestone 完成；文件或測試 fixture 不代表產品能力已實作。

## Readiness Recovery 與 GOAL-policy 重驗排序

`forgepilot reconcile --goal <goal-id>` 是新增的唯一寫入指令。

| 指令 | 輸入與成功結果 |
|---|---|
| `forgepilot reconcile --goal <goal-id>` | 依目前 repository facts 重算該 Goal 的 PENDING／READY readiness。有變更時輸出 `Goal <id> reconciled` 與每筆 `WI-00n PENDING -> READY`；沒有變更時輸出 `Goal <id> readiness unchanged`。 |

Readiness 是既有的持久化欄位，這個指令只把它重新對齊可計算的 projection，不新增 durable state、不升 schema。允許的移動只有 `PENDING → READY` 與 `READY → PENDING`；RUNNING、VERIFYING、REVIEW、VERIFIED、DONE 一律不動，Verification Evidence、Review Evidence、Gate 決策與 Review Policy 也一律不動。判準完全沿用既有的 dependency progression predicate，CLI 不另寫一套。Goal 必須存在且為 ACTIVE，BLOCKED／CANCELLED／COMPLETED 都拒絕並說明原因。Goal 的存在與狀態在取 Git facts 之前檢查；facts 在 `storage.Update` 的受鎖 callback 內解析，且只解析這個 Goal 的 PENDING／READY 工作其 prerequisite 實際需要的種類——其他 Goal 的 SNAPSHOT Evidence 不會讓這個 Goal 需要 workspace digest。任何必要 facts 取得失敗即拒絕整個命令，不做部分更新，也不把 unknown 當 fresh。相同 state 與 facts 下重複執行不產生 domain 變更，未改變的項目 `UpdatedAt` 不動。state lock 只序列化 ForgePilot 自己的 state 交易，不是 Git workspace lock。

`next` 新增 `RECONCILE` action，並改為以下順序：合法 RUNNING 的 RESUME／REPAIR；WORK_ITEM 模式既有 stale REVIEW 的 REVERIFY；可合法前進的工作（已 READY 為 START，PENDING 但 readiness 可恢復為 RECONCILE，兩者共用同一個 created-at／numeric-ID 排序）；GOAL-policy stale VERIFIED 的 REVERIFY；最後才是既有等待原因或 `COMPLETE_GOAL`。`next` 與 `reconcile` 共用同一個 `advanceable` 判準，因此不會出現「推薦 reconcile 但 reconcile 一直 unchanged」的空轉；`next` 仍是純查詢，不自行執行 reconciliation。本輪只調整合法動作之間的排序，不改變合法性的標準：READY 不足以推薦 START，prerequisite 的 Gate 與 freshness 仍須成立，且中途延後的重驗必須在 Goal completion action 前補完。

驗收：readiness 由 Gate 與 Candidate 移動退回 PENDING、條件恢復後經 `reconcile` 復原的完整 CLI／Git 流程，且復原過程不新增 Verification Evidence；stale prerequisite、未解除的 prerequisite Gate、工作自身的 Gate、BLOCKED／CANCELLED／COMPLETED Goal、未知 Goal 與 facts 取得失敗都 fail closed 且不留部分更新；reconcile Goal A 不改動 Goal B；三張連續任務各自產生新 commit 時 action sequence 為 START／VERIFY 交錯，原始 P1-005 實作在邊界逐張 REVERIFY、總計 5 次 Verification Run；schema v9 的 accepted fan-out 後改為 3 次 execution，第二與第三次 PASS 分別刷新當時 eligible 的 stale peers；SNAPSHOT 以持續演進的 digest 重做同一流程；多 prerequisite 任一不符即不能 START，但同一 shared PASS 可同時刷新多個已 VERIFIED stale prerequisites；WORK_ITEM policy 的 REVIEW → Human Approval → DONE 與 stale REVIEW 導航不變；`next` 不寫 state、HEAD、real index 或持久化 refs，重複執行結果穩定，`reconcile` 第二次無 domain 變更。

## Long-running Runner MVP

`forgepilot run` 是這一輪新增的執行命令。設計主張與範圍見 [docs/specs/runner-mvp/spec.md](specs/runner-mvp/spec.md)；界線見 [ADR-0018](adr/0018-runner-may-launch-a-local-coding-cli.md)、[ADR-0019](adr/0019-runner-executes-forgepilot-decides.md) 、[ADR-0020](adr/0020-worker-ownership-is-fail-closed.md) 、[ADR-0021](adr/0021-execution-limits-are-bounded-and-named.md) 與 [ADR-0022](adr/0022-pending-cleanup-outlives-the-process.md)。

| 指令 | 輸入與成功結果 |
|---|---|
| `forgepilot run --goal <goal-id> --runtime <codex\|fake> --snapshot` | 對指定 Goal 執行 Runner 迴圈。Goal 必須 ACTIVE、非空且 review policy 為 `GOAL`。`--snapshot` 必須明確傳入。逐步輸出 step、action、Work Item 與結果，結束時輸出 run id 與停止原因 |
| `forgepilot run --goal <goal-id> --runtime <name> --snapshot --dry-run` | 只讀取、檢查並呈現計畫：Goal 條件、runtime executable 與版本、預算設定、目前的第一個合法 action。不呼叫模型、不 start／reconcile／verify、不新增 run record、不建立 snapshot |
| `forgepilot run status <run-id>` | 輸出該 run 當時的停止結果，以及目前重新計算的 Goal readiness，兩者分開呈現 |
| `forgepilot run status <run-id> --json` | 同上，單一 JSON 物件 |
| `forgepilot run resume <run-id>` | 沿用原本的 workspace、Goal、runtime 設定與已消耗預算繼續執行。需要實作時仍建立新 session；不延長 deadline |

`run` 的其餘 flag 與預設值：

```text
--max-steps                 100
--max-attempts-per-work      3
--max-duration              8h
--agent-timeout             30m
--verify-timeout            30m
--runtime-command           （選填）覆寫 runtime executable 路徑，測試用
--max-handoff-bytes       65536
--max-agent-output-bytes 1048576
--max-run-bytes          16777216
--max-runs-bytes        134217728
```

`--max-steps` 計算 start、reconcile、Agent session 與 verification。`--max-attempts-per-work` 計算同一 run、同一 Work Item 的 technical attempts：每個 Agent Session 在啟動前先加入單調的 attempt 序號；session 交回合法且已 checkpoint 的 `needs_human` outcome 後，才另記為 human wait 並從 technical count 扣除。選項不足時不製造 Gate，但仍是 human wait；protocol error、execution failure、中斷與一般 implementation session 都維持計費。Human wait 仍消耗 step、duration 與 artifact budget；舊 run record 沒有 human-wait 欄位時保守不扣除。`--max-duration` 從第一次啟動計算，而且約束整段執行而不只是步驟之間的空隙：一次執行的有效期限是 `min(原始 run deadline, 本次開始時間 + 本次 timeout)`，所以 `--agent-timeout` 與 `--verify-timeout` 都不會讓 run 越過總期限。整個 run 到期記 `MAX_DURATION`，單次執行自己逾時才記 `AGENT_TIMEOUT`／`VERIFY_TIMEOUT`；多個原因幾乎同時到達時先成立的算數，完全同時則 signal → 總期限 → 單次 timeout。期限到期之後不再啟動新的業務工作，但終止本身有一段有界的清理寬限（先 SIGTERM、有限等待、必要時 SIGKILL、最後確認程序群組已空）。獨立的 `forgepilot verify` 不繼承這些期限。詳見 [ADR-0021](adr/0021-execution-limits-are-bounded-and-named.md) 與 [ADR-0026](adr/0026-resolved-gates-cross-agent-session-boundaries.md)。**任何限制都不接受 `0` 或負值**——預算與三個容量上限都在啟動前驗證，三個容量上限還必須由內而外遞增（單次寫入 ≤ 單 run ≤ 全部 runs）。`resume` 沿用 run record 裡的預算與容量上限，不套用命令列預設值。

三個容量上限都是 agent session 產出的上限（console log 與結構化結果），**三個都不套用在 ForgePilot 自己的 run record 上**：run record 的大小已由其結構決定（保留的 attempt 數乘以截斷後的摘要長度，加上每件工作一筆），而寫不出 run record 會讓已啟動的 worker 失去可恢復的紀錄。只豁免單次寫入上限並不夠——總量上限同樣擋得住那次存檔，而它偏偏發生在 workspace 最滿的時候，屆時唯一的出路會變成「從 record 裡拿掉東西讓它寫得下」，那正是讓活著的 worker 從紀錄裡消失的路徑。session 寫的每一個 artifact 仍然受三個上限管轄。

`run` 與 `run resume` 的退出碼：

| 退出碼 | 意義 |
|---|---|
| 0 | Goal 已在單一 transaction 內原子完成 |
| 2 | 因 Gate、Goal 狀態或需要外部處理的條件停止（含 `needs_human`、scope changed、recovery blocked） |
| 3 | 達到預算、timeout 或無進展限制 |
| 1 | 參數、runtime、repository、storage 或其他執行錯誤 |

SIGINT／SIGTERM 停止目前的 worker 程序群組**與正在執行的 canonical check**，保存恢復資訊後以 130／143 退出。`run status` 與 `--dry-run` 的成功退出不代表 Goal 已準備好總檢。既有命令的退出碼與文字輸出契約不變；`run` 不新增 schema 版本，`state.json` 不因 Runner 增加欄位。

執行紀錄落在 `.forgepilot/runs/<run-id>/`，涵蓋在既有 `.forgepilot/` ignore 範圍內，因此不改變 Candidate digest。本版不自動刪除 artifacts；容量不足時拒絕並指出可清理的目錄。

### Runner MVP 驗收矩陣

每一列都對應實際存在且通過的測試。fake subprocess adapter 是產品程式碼，不是測試替身——它遵守與 Codex 相同的 session 契約，所以這些測試驗的是產品路徑。

| 驗收 | 測試 |
|---|---|
| A → B → C 相依工作循序完成、各 attempt 新 session、預設 GOAL policy 自動完成而 Work Item 保持 VERIFIED | `TestRunnerDrivesDependentWorkToGoalCompletion` |
| 多依賴／匯合依賴：必要依賴 stale 時仍先重驗 | `TestConvergingDependenciesPayTheirDeferredReverifications` |
| 同 repository 多個 Goal：只執行指定 Goal，不因全域排序假停滯 | `TestRunnerDrivesOnlyTheNamedGoal`、`internal/work` 的 `TestActionableNextForGoalAnswersOnlyTheNamedGoal`、`TestActionableNextForGoalDoesNotBorrowAnotherGoalsBlocker` |
| 篩 Goal 不影響依賴判斷所需的完整 state | `internal/work` 的 `TestActionableNextForGoalStillJudgesDependenciesAgainstFullState` |
| Agent exit 0、結果不合法：協定錯誤，不視為完成 | `TestAgentCleanExitWithoutAResultIsNotCompletion`、`internal/agent` 的 `TestCleanExitWithoutAResultIsAProtocolError`、`TestDecodeResultAcceptsOnlyTheThreeOutcomes` |
| verification 命令正常退出但 Evidence 為 FAIL：有限 repair，不當成 PASS | `TestVerificationFailureLeadsToBoundedRepairThenPass` |
| 認證、runtime、toolchain 問題正確分類，不產生假的工程 FAIL | `TestAgentExecutionFailedStopsWithoutInventingAVerificationFailure`、`TestAnUnsatisfiableToolchainStopsWithoutFakeFailEvidence`、`internal/app` 的 `TestRefusalSurvivesWrappingAndDoesNotSwallowOtherFailures` |
| 可恢復 PENDING readiness 經既有 reconcile 推進 | `TestRunnerRecoversWithheldReadinessThroughReconcile` |
| `NONE` 且有 VERIFYING／未滿足依賴：不回報完成或可總檢 | `TestALiveVerificationElsewhereStopsTheRunWithoutClaimingCompletion`、`internal/work` 的 `TestGoalStallClassifiesWhyNothingCanAdvance` |
| Gate／Goal 狀態改變：不啟動被禁止的新工作，不自動解除 | `TestAnOpenGateStopsTheRunWithoutBeingResolved`、`TestABlockedGoalStopsTheRun`、`TestNeedsHumanStopsAndRecordsAGate`、`TestNeedsHumanWithoutOptionsStopsWithoutFabricatingAGate` |
| Gate resolve 後的新 session 與 transitive downstream 都收到 durable decision；合法 human wait 不耗盡 technical attempt budget | `TestResolvedGateDecisionFlowsIntoResumeAndDownstreamHandoffs` |
| 第二個 Runner／symlink 路徑：拒絕重疊 writer，且 `status` 仍可回答 | `TestASecondRunnerIsRefusedThroughAnAliasToo` |
| Crash window、signal、timeout 有明確恢復結果；不確定時拒絕續跑 | `TestSignalStopsTheWorkerAndLeavesAResumableRun`、`TestResumeRefusesAChargedWorkerWithoutPendingOwnership`、`TestRunnerReclaimsAnAbandonedVerificationRun`、`internal/agent` 的 `TestTimeoutStopsTheWholeProcessGroup`、`TestInspectDistinguishesGoneFromOursFromUnrelated` |
| Evidence 保存後崩潰：依最新 domain state 恢復，不重複實作 | `TestResumeKeepsBudgetAndDoesNotReimplementVerifiedWork` |
| Goal completion 已提交但清除最後 facts-read pending 的 run-record save 失敗：resume 以 aggregate evidence 證明後修復終止紀錄，且不先碰 readiness／runtime | `TestResumeRepairsCompletionAfterFinalPendingClearSaveFails` |
| Resume：新 session，預算與 deadline 不重置 | `TestResumeContinuesAfterTheBlockerIsCleared`、`TestResumeKeepsBudgetAndDoesNotReimplementVerifiedWork` |
| 無進展、預算、容量超限：有界停止，不無限重試 | `TestARunThatChangesNothingStopsForNoProgress`、`TestMaxAttemptsPerWorkIsBounded`、`TestMaxStepsStopsTheRun`、`TestExceedingTheArtifactBudgetStopsSafely`、`internal/runner` 的 `TestBudgetRefusesAnyCancelledLimit` |
| Runner artifacts 寫入不影響 Candidate digest | `TestRunnerArtifactsDoNotChangeTheCandidateDigest` |
| 全新 `run`（不只 `resume`）也要先處理前一個 run 遺留的 worker；無法確認時拒絕且不新增 run | `TestAFreshRunRefusesWhileAnEarlierWorkerCannotBeConfirmed` |
| Session 寫入 `.forgepilot/state.json` 時停止，且偽造的 VERIFIED 不被當成總檢依據 | `TestASessionThatWritesForgePilotStateStopsTheRun` |
| 容量上限不可被旗標關閉，且必須由內而外遞增 | `internal/runner` 的 `TestArtifactLimitsRefuseAnyCancelledBound` |
| `resume` 沿用原本的容量上限與 Goal／runtime，不套用命令列預設 | `internal/runner` 的 `TestResumeInheritsTheLimitsTheRunStartedWith` |
| 交接有上限、超限不靜默刪除驗收條件、不含憑證 | `internal/agent` 的 `TestHandoffKeepsRequirementsAndMarksWhatItTrimmed` |
| Dry-run 不呼叫模型、不改 state、不產生 snapshot 或 run record | `TestDryRunInspectsWithoutChangingAnything` |
| `run status` 區分當時的停止結果與目前的 Goal readiness | `TestRunStatusSeparatesTheStoredResultFromCurrentReadiness` |
| 退出碼分類，未分類的停止原因落在錯誤而非成功 | `internal/runner` 的 `TestExitCodesSeparateReviewFromEveryOtherEnding` |
| 拒絕 WORK_ITEM policy、空 Goal、未知 Goal | `TestRunRefusesGoalsItMayNotDrive` |
| 缺少 `--snapshot` 時拒絕，不自動 commit | `TestRunRefusesWithoutSnapshotAndNeverCommits` |
| 較多 Work Items 的 deterministic soak，不以 sleep 冒充長跑 | `TestSoakSchedulesManyWorkItemsAcrossSessions` |
| 既有功能回歸：全域 `next`、WORK_ITEM policy、`verify`、`review`、`status` | `integration_test.go` 與 `readiness_integration_test.go` 全數未修改即通過 |
| `run resume` 也要先處理**其他** run 遺留的 worker，不只自己那筆 | `TestResumeRefusesWhileAnotherRunsWorkerCannotBeConfirmed` |
| canonical check 期間寫入 `.forgepilot/state.json` 時停止——untrusted code 執行的第二個地方 | `TestACanonicalCheckThatWritesForgePilotStateStopsTheRun` |
| run record 不受三個容量上限管轄，session artifact 仍受管轄 | `internal/runner` 的 `TestTheRunRecordIsNotSubjectToTheArtifactBounds` |
| session 正常結束也終止整個 process group，不留下背景子孫程序 | `internal/agent` 的 `TestACleanExitStillStopsTheWholeProcessGroup` |
| 引用失敗 log 的節錄會說自己被截斷，且不從半行開始 | `internal/runner` 的 `TestTailSaysWhenItCut`、`TestTailQuotesAShortLogWhole` |
| SIGINT 與 SIGTERM 分別以 130／143 退出，`run status` 也據實回報 | `TestSignalStopsTheWorkerAndLeavesAResumableRun`、`TestTerminationExitsWithItsOwnCode` |
| 真實 Codex smoke | `TestCodexSmokeDrivesDependentWorkToGoalCompletion` 驗證 Runner 自動完成 Goal；Codex smoke 仍 opt-in（`FORGEPILOT_CODEX_SMOKE=1`），執行時另需明確指定 `FORGEPILOT_SMOKE_MODEL`；預設 CI 不跑；舊 artifacts 的 `AWAITING_GOAL_REVIEW` 保留為歷史紀錄。 |
| 真實 Codex smoke 的啟用條件只接受完全等於 `1`，且判斷在任何副作用之前 | `TestSmokeOptInAcceptsOnlyTheExactValueOne` 逐值陳述契約（未設定、空字串、`0`、`false`、`FALSE`、`off`、`no`、`true`、`yes`、`2`、`" 1 "`、`"1\n"` 一律視為未啟用，只有 `1` 啟用）；`TestRealCodexSmokeConsultsTheOptInBeforeAnySideEffect` 以 PATH 上的 Codex spy 隔離真實 CLI，跑編譯後的測試 binary 確認五個未啟用環境下目標測試回報 SKIP、Codex 未被呼叫、證據匯出未寫入，並以第六個反向對照案例（`1`）確認 opt-in 真的會啟動那一輪並觸及 runtime——見 [ticket 12](specs/runner-mvp/issues/12-smoke-opt-in-closure.md)。修正後的入口尚未在真實模型下重跑。 |
| 結構化結果的 schema 符合 strict structured output（每個物件的 `required` 涵蓋全部 `properties`） | `internal/agent` 的 `TestResultSchemaSatisfiesStrictStructuredOutput`、`TestDecodeResultAcceptsTheNullsTheSchemaRequires` |
| 正式 verification 執行中收到 SIGINT：停止程序群組、退出碼 130、留下 INTERRUPTED 而非 FAIL，且不留下無人結案的 VERIFYING | `TestSignalDuringVerificationStopsTheCheckAndItsProcessGroup` |
| 正式 verification 執行中收到 SIGTERM：停止程序群組、退出碼 143 | `TestTerminationDuringVerificationExitsWithItsOwnCode` |
| signal 到達後不啟動下一張工作，已保存的合法 Evidence 不被覆寫 | `TestASignalNeitherStartsTheNextWorkNorRewritesSavedEvidence` |
| canonical check 正常退出（PASS）後不留下背景子程序 | `TestACanonicalCheckLeavesNoBackgroundChildBehindOnAPass`、`internal/process` 的 `TestACleanExitStillEmptiesItsProcessGroup` |
| canonical check 非零退出（FAIL）後仍完成清理，且 FAIL Evidence 照常保存 | `TestACanonicalCheckLeavesNoBackgroundChildBehindOnAFailure`、`internal/process` 的 `TestAFailingExitStillEmptiesItsProcessGroup` |
| 背景子程序繼承輸出描述元時，等待與輸出收集都不無界阻塞 | `internal/process` 的 `TestAnInheritedOutputDescriptorDoesNotHoldTheCommandOpen` |
| 無法確認清理完成時停止並回報，且不清掉恢復所需的 worker 紀錄 | `internal/agent` 的 `TestAStoppedSessionReportsWhyAndWhetherItsGroupIsSettled` |
| 子程序不回應 SIGTERM 時仍在有界時間內停止，且清理結果經過確認 | `internal/process` 的 `TestAProcessThatIgnoresTerminationIsStillStoppedWithinBounds`、`TestStopRefusesAnUnrecordedGroup` |
| 沒有可用結果的執行不算完成，不會以 exit code 0 變成 PASS Evidence | `internal/process` 的 `TestAnUnusableWaitResultIsNotACompletion` |
| 已完成的執行結果不被同時到達的取消丟棄 | `internal/process` 的 `TestACompletedCommandIsNotDiscardedByALaterCancellation` |
| 已被叫停的步驟不會是啟動新外部程序的那一個（含 preflight） | `internal/process` 的 `TestAnAlreadyCancelledContextStartsNothing`、`TestNoVerificationStartsAfterTheRunDeadlineHasPassed` |
| 總期限在 Agent 執行中到達：`MAX_DURATION` | `TestTheRunDeadlineStopsAnAgentSessionInFlight` |
| 總期限在 verification 執行中到達：`MAX_DURATION`，且不製造工程 FAIL | `TestTheRunDeadlineStopsAVerificationInFlight` |
| Agent 完成時總期限已到，不再啟動 verification | `TestNoVerificationStartsAfterTheRunDeadlineHasPassed` |
| `--agent-timeout` 先到仍是 `AGENT_TIMEOUT`，`--verify-timeout` 先到仍是 `VERIFY_TIMEOUT` | `TestAnAgentTimeoutInsideTheRunDeadlineKeepsItsOwnReason`、`TestAVerifyTimeoutInsideTheRunDeadlineKeepsItsOwnReason` |
| 單次期限取 `min(run deadline, now + timeout)`；多原因同時到達依固定優先序分類 | `internal/runner` 的 `TestAStepIsBoundedByWhicheverLimitComesFirst`、`TestAStepThatOutlivesItsOwnTimeoutSaysSo`、`TestASignalOutranksEveryExpiry`、`TestTheRunDeadlineOutranksACoincidentStepTimeout`、`TestTheFirstReasonToArriveIsTheOneRecorded`、`TestEachCauseMapsOntoItsOwnStopReason` |
| 已到期的 run 經 `resume` 不啟動新 session，也不重置 deadline、steps 或 attempts | `TestResumingAnExpiredRunStartsNoNewSession` |
| 獨立 `forgepilot verify` 不繼承 Runner 總期限，PASS／FAIL 輸出與退出行為不變 | `TestStandaloneVerifyKeepsItsOwnContract` |
| SIGINT 送達時 Runner 正阻塞在 `git worktree add` 的 post-checkout hook：停止、退出碼 130 | `TestAStopReachesGitWhileItIsCheckingOutTheCandidate/interrupt` |
| 同上，SIGTERM：退出碼 143 | `TestAStopReachesGitWhileItIsCheckingOutTheCandidate/termination` |
| 同上，run deadline 到期：`MAX_DURATION`、退出碼 3 | `TestAStopReachesGitWhileItIsCheckingOutTheCandidate/run_deadline` |
| Git 被取消時不啟動正式 verification，也不開始下一張工作 | `TestAStopReachesGitWhileItIsCheckingOutTheCandidate`（三個 subtest 皆斷言） |
| snapshot capture 被取消：使用者 HEAD、branch、real index、staging state 與工作檔案未被破壞 | `TestCancellingSnapshotCaptureLeavesTheUsersRepositoryAlone` |
| SIGKILL 之後仍收不到 wait 結果時，在有界時間內回傳未確認結果 | `internal/process` 的 `TestAWaitResultThatNeverArrivesIsReportedRatherThanWaitedFor` |
| 未確認停止的執行不被當成完成，不以零值退出碼變成 PASS | `internal/process` 的 `TestAnUnconfirmedStopIsNotACompletion` |
| PID 重用且群組仍有成員時不猜測、不發送 signal，回報未確認 | `internal/agent` 的 `TestInspectDistinguishesGoneFromOursFromUnrelated` |
| 取消與清理失敗同時發生時，`VerifyResult.Cleanup` 不遺失，原始中斷原因仍可讀 | `internal/app` 的 `TestACancellationDoesNotSwallowAnUnconfirmedCleanup`（canonical 與 runtime preflight 兩個 subtest） |
| 清理未確認時不刪除恢復紀錄指向的 checkout | `internal/app` 的 `TestAnUnconfirmedCleanupLeavesTheCheckoutInPlace` |
| 未確認的清理阻擋 resume 原 run、新 run 與同 workspace 的另一個 Goal | `TestAnUnconfirmedCleanupBlocksEveryWayBackIntoTheWorkspace` |
| 被阻擋的嘗試不消耗 steps／attempts，也不改寫既有 run 的 deadline | `TestAnUnconfirmedCleanupBlocksEveryWayBackIntoTheWorkspace` |
| `resume` 清除 `stop` 但不清除 `pending` | `TestResumeClearsTheStopButNotThePendingCleanup` |
| 沒有觀察到 identity 的 pending execution 一律 fail closed | `TestAPendingExecutionWithNoObservedIdentityIsRefused` |
| 群組確認消失後恢復解除，且 `pending` 在同一次確認中清除 | `TestRecoveryResumesOnceTheGroupIsConfirmedGone` |
| 缺少可省略的 `pending` 不阻擋下一輪；毀損紀錄或缺少對應 `pending` 的 charged worker 仍阻擋 | `TestStoredRunRecordsRespectChargedOwnership`（三個 subtest） |

#### 08 — 恢復判準只有一份、清理不被吞掉、停止原因不被改名

[08-recovery-closure](specs/runner-mvp/issues/08-recovery-closure.md)。沒有新的架構決定：ADR-0019／0020／0021／0022
的實作在這一輪才在所有呼叫路徑上成立。

| 行為 | 驗收測試 |
|---|---|
| charged run 的紀錄只有 `worker` 沒有對應 `pending`：resume、同 Goal 新 run、同 workspace 另一個 Goal 三者皆阻擋 | `TestAWorkerWithoutChargedPendingBlocksEveryWayBackIntoTheWorkspace` |
| 被阻擋的嘗試不消耗 steps／attempts、不改寫 deadline，`worker` 保留；群組消失也不會掩蓋缺少的 ownership receipt | 同上 |
| 不一致的 charged worker 不對其他程序群組送出 signal；PID 身分判定在 agent 邊界測試 | `TestInconsistentChargedWorkerDoesNotSignalAnUnrelatedProcess`、`internal/agent` 的 `TestInspectDistinguishesGoneFromOursFromUnrelated` |
| 歷史 `RECOVERY_BLOCKED` 的 `stop` 不再讓已可確認的 workspace 繼續被拒 | `TestAnOldRecoveryBlockedStopDoesNotBlockASettledWorkspace` |
| `RemoveWorktree` 不以 `os.RemoveAll` 遮蔽 `ErrNotSettled`，也不追加遞迴刪除 | `internal/repository` 的 `TestRemoveWorktreeAddsNoRecursiveDeleteToAnUnconfirmedGroup` |
| Git 已完成刪除才回報未確認時仍不回報成功 | `internal/repository` 的 `TestARemovalThatSucceededStillReportsItsUnconfirmedGroup` |
| `AddWorktree` 的前置清理同樣不吞掉 `ErrNotSettled` | `internal/repository` 的 `TestAddWorktreeRefusesToClearACheckoutItCannotConfirmIsIdle` |
| 一般清理失敗仍照舊清除殘留目錄，沒有因修正而永久阻擋 | `internal/repository` 的 `TestRemoveWorktreeStillClearsADirectoryGitDoesNotKnowAbout` |
| PASS 之後的 deferred 清理未確認：Evidence 不變，`Cleanup` 與 `Unresolved` 回到呼叫端而不只是 warning | `internal/app` 的 `TestAnUnconfirmedTidyUpTravelsWithAPassRatherThanBecomingAWarning` |
| Runner 收到後保存為 unresolved `pending`、停在 `RECOVERY_BLOCKED`，新的 CLI 程序三種入口皆被阻擋，確認後解除 | `TestAnUnconfirmedTidyUpKeepsTheEvidenceAndStopsTheRun` |
| 沒有 orphan、業務工作時間超過 `CleanupGrace` 時，善後仍取得有效預算且 checkout 確實被移除 | `internal/app` 的 `TestWorkLongerThanOneCleanupWindowStillLeavesOneForTheTidyingUp` |
| 有 orphan：開工前回收的 window 不耗掉收工善後的 window | `internal/app` 的 `TestReclaimingAnOrphanDoesNotSpendTheLaterTidyingUpsWindow` |
| 同一清理階段的多個 helper 共用一份遞減預算，不每層重領 | `internal/app` 的 `TestOneCleanupStageSpendsOneAllowanceAcrossItsHelpers` |
| 步驟間 Candidate facts 查詢被 SIGINT／SIGTERM／run deadline 打斷時記為 `INTERRUPTED`／`TERMINATED`／`MAX_DURATION`，退出碼 130／143／3，不產生 `STALLED` | `TestAStopDuringABetweenStepsFactsReadKeepsItsOwnReason`（三個 subtest） |
| 一般 Git 失敗仍是操作失敗，不被歸類成停止 | `TestAnOrdinaryGitFailureDuringAFactsReadIsStillAnOperationalFailure` |
| 取消測試的兩個 subtest 真的分別進入 canonical 與 runtime preflight，並實際斷言 kind、PGID 與 checkout 位置 | `internal/app` 的 `TestACancellationDoesNotSwallowAnUnconfirmedCleanup` |
| 驗證後 readiness refresh 回報未確認群組時一併帶回，且接著的清理留著 checkout | `internal/app` 的 `TestAnUnconfirmedGroupInTheReadinessRefreshBlocksAndKeepsTheCheckout` |

`make verify` 因 `internal/app` 的兩個 cleanup-window 測試各跑一次超過 `process.CleanupGrace` 的
業務工作而增加約 50 秒；理由與備選記在 [08-recovery-closure](specs/runner-mvp/issues/08-recovery-closure.md)。

本期結案紀錄（交付邊界、證據對照表、本機與 CI 驗證結果、已知限制）在 [specs/runner-mvp/closure.md](specs/runner-mvp/closure.md)。

### Runner MVP Exit checklist

- [x] ADR-0018／0019／0020 與 spec、tickets 寫在實作之前；ADR-0010 加註例外並保留原本的失效條件。
- [x] `internal/work` 仍是純狀態機：goal-scoped 查詢與全域查詢共用一份實作，沒有新增 interface、filesystem、Git 或 subprocess。
- [x] verification orchestration 只有一份，在 `internal/app`；`internal/cli/verify.go` 是薄殼，既有輸出文字與退出碼不變。
- [x] Runner 只寫 execution history，lifecycle 更新全部走既有 transition；state schema 未升版。
- [x] `make verify` 與 `go test -race -count=1 ./...` 實跑通過。
- [x] 針對狀態機繞過、錯誤成功判定、跨 Goal 執行、重疊 writer、crash window、預算重置、Candidate freshness 與無上限輸出做過 code review，**且修正本身也經過第二輪 review**——第一輪的六項修正帶進 2 HIGH 與 3 MEDIUM，已各自以先寫失敗測試的方式修掉。
- [x] 停止訊號、程序清理與兩種期限的執行控制補強（[06-execution-control](specs/runner-mvp/issues/06-execution-control.md)）：SIGINT／SIGTERM 傳達到 canonical check、正常退出也清理程序群組、`--max-duration` 約束整段執行；決定記在 [ADR-0021](adr/0021-execution-limits-are-bounded-and-named.md)。
- [x] 取消到得了執行中的 Git 子程序、終止與等待都有上限、清理未確認時跨 run／resume／重啟持續阻擋（[07-recovery-hardening](specs/runner-mvp/issues/07-recovery-hardening.md)）：決定記在 [ADR-0022](adr/0022-pending-cleanup-outlives-the-process.md)。
- [x] 恢復判準只有一份實作、清理錯誤不被吞掉、清理預算從清理開始才計時、步驟間 Git 查詢被取消時保存正確的停止原因（[08-recovery-closure](specs/runner-mvp/issues/08-recovery-closure.md)）：沒有新的架構決定，ADR-0019／0020／0021／0022 的實作在這一輪才在所有呼叫路徑上成立。
- [x] ADR-0019／architecture 已明列兩個 state-write 偵測窗口與信任模型：agent session 比對整份 state，canonical check 只保護正在驗證的 Work Item，並明說這不是全域 state 完整性或 sandbox 保證。

### Candidate-level canonical verification fan-out（完成）

CLI 契約不變：`forgepilot verify <work-id> [--snapshot]` 的 Work Item 是 anchor，caller 不傳 cohort。`internal/app` 在 per-anchor orphan reclaim 後取得 repository-wide canonical lock，解析一個 Candidate／Resolved Runtime、建立一個 exclusive run-keyed log、執行一次 `make verify`。PASS 由 `internal/work` 在單一 state transaction 內為 anchor 與仍符合 begin-time plan 的 same-Goal stale REVIEW／VERIFIED recipients 建立 distinct Evidence；FAIL／INTERRUPTED 維持 anchor-only。Runner 將回傳的完整 Evidence ID set 寫入 execution history，下一輪仍重新查詢 typed action，不保存 cohort lifecycle。

Schema 升至 v9：root 新增 `next_verification_run_id`，active Run 與 Verification Evidence 新增必填 `verification_run_id`。新 execution 只配發 `VR-*`；v8 migration 對歷史 Verification Evidence 與 orphan Run 配發 distinct `LVR-*`，Review Evidence 保持空值。新 log 以 run ID 為 filename token 並 exclusive-create；Runner 對 `VR-*` 要求 token-bounded unique lookup，遺失或多筆時 fail closed，`LVR-*` 才沿用 Work Item／revision lookup。決定與 rollback 見 [ADR-0027](adr/0027-candidate-verification-pass-fans-out-by-run.md)，完整 acceptance matrix 見 [candidate-verification-fanout spec](specs/candidate-verification-fanout/spec.md)。

驗收涵蓋：一次 PASS fan-out 多筆 shared-run Evidence、FAIL anchor-only、begin/completion eligibility 與 recursive prerequisite closure、optional concurrent change 的 transitive skip、repository lock 在新 artifacts 前拒絕、exclusive log collision 與 `VR-100`／`VR-1000` lookup boundary、Runner 全 Evidence IDs 與 repair excerpt、v8→v9 migration／malformed provenance refusal，以及 canonical `make verify` 與 race suite。

### Public handoff recovery（issue #43）

外部 Agent 不讀寫 `.forgepilot/`，只透過 `goal create --json`、`work add --external-ref <ref> --json` 與 `work list --goal <id> --json` 建立或恢復一個 Goal。三條成功輸出都採 `format_version: "forgepilot.cli/v1"`；create 回傳 Goal，add 回傳 Work Item 與 `created`，list 依建立順序回傳 Goal 及其 Work Items 的 `id`、`goal_id`、`story_ref`、`status`、`depends_on`、`external_ref`。同 Goal／external ref 的完全相同 request 回傳既有項目和 `created: false`；Story 或 dependency set 不同時拒絕，不採用或猜測既有 keyless Work Item。JSON 只保證 exit 0；非零 exit 的 integration caller 必須停止。schema v11 保存 external ref 與 explicit Goal completion policy，且延續明確 migrate／backup／手動 rollback 契約。

### FP-53 Execution Plan Authorization

FP-53 adds two explicit commands:

| Command | Contract |
|---|---|
| `forgepilot execution plan --request <path> --json` | Read-only, versioned preview of the embedded FP-52 Goal Plan request, explicit node mapping, Worker Profile and bounded caps. Returns a current approval token plus diagnostics; it never writes state or launches a subprocess. |
| `forgepilot execution authorize --request <path> --approval-token <token> --by <name> --json` | Revalidates the same request, artifacts and current Goal registration, then publishes the complete initial binding, revision-one authorization, zero-use ledger and Goal witness in one storage transaction. `--by` is a self-declaration, not authentication. |
| `forgepilot execution revise plan --request <path> --json` | Read-only preview of an additive reviewed revision. Existing Plan Node mappings must name their existing Work Item; each new node explicitly uses an empty `workItemId`. |
| `forgepilot execution revise authorize --request <path> --approval-token <token> --by <name> --json` | Rechecks the preview under workspace and state locks, atomically creates the explicit new Work Items, and appends the plan binding and authorization without resetting cumulative consumption. |
| `forgepilot execution resume --goal <goal-id> [--json]` | Rechecks recovery, the current Goal authorization, and current bindings. It resumes only a run that still satisfies its exact contract; otherwise it creates at most one newly charged run. It accepts no run ID, runtime, budget, or artifact-cap override. |
| `forgepilot execution stop --goal <goal-id> --by <name> --reason <reason> [--json]` | Persists one user-requested pause for the Goal's current run before it asks the owned worker process group to stop. It never clears a pending execution, edits lifecycle state, Evidence, Gate, or authorization, and it fails closed when worker ownership cannot be proved. |
| `forgepilot execution declare --request <path> --json` | Validates and records one named External Fulfillment Declaration against an existing external wait. The strict `forgepilot.external-fulfillment-declaration/v1` request supplies `goalId`, `waitId`, `fact`, and `declaredBy`; ForgePilot derives and records the exact node, plan binding, and authorization revision from the durable wait. Recording a declaration never resumes a run. |

The current request is strict JSON with `formatVersion: "forgepilot.execution-plan-request/v2"`, an embedded `goalPlanRequest` using `forgepilot.goal-preflight-request/v1`, an explicit `workerProfile`, an explicit immutable `engineGeneration`, explicit `caps`, and an absolute `expiresAt` in UTC RFC 3339 form. Historical v1 requests remain part of prior authorization evidence; new previews reject them. `workerProfile` supplies the Codex executable path, fixed model, `effort: "medium"`, and `sandbox: "workspace-write"`; no value is inferred from an earlier Gate or a local runtime default. The executable path must resolve to a regular executable file and its file digest is rechecked at authorization. `caps` explicitly supplies `maxSteps`, `maxTechnicalAttemptsPerNode`, `maxRuns`, `maxRecoveries`, `maxHandoffBytes`, `maxWriteBytes`, `maxRunBytes`, and `maxTotalBytes`. Every value must be positive and artifact bounds must be ordered. The exact expiry must be in the future and no more than fourteen days from the operation; it is never recomputed or extended during authorization.

The preview token is a domain-separated digest, not a credential. It binds exact request bytes, current artifact digests, canonical workspace and Goal, the relevant Work Item/dependency registration, the observed absence of an existing authorization, explicit profile and caps, and exact expiry. Authorization rechecks those facts under the storage lock; stale input, another writer, invalid topology, or a save error publishes none of the aggregate. Authorization does not change Goal or Work Item lifecycle, Gate, Evidence, Human Review, or completion state, and it does not assign PraxisBound manifest or coverage semantics to ForgePilot.

FP-53 records the explicitly requested Worker Profile and executable file digest, but does not claim an executable-reported version or managed ForgePilot engine generation. Since preview is read-only and may not launch a subprocess, the initial authorization is a plan/profile/caps binding, not a launchable worker authorization. FP-58 owns pinned engine-generation and selected executable identity validation; FP-54/58 admission must remain fail-closed until those checks pass. No mutable `launchable` flag is stored.

State schema advances to v16 with an optional Goal-owned execution aggregate containing the initial plan binding, authorization, zero-use ledger, witness, and append-only stable-ID artifact-byte reservations. `migrate` backs up the prior snapshot before advancing; it does not infer or adopt a plan for existing Goals. A migrated v15 authorization has unknown historical artifact consumption, which fail-closes new execution and revision cap validation until explicit reauthorization; migration never scans run records or logs to manufacture that fact. Rollback remains manual restoration of the backup.

### FP-56 Execution Control and waits

State schema v17 fences the versioned `.forgepilot/execution-control.json`
sidecar from older binaries. The sidecar is execution-control history only: it
contains the active pause, the active human or external wait, and append-only
External Fulfillment Declarations; it never contains a Work Item state,
Evidence, Gate decision, review, or completion fact. Its own short-lived
`execution-control.lock` serializes pause requests with the Runner's narrow
worker-launch admission window. The long-lived workspace Runner lock remains
the single-writer lock for Runner loops and is not reused for a stop request.

An agent's `needs_human` result may name an `externalFact`. ForgePilot creates
an external wait only after the result, its charged ACTION receipt, and the
one-time needs-human disposition are durably confirmed. A result with no named
external fact remains a human wait. Both consume the already-reserved action,
step, duration, and artifact capacity; only the confirmed disposition removes
the technical-attempt charge. Missing, malformed, or crashed results create no
wait and retain that charge.

Every Runner admission reads control while holding the control lock. A persisted
pause or wait stops it before any new process; after a worker is launched its
identity is saved before the lock is released. `execution stop` obtains that
same lock, writes the pause atomically, then stops only an identity that passes
the existing ownership check. Resume first settles pending cleanup, rechecks
current plan and authorization, verifies an external declaration when one is
required, and explicitly acknowledges/clears the control block only when it is
safe to admit a new action. No declaration or restart resumes work implicitly.

### FP-58 Pinned Engine Generation ownership

FP-58 upgrades the execution request to strict
`formatVersion: "forgepilot.execution-plan-request/v2"`. It retains every v1
field and adds an explicit `engineGeneration` object with full lowercase
`sourceCommit` and canonical `payloadSHA256`; no engine selection is inferred
from Bootstrap `current`, an earlier authorization, Runner options, or the
calling chat model. The v2 request is required for a new pinned authorization
and for every authorization revision. The existing command names remain the
contract surface:

| Command | FP-58 contract |
|---|---|
| `forgepilot execution plan --request <path> --json` | A read-only v2 preview. It syntax-validates and displays the proposed engine tuple but neither resolves the Bootstrap helper nor acquires a marker. Its approval token binds that exact tuple. |
| `forgepilot execution authorize --request <path> --approval-token <token> --by <name> --json` | Re-resolves the process-image-anchored managed helper and requires it to report the requested tuple. It acquires the authorization-owner marker before publishing the initial execution authorization. |
| `forgepilot execution revise plan --request <path> --json` | A read-only v2 revision preview. Its diff explicitly says whether `engineGeneration` changes and its approval token binds both old and proposed tuple. |
| `forgepilot execution revise authorize --request <path> --approval-token <token> --by <name> --json` | For an engine change, requires persisted pause, confirmed full-record cleanup, successful read-only Engine Compatibility Check, exact candidate tuple re-observation, and new authorization marker acquisition before appending the revision. It never clears the pause or silently substitutes a different generation. After commit it tries retention reconciliation; release failure is returned as `retentionWarning` beside the committed revision, for explicit retry. |
| `forgepilot execution retention reconcile --json` | Rechecks durable authorization and Run closure, then idempotently releases only their exact Bootstrap markers. A failed release leaves closure intact for retry. |

The authorization owner and every supervised Run owner use separate
domain-separated opaque references. `run.json` records its immutable
authorization digest and engine tuple before a worker starts, but never the raw
reference. A Run remains an owner through ordinary stops, waits, recovery and
unresolved cleanup; it closes only through a durable terminal disposition after
the complete cleanup criterion succeeds. A current authorization closes only
when safely superseded or its Goal becomes non-launchable. Closing is persisted
before the idempotent Bootstrap `retention-v1 release` call, so every crash or
release failure leaves at worst an extra marker. Historical authorization and
Run bindings remain immutable.

The Engine Compatibility Check is an `internal/app` read-only query under the
workspace lock. It validates the candidate process image and strict readability
of state, control sidecar and every ownership-relevant Run Record without
migration, repair, Agent launch, Git, canonical verification or an ambient
runtime probe. Unknown/malformed ownership, a missing record, a stale pause,
uncertain cleanup, candidate drift or a failed marker operation rejects before
any worker starts. The concrete ordering and trust boundary are fixed by
[ADR-0039](adr/0039-per-owner-engine-generation-retention.md).

The implementation advances state schema to v18 and execution-control schema
to v2. Migration does not infer owner closure, engine tuples or cleanup from
v17 state and old Run Records: it preserves readable history as unknown and
blocks pinned launch, engine revision and release until a v2 authorization has
established the required facts. An explicit v2 revision may first pin a migrated
Goal only when its ledger has no charged execution and the workspace has no Run
Records; unknown old ownership is never released. The migration backs up the prior state as usual;
rollback restores that backup, while extra Bootstrap markers are safe to retain.

Required focused coverage includes each acquire/state-write/Run-Record-save and
closure/release crash boundary; release retry; old authorization and Run
immutability; revision refusal for missing/stale pause, unsettled/malformed
records, incompatibility, candidate drift and stale approval; credential
redaction; and the fact that old run markers outlive authorization release until
their own durable closure. The FP-58 acceptance commands remain
`go test ./internal/agent ./internal/app`, `go test ./internal/app ./internal/storage`,
`go test ./internal/app ./internal/runner`, and
`scripts/forgepilot-bootstrap_test.sh`; repository-wide full and race gates
remain FP-61 / final-acceptance work.
