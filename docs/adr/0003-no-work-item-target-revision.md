# Work Item 不保存 target_revision

`docs/architecture.md` 原本的 Domain data 表把 Work Item 的 `target_revision` 列為 M2 啟用欄位。M2 定案時刪除它：revision 只存在於 Evidence（已完成的事實）與 `current_run`（進行中的執行）之中，Work Item 本身不保存任何 revision。

Verification 的標的永遠是執行當下的 HEAD，而 Evidence 自帶完整 SHA。若 `target_revision` 在驗證時寫入，它就是最新一筆 Evidence 的第二份副本，遲早不一致；若在 `start` 時寫入，它有獨立語意（工作從哪個 base revision 開始）但 M2 沒有任何規則會讀它。

此條特別記錄，是因為架構文件曾經列出這個欄位，日後讀者很可能把它的缺席當成疏漏而「補回來」。缺席是決定，不是遺漏。

**Falsified if:** 出現真正需要「這件工作針對哪個 base revision」的規則——例如 M3 的 reopen policy 需要區分 base 與受驗 revision。屆時新增的欄位語意必須是 base revision，不得是最新受驗 revision，否則會與 `internal/work/work.go` 中的 Evidence 重複。
