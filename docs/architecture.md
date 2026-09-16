# Architecture

## 文件狀態與範圍

MVP 的 M1–M5、P0-001–P0-003、P1-004 Deterministic Runtime Resolution 與 Goal-level Review Policy 已依本文件實作。原始專案需求是產品邊界；標記為「待定」的事項不得視為已決定的功能。

核心名詞只在 [CONTEXT.md](../CONTEXT.md) 定義；Milestone 與驗收只在 [development-plan.md](development-plan.md) 維護。

本文件的視覺化見 [diagrams/](diagrams/README.md)：狀態機、分層與依賴方向、交易邊界，以及 `verify` 與 `review approve` 的順序。圖與本文件衝突時以本文件與程式碼為準。

## Authority boundaries

| 擁有者 | 責任 |
|---|---|
| Human | Goal approval、架構／範圍／安全判斷、production operation、merge／release authorization |
| ForgePilot | Goal、工作佇列、狀態、Gate、Evidence index、Candidate identity、next-work selection |
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
| Goal | `id`, `title`, `description`, `repository`, `status`, `review_policy`, `created_at`, `updated_at` | M1；`review_policy` 於 Goal-level Review Policy 加入 |
| Work Item | `id`, `goal_id`, `story_ref`, `status`, `depends_on`, `created_at`, `updated_at` | M1 |
| Work Item run | `current_run`（Verification Run ID、Candidate identity、worktree path、started_at、log path、resolved runtime；閒置時為 null） | M2；`log path` 於 M5、Candidate kind／base／digest 於 P0-001、runtime 於 P1-004、run ID 於 schema v9 加入 |
| Work Item claim | `claimed_by` | 待定；M1 不建立 Agent 身分或 lease 協定 |
| Gate | `id`（`GATE-001`）、`work_item_id`、`question`、`rationale`（開啟時的說明）、`options`（至少兩個）、`status`、`opened_at`，以及關閉時的 `choice`／`note`（resolve）或 `reason`（cancel）、`decided_by`、`decided_at` | M3 |
| Evidence | `id`（`EV-001`）、`type`、repository、Work Item、Story、完整 Candidate identity、`result`、timestamp；verification 另有 Verification Run ID、`command`、`exit_code` 與選填 actual `runtime`，review 另有 `reviewer`、`note` 與 M4 起的選填 `pr` | M2 起；Candidate kind／base／digest 於 P0-001、runtime 於 P1-004、run ID 於 schema v9 加入 |

Goal statuses：`ACTIVE`, `BLOCKED`, `COMPLETED`, `CANCELLED`。M1 僅建立 ACTIVE Goal，不提供其他 Goal lifecycle 操作。

Work Item statuses：`PENDING`, `READY`, `RUNNING`, `VERIFYING`, `REVIEW`, `VERIFIED`, `DONE`。M1 可到達的狀態只有前三者，M2 加上 `VERIFYING` 與 `REVIEW`，`DONE` 自 M3 起由 `review approve` 在條件滿足時達成。`VERIFIED` 只屬於 `GOAL` Review Policy：它表示目前 Candidate 的 machine PASS，可滿足該 policy 下的依賴 progression，絕不表示 Human acceptance 或 DONE。

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
- 新增時全部依賴滿足該 Goal 的 progression policy 則為 READY，否則 PENDING：`WORK_ITEM` 要求 DONE；`GOAL` 可接受無 OPEN Gate 的 VERIFIED。無依賴時視為已滿足。
- 候選必須屬於 ACTIVE Goal、為 READY、全部依賴滿足同一 progression policy 且 Candidate freshness 適用，並且沒有 unresolved Gate。Gate 條件在 M3 啟用。
- 候選按 `created_at` 升冪排序，同時間以 Work Item ID 的配發序號升冪決勝。
- `Next()` 是 READY selection 的純規則；`next` command 在 P0-003 以它作為最後一層候選，不 claim、不 start、不改寫 READY 狀態。
- `start` 必須在寫交易內重查 Goal 與依賴；不能只相信保存的 READY 值。
- `reconcile --goal <goal-id>` 是唯一把 readiness 重新對齊事實的寫入指令。readiness 是持久化欄位，但推導它的 facts 不是：Gate 或 Candidate 移動讓下游退回 PENDING 後，條件恢復不會自己更新那個值。它沿用同一個 progression predicate，只允許 `PENDING ↔ READY`，不碰其他狀態、Evidence、Gate 或 review policy；Goal 必須存在且 ACTIVE，facts 在受鎖 callback 內只解析這個 Goal 實際需要的種類，取得失敗即拒絕整個命令。相同 state 與 facts 下重複執行不產生變更，未改變的項目 `UpdatedAt` 不動。詳見 [ADR-0017](adr/0017-readiness-is-a-projection-made-durable.md)。
- 不限制全域只能存在一個 RUNNING；`status` 必須能列出多個進行中的工作。

M3 完成某個 Work Item 時，在同一 state transaction 中只更新該 prerequisite 的直接 dependents；只有全部依賴 DONE 且符合適用條件時才 READY。Goal-level Review Policy 下，同一個 transaction 也可由無 OPEN Gate、且以 CLI 在受鎖 callback 內解析的 repository facts 證實仍 fresh 的 VERIFIED dependency 推進 READY；沒有 facts 時採 fail-closed，保留 PENDING。若該 prerequisite 開始重驗或新增 OPEN Gate 而不再滿足同一 predicate，受影響的 READY downstream 會回到 PENDING，fresh PASS 或帶 current facts 的 Gate closure／Goal unblock 後再回到 READY；局部 refresh 不改寫無關 Goal 的 READY。Goal BLOCKED 仍保留 Work Item status 不變；這不放寬 Goal ACTIVE、Gate、verification FAIL／INTERRUPTED 或 stale Candidate 的既有規則。

