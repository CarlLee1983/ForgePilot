# Architecture

## 文件狀態與範圍

本文件描述 [ADR-0040](adr/0040-forgepilot-is-a-passive-dag-ledger.md) 收斂後的 ForgePilot：一本被動的 DAG 帳本。外部 Agent 跑迴圈；ForgePilot 判定下一個合法動作、保存綁定確切 Candidate 的 Verification Evidence，並在工作完成的同一次交易內解鎖下游。它不啟動 coding CLI，除了 Git 與受管理 repository 的 canonical `make verify` 之外不啟動任何程序，也不發出網路請求。

核心名詞只在 [CONTEXT.md](../CONTEXT.md) 定義；CLI 契約只在 [development-plan.md](development-plan.md) 維護，各 milestone 的驗收紀錄也在那裡。已被取代的決定留在 [ADR](adr/README.md)，本文件只寫現行的邊界。

本文件的視覺化見 [diagrams/](diagrams/README.md)：狀態機、分層與依賴方向、交易邊界，以及 `verify` 與 `review approve` 的順序。圖與本文件衝突時以本文件與程式碼為準。

## Authority boundaries

| 擁有者 | 責任 |
|---|---|
| Human | Goal 拆分與 approval、架構／範圍／安全判斷、production operation、merge／release authorization |
| ForgePilot | Goal、DAG、工作狀態、Gate、Evidence、Candidate identity、next-action 判定 |
| PraxisBound | Story schema、acceptance criteria、工程流程、coding standards、測試與驗證契約、人工審查原則 |
| Repository | 程式碼、tests、formatters、linters、type／architecture checks、canonical `make verify`，以及它自己固定的 toolchain |
| Agent | 驅動迴圈：依 `next` 取得動作，讀 Story、實作、修復、呼叫 `verify`；需要人判斷時開 Gate |

Work Item 只保存 `story_ref`，不複製 Story requirements。ForgePilot 不讀程式碼後自行判斷正確性，也不替代 PraxisBound 的工程 lifecycle。

### Graph Engineering：規格拆分至 DAG 驅動閉環

1. **上游規格體系（PraxisBound）**：ADR 固化架構取捨，Spec 界定模組邊界與契約，再拆成各自帶 acceptance criteria 與驗證指令的 Story。
2. **下游 DAG 帳本（ForgePilot）**：
   - **Goal Plan 匯入**：`goal import <plan>` 一次建立 Goal 與所有節點，節點 ID 即 Work Item ID；再次匯入只接受新增節點。ForgePilot 只驗證合法 DAG 與 Story 路徑存在，拆得對不對是上游與人的責任。
   - **拓撲推進**：readiness 讀取時計算，`next` 給出唯一建議動作，同一 workspace 同時最多一件 RUNNING 或 VERIFYING。
   - **完成條件**：`verify` 在 immutable Candidate 的隔離 checkout 執行 repository 的 `make verify`；PASS 即 DONE 並同交易解鎖下游。Goal 有 Approval Requirement 時 PASS 先進 REVIEW，`review approve` 後才 DONE。全部 DONE 時 Goal 自動完成。
   - **人工介入**：需要人判斷時以 Gate 記錄並阻擋該工作；換 session 的 Agent 由 `status`／`next` 得知正在等人。

Breaking change、architecture trade-off、security-sensitive decision、production operation、destructive action、scope expansion、ambiguous requirement、merge／release authorization 都需要明確 Human Decision。這些決策以 Gate 表示並保存；merge／release authorization 不在產品範圍內，Gate resolution 不授予該權限。

## Implementation boundaries

Go 1.25.5、只用標準函式庫、module `github.com/CarlLee1983/ForgePilot`，只支援 macOS 本機檔案系統。

