# 11 — 一輪受控的真實 Codex smoke，以及它留下來的證據

[10](10-recovery-to-final-review.md) 之前的每一張票，都是用 `--runtime fake` 的
subprocess adapter 驗收的。那證明了 ForgePilot 的程序、鎖與恢復處理正確，
不證明無人值守跑真實模型會走到同一個結果——
`AGENTS.md` 的「地雷」一節把這句話寫在那裡，就是為了不讓綠燈被讀成後者。

本輪做兩件事，而且順序是固定的：

1. 先補齊證據留存。smoke fixture 是一個 `t.TempDir()`，
   測試結束就連同 `.forgepilot/`、session 產出與 Git objects 一起消失，
   跑完之後能讀的只剩終端機捲軸。一輪真實 smoke 會消耗模型額度、無法重播，
   正是「證據必須比執行本身活得久」的那種情況。
2. 再跑一輪受控的真實 Codex smoke，用既有測試
   `TestCodexSmokeDrivesDependentWorkToTheGoalReviewBoundary`，不改契約。

**本輪只驗收既有正常執行路徑。** 各種 recovery 情境（07、08、09、10）
仍然只有 fake runtime 的驗收，本輪沒有碰它們；一次 smoke 成功也不是
長時間無人值守可靠度的保證。沒有新的架構決策，因此沒有新的 ADR。

## Baseline 與實際受測版本

| | |
| --- | --- |
| 分支 | `test/real-codex-smoke-acceptance` |
| Baseline | `de55ba4`（main，PR #23 合併後），working tree 乾淨 |
| 實際受測 source SHA | `2deaf14`（Go 程式碼未再改動；受測時 working tree 只有文件修改，見下） |
| 環境 | macOS 26.5.1、arm64、Go 1.25.5、git 2.55.0、codex-cli 0.154.0 |

開工前在未啟用 smoke 的環境重跑 baseline：

```
env -u FORGEPILOT_CODEX_SMOKE make verify              → exit 0（root 套件 307.9s）
```

## 改了什麼

### `smoke_artifact_export_test.go`（新增，測試專用）

`FORGEPILOT_SMOKE_ARTIFACT_DIR` 是本輪新增的**測試用**設定，不是產品 CLI flag。
它把一輪的證據複製到 fixture 之外的一個全新目錄，並附一份 SHA-256 manifest。

它與 `FORGEPILOT_CODEX_SMOKE` 刻意分開：**要證據不能是開始花額度的原因**。
變數未設時，任何測試都不會寫到自己的暫存目錄以外。

型別本身只有兩個性質是重點：

- **不覆蓋**。目錄名帶 UTC 時間戳與隨機尾碼，且用 `os.Mkdir` 建立——
  已存在就是錯誤，不是靜默合併。同一秒起跑的兩輪也不會撞在一起。
- **不吞錯**。三種情況分成三欄，因為對讀 manifest 的人意義不同：
  複製失敗累積在 `failures`（匯出壞了，`close` 回報並點名檔案）；
  「來源根本沒產生」記為 `missing`（那一輪真的缺這份證據）；
  「刻意不匯出」記為 `excluded_on_purpose`（`locks/`、verification worktree、
  `.git`——不是證據，但仍寫下來，不留給後人猜）。
  `manifest.Complete` 只看前兩者。

### `smoke_evidence_test.go`（新增，測試專用）

把一輪的東西實際收齊。註冊順序就是清理順序：
`t.Cleanup` 後進先出，而 fixture 的暫存目錄在被建立時就登記了自己的刪除，
所以這裡登記的匯出必然先於它們執行；此時 Runner 程序已經結束
（`runForge` 等它結束），fixture 裡沒有還在寫的 writer。

收的內容對應工單的四類：

- **A 執行身分與設定** — `environment.json`：ForgePilot source SHA 與 working tree
  是否有修改（有的話另存 `forgepilot-working-tree.patch`）、macOS／arch／Go／Git／
  Codex 版本、實際命令與 Runner 預算、起訖時間與時區、run record 裡的 runtime 版本。
  **模型資訊分兩欄**：`codex_settings_requested_by_configuration`（設定要求的值，
  以白名單取 key，不整份複製 `config.toml`——那裡面可能有 MCP server 設定）
  與 `model_identity_reported_by_the_session_logs`（執行紀錄實際回報的值，
  讀不到就是 `unknown`，不由 CLI 版本推測）。