## Central transition policy

| Transition | 條件與階段 |
|---|---|
| 建立 → PENDING／READY | 依賴規則決定；M1 |
| PENDING → READY | 全部依賴滿足 Goal 的 progression policy（WORK_ITEM 為 DONE，GOAL 可為無 OPEN Gate 的 VERIFIED），且符合 Goal／Gate 條件；M1 測試規則，M3 提供真實完成來源 |
| READY → RUNNING | ACTIVE Goal、依賴滿足同一 progression policy（VERIFIED dependency 另須 Candidate fresh）、無 unresolved Gate；M1，Goal-level Review Policy 擴充 |
| RUNNING → VERIFYING | 允許開始 canonical verification；M2 |
| VERIFYING → RUNNING | `WORK_ITEM` policy 下 PASS／FAIL，或回收中斷的 Verification Run；PASS 只記錄 machine Evidence；ADR-0023 |
| VERIFYING → VERIFIED | `GOAL` policy 下 PASS evidence；只滿足依賴 progression，不是 Human acceptance；Goal-level Review Policy |
| RUNNING → REVIEW | `review request`：WORK_ITEM policy、ACTIVE Goal、無 OPEN Gate、最新 PASS 是 current Candidate 且晚於任何 Human Review；ADR-0023 |
| REVIEW → VERIFYING | 由明確的 `verify` 命令觸發；stale 本身不改變狀態；M2 |
| REVIEW → RUNNING | Human Review REJECTED；M3 |
| REVIEW → DONE | `WORK_ITEM` policy：同一 Candidate 的最新 Verification 為 PASS 且最新 Human Review 為 APPROVED，且無 unresolved Gate；由 `review approve` 在同一交易內達成；M3 |

非法 transition 回傳明確 domain error，不能偷偷改成另一個操作。M1 不提供任意 state setter、complete 或測試專用 approve 指令。

開啟或關閉 Gate 不是 transition：它不改變 Work Item 的狀態，只改變它能否推進。Gate 可以開在任何非 DONE 的工作上。

DONE 是終態，不因後續 commit 重開，也沒有 reopen 操作；需要重做就新增一件 Work Item，讓「為什麼重做」有地方被記錄。詳見 [ADR-0006](adr/0006-done-is-terminal.md)。不能把「Gate 已 resolve」直接等同於 READY 或 DONE。

### Goal-level Review Policy 與 schema v8

Goal 的 `review_policy` 是持久化 enum：`WORK_ITEM` 為預設且完整保留既有行為，`GOAL` 把例行 Human Review 邊界移至 Goal final review。CLI 僅在 `goal create` 接受 `--review-policy work-item|goal`；它不是 `--skip-review` 或每次命令可選的 bypass。`GOAL` 下 Work Item PASS 轉為 VERIFIED，只有它可依 policy 推進下游；其 prerequisite 進入重驗或新開 OPEN Gate 時，已 READY downstream 依同一 central predicate 回到 PENDING，並在 fresh PASS／Gate closure 後重新 READY。Goal BLOCKED 只阻擋推進、保留既有 status；verification FAIL／INTERRUPTED 與 Candidate freshness 均照常阻擋或要求重驗。

Goal final readiness 是 fail-closed 的純 projection，不是 Goal status、transition 或自動完成：只在 ACTIVE、非空的 `GOAL` Goal 中，所有 Work Item 都為 VERIFIED、其 latest Verification 都是 PASS 且仍匹配目前 COMMIT／SNAPSHOT Candidate、並且沒有 OPEN Gate 時成立。它保留構成 target 的 Verification Evidence IDs，供將來建立精確 aggregate Evidence。`status` 呈現 policy 與此 readiness；`next` 在沒有合法 agent action 時回報等待 Goal final review，stale VERIFIED 則仍優先建議 reverify。

此切片尚未提供 goal-level `review approve`／`reject`、Goal Evidence 或 Runner；因此 `goal complete` 對 `GOAL` policy 一律拒絕。這刻意分開「可安全推進工程」和「Human final acceptance」，避免 machine PASS 被誤當作完成。未來只能在這個 projection 上加入接受精確 Evidence ID 集合的 Goal Evidence 與 Runner，而不能回頭將 VERIFIED 解釋為 DONE。Schema v8 對 Goal 新增必填 `review_policy`；migration 將 v7 與更舊 state 明確填為 `WORK_ITEM`，先備份 `state.json.v<n>.bak`。rollback 是手動還原該備份，沒有 downgrade。

### Candidate Verification fan-out 與 schema v9

`internal/app.Verify` 仍由一張 anchor Work Item 觸發，但 repository canonical check 對 immutable Candidate 與 Resolved Runtime 只執行一次。PASS 時，`internal/work` 在單一 transaction 內為 anchor 與同 Goal、已有 stale PASS、狀態為 REVIEW／VERIFIED、沒有 OPEN Gate、且 prerequisite closure 仍成立的 recipients 各建立一筆 Evidence。這些 Evidence 的 ID 與 Story association 各自獨立，卻共享 Verification Run ID、Candidate、runtime、command、result、timestamp 與 log。FAIL／INTERRUPTED 仍只記 anchor；optional recipients 不進 VERIFYING，crash reclaim 也維持 anchor-only。

