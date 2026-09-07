# Architecture

## 文件狀態與範圍

M1 已依本文件實作；M2 以後仍是規劃。原始專案需求是產品邊界；標記為「待定」的事項不得視為已決定的功能。

核心名詞只在 [CONTEXT.md](../CONTEXT.md) 定義；Milestone 與驗收只在 [development-plan.md](development-plan.md) 維護。

## Authority boundaries

| 擁有者 | 責任 |
|---|---|
| Human | Goal approval、架構／範圍／安全判斷、production operation、merge／release authorization |
| ForgePilot | Goal、工作佇列、狀態、Gate、Evidence index、revision identity、next-work selection |
| ForgeFlowV2 | Story schema、acceptance criteria、工程流程、coding standards、測試與驗證契約、人工審查原則 |
| Repository | 程式碼、tests、formatters、linters、type／architecture checks、canonical `make verify` |
| Agent | 讀取 Story、實作、修復、推理與工具操作 |

Work Item 只保存 `story_ref`，不複製 Story requirements。ForgePilot 不讀取程式碼後自行判斷正確性，也不替代 ForgeFlowV2 的工程 lifecycle。

Breaking change、architecture trade-off、security-sensitive decision、production operation、destructive action、scope expansion、ambiguous requirement、merge／release authorization 都需要明確 Human Decision。M3 提供 Gate 紀錄；M1／M2 尚不能在產品內完整表示這些決策，不得因尚未實作而視為已獲授權。

## Implementation boundaries

M1 使用 Go 1.25.5 與標準函式庫，module 為 `github.com/carl/forgepilot`。CLI 只負責參數、呈現與錯誤映射；domain rules 集中管理，不直接呼叫 filesystem、Git 或 subprocess。

建議從四個 cohesive packages 開始：

| Package | 責任 |
|---|---|
| `internal/cli` | CLI 解析、應用流程串接、輸出；不自行決定 transition 合法性 |
| `internal/work` | Goal／Work Item types、transition policy、依賴與 selection rules |
| `internal/repository` | Repository 與 Story 路徑檢查；M2 再加入 revision 與 verification adapters |
| `internal/storage` | State snapshot、交易鎖、JSON decode／validate 與原子保存 |

由入口組裝依賴；時間、I/O 與外部執行結果透過明確參數或必要的小介面進入規則。不要為每個 entity 預先建立一組 Save／Get repository interfaces。

單一 JSON snapshot 的一致性邊界涵蓋 Goal 與 Work Item；不能以多次獨立 Save 取代一次受鎖保護的操作。M2／M3 有實際需求時才加入 Evidence 與 Gate 的具體邊界。

## Domain data

以下是目標模型，不代表所有欄位都必須在 M1 序列化。M1 只持久化該階段使用的資料；未啟用欄位不填入假的 revision 或 evidence。

| 物件 | 目標欄位 | 啟用階段 |
|---|---|---|
| Goal | `id`, `title`, `description`, `repository`, `status`, `created_at`, `updated_at` | M1 |
| Work Item | `id`, `goal_id`, `story_ref`, `status`, `depends_on`, `created_at`, `updated_at` | M1 |
| Work Item run | `current_run`（revision、worktree path、started_at；閒置時為 null） | M2 |
| Work Item claim | `claimed_by` | 待定；M1 不建立 Agent 身分或 lease 協定 |
| Gate | `id`, `goal_id`, `work_item_id`, `question`, `reason`, `options`, `status`，以及決策、決策者、時間的紀錄 | M3；詳細 schema 待定 |
| Evidence | `id`（`EV-001`）、`type`、repository、Work Item、Story、完整 commit SHA、實際 command、exit code、`result`、timestamp | M2 起 |

Goal statuses：`ACTIVE`, `BLOCKED`, `COMPLETED`, `CANCELLED`。M1 僅建立 ACTIVE Goal，不提供其他 Goal lifecycle 操作。

Work Item statuses：`PENDING`, `READY`, `RUNNING`, `VERIFYING`, `REVIEW`, `WAITING_HUMAN`, `BLOCKED`, `DONE`。M1 可到達的狀態只有前三者。

Gate statuses：`OPEN`, `RESOLVED`, `CANCELLED`。Gate resolution 不等同於 Human Review，也不授予 merge／release 權限。

## Repository 與 Story identity

M1 每個 `.forgepilot/` 管理一個 repository，可以包含多個 Goal；不建立跨 repository 全域佇列。

`init` 的作用位置是使用者指定工作的 repository root。後續命令從目前目錄向上尋找最近的 `.forgepilot/`；找不到時明確要求先 init。Goal 的 repository 必須與該 state root 一致。

Story reference 為 repository-relative path，必須存在於 `specs/stories/` 內。可指向 ForgeFlow 所使用的檔案或目錄；不猜測其 business schema。路徑正規化與 symlink 解析後仍須在允許範圍，拒絕逃出 repository 的 reference。