| Package | 責任 |
|---|---|
| `internal/cli` | 參數、呈現、錯誤映射；不自行決定 transition 合法性 |
| `internal/app` | `verify`、`goal import` 與 Candidate facts 的 orchestration；`verify` 的流程只有這一份（`internal/cli/verify.go` 是薄殼）。`status`、`review`、`gate` 的讀取路徑 `internal/cli` 仍直接用 `storage` 與 `repository` |
| `internal/work` | 純狀態機：Goal／Work Item／Evidence／Gate、Goal Plan 驗證、transition policy、readiness、`next` 判定。沒有 interface，外部事實以純值參數傳入（時間是 `now`，Git 事實是 `RepositoryState`，執行結果是 exit code） |
| `internal/repository` | 唯一碰 Git 的地方：Story 路徑檢查、revision、Candidate Snapshot、detached worktree、canonical check 的啟動 |
| `internal/storage` | state snapshot、交易鎖、decode／validate、原子保存、Verification 的 flock |
| `internal/process` | 程序群組的啟動、有界停止與停止確認；`make verify` 會 fork，只終止它的 leader 會讓真正的工作繼續跑 |


由入口組裝依賴。不為每個 entity 預先建立 Save／Get repository interfaces；`storage.Update(root, func(*work.State) error)` 是唯一的回呼形態。

Goal、Work Item、Evidence、Gate 共用同一份 JSON snapshot 與同一次受鎖的原子替換，不能以多次獨立 Save 取代。完成一件工作與解鎖其下游必須落在同一次交易內，否則讀取者會看到「A 已 DONE 但 B 仍 PENDING」的中間狀態——所以下游的 readiness 不寫入，而是讀取時計算。

## Domain data

| 物件 | 欄位 |
|---|---|
| Goal | `id`, `title`, `description`, `repository`, `require_approval`, `status`, `reason`（僅 CANCELLED）, `created_at`, `updated_at` |
| Work Item | `id`, `goal_id`, `story_ref`, `status`, `depends_on`, `current_run`, `created_at`, `updated_at` |
| Work Item run | `current_run`：Verification Run ID、Candidate identity（kind／revision／base／digest）、worktree path、log path、`started_at`；閒置時為 null |
| Gate | `id`（`GATE-001`）、`work_item_id`、`question`、`rationale`、`options`（至少兩個）、`status`、`opened_at`，以及關閉時的 `choice`／`note`（resolve）或 `reason`（cancel）、`decided_by`、`decided_at` |
| Evidence | `id`（`EV-001`）、`type`、repository、Work Item、Story、完整 Candidate identity、`result`、`created_at`；verification 另有 Verification Run ID、`command`、`exit_code`，review 另有 `reviewer` 與 `note` |

Goal statuses：`ACTIVE`、`COMPLETED`、`CANCELLED`。Goal 的 `require_approval` 在匯入時定下，之後不能改。

Work Item 持久化的 status 只有五個：`NOT_STARTED`、`RUNNING`、`VERIFYING`、`REVIEW`、`DONE`。`REVIEW` 只存在於要求 Approval 的 Goal 底下。讀者看到的 `PENDING`／`READY` 是 `NOT_STARTED` 在讀取時依「依賴是否全部 DONE」算出的投影，從不保存；沒有 `reconcile`，也沒有需要被補寫的欄位。

沒有 `BLOCKED` 或 `WAITING_HUMAN`：阻擋由「該 Work Item 有沒有未解除的 Gate」表達，不佔用狀態欄。兩處各表達一次同一事實，就會需要「記住進入阻擋前是什麼狀態」這種只為修補覆寫而存在的欄位。詳見 [ADR-0007](adr/0007-blocking-is-not-a-status.md)。Goal 也沒有 `BLOCKED`；擋整個 Goal 用 `goal cancel` 結束它。

Gate statuses：`OPEN`、`RESOLVED`、`CANCELLED`。Gate resolution 不等同於 Human Review，也不授予 merge／release 權限。

Evidence 沒有 PR 欄位，Work Item 沒有 revision 欄位：revision 只存在於 Evidence 與 `current_run`（[ADR-0003](adr/0003-no-work-item-target-revision.md)）。Evidence 沒有指向輸出的 path 欄位，輸出以 Verification Run 為鍵存在 state 之外（[ADR-0012](adr/0012-verification-log-outside-state.md)）。

## Repository 與 Story identity

每個 `.forgepilot/` 管理一個 repository，可以包含多個 Goal；不建立跨 repository 的全域佇列。`init` 在 repository root 建立 state；後續命令從目前目錄向上尋找最近的 `.forgepilot/`，找不到時要求先 init。Goal 的 repository 必須與該 state root 一致。

