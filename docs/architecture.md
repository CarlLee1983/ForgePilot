# Architecture

## 文件狀態與範圍

MVP 的 M1–M3 與 M4 已依本文件實作；M5 為開工前定案階段，尚未開始實作。原始專案需求是產品邊界；標記為「待定」的事項不得視為已決定的功能。

核心名詞只在 [CONTEXT.md](../CONTEXT.md) 定義；Milestone 與驗收只在 [development-plan.md](development-plan.md) 維護。

本文件的視覺化見 [diagrams/](diagrams/README.md)：狀態機、分層與依賴方向、交易邊界，以及 `verify` 與 `review approve` 的順序。圖與本文件衝突時以本文件與程式碼為準。

## Authority boundaries

| 擁有者 | 責任 |
|---|---|
| Human | Goal approval、架構／範圍／安全判斷、production operation、merge／release authorization |
| ForgePilot | Goal、工作佇列、狀態、Gate、Evidence index、revision identity、next-work selection |
| ForgeFlowV2 | Story schema、acceptance criteria、工程流程、coding standards、測試與驗證契約、人工審查原則 |
| Repository | 程式碼、tests、formatters、linters、type／architecture checks、canonical `make verify` |
| Agent | 讀取 Story、實作、修復、推理與工具操作 |

Work Item 只保存 `story_ref`，不複製 Story requirements。ForgePilot 不讀取程式碼後自行判斷正確性，也不替代 ForgeFlowV2 的工程 lifecycle。

Breaking change、architecture trade-off、security-sensitive decision、production operation、destructive action、scope expansion、ambiguous requirement、merge／release authorization 都需要明確 Human Decision。M3 起這些決策以 Gate 表示並保存；merge／release authorization 仍不在產品範圍內，Gate resolution 不授予該權限。

## Implementation boundaries

M1 使用 Go 1.25.5 與標準函式庫，module 為 `github.com/CarlLee1983/ForgePilot`。CLI 只負責參數、呈現與錯誤映射；domain rules 集中管理，不直接呼叫 filesystem、Git 或 subprocess。

建議從四個 cohesive packages 開始：

| Package | 責任 |
|---|---|
| `internal/cli` | CLI 解析、應用流程串接、輸出；不自行決定 transition 合法性 |
| `internal/work` | Goal／Work Item types、transition policy、依賴與 selection rules |
| `internal/repository` | Repository 與 Story 路徑檢查；M2 再加入 revision 與 verification adapters |
| `internal/storage` | State snapshot、交易鎖、JSON decode／validate 與原子保存 |

由入口組裝依賴；時間、I/O 與外部執行結果透過明確參數或必要的小介面進入規則。不要為每個 entity 預先建立一組 Save／Get repository interfaces。

單一 JSON snapshot 的一致性邊界涵蓋 Goal、Work Item、Evidence 與 Gate：四者共用同一次受鎖的原子替換，不能以多次獨立 Save 取代。完成一件工作與解鎖其下游必須落在同一次交易內，否則讀取者會看到「A 已 DONE 但 B 仍 PENDING」的中間狀態。

## Domain data

以下是目標模型，不代表所有欄位都必須在 M1 序列化。M1 只持久化該階段使用的資料；未啟用欄位不填入假的 revision 或 evidence。

| 物件 | 目標欄位 | 啟用階段 |
|---|---|---|
| Goal | `id`, `title`, `description`, `repository`, `status`, `created_at`, `updated_at` | M1 |
| Work Item | `id`, `goal_id`, `story_ref`, `status`, `depends_on`, `created_at`, `updated_at` | M1 |
| Work Item run | `current_run`（revision、worktree path、started_at、log path；閒置時為 null） | M2；`log path` 於 M5 加入（schema v5） |
| Work Item claim | `claimed_by` | 待定；M1 不建立 Agent 身分或 lease 協定 |
| Gate | `id`（`GATE-001`）、`work_item_id`、`question`、`rationale`（開啟時的說明）、`options`（至少兩個）、`status`、`opened_at`，以及關閉時的 `choice`／`note`（resolve）或 `reason`（cancel）、`decided_by`、`decided_at` | M3 |
| Evidence | `id`（`EV-001`）、`type`、repository、Work Item、Story、完整 commit SHA、`result`、timestamp；verification 另有 `command` 與 `exit_code`，review 另有 `reviewer`、`note` 與 M4 起的選填 `pr` | M2 起 |

