# Worker 程序 ownership 採 fail-closed

Runner 會啟動長時間執行的 coding CLI 子程序。Runner 自己被 kill、當機或被 SIGINT 中斷時，那個子程序可能還活著，而且還在寫 workspace。恢復時必須回答一個問題：現在可不可以啟動新的 writer。

答案只有在**確定**舊 writer 已經不存在時才是「可以」。

判定材料有三份，缺一不可。其一，Runner 以 `Setpgid` 啟動子程序，因此整個所屬程序群組有一個已知的 pgid，停止時送給 `-pgid` 而不是只送給最外層程序——`make verify` 這類命令會再 fork，只停最外層會留下還在跑的孫程序。其二，run record 在程序啟動前後各保存一次：啟動前先扣掉 attempt 預算，啟動後再補齊 pid、pgid、argv[0] 與記錄當下的時間。其三，恢復時以 `ps` 讀回該 pid 的啟動時間與 command，與記錄比對。

四種結果對應四種行為。pid 不存在：舊 worker 已死。pid 存在且識別相符：那確實是我們的 worker，可以停止它自己的程序群組，然後繼續。pid 存在但識別不符：pid 已被重用，我們的 worker 已死，且**不得**停止那個程序。無法判定——`ps` 失敗、輸出無法解析、記錄缺少 pid 或 pgid：**recovery blocked**，拒絕啟動新 writer，也不殺任何東西。

「無法判定」包含一種不會報錯的情況。macOS 的 `ps` 讀不到某個程序的參數時不會失敗，而是改印括號包住的 accounting name（`(sh)`），退出碼照樣是 0。那一行的形狀和一條真正的 command 完全一樣，所以直接拿去比對就會把活著的 worker 判成別人的。因此括號形式一律當成「再問一次」：短暫重讀數次仍是括號，才回報無法判定；命令欄整個空白是同一個窗口的另一種寫法，同樣重讀。啟動時走同一條路徑，寧可記不到識別（記錄不完整本來就會走 recovery blocked），也不記下一個永遠對不回來的 command。

比對端也要擋一次：舊版本已經把 `(sh)` 寫進 run record，那筆識別永遠不可能和 ps 現在報回的 command 相符。拿它去比對只會得到「pid 被重用」這個錯誤結論，所以帶著括號識別的紀錄一律回報無法判定。代價是這種紀錄只要 pid 還活著就需要人確認——但**順序是先問作業系統再看紀錄**：pid 不存在是關於機器的事實，與紀錄裡寫什麼無關，那條路照樣回 Gone，舊版本留下的、多半早已結束的 worker 因此仍能自動收乾淨。

證據分級：括號渲染是這台機器上觀察到的；它就是那次 CI 失敗（macOS runner 上 `TestInspectDistinguishesGoneFromOursFromUnrelated` 把活著的 worker 判成 `UNRELATED`，本機與加壓皆無法重現）的成因，是推論。無論成因是否如此，拿一個讀不到的 argv 當 command 比對都是錯的。

前兩種「舊 worker 已死」的結論後來被收窄了一次，記在這裡以免後人讀成原本的樣子（[ADR-0022](0022-pending-cleanup-outlives-the-process.md)）：**leader 已死不等於群組已空。** coding CLI 與 `make verify` 分叉出來的子程序沿用 leader 的 pgid，leader 退出後它們照樣活著、照樣在寫這個 workspace。因此 pid 不存在與 pid 被重用這兩種情況，只有在 `kill(-pgid, 0)` 同時回報群組為空時才可以繼續；群組仍有成員時維持 recovery blocked。這兩種情況下也**不**送 signal：記錄的識別已經和持有那個 pid 的東西對不上，那個 pgid 底下的群組就不確定是不是我們的，殺它會是猜測。代價是承認一種無法自動解除的狀態——pid 重用且群組非空需要人來確認——但相對的是不猜測就不會殺錯程序，這與這份決定其餘部分的取捨一致。

只看 pid 不夠，因為 pid 會重用；只看 Runner 的 workspace lock 也不夠，因為那個 lock 隨 Runner 程序釋放，而子程序活得比 Runner 久。兩者都必須看。

這個判定是 best-effort 而不是保證。`ps` 的啟動時間解析度有限，極端情況下同一秒內的 pid 重用仍可能誤判；ForgePilot 也無法阻止使用者自己的編輯器或另一個 agent 寫同一個 workspace。所以承諾的不是 exactly-once，而是三件比較小的事：不主動製造重疊的 writer、恢復前核對事實而不是假設、以及不確定時拒絕而不是猜測。

**Consequences:** Runner artifacts 必須能承受「保存到一半當機」——保存是原子的，保存失敗即停止，不在沒有恢復紀錄的情況下啟動 worker。Agent 的外部副作用（已經寫進 workspace 的檔案）不保證 exactly-once：同一張工作可能被實作兩次，這由 attempt 預算與「恢復時重讀最新 domain state」限制，而不是由程序控制消除。Recovery blocked 需要人介入，而且該說出卡住的是哪個 pid。

**Falsified if:** `internal/agent/` 開始只用 pid 判定存活，或 `ps` 印出的括號形式被當成一條可以比對的 command，或開始在識別不符時送出 signal；或 kill 路徑改為只送給 pid 而非 process group；或 run record 改為只在程序啟動後保存一次。任一項發生，這份 fail-closed 的判定已經失效。
