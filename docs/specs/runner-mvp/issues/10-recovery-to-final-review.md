# 10 — 複合故障恢復之後，同一個 run 走到 Goal 最終人工審查

[09](09-cleanup-survives-state-save-failure.md) 驗收的是**阻擋成立**：
facts refresh 回報未確認程序群組、同一次交易的 `state.json` 存檔失敗，
Runner 仍保存 unresolved Pending 並停在 `RECOVERY_BLOCKED`，各種重啟入口都被擋住。

09 的最後一步只問「阻擋有沒有解除」——`out, _ := fixture.runForge(...)`
丟掉退出碼，只斷言輸出不含 `RECOVERY_BLOCKED`。
**「cleanup 已解除」不等於「剩餘工作已成功完成」**，
而那條沒被驗到的路徑，正是無人值守跑完一輪之後，人真正會拿到手的東西。

本輪只補驗收，不改任何契約，因此沒有新的 ADR。

## 補上的驗收缺口

1. resume 的**退出碼**沒有被檢查。輸出不含某個字串，不證明退出碼是 0。
2. 沒有從磁碟讀回 `run.json` 確認 `Stop.Reason` 是 `AWAITING_GOAL_REVIEW`。
3. 沒有確認是**原 run** 走完，而不是另一個新 run 代勞。
4. 沒有確認第二張 Work Item 真的啟動自己的 session，也沒有檢查交接內容——
   「session 總數增加」對「同一張工作重試一次」同樣成立。
5. 沒有從持久化 state 驗證兩張工作的 VERIFIED、PASS Evidence 與 Candidate freshness。
6. 沒有用既有 Goal readiness 投影確認 `GoalAwaitingFinalReview`。
7. 沒有負向對照：解除阻擋之後若剩餘工作失敗，不得宣稱審查邊界。

## 正向：完整恢復路徑

`TestRunnerPreservesPendingWhenStateSaveFails` 保留 09 的雙故障注入與三個入口的阻擋驗收，
在確認群組消失之後改為驗完整路徑。實際跑出來的順序（本輪擷取自 resume 的輸出）：

```
Recovering run run-…: GIT is confirmed stopped
Step 4: START WI-002
Step 5: RESUME WI-002 (attempt 1/3, new session)
Step 6: VERIFY WI-002            → EV-001 PASS, WI-002 VERIFIED
Reclaiming an abandoned verification of WI-001
Step 7: RECLAIM WI-001           → EV-002 INTERRUPTED
        VERIFY WI-001            → EV-003 PASS, WI-001 VERIFIED
Goal queue is awaiting final review. Evidence: EV-003, EV-001
Stopped: AWAITING_GOAL_REVIEW
```

斷言的是：

- CLI 退出碼等於 `runner.ExitAwaitingReview`（0），不是「輸出裡沒有 RECOVERY_BLOCKED」。
- 從磁碟讀回的原 run record，`stop.reason` 為 `AWAITING_GOAL_REVIEW`，`run_id` 未變，
  沒有 unresolved Pending，`worker` 為 nil。
- WI-002 恰好啟動一個 session，其 handoff 檔含 `# ForgePilot work item WI-002`、
  `Work Item: WI-002` 與 `` `specs/stories/b.md` ``，且不含第一張工作的 Story。
  交接內容由 fake agent 落檔（`runner_fixture_test.go` 的 `handoff` helper），
  讀不到就 `t.Fatal`，不會以空字串矇混過去。
- 以 `runner.LoadRecord` 這個既有 typed loader 讀回比對（不靠 JSON key）：
  `Deadline` 與阻擋當下完全相同（resume 不延長也不重設）；
  `Steps` 嚴格大於阻擋當下且不超過 record 自己的 `Budget.MaxSteps`；
  `Attempts` 的 WI-001 不歸零、WI-002 至少 1。
  **不硬編碼總步數**——回收、reconcile 與重新驗證各自依法消耗步驟。
- 持久化 state：WI-001／WI-002 皆 `VERIFIED`，皆非 `DONE`，
  各自的 latest verification 為 PASS；沒有任何 `review` Evidence；
  沒有任何 verification `FAIL`（存檔失敗沒有被記成工程 FAIL）；
  被放棄的那次 WI-001 驗證恰好留下一筆 `INTERRUPTED`；goal `queue` 仍為 `ACTIVE`。
- `app.GoalReadiness` 回報 `GoalAwaitingFinalReview`，
  且 run record 的 `Stop.EvidenceIDs` 與投影的 `VerificationEvidenceIDs` 完全相同——
  這不是獨立的第二意見（Runner 本來就抄自同一個投影），它說的是**事後重算仍成立**：
  對當前 Candidate 已經過期的 Evidence 會從投影掉出來，卻不會從 record 掉出來；
  每一筆都能在 state 中找到、是 verification PASS、
  且正是所屬 Work Item 的 latest verification。
  **不要求 Evidence 總筆數**：回收留下的 `INTERRUPTED` 是合法歷史。
