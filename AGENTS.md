# AGENTS.md

接手 ForgePilot 前先讀這份。它記的是不可重建的知識——程式碼可以重推，踩過的坑不行。

## 這是什麼

被動的 DAG 帳本（[ADR-0040](docs/adr/0040-forgepilot-is-a-passive-dag-ledger.md)）：工單拆分完成後，外部 Agent 依 `next` 的建議依拓撲順序推進一張 DAG；ForgePilot 只判定下一個合法動作、保存綁定確切 Candidate 的 Verification Evidence，並在完成的同一次交易內解鎖下游。本機 CLI，Go 1.25.5、**只用標準函式庫**、module `github.com/CarlLee1983/ForgePilot`、只支援 macOS 本機檔案系統。ADR-0040 的收斂已全部實作；roadmap 沒有下一個 milestone，後續工作來自 dogfood，開在 GitHub Issues。

## 讀的順序

1. [CONTEXT.md](CONTEXT.md) — 詞彙。把 Story 與 Work Item 混用是這個 domain 最容易犯的錯
2. [docs/architecture.md](docs/architecture.md) — 責任邊界、資料模型、生命週期、`next` 規則、儲存
3. [docs/adr/README.md](docs/adr/README.md) — 40 份決定：13 份 accepted、1 份（0008）部分被取代、26 份被 0040 取代；沒有 proposed 的開放問題。被取代的 ADR 只當歷史讀
4. [docs/development-plan.md](docs/development-plan.md) — CLI 契約（**flag 命名以此為準**）與變更面驗證矩陣；其下的「歷史紀錄」是 ADR-0040 之前的 milestone，不代表現況
5. `docs/specs/` 與 `specs/stories/` — 歷史規格與 ForgePilot 自己的 Story，描述已移除能力者頂部有標示

需要先看懂形狀時，[docs/diagrams/](docs/diagrams/README.md) 有四張圖：狀態機、分層、交易邊界與 `verify` 的順序。

## 不要「修回去」的事

這些看起來像疏漏，其實是決定。動手前先讀對應的 ADR。

- **Work Item 上沒有 revision 欄位，Candidate 也不存在 Work Item 上。** revision 與 `COMMIT`／`SNAPSHOT` identity 只隨 `current_run` 與 Evidence 存在；snapshot ref 在 `refs/forgepilot/snapshots/`，不建立 branch、tag 或 WIP commit——ADR-0003、ADR-0014
- **PENDING／READY 不存在 state 裡。** 持久化狀態只有 `NOT_STARTED`、`RUNNING`、`VERIFYING`、`REVIEW`、`DONE`；readiness 讀取時由依賴是否全 DONE 算出。沒有 `reconcile`，不要為了「讀得快」把它寫回去——ADR-0040
- **同一 workspace 最多一件 RUNNING 或 VERIFYING**（含孤兒 VERIFYING），REVIEW 不佔位，終態 Goal 的工作也不佔位。平行執行是刻意延後——ADR-0040
- **沒有完成指令。** 沒有 `done`、沒有 `complete <work-id>`、沒有測試專用 approve。DONE 只能是 `verify` PASS（Goal 無 Approval Requirement）或 `review approve`（有）的結果——ADR-0008、ADR-0040
- **Goal 完成不要求每個節點的 PASS 都對上最終 Candidate。** 全部節點 DONE 即完成；拓撲序中後完成的節點，其 `make verify` 已涵蓋整個 repository。不要「補上」這個檢查——ADR-0040
- **DONE 沒有 reopen，也不判 stale。** 要重做就新增一件 Work Item，讓「為什麼重做」有地方被記錄——ADR-0006
- **沒有 `WAITING_HUMAN`，Work Item 與 Goal 也都沒有 `BLOCKED`。** 阻擋由「有沒有未解除的 Gate」表達，不佔用狀態欄——ADR-0007
- **決策者身分不做認證。** 半套的認證比不做更危險，它會讓人以為那個名字有保證——ADR-0005
- **不發出任何網路請求，除 Git 與受管理 repository 的 `make verify` 外不啟動程序。** 不自行 HTTP，不 spawn `gh`，沒有 PR 欄位；ForgePilot 也不啟動 coding CLI——ADR-0010、ADR-0040
- **ForgePilot 不解析 toolchain，也只有一個節點一次 verify。** Toolchain 由受管理專案的 `make verify` 自行固定；一次 `verify` 只為觸發它的那件工作留下 Evidence，讓「哪個節點在哪個 Candidate 完成」保持單義——ADR-0040
- **Evidence 上沒有指向 verification 輸出的欄位。** 輸出以 Verification Run 為鍵存在 `.forgepilot/logs/`，只有 `current_run` 有 log path——ADR-0012
- **`verify` 先無條件回收孤兒，再判斷能不能開始新的執行。** `internal/app/verify.go` 有一段被縮小的承諾：被 Gate 擋住的 `verify` 仍會記下先前中斷的那一筆 INTERRUPTED——ADR-0009
- **`goal import` 的重新匯入有四個細節，不是疏漏。** 依賴以集合比較（重排不算改動）；計畫必須列出每個既有節點（漏列即拒絕，計畫永遠是整張 DAG）；零節點的計畫拒絕；既有節點只逐字比較，不重新檢查 Story 是否存在（已完成而後來被搬走的 Story 不該凍結整個 Goal）。節點 ID 還必須是合法的 Git ref component，因為它會被嵌進 snapshot ref——GitHub issue #68 的留言
- **沒有 migration。** Schema 19 是斷代：舊版 state 被拒讀並指向 `tools/export-plan`，不要加 `migrate`。匯出腳本在三個採用 repository 遷移完成後刪除（issue #77）
- **`Validate` 不檢查 DONE 的完成條件。** 加上去會讓 Validate 與當下的完成規則綁死，日後規則一改，舊的合法 DONE 就變成讀不進來的 state

