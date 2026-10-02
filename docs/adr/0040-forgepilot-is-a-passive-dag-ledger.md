---
status: accepted
---

# ForgePilot 是被動的 DAG 帳本

ForgePilot 的定位收斂為：工單拆分完成後，讓外部 Agent 依拓撲順序把一張 DAG 上的工作全部完成。
外部 Agent 自己跑迴圈；ForgePilot 只判定下一個合法動作、保存綁定確切 Candidate 的 Verification
Evidence，並在節點完成的同一次交易內解鎖下游。它不啟動 coding CLI、不管理安裝版本，
除了 Git 與受管理 repository 的 canonical `make verify` 之外不啟動任何程序，也不發出網路請求。

**文件狀態：** 2026-10-02 的 `grill-with-docs` 兩輪 Q1–Q12 均獲使用者「照建議」確認，
共識摘要已獲明確確認。這是已定案、尚未實作的收斂；程式碼在實作完成前仍含被移除的能力。

## 依據

2026-10-02 量測三個採用 repository（ForgePilot、StoreWeave、Dbcli）的 state 與程式碼足跡：
Runner 只在 ForgePilot 自身執行過 15 次（全在 2026-09-21），其他兩個 repository 都由外部
Agent 逐步呼叫 CLI。Supervised execution 正式程式約 9.3k 行、Runner 約 5.4k 行，
兩者加總超過 DAG、Verification、Snapshot、Gate、Review 等九群的總和（約 8.9k 行）；
schema 18 次升版中 6 次源自 supervised execution。Snapshot Candidate 被使用 148 次、
Gate 10 次，屬實際在用的能力。

## 決定

- **驅動者在外部。** 移除 Runner、Agent Session、Supervised execution（Execution Authorization、
  Ledger、Witness、Approval Token、Plan Binding、Engine retention）與只服務 Runner 的
  Story Readiness review。Claude Code 之類的 Agent 已是合格的迴圈執行者；ForgePilot 不可取代的
  是合法動作判定與不可偽造的完成條件。
- **DAG 由一份 Goal Plan 匯入。** `goal import <plan>` 以 ForgePilot 自己定義的最小 JSON 格式
  一次建立 Goal 與所有節點；節點 ID 即 Work Item ID。重新匯入同一 Goal 只接受新增節點與指向
  新節點的依賴，以節點 ID 冪等。移除 `work add`、External Work Reference 與自動配發編號。
  計畫拆得對不對是上游與人的責任，ForgePilot 只保證收到的是合法 DAG、Story 路徑存在。
- **PASS 即完成。** 預設 Goal 的節點在 current Candidate 上取得 Verification PASS 即成為 DONE；
  Goal 以 `require_approval` 開啟時 PASS 先進 REVIEW，`review approve` 後才 DONE。
  VERIFIED、Goal Review Policy、Completion Policy 與 Goal Completion Evidence 合併消失；
  PR Reference 移除。DONE 仍是終態且不判 stale（ADR-0006）；全部節點 DONE 時 Goal 自動完成，
  不再要求每個節點的 PASS 都對上最終 Candidate——拓撲序中後完成的節點，其 `make verify`
  已涵蓋整個 repository。
- **Readiness 讀取時計算。** PENDING／READY 不再保存，`reconcile` 移除。
- **同時最多一個 RUNNING。** 平行分支執行延後到確實需要時另行設計。
- **Verification 只保留 Snapshot 加固。** 移除 Runtime resolution（toolchain 由受管理專案的
  `make verify` 自行固定）與 Verification fan-out（一個節點一次 verify，讓「哪個節點在哪個
  Candidate 完成」保持單義）。
- **分發降為 `go install` 加手動複製 skill。** 移除 bootstrap transaction、generation retention、
  正式 macOS release trust 與 CI trial release。
- **Schema 斷代。** 不寫 v18 → 新 schema 的 migration，`migrate` 與舊升版鏈一併移除；
  以一次性匯出腳本把既有 repository 未完成的節點轉成 Goal Plan 後重新匯入，舊 state 封存。
- **保留不變：** Gate 與 Human Decision、Snapshot Candidate、detached worktree verification、
  Evidence 與 state 同一交易、離線、決策者自述。

## Considered Options

- **保留 Runner、只砍 supervised execution：** Runner 與 supervised execution 共用
  `runner.go`（約 850 行交織），且 Runner 無外部使用者；只砍一半留下的是無人用的半套。
- **節點完成仍要人工 approve：** Dbcli 實際逐件審查，所以保留為 Goal 層級選項而非刪除；
  但 ForgePilot 自身與 StoreWeave 都不用，不能作為預設。
- **開新 module 重寫：** 拒絕。原地分段刪減（J/K/I → L/D/E → 合併生命週期 → 重設 schema），
  每段 `make verify` 綠燈，避免拿可用的產品換未完成的重寫。

## Superseded

本 ADR 取代：0008（「approve 完成工作」部分；「沒有完成指令」仍成立）、0011、0015、0016、0017、
0018–0022、0023、0024、0025、0026、0027、0028、0029、0030、0031、0032、0033、0034、0035、
0036、0037、0038、0039。ADR-0010 的 Runner 例外隨 0018 失效，回到完全離線。

**Falsified if:**

- `internal/work/next.go` 一次推薦多於一個可開始的節點，或允許第二個 RUNNING 並存。
- `internal/storage/storage.go` 保存的 state 重新持久化 PENDING／READY readiness，
  或出現 Runner、execution 相關的持久化結構。
- `internal/app/verify.go` 讓一次 Verification Run 完成多於一個節點，或啟動 Git 與 canonical
  check 以外的程序。
- 一個 Goal 在仍有非 DONE 節點時被標為完成。