Goal statuses：`ACTIVE`, `BLOCKED`, `COMPLETED`, `CANCELLED`。M1 僅建立 ACTIVE Goal，不提供其他 Goal lifecycle 操作。

Work Item statuses：`PENDING`, `READY`, `RUNNING`, `VERIFYING`, `REVIEW`, `DONE`。M1 可到達的狀態只有前三者，M2 加上 `VERIFYING` 與 `REVIEW`，`DONE` 自 M3 起由 `review approve` 在條件滿足時達成。

沒有 `BLOCKED` 或 `WAITING_HUMAN`：阻擋由「該 Work Item 有沒有未解除的 Gate」表達，不佔用狀態欄。狀態描述工作在生命週期的位置，Gate 是另一個維度的條件；兩處各表達一次同一事實，就會需要「記住進入阻擋前是什麼狀態」這種只為修補覆寫而存在的欄位。詳見 [ADR-0007](adr/0007-blocking-is-not-a-status.md)。Goal 的 `BLOCKED` 保留，它是刻意的不對稱——擋整個 Goal 用 Goal 狀態，擋一件工作用 Gate。

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
| REVIEW → RUNNING | Human Review REJECTED；M3 |
| REVIEW → DONE | 同一 revision 的最新 Verification 為 PASS 且最新 Human Review 為 APPROVED，且無 unresolved Gate；由 `review approve` 在同一交易內達成；M3 |

非法 transition 回傳明確 domain error，不能偷偷改成另一個操作。M1 不提供任意 state setter、complete 或測試專用 approve 指令。

開啟或關閉 Gate 不是 transition：它不改變 Work Item 的狀態，只改變它能否推進。Gate 可以開在任何非 DONE 的工作上。

DONE 是終態，不因後續 commit 重開，也沒有 reopen 操作；需要重做就新增一件 Work Item，讓「為什麼重做」有地方被記錄。詳見 [ADR-0006](adr/0006-done-is-terminal.md)。不能把「Gate 已 resolve」直接等同於 READY 或 DONE。

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

`internal/work` 的版本檢查是嚴格相等，因此新版 binary 一律拒讀較舊的 state，舊版 binary 也拒讀較新的。升級不自動發生，必須由使用者明確執行 `forgepilot migrate`：該指令先把 `state.json` 備份為以來源版本命名的 `state.json.v<n>.bak`，備份檔已存在時拒絕執行而非覆寫；對已是最新版本的 state 回報「已是最新版本」並以 exit 0 結束，使重複執行安全。不提供 downgrade——新版的 Evidence 與 Gate 在舊版無容身之處，要回頭的人手動還原備份。

`migrate` 逐版套用升級步驟，因此跳過某一版的使用者只需執行一次即可走到最新，而不是按跳過的版本數重跑。

`worktrees/` 由 ForgePilot 完全掌控：每次 `verify` 前先 `git worktree prune` 清除被強制終止的程序留下的殘骸，結束後一律 `git worktree remove --force`，PASS 與 FAIL 皆刪除。

### M5 storage layout 與 schema 升級

```text
.forgepilot/
├── state.json
├── locks/
│   └── verify-<work-id>
├── worktrees/
│   └── <work-id>-<short-sha>/
└── logs/
    └── <work-id>-<short-sha>-<started-at>.log
```