- **B 實際輸入** — 三張 Story 的實際內容、fixture 的 `AGENTS.md`、`Makefile`
  （canonical verification 的定義）、`go.mod`，以及每次 session 的 `handoff.md`
  與 adapter 交給模型的 `result-schema.json`（都在 `forgepilot/runs/` 底下）。
  只記 Story 路徑不算保存輸入。
- **C 執行與驗證結果** — 整個 `.forgepilot/`（run record、`steps.jsonl`、
  每個 attempt 的 `result.json`／`session.log`／`handoff.md`、verification log、
  最終 `state.json`），加上 `git bundle --all` 產生的 `fixture.bundle`。
  最後一項是為了「只保存 snapshot SHA 而讓 Git objects 隨 fixture 被刪除，
  不算完整保存被驗證的內容」——bundle 帶著 `refs/forgepilot/snapshots/`，
  被驗證的 Candidate 可以從匯出本身還原出來。
- **D 證據索引** — `manifest.json`：相對路徑、SHA-256、對應 Run ID／Work Item／
  attempt（由 `runs/<run>/wi-00N-attempt-M/` 路徑推得），
  以及缺漏（`missing`）、刻意排除（`excluded_on_purpose`）與匯出失敗（`failures`），各附原因。

認證檔、token、完整 HOME 與未篩選的環境變數一律不碰。

`go test` 自己的輸出與退出碼在測試內部拿不到，
所以匯出目錄裡放一份 `outer-capture.md` 說明這件事與正確的擷取方式
（`tee` 會回報 tee 的退出碼，不是測試的）。

### `runner_codex_smoke_test.go`（修改）

- 預算改為在呼叫處完整寫出：`--max-steps 18 --max-attempts-per-work 2
  --agent-timeout 10m --max-duration 45m --verify-timeout 5m`。
  這是本輪的執行預算，**不是改產品預設值**——`internal/cli/run.go` 的
  `defaultBudget()` 一行未動。
- 驗收條件改用既有 typed query，不靠搜尋終端輸出的成功字串：
  - `runner.LoadRecord` 讀回 `Stop.Reason`、`EvidenceIDs`、`Worker`、`UnresolvedPending()`；
  - CLI 退出碼與 `Stop.Reason.ExitCode()` 對照，而不是只檢查 0；
  - `app.GoalReadiness` 在 fixture 清理前重算 Goal readiness，
    必須是 `GoalAwaitingFinalReview`，且其 Evidence IDs 與 run record 的一致；
  - 沒有任何 Work Item 是 `DONE`、沒有任何 `ReviewEvidence`、Goal 仍是 `ACTIVE`、
    沒有未解決的 Gate。
- **attempts 的斷言分開報告**。既有測試要求每張工作恰好一次成功。
  那是對模型的期待，不是 Runner 的契約：`--max-attempts-per-work 2` 明白允許
  第二次 attempt，而 Runner 從第二次 attempt 走到 `AWAITING_GOAL_REVIEW`
  仍然完全符合契約。所以斷言保留（沒有刪除、沒有放寬），但改成最後一項、
  用 `t.Errorf` 並在訊息裡標明 `SMOKE EXPECTATION (not a Runner contract failure)`，
  讓 Runner 實際結果與 smoke 測試結果可以分別讀到。

### 一次 code review 之後改的東西

review 找到四個 HIGH，全部修掉，其中兩個正是 `AGENTS.md`
「註解不算數，斷言才算數」那條地雷的同型錯誤：

1. **gap 不會讓任何人知道。** cleanup 原本只在複製失敗時 `t.Errorf`；
   `git bundle` 失敗或整個 `.forgepilot/` 沒產生，只會讓 `complete` 變 `false`，
   測試照樣 PASS。現在 cleanup 也對 `len(export.missing) > 0` 報錯。
2. **`len(state.Gates) != 0` 的註解寫「none outstanding」，
   實際斷言的是「從來沒有開過 Gate」**——`state.Gates` 是完整歷史，含已關閉的。
   改成逐筆看 `gate.Status == work.GateOpen`。
3. **DONE 檢查是死碼。** 它排在 `!= Verified` 的 `t.Fatalf` 後面，永遠到不了，
   真的出現 DONE 時使用者看到的是 `want VERIFIED`。改成先檢查 DONE。
