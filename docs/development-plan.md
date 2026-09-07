# Development plan

## 計畫狀態

M1 已完成；M2 以後仍為規劃。本文件供後續開發拆分工作、驗收與交接；產品規則見 [architecture.md](architecture.md)。

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
- M3 才執行真實的 A → verify → review → DONE → B READY 端到端驗收。

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

### M3

先定義 Gate resolve／cancel／resume、多 Gate、BLOCKED 建立與 recovery、Human identity、Goal lifecycle 與 DONE／reopen policy，再實作 Gate、Review 與完成流程。

驗收未解 Gate 不可推進、不同 revision 的 PASS／APPROVED 不可組合、未 review 不可 DONE、approve 後依賴原子解鎖。完成真實 A DONE → B READY 的端到端流程，並覆蓋完整 MVP 的 Goal lifecycle、Gate blocking 與 Evidence append 測試。

### M4

實作前另定 PR metadata 的來源、授權與 read-only integration 邊界。Evidence 必須包含 repository、PR number 與 exact HEAD SHA；HEAD 改變即為新 target。不得加入 automatic merge 或 release。

## 每階段交付格式

開發者完成後提供：

1. **Implementation Summary**：實際完成的行為與 milestone。
2. **Architecture Decisions**：本階段定案事項與理由。
3. **Files Changed**：實際變更檔案。
4. **Verification Result**：執行命令、PASS／FAIL／not run 與原因。
5. **Deferred Work**：仍未實作的後續功能。
6. **Risks / Open Questions**：未解風險、操作限制與下一階段前置決策。

只有 acceptance criteria 與 required checks 實際通過才能宣告 milestone 完成；文件或測試 fixture 不代表產品能力已實作。
