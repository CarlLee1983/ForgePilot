# M5-B：verification 輸出串流成 state 之外的 log

來源：GitHub issue #2 / #5 / #6 / #7，設計見 docs/adr/0012-verification-log-outside-state.md。

`verify` 把 canonical 檢查的輸出邊執行邊寫進 `.forgepilot/logs/<work-id>-<short-sha>-<started-at>.log`，
執行開始前印出路徑，非 PASS 不再把全文印進 stdout。schema 升 v5，`current_run` 的 `Run` 增 `LogPath`。
中斷留下截斷 log，孤兒回收從 state 記錄的路徑報出位置。Evidence 上不新增任何指向 log 的欄位。
