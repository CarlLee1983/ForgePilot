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

**M1–M5、P0-001 Candidate Snapshot、P0-002 Work Item Status Summary、P0-003 Actionable Next 與 P1-004 Deterministic Runtime Resolution 已實作。**

M1 提供本機 CLI、Goal、Work Item、依賴、READY → RUNNING 與原子 JSON state。

M2 加上 `forgepilot verify`：在隔離的 detached worktree 對確切的 commit 執行受管理專案自己的 `make verify`，把結果保存成綁定該 revision 的 Evidence，PASS 進入 REVIEW、FAIL 退回 RUNNING。`status` 呈現最新一筆 Evidence 與它是否已對不上目前的 HEAD。M2 另提供 `forgepilot migrate`。

M3 接上工作真正能完成的那條線：`gate` 讓需要人判斷的問題被記錄下來並確實擋住工作，`review` 記錄人對某個確切 revision 的 APPROVED／REJECTED，`goal` 讓 Goal 能被暫停、取消或宣告完成。條件滿足時 `review approve` 在同一次交易內讓工作進入 DONE 並解鎖下游依賴——佇列因此第一次會前進。

M4 讓 Human Review 可以指明它發生在哪個 pull request 上：`review approve` 與 `review reject` 接受選填的 `--pr owner/name#number`，該值連同確切的 commit SHA 一起存進 Evidence。ForgePilot **不去 GitHub 查證**那個 PR——它不發出任何網路請求（[ADR-0010](docs/adr/0010-no-outbound-network-requests.md)），只驗格式。PR 是識別資料，不參與完成或 stale 的判定（[ADR-0011](docs/adr/0011-pr-identity-does-not-gate-completion.md)）；「HEAD 一變就是新的 review target」由既有的 SHA 比對成立。

P0-001 新增 `forgepilot verify WI-001 --snapshot`：不需要先製造 WIP commit，就能把 staged、unstaged、tracked deletion 與 non-ignored untracked content 固定成 immutable local Candidate，再讓 Verification 與 Human Review 綁定同一個 snapshot revision。既有不帶 flag 的 clean-HEAD verification 完全保留。

P0-002 新增 `forgepilot status --work WI-001 --summary`：以固定的少量行數呈現單一 Work Item 的 current status、最新 Verification／Human Review、未解除 Gate、Goal 狀態與 completion projection；既有 `forgepilot status` 的完整 history 輸出保持不變。

P0-003 擴充 `forgepilot next`：它優先建議續接已 RUNNING 的工作、修復最新 verification FAIL 的工作、或重新驗證 stale REVIEW candidate；只有沒有這些工作時才推薦最早的 READY Work Item。它只輸出下一個合法 agent action 與原因，不會自動執行 `start`／`verify` 或改寫 state。

P1-004 讓 `verify` 先在實際 Candidate checkout 讀取 repository 的 runtime/toolchain 宣告，再以本機已安裝且版本相符的 Node、Go、Python、Rust 建立該次 subprocess environment。caller shell 的預設版本不再決定驗證結果；宣告版本不可用時會在 Verification Run 開始前拒絕，不產生假的 FAIL Evidence。實際版本會保存於 Verification Evidence。

初始支援平台是 macOS 的本機檔案系統，使用 Go 1.25.5。state 由程序鎖與原子替換保護；其他平台尚未宣稱支援。

## 文件入口

| 文件 | 用途 |
|---|---|
| [Domain vocabulary](CONTEXT.md) | 統一核心名詞，避免把 Story 與 Work Item 混用 |
| [Architecture](docs/architecture.md) | 責任邊界、資料模型、狀態規則、持久化與 revision 契約 |
| [Development plan](docs/development-plan.md) | Milestone、開發順序、CLI 契約、測試與驗收清單 |
| [Decision records](docs/adr/README.md) | 不易反轉的決定與其失效條件 |
| [架構圖](docs/diagrams/README.md) | 狀態機、分層、交易邊界與兩條主要流程的視覺化 |
| [專案導覽](docs/show-me-forgepilot.html) | 一頁講完問題、核心概念、四個 milestone 與關鍵決定 |
| [AGENTS.md](AGENTS.md) | 接手這個 repo 的 Agent 該先知道的事：邊界、地雷與工作方式 |

