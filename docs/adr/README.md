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
| [0010](0010-no-outbound-network-requests.md) | 不主動發出網路請求——不自行 HTTP，也不 spawn `gh` | accepted（由 0018 加註例外） |
| [0011](0011-pr-identity-does-not-gate-completion.md) | PR identity 不參與完成判定與 stale 判定 | accepted |
| [0012](0012-verification-log-outside-state.md) | Verification 輸出串流到 state 之外的 log；Evidence 不保存 path（run-ID lookup 由 0027 修正） | accepted（由 0027 修正 lookup） |
| [0013](0013-forgepilot-self-adoption-of-forgeflow.md) | ForgePilot 借用 PraxisBound 的 Story 目錄格式，不執行 bootstrap、不交出治理所有權 | accepted |
| [0014](0014-working-tree-snapshot-is-a-candidate.md) | Working tree snapshot 是保留在 ForgePilot ref 下的 Candidate，不是 branch、stash 或 Work Item target | accepted |
| [0015](0015-runtime-contract-belongs-to-candidate.md) | Runtime Contract 從 Candidate checkout 解析；只固定該次 subprocess environment，actual versions 隨 run 進 Evidence | accepted |
| [0016](0016-goal-level-review-is-policy.md) | Goal-level review 是持久化 policy，不是略過 Human review 的捷徑 | accepted |
| [0017](0017-readiness-is-a-projection-made-durable.md) | Readiness 由 `reconcile` 明確重算；GOAL-policy stale VERIFIED 的重驗延後到無法前進時，仍欠在總審邊界 | accepted |
| [0018](0018-runner-may-launch-a-local-coding-cli.md) | Runner 可以啟動本機 coding CLI；核心治理命令與狀態判定仍不依賴模型服務 | accepted |
| [0019](0019-runner-executes-forgepilot-decides.md) | Runner 只保存 execution history，每一步重新向 typed query 取得合法動作 | accepted |
| [0020](0020-worker-ownership-is-fail-closed.md) | Worker 程序 ownership 以 pgid 加識別比對判定，不確定時拒絕續跑 | accepted |
| [0021](0021-execution-limits-are-bounded-and-named.md) | 停止原因在觸發當下寫定；單次期限與總期限取小；清理寬限是另一個量，且必須確認 | accepted |
| [0022](0022-pending-cleanup-outlives-the-process.md) | 未確認的清理持久化為 workspace 層級的恢復阻擋，與「上一次為什麼停」分開 | accepted |
| [0023](0023-verification-does-not-submit-for-review.md) | Verification 只記錄 Evidence；只有明確 `review request` 才送入 Human Review | accepted |
| [0024](0024-onboarding-stays-outside-the-offline-cli.md) | 安裝與 Agent 導入是分發程序，不讓核心治理 CLI 下載自己或接管 Story 內容 | accepted |
| [0025](0025-formal-macos-release-trust.md) | 正式 macOS binary 導入須先完成簽署、原生驗收與經核准的 immutable Release | accepted |
| [0026](0026-resolved-gates-cross-agent-session-boundaries.md) | Resolved Gate 從 current state 投影到後續 session；合法 Human wait 不消耗 technical attempt budget | accepted |
| [0027](0027-candidate-verification-pass-fans-out-by-run.md) | 同 Goal stale 工作共享一個 Candidate Verification Run；只有 PASS 原子 fan-out | accepted |
| [0028](0028-agent-session-checks-are-diagnostic.md) | Agent Session 做 focused diagnostics；Runner canonical Verification 是唯一 PASS owner | accepted |
| [0029](0029-story-readiness-contract-is-upstream-owned.md) | Whole-DAG Story readiness 使用 PraxisBound-owned sidecar 與 source digest；ForgePilot 只讀取並在 Runner preflight 檢查 | accepted |
| [0030](0030-source-built-onboarding-without-apple-developer.md) | 正式導入從固定 source version 本機建置；unsigned binary 只作 maintainer trial | accepted（部分取代 0025 的現行前提） |
| [0031](0031-goal-scoped-external-work-reference.md) | External Work Reference 是 Goal-scoped 的 immutable idempotency key；machine JSON 是狹窄 public projection | accepted |
| [0032](0032-formal-macos-onboarding-is-apple-silicon-only.md) | 正式 macOS onboarding 僅支援 Apple Silicon；Intel trial asset 不構成支援承諾 | accepted（取代 0025、0030 的雙架構前提） |
| [0033](0033-source-built-bootstrap-separates-distribution-from-onboarding.md) | Source-built Bootstrap 同版安裝 CLI 與 Codex skill；Repository Onboarding 仍獨立核准 | accepted（部分取代 0024、0030） |
| [0035](0035-supervised-goal-execution-with-bounded-rollover.md) | 長任務採 upstream 計畫／覆蓋核准、持久化有界授權、固定引擎背景執行與共用唯讀進度（待實作） | accepted |
| [0038](0038-versioned-bootstrap-generation-retention.md) | Bootstrap generation 綁定 source commit 與 payload digest；版本化 retention protocol 與 installer 共用鎖及狀態 | accepted |

## 什麼時候該加一份

三個條件同時成立才值得：難以反轉、後人不知道脈絡會覺得奇怪、而且是真實取捨的結果。例外是刻意的偏離——一個後人很可能會「修回去」的決定，即使反轉很便宜也該記，因為便宜的反轉正是要防的那件事（0003 與 0011 都屬於這一類）。

這個目錄不是只表達「做完了」的地方：proposed 的開放問題就住在這裡，不會被擠進只表達「做完了」的 roadmap 欄位。