4. **`SMOKE EXPECTATION` 的訊息說自己不是失敗，卻用 `t.Errorf` 讓測試變紅。**
   斷言保留為失敗（降級才是偷偷放寬標準），但訊息改成
   `SMOKE EXPECTATION FAILED (the Runner contract above held)`，
   不再宣稱自己不在判決裡。

另外修掉的：`gitCapture` 原本把錯誤字串當成內容存成 `.patch`／`.txt` 且算成功——
改成回傳 error，失敗記 gap；`copyFile` 補上 regular-file 檢查
（fixture 是真實模型有寫入權的目錄，symlink 會把外面的東西複製進來）；
`close` 用 `errors.Join` 不再丟掉 manifest 自己的寫入錯誤；
證據收集器裡的 `t.Fatal` 拿掉（它會跳過 `close`，讓已複製的檔案沒有索引）；
bundle 直接產在匯出目錄裡，不再經過系統 `TMPDIR`。

新增 `TestTheRecordedSandboxIsTheOneTheAdapterAsks`：
`environment.json` 裡那句 sandbox 說明是手寫的，
這條測試把它綁到 `agent.Codex.Plan` 實際產生的 argument list 上，
adapter 一改就會紅，而不是讓證據繼續宣稱舊設定。

### `smoke_artifact_export_behaviour_test.go`（新增）

匯出 helper 的測試，全部不需要模型，跟著預設 suite 跑：
成功路徑留存並可由 manifest 核對 digest、缺漏記為 gap 但仍寫出 manifest、
刻意排除不會讓 manifest 誤報為不完整、
匯出錯誤不被吞掉（`close` 回報並點名失敗檔案）、
每輪自己的目錄不覆蓋上一輪、逃逸路徑被拒絕、目的地必須是絕對路徑。

另有 `TestSmokeEvidenceOutlivesTheFixtureItCameFrom`：
用 `--runtime fake` 跑完一輪真的 Runner，再整套匯出一次，
斷言 `environment.json`、輸入、`state.json`、run record、
`result.json`／`handoff.md` 與 bundle 都在，
並且把 bundle clone 回來、用 `git cat-file -t` 確認
Verification Evidence 指的那個 snapshot commit 真的在匯出裡。
這是唯一能在花額度之前發現「匯出接錯了」的方法。

## 真實 smoke 的執行

**結果：PASS。**

```
FORGEPILOT_CODEX_SMOKE=1 \
FORGEPILOT_SMOKE_ARTIFACT_DIR="/Users/carl/.forgepilot-smoke-evidence" \
go test -count=1 -v -timeout=60m \
  -run '^TestCodexSmokeDrivesDependentWorkToTheGoalReviewBoundary$' . \
  > go-test.log 2>&1; echo $? > go-test-exit-code.txt
```

退出碼由 `echo $?` 直接取得，沒有經過 `tee`。

上列是當時的執行紀錄。現在重跑請使用目前的測試名稱，並明確指定要授權的模型；單獨設定模型不會啟動 smoke：

```
FORGEPILOT_CODEX_SMOKE=1 FORGEPILOT_SMOKE_MODEL="gpt-5.6-sol" \
go test -count=1 -v -timeout=60m \
  -run '^TestCodexSmokeDrivesDependentWorkToGoalCompletion$' .
```

| | |
| --- | --- |
| Run ID | `run-20260915t034856-f0e621` |
| 起訖 | 2026-09-15 11:48:56 → 12:00:51（CST+08:00），go test 回報 716.18s |
| `go test` 退出碼 | 0 |
| Runner 退出碼 | 0（`AWAITING_GOAL_REVIEW` 的契約值） |
| Runtime | codex，`codex-cli 0.154.0` |
| 設定要求的模型 | `gpt-5.6-sol`，`model_reasoning_effort = high`，`approval_policy = on-request` |
| session log 實際回報的模型 | `gpt-5.6-sol` |
| Sandbox | `--sandbox workspace-write`，adapter 明確指定，沒有任何 bypass flag |
| 受測 source SHA | `2deaf14` |

受測時 working tree 有修改，但**只有文件**：`environment.json` 記錄
` M AGENTS.md`、` M README.md` 與未追蹤的本票，對應的 patch 存為
`forgepilot-working-tree.patch`。沒有任何 `.go` 檔案被改動，
所以 fixture 建置出來的 `forgepilot` binary 就是 `2deaf14` 的程式碼。