begin transaction 產生只存在記憶體的 `FanoutPlan`，凍結 optional item、Goal、Gate、latest Verification 與遞迴 prerequisite facts；completion 重新比對並反覆重算 closure，任何改變都明確列為 skipped，不抹去 anchor 誠實取得的 PASS。全部 Evidence 與 status 先落到 final value，才以 transaction 內解析的 live repository facts refresh readiness 一次；facts 失敗時整組 Evidence 保留，promotion fail closed。

Schema v9 新增根層 `next_verification_run_id`，並要求 active Run 與 Verification Evidence 保存 run ID。新 execution 使用 `VR-*`；v8 migration 對每筆歷史 Verification Evidence 與 orphan Run 配發 distinct `LVR-*`，Review Evidence 不得帶 run ID。共享 ID 的 Verification Evidence 必須有相同 Candidate、runtime、command、result 與 timestamp，且不可重複 Work Item、不可同時 active 與 settled；repository 與 Story association 仍逐筆對 owning Work Item 驗證。

per-Work-Item flock 仍先保證 anchor liveness 與 orphan reclaim；之後另持有 repository-wide non-blocking canonical lock，固定順序為 anchor lock → repository lock，並涵蓋 Candidate capture 到 cleanup。競爭失敗在新 state、checkout、log 或 subprocess 之前拒絕。新 log 以 `VR-<n>-<short-sha>-<started-at>-<random>.log` exclusive-create；Runner 對 `VR-*` 用完整 `runID + "-"` token 唯一查找，`LVR-*` 才沿用舊 Work Item／revision lookup。這修正 ADR-0012 的舊 lookup 契約；Evidence 仍不保存 filesystem path。詳見 [ADR-0027](adr/0027-candidate-verification-pass-fans-out-by-run.md)。

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

### P0-001 Candidate Snapshot 與 schema v6

Candidate 是 Evidence 所針對的 immutable code identity，不是另一套 workflow state。`COMMIT` candidate 只有既有 revision；`SNAPSHOT` candidate 另保存建立時的 `base_revision` 與 ForgePilot 自動計算的 `candidate_digest`，其 `revision` 是可 checkout 的 snapshot commit。Candidate 只存在於 `current_run` 與 Evidence，不放到 Work Item 本身；Verification 結束或中斷時，Evidence 從已固定的 run candidate 取得 identity，不重新讀取 live workspace。

`verify <work-id>` 保持 M2 契約：要求 clean workspace，解析 HEAD，對 detached checkout 執行 `make verify`。`verify <work-id> --snapshot` 使用 temporary index，從 HEAD seed 後以 `git add -A` 收進 tracked staged／unstaged 修改、tracked deletion 與 non-ignored untracked files；ignored untracked files 不進 candidate，原本 tracked 的內容仍會進。這個過程不修改 current branch、HEAD、real index、staging state 或 working files。

snapshot tree 先形成 immutable commit，再以 `refs/forgepilot/snapshots/<work-id>/...` 保留，之後才開始 Verification Run。ref 不屬於 branch、tag 或正常 commit history，而且只存在本機；即使後續 canonical check 拒絕、FAIL 或 state 寫入失敗，也不刪除已建立的 ref，以免產生 state 指向會被 GC 回收的 object。完整 retention／GC policy 不在本 ticket。

digest 是 `sha256:` 加 lowercase hex，涵蓋版本化格式、base revision 與依 path 排序的 Git tree entry（relative path、mode、type、object identity）；Git blob/tree identity cryptographically 綁定內容，因此不讀 mtime、absolute path、temporary directory 或 filesystem enumeration order。`status` 與 review 重算 digest 時另用 temporary object database，避免查詢本身把 loose objects 寫進 repository。

Snapshot PASS 後，stale 以目前 workspace digest 是否仍等於 Verification Evidence 判斷，不以 snapshot revision 是否等於 HEAD 判斷。Snapshot review 同樣先重算 digest：相同就把 Human Review 綁回已驗證的 snapshot revision；不同就拒絕且不 append Evidence，要求重新執行 `verify <work-id> --snapshot`。`COMMIT` review 維持 clean workspace＋HEAD 的既有行為。DONE 的條件仍是最新 PASS 與最新 APPROVED 的 `revision` 相同、無 unresolved Gate、Goal ACTIVE；Candidate Snapshot 沒有第二條 completion lifecycle。

Schema v6 對 `current_run` 與每筆 Evidence 新增 `candidate_kind`、`base_revision`、`candidate_digest`。v5→v6 migration 不查 Git，而是把所有舊 run／Evidence 的既有 revision 明確標成 `COMMIT`；舊 binary 拒讀 v6，新 binary 拒讀未 migration 的 v5。migration 前備份 `state.json.v5.bak`，rollback 必須還原該備份；snapshot refs 可以留在本機，不影響舊版 commit-only 流程。決定與失效條件見 [ADR-0014](adr/0014-working-tree-snapshot-is-a-candidate.md)。

### P0-002 Work Item Status Summary