## 安裝

需要 Go 1.25.5 或以上。ForgePilot 只用標準函式庫，沒有外部相依。

```bash
go install github.com/CarlLee1983/ForgePilot/cmd/forgepilot@latest
```

或從原始碼建置：

```bash
git clone https://github.com/CarlLee1983/ForgePilot.git
cd ForgePilot
go build -o forgepilot ./cmd/forgepilot
```

初始支援平台只有 macOS 的本機檔案系統——程序鎖使用 OS `flock`，其他平台尚未驗證行為相同，因此不宣稱支援。

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

此時保存的是 `WI-001 = RUNNING`、`WI-002 = PENDING`。重新啟動 CLI 後，`status` 應呈現相同狀態；`next` 會推薦 `WI-001` 的 `resume implementation`，而不是開始另一張 READY 工作。沒有進行中的工作時，它才會輸出像 `Action: forgepilot start WI-002` 的建議。遇到 fresh REVIEW 的 Human Review、OPEN Gate 或 BLOCKED Goal 而沒有其他可做工作時，`next` 明確輸出等待原因；它從不替 Agent 執行建議。

Agent 可以選擇驗證已提交 revision：

```bash
forgepilot verify WI-001
```

ForgePilot 會確認工作樹乾淨、解析目前的 HEAD，在 `.forgepilot/worktrees/` 底下建立該 commit 的 detached worktree，於其中解析 `mise.toml`、`.tool-versions`、language-specific version files 與支援的 ecosystem manifests，再用本機已安裝且符合宣告的 runtime 執行你的專案所定義的 `make verify`，然後保存 Evidence。通過則 `WI-001` 進入 REVIEW，失敗則退回 RUNNING 讓 Agent 繼續修。repository 沒有支援的 runtime declaration 時維持原本 PATH；有宣告但找不到符合版本時直接拒絕，不記成 Verification FAIL。

因為驗證跑在隔離的 checkout，**你的 `make verify` 必須能在全新 checkout 上執行**——需要 `.env`、本機已安裝依賴或既有 build cache 的專案會失敗。這與 CI 的要求相同。工作樹不乾淨（含未追蹤檔案）時 `verify` 會拒絕執行，因為 commit 無法描述未提交的內容。

若要直接驗證目前 working tree，不先建立 WIP commit：

```bash
forgepilot verify WI-001 --snapshot
```

ForgePilot 會用 private Git index 建立 local immutable snapshot commit，收進 tracked staged／unstaged 修改、tracked deletion 與 non-ignored untracked files；ignored runtime artifacts 不會進入 snapshot（已 tracked 的內容除外）。current branch、HEAD、real index、staging state 與 working files 在 capture 前後保持不變。snapshot 以 `refs/forgepilot/snapshots/` 保留，不建立 branch 或 tag，也不發網路請求。

Snapshot PASS 後，`status` 以目前 workspace 的自動 digest 判斷 freshness，不會因 snapshot revision 本來就不同於 HEAD 而立刻標 stale。`review approve`／`reject` 也會重算同一 digest：workspace 未變就把 review 綁回已驗證的 snapshot revision；若已改變則拒絕並要求重新執行 `verify WI-001 --snapshot`。

之後每新增一個 commit，`status` 就會把先前的 PASS 標示為 stale：它保留為歷史，但不適用於新的 revision，要重新取得適用的 Evidence 就再跑一次 `verify`。

驗證中斷（Ctrl-C、關掉終端機、機器重開）不會留下假結果：下一次 `verify` 會把那次執行記為 INTERRUPTED 並退回 RUNNING。這件事在任何拒絕之前發生，所以就算那件工作此刻被 Gate 擋著、`verify` 會被拒絕，中斷仍然被記錄下來——被擋住的是開始新的執行，不是記錄已經發生的事。

### 遇到需要人決定的問題

