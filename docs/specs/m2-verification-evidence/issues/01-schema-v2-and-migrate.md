# 01: Schema v2 與 forgepilot migrate

**What to build:** 使用者拿著 M1 留下的 state 執行任何指令時，會被明確拒絕並被告知該執行 `forgepilot migrate`，而不是看到一個像損毀的錯誤。執行 `migrate` 之後 state 升級為 v2，既有的 Goal、Work Item 與依賴關係完整保留，而且先前的內容已經被備份下來。重複執行 `migrate` 是安全的。

v2 在此建立 Evidence 的容器與 Work Item 的 Verification Run 欄位，此時還沒有任何東西會寫入它們——它們在票 02 與 03 才開始有內容。

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] M2 binary 讀到 v1 state 時拒絕執行，錯誤訊息指出應執行 `migrate`，且不修改既有資料
- [ ] `migrate` 將 v1 升級為 v2，Goal、Work Item、依賴與 ID 配發計數完整保留
- [ ] `migrate` 升級前建立備份；備份檔已存在時拒絕執行且不修改 state
- [ ] `migrate` 對已是 v2 的 state 回報已是最新版本，以 exit 0 結束
- [ ] v2 包含 Evidence 容器、Evidence ID 配發計數與 Work Item 的 Verification Run 欄位，初始為空
- [ ] 不提供 downgrade；較舊的 binary 仍拒讀 v2
- [ ] 既有那個以 `schema_version` 為 2 作為「較新 schema 應被拒讀」的測試 fixture 改用比 v2 更新的版本號，否則該測試會在無人察覺下改為驗證一個合法的 state
- [ ] `make verify` 通過
