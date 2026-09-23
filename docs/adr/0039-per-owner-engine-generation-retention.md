---
status: accepted
---

# Per-owner engine generation retention

FP-58 必須同時保證 immutable ForgePilot engine generation 不會在仍可執行時被 Bootstrap
刪除，且在 crash／state-write failure 時不會因猜測 owner 而漏留 pin。採用每個 current
Execution Authorization 與每個尚未 durable-close 的 supervised Run 各自持有一個
domain-separated、deterministic opaque retention reference；Bootstrap 繼續只保存 reference
hash 與 generation tuple，不保存 repository、Goal、Run ID 或 raw reference。

Authorization owner 的 reference 只由 immutable authorization facts 與 exact generation tuple
導出；Run owner 的 reference 另加 immutable authorization digest 與 Run ID，不能與前者或另一
Run 共用。raw reference 不寫入 ForgePilot state 或 Run Record。`internal/app` 是唯一知道如何
derive／acquire／release 這些 reference 的 module；`internal/runner` 只把 durable lifecycle facts
交給它，`internal/agent` 不接觸 Bootstrap，Bootstrap 也不讀 target repository。

Acquire 必須先於第一次把 owner 的 generation tuple 寫入 execution state 或 `run.json`。Authorization
acquire 成功、state transaction 失敗時留下 orphan marker；Run acquire 成功、Run Record save
失敗時同樣留下 marker。兩者都是刻意安全的 retention leak。相反地，release 只在 owner 已先
durable-close 後進行：若 release failure 或 crash 發生，新的 owner state 已禁止其後續 launch，
多餘 marker 可以在後續相同 owner-closure recheck 中 idempotently retry；絕不先 release 再改寫
owner state。unknown、malformed、missing 或 unreadable owner／cleanup facts 一律不 release。

一個 current authorization 是 authorization owner，直到它被成功 supersede 或其 Goal 已進入
不允許 Runner launch 的終態；歷史 authorization 仍保存 tuple 但不再是 authorization owner。
一個 supervised Run 是 run owner，直到它保存了 immutable tuple 與 authorization digest、並經
一個 durable closure transition 證明不再可 launch／recover：Goal terminal、authorization revision
使其 exact resume 不合法，或其他已記錄且不可恢復的 terminal disposition。普通停止、human wait、
pending cleanup、record preparation 或 recovery ambiguity 都不 close Run owner。每個 closure 都先
以 Runner 現有的 full-record、fail-closed cleanup criterion 確認 `Worker` 為空且沒有 unresolved
`Pending`；不能以 stop reason、pid 消失或另一個 successor 的存在取代這個確認。

Engine revision 是特別的 authorization revision，不能由一般 revision 悄悄改變。v2 execution
request 必須明示 proposed `engineGeneration` tuple，revision preview／diff／approval token 都綁定它。
authorize 端從 candidate process image 的 managed helper 重新解析 tuple，拒絕不是 exact match 的
request、drifted helper 或 ambient `PATH` substitution。它先持久化一個包含 old authorization digest
與 old generation 的 pause intent，再在 workspace lock 內完成全部相關 Run Record 的 cleanup audit，
然後執行 read-only Engine Compatibility Check。該 check 至少要求 candidate 能以無 migration、無
repair、無 subprocess launch 的方式嚴格讀取 current state、control sidecar 與所有 relevant Run
Record，並確認它們的 engine/authorization bindings；缺檔、unknown schema、舊 schema、corruption
或任何不確定 owner 都失敗。

只有 compatibility 成功後才 acquire new authorization marker，並在一個 state transaction 重新檢查
pause revision、old authorization digest、candidate tuple、compatibility witness 與 approval token，再
append new authorization。commit 後保留 pause，直到明確 resume；它不改寫歷史 authorization、Run
Record、ledger、Evidence 或 Work Item lifecycle。舊 authorization marker 可在 commit 後 release，
但每個舊 Run marker 要等該 Run own closure。這讓「歷史 run 仍綁定舊 generation」與「舊 generation
可在所有 owner 結束後被 prune」同時成立。

State schema v18 增加 engine-retention owner／closure provenance；Run Record schema 增加 immutable
engine tuple、authorization digest 及 closure provenance；execution-control sidecar v2 的 revision pause
增加 old authorization digest 與 old generation tuple。v17／舊 Run Record 不推測 owner 或 cleanup
completion：migration 保留歷史但標記為 unknown，engine revision、release 與新的 pinned launch 必須
fail closed，直到明確 v2 authorization / compatible successor 建立新的 facts。rollback 必須還原 migration
backup；已取得的 Bootstrap marker 可以留下，因為它是安全且不包含 repository identity 的 leak。

**Considered Options:** 掃描所有 authorization 與 Run Record 即時推測 owner，會把 lifecycle rule 分散到
`Stop`、`Worker`、`Pending`、preparation state 和可損毀檔案，且無法安全處理 release retry；一個
Goal-wide shared refcount 則建立跨 state／run-file crash recovery 的第二個 execution authority。per-owner
marker 讓 Bootstrap 現有 marker set 表達 owner 聯集，每個 crash window 都只會過度保留。

**Falsified if:** `internal/runner` 自行導出或呼叫 Bootstrap reference；raw reference、workspace、Goal
或 Run identity 寫入 Bootstrap state；任何 durable generation reference 先於 acquire；任何 release
先於 durable owner closure 或未確認 cleanup；ordinary authorization revision 可無 pause／compatibility
而改 engine；或 historical authorization／Run binding 被覆寫以取得 retention release。