### 實際發生的順序

```
Step 1: START WI-001
Step 2: RESUME WI-001 (attempt 1/2, new session)
Step 3: VERIFY WI-001      → EV-001 PASS at 0f47dbfc25a4, WI-001 VERIFIED
Step 4: START WI-002
Step 5: RESUME WI-002 (attempt 1/2, new session)
Step 6: VERIFY WI-002      → EV-002 PASS at c83937e09c50, WI-002 VERIFIED
Step 7: START WI-003
Step 8: RESUME WI-003 (attempt 1/2, new session)
Step 9: VERIFY WI-003      → EV-003 PASS at 0a9b74e154f1, WI-003 VERIFIED
Step 10: REVERIFY WI-001   → EV-004 PASS at 4642843548da, WI-001 VERIFIED
Step 11: REVERIFY WI-002   → EV-005 PASS at 398aeb379d09, WI-002 VERIFIED
Goal smoke is awaiting final review. Evidence: EV-004, EV-005, EV-003
Stopped: AWAITING_GOAL_REVIEW
```

Step 10 與 11 不是重試。三張工作依序完成之後 Candidate 已經演進，
WI-001 與 WI-002 原本的 PASS 綁在較早的 snapshot 上而變 stale，
freshness 規則要求它們在目前 Candidate 上重驗——這是既有契約，不是失敗。
最終三份 Evidence 綁在同一個 snapshot digest
`sha256:be62f2d99f1be09b5ed423648e2d24ca2001e733c8ce0be61e4e891813bcc683`。

### 驗收條件逐條對照

| # | 條件 | 結果 |
| --- | --- | --- |
| 1 | 指定測試確實執行，不是 SKIP | `--- PASS: TestCodexSmoke… (716.18s)`，非 `--- SKIP` |
| 2 | 實際使用 Codex runtime | `runtime_executable` 為本機 codex；三個 session 都有 `session.log` 的真實模型輸出 |
| 3 | 三張相依工作各自啟動新 session | `attempts = {WI-001:1, WI-002:1, WI-003:1}`，三個 `wi-00N-attempt-1/` 目錄各有 `handoff.md`／`result.json`／`session.log` |
| 4 | 每張工作皆有綁定 Snapshot Candidate 的正式 PASS Evidence | EV-004／EV-005／EV-003，`candidate_kind = SNAPSHOT` |
| 5 | 三張工作皆 VERIFIED，沒有 DONE | state 中三張皆 `VERIFIED`；測試明確拒絕任何 `DONE` |
| 6 | run record 停在 `AWAITING_GOAL_REVIEW`，退出碼符合契約 | `stop.reason = AWAITING_GOAL_REVIEW`，並與 `Stop.Reason.ExitCode()` 對照相等（0） |
| 7 | fixture 清理前以既有 typed query 重算 readiness | `app.GoalReadiness` 回 `GoalAwaitingFinalReview`，Evidence IDs 與 run record 的 `[EV-004 EV-005 EV-003]` 逐字相同 |
| 8 | 沒有 Review Evidence、沒有核准動作，Goal 仍 ACTIVE | state 的 evidence 全為 `verification`，goal `smoke` 為 `ACTIVE` |
| 9 | 沒有未解決 Pending，沒有仍被記錄為執行中的 Worker | `worker` 為 nil、`UnresolvedPending()` 為空、`gates` 為空 |
| 10 | 測試結束後 artifact 仍存在、可讀、manifest 可核對 | 62 個檔案、368 KB，`manifest.json` 每筆帶 SHA-256 |

第 7 條是這一輪唯一真正新加的檢查：在 fixture 被刪掉之前，
用產品自己的 typed query 重算一次，而不是在終端輸出裡找字串。

### attempts：Runner 結果與 smoke 期待分開報告

本輪兩者一致——三張工作各一次 attempt 就成功，
所以「Runner 契約」與「smoke 對模型的期待」都成立，沒有需要分開解釋的分歧。
分開報告的機制仍然留著（見上），因為下一輪未必如此。

### 證據留存

原始資料在本機非版控目錄 `~/.forgepilot-smoke-evidence/`：