Schema v5 相對 v4 只有新增：`schema_version` 改為 5，Work Item 的 `current_run` 加入 `log path`。無欄位刪除或語意改變。沿用既有升級契約——`migrate` 備份為 `state.json.v4.bak` 後升級，備份已存在時拒絕；已是最新版本者回報並以 exit 0 結束。

`logs/` 由 ForgePilot 寫入、不自動清理，也不提供清理指令；`.forgepilot/` 已被忽略，成長由使用者處理。理由與備選見 [ADR-0012](adr/0012-verification-log-outside-state.md)。

## Verification 與 exact revision：M2 起

Verification Evidence 必須至少保存 repository、Work Item、Story、完整 commit SHA、實際 command、exit code 與 timestamp。`result` 有三個值：`PASS`、`FAIL`、`INTERRUPTED`。INTERRUPTED 沒有 exit code（欄位為 null）——未產生結果就沒有結果碼，填 0 會被讀成成功。FAIL 與 INTERRUPTED 同樣 append；既有 Evidence 不覆寫。INTERRUPTED 表示未產生結果，不得視為 FAIL。

受驗 revision 未提供 `make verify` target 時，`verify` 拒絕執行並回傳明確錯誤，不 append 任何 Evidence——那是無法驗證，不是驗證失敗。此檢查必須在隔離 checkout 內執行：被 gitignore 的 Makefile 會讓主工作樹看起來可驗證，而受驗的 commit 其實沒有 canonical 檢查。

已知限制：只終止 `make` 子程序而 ForgePilot 本身存活時，ForgePilot 收到的是一個 exit code，會記為 FAIL。中斷語意僅涵蓋 ForgePilot 自身被終止的情況。

Canonical verification 固定為 repository 的 `make verify`；不開放任意 shell command template。Domain 接收結果，不直接執行 shell。

Human Review Evidence 同樣綁定 repository、Story 與 exact commit。進入 DONE 必須同時檢查同一 revision 的 PASS 與 APPROVED；只檢查 review SHA 不足夠。

HEAD 改變後舊 PASS／APPROVED 保留為歷史，但不可套用到新 revision。新的 review target 必須重新驗證與審查。M4 讓 Human Review 額外攜帶 PR Reference，見下方「PR review target：M4 起」。

### M2 開工前定案（已完成）

1. **Dirty worktree policy**：驗證標的只能是 commit。工作樹不乾淨即拒絕執行 `verify`，不為該次執行記錄任何 Evidence。乾淨採嚴格定義——tracked 檔案無修改、無 staged 變更、且無 untracked 檔案；ignored 檔案不計入。
2. **隔離方式**：`git worktree add --detach <SHA>` 到 `.forgepilot/worktrees/` 下的暫存目錄執行，不在主工作樹原地驗證。canonical 檢查的存在性也在該 checkout 內判斷，不在主工作樹。見 [ADR-0002](adr/0002-verify-in-detached-worktree.md)，其中含對受管理專案強加的「`make verify` 必須能在全新 checkout 上執行」契約。
3. **Interruption 與 timeout**：Verification Run 期間額外持有 `locks/verify-<work-id>` 的 flock 作為存活標記；不設逾時上限。孤兒 VERIFYING 由下一次 `verify` 的開頭交易回收，append 一筆 INTERRUPTED Evidence 後退回 RUNNING，不推斷 PASS 或 FAIL。見 [ADR-0004](adr/0004-verifying-liveness-via-flock.md)。

   回收發生在任何拒絕之前，且不受 Gate 或 Goal 狀態約束——中斷是已經發生的事實，而阻擋擋的是開始新的執行。因此被拒絕的 `verify` 在有孤兒時會寫入那一筆 INTERRUPTED（並印在輸出上），在沒有孤兒時什麼都不寫。見 [ADR-0009](adr/0009-reclaim-before-refusing.md)。
