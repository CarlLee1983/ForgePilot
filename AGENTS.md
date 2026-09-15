# AGENTS.md

接手 ForgePilot 前先讀這份。它記的是不可重建的知識——程式碼可以重推，踩過的坑不行。

## 這是什麼

管理工程工作的可執行性、進度與決策證據的本機 CLI。Go 1.25.5、**只用標準函式庫**、module `github.com/CarlLee1983/ForgePilot`、只支援 macOS 本機檔案系統。M1–M5、P0／P1 與 Goal-level Review Policy 全部實作完成，另有 Long-running Runner MVP（`forgepilot run`）；roadmap 沒有下一個 milestone，後續工作來自 dogfood，開在 issue tracker 上。

## 讀的順序

1. [CONTEXT.md](CONTEXT.md) — 詞彙。把 Story 與 Work Item 混用是這個 domain 最容易犯的錯
2. [docs/architecture.md](docs/architecture.md) — 責任邊界、資料模型、狀態規則、各階段的開工前定案
3. [docs/adr/README.md](docs/adr/README.md) — 16 份 accepted 決定與其失效條件，另有 1 份尚未回答的 proposed 開放問題
4. [docs/development-plan.md](docs/development-plan.md) — CLI 契約表（**flag 命名以此為準**）與各階段 exit checklist
5. `docs/specs/` — M1–M4 每個 milestone 一個 `m*/` 目錄，收 spec 與 ticket；M5 之後改以單一檔案記錄（`m5-dogfood-friction.md`），衍生的工作以 issue 追蹤

需要先看懂形狀時，[docs/diagrams/](docs/diagrams/README.md) 有四張圖：狀態機、分層、交易邊界與兩條主要流程。

## 不要「修回去」的事

這些看起來像疏漏，其實是決定。動手前先讀對應的 ADR。

- **Work Item 上沒有 revision 欄位，也沒有 PR 欄位。** 兩者都只存在於 Evidence。看到 Evidence 上有一個沒有任何規則讀取的 `pr`，那是刻意的——ADR-0003、ADR-0011
- **Candidate 不存在 Work Item 上。** `COMMIT`／`SNAPSHOT` identity 只隨 `current_run` 與 Evidence 存在；snapshot ref 在 `refs/forgepilot/snapshots/`，不建立 branch、tag 或 WIP commit——ADR-0014
- **Runtime 不從 main worktree 或 caller shell 猜。** Runtime Contract 在 Candidate checkout 解析；actual versions 先固定於 `current_run` 再隨 Verification Evidence 保存。沒有 declaration 才沿用目前 PATH——ADR-0015
- **Evidence 上沒有指向 verification 輸出的欄位。** 輸出以 run 為鍵存在 `.forgepilot/logs/` 底下，`current_run` 才有 `LogPath`——ADR-0012
- **沒有完成指令。** 沒有 `done`、沒有 `complete <work-id>`、沒有測試專用的 approve。DONE 只能是 `review approve` 在條件滿足時的結果——ADR-0008
- **DONE 沒有 reopen。** 要重做就新增一件 Work Item，讓「為什麼重做」有地方被記錄——ADR-0006
- **沒有 `WAITING_HUMAN`，Work Item 也沒有 `BLOCKED`。** 阻擋由「有沒有未解除的 Gate」表達，不佔用狀態欄——ADR-0007
- **決策者身分不做認證。** 半套的認證比不做更危險，它會讓人以為那個名字有保證——ADR-0005
- **不發出任何網路請求。** 不自行 HTTP，不 spawn `gh`。PR Reference 是使用者輸入的字串，只驗格式，不查證那個 PR 存在——ADR-0010
- **`verify` 先無條件回收孤兒，再判斷能不能開始新的執行。** `internal/cli/verify.go` 有一段被縮小的承諾，那是 M3 刻意改的——ADR-0009
- **Runner 不保存 Work Item lifecycle。** `internal/runner/` 只有 execution history——step、attempt、預算、程序 ownership、停止原因。每一步都重新向 `internal/work` 的 typed query 取得合法動作，不解析 CLI 輸出、不快取啟動時的清單。看到「run record 裡沒有進度」不是疏漏——ADR-0019
- **verification orchestration 只有一份，在 `internal/app`。** `internal/cli/verify.go` 是薄殼。要在 Runner 加驗證流程時，改的是 `internal/app/verify.go`，不是複製一份到 `internal/runner/`——ADR-0019
- **Runner 啟動本機 coding CLI 是 ADR-0010 的明確例外，不是它被推翻。** 既有命令仍然完全離線；ForgePilot 沒有 HTTP client，也不持有憑證。不要因為 Runner 存在就往 `internal/repository` 或 `internal/work` 加網路呼叫——ADR-0018
- **Worker 存活判定不只看 pid。** pid 會重用，所以識別是 pid 加「啟動時由 `ps` 自己報回的 start time 與 command」。判不出來時 fail-closed 回報 recovery blocked，不猜、不殺——ADR-0020
- **`Validate` 不檢查 DONE 的四項條件。** 加上去會讓 Validate 與當下的完成規則綁死，日後規則一改，舊的合法 DONE 就變成讀不進來的 state

