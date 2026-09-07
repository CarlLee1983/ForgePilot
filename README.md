# ForgePilot

ForgePilot tells your engineering agents what work is actionable next.

ForgeFlowV2 defines how that work must be engineered and verified.

ForgePilot does not replace ForgeFlowV2 or your coding agent.

```text
Human
 ↓
ForgePilot
 ↓
ForgeFlow Story
 ↓
Agent
 ↓
make verify
 ↓
Evidence
 ↓
ForgePilot
```

ForgePilot 是服務 AI-assisted software engineering 的 Engineering Control Plane：管理工作佇列、狀態、人工決策與驗證證據，讓工程工作能跨 Agent 與 session 延續。

## 目前狀態

**M1 — Durable Engineering Queue 與 M2 — Verification Evidence 已實作並通過 `make verify`。**

M1 提供本機 CLI、Goal、Work Item、依賴、READY → RUNNING 與原子 JSON state。

M2 加上 `forgepilot verify`：在隔離的 detached worktree 對確切的 commit 執行受管理專案自己的 `make verify`，把結果保存成綁定該 revision 的 Evidence，PASS 進入 REVIEW、FAIL 退回 RUNNING。`status` 呈現最新一筆 Evidence 與它是否已對不上目前的 HEAD。M2 另提供 `forgepilot migrate`，用來升級 M1 留下的 state。

**尚未實作**：Gate、Human Decision、Human Review、DONE 與依賴解鎖（M3），以及 PR review target（M4）。目前沒有任何路徑能讓工作到達 DONE。

初始支援平台是 macOS 的本機檔案系統，使用 Go 1.25.5。state 由程序鎖與原子替換保護；其他平台尚未宣稱支援。

## 文件入口

| 文件 | 用途 |
|---|---|
| [Domain vocabulary](CONTEXT.md) | 統一核心名詞，避免把 Story 與 Work Item 混用 |
| [Architecture](docs/architecture.md) | 責任邊界、資料模型、狀態規則、持久化與 revision 契約 |
| [Development plan](docs/development-plan.md) | Milestone、開發順序、CLI 契約、測試與驗收清單 |
| [Decision records](docs/adr/) | 不易反轉的決定與其失效條件 |

## 使用流程

在已有 ForgeFlow Stories 的 repository root 執行：

```bash
forgepilot init
forgepilot goal create --id dbcli-dba --title "DBA Workflow Support"
forgepilot work add --goal dbcli-dba --story specs/stories/DBCLI-001
# 假設上一個指令回傳 WI-001。
forgepilot work add --goal dbcli-dba --story specs/stories/DBCLI-002 --depends-on WI-001
forgepilot next
forgepilot start WI-001
forgepilot status
```

`--depends-on` 與 `start` 使用 Work Item ID；`--story` 使用 Story 路徑。Agent 讀取 Story，依 ForgeFlowV2 執行工程工作。

此時保存的是 `WI-001 = RUNNING`、`WI-002 = PENDING`。重新啟動 CLI 後，`status` 應呈現相同狀態。`next` 只選取 READY 工作，不負責續接已在 RUNNING 的工作。

Agent 完成實作並 commit 之後：

```bash
forgepilot verify WI-001
```

ForgePilot 會確認工作樹乾淨、解析目前的 HEAD，在 `.forgepilot/worktrees/` 底下建立該 commit 的 detached worktree，於其中執行你的專案所定義的 `make verify`，然後保存 Evidence。通過則 `WI-001` 進入 REVIEW，失敗則退回 RUNNING 讓 Agent 繼續修。

因為驗證跑在隔離的 checkout，**你的 `make verify` 必須能在全新 checkout 上執行**——需要 `.env`、本機已安裝依賴或既有 build cache 的專案會失敗。這與 CI 的要求相同。工作樹不乾淨（含未追蹤檔案）時 `verify` 會拒絕執行，因為 commit 無法描述未提交的內容。

之後每新增一個 commit，`status` 就會把先前的 PASS 標示為 stale：它保留為歷史，但不適用於新的 revision，要重新取得適用的 Evidence 就再跑一次 `verify`。

驗證中斷（Ctrl-C、關掉終端機、機器重開）不會留下假結果：下一次 `verify` 會把那次執行記為 INTERRUPTED 並退回 RUNNING。

真實的「驗證 → 人工審查 → DONE → 解鎖後續工作」須在 M3 完成後才能執行；目前不提供繞過審查的完成捷徑。

### 從 M1 升級

M1 寫下的 state 是 schema v1，M2 的 binary 會拒讀並要求升級：

```bash
forgepilot migrate
```

它會先把原本的 snapshot 備份為 `state.json.v1.bak` 再升級。升級是單向的，不提供 downgrade；要回到 M1 就手動還原那個備份。

## 範圍

MVP 包含本機 CLI、Goal、Work Item、Gate、Evidence、受規則約束的狀態轉移、deterministic next-work selection、Story reference 與 `make verify` 整合。

不包含 Web UI、雲端服務、資料庫服務、daemon、multi-agent scheduler、Agent spawning、token quota、generic workflow DSL、plugin framework、network API、通訊平台整合或 research／ML workflows。

ForgePilot 不自動產生 Story、不以 LLM 判斷 PASS、不自動決定架構，也不自動 merge、release 或執行 production writes。

## 開發驗證

repository root 的 `make verify` 是 ForgePilot 自身的 canonical verification command；它會檢查格式、執行 `go vet`、測試與 CLI build。
