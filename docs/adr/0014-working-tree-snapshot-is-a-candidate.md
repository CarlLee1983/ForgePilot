# Working tree snapshot 是 Candidate，不是暫存 workflow state

`verify --snapshot` 以 private Git index 把當下 working tree 固定成一個本機 snapshot commit，並保留在 `refs/forgepilot/snapshots/`；Verification Run 與後續 Evidence 保存同一份 Candidate identity。Candidate 只存在於 run 與 Evidence，不放在 Work Item 上，因為放在 Work Item 會重建 ADR-0003 已拒絕的 mutable target；用 branch、tag、stash 或目前 index 則會讓驗證協定污染開發者的 workspace。

Candidate digest 是 ForgePilot 自動計算的 `sha256:` 值，綁定 base revision 與 canonical Git tree entries，因此包含相對路徑、mode/type、內容，以及相對 base 的新增／刪除意義，不依賴 mtime、絕對路徑或檔案列舉順序。`status` 與 snapshot review 以隔離的 temporary object database 重算 digest，不改 state、real index 或 repository object store；review 只有在 digest 相同時才複用已驗證的 snapshot revision。snapshot ref 在 state 開始引用它之前建立，之後不因 PASS、FAIL 或拒絕而刪除；完整 retention／GC policy 留給後續工作。

**Falsified if:** `internal/work` 把 Candidate 存到 Work Item 作為可變 target、`internal/repository` 的 snapshot capture 修改 real index／HEAD／branch／working files，或 `internal/cli/review.go` 對 snapshot Evidence 改以 HEAD 而非已驗證 digest 選擇 review revision。