## 地雷

每一條都是實際踩過的，不是假設。

**前置檢查與它把關的交易必須用同一個判準。** 這個專案犯過兩次同型錯誤：M2 在主工作樹檢查卻在隔離 worktree 執行；M3 的 `CanBeginVerification` 孤兒分支漏了 Gate 檢查，卡在 VERIFYING 的工作因此能繞過未解除的 Gate。兩次都是 code review 找到的。寫任何檢查時問一次：檢查的對象是不是執行的對象，判準是不是比它把關的交易寬鬆。

**Snapshot capture 只操作 private index。** `verify --snapshot` 可以寫 local Git objects 與 ForgePilot snapshot ref，但 capture 前後的 current branch、HEAD、real index、staging state 與 working files 必須相同；`status`／snapshot review 重算 digest 時連 object database 都要隔離。詳見 ADR-0014。

**每次 schema 升版，兩處 fixture 的版本號必須跟著往上調**——`internal/storage/storage_test.go` 中驗證「較新 schema 應被拒讀」的那一筆，與 `internal/work/work_test.go` 中區分較舊／較新 schema 錯誤的那一筆。它們壞掉的方式不是變紅，是在無人察覺下改為驗證一個合法的 state。M2 踩過一次。同一個檔案裡還有一處用字串替換改寫版本號的測試，改動時確認它仍然抓得到你要它抓的東西。

**Runner 的測試不能全用記憶體 mock。** 程序群組、flock、crash recovery 都必須用真的 subprocess 驗證——`--runtime fake` 的 adapter 就是為此存在的產品程式碼，不是測試替身。fake 跑綠只證明 ForgePilot 的程序、鎖與恢復處理正確，不證明無人值守跑真實模型會成功。真實 Codex smoke test 是 opt-in（`FORGEPILOT_CODEX_SMOKE=1`），預設 CI 不跑。smoke fixture 是 `t.TempDir()`，測試結束就連同 `.forgepilot/` 與 Git objects 一起消失；一輪真實執行無法重播，所以要留證據就設 `FORGEPILOT_SMOKE_ARTIFACT_DIR`（fixture 外的絕對路徑）——它是測試用設定，不是產品 flag，而且**單獨設它不會啟動真實模型**。見 [ticket 11](docs/specs/runner-mvp/issues/11-real-codex-smoke-acceptance.md)。

**測試的註解不算數，斷言才算數。** M4 有一條測試，註解寫「同一 revision 上標不同 PR 的兩筆 review 仍互相取代」、變數也叫 `rejecting`，但整段只記了一筆 review。它照樣通過，exit checklist 也照樣被勾成完成。寫完一條測試後讀一遍：註解宣稱的事，斷言真的驗到了嗎。

