# 02: forgepilot verify 的最小完整路徑

**What to build:** 使用者對一件 RUNNING 的 Work Item 執行 `forgepilot verify <work-id>`，ForgePilot 在隔離的環境裡對目前的 commit 執行受管理專案自己的 canonical 檢查，把結果保存成 Evidence，並依結果推進狀態——通過進入 REVIEW，失敗退回 RUNNING。使用者因此第一次能讓專案自己的檢查決定一件工作是否通過，而且那個判斷永久綁定在一個確切的 commit 上。

同一個指令的前置條件也在這張票裡：工作樹不乾淨、或受管理專案根本沒有 canonical 檢查時，指令被拒絕且不留下任何 Evidence——那些不是驗證失敗，是無法驗證。

隔離目錄放在 ForgePilot 自己的 state 目錄底下。這帶來一個必須確認的副作用：該目錄裡會出現一份受管理專案的完整 checkout，實作時要確認它不會被主工作樹自己的建置或測試遞迴掃到。

**Blocked by:** 01

**Status:** ready-for-agent

- [ ] `verify` 在乾淨工作樹上解析目前 HEAD，並在以 detach 加完整 SHA 建立的隔離 worktree 中執行受管理專案的 canonical 檢查
- [ ] 通過時 Work Item 進入 REVIEW，失敗時退回 RUNNING
- [ ] 每次執行 append 一筆 Evidence，含 repository、Work Item、ForgeFlow Story、完整 commit SHA、實際執行的 command、exit code、result、type、遞增 ID 與 timestamp
- [ ] 既有 Evidence 永不被覆寫
- [ ] 工作樹有已修改的 tracked 檔案、staged 變更或未追蹤檔案時拒絕執行且不留 Evidence；ignored 檔案不計入
- [ ] 受管理專案沒有 canonical 檢查的 target 時拒絕執行且不留 Evidence
- [ ] Goal 非 ACTIVE、Work Item 不在 RUNNING 或 REVIEW、repository 沒有 HEAD 時皆拒絕執行
- [ ] 隔離 worktree 在建立前先清除先前留下的殘骸註冊，結束後強制移除；通過與失敗皆移除
- [ ] 以測試證明隔離 worktree 看不到主工作樹的未提交內容
- [ ] Integration fixture 加入真實 commit 與 canonical 檢查；另備一個以非零 exit code 結束的版本以驗證失敗路徑
- [ ] 判斷邏輯留在 domain 純函式中，Git 與 subprocess 的結果以參數傳入，domain 不直接執行外部程式
- [ ] 隔離目錄不會被主工作樹的建置或測試遞迴掃到
- [ ] `make verify` 通過