M1 不要求受管理專案已提供 `make verify`；該檢查與執行屬於 M2。ForgePilot 自身的 `make verify` 則由 M1 交付。

Repository 路徑搬移、跨 worktree 共用 state 與 repository identity migration 尚未定義；M1 不默默重綁到其他 repository。

M2 為驗證建立的 worktree 不在此限：它們是短暫的、detached 的，且不含自己的 `.forgepilot/`。State 永遠留在主工作樹，ForgePilot 只把 `make verify` 執行於該處。

## Dependency 與 selection rules

- Work Item ID 在本機 state 內唯一，由受鎖保護的新增操作配發，例如 `WI-001`。
- `--depends-on` 使用 Work Item ID，不使用 Story path 或 Story 名稱。
- 依賴必須已存在且屬於同一 Goal；拒絕未知、自我或重複依賴，禁止 cycle。
- M1 只允許建立時指定依賴，不提供修改或刪除操作。依賴指向既有節點，因此新增操作不能產生 cycle；載入 state 仍需驗證完整資料一致性。
- 新增時全部依賴 DONE 則為 READY，否則 PENDING。無依賴時視為已滿足。
- 候選必須屬於 ACTIVE Goal、為 READY、全部依賴 DONE，且沒有 unresolved Gate。Gate 條件在 M3 啟用。
- 候選按 `created_at` 升冪排序，同時間以 Work Item ID 的配發序號升冪決勝。
- `next` 是純查詢，不 claim、不 start、不改寫 READY 狀態。
- `start` 必須在寫交易內重查 Goal 與依賴；不能只相信保存的 READY 值。
- 不限制全域只能存在一個 RUNNING；`status` 必須能列出多個進行中的工作。

M3 完成某個 Work Item 時，在同一 state transaction 中更新受影響的 PENDING 工作；只有全部依賴 DONE 且符合適用條件時才 READY。

## Central transition policy

| Transition | 條件與階段 |
|---|---|
| 建立 → PENDING／READY | 依賴規則決定；M1 |
| PENDING → READY | 全部依賴 DONE，且符合 Goal／Gate 條件；M1 測試規則，M3 提供真實完成來源 |
| READY → RUNNING | ACTIVE Goal、依賴 DONE、無 unresolved Gate；M1 |
| RUNNING → VERIFYING | 允許開始 canonical verification；M2 |
| VERIFYING → REVIEW | PASS evidence 對應受驗證且目前有效的 revision；M2 |
| VERIFYING → RUNNING | Verification FAIL，或回收中斷的 Verification Run；M2 |
| REVIEW → VERIFYING | 由明確的 `verify` 命令觸發；stale 本身不改變狀態；M2 |
| REVIEW → DONE | 同一有效 revision 有 Verification PASS 與 explicit Human Review APPROVED，且無 unresolved Gate；M3 |
| RUNNING → BLOCKED | M3；建立阻擋、解除與重試操作須在該階段開工前定案 |
| 非終態 → WAITING_HUMAN | 建立需要 Human Decision 的 Gate；M3 |

非法 transition 回傳明確 domain error，不能偷偷改成另一個操作。M1 不提供任意 state setter、complete 或測試專用 approve 指令。

`WAITING_HUMAN` 的恢復規則、多個 Gate、Gate cancellation 是否解除阻擋、BLOCKED recovery，以及 DONE 是否允許重新開啟，都必須在 M3 開工前補齊。不能把「Gate 已 resolve」直接等同於 READY 或 DONE。

## Durable local storage

M1 storage layout：

```text
.forgepilot/
├── state.json
└── locks/
```

State snapshot 包含 `schema_version`、Goal、Work Item 與必要 ID 配發資訊。Timestamp 使用 UTC，保留足夠精度；規則測試使用可控制的時間。

每次修改遵循：

```text
取得固定 lock file 的程序鎖
→ 讀取、decode、驗證 snapshot
→ 執行 domain operation
→ 更新受影響狀態
→ 寫入同目錄暫存檔並同步
→ 原子替換 state.json，依平台需要同步目錄
→ 釋放鎖
```

鎖涵蓋 read-modify-write 全程；只有 atomic rename 不足以防止 lost update。鎖生命週期不能依賴 Agent session，也不能只靠一個可能殘留的 PID 檔。M1 初始支援 macOS 本機檔案系統，使用 OS 在程序終止時自動釋放的 `flock` advisory lock；跨平台支援在驗證相同行為後才加入。

`next`／`status` 讀取完整 snapshot，不寫入狀態。成功回應只能發生在保存成功之後。寫入失敗須留下可讀的舊 snapshot，或完整的新 snapshot，不得留下截斷 JSON。

其他要求：

- 重複 `init` 不覆寫 state；初始化失敗後重試不得破壞既有資料。
- `.gitignore` 僅補上缺少的 `.forgepilot/` entry，保留既有內容。
- JSON 損毀、資料不一致與不支援的 schema version 明確拒讀，不清空重建。
- 後續新增 schema 時，舊程式必須拒絕較新版本，避免重新保存時丟失未知欄位。
- M1 不加入 migration framework；需要 migration 的階段再設計備份、升級與回復程序。
- State 是本機信任資料，不宣稱具有防竄改或身份認證能力。