`status --work <work-id> --summary` 是單一 Work Item 的 read-only current-state projection。它的資料流固定為 domain state → summary projection → CLI formatter：`internal/work` 以純值形式接收目前 repository revision 與 snapshot digest，選擇 latest Verification／Human Review、未解除 Gates，並重用 Candidate stale 規則；`internal/cli` 才讀取 Git 事實及格式化固定輸出。它不持久化 `summary`、`current_blocker`、`completion_text` 或 `next_action`，也不改變 Work Item lifecycle。

### P0-003 Actionable Next

`next` 的資料流與 summary 相同：domain state + current repository facts → `ActionableNext()` projection → CLI formatter。Projection 不是 lifecycle state，也不持久化。它按以下順序選擇一件工作：可驗證條件成立的 RUNNING；可驗證條件成立且 candidate stale 的 REVIEW；可合法前進的工作——已 READY 者建議 `start`，PENDING 但依賴已滿足者建議 `reconcile`，兩者共用同一個 created-at／numeric-ID 排序與同一個 `advanceable` 判準；再來才是 GOAL-policy 下 candidate stale 的 VERIFIED 重驗。GOAL policy 每換一個 Candidate 都會讓先前 VERIFIED 變 stale，若讓它們永遠優先，連續任務會在每一步重驗整條歷史；延後不放寬 freshness，Goal final review boundary 仍要求每件工作都有匹配目前 Candidate 的 PASS，欠下的重驗必須在總審前補完。RUNNING 的最新 Verification 為 FAIL 時，projection 稱為 repair；PASS 留在 RUNNING，直到 agent 完成 AC audit 後明確執行 `review request`。REVIEW／VERIFIED 的 freshness 一律復用 Candidate identity：COMMIT 比 HEAD，SNAPSHOT 比 workspace digest。

Gate 與 Goal 規則不在 CLI 重建：RUNNING／REVIEW／VERIFIED 是否仍可重驗由 `Verifiable` 決定，READY 是否可開始、PENDING 是否可恢復都由同一個 `advanceable` predicate 決定——這也是 `next` 不會推薦一個 `reconcile` 隨即回報 unchanged 的原因。沒有 agent action 時，projection 才可回報最早的 human-only blocker：OPEN Gate、非 ACTIVE Goal、fresh PASS REVIEW 缺 Work Item Human Review，或完整 Goal candidate 等待 final review；PENDING dependency 與 VERIFYING 不被虛構為 Human wait。CLI 的 Action 欄永遠只是文字推薦，不能執行或持久化任何 transition。

completion 是 presentation text，不是新狀態。它只投影現有 Work Item status、latest Evidence、Goal status、Gate status 與 stale 判定；Gate 與 Goal 保持各自原有的 blocking 規則，DONE 仍為終態。GOAL policy 的 fresh VERIFIED 顯示 `verified for goal review`，stale 時仍顯示 `verification stale`。APPROVED 後才因 Gate／Goal 解除或新的 matching PASS 而滿足所有條件時，projection 明確提示重跑既有的 `review approve`，不暗中完成。summary 只列出 unresolved Gate IDs，避免已 RESOLVED／CANCELLED 的歷史遮蔽當前行動。

### P1-004 Deterministic Runtime Resolution 與 schema v7

Runtime Contract 屬於 Candidate 的內容，因此 discovery 固定發生在 COMMIT／SNAPSHOT 已建立的 detached checkout 內，不得先讀 main worktree。`internal/repository` 提供單一 runtime resolver interface，封裝 declaration parsing、precedence、local installation／shim discovery、actual-version validation 與 child-process environment；`internal/cli` 只接收 opaque Resolved Runtime、印 summary，並把 actual version values 傳給 domain。canonical target precheck 與真正的 `make verify` 接收同一份 environment，避免把關條件與交易使用不同 PATH。

支援來源依序為 `mise.toml`、`.tool-versions`、language-specific files、ecosystem manifest。Exact declarations 必須互相相容；較低 precedence 的 range／minimum 仍是 contract constraint，不能被高 precedence 選擇靜默違反。resolver 可從 mise、asdf、nvm、pyenv、rustup 的既有 data directory 或 caller PATH/shim 找 executable；選定後建立 private temporary command directory，讓 `node`／`go`／`python`／`rustc` 等名稱精確指向各自驗證過的 executable，再把它與所需 bin directories 組成一次性 PATH。這避免前一個 runtime 的 manager directory 意外遮蔽另一個 runtime。它不呼叫 install、不 source profile、不寫 repository 或 global manager state，temporary directory 隨命令清理。沒有 declaration 時不建立 environment override，沿用舊行為。

解析、可用性與 actual-version validation 全部在 `EnsureCanonicalCheck`、log 建立與 `BeginCandidateVerification` 之前完成。任一步失敗是 precondition failure：移除 detached checkout，Work Item 保持原狀，不 append FAIL Evidence。成功時 resolved version map 先存進 `current_run`，PASS／FAIL／INTERRUPTED Evidence 都從 run 複製，完成時不重查 live shell。

