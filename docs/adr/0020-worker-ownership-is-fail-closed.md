# Worker 程序 ownership 採 fail-closed

Runner 會啟動長時間執行的 coding CLI 子程序。Runner 自己被 kill、當機或被 SIGINT 中斷時，那個子程序可能還活著，而且還在寫 workspace。恢復時必須回答一個問題：現在可不可以啟動新的 writer。

答案只有在**確定**舊 writer 已經不存在時才是「可以」。

判定材料有三份，缺一不可。其一，Runner 以 `Setpgid` 啟動子程序，因此整個所屬程序群組有一個已知的 pgid，停止時送給 `-pgid` 而不是只送給最外層程序——`make verify` 這類命令會再 fork，只停最外層會留下還在跑的孫程序。其二，run record 在程序啟動前後各保存一次：啟動前先扣掉 attempt 預算，啟動後再補齊 pid、pgid、argv[0] 與記錄當下的時間。其三，恢復時以 `ps` 讀回該 pid 的啟動時間與 command，與記錄比對。

四種結果對應四種行為。pid 不存在：舊 worker 已死，可以繼續。pid 存在且識別相符：那確實是我們的 worker，可以停止它自己的程序群組，然後繼續。pid 存在但識別不符：pid 已被重用，我們的 worker 已死，可以繼續，且**不得**停止那個程序。無法判定——`ps` 失敗、輸出無法解析、記錄缺少 pid 或 pgid：**recovery blocked**，拒絕啟動新 writer，也不殺任何東西。

只看 pid 不夠，因為 pid 會重用；只看 Runner 的 workspace lock 也不夠，因為那個 lock 隨 Runner 程序釋放，而子程序活得比 Runner 久。兩者都必須看。

這個判定是 best-effort 而不是保證。`ps` 的啟動時間解析度有限，極端情況下同一秒內的 pid 重用仍可能誤判；ForgePilot 也無法阻止使用者自己的編輯器或另一個 agent 寫同一個 workspace。所以承諾的不是 exactly-once，而是三件比較小的事：不主動製造重疊的 writer、恢復前核對事實而不是假設、以及不確定時拒絕而不是猜測。

**Consequences:** Runner artifacts 必須能承受「保存到一半當機」——保存是原子的，保存失敗即停止，不在沒有恢復紀錄的情況下啟動 worker。Agent 的外部副作用（已經寫進 workspace 的檔案）不保證 exactly-once：同一張工作可能被實作兩次，這由 attempt 預算與「恢復時重讀最新 domain state」限制，而不是由程序控制消除。Recovery blocked 需要人介入，而且該說出卡住的是哪個 pid。

**Falsified if:** `internal/agent/` 開始只用 pid 判定存活，或開始在識別不符時送出 signal；或 kill 路徑改為只送給 pid 而非 process group；或 run record 改為只在程序啟動後保存一次。任一項發生，這份 fail-closed 的判定已經失效。
