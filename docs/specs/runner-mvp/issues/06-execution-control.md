# 06 — 停止訊號、程序清理與總期限的執行控制

Runner MVP 交付後的 dogfood 修正。不重新設計 Runner，不加新能力：補上三個「執行控制」的漏洞，
讓 Runner 在 **Agent session 與正式 verification 兩段執行期間**都能正確處理停止訊號、
子程序生命週期、單次 timeout 與整個 run 的總期限。

責任分工不變（[ADR-0019](../../../adr/0019-runner-executes-forgepilot-decides.md)）：
Runner 只執行，`internal/app` 判定合法轉移，受管理專案的 canonical check 負責工程驗證，
Goal 最終總檢仍歸人。ownership 仍 fail-closed（[ADR-0020](../../../adr/0020-worker-ownership-is-fail-closed.md)）。

## 三個問題

**A — 正式 verification 收不到停止訊號。** CLI 收到 SIGINT／SIGTERM 後關閉 `Stop` channel，
但 `runner.verify` 以 `context.Background()` 呼叫 `app.Verify`，整段 verification（含 preflight
的 `make -n verify` 與 runtime 版本探測）因此不在任何取消路徑上。Ctrl-C 之後 `make verify` 繼續跑到完。

**B — verification 正常退出不清理程序群組。** `RunCanonicalCheckInContext` 只有在 context 結束時
才停止程序群組；主程序自己結束時直接回傳。`make verify` 起的背景子程序因此活著進入下一個步驟，
繼續寫這個 workspace。`internal/agent` 的 session 路徑早就做了這件事，verification 路徑沒有。

**C — `--max-duration` 不約束執行中的步驟。** 總 deadline 只在主迴圈開頭檢查。Agent 用滿整個
`--agent-timeout`，結束後還可能再啟動一次用滿整個 `--verify-timeout` 的驗證，整個 run 因此可以
越過原始總期限近兩倍。

## 交付

- `internal/repository`：受管理程序群組的共用執行 helper（`Setpgid` 啟動、有界終止、確認清理結果、
  自有輸出管線），canonical check 與 preflight 共用；`RunCanonicalCheckInContext` 改回傳
  「完成與否」與「清理是否確認」，`ResolveRuntimeInContext` 與 `EnsureCanonicalCheckInContext`
  讓前置程序也受取消與期限約束。
- `internal/app`：`Verify` 全程使用呼叫者的 context；新增 `ErrVerificationInterrupted`
  區分「被外部取消」與既有的 `ErrVerificationTimedOut`；兩者都走既有 reclaim 流程保存
  INTERRUPTED Evidence；`VerifyResult.Cleanup` 回報未能確認的程序清理。
- `internal/agent`：`Session.Wait` 改收 context，終止與清理結果可被確認並回報。
- `internal/runner`：`execution` 執行控制型別——單次期限為
  `min(原始 run deadline, 本次開始時間 + 本次 timeout)`，記錄明確的終止原因而非事後猜 context。
- 文件：architecture 的 Runner 執行保護一節、development-plan 的驗收矩陣。

## 不在範圍

新 coding runtime、daemon／scheduler／Web UI、多 Goal、A2A／MCP、自動 commit／push／merge、
第二套 lifecycle、state schema migration、與這三個問題無關的重構。

## 停止原因判定規則

多個原因幾乎同時到達時，**先成立的那個就是這次執行的終止原因**；完全同時則依固定優先序
signal → 總期限 → 單次 timeout。原因在觸發當下一次寫定，不由事後檢查 `context.Err()` 推論。

| 情況 | 停止原因 | 退出碼 |
|---|---|---|
| 整個 run 的 deadline 到期 | `MAX_DURATION` | 3 |
| Agent session 自身 timeout 先到 | `AGENT_TIMEOUT` | 3 |
| verification 自身 timeout 先到 | `VERIFY_TIMEOUT` | 3 |
| SIGINT | `INTERRUPTED` | 130 |
| SIGTERM | `TERMINATED` | 143 |
| 清理無法確認完成 | `RECOVERY_BLOCKED` | 2 |

執行期限與清理寬限是兩件事：期限到期後不再啟動任何新的業務工作，但允許有界的終止寬限
（先 SIGTERM、有界等待、必要時 SIGKILL、再確認）與 INTERRUPTED Evidence 的保存時間。

## 驗收

### A — 停止訊號

- [x] 正式 verification 執行中收到 SIGINT：程序群組停止、退出碼 130、留下 INTERRUPTED 恢復資訊。
- [x] 正式 verification 執行中收到 SIGTERM：程序群組停止、退出碼 143。
- [x] signal 到達後不啟動下一張工作的 session 或 verification。
- [x] 已完成並保存的合法 Evidence，不被稍後到達的 signal 覆寫。
- [x] 原有 Agent session 中斷行為（130／143、可 resume、程序群組全滅）不退化。
- [x] verdict 在 check 期間被改動時仍 fail closed，不為了清理強行寫回狀態。

### B — 程序清理

- [x] `make verify` 啟動背景程序後 exit 0，背景程序不會存活到下一個步驟。
- [x] `make verify` 啟動背景程序後非零退出，仍完成清理且 FAIL Evidence 照常保存。
- [x] 背景子程序繼承輸出描述元時，收集輸出與等待都不會無界阻塞。
- [x] 子程序不回應 SIGTERM 時，在有界時間內被 SIGKILL 處理掉。
- [x] 無法確認清理完成時停止並回報，不宣告可以安全前進，也不清掉恢復所需的 ownership 資訊。
- [x] 清理錯誤不被包裝成工程 FAIL；已成立的 Evidence 照常保留。

### C — 總期限

- [x] Agent 執行中總期限到達，停止原因為 `MAX_DURATION`。
- [x] verification 執行中總期限到達，停止原因為 `MAX_DURATION`。
- [x] Agent 完成時總期限已到，不再啟動 verification。
- [x] 前置檢查（runtime 解析、`make -n verify`）遇到停止或到期，不繼續啟動正式檢查。
- [x] `--agent-timeout` 先到仍為 `AGENT_TIMEOUT`，`--verify-timeout` 先到仍為 `VERIFY_TIMEOUT`。
- [x] `resume` 不重置 deadline、steps 或 attempts；已到期的 run 不因 resume 啟動新 session。

### D — 既有行為

- [x] 三張相依工作仍循序執行，每次 implementation／repair 仍是新 session。
- [x] 正常 PASS、正常工程 FAIL 與有限 repair 不退化，最終仍停在 `AWAITING_GOAL_REVIEW`。
- [x] 獨立 `forgepilot verify` 契約不變：不繼承 Runner 的總期限，PASS／FAIL 輸出與退出行為照舊。
- [x] `next`、`review`、Gate 與 snapshot 行為不退化。