## 地雷

每一條都是實際踩過的，不是假設。

**前置檢查與它把關的交易必須用同一個判準。** 這個專案犯過兩次同型錯誤：M2 在主工作樹檢查卻在隔離 worktree 執行；M3 的 `CanBeginVerification` 孤兒分支漏了 Gate 檢查，卡在 VERIFYING 的工作因此能繞過未解除的 Gate。兩次都是 code review 找到的。現在 PASS 與 `review approve` 共用同一個 `CompletionBlock`、`start` 與 `verify` 共用同一個佔位判準，就是為了這件事。寫任何檢查時問一次：檢查的對象是不是執行的對象，判準是不是比它把關的交易寬鬆。

**Snapshot capture 只操作 private index。** `verify --snapshot` 可以寫 local Git objects 與 ForgePilot snapshot ref，但 capture 前後的 current branch、HEAD、real index、staging state 與 working files 必須相同；`status`／snapshot review 重算 digest 時連 object database 都要隔離。詳見 ADR-0014。

**每次 schema 升版，檢查兩處拒讀 fixture 還在驗版本。** `internal/storage/storage_test.go` 的 `currentShape` 與 `internal/work/work_test.go` 的 `TestSchemaVersionErrorsDistinguishOlderFromNewer` 相對於 `SchemaVersion` 寫，並先確認同一份文件在目前版本能載入——否則 fixture 會因為不相干的原因（未知欄位、缺計數器）而「拒讀」，在無人察覺下不再測版本。升版時 `currentShape` 要隨新欄位更新，work_test 內寫死的 `SchemaVersion != 19` 要改。同一個檔案裡有一筆刻意寫死的 v18 文件，用來確認拒讀訊息講版本而不是 unknown field。

**測試的註解不算數，斷言才算數。** M4 有一條測試，註解寫「同一 revision 上標不同 PR 的兩筆 review 仍互相取代」、變數也叫 `rejecting`，但整段只記了一筆 review。它照樣通過，exit checklist 也照樣被勾成完成。寫完一條測試後讀一遍：註解宣稱的事，斷言真的驗到了嗎。

**驗收條件可能與 ADR 抵觸。** M4 的票 03 原本寫「同一件工作在 HEAD 改變後重新 verify 與 approve」——那需要 reopen，而 ADR-0006 說 DONE 是終態。實作前把每一條驗收條件對一次 ADR，不要因為它寫在 ticket 上就假定它成立。

**`status` 的每一行都有人在測。** 改輸出格式會打到 `integration_test.go` 一票斷言。這是刻意的：ADR-0008 要求 `status` 不能對未完成的原因沉默。