Story reference 是 repository-relative path，必須存在且位於 repository 的 `specs/stories/` 底下（該目錄必須存在），可指向檔案或目錄；不猜測其 business schema。路徑正規化與 symlink 解析後仍須落在 `specs/stories/` 內，拒絕絕對路徑、`..` 穿越與逃出的 reference（`internal/repository/repository.go` 的 `ValidateStory`）。

## Goal Plan 與 DAG

`goal import <plan-path>` 讀一份 JSON（標準函式庫解析、拒絕未知欄位），格式見 [development-plan.md](development-plan.md#goal-plan-格式)。規則：

- 節點 ID 即 Work Item ID，在 workspace 內唯一；Goal ID 與節點 ID 同一字元規則，且因為 Work Item ID 會被嵌進 snapshot ref，必須是合法的 Git ref component。
- 依賴只能指向同一 Goal 的節點。拒絕環、未知、自我與重複依賴、重複節點、無節點的計畫，以及不存在、不在 `specs/stories/` 底下、路徑穿越或 symlink 逃逸的 Story。任何一項失敗整份不寫入，錯誤指出節點與欄位。
- 節點順序是多件工作同時 READY 時的推薦 tie-break；跨 Goal 的順序是 Goal 匯入的順序。
- 重新匯入同一 Goal 只能新增節點：Goal 屬性必須相同；既有節點必須全部列出，其 `story` 與 `depends_on`（依賴以集合比較，重排不算改動）必須不變，且不重新檢查 Story 是否仍存在——已完成而後來被搬走的 Story 不該凍結整個 Goal；新節點可依賴新舊節點（含已 DONE 者）；完全相同是無變化的成功；終態 Goal 拒絕；整份原子寫入。
- 工作的存在與依賴在建立後不可修改或刪除，因此新增節點不能產生環；載入 state 仍驗證完整資料一致性。

## Readiness、佔位與 selection

- Readiness 是 `NOT_STARTED` 工作在讀取時的計算：ACTIVE Goal 底下，每個依賴都是 DONE 為 READY，否則 PENDING。已開始的工作與終態 Goal 的工作沒有 readiness。READY 不表示可以忽略該工作自己的 Gate。
- 同一 workspace 最多一件 RUNNING 或 VERIFYING（含孤兒 VERIFYING）；REVIEW 不佔位，因為人在決定時沒有任何東西在執行。終態 Goal 的工作不佔位，否則它永遠無法結束而會卡死整個 workspace。`start` 與任何會讓工作回到 RUNNING／VERIFYING 的 transition（`review reject`、對 REVIEW 工作重新 `verify`）在佔位時被拒絕，錯誤指出佔位的工作與脫身方式。
- `start` 在寫交易內重查 Goal、依賴、Gate 與佔位；不能只相信先前讀到的結果。

## Central transition policy

| Transition | 條件 |
|---|---|
| NOT_STARTED → RUNNING（`start`） | ACTIVE Goal、全部依賴 DONE、無未解除 Gate、workspace 無其他 RUNNING／VERIFYING |
| RUNNING → VERIFYING（`verify`） | 無未解除 Gate、ACTIVE Goal、無其他佔位；Candidate 已固定 |
| REVIEW → VERIFYING（`verify`） | 同上；用於 Candidate 已 stale 時重新驗證 |
| VERIFYING → DONE | PASS、Goal 無 Approval Requirement、ACTIVE Goal、無未解除 Gate；同一交易解鎖下游，若為 Goal 最後一件則 Goal → COMPLETED |
| VERIFYING → REVIEW | PASS、Goal 有 Approval Requirement |
| VERIFYING → RUNNING | FAIL 或 INTERRUPTED；PASS 但 Goal 已非 ACTIVE 或期間開了 Gate 時，Evidence 照記、工作留在 RUNNING，由之後的 verify 完成 |
| REVIEW → DONE（`review approve`） | 最新 Verification 為 PASS 且仍是目前 Candidate、無未解除 Gate、ACTIVE Goal；同一交易解鎖下游並可完成 Goal |
| REVIEW → RUNNING（`review reject`） | 需附理由；不受 Gate 或 stale 約束，停下工作不需要它們所缺的授權；受佔位規則約束 |

非法 transition 回傳明確 domain error，不能偷偷改成另一個操作。沒有完成指令、任意 state setter 或測試專用 approve：DONE 只能是上表的結果（[ADR-0008](adr/0008-approval-completes-work.md) 仍成立的部分）。

開啟或關閉 Gate 不是 transition：它不改變 Work Item 的狀態，只改變它能否推進。Gate 可以開在任何非 DONE 的工作上。

DONE 是終態，不因後續 commit 重開也不判 stale，沒有 reopen；要重做就新增一件 Work Item，讓「為什麼重做」有地方被記錄（[ADR-0006](adr/0006-done-is-terminal.md)）。Goal 完成的條件只有「每個節點都是 DONE」，不再要求每個節點的 PASS 都對上最終 Candidate——拓撲序中後完成的節點，其 `make verify` 已涵蓋整個 repository。

## `next` 與 `status`

`next` 的資料流是 domain state + 目前 repository facts → `ActionableNext()` → CLI formatter。它是純讀的 projection，不是 lifecycle state，不持久化，也從不替 Agent 執行建議。它只回傳一個動作，順序為：

1. 佔住 workspace 的工作：RUNNING 且最新 Verification 為 FAIL 為 `REPAIR`，否則 `RESUME`；VERIFYING 且 verifier 已不存在（孤兒）為 `RECOVER`，verifier 仍在跑為 `WAIT`；佔位的工作被 Gate 擋住則進入等待判定。
2. 要求 Approval 的 Goal 中，Candidate 已 stale 且可驗證的 REVIEW 工作：`REVERIFY`。
3. 最早的、ACTIVE Goal 底下 READY 且無未解除 Gate 的工作：`START`。
4. 等人：列出 OPEN Gate 與 Candidate 仍是目前版本的 REVIEW 工作：`WAIT`。
5. 已結束的 Goal（沒有任何 ACTIVE Goal 時）：`GOAL_ALREADY_COMPLETED` 或 `GOAL_CANCELLED`；什麼都沒有：`NONE`。

佔位時第 2、3 步跳過，因為它們推薦的 transition 會被拒絕。「最早」是 `created_at`，同時間以工作在 state 中的位置（即計畫的節點順序）決勝。`--json` 的形狀固定：每個欄位永遠存在，沒有值就是空字串，`waiting` 是空陣列。

`status [--goal] [--work] [--json]` 列出每個 Goal 的節點、狀態（PENDING／READY 為計算值）、最新 Verification 與 Human Review、未解除 Gate，並對每件未完成工作說明為何不能前進（依賴、Gate、等待 approval、佔位、verification 進行中、Goal 已結束）。DONE 的工作不標示 stale——stale 的用途是提示需要重驗，對終態工作那個提示是錯的，而沒有行動意義的警示會讓人開始忽略所有警示。`status` 不對未完成的原因沉默（ADR-0008）。

## Verification 與 Candidate

Candidate 是 Evidence 所針對的 immutable code identity，不是另一套 workflow state。`COMMIT` candidate 只有既有 revision；`SNAPSHOT` candidate 另保存建立時的 `base_revision` 與自動計算的 `candidate_digest`，其 `revision` 是可 checkout 的 snapshot commit。Candidate 只存在於 `current_run` 與 Evidence；Evidence 從已固定的 run candidate 取得 identity，不重讀 live workspace。

`verify <work-id>`：要求工作樹乾淨（tracked 無修改、無 staged、無 untracked；ignored 不計），解析 HEAD，以 `git worktree add --detach` 在 `.forgepilot/worktrees/` 建立隔離 checkout，在其中執行 repository 的 `make verify`。canonical 檢查的存在性也在該 checkout 內判斷——被 gitignore 的 Makefile 會讓主工作樹看起來可驗證，而受驗的 commit 其實沒有。因此受管理專案的 `make verify` 必須能在全新 checkout 上執行（[ADR-0002](adr/0002-verify-in-detached-worktree.md)）。缺少 `make verify` target 是無法驗證，不是驗證失敗：拒絕執行且不留 Evidence。

Toolchain 由受管理專案的 `make verify` 自行固定，ForgePilot 不解析 runtime 宣告，檢查繼承呼叫者的環境。一次 `verify` 只為觸發它的那件工作留下 Evidence。

`verify <work-id> --snapshot`：用 private Git index 從 HEAD seed 後 `git add -A`，收進 tracked staged／unstaged 修改、tracked deletion 與 non-ignored untracked files；ignored 檔案不進 Candidate，原本 tracked 者仍會進。capture 前後的 current branch、HEAD、real index、staging state 與 working files 必須相同。snapshot tree 先形成 immutable commit，再以 `refs/forgepilot/snapshots/<work-id>/...` 保留，之後才開始 Verification Run；ref 不屬於 branch、tag 或正常 history、只存在本機，即使後續失敗也不刪除，以免 state 指向會被 GC 回收的 object。digest 是 `sha256:` 加 lowercase hex，涵蓋版本化格式、base revision 與依 path 排序的 Git tree entry；`status` 與 review 重算 digest 時另用 temporary object database，避免查詢把 loose objects 寫進 repository（[ADR-0014](adr/0014-working-tree-snapshot-is-a-candidate.md)）。

Stale 只對 REVIEW 工作有意義：COMMIT Evidence 比較目前 HEAD，SNAPSHOT Evidence 比較目前 workspace digest。stale 不造成任何自動 transition，只有明確的 `verify` 重新驗證，`review approve` 在 stale 時拒絕並指出該跑的命令。Snapshot 的 approve 先重算 digest，相同才把 review 綁回已驗證的 snapshot revision。

Evidence 的 `result`：`PASS`、`FAIL`、`INTERRUPTED`。INTERRUPTED 沒有 exit code（欄位為 null）——未產生結果就沒有結果碼，填 0 會被讀成成功；它不得視為 FAIL。Evidence 只 append，不覆寫。同一 revision 的多筆結果各取最新一筆，曾經出現過即算數會讓「重跑以確認 PASS 是否穩定」變成漏洞。已知限制：只終止 `make` 子程序而 ForgePilot 自身存活時，ForgePilot 收到的是 exit code，會記為 FAIL；中斷語意只涵蓋 ForgePilot 自身被終止。

Verification Run 期間持有兩把 flock：`locks/verify-<work-id>` 作為該工作的存活標記（OS 在程序終止時自動釋放，PID 會重用所以不能用；不設逾時上限，[ADR-0004](adr/0004-verifying-liveness-via-flock.md)），以及 repository 層級、非阻塞的 `locks/canonical-verification.lock`。取鎖順序固定為前者再後者，競爭失敗在建立 run、Candidate artifact、log 或 subprocess 之前就拒絕。孤兒 VERIFYING 由下一次 `verify` 的開頭交易回收：append 一筆 INTERRUPTED Evidence 並退回 RUNNING，不推斷 PASS 或 FAIL。回收發生在任何拒絕之前，且不受 Gate 或 Goal 狀態約束——中斷是已發生的事實，被擋住的是開始新的執行（[ADR-0009](adr/0009-reclaim-before-refusing.md)）。

canonical 檢查的輸出邊執行邊串流寫進 `.forgepilot/logs/`，檔名 `<VR-n>-<short-sha>-<started-at>-<random>.log`，以 run 為鍵（Evidence ID 在交易結束後才發號，開檔時還不存在）；`current_run` 保存實際的 log path，Evidence 只保存 Verification Run ID。log 開不起來就中止 `verify`，不靜默降級。log 與 `worktrees/` 不自動清理（worktree 於 run 結束後一律移除，每次 `verify` 前先 `git worktree prune`）。輸出是診斷材料，結論只在 Evidence。

## Human decision

OPEN Gate 必須阻擋工作推進：開著的 Gate 讓該工作不能 `start`、不能 `verify`、不被 `next` 推薦、也不能到達 DONE。Resolve 保存明確的選擇與時間；不能由 Agent 推測選項或以逾時當同意。

- Gate 只依附單一 Work Item，沒有 Goal 層級 Gate；開啟者不區分人或 Agent。一件工作可同時有多個 OPEN Gate，推進條件是 OPEN 數為零。
- `options` 必填且至少兩個；`resolve` 只能選其中之一並可附 note；選項全都不對時，正確動作是 `cancel`（須附理由）再開一個。CANCELLED 同樣解除阻擋，差別只在它記錄「這個問題問錯了」。Gate 集合 append-only，進入 RESOLVED 或 CANCELLED 後不可變更。
- 決策者身分是自述（預設取 Git 的 `user.email`，可用 `--by` 覆寫），不做認證——半套的認證比不做更危險（[ADR-0005](adr/0005-self-asserted-decision-maker.md)）。

Human Review 只存在於要求 Approval 的 Goal，是 Evidence 的第二個 `type`，與 Verification 共用同一容器與同一條 ID 序列，因此一件工作的歷史是單一時間軸。它綁定確切的 Candidate；`result` 為 `APPROVED` 或 `REJECTED`，REJECTED 必須附理由。review 沒有 `command` 與 `exit_code`，verification 不得攜帶 `reviewer` 或 `note`，兩者以 `type` 分流驗證。`review approve` 在記錄的同一次交易內檢查完成條件，不滿足就拒絕而不記錄。對不要求 Approval 的 Goal，`review` 一律拒絕並說明原因。

## Durable local storage

```text
.forgepilot/
├── state.json
├── locks/
│   ├── verify-<work-id>
│   └── canonical-verification.lock
├── worktrees/
│   └── <work-id>-<short-sha>/
└── logs/
    └── <VR-n>-<short-sha>-<started-at>-<random>.log
```

State snapshot 包含 `schema_version`、`next_evidence_id`、`next_gate_id`、`next_verification_run_id`、Goals、Work Items、Evidence 與 Gates。Timestamp 使用 UTC；規則測試使用可控制的時間。

每次修改遵循：取得固定 lock file 的程序鎖 → 讀取、decode、驗證 snapshot → 執行 domain operation → 寫入同目錄暫存檔並同步 → 原子替換 `state.json`，並同步目錄 → 釋放鎖。鎖涵蓋 read-modify-write 全程；只有 atomic rename 不足以防止 lost update。鎖使用 OS 在程序終止時自動釋放的 `flock` advisory lock，所以生命週期不依賴 Agent session，也不靠可能殘留的 PID 檔；其他平台在驗證相同行為後才加入。

`next`／`status` 讀取完整 snapshot，不取鎖、不寫入。成功回應只能發生在保存成功之後；寫入失敗須留下可讀的舊 snapshot 或完整的新 snapshot，不得留下截斷 JSON。其他要求：

- 重複 `init` 不覆寫 state；`.gitignore` 只補上缺少的 `.forgepilot/` entry，保留既有內容。注意這會讓 `.gitignore` 在新 repository 中成為未追蹤檔，commit-mode `verify` 在它被 commit 前會因工作樹不乾淨而拒絕。
- JSON 損毀、資料不一致與不支援的 schema version 明確拒讀，不清空重建；decode 拒絕未知欄位。
- State 是本機信任資料，不宣稱具有防竄改或身份認證能力。

### Schema 19

目前的 schema 版本是 19，是一次斷代：新 binary 只讀 19，沒有 migration 框架，也沒有 `migrate` 指令。讀到較舊的 state 時拒絕並建議以 Goal Plan 重新 `goal import`，讀到較新的拒絕為「較新 schema」，避免舊 binary 重新保存時丟失未知欄位。既有 repository 的做法是：為每個未完成的 Goal 寫一份 Goal Plan，把舊 `.forgepilot/` 封存（不刪除），`init` 後逐份 `goal import`；一次性匯出腳本已在三個採用 repository 遷移後刪除。下一次升版時再決定需不需要 migration 框架。

## 網路與程序邊界

ForgePilot 不發出任何網路請求：不自行 HTTP、不 spawn `gh`（[ADR-0010](adr/0010-no-outbound-network-requests.md)）。除 Git 與受管理 repository 的 `make verify` 外不啟動任何程序，Git 呼叫全部集中在 `internal/repository`。ForgePilot 不持有憑證，也不知道 Agent 是誰：Agent 的 session、進度與執行歷程都在 ForgePilot 之外。