Schema v7 對 `current_run` 與 Verification Evidence 新增選填 `runtime` map。v6→v7 不回填，因為舊執行的 actual runtime 無從證明；沒有 runtime 的舊 Evidence 繼續合法。Human Review 不是 subprocess outcome，因此禁止攜帶 runtime。決定與失效條件見 [ADR-0015](adr/0015-runtime-contract-belongs-to-candidate.md)。

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
5. **Stale 觸發**：M2 的 COMMIT Evidence 以 SHA 是否等於目前 HEAD 判斷；P0-001 的 SNAPSHOT Evidence 改以 candidate digest 是否等於目前 workspace 判斷。它不造成任何自動 transition；REVIEW／VERIFIED → VERIFYING 只由明確的 `verify` 命令推動。`next` 與 `status` 呈現 stale 但不寫入，`start` 不接受 stale VERIFIED dependency。

Revision 只存在於 Evidence 與 `current_run`，Work Item 本身不保存 `target_revision`；該欄位的刪除見 [ADR-0003](adr/0003-no-work-item-target-revision.md)。

M5 起，canonical 檢查的輸出邊執行邊串流寫進 state 之外的 `.forgepilot/logs/`，以那次執行為鍵命名；`current_run` 對應多出的 `log path`，一如既有的 `WorktreePath`。輸出是診斷材料，不是結論——結論仍只在 Evidence。M5 當時的 Evidence 沒有輸出 lookup 欄位；schema v9 由 ADR-0027 加入非 path 的 Verification Run ID，供 shared execution provenance 與新 log 唯一查找。理由與備選見 [ADR-0012](adr/0012-verification-log-outside-state.md) 與 [ADR-0027](adr/0027-candidate-verification-pass-fans-out-by-run.md)。

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

## Long-running Runner：`forgepilot run`

Runner 由使用者明確啟動，對單一 Goal 循序執行：取得下一個合法動作、必要時開一個新的 coding agent session 實作指定的 Work Item、跑正式 verification、重新讀取狀態，再繼續。範圍與驗收見 [specs/runner-mvp/spec.md](specs/runner-mvp/spec.md)。

責任分工是全部：**Runner 負責執行，ForgePilot 負責判定，ForgeFlowV2 負責工程驗證規範。**

### 分層與新的 package

| Package | 責任 |
|---|---|
| `internal/app` | CLI 與 Runner 共用的 orchestration：Goal-scoped typed 查詢、start、reconcile、verification、Gate 開立 |
| `internal/agent` | Agent runtime 邊界：啟動本機 coding CLI、交接內容、結果驗證、程序群組控制 |
| `internal/runner` | 執行迴圈、session 邊界、預算、期限與停止判定、execution history |
| `internal/process` | 受管理程序群組的啟動、有界終止與清理確認，`agent` 與 `repository` 共用 |

`internal/work` 仍是純狀態機，沒有新增 interface；Git 仍只在 `internal/repository`；原子保存與鎖仍只在 `internal/storage`。verification orchestration 從 `internal/cli/verify.go` 搬到 `internal/app`，CLI 的 `verify` 變成薄殼，輸出文字與退出碼不變——Runner 使用的是同一段程式，不是複製品。

