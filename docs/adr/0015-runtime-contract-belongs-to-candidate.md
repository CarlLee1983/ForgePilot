# Runtime Contract 屬於 Candidate，Resolved Runtime 屬於 Verification Run

ForgePilot 只在 immutable Candidate 的 detached checkout 解析 runtime declarations，並在 canonical precheck 之前解析本機已安裝且符合全部 contract constraints 的 executables。每個 runtime command 先在 private temporary bin directory 綁到已驗證的 executable，再組成該次 Verification subprocess 的 environment，避免不同 manager bin directories 互相遮蔽；目錄隨命令清理。不 source shell profile、不修改 repository declaration、不切換 global manager state，也不安裝 runtime。沒有支援的 declaration 才完整沿用 caller environment。

這個位置讓 COMMIT 與 SNAPSHOT 共用同一 resolver，也讓 `EnsureCanonicalCheck` 與真正的 `make verify` 使用完全相同的 environment。若 declaration 衝突或版本不可用，命令在 `current_run`、log 與結果 Evidence 建立前拒絕，因此環境問題不會被誤記為 Verification FAIL。

Resolved Runtime 的 actual version map 先隨 `current_run` 固定，再由 PASS、FAIL 或 INTERRUPTED Evidence 複製；它不在結果完成時重讀 live PATH。Schema v7 將 `runtime` 設為選填，因為 v6 與更舊的 Evidence 無法可靠回填當時環境，而缺少 metadata 不會使真實的舊結果變成非法。

**Falsified if:** `internal/cli/verify.go` 在 main worktree 解析 declaration、canonical precheck 與 runner 使用不同 environment，`internal/repository/runtime.go` 安裝或全域啟用 runtime，或 `internal/work/evidence.go` 在結果完成時從 live shell 重建 runtime metadata。