工程過程中冒出 ForgePilot 與 Agent 都無權決定的問題時——架構取捨、範圍變更、有安全影響的選擇、規格本身有歧義——把它掛成一個 Gate，而不是讓 Agent 自己選一個繼續往下寫：

```bash
forgepilot gate open --work WI-001 \
  --question "舊資料要不要回填？" --option "回填" --option "不回填" \
  --reason "規格沒有說明既有資料如何處理"
```

至少要列出兩個選項——一個選項不是問題。Gate 開著的期間，`WI-001` 無法 `start` 也無法 `verify`，`next` 也不會選中它，但它的狀態不變：RUNNING 的工作仍然是 RUNNING，阻擋是另一個維度的條件。

```bash
forgepilot gate resolve GATE-001 --option "不回填" --note "目前還沒有舊資料"
forgepilot gate cancel GATE-002 --reason "這個問題問錯了"
```

`resolve` 只接受列出的選項之一。選項全都不對時就 `cancel` 並附理由，再開一個問對的 Gate；`cancel` 同樣解除阻擋，但它留下不可變的紀錄並顯示在 `status`，所以撤銷無法悄悄發生。決策者身分預設取自 Git 的 `user.email`，可用 `--by` 覆寫——那是**自述**的身分，ForgePilot 不做認證。

### 人工審查與完成

```bash
forgepilot review approve WI-001 --note "解決的是對的問題"
forgepilot review approve WI-001 --pr carl/forgepilot#123
forgepilot review reject WI-001 --reason "錯誤路徑沒有處理"
```

REJECTED 把工作退回 RUNNING 讓 Agent 繼續修。ForgePilot **沒有完成指令**：DONE 只能是條件被滿足後的結果。`review approve` 在記錄審查的同一次交易內檢查四項條件——同一 revision 的最新 Verification 為 PASS、最新 Human Review 為 APPROVED、該工作無未解除 Gate、其 Goal 為 ACTIVE——全部滿足才進入 DONE，並在同一次交易內把因此滿足依賴的下游工作轉為 READY。

條件沒滿足時審查仍然被記錄（先審後驗是正當流程），只是不完成，而 `status` 會說出差在哪裡。DONE 是終態，不因後續 commit 重開，也沒有 reopen；需要重做就新增一件 Work Item，讓「為什麼重做」有地方被記錄。

### Goal 的暫停與終點

```bash
forgepilot goal block dbcli-dba --reason "方向不對，先停下來"
forgepilot goal unblock dbcli-dba
forgepilot goal complete dbcli-dba
forgepilot goal cancel dbcli-dba --reason "需求已撤回"
```

被擋住的 Goal 底下，正在進行的工作維持原狀——暫停不丟狀態，所以 `unblock` 之後一切照舊。它擋的是「開始新工作」與「到達 DONE」，不是「記錄已發生的事」：某次驗證進行中 Goal 被擋住，那次驗證跑完仍然記錄它的 Evidence。`goal complete` 是人手動宣告，且在尚有非 DONE 工作時被拒絕。

### 升級舊版的 state

舊版寫下的 state 會被新的 binary 拒讀並要求升級：

```bash
forgepilot migrate
```

它會先把原本的 snapshot 備份為以來源版本命名的 `state.json.v<n>.bak` 再升級，備份已存在時拒絕執行。跳過幾個版本沒關係，`migrate` 逐版套用，執行一次就會走到最新。升級是單向的，不提供 downgrade；要回頭就手動還原那個備份。

## 範圍

MVP 包含本機 CLI、Goal、Work Item、Gate、Evidence、受規則約束的狀態轉移、deterministic next-work selection、Story reference 與 `make verify` 整合。

不包含 Web UI、雲端服務、資料庫服務、daemon、multi-agent scheduler、Agent spawning、token quota、generic workflow DSL、plugin framework、network API、通訊平台整合或 research／ML workflows。

ForgePilot 不自動產生 Story、不以 LLM 判斷 PASS、不自動決定架構，也不自動 merge、release 或執行 production writes。

## 開發驗證

repository root 的 `make verify` 是 ForgePilot 自身的 canonical verification command；它會檢查格式、執行 `go vet`、測試與 CLI build。