- 只檢查目標 Goal。fixture 裡的第二個 Goal 存在是為了驗跨 Goal 阻擋，從未預期它完成。

## 負向對照

`TestRunnerRecoveryDoesNotClaimFinalReviewAfterAgentFailure`
沿用同樣的雙故障與同樣的解除前提，只讓第二張工作的 fake agent
回報合法的 `execution_failed`（缺憑證，不是工程結果）。
分支寫在 fake agent 的 shell body 裡，用既有的 `$item`——
沒有新的產品旗標、環境變數或通用故障注入框架。

先斷言**前提成立**（阻擋確實解除、unresolved Pending 清空、WI-002 確實拿到 session），
否則後面每一條都會對一個根本沒走到失敗點的 run 成立。再斷言結論：

- `stop.reason` 為 `AGENT_EXECUTION_FAILED`，退出碼等於該契約的 `ExitCode()`（2）。
- 退出碼不為 0，輸出不含 `AWAITING_GOAL_REVIEW`。
- WI-002 既非 `VERIFIED` 也非 `DONE`，且沒有任何 Verification Evidence。
- 與正向路徑同一套人工審查邊界：沒有任何工作 `DONE`、沒有 review Evidence、goal 仍 `ACTIVE`。
- `app.GoalReadiness` 不為 `GoalAwaitingFinalReview`。

反向驗證（暫時把 agent 的失敗分支關掉）確認這條測試不是空過：
關掉之後它以 `stop = …reason:AWAITING_GOAL_REVIEW…, want AGENT_EXECUTION_FAILED` 失敗。

## 測試實作限制（本輪遵守的）

- 恢復入口一律真正的新 CLI subprocess；初次雙故障沿用既有 in-process 注入接縫。
- 兩條測試共用 `process` 與 `storage` 的全域注入接縫，皆不 `t.Parallel`。
- 只對測試自己建立、ownership 可確認的群組送 signal（`liveGroup`／`stopGroup`）。
- 沿用既有 `runnerOptions` 的預算（`MaxSteps` 100、`MaxDuration` 5 分鐘）。
  一度考慮放寬 wall-clock 預算，實測整條測試 2–4 秒，既有預算已有兩個數量級的餘裕，
  因此不加這個旋鈕：超過 `go test` 預設 package timeout 的預算等於讓它再也擋不住掛住的 run。
  預算若要改，只在「合法選擇預算的地方」改一次，事後不竄改任何紀錄。
- 沒有 `--force`，沒有關閉 hooks／filters，沒有手改 `state.json`／`run.json`。

## 產品程式

**未修改。**本輪補齊驗收證據，未發現需修正的產品缺陷——
強化後的斷言在未動產品程式的情況下即通過。
沒有為了製造 red → green 紀錄而改寫本來就正確的程式。

## 驗證

- baseline SHA：`9154db1839db39724955209499ac2ddb21260f72`（PR #22 合併後的 main）
- 分支：`test/runner-recovery-to-final-review`
- 環境：macOS 26.5.1、arm64（M4）、go1.25.5 darwin/arm64

| 指令 | 結果 |
| --- | --- |
| `make verify`（baseline，乾淨 worktree） | PASS（exit 0） |
| `go test -race -count=1 ./...`（baseline） | PASS（exit 0，root package 243.8s） |
| `make verify`（本輪） | PASS（exit 0） |
| `go test -race -count=1 ./...`（本輪） | PASS（exit 0，root package 262.8s） |
| `go test -race -count=3 -run '^(TestRunnerPreservesPendingWhenStateSaveFails\|TestRunnerRecoveryDoesNotClaimFinalReviewAfterAgentFailure)$' .` | PASS，3 次皆選中兩條測試並通過（15.7s） |

真實 Codex smoke（`FORGEPILOT_CODEX_SMOKE=1`）：**NOT RUN**——opt-in，本輪未取得額度授權。
fake runtime 的綠燈不是真實模型驗收。

## 不在範圍

daemon、scheduler、新 runtime、A2A、MCP、UI、rename 之後的 sync 失敗語意、
07 其餘未驗收的 crash window、獨立 `verify` 是否讀取 run record 的契約。
既有 exit code、lifecycle、review policy 與 recovery 契約一律未動。

## 未關閉的邊界

- 只驗了「第二張工作在 resume 後失敗」這一種剩餘工作失敗形狀；
  `needs_human`、`VERIFICATION_REFUSED`、預算耗盡在恢復之後的形狀未另外驗收。
- 第一張工作的 orphan 以 `INTERRUPTED` 結案再重新驗證，是既有流程的行為；
  本輪驗收它，未改變它。
- `reclaimOrphan` 仍會移除它回收的舊 worktree（ADR-0022 Consequences），本輪未觸及。