4. **Crash consistency**：Evidence 保存在 `state.json` 內，與 Work Item 共用同一次受鎖的原子替換，因此不存在單邊寫入的中間態。見 [ADR-0001](adr/0001-evidence-in-state-snapshot.md)。
5. **Stale 觸發**：Stale 定義為最新一筆 Verification Evidence 的 SHA 不等於目前 HEAD。它不造成任何自動 transition；REVIEW → VERIFYING 只由明確的 `verify` 命令推動。`next` 與 `status` 呈現 stale 但不寫入。

Revision 只存在於 Evidence 與 `current_run`，Work Item 本身不保存 `target_revision`；該欄位的刪除見 [ADR-0003](adr/0003-no-work-item-target-revision.md)。

M5 起，canonical 檢查的輸出邊執行邊串流寫進 state 之外的 `.forgepilot/logs/`，以那次執行為鍵命名；`current_run` 對應多出的 `log path`，一如既有的 `WorktreePath`。Evidence 不新增任何指向這份輸出的欄位。輸出是診斷材料，不是結論——結論仍只在 Evidence。理由與備選見 [ADR-0012](adr/0012-verification-log-outside-state.md)。

### M5 開工前定案（已完成）

1. **檔名以 run 為鍵**：`<work-id>-<short-sha>-<started-at>.log`，不以 Evidence ID 命名——Evidence ID 在交易結束後才發號，串流開檔當下還不存在。
2. **串流而非事後一次寫**：canonical runner 把子行程輸出邊執行邊接到檔案，INTERRUPTED 因此第一次留得下截斷輸出；事後一次寫最小，但拿不到中斷時的內容，執行期間也依然無聲。
3. **開檔失敗即中止**：log 檔開不起來時 `verify` 中止並回報，不靜默降級、不留 Evidence；這發生在準備階段，不受「回收不得否決結果」的承諾約束。
4. **log 內容不加 header**：純輸出，revision、command、開始時間已在 Evidence 與 `current_run` 上，header 會製造第二份權威。
5. **保留政策**：PASS 與非 PASS 皆留檔；不自動清理、不提供清理指令。

理由與備選見 [ADR-0012](adr/0012-verification-log-outside-state.md)，不在此重述。

## Human decision：M3 起

OPEN Gate 必須阻擋工作推進。Resolve 需保存明確 Decision 與時間；不能由 Agent 推測選項或以逾時當同意。

`review approve` 是 explicit CLI action，ForgePilot 不自動 approve。

### M3 開工前定案（已完成）

1. **Gate 的依附與開啟**：Gate 只依附單一 Work Item；擋整個 Goal 用 `Goal.BLOCKED`，不另設 Goal 層級 Gate。開啟者不區分人或 Agent——ForgePilot 記錄「有人提出了這個問題」，不宣稱知道那是誰。
2. **Gate 生命週期**：一件 Work Item 可同時有多個 OPEN Gate，推進條件是 OPEN 數為零。`options` 必填且至少兩個，`resolve` 只能選其中之一並可附自由文字 note；選項全都不對時的正確動作是 `cancel`（須附理由）再重開一個。CANCELLED 解除阻擋——同一個人本來就能用 resolve 選任何選項，cancel 沒有打開 resolve 沒打開的門，差別只在它記錄「這個問題問錯了」。Gate 集合為 append-only，進入 RESOLVED 或 CANCELLED 後不可變更或刪除，因此不需要另外複製成 Evidence。
3. **決策者身分**：保存自述的身分（預設取 Git 的 `user.email`），明確標示為聲明而非認證。不做認證——半套的認證比不做更危險，它會讓人以為那個名字有保證。詳見 [ADR-0005](adr/0005-self-asserted-decision-maker.md)。
4. **阻擋不佔用狀態欄**：不設 `WAITING_HUMAN` 與 Work Item 的 `BLOCKED`。詳見 [ADR-0007](adr/0007-blocking-is-not-a-status.md)。
5. **DONE 的可逆性**：終態，無 reopen。詳見 [ADR-0006](adr/0006-done-is-terminal.md)。
6. **完成的達成方式**：不提供獨立的完成指令。`review approve` 在同一交易內檢查條件，滿足就進入 DONE 並解鎖下游。詳見 [ADR-0008](adr/0008-approval-completes-work.md)。
7. **多筆結果的判定**：同一 revision 各取最新一筆——DONE 要求最新 Verification 為 PASS 且最新 Human Review 為 APPROVED。曾經出現過即算數會讓「重跑以確認 PASS 是否穩定」反過來變成漏洞。
8. **Goal lifecycle**：提供 `goal block` / `unblock` / `complete` / `cancel`。COMPLETED 是人手動宣告且要求全部 Work Item 皆為 DONE，不由系統推斷——自動標記會產生一個需要退回 ACTIVE 的可逆狀態，與 DONE 不可逆的立場矛盾。Goal 轉為非 ACTIVE 時，底下活躍的工作維持原狀但無法推進；進行中的 Verification Run 跑完仍須記錄其 Evidence，那是已發生的事實。

