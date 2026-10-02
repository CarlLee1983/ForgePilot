# ForgePilot

[English](README.md) | **繁體中文** | [日本語](README.ja.md)

ForgePilot tells your engineering agents what work is actionable next.

Warrant bounds that work to human-approved Stories and proves completion with your repository's own verification.

ForgePilot does not replace Warrant or your coding agent.

```text
Human
 ↓
ForgePilot
 ↓
Warrant Story
 ↓
Agent
 ↓
make verify
 ↓
Evidence
 ↓
ForgePilot
```

ForgePilot 是被動的 DAG 帳本：工單拆分完成後，外部 coding Agent 依 `forgepilot next` 的建議，依拓撲順序把一張 DAG 上的工作全部做完。ForgePilot 不啟動 Agent；它判定下一個合法動作、保存綁定確切 Candidate 的 Verification Evidence，並在工作完成的同一次交易內解鎖下游，讓工程工作能跨 Agent 與 session 延續。

## 目前狀態

[ADR-0040](docs/adr/0040-forgepilot-is-a-passive-dag-ledger.md) 的收斂已全部實作，產品的形狀是：

- 一份 **Goal Plan**（JSON）以 `forgepilot goal import` 一次建立 Goal 與整張 DAG；節點 ID 就是 Work Item ID。補單時改計畫檔再匯入，只能新增節點。
- Agent 跑迴圈：`next` 回傳唯一一個合法的下一步，Agent `start`、實作、`verify`。PENDING／READY 讀取時由依賴計算，不保存；同一 workspace 同時最多一件 RUNNING 或 VERIFYING。
- `verify` 在隔離的 detached worktree 對確切的 Candidate（已 commit 的 HEAD，或用 `--snapshot` 固定的未 commit 工作樹）執行受管理專案自己的 `make verify`，把 PASS／FAIL／INTERRUPTED 保存成綁定該 Candidate 的 Evidence。**PASS 即 DONE**，同一次交易解鎖下游；最後一件 DONE 時 Goal 自動完成。
- Goal 在匯入時可要求 **Approval**（`require_approval`）：PASS 先進 REVIEW，人 `review approve` 才 DONE，`review reject` 退回 RUNNING。
- 需要人判斷時，Agent 開一個 **Gate** 擋住該工作；換 session 的 Agent 由 `next`／`status` 得知正在等人。`status` 對每件未完成工作說明為何不能前進。
- 完全離線：不發出網路請求，除 Git 與 `make verify` 外不啟動程序。

沒有 Runner、supervised execution、Bootstrap 或 Story readiness review，也沒有 `work add`、`reconcile`、`migrate`、`--pr` 與 Review Policy——這些在 ADR-0040 移除，歷史規格留在 `docs/specs/`。

初始支援平台是 macOS 的本機檔案系統，使用 Go 1.25.5。state 由程序鎖與原子替換保護；其他平台尚未宣稱支援。

## 文件入口

| 文件 | 用途 |
|---|---|
| [Domain vocabulary](CONTEXT.md) | 統一核心名詞，避免把 Story 與 Work Item 混用 |
| [Architecture](docs/architecture.md) | 責任邊界、資料模型、生命週期、`next` 規則與持久化 |
| [Development plan](docs/development-plan.md) | CLI 契約、Goal Plan 格式、變更面驗證矩陣（其下是歷史 milestone 紀錄） |
| [Decision records](docs/adr/README.md) | 不易反轉的決定與其失效條件 |
| [架構圖](docs/diagrams/README.md) | 狀態機、分層、交易邊界與 `verify`／`review approve` 順序的視覺化 |
| [專案導覽](docs/show-me-forgepilot.html) | 一頁講完問題、核心概念與關鍵決定 |
| [AGENTS.md](AGENTS.md) | 接手這個 repo 的 Agent 該先知道的事：邊界、地雷與工作方式 |

## 安裝

需要 Go 1.25.5 或以上。ForgePilot 只用標準函式庫，沒有外部相依。

```bash
go install github.com/CarlLee1983/ForgePilot/cmd/forgepilot@<tag>
```

`<tag>` 換成要安裝的發行 tag，例如 `v0.4.0`——它是第一個收斂後的版本，也是本文描述的產品；`v0.3.1`（含）以前的 tag 是收斂前的產品，仍含 Runner 與舊 schema。

Skill 以手動複製安裝：把 repository 的 `skills/<agent>/forgepilot/` 目錄複製到對應 Agent 的 skill 目錄——Claude Code 為 `~/.claude/skills/`（`skills/claude-code/`），Codex 為 `~/.agents/skills/`（`skills/codex/`）。skill 教 Agent 照 `next → start → 實作 → verify` 推進整張 DAG，遇到需要人判斷的事開 Gate 並停下。

