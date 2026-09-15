# Long-running Runner MVP — 本期結案紀錄

這份文件只做一件事：把 Runner MVP 這一期**已經交付的範圍**與**支撐它的證據**固定下來，
並如實揭露沒有驗收的部分。它不是新的 PRD、不是 ADR、不是 roadmap，
也不新增任何完成條件。

**「本期工程結案」不等於「受管理的 Goal 已被人類接受」。**
這份文件沒有修改任何 `.forgepilot` state、沒有製造 Review Evidence，
也沒有把任何實際工作標成 DONE。

| | |
| --- | --- |
| BASELINE_SHA（本次受檢查版本） | `b3a0a107e9ddc5ae8657c201a4510697ae0ccd21`（origin/main，PR #26 合併後） |
| 分支 | `docs/runner-mvp-closure`（獨立 worktree，未動使用者既有 checkout） |
| 平台 | macOS 26.x／arm64 本機檔案系統——repository 現有宣告的唯一支援平台 |
| 本次修改 | 只有這份文件與兩處入口連結；`internal/`、測試與 `.forgepilot` state 一行未動 |

受檢查版本固定於 `b3a0a10` 之後不再追新的 main commit。
它與開工指令給的參考 baseline 相同，因此沒有需要評估的新增差異。

---

## A. 結案範圍

### 本期完成、且有證據的部分

從使用者明確啟動 `forgepilot run` 開始，到 Runner 停在
`AWAITING_GOAL_REVIEW`（或停在需要人的條件、或撞到有界上限）為止：

1. 對單一 `GOAL` review-policy 的 Goal，依相依關係循序處理工作。
2. 每張工作與每次修復都啟動一個全新的 coding agent session。
3. 結果只由正式 snapshot verification 與 Verification Evidence 判定。
   Agent 自稱完成、agent exit code 0、verification 命令 exit code 0 都不算 PASS。
4. 預算、逾時、停止與恢復機制：`--max-steps`、`--max-attempts-per-work`、
   `--max-duration`、`--agent-timeout`、`--verify-timeout` 與三個容量上限，
   加上 ownership fail-closed、pending cleanup 跨程序阻擋、signal 與 crash recovery。
5. 條件滿足時停在 `AWAITING_GOAL_REVIEW`，退出碼 0——**那個 0 不表示 Goal 完成**。
6. Runner 不自行核准、不解除人工 Gate、不把 VERIFIED 當成 DONE、不自行完成 Goal。
7. smoke opt-in 收斂（只接受完全等於 `1`）與 smoke 測試檔案清理安全性修正保留在樹上，
   兩者都有回歸測試守著。

### 明確不在本期

- **Goal 最終人工接受與正式結案。** 沒有 goal-level approve／reject、
  沒有 Goal Evidence、沒有 Goal completion；`goal complete` 對 `GOAL` policy 仍然拒絕。
  自動執行與 machine verification 走到 `AWAITING_GOAL_REVIEW` 就是本期的終點線。
- 上面第 7 項修正之後的**真實模型重跑**（見 C／D）。
- Runner loop、recovery、ownership、budget 的重構；新 schema、migration、
  CLI flag、runtime adapter、相依；多 Agent、多 Goal、daemon、排程、UI。

---

## B. 證據對照表

「證據類型」三種：**CI**（GitHub Actions，本次已核對同 SHA）、
**本機**（本次在這台機器上實際執行）、**歷史**（過去某個版本的實跑紀錄）。

