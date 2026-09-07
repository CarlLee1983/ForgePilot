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

**M1 — Durable Engineering Queue 已實作並通過 `make verify`。**它提供本機 CLI、Goal、Work Item、依賴、READY → RUNNING 與原子 JSON state；M1 不包含驗證、人工審查或完成工作指令。

初始支援平台是 macOS 的本機檔案系統，使用 Go 1.25.5。state 由程序鎖與原子替換保護；其他平台尚未宣稱支援。

## 文件入口

| 文件 | 用途 |
|---|---|
| [Domain vocabulary](CONTEXT.md) | 統一核心名詞，避免把 Story 與 Work Item 混用 |
| [Architecture](docs/architecture.md) | 責任邊界、資料模型、狀態規則、持久化與 revision 契約 |
| [Development plan](docs/development-plan.md) | Milestone、M1 開發順序、CLI 契約、測試與驗收清單 |

## M1 使用流程

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

M1 到此保存 `WI-001 = RUNNING`、`WI-002 = PENDING`。重新啟動 CLI 後，`status` 應呈現相同狀態。`next` 只選取 READY 工作，不負責續接已在 RUNNING 的工作。

真實的「驗證 → 人工審查 → DONE → 解鎖後續工作」須在 M3 完成後才能執行；M1 不提供繞過驗證或審查的完成捷徑。

## 範圍

MVP 包含本機 CLI、Goal、Work Item、Gate、Evidence、受規則約束的狀態轉移、deterministic next-work selection、Story reference 與 `make verify` 整合。

不包含 Web UI、雲端服務、資料庫服務、daemon、multi-agent scheduler、Agent spawning、token quota、generic workflow DSL、plugin framework、network API、通訊平台整合或 research／ML workflows。

ForgePilot 不自動產生 Story、不以 LLM 判斷 PASS、不自動決定架構，也不自動 merge、release 或執行 production writes。

## 開發驗證

M1 在 repository root 提供 `make verify`，作為 ForgePilot 自身的 canonical verification command；它會檢查格式、執行 `go vet`、測試與 CLI build。