### M2 storage layout 與 schema 升級

```text
.forgepilot/
├── state.json
├── locks/
│   └── verify-<work-id>
└── worktrees/
    └── <work-id>-<short-sha>/
```

Schema v2 相對 v1 只有新增：`schema_version` 改為 2、根層加入 `evidence` 陣列與 `next_evidence_id`、Work Item 加入 `current_run`。無欄位刪除或語意改變。

`internal/work` 的版本檢查是嚴格相等，因此 M2 binary 同樣拒讀 v1 state。升級不自動發生，必須由使用者明確執行 `forgepilot migrate`：該指令先把 `state.json` 備份為 `state.json.v1.bak`，備份檔已存在時拒絕執行而非覆寫；對已是 v2 的 state 回報「已是最新版本」並以 exit 0 結束，使重複執行安全。不提供 downgrade——v2 的 Evidence 在 v1 無容身之處，要回頭的人手動還原備份。

`worktrees/` 由 ForgePilot 完全掌控：每次 `verify` 前先 `git worktree prune` 清除被強制終止的程序留下的殘骸，結束後一律 `git worktree remove --force`，PASS 與 FAIL 皆刪除。

## Verification 與 exact revision：M2 起

Verification Evidence 必須至少保存 repository、Work Item、Story、完整 commit SHA、實際 command、exit code 與 timestamp。`result` 有三個值：`PASS`、`FAIL`、`INTERRUPTED`。FAIL 與 INTERRUPTED 同樣 append；既有 Evidence 不覆寫。INTERRUPTED 表示未產生結果，不得視為 FAIL。

受管理專案未提供 `make verify` target 時，`verify` 拒絕執行並回傳明確錯誤，不 append 任何 Evidence——那是無法驗證，不是驗證失敗。

Canonical verification 固定為 repository 的 `make verify`；不開放任意 shell command template。Domain 接收結果，不直接執行 shell。

Human Review Evidence 同樣綁定 repository、Story 與 exact commit。進入 DONE 必須同時檢查同一 revision 的 PASS 與 APPROVED；只檢查 review SHA 不足夠。

HEAD 改變後舊 PASS／APPROVED 保留為歷史，但不可套用到新 revision。新的 review target 必須重新驗證與審查。PR review 至少另綁定 PR number 與 HEAD SHA；M4 才實作。

### M2 開工前定案（已完成）

1. **Dirty worktree policy**：驗證標的只能是 commit。工作樹不乾淨即拒絕執行 `verify`，不記錄任何 Evidence。乾淨採嚴格定義——tracked 檔案無修改、無 staged 變更、且無 untracked 檔案；ignored 檔案不計入。
2. **隔離方式**：`git worktree add --detach <SHA>` 到 `.forgepilot/worktrees/` 下的暫存目錄執行，不在主工作樹原地驗證。見 [ADR-0002](adr/0002-verify-in-detached-worktree.md)，其中含對受管理專案強加的「`make verify` 必須能在全新 checkout 上執行」契約。
3. **Interruption 與 timeout**：Verification Run 期間額外持有 `locks/verify-<work-id>` 的 flock 作為存活標記；不設逾時上限。孤兒 VERIFYING 由下一次 `verify` 的開頭交易回收，append 一筆 INTERRUPTED Evidence 後退回 RUNNING，不推斷 PASS 或 FAIL。見 [ADR-0004](adr/0004-verifying-liveness-via-flock.md)。
4. **Crash consistency**：Evidence 保存在 `state.json` 內，與 Work Item 共用同一次受鎖的原子替換，因此不存在單邊寫入的中間態。見 [ADR-0001](adr/0001-evidence-in-state-snapshot.md)。
5. **Stale 觸發**：Stale 定義為最新一筆 Verification Evidence 的 SHA 不等於目前 HEAD。它不造成任何自動 transition；REVIEW → VERIFYING 只由明確的 `verify` 命令推動。`next` 與 `status` 呈現 stale 但不寫入。

Revision 只存在於 Evidence 與 `current_run`，Work Item 本身不保存 `target_revision`；該欄位的刪除見 [ADR-0003](adr/0003-no-work-item-target-revision.md)。

## Human decision：M3 起

OPEN Gate 必須阻擋工作推進。Resolve 需保存明確 Decision 與時間；不能由 Agent 推測選項或以逾時當同意。

`review approve` 是 explicit CLI action，ForgePilot 不自動 approve。CLI action 本身只能記錄聲明，不能證明執行者為人；信任模式、操作者身分與是否需要額外認證須於 M3 決定。

建議 DONE 保留「在某 revision 完成」的歷史意義，不因 repository 每次新增 commit 就重開所有已完成工作；新 target 不能沿用舊 Evidence。此 DONE／reopen policy 尚待 M3 定案。