離線測試套件的每一列都同時由 CI 與本機兩筆執行涵蓋（見 C），
所以下表的「受測 SHA」對它們一律是 `b3a0a10`；
逐條驗收與測試名稱的完整對照在
[`docs/development-plan.md` 的 Runner MVP 驗收矩陣](../../development-plan.md#runner-mvp-驗收矩陣)，
這裡只列本期能力層級的對應，不重抄。

| # | 驗證的是什麼 | 受測 SHA | 類型 | 實際結果 | 可追查位置 |
| --- | --- | --- | --- | --- | --- |
| 1 | 相依工作循序完成、每張工作新 session、最後停在等待總檢而非 DONE | `b3a0a10` | CI + 本機 | PASS | `TestRunnerDrivesDependentWorkToTheGoalReviewBoundary`（`runner_integration_test.go`）；匯合依賴：`TestConvergingDependenciesPayTheirDeferredReverifications` |
| 2 | Agent 自稱完成／exit 0 不被當成完成 | `b3a0a10` | CI + 本機 | PASS | `TestAgentCleanExitWithoutAResultIsNotCompletion`（`runner_failure_test.go`）、`internal/agent` 的 `TestCleanExitWithoutAResultIsAProtocolError`、`TestDecodeResultAcceptsOnlyTheThreeOutcomes` |
| 3 | verification 命令正常退出但 Evidence FAIL 時走有界修復，不當成 PASS；認證／runtime／toolchain 問題不產生假 FAIL Evidence | `b3a0a10` | CI + 本機 | PASS | `TestVerificationFailureLeadsToBoundedRepairThenPass`、`TestAgentExecutionFailedStopsWithoutInventingAVerificationFailure`、`TestAnUnsatisfiableToolchainStopsWithoutFakeFailEvidence`（`runner_failure_test.go`） |
| 4 | 預算、步數、無進展、容量上限都有界停止；上限不可被旗標關閉，`resume` 不套用命令列預設 | `b3a0a10` | CI + 本機 | PASS | `TestMaxStepsStopsTheRun`、`TestMaxAttemptsPerWorkIsBounded`、`TestARunThatChangesNothingStopsForNoProgress`、`TestExceedingTheArtifactBudgetStopsSafely`；`internal/runner` 的 `TestArtifactLimitsRefuseAnyCancelledBound`、`TestResumeInheritsTheLimitsTheRunStartedWith` |
| 5 | 逾時與停止訊號：兩種期限、signal 優先序、程序群組清理有上限 | `b3a0a10` | CI + 本機 | PASS | `internal/runner` 的 `TestAStepIsBoundedByWhicheverLimitComesFirst`、`TestASignalOutranksEveryExpiry`、`TestTheRunDeadlineOutranksACoincidentStepTimeout`；`internal/agent` 的 `TestTimeoutStopsTheWholeProcessGroup` |
| 6 | 恢復：signal 後可 resume、確認不了 worker 就拒絕續跑、pending cleanup 跨 run／resume／重啟持續阻擋 | `b3a0a10` | CI + 本機 | PASS | `TestSignalStopsTheWorkerAndLeavesAResumableRun`、`TestResumeRefusesWhenAWorkerCannotBeConfirmed`、`TestResumeKeepsBudgetAndDoesNotReimplementVerifiedWork`（`runner_recovery_test.go`）、`TestAnUnconfirmedTidyUpKeepsTheEvidenceAndStopsTheRun`（`runner_tidyup_recovery_test.go`） |
| 7 | 人工 Gate 邊界：OPEN Gate、BLOCKED Goal、`needs_human` 一律停止，不自動解除；不接受 WORK_ITEM policy／空 Goal／未知 Goal | `b3a0a10` | CI + 本機 | PASS | `TestAnOpenGateStopsTheRunWithoutBeingResolved`、`TestABlockedGoalStopsTheRun`、`TestNeedsHumanStopsAndRecordsAGate`（`runner_scheduling_test.go`）、`TestRunRefusesGoalsItMayNotDrive` |
| 8 | 偽造的 VERIFIED 不被當成總檢依據；Runner artifacts 不改變 Candidate digest；退出碼分類不把未分類停止原因當成功 | `b3a0a10` | CI + 本機 | PASS | `TestASessionThatWritesForgePilotStateStopsTheRun`、`TestRunnerArtifactsDoNotChangeTheCandidateDigest`；`internal/runner` 的 `TestExitCodesSeparateReviewFromEveryOtherEnding` |
| 9 | smoke opt-in 只接受完全等於 `1`，且判斷在任何副作用之前 | `b3a0a10` | CI + 本機 | PASS | `TestSmokeOptInAcceptsOnlyTheExactValueOne`、`TestRealCodexSmokeConsultsTheOptInBeforeAnySideEffect`（`smoke_opt_in_test.go`）；[ticket 12](issues/12-smoke-opt-in-closure.md) |
| 10 | smoke 測試的清理安全性：`requireAbsent` 是唯讀斷言，不刪使用者資料，dangling symlink 也算存在 | `b3a0a10` | CI + 本機 | PASS | `require_absent_test.go` 五條 `TestRequireAbsent*`；[ticket 13](issues/13-smoke-test-cleanup-safety.md) |
| 11 | **真實 Codex 正常路徑**：三張相依工作、三次新 session、三份綁定 SNAPSHOT Candidate 的 PASS Evidence、停在 `AWAITING_GOAL_REVIEW` | `2deaf14`（**不是** `b3a0a10`） | 歷史（artifacts 本次實際核對） | PASS，退出碼 0，716.18s | run `run-20260915t034856-f0e621`；[ticket 11](issues/11-real-codex-smoke-acceptance.md)；artifacts 在本機非版控目錄 `~/.forgepilot-smoke-evidence/codex-smoke-20260915T034856-001b6e/` |
| 12 | 更早一輪真實 Codex smoke | 未記錄完整 SHA | 歷史（**僅文件紀錄**，無 artifacts） | 文件記為 PASS，339.28s | `docs/development-plan.md` 驗收矩陣「真實 Codex smoke」一列，run `run-20260914t045121-d8bd10`。那一輪沒有留下 fixture 之外的證據，**不作為本期完成判定的依據** |

### 第 11 列的 artifacts 本次核對到什麼程度

本次實際讀取了那個匯出目錄，不是只引用文件：

- `manifest.json` 62 筆檔案，逐筆重算 SHA-256，**62/62 相符、0 缺漏**。
- `manifest.json` 的 `complete` 是 `false`，`missing` 四筆分別是
  `fixture-worktree/.forgepilot`、`fixture-worktree/.git`、`forgepilot/locks`、
  `forgepilot/worktrees`——每筆都帶「刻意不匯出」的理由。
  這與 [ticket 11](issues/11-real-codex-smoke-acceptance.md) 的說明一致：
  `excluded_on_purpose` 欄位是那一輪之後才拆出來的，原始檔案沒有被改寫。
- `environment.json`：`forgepilot_commit = 2deaf1404db8e10bb218c96f39a022697654b5dc`、
  macOS 26.5.1／arm64、Go 1.25.5、git 2.55.0、codex-cli 0.154.0，
  設定要求與 session log 回報的模型都是 `gpt-5.6-sol`。
- run record 的 `stop.reason = AWAITING_GOAL_REVIEW`，
  `evidence_ids = [EV-004, EV-005, EV-003]`，`worker` 為 null。
- 最終 `state.json`：Goal `smoke` 仍 `ACTIVE`／policy `GOAL`，
  WI-001／002／003 全部 `VERIFIED`、**沒有 DONE**，沒有 Gate。
- `fixture.bundle` clone 成 bare repo 後，五筆 Evidence revision
  （`0f47dbfc`、`c83937e0`、`0a9b74e1`、`46428435`、`398aeb37`）
  以 `git cat-file -t` 全部回 `commit`——被驗證的 Candidate 確實可從匯出還原。

歷史檔案一個字都沒有改，也沒有把它冒充成 `b3a0a10` 的結果。

---

## C. 本次驗證結果

### C-1 本次本機執行

| | |
| --- | --- |
| 平台 | Darwin 25.5.0（macOS 26.x）、arm64、MacBook Air M4 |
| Go | go1.25.5 darwin/arm64 |
| 受測 SHA | `b3a0a107e9ddc5ae8657c201a4510697ae0ccd21`（worktree HEAD，乾淨，文件尚未提交前執行） |
| 執行時間 | 2026-09-15 21:40 CST 起 |

三個 smoke 相關環境變數一律明確移除，避免繼承呼叫者設定：

```
env -u FORGEPILOT_CODEX_SMOKE -u FORGEPILOT_SMOKE_ARTIFACT_DIR -u FORGEPILOT_FAKE_AGENT \
    make verify                                → exit 0（root 套件 314.9s）
env -u FORGEPILOT_CODEX_SMOKE -u FORGEPILOT_SMOKE_ARTIFACT_DIR -u FORGEPILOT_FAKE_AGENT \
    go test -race -count=1 ./...               → exit 0（root 套件 283.8s）
git diff --check                               → exit 0
```

三者全部通過。`go test ./...` 與 `go test -race -count=1 ./...` 的八個套件逐一 `ok`，
沒有 FAIL、沒有 SKIP 之外的異常；`make verify` 的 gofmt 檢查、`go vet` 與 CLI build 也都過。
`git diff --check` 在這份文件與兩處入口連結加上之後才跑，退出碼 0，沒有空白或衝突標記問題。

本機平台就是 repository 宣稱支援的 macOS arm64，**沒有任何一項標為 NOT RUN**（除了 C-3）。

### C-2 GitHub CI（同一個 BASELINE_SHA）

| | |
| --- | --- |
| Run | [34950965871](https://github.com/CarlLee1983/ForgePilot/actions/runs/34950965871) |
| Commit | `b3a0a107e9ddc5ae8657c201a4510697ae0ccd21` — 與 BASELINE_SHA 逐字相同 |
| Runner | `macos-latest`（workflow 明文說明：macOS 是唯一宣稱支援的平台） |
| 步驟 | `make verify` 與 `go test -race -count=1 ./...` |
| 結果 | **success**，9m2s，2026-09-15T09:09:35Z |

CI 依 repository 既有流程執行，本次沒有修改或繞過它。
沒有引用任何其他 SHA 的 CI 成功紀錄。

### C-3 真實 Codex smoke

**NOT RUN — no explicit quota authorization for this task.**

本次開工指令明確不授權透過測試啟動真實 Codex 或消耗模型額度。
沒有本期的 TESTED_SHA、沒有新的 Run ID、沒有新的證據目錄。
離線套件中的 Codex spy 正向對照（`TestRealCodexSmokeConsultsTheOptInBeforeAnySideEffect`
的第六案）照常執行，它到的是 PATH 上的 spy，不是模型。

### C-4 受測版本與文件提交版本的差異

上面兩筆驗證跑在 `b3a0a10` 這棵樹上，這份文件是在那之後才提交的。
文件本身的 commit SHA 不回填進文件內容——那需要反覆提交，
而且文件變動不改變任何受測行為。
交付報告會一併給出文件提交 SHA。
驗證之後若只補填結果、時間或措辭，不重跑整套測試。

---

## D. 已知限制

只列與使用判斷有關的：

- **支援平台以 repository 現有宣告為準**：macOS 本機檔案系統。程序鎖是 OS flock，
  其他平台沒有被驗證過，本次也沒有為了結案而新增跨平台支援。
- **最新修正版（`b3a0a10`）本次沒有重跑真實 Codex smoke。**
  唯一一輪留有完整證據的真實執行在 `2deaf14`，也就是 ticket 12／13 的修正之前。
  修正後的 opt-in 入口與清理安全性修正**尚未在真實模型下走過一次完整路徑**。
- **歷史 smoke 成功不代表 recovery 情境已被真實模型驗收。**
  07／08／09／10 的 recovery 情境目前只有 `--runtime fake` 的驗收。
  fake adapter 是產品程式碼、遵守同一份 session 契約，它證明 ForgePilot 的程序、
  鎖與恢復處理正確，不證明無人值守跑真實模型會走到同一個結果。
- **樣本數是一。** 單一機器、單一 Codex 設定、單一模型（`gpt-5.6-sol`）、
  單一 reasoning effort、單一 sandbox 設定。換任何一項都沒有被驗證，
  長時間無人值守的可靠度也沒有。
- **GOAL policy 的最終人工接受與正式完成不在本期。**
  Runner 走到 `AWAITING_GOAL_REVIEW` 就停，退出碼 0 只表示「已達到等待總檢的條件」。
- 受測環境的 coding CLI 設定會成為 Candidate 內容的一部分——
  `2deaf14` 那一輪的 fixture 工作樹裡出現了本機 Codex 索引工具寫入的 `graft/`。
  它不影響 canonical check 結果，但它說明 Candidate 不只包含 Story 要求的東西。
- `make verify` 因兩個 cleanup-window 測試與 smoke 入口回歸的子程序而變慢。
  這是刻意的成本，不是缺陷。

以下不影響本期完成判定，僅記錄：測試速度、helper 抽象程度、命名風格、
額外平台與 runtime、額外壓測、文件美化。不拆票、不列為本期完成條件。

---

## E. 結案判定

**Runner MVP 已達本期交付範圍，可結案；已知限制如上。**

沒有發現阻擋問題：沒有可重現的資料破壞、沒有未授權的模型呼叫、
Runner 沒有越過人工核准邊界、沒有無效或過期 Evidence 被接受而錯誤推進、
既定主要流程可以完成、必要驗證通過。

**下一期只保留一項範圍說明：Goal 最終人工總檢與正式結案。**
本次不為它寫 spec、不拆票、不開始實作。
