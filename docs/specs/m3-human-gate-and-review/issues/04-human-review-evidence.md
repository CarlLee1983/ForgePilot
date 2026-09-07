# 04: Human Review Evidence 與 reject

**What to build:** 人對通過驗證的工作記錄自己的判斷。認可就 `review approve`，不認可就 `review reject` 並附理由，後者把工作退回 RUNNING 讓 Agent 繼續修。審查結果綁定當下的確切 commit，與 Verification Evidence 放在同一個地方、同一條時間軸上，所以「這個 revision 上發生過什麼」能一次讀完。

此票的 approve 只記錄審查，還不會完成工作——完成的條件檢查與依賴解鎖是票 05 疊上來的，不會取代這裡的行為。

**Blocked by:** 01

**Status:** ready-for-agent

- [ ] `review approve` append 一筆 APPROVED Evidence，綁定 repository、Work Item、ForgeFlow Story、當下的完整 commit SHA、自述審查者與時間
- [ ] `review reject` append 一筆 REJECTED Evidence 並要求理由，缺少時被拒絕
- [ ] REJECTED 把 Work Item 退回 RUNNING
- [ ] 審查者身分預設取自 Git 設定，可由參數覆寫
- [ ] 只有 REVIEW 狀態的工作可被審查，其他狀態被拒絕
- [ ] 既有 Evidence 不被覆寫；review 與 verification 共用同一條 ID 序列
- [ ] 工作樹不乾淨時被拒絕——審查同樣必須指向一個 commit 能描述的內容
- [ ] `status` 顯示最新一筆 Human Review 的結果與其 revision
- [ ] `make verify` 通過