### M3 schema v3

Schema v3 相對 v2 同樣只有新增：`schema_version` 改為 3、根層加入 `gates` 陣列與 `next_gate_id`、Evidence 加入 review 專用的 `reviewer` 與 `note`、Goal 加入 `reason`（僅 BLOCKED 與 CANCELLED 使用）。無欄位刪除或語意改變。

Gate 與 Work Item 共用同一份 state snapshot 與同一次受鎖的原子替換，理由同 [ADR-0001](adr/0001-evidence-in-state-snapshot.md)：分開存放會產生兩者不一致的中間態。Gate ID 由受鎖操作發出遞增序號，格式 `GATE-001`。

### M3 Human Review Evidence

Human Review 是 Evidence 的第二個 `type`，與 Verification 共用同一個容器與同一條 ID 序列，因此一件工作的歷史是單一時間軸。它綁定 repository、Work Item、Story 與當下的完整 commit SHA，工作樹不乾淨時拒絕記錄。

`result` 為 `APPROVED` 或 `REJECTED`；REJECTED 必須附理由並把工作退回 RUNNING。review 沒有 `command` 也沒有 `exit_code`——它是判斷，不是跑過的命令；verification 則不得攜帶 `reviewer` 或 `note`。兩者以 `type` 分流驗證，避免一種 Evidence 被當成另一種讀。

進入 DONE 的四項條件在 `review approve` 的同一次交易內檢查：同一 revision 的最新 Verification 為 `PASS`、最新 Human Review 為 `APPROVED`、該 Work Item 無未解除 Gate、其 Goal 為 `ACTIVE`。滿足則進入 DONE 並於同一交易重新計算受影響的 PENDING 工作，只有全部依賴皆為 DONE 者轉為 READY。

### 呈現規則

`status` 顯示每件工作的 OPEN Gate 數量。DONE 的工作不標示 stale——stale 的用途是提示需要重驗，對終態工作那個提示是錯的，而沒有行動意義的警示會讓人開始忽略所有警示；仍顯示其完成時的 revision。

`review approve` 當下的 HEAD 與最新 PASS 的 revision 對不上時，審查仍被記錄（先審後驗是正當流程）但不進入 DONE，`status` 必須說出沒有進入 DONE 的原因。

## PR review target：M4 起

Human Review Evidence 可以額外攜帶 PR Reference，聲明這次審查發生在哪個 pull request 上。它是識別資料，不改變任何狀態機行為。

### M4 開工前定案（已完成）

