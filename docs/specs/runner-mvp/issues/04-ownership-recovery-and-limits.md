# 04 — Ownership、crash recovery、預算、無進展與容量保護

## 交付

- `storage`：workspace lock 與 run artifact 的原子保存、容量上限。
- `runner`：預算（steps／attempts／duration／timeout）、無進展偵測、恢復流程。
- `agent`：pid／pgid／識別比對的 fail-closed ownership 判定。

## 驗收

- [x] 第二個 Runner 或 symlink 別名路徑被拒絕。
- [x] attempt 預算在程序啟動前先保存，crash 後不會無限重試。
- [x] `max-duration` 由第一次啟動計算，`resume` 不延長 deadline、不重置已消耗預算。
- [x] 以 `0` 取消限制被拒絕。
- [x] 五個 crash window 各有明確恢復結果；無法確認子程序身分時回報 recovery blocked 且不殺程序。
- [x] orphan verification 走既有 exclusive lock 與 reclaim，不直接改 VERIFYING。
- [x] 無進展以語意事實判斷；逐張合法 reverify 不被誤判為卡住。
- [x] 單次輸出、單 run 與總容量上限可注入；超限或寫入失敗時安全停止。
- [x] Runner artifacts 寫入不改變 Candidate digest。