**CLI flag 以 development-plan 的契約表為準。** M3 實作時一度自行命名成 `--as` / `--rationale`，後來改回文件寫的 `--by` / `gate open --reason`。先改契約表，再實作。

**`init` 加進 `.gitignore` 的那一行讓新 repository 的工作樹不乾淨。** commit-mode `verify` 在 `.gitignore` 被 commit 前會因未追蹤檔而拒絕；文件示例與手動流程要先 commit 它（整合測試是自己先寫好 `.gitignore`）。

## 分層

- `internal/cli` — 參數、呈現、錯誤映射。不自行決定 transition 合法性
- `internal/app` — CLI 的 orchestration（`verify`、`goal import`、Candidate facts）。唯一可以同時碰 `work`、`storage`、`repository` 的地方
- `internal/work` — 純狀態機。**沒有任何 interface**，外部事實一律以純值參數傳入（時間是 `now time.Time`，Git 事實是 `RepositoryState`，執行結果是 exit code）。不碰 filesystem、Git 或 subprocess
- `internal/repository` — **唯一允許碰 Git 的地方**。所有 git 呼叫走檔尾一個未匯出的 `git(root, args...)` helper
- `internal/storage` — snapshot、交易鎖、Verification flock、decode／validate、原子保存。唯一的回呼形態是 `storage.Update(root, func(*work.State) error)`
- `internal/process` — 程序群組的啟動、有界停止與停止確認。`make verify` 會 fork，只終止 leader 會讓真正的工作繼續跑

Goal、Work Item、Evidence、Gate 共用同一份 JSON snapshot 與同一次受鎖的原子替換。完成一件工作與解鎖其下游必須落在同一次交易內，否則讀取者會看到「A 已 DONE 但 B 仍 PENDING」的中間狀態——這也是 readiness 讀取時計算、不寫入的原因。

## 工作方式

`/grill-with-docs` → `/to-spec` → `/to-tickets` → `/implement`（內含 TDD 與 code review）。邊界決策先問清楚並寫進 architecture 與 ADR，再寫程式；已經定案的不重新翻案。

Issue 開在 GitHub Issues（`CarlLee1983/ForgePilot`），一律用 `gh` CLI 操作，詳見 `docs/agents/issue-tracker.md`。`docs/specs/` 與 `specs/stories/` 底下是進版控的規格與歷史紀錄，不是 tracker，也不放 `.scratch/`。

Commit 格式 `<type>: [ <scope> ] <subject>`，scope 用 `repo`（歷史上用 milestone 代號）。實作完成後跑一次 code review：M2 找到一個真 CRITICAL，M3 兩個真 HIGH，M4 一個真 HIGH，三次都值得。

## 驗證

**驗證分級：** 修改文件／靜態頁面、Agent skill、圖、Go／Makefile 前，先讀 [development plan 的變更面驗證矩陣](docs/development-plan.md#變更面驗證矩陣)，執行該變更面直接需要的檢查並回報未跑的完整 gate 與理由。

`make verify` 是這個專案自己的 canonical full gate（格式、`go vet`、`go test ./...` 與 CLI build），也是它對受管理專案要求的同一個命令。`go test -race -count=1 ./...` 是 integration／final acceptance 的 race gate，CI 兩者都跑；Story、整合或 Human final acceptance 明定時必跑，不以 focused check 取代。

**開工前自己重跑一次受影響的檢查。** 任何文件裡寫的「上次通過了」都是紀錄，不是現在的結果。

## 這個 repo 的一個細節

`graft/` 被 `.gitignore` 忽略但確實存在。根目錄的 `.ignore` 讓 ripgrep 仍能搜尋那棵樹——它不是殘留檔案，不要刪。

## Agent skills

`skills/claude-code/forgepilot/` 與 `skills/codex/forgepilot/` 是給外部 Agent 的迴圈 skill，手動複製到各 Agent 的 skill 目錄（見 README）。兩份內容相同，只有呼叫方式不同；改一份要同步另一份。

### Issue tracker

Issue 開在 GitHub Issues（`CarlLee1983/ForgePilot`），一律用 `gh` CLI 操作。See `docs/agents/issue-tracker.md`.

### Triage labels

沿用五個預設角色標籤，標籤字串等同名稱。See `docs/agents/triage-labels.md`.

### Domain docs

Single-context：根目錄 `CONTEXT.md` + `docs/adr/`。See `docs/agents/domain.md`.