Agent runtime（Codex 等 coding CLI）與 Verification toolchain（Candidate checkout 宣告的 Go／Node／Python）是兩個不同的邊界。agent adapter 不進入 [P1-004](#p1-004-deterministic-runtime-resolution-與-schema-v7) 的 runtime resolution。

### 判定只有一份

Runner 呼叫 `State.ActionableNextForGoal(goalID, repository)` 取得有型別的 `NextAction`，不解析任何 CLI 輸出。Goal-scoped 與全域查詢共用同一份 priority／legality 實作；篩選只作用在候選集合，依賴與 freshness 仍然看完整 state。`State.GoalStall` 把「沒有合法動作」分類成 VERIFYING、Gate、依賴未滿足、Goal 非 ACTIVE、空 Goal 或未知；VERIFYING 是 live 還是 orphan 由 `internal/app` 以既有的 flock 判斷，因為那不是狀態機能回答的事。

Runner 能做的寫入只有四種既有 transition：start、Goal readiness reconciliation、verification orchestration，以及 agent 以至少兩個選項回報 `needs_human` 時的 bounded `OpenGate`。最後一種只會增加阻擋，不會放行；Runner 不寫 VERIFIED／DONE、不核准 review、不解除 Gate、不完成 Goal。理由記在 [ADR-0019](adr/0019-runner-executes-forgepilot-decides.md)。

### 網路邊界

[ADR-0010](adr/0010-no-outbound-network-requests.md) 的「不主動發出網路請求」對所有既有命令完整適用。唯一的例外是使用者明確執行 `run` 時，Runner 可以啟動一個指定的本機 coding CLI，而該 CLI 會連線到模型服務。ForgePilot 自身沒有 HTTP client、不取得憑證、不代理任何遠端狀態，Evidence 的 result 集合也沒有改變。見 [ADR-0018](adr/0018-runner-may-launch-a-local-coding-cli.md)。

### Session 邊界與結果契約

每張工作、每次 repair 都是新的 session；不使用 `codex resume --last`。以 executable 加 argument array 啟動，prompt 走 stdin 與一份受控檔案，workspace 以 `--cd` 明確指定，權限為 `--sandbox workspace-write`，不使用任何 bypass flag。

結果是三選一的結構化 JSON：`implementation_finished`、`needs_human`、`execution_failed`。解碼是嚴格的（拒絕未知欄位、拒絕多餘值、檢查每個 outcome 的必要欄位），exit code 0 但沒有合法結果是 protocol error 而不是完成。`implementation_finished` 只表示這次實作結束——PASS 只能來自 Candidate checkout 上的 canonical check。`needs_human` 一律停止；當它列出至少兩個選項時，透過既有 Gate service 開一個 OPEN Gate，選項照原樣保存，ForgePilot 不代答也不自行解除。

交接內容有位元組上限。必要段落（工作識別、Story 與其驗收要求、目前工作及所有 transitive prerequisites 的 RESOLVED Gate decisions、禁止事項、結果契約）先寫且不截斷；必要段落本身放不下時拒絕建立 session，不把不完整 briefing 交給 worker。Gate decisions 每次都從 current state 投影，包含原問題、選定選項與 resolution note，不從前次 attempt summary 猜測；同一次讀取也重驗原 action 仍是 typed query 的目前答案，若 OPEN Gate 或其他 state change 已使它失效，就不啟動 session，而由下一輪重新判定。依賴摘要、前次 attempt 摘要與 verification 失敗摘錄排在後面並在超限時截斷，截斷一定留下明說的標記與檔案路徑。不放完整環境變數、token 或認證檔。OPEN／CANCELLED、sibling 與 downstream Gates 不構成可沿用的 decision；Runner 不做語意去重、不自動 resolve。見 [ADR-0026](adr/0026-resolved-gates-cross-agent-session-boundaries.md)。

### 執行保護

Workspace lock 涵蓋整段 Runner，鍵是 canonical path，因此 symlink 別名無法啟動第二個 Runner。它不是 per-work 的 verification lock，也不是 state 交易鎖——模型或 canonical check 執行期間 `status` 仍可回答。

那個 lock 只證明「沒有活著的 Runner」，不證明「沒有活著的 worker」：被 SIGKILL 的 Runner 會釋放 lock，而它啟動的 coding CLI 還活著。因此 `run` 與 `run resume` **都**會在取得 lock 之後掃描既有 run record，套用同一套判定；任何一筆判不出來就 recovery blocked，不新增 run、不啟動 writer。

掃描的對象不只 worker。canonical check、runtime preflight 與 Git 子程序都不會留下 worker 欄位，只看 worker 等於讓這三種執行完全沒有恢復保護。run record 因此另有 `pending`：每一筆未確認的執行在程序啟動**之前**先寫下（所屬 run、workspace、Work Item、執行種類與階段、相關 checkout 位置），啟動後補記觀察到的 PID／PGID 與 identity，確認清理完成後才在同一次原子替換中移除。停止原因與清理失敗原因分成兩個欄位保存，清理失敗不會蓋掉「這是一次 Ctrl-C」。

`pending` 與 `stop` 的生命週期刻意不同。`stop` 說的是「上一次為什麼結束」，`resume` 會清掉它；恢復阻擋不能寄存在會被 resume 清掉的欄位上。同一個 workspace 換 Goal、換 run ID 或重啟 CLI 都是同一個 workspace，三者讀的是同一份掃描。解除只有一種方式：確認安全。沒有 `--force`，刪 `run.json` 或清空 ownership 也不是修復方法——那是把唯一的保證花掉。沒有 `pending` 欄位的舊紀錄照舊只走 worker 路徑，但「沒有這個欄位」讀作「這次 run 除了 worker 沒記別的」，不讀作「這個 workspace 已確認乾淨」；讀不出來的紀錄同樣阻擋。詳見 [ADR-0022](adr/0022-pending-cleanup-outlives-the-process.md)。

升級後遇到舊的 `RECOVERY_BLOCKED` 紀錄而其中缺少可確認的 identity：它仍然阻擋，這是刻意的。處理方式是人自己確認那個 workspace 沒有 writer（`ps`、`lsof`），然後把 run record 裡對應的 `worker` 或 `pending` 條目清掉；CLI 不提供做這件事的指令。

Agent session 拿到的是 workspace 寫入權限，而 `.forgepilot/` 在 workspace 裡。交接內容裡的禁令是對未受信任模型的請求，不是機制；session 前後會比對整份 `state.json` digest，不同即以 `AGENT_WROTE_FORGEPILOT_STATE` 停止，且不採信該次 attempt 的任何回報。canonical check 是第二個不受 Runner 控制的執行窗口；它比較完整 target Work Item（含 `current_run`）、owning Goal 的 repository／review policy 與完整 latest Verification Evidence，並在寫入結果的同一個 state transaction 再檢查一次。Goal lifecycle 的變化不在此 projection，讓已開始的 check 在 Goal 被 block/cancel 後仍能依既有規則保存 Evidence。這表示它**不保證**偵測 sibling Work Item、其他 Goal 或 Gate 的改寫，亦防不了 transaction 完成後或仍在進行的寫入；兩個機制都是事後偵測，不是 sandbox。限度與理由記在 [ADR-0019](adr/0019-runner-executes-forgepilot-decides.md)。

程序 ownership 以 pgid 加「啟動時由作業系統自己報回的 start time 與 command」比對判定。四種結果分別對應繼續、停止自己的程序群組、不得發送 signal、以及 recovery blocked，但「繼續」與「不得發送 signal」兩種都再問一次群組是否為空：leader 已死不等於群組已空，它分叉出來的子程序沿用同一個 pgid，而在識別對不上的情況下那個群組也不確定是不是我們的。群組仍有成員時維持 recovery blocked，不猜測、不送 signal。詳見 [ADR-0020](adr/0020-worker-ownership-is-fail-closed.md)。attempt 預算在程序啟動前先保存；程序啟動後立即補記 identity。所有 session slots 保持單調 attempt 編號；合法且已 checkpoint 的 `needs_human` outcome 另計為 human wait，不消耗 technical `--max-attempts-per-work`，但仍消耗 step、duration 與 artifact budget。選項不足時不製造 Gate，但分類仍是 human wait；舊 run record 缺少 human-wait accounting 時保守視為沒有可扣除的 wait。見 [ADR-0026](adr/0026-resolved-gates-cross-agent-session-boundaries.md)。

**這個判準只有一份實作。** `worker` 與 `pending` 問的是同一個問題——這個 workspace 可不可以開始寫——所以恢復對兩者呼叫同一個 `settleExecution`，而完整 identity 的情形直接交給 `internal/agent` 的 `TerminateOwned`：那裡本來就是 Gone／Ours／Unrelated／Unknown 四種結果的所在地。兩份規則各自演化的結果已經看過一次，`worker` 分支曾把「leader 不在了」直接讀成「可以了」，而 `pending` 分支同一時間要求群組為空。agent 啟動流程先存 `worker.identity`、只有在清理失敗時才寫 `pending`，所以兩者之間的 crash 留下的正是「有 worker、沒有 pending」——那不是只有舊紀錄才走得到的路徑。

恢復的結論是**這一次判定的結果**，不是紀錄裡帶著的 `stop`。一筆 run record 可以同時有 worker、pending 與上一個程序寫下的 `RECOVERY_BLOCKED`；把那個舊 `stop` 讀成「這次也拒絕」，會讓一個群組確實已經消失、這次恢復其實成功的 workspace 仍然被擋，要人再跑一次同樣的指令才會過。

### 停止訊號、程序清理與兩種期限

Runner 的每一段會阻塞的執行——Agent session、正式 verification、verification 的前置程序（runtime 版本探測、`make -n verify`），以及 Runner 啟動的每一個 **Git 子程序**——都在同一條取消路徑上。Git 值得單獨點名：`worktree add` 會跑這個 repository 的 post-checkout hook，`add -A` 會跑它的 clean filter，兩者都是 ForgePilot 不擁有的程式碼，而且可以想阻塞多久就阻塞多久。呼叫前的 `ctx.Err()` 只是啟動閘門，對已經在執行的程序沒有作用，因此 `internal/repository` 的 Git 與其他外部程序走同一條啟動、終止與確認路徑。CLI 收到 SIGINT／SIGTERM 後關閉的那個 channel，經 Runner 的執行控制傳到 `internal/app`，再傳到 canonical check 的程序群組。任何新的外部程序在啟動前都會重新檢查停止條件，Agent 結束到 verification 啟動之間的邊界也是一次明確的檢查點：不這樣做，一個剛好在期限前結束的 session 後面還能再接一次用滿 `--verify-timeout` 的驗證。

| 階段 | 受哪一種期限約束 |
|---|---|
| 啟動準備（handoff、capacity、state digest、Git 前置） | 本次執行有效期限 `min(run deadline, step deadline)` |
| 執行（agent session、canonical check、Git 子程序、runtime probe） | 同上 |
| 必要清理 | `process.CleanupGrace`，一次被叫停的執行一份總預算 |

**單次期限與總期限是兩個不同的量。** 一次執行的有效期限是 `min(原始 run deadline, 本次開始時間 + 本次 timeout)`。原始 deadline 從第一次啟動算起，`resume` 沿用它，不重置已消耗的步數與 attempts——每開始一個步驟就重新取得完整 `--max-duration`，會讓總期限變成「每步一次」的建議值。

**停止原因在觸發當下寫定，不事後從 `context.Err()` 推論**：一個結束的 context 只記得自己結束了，而「cancelled」對 Ctrl-C、到期的 run 與用完自身 timeout 的步驟是同一個字。多個原因幾乎同時到達時，先成立的就是終止原因；完全同時則依固定優先序 signal → 總期限 → 單次 timeout。整個 run 到期記 `MAX_DURATION`，Agent 自身 timeout 記 `AGENT_TIMEOUT`，verification 自身 timeout 記 `VERIFY_TIMEOUT`，兩個訊號各自維持 `INTERRUPTED`／`TERMINATED` 與 130／143。

**停止原因的判定順序在每一條路徑上都一樣**：先處理未確認的清理（那是唯一會讓下一步不能開始的事），再處理這次執行已經成立的停止原因（signal／run deadline／單次 timeout，在觸發當下寫定），剩下的才是一般的 Git 或操作失敗。步驟之間那些短的 Git 查詢也走這個順序：一個落在 Candidate facts 查詢裡的 Ctrl-C 記成 `INTERRUPTED`（退出碼 130），不是 `STALLED`，也不是沒有停止原因的退出碼 1。

**執行期限不等於清理寬限。** 期限到期後不再啟動任何新的業務工作，但終止是有界而非瞬時的：先 SIGTERM，等一段有限的寬限讓 canonical check 把輸出寫進 log，必要時 SIGKILL，最後**確認**程序群組真的空了。這四個等待——leader 的兩輪、程序群組的兩輪、輸出收集——**共用一份 `process.CleanupGrace` 預算**，不是每一層各領一份完整寬限：後者會讓一條由數個 helper 串成的清理路徑沒有可說明的總上限。SIGKILL 之後的等待也有上限；等不到 wait 結果就回報「未確認」，因為「送過 SIGKILL」與「程序已停止」是兩句不同的話，而分不清這兩者的呼叫端正是會啟動第二個 writer 的那一個。清理失敗時不刪除 worktree：那個 checkout 正是恢復紀錄指向的位置。

一次被中斷的 verification 因此有**兩個**具名且各自有上界的量，不是一個：停止那次 canonical check 自己的 `CleanupGrace`，以及之後收拾善後（移除 worktree、prune、關閉 runtime environment）的一段 cleanup window，同樣以 `CleanupGrace` 為上界。兩者都是總量：window 內啟動的每一個受管理程序共用 window 帶下去的那一份預算，不各自再開一份完整寬限——否則一條由數個 Git 指令串成的善後路徑就沒有任何人說得出來的總上限。

**一份預算屬於一個清理階段，而階段從它真的開始的那一刻才計時。** 一次 `forgepilot verify` 有兩個清理階段：開工前回收孤兒，以及收工後的善後，中間隔著整段業務執行。它們各有一份 window，各自在自己階段第一次用到時才打開。共用一份會讓善後從一個在 snapshot 之前就起算的 deadline 上支付；而在沒有孤兒可回收時把 window 先打開，等於讓整段驗證都在清理預算裡跑掉。那不只是「預算變少」：過期的 context 會讓下一個受管理程序在啟動前就返回，於是 checkout 根本沒有被移除，也沒有人被告知。

**清理錯誤分兩種，不能混。** 一般的清理失敗可以是 warning；`ErrNotSettled` 不行。它不是整潔問題而是執行安全結論，所以不得被 `os.RemoveAll` 的成功遮蔽（那還會順手刪掉恢復紀錄指向的 checkout）、不得只印在 stdout 上，而要沿 `VerifyResult.Cleanup` 與 `Unresolved` 回到 Runner 並保存成 `pending`。Git 若在回報未確認之前已經完成刪除，不假裝可以回滾——但也不回報成功。送出 signal 不等於清理完成，這個區別是 `RECOVERY_BLOCKED` 存在的理由。被中斷而沒有產生結果的 Verification Run 走既有 reclaim 流程保存 INTERRUPTED Evidence，不製造工程 FAIL，也不留下可正常結案卻未結案的 VERIFYING；verdict 在 check 期間被改動時仍然 fail closed，不為了清理強行寫回狀態。

**程序清理在每一條路徑上都要做，正常退出也一樣。** `make verify` 分叉出的背景子程序在主程序 exit 0 之後仍然活著、仍然在寫這個 worktree，而且已經在 session digest 的窗口之外。它由 `internal/process` 的共用 helper 處理：子程序一律以 `Setpgid` 啟動、輸出給的是 ForgePilot 自己持有的描述元（交給 `os/exec` 的管線會讓 `Wait` 等到每個繼承它的子孫關閉為止，清理程式碼因此可能永遠走不到）、停止有界、結果要確認。無法確認清理完成時 Runner 停止並回報，不宣告可以安全前進，也不清掉恢復所需的 ownership 資訊；已經成立並保存的 Evidence 照常保留，因為工程結果與執行安全是兩個不同的判斷。保證的範圍限於受管理的程序群組——脫離群組的 daemon 不在內，這不是作業系統層級的隔離。

獨立的 `forgepilot verify` 契約不變：它沒有內建時間上限（[ADR-0004](adr/0004-verifying-liveness-via-flock.md)），共用 service 不是給它加上 Runner 總期限的理由；它同樣會清理自己啟動的程序群組，並在無法確認時印出警告而不改變 PASS／FAIL 的退出碼。詳見 [ADR-0021](adr/0021-execution-limits-are-bounded-and-named.md)。

無進展以語意事實判斷——每張工作的 status、最新 verification 結果、freshness、open gate 數、Goal 狀態、本次 action 與目前 Candidate。Evidence ID、timestamp、attempt 編號與 log 量刻意不在其中，因為它們正是「什麼都沒動」時仍會變的東西。連續三輪語意事實完全相同即停止。

### 執行紀錄與容量

`.forgepilot/runs/<run-id>/` 保存 `run.json`（原子替換）、`steps.jsonl` 與每次 session 的 `handoff.md`、`session.log`、`result.json`。它落在 `init` 寫入的既有 `.forgepilot/` ignore 範圍內，因此不改變 Candidate digest。

單次寫入、單 run 與全部 runs 的總容量各有有限上限，全部可注入，且都在啟動前驗證：不接受 `0`，也不接受由內而外不遞增的組合。單次寫入的上限管的是 agent session 自己產出的東西，不套用在 run record 上——run record 的大小由其結構決定，而因為一個 console 輸出旗標就寫不出 run record，會讓已啟動的 worker 失去可恢復的紀錄。session 會自己寫檔，所以啟動前先預留空間；console log 超過單次上限即截斷並在 log 內明說。超限是停止，不是清理的理由——總檢需要的 artifacts、Evidence 與 snapshot refs 不會被自動刪除，拒絕訊息指出該檢查哪個目錄。

`run status` 分開呈現「當時的停止結果」與「目前重新計算的 Goal readiness」，並標示 scope 是否已經改變。這兩者在 workspace 變動後會不一致，把儲存的結論當成現況陳述正是要避免的事。