**`ps` 讀不到參數時不報錯。** macOS 的 `ps` 會改印 `(sh)` 這種括號包住的 accounting name，退出碼 0，那一行看起來就是一條正常的 command。括號渲染本身是這台機器上看得到的（`ps -axo pid=,args=` 會出現 `86853 (git)` 這種一閃而過的行）；`internal/agent` 的 worker 識別在 CI 上失敗過一次——一個剛啟動、還活著的 worker 被判成 `UNRELATED`，本機重現不了——成因是推論而非實測。括號形式必須當成「再問一次」而不是一條 command——ADR-0020。

**驗收條件可能與 ADR 抵觸。** M4 的票 03 原本寫「同一件工作在 HEAD 改變後重新 verify 與 approve」——那需要 reopen，而 ADR-0006 說 DONE 是終態。實作前把每一條驗收條件對一次 ADR，不要因為它寫在 ticket 上就假定它成立。

**`status` 的每一行都有人在測。** 改輸出格式會打到 `integration_test.go` 一票斷言。這是刻意的：ADR-0008 要求 `status` 不能對未完成的原因沉默。

**CLI flag 以 development-plan 的契約表為準。** M3 實作時一度自行命名成 `--as` / `--rationale`，後來改回文件寫的 `--by` / `gate open --reason`。先改契約表，再實作。

## 分層

- `internal/cli` — 參數、呈現、錯誤映射。不自行決定 transition 合法性
- `internal/app` — CLI 與 Runner 共用的 orchestration。唯一可以同時碰 `work`、`storage`、`repository` 的地方
- `internal/agent` — Agent runtime 邊界：啟動本機 coding CLI、交接內容、結果驗證、程序群組控制。與 Candidate 的 verification toolchain 是兩件事，不要合併
- `internal/runner` — 執行迴圈、預算、停止條件、execution history
- `internal/work` — 純狀態機。**沒有任何 interface**，外部事實一律以純值參數傳入（時間是 `now time.Time`，執行結果是 exit code）。不碰 filesystem、Git 或 subprocess
- `internal/repository` — **唯一允許碰 Git 的地方**。所有 git 呼叫走檔尾一個未匯出的 `git(root, args...)` helper
- `internal/storage` — snapshot、交易鎖、decode／validate、原子保存。唯一的回呼形態是 `storage.Update(root, func(*work.State) error)`

Goal、Work Item、Evidence、Gate 共用同一份 JSON snapshot 與同一次受鎖的原子替換。完成一件工作與解鎖其下游必須落在同一次交易內，否則讀取者會看到「A 已 DONE 但 B 仍 PENDING」的中間狀態。

## 工作方式

`/grill-with-docs` → `/to-spec` → `/to-tickets` → `/implement`（內含 TDD 與 code review）。邊界決策先問清楚並寫進 architecture 與 ADR，再寫程式；已經定案的不重新翻案。

這個專案**沒有外部 issue tracker**——`docs/specs/<milestone>/issues/` 底下的檔案就是 tracker，進版控，不放 `.scratch/`。

Commit 格式 `<type>: [ <scope> ] <subject>`，scope 用 milestone 代號（`m4`）或 `repo`。實作完成後跑一次 code review：M2 找到一個真 CRITICAL，M3 兩個真 HIGH，M4 一個真 HIGH，三次都值得。

## 驗證

`make verify` 是這個專案自己的 canonical check（格式、`go vet`、`go test ./...`、CLI build），也是它對受管理專案要求的同一個命令。另跑 `go test -race -count=1 ./...`。

**開工前自己重跑一次。** 任何文件裡寫的「上次通過了」都是紀錄，不是現在的結果。

## 這個 repo 的一個細節

`graft/` 被 `.gitignore` 忽略但確實存在。根目錄的 `.ignore` 讓 ripgrep 仍能搜尋那棵樹——它不是殘留檔案，不要刪。

## Agent skills

### Issue tracker

Issue 開在 GitHub Issues（`CarlLee1983/ForgePilot`），一律用 `gh` CLI 操作。See `docs/agents/issue-tracker.md`.

### Triage labels

沿用五個預設角色標籤，標籤字串等同名稱。See `docs/agents/triage-labels.md`.

### Domain docs

Single-context：根目錄 `CONTEXT.md` + `docs/adr/`。See `docs/agents/domain.md`.