1. **Evidence 斷言的是本機記錄，不是 GitHub 的結論**。`review approve` 仍然是人在本機下的明確指令，ForgePilot 記錄「某人聲稱在 PR X 的這個 HEAD 上核准」。不去 GitHub 讀該 PR 的 review state，因此不繼承外部系統的可用性、授權與 schema。
2. **不主動發出網路請求**。development-plan 的「不得加入 network API」讀成嚴格版：既不對外開介面，也不自行 HTTP、不 spawn `gh`。詳見 [ADR-0010](adr/0010-no-outbound-network-requests.md)。推論是 PR review target 的 HEAD 必須是本機 repository 裡真實存在的 commit，`review` 才能記錄。
3. **PR Reference 只存在 Evidence 上**，Work Item 不設 PR 欄位。理由同 [ADR-0003](adr/0003-no-work-item-target-revision.md)：Work Item 上的識別欄位會立刻產生「誰負責讓它保持正確」的問題，而一件工作經歷多個 PR（第一個被關掉重開）是常見的事。存在 Evidence 上，那是一條時間軸而不是一個被覆寫的欄位。
4. **只有 Review PR 化，Verification 不變**。verification Evidence 的 `pr` 必須為空，與既有的「review 不得帶 `command`／`exit_code`、verification 不得帶 `reviewer`／`note`」同屬一套嚴格分流規則。
5. **PR Reference 的形式為 `owner/name#number` 的單一字串**，以嚴格 pattern 驗證：每段須以英數開頭，number 拒絕 0 與前導零，總長上限 255。只接受這一種形式——不接受完整 URL、不做正規化，明確帶了 `--pr` 卻給空值是輸入無效而非「沒有 PR」。大小寫照原樣保存與比較，因此同一個 PR 仍可能有兩種寫法（GitHub 的 owner／repo 名稱大小寫不敏感）；這是刻意接受的代價，另一條路會拒絕使用者從 PR 頁面直接抄下來的合法寫法。多一種輸入法就多一組解析錯誤與一個「這兩筆是不是同一個 PR」的比較問題。既有的 `repository` 欄位保持不變（本機 state root 路徑），PR Reference 自帶完整識別，因此離開這台機器仍可解讀。
6. **`--pr` 為選填**。不帶就是 M3 那種純 commit review。強制必填會讓 M3 時代合法完成的 DONE 變成讀不進來的 state，正是 [ADR-0006](adr/0006-done-is-terminal.md) 要避免的事；而本機先審、之後才開 PR 是正當流程，強制順序沒有換到任何東西。
7. **PR 不參與完成判定與 stale 判定**。DONE 的四項條件與 `Stale` 的定義一字不改。詳見 [ADR-0011](adr/0011-pr-identity-does-not-gate-completion.md)。
8. **格式驗證屬於 domain**。PR Reference 的格式純粹是字串規則，不碰任何外部系統，因此規則放在 `internal/work` 與其他 Evidence 欄位規則同處，`validateEvidence` 才能對載入的既有 state 一併把關；只在 CLI 驗，手改過的 `state.json` 會夾帶非法值進來。

### M4 schema v4

Schema v4 相對 v3 只有新增：`schema_version` 改為 4、Evidence 加入選填的 `pr`。無欄位刪除或語意改變，既有資料不需改寫。

版本檢查是嚴格相等，所以即使升級步驟不動任何資料，仍然必須升版並提供 v3→v4 這一步。該步驟照走完整儀式——備份為 `state.json.v3.bak`、備份已存在時拒絕而非覆寫、對已是最新版本者回報並以 exit 0 結束。不為「這次沒有資料要動」開特例：使用者面對的契約是「升級就是備份加改版號」，而不是「有時候會備份」。

### M4 呈現規則

`status` 在顯示最新一筆 Human Review 時，若該筆帶有 PR Reference 就一併顯示，沒有就什麼都不印。它是描述而非警告，不因缺少 PR 而提示任何事——缺 PR 是合法狀態，不是問題。[ADR-0008](adr/0008-approval-completes-work.md) 要求 `status` 不對未完成的原因沉默，而 PR 不是完成條件之一，因此不在該要求的範圍內。
