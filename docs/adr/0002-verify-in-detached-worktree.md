# Verification 在隔離的 detached worktree 執行

Canonical verification 不在使用者的主工作樹執行，而是 `git worktree add --detach <SHA>` 到 `.forgepilot/worktrees/` 下的暫存目錄，在該處執行 `make verify`，結束後一律 `git worktree remove --force`。

即使已要求工作樹乾淨（見 architecture.md 的 dirty worktree policy），驗證開始後仍可能有人動檔案；原地驗證加上前後比對 HEAD 只能偵測到「被動過」，無法阻止，偵測到之後依然只能丟棄整次驗證。隔離是唯一真正兌現「Evidence 綁定確切受測 revision」的做法。實測本 repo 的額外成本約 3 秒（Go 的 build cache 跨 worktree 共用，test cache 因路徑改變而落空）。

**Consequences:** 這對受管理的專案強加了一條產品契約——**`make verify` 必須能在全新 checkout 上執行**。依賴 `.env`、本機已安裝的依賴或既有 build cache 的專案會失敗。這與 CI 本來就會強加的條件相同，因此視為合理限制而非缺陷。自動化上另有三個已實測的必要條件：必須用 `--detach` 加完整 SHA（用 branch name 會撞 `already used by worktree`）、移除時必須加 `--force`（worktree 內有未追蹤檔案時普通 remove 會失敗）、以及每次執行前先 `git worktree prune` 清掉被強制終止的程序留下的 prunable 殘骸。

**Falsified if:** 需要支援其 `make verify` 無法在全新 checkout 上執行的專案。屆時要重新設計的是 `internal/repository/` 的 verification adapter 與此處的隔離策略，而不是放寬 revision identity。
