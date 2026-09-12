# Decision records

不易反轉的決定與其失效條件。每一份都以 `**Falsified if:**` 收尾：那一段用 backtick 標出的檔案，就是這個決定所依賴的邊界。

閱讀順序沒有規定，但 0001 與 0003 定下了「事實存在哪裡」的形狀，後面幾份多半引用它們。

| ADR | 決定 | 狀態 |
|---|---|---|
| [0001](0001-evidence-in-state-snapshot.md) | Evidence 存在 state snapshot 內，與 Work Item 共用同一次受鎖的原子替換 | accepted |
| [0002](0002-verify-in-detached-worktree.md) | Verification 在 detached worktree 執行，canonical 檢查的存在性也在該處判斷 | accepted |
| [0003](0003-no-work-item-target-revision.md) | Work Item 不保存 `target_revision`；revision 只存在於 Evidence 與 `current_run` | accepted |
| [0004](0004-verifying-liveness-via-flock.md) | Verification Run 的存活以 flock 標記，不設逾時上限 | accepted |
| [0005](0005-self-asserted-decision-maker.md) | 決策者身分是自述而非認證——半套的認證比不做更危險 | accepted |
| [0006](0006-done-is-terminal.md) | DONE 是終態，沒有 reopen；要重做就新增一件 Work Item | accepted |
| [0007](0007-blocking-is-not-a-status.md) | 阻擋由 Gate 表達，不佔用狀態欄 | accepted |
| [0008](0008-approval-completes-work.md) | 沒有完成指令；`review approve` 在條件滿足時於同一交易內完成工作 | accepted |
| [0009](0009-reclaim-before-refusing.md) | `verify` 先無條件回收孤兒，再判斷能不能開始新的執行 | accepted |
| [0010](0010-no-outbound-network-requests.md) | 不主動發出網路請求——不自行 HTTP，也不 spawn `gh` | accepted |
| [0011](0011-pr-identity-does-not-gate-completion.md) | PR identity 不參與完成判定與 stale 判定 | accepted |
| [0012](0012-verification-log-outside-state.md) | Verification 輸出串流到 state 之外的 log，以 run 為鍵；Evidence 上不加欄位 | accepted |
| [0013](0013-forgepilot-self-adoption-of-forgeflow.md) | ForgePilot 是否自我套用 ForgeFlowV2——尚未回答的開放問題 | proposed |
| [0014](0014-working-tree-snapshot-is-a-candidate.md) | Working tree snapshot 是保留在 ForgePilot ref 下的 Candidate，不是 branch、stash 或 Work Item target | accepted |
| [0015](0015-runtime-contract-belongs-to-candidate.md) | Runtime Contract 從 Candidate checkout 解析；只固定該次 subprocess environment，actual versions 隨 run 進 Evidence | accepted |

## 什麼時候該加一份

三個條件同時成立才值得：難以反轉、後人不知道脈絡會覺得奇怪、而且是真實取捨的結果。例外是刻意的偏離——一個後人很可能會「修回去」的決定，即使反轉很便宜也該記，因為便宜的反轉正是要防的那件事（0003 與 0011 都屬於這一類）。

這個目錄不是只表達「做完了」的地方：proposed 的開放問題就住在這裡，不會被擠進只表達「做完了」的 roadmap 欄位。