```
codex-smoke-20260915T034856-001b6e/
  environment.json                  執行身分、版本、預算、模型（設定值／回報值分欄）
  forgepilot-working-tree.patch     受測時的文件改動
  runner-output.txt                 Runner 完整輸出
  inputs/                           三張 Story、AGENTS.md、Makefile、go.mod
  forgepilot/runs/<run>/            run.json、steps.jsonl、三個 attempt 的
                                    handoff.md／result-schema.json／result.json／session.log
  forgepilot/logs/                  五份 verification log
  forgepilot/state.json             最終 state
  fixture.bundle                    git bundle --all，含五個 snapshot ref
  fixture-refs.txt / fixture-log.txt
  fixture-worktree/                 最後的工作樹
  manifest.json                     62 筆，各帶 SHA-256 與 Run／Work／attempt 對應
  outer-capture.md
go-test.log / go-test-exit-code.txt  外層擷取（0）
```

Candidate 可還原這件事有實際核對過，不是設計意圖：
把 `fixture.bundle` clone 成 bare repo，
`git cat-file -t` 對五個 Evidence revision 全部回 `commit`。

那一輪的 `manifest.json` 由 `2deaf14` 產生，也就是上面那些修正之前的版本：
「刻意不匯出」（`locks/`、verification worktree、`.git`）當時被列在 `missing` 裡，
於是 `complete` 是 `false`。後續 commit 才把刻意排除拆成獨立的
`excluded_on_purpose` 欄位，讓 `complete` 只反映真正的缺漏。
**原始檔案沒有被改寫**——它是那一輪實際產生的東西，
用後來的程式重寫它就不再是原始證據。62 筆檔案本身逐一核對過，沒有缺漏。

### 一個環境觀察

fixture 工作樹裡出現了 `graft/`（`INDEX.md`、`.cache/`、`.graph/`）。
那不是 ForgePilot 也不是 Story 要求的，是本機 Codex 設定裡的索引工具
在 session 期間寫進 workspace 的。它被含進 snapshot Candidate，
不影響 canonical check 的結果，但它說明一件事：
**受測環境的 coding CLI 設定會成為 Candidate 內容的一部分。**
記在這裡，不改任何東西。

## 後續

[12](12-smoke-opt-in-closure.md) 修正了本票留下的一個啟用條件缺口：
`FORGEPILOT_CODEX_SMOKE` 當時寫成「非空即啟用」，`0` 與 `false` 因此會啟動真實模型。
本票以下的紀錄是本票那一輪實際發生的事，沒有被改寫；
修正後的入口尚未在真實模型下重跑。

## 已知限制與未驗收範圍

- 本輪只驗收**既有正常執行路徑**。07／08／09／10 的 recovery 情境
  仍然只有 fake runtime 的驗收，本輪沒有用真實模型重跑它們。
  不要把本輪讀成「所有 recovery 情境已通過真實模型驗收」。
- 一次 smoke 成功不是長時間無人值守的可靠度保證。樣本數是一。
- 這一輪的環境是單一機器、單一 Codex 設定、單一模型（`gpt-5.6-sol`）。
  換模型、換 reasoning effort 或換 sandbox 設定都沒有被驗證。
- 外層程序若遭 SIGKILL 或被強制 timeout，cleanup 不會執行，
  匯出目錄會缺資料。匯出 helper 不保證那種情況，也不宣稱。
- `TestSmokeEvidenceOutlivesTheFixtureItCameFrom` 在預設 suite 裡跑，
  因此每次 `make verify` 都會讀一次 `~/.codex/config.toml` 的白名單欄位
  並 spawn `codex --version`。兩者都不啟動模型，但它們確實會碰到本機設定。
- 匯出的 `forgepilot-working-tree.patch` 是當下 ForgePilot checkout 的
  完整 `git diff HEAD`。它不含被 gitignore 的檔案，但它的內容取決於
  執行當時的 working tree——把匯出目錄交給別人之前值得看一眼。
- `docs/development-plan.md` 記有 2026-09-14 一輪對 `codex-cli 0.154.0`
  的實跑（run `run-20260914t045121-d8bd10`）。那一輪沒有留下證據——
  這正是本票要補的缺口。依 evidence 規則，那筆紀錄是紀錄，不是現在的結果；
  本票的結論只依據 `run-20260915t034856-f0e621`。
