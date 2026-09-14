# 未確認的清理是持久化事實，不是一次停止訊息

[ADR-0021](0021-execution-limits-are-bounded-and-named.md) 已經決定「送出 signal 不等於清理完成」，而且無法確認時要停在 `RECOVERY_BLOCKED`。它沒有說的是那個判斷活多久。目前它只活到這個 Runner 程序結束為止：`RECOVERY_BLOCKED` 寫進 `Stop.Reason`，而下一次啟動只檢查 `Worker != nil`，於是一個沒有 Worker 的未確認清理——canonical check 的程序群組、runtime preflight 的 probe、一個還卡在 filter 裡的 Git——在重啟、換 run ID 或換 Goal 之後就消失了。`Stop` 是「上一次為什麼結束」，`resume` 本來就會清掉它；把恢復阻擋寄存在同一個欄位，等於讓 resume 順手放行它。

這份決定把兩件事分開。

**恢復阻擋是 workspace 的性質，不是某一次 run 的結論。** `Record.Pending` 保存的是仍待確認的執行，每一筆在程序啟動之前就寫下，在確認清理完成之後才原子移除。`Start` 與 `Resume` 在同一個 workspace lock 內走同一個判準：掃過這個 workspace 所有 run 的未解決 `Pending`，任何一筆無法確認安全就拒絕啟動業務程序。換 Goal 不行，換 run ID 不行，重啟 CLI 也不行，因為判準讀的是 workspace 而不是命令列。`Stop` 照舊由 resume 清除，`Pending` 不由它清除。

**`Worker` 之外的執行也要有恢復紀錄。** 原本只有 agent session 被記下來，於是「Worker 路徑修好了」被誤讀成「程序恢復保護做完了」。Runner 啟動的 Git、runtime preflight、canonical check 與 agent session 走同一份 `Pending`，欄位一樣：所屬 run 與 workspace、執行種類與階段、相關 checkout 或 artifact 位置、觀察到的 PID／PGID 與 identity、是否尚待確認，以及原始停止原因與清理失敗原因兩者分開保存。自由文字只在 detail 裡，判定不讀它；`steps.jsonl` 仍然只是診斷。

分層由一個最小的 typed 邊界維持，不反向依賴：`internal/process` 定義「有一個程序群組沒被確認停止」這個值，`internal/app` 把它隨 `VerifyResult` 往上帶，`internal/runner` 才把它翻譯成 `Pending` 並持久化。`internal/app` 與 `internal/process` 都不知道 run record 長什麼樣子。

**取消不得吃掉清理失敗。** 一個被取消的 runtime resolution 或 `make -n verify` 仍然可能留下沒確認的程序群組。原本的順序先問 `ctx.Err()`，於是那個群組在「反正是被中斷」裡消失。順序反過來：先處理未確認的清理，再處理一般的中斷分類。理由和 ADR-0021 把停止原因寫定在觸發當下一樣——兩個同時成立的事實，不能因為其中一個比較容易解釋就丟掉另一個。

**清理預算是一個總量，不是每一層各領一份。** 每進入下一個 cleanup helper 就重新取得完整 `CleanupGrace`，會讓整條清理路徑變成沒有上限的遞迴寬限。因此一次被叫停的執行只發一份預算，沿著清理路徑傳遞並遞減；預算用完就是「無法確認」，而不是再等一輪。預算隨 context 傳遞而不是隨參數，因為這條路徑會經過 `internal/repository` 的 Git helper——它們沒有理由知道什麼是清理預算，只需要不要默默另開一份。一次被中斷的 verification 因此有兩個具名的量：停止 canonical check 本身，以及之後的善後 window；兩者各自是總量，不是每個 helper 一份。這也讓 `process.CleanupGrace` 第一次名符其實：在這之前 `StopLeader` 在 SIGKILL 之後無界等待 `finished`，那個常數描述的是一條實際上可以永遠不返回的路徑。

**Consequences.** Git 走受管理路徑有三個使用者看得到的代價，明講比讓人日後自己撞到好。其一，hook 或 filter 刻意留在背景的程序（watcher、cache warmer）會被一併收掉——ForgePilot 不留下自己啟動的程序，這是 [ADR-0020](0020-worker-ownership-is-fail-closed.md) 的既有立場，只是現在也適用於 Git 起的那些。其二，一個不理會 SIGTERM 的 hook 子程序會讓那一次 Git 呼叫多花一段 `TerminationGrace`；始終無法確認的話，一個 exit 0 的成功 Git 會回報「未確認」——那不是把成功說成失敗，是說「這次成功了，但有東西還在裡面跑」，而那正是下一步不能開始的理由。其三，只記到 PGID、沒有 identity 可比對的 pending，在那個 pgid 被別的程序重用之後會永遠 unresolved：這是刻意的 fail-closed 選擇，代價是需要人確認並手動清掉該筆條目，而且沒有 `--force` 可以跳過。

有一個已知邊界**沒有**被這份決定關掉：`reclaimOrphan` 仍會刪掉它回收的那個舊 worktree，不問裡面有沒有東西在跑。Runner 自己碰不到這條路——`settleWorkspace` 會先擋住——所以暴露面是「一個 Runner 留著未解決的 pending，而人另外手動跑 `forgepilot verify`」。要關掉它得讓獨立命令去讀 run record 並拒絕執行，那是改動 [ADR-0004](0004-verifying-liveness-via-flock.md) 明確保留的契約，不在這一輪的範圍內。

**Falsified if:** `internal/runner/` 的 `Start` 與 `Resume` 判斷恢復阻擋時讀的不再是同一份 workspace 掃描；或 `Pending` 的解除出現在確認清理完成以外的地方——刪 `run.json`、清 ownership、`--force` 旁路都算；或 `internal/app/` 在 `ctx.Err()` 成立時不再回報已觀察到的清理失敗；或 `internal/process/` 的任何停止路徑重新出現沒有上限的等待。任一項發生，恢復阻擋又回到只活一個程序的長度。
