# 01: Schema v4 與 v3→v4 升級

**What to build:** 使用者在既有的 v3 state 上執行 `forgepilot migrate`，state 被備份後升到 v4，佇列、Evidence 與 Gate 一筆不少。升級後 Evidence 多了一個選填的 PR Reference 欄位——這張票還沒有任何指令會寫入它，但 state 從此能容納它、能驗證它，也能正確拒讀不是自己版本的 state。

即使這一步不改動任何既有資料，升級儀式照走：備份、備份已存在時拒絕、對已是最新版本者回報並成功結束。使用者面對的契約是「升級就是備份加改版號」，不是「有時候會備份」。

**Blocked by:** None (can start immediately)

**Status:** done

- [x] `schema_version` 升至 4；Evidence 新增選填的 PR Reference 欄位，無其他欄位變更或語意改變
- [x] v3 state 被新版 binary 拒讀，錯誤訊息指示執行 `migrate`；v4 state 被舊版拒讀
- [x] `migrate` 把 v3 state 備份為 `state.json.v3.bak` 後升級，佇列、Evidence 與 Gate 逐筆完整
- [x] 備份檔已存在時 `migrate` 拒絕執行而非覆寫
- [x] 對已是 v4 的 state，`migrate` 回報「已是最新版本」並以 exit 0 結束，重複執行安全
- [x] `migrate` 逐版套用，從 v2 出發者執行一次即可走到 v4
- [x] 兩處版本號 fixture 調到 v4：`internal/storage` 中驗證「較新 schema 應被拒讀」者，與 `internal/work` 中區分較舊／較新 schema 錯誤者
- [x] 整合測試建立舊版快照的方式維持操作解碼後的文件，不用字串替換
- [x] `make verify` 通過
