# Runner resolved-Gate continuity

## Outcome

一個 Agent Session 以 `needs_human` 停止並建立 Gate、由人 resolve 後，resume 的新 session 必須直接取得 durable Human Decision；依賴該工作的下游 session 也必須取得相同決策。合法的人工作答不消耗 technical retry budget，但仍受整個 run 的 step、duration 與 artifact bounds 約束。

這一個 slice 不處理 Candidate-level verification fan-out、worker verification profile、決策語意去重或自動套用。Runner 仍不 resolve Gate、不推進 Human Review、不完成 Goal。

## Interface and seam

- `internal/runner` 擁有 handoff projection：每次 session 啟動前重新讀取 current state、重驗原 action 仍是 typed query 的目前答案，再取目前 Work Item 加上完整 transitive prerequisite closure。action 已因 OPEN Gate 或其他 state change 失效時不啟動 session，由下一輪重新判定。
- `internal/agent.Handoff` 是 worker 與測試共同使用的 interface。它接收 typed resolved decisions，render 時把它們放在 attempt summaries 之前。
- 測試 seam 是 fake runtime 實際收到並保存的 `handoff.md`，以及持久化的 `run.json` accounting；不測 private traversal implementation。

## Decision projection

每一筆交接的 resolved decision 包含：Gate ID、所屬 Work Item ID、question、selected choice、resolution note。順序沿用 state 中 Gate 的開立順序。

納入：

- 目前 Work Item 的 RESOLVED Gates。
- 所有 direct 或 transitive prerequisites 的 RESOLVED Gates；diamond dependency 只呈現一次。

排除：

- OPEN Gates：它們阻擋合法 session，本來就不應進入可執行 handoff。
- CANCELLED Gates：它沒有 selected choice，表示問題被撤回而不是一個可沿用的決策。
- sibling、downstream、其他 Goal 的 Gates。

ForgePilot 不判斷兩個自然語言問題是否等價，也不因既有決策而自動 resolve 新 Gate。已決資訊的作用是讓 worker 先看見權威脈絡；是否真的需要一個新問題仍由 worker 依 Story 與專案規則判斷。

## Retry accounting

- `attempts[work]` 保留所有已配置 Agent Session slots 的單調序號，繼續作為 session artifact key；啟動前的 fail-closed charge 不回收或重用。
- `human_waits[work]` 記錄已通過 result protocol 並 checkpoint outcome 的 `needs_human` sessions。
- technical attempts = `attempts[work] - human_waits[work]`，`--max-attempts-per-work` 只限制這個值。
- 一次 session 在啟動前先加入 attempts；合法 `needs_human` outcome 的 attempt summary checkpoint 成功後才增加 human waits。至少兩個選項時另以既有 service 建立 durable Gate；選項不足時不製造 Gate，但分類仍是 human wait。
- Protocol error、execution failure、timeout／signal 與 implementation session 都不 reclassify。
- 舊 run record 缺少 `human_waits` 時讀成空 map，保守保留所有既有 attempt charge；不重置 deadline、steps 或 attempt sequence。

## Acceptance matrix

| Scenario | Observable result |
|---|---|
| `needs_human` 有至少兩個選項 | 建立一個 OPEN Gate，以 `AGENT_NEEDS_HUMAN` 停止 |
| 人 resolve 後 resume 同一 Work Item | 新 session 的 handoff 含完整 question、choice、note；session ordinal 增加 |
| `--max-attempts-per-work=1` 且第一個 session 是合法 human wait | resolve 後仍可啟動一個 technical session；run record 為 2 attempts、1 human wait |
| 下游有 direct／transitive prerequisites | handoff 繼承 ancestor RESOLVED Gates，diamond 不重複 |
| sibling 或 CANCELLED Gate | 不出現在該 worker 的 resolved-decision section |
| 原 decision 之後、handoff 之前開啟 OPEN Gate | stale action 被拒，不啟動 Agent Session；下一輪回報 Gate wait |
| 合法 `needs_human` 缺少可記錄選項 | 不製造 Gate，但增加 human waits；仍受 steps／duration 限制 |
| technical sessions 不收斂 | 既有 max-attempts stop 仍然有界 |
| handoff 接近 byte budget | resolved decisions 與其他 required sections 保留；optional attempt／failure context 可明示截斷 |
| pre-launch state digest 讀取失敗 | attempt charge 保留，但 run record 不留下從未啟動的 phantom worker |
