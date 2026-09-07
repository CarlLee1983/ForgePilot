# 01: Schema v3 與 v2→v3 升級

**What to build:** 使用者拿著 M2 留下的 state 執行任何指令時被明確拒絕並被告知執行 `migrate`；升級之後既有的 Goal、Work Item、依賴關係與累積的 Evidence 一件不少，先前的內容已備份，重複執行安全。

v3 在此建立 Gate 的容器與其 ID 配發計數，以及 Evidence 上 review 專用的可空欄位。此時還沒有任何東西寫入它們——票 02 與 04 才是它們的第一個寫入者。

**Blocked by:** None (can start immediately)

**Status:** done

- [x] M2 的 state 被拒讀，錯誤訊息指示執行 `migrate`，且不修改既有資料
- [x] `migrate` 升級後 Goal、Work Item、依賴、既有 Evidence 與各 ID 配發計數完整保留
- [x] `migrate` 升級前備份，備份檔已存在時拒絕執行且不修改 state；檔名沿用以來源版本命名的既有規則
- [x] `migrate` 對已是最新版本的 state 回報並以 exit 0 結束
- [x] v3 包含 Gate 容器、Gate ID 配發計數與 Evidence 的 review 專用可空欄位，初始為空
- [x] 不提供 downgrade；較舊的 binary 仍拒讀 v3
- [x] Work Item 狀態集合縮為 PENDING、READY、RUNNING、VERIFYING、REVIEW、DONE；載入含已移除狀態的 state 被拒
- [x] `make verify` 通過