## 使用流程

在已有經人核准的 [Warrant](https://github.com/CarlLee1983/Warrant) Story（`specs/stories/<slug>.md`）的 repository root 執行：

```bash
forgepilot init
git add .gitignore && git commit -m "chore: ignore ForgePilot state"   # init 補上 .forgepilot/ 的 ignore 項目
forgepilot goal import plans/dbcli-dba.json
forgepilot next
forgepilot start DBCLI-001
forgepilot status
```

Goal 與整張依賴 DAG 由一份 Goal Plan（JSON）一次建立；節點 ID 就是 Work Item ID，`start`、`verify` 等指令都用它：

```json
{
  "goal": { "id": "dbcli-dba", "title": "DBA Workflow Support", "require_approval": false },
  "nodes": [
    { "id": "DBCLI-001", "story": "specs/stories/DBCLI-001.md", "depends_on": [] },
    { "id": "DBCLI-002", "story": "specs/stories/DBCLI-002.md", "depends_on": ["DBCLI-001"] }
  ]
}
```

ID 以英數開頭，其後可含英數、`.`、`_`、`-`，至多 64 字元，且不含 `..`、不以 `.` 或 `.lock` 結尾；節點 ID 在整個 state 內唯一。`story` 必須存在且位於 repository 的 `specs/stories/` 底下。計畫中任何一項驗證失敗（環、未知／自我／重複依賴、重複節點、不存在或不在 `specs/stories/` 底下的 Story 路徑、未知 JSON 欄位）整份都不寫入，錯誤訊息指出節點與欄位。節點順序是多件工作同時 READY 時 `next` 的推薦順序。完整的指令與計畫格式契約見 [docs/development-plan.md 的 CLI 契約](docs/development-plan.md#cli-契約)。

需要補單時修改計畫檔再匯入同一個 Goal：只接受新增節點（可依賴既有節點），計畫必須列出每個既有節點，其 `story`、`depends_on` 與 Goal 的屬性必須與原本相同；完全相同的計畫是無變化的成功，COMPLETED 或 CANCELLED 的 Goal 一律拒絕。若新加入節點的 Story 尚未提交，`goal import` 會在成功輸出後提示：用 `forgepilot verify <work-id> --snapshot` 驗證 working tree，或先 commit 再做 commit-mode verification。

此時保存的是 `DBCLI-001 = RUNNING`、`DBCLI-002 = NOT_STARTED`（`status` 顯示為 PENDING）。重新啟動 CLI 後，`next` 會推薦 `DBCLI-001` 的 `resume implementation`，而不是開始另一張 READY 工作。遇到 OPEN Gate 或等待 Human Review 而沒有其他可做工作時，`next` 明確輸出等待原因；它從不替 Agent 執行建議。給 Agent 的迴圈用 `forgepilot next --json`，每個欄位永遠存在（沒有值是空字串，`waiting` 是空陣列）。

### 驗證與完成

Agent 實作完成後驗證已提交的 revision：

```bash
forgepilot verify DBCLI-001
```

ForgePilot 會確認工作樹乾淨、解析目前的 HEAD，在 `.forgepilot/worktrees/` 底下建立該 commit 的 detached worktree，以呼叫者環境執行你的專案所定義的 `make verify`——toolchain 由該檢查自行固定，ForgePilot 不解析 runtime 宣告——然後保存 Evidence，輸出會印出 log 路徑（`.forgepilot/logs/`）。PASS 時，不要求 Approval 的 Goal 讓 `DBCLI-001` 直接成為 DONE，同一次交易把依賴它的工作轉為 READY，最後一件 DONE 時 Goal 自動 COMPLETED；要求 Approval 的 Goal 則進入 REVIEW。FAIL 或 INTERRUPTED 一律退回 RUNNING 讓 Agent 讀 log 修復。一次 verify 只為觸發它的那件工作留下 Evidence，DONE 是終態，不因後續 commit 重開。

因為 commit-mode 驗證跑在隔離的 checkout，**你的 `make verify` 必須能在全新 checkout 上執行**——需要 `.env`、本機已安裝依賴或既有 build cache 的專案會失敗。這與 CI 的要求相同。不帶 flag 的 `verify` 在工作樹不乾淨（含未追蹤檔案）時會拒絕執行，因為 commit 無法描述未提交的內容；要驗證那些內容則使用 snapshot mode：

```bash
forgepilot verify DBCLI-001 --snapshot
```

ForgePilot 會用 private Git index 建立 local immutable snapshot commit，收進 tracked staged／unstaged 修改、tracked deletion 與 non-ignored untracked files；ignored runtime artifacts 不會進入 snapshot（已 tracked 的內容除外）。current branch、HEAD、real index、staging state 與 working files 在 capture 前後保持不變。snapshot 以 `refs/forgepilot/snapshots/` 保留，不建立 branch 或 tag，也不發網路請求。

驗證中斷（Ctrl-C、關掉終端機、機器重開）不會留下假結果：下一次 `verify` 會把那次執行記為 INTERRUPTED 並退回 RUNNING；`next` 在那之前會回報 `RECOVER`。

### 遇到需要人決定的問題

工程過程中冒出 ForgePilot 與 Agent 都無權決定的問題時——架構取捨、範圍變更、有安全影響的選擇、規格本身有歧義——把它掛成一個 Gate，而不是讓 Agent 自己選一個繼續往下寫：

```bash
forgepilot gate open --work DBCLI-001 \
  --question "舊資料要不要回填？" --option "回填" --option "不回填" \
  --reason "規格沒有說明既有資料如何處理"
```

至少要列出兩個選項——一個選項不是問題。Gate 開著的期間，`DBCLI-001` 無法 `start` 也無法 `verify`，`next` 也不會推薦它，但它的狀態不變：RUNNING 的工作仍然是 RUNNING，阻擋是另一個維度的條件。

```bash
forgepilot gate resolve GATE-001 --option "不回填" --note "目前還沒有舊資料"
forgepilot gate cancel GATE-002 --reason "這個問題問錯了"
```

`resolve` 只接受列出的選項之一。選項全都不對時就 `cancel` 並附理由，再開一個問對的 Gate；`cancel` 同樣解除阻擋，但它留下不可變的紀錄並顯示在 `status`，所以撤銷無法悄悄發生。決策者身分預設取自 Git 的 `user.email`，可用 `--by` 覆寫——那是**自述**的身分，ForgePilot 不做認證。

### 人工審查

Goal 在 Goal Plan 中宣告 `"require_approval": true` 時，每件工作 PASS 後進入 REVIEW：

```bash
forgepilot review approve DBCLI-001 --note "解決的是對的問題"
forgepilot review reject DBCLI-001 --reason "錯誤路徑沒有處理"
```

REJECTED 把工作退回 RUNNING 讓 Agent 繼續修。ForgePilot **沒有完成指令**：`review approve` 在記錄審查的同一次交易內檢查完成條件——最新 Verification 為 PASS 且仍是目前的 Candidate、該工作無未解除 Gate、其 Goal 為 ACTIVE——全部滿足才 DONE 並解鎖下游。HEAD（或 snapshot 的工作樹）在 PASS 之後改變時，approve 會被拒絕並要求重新 `verify`；REVIEW 工作 stale 時，`next` 也會建議重新驗證。對不要求 Approval 的 Goal，`review` 一律拒絕。

DONE 是終態，沒有 reopen；需要重做就在計畫檔新增一個節點再匯入，讓「為什麼重做」有地方被記錄。放棄整個 Goal 用 `forgepilot goal cancel <goal-id> --reason <text>`。

### 舊版 state

State 只讀 schema 19，沒有升級指令。舊版寫下的 state 會被拒讀，訊息說明 schema 已斷代、此版本不讀舊 state。要延續未完成的工作，為每個 Goal 寫一份 Goal Plan，把舊的 `.forgepilot/` 移到封存位置後 `forgepilot init`，再對每份計畫執行 `forgepilot goal import`。

## 範圍

本機 CLI、Goal Plan 匯入、DAG 與讀取時計算的 readiness、Gate、Evidence、受規則約束的狀態轉移、deterministic 的 `next`、Story reference 與 `make verify` 整合。

不包含 Web UI、雲端服務、資料庫服務、daemon、排程器、啟動 coding agent 的 Runner、multi-agent 平行執行、token quota、generic workflow DSL、plugin framework、network API、遠端執行、通訊平台整合或 research／ML workflows。

ForgePilot 不自動產生 Story、不拆單、不以 LLM 判斷 PASS、不自動決定架構，也不自動 merge、release 或執行 production writes。

## 開發驗證

repository root 的 `make verify` 是 ForgePilot 自身的 canonical verification command；它會檢查格式、執行 `go vet`、測試與 CLI build。整合與最終驗收另跑 `go test -race -count=1 ./...`。
