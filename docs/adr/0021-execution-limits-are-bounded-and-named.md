# 執行期限是有界的，而且每一個都有名字

Runner 有三種可以打斷一段執行的東西：使用者送來的 SIGINT／SIGTERM、整個 run 的 `--max-duration`，以及單次執行的 `--agent-timeout`／`--verify-timeout`。它們全部經由同一個被取消的 context 送達，所以在被打斷的那一端看起來一模一樣——`context.Canceled` 對三者是同一個字。

事後從 context 判斷是哪一個，做不到，而且會錯得很安靜。所以**原因在觸發當下一次寫定**：執行控制在被取消的那一刻記下 `signal`／`run deadline`／`step timeout`，之後不再改寫。`AGENT_TIMEOUT` 與 `VERIFY_TIMEOUT` 只有設定這兩個旗標的那一層知道，因此也在那一層決定；`internal/agent` 與 `internal/app` 只回報「被叫停了」與 context 給的 cause，不猜是哪一條限制。

多個原因幾乎同時到達時，**先成立的那個就是終止原因**；完全同時則依固定優先序 signal → 總期限 → 單次 timeout。理由是誰在說話：一個人按了 Ctrl-C，不該被告知程式是因為逾時才停的；一個步驟與它所屬的 run 在同一瞬間到期，該記在 run 上，因為那才是真正卡住的限制。

**單次期限不是完整的 timeout，而是 `min(原始 run deadline, 本次開始時間 + 本次 timeout)`。** 每開始一個步驟就重新發一次完整 timeout，會讓總期限變成「每步一次」的建議值：一個在期限前一秒啟動的 Agent session 可以再跑滿 `--agent-timeout`，結束後還能接一次跑滿 `--verify-timeout` 的驗證。原始 deadline 從第一次啟動計算，`resume` 沿用它，不重置已消耗的步數與 attempts。

**期限到期不代表清理必須在零時間內完成。** 到期之後不再啟動任何新的業務工作，但終止本身是有界而不是瞬時的：先 SIGTERM，給一段有限的寬限讓 canonical check 把輸出寫進 log，必要時 SIGKILL，最後確認程序群組真的空了。這段寬限是執行期限之外的另一個量，文件與實作都把兩者分開講。

**送出 signal 不等於清理完成。** 清理要確認：無法確認時 Runner 停止並回報 `RECOVERY_BLOCKED`，不宣告可以安全前進，也不清掉恢復所需的 ownership 資訊。這與工程結果是兩個不同的判斷——已經成立並保存的 Evidence 照常保留，一個清理失敗不會被包裝成 FAIL，反過來一個 PASS 也不會讓 Runner 假裝群組是空的。

被打斷而沒有產生結果的 Verification Run 走既有 reclaim 流程保存 INTERRUPTED Evidence（[ADR-0004](0004-verifying-liveness-via-flock.md)、[ADR-0009](0009-reclaim-before-refusing.md)）。不製造工程 FAIL，也不留下可以正常結案卻沒結案的 VERIFYING；verdict 在 check 期間被改動時仍然 fail closed，不為了清理強行寫回狀態。

獨立的 `forgepilot verify` 不繼承這些期限。它沒有內建時間上限是 [ADR-0004](0004-verifying-liveness-via-flock.md) 的決定，與 Runner 共用同一段 orchestration 不是改變它的理由；它只是同樣會清理自己啟動的程序群組，並在無法確認時印警告而不改變 PASS／FAIL 的退出碼。

清理寬限本身也有名字與上界：`process.CleanupGrace` 是一次受管理執行在被叫停之後最壞會花的時間（leader 的寬限、程序群組的兩輪、輸出收集）。「期限到了」與「全部停下來了」是兩個時刻，把它具名比假裝它是零誠實。

**Consequences:** 停止原因與退出碼變成執行控制的輸出，而不是各呼叫點各自的猜測，因此新增一種「可以打斷執行的東西」時，必須同時給它一個 `stopCause` 與一條優先序，否則它會落進未分類而以退出碼 1 結束。保證的範圍限於受管理的程序群組——脫離群組的 daemon 不在內，這不是作業系統層級的隔離，[ADR-0020](0020-worker-ownership-is-fail-closed.md) 對 ownership 說的 best-effort 在這裡同樣成立。

**Falsified if:** `internal/runner/` 開始用 `context.Err()` 或 `context.DeadlineExceeded` 判斷停止原因；或任何一段會阻塞的執行以完整的單次 timeout 啟動而不與 `Deadline` 取小；或 `internal/process/` 的停止路徑不再確認程序群組已空就回報成功；或 `internal/repository/` 的 canonical check 在正常退出時不再處理自己的程序群組。
