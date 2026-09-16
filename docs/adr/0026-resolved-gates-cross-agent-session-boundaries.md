# Resolved Gates cross Agent Session boundaries

Runner 每次 attempt 都建立新的 Agent Session，因此前一個 session 等待 Human Decision 後，下一個 session 不能只從未受信任的 attempt summary 猜答案。交接必須從目前的 ForgePilot state 即時投影該 Work Item 與其所有 transitive prerequisites 上的 RESOLVED Gate，包含原問題、選定選項與 resolution note；依 Gate 開立順序呈現，排除 OPEN、CANCELLED、sibling 與 downstream Gate。Runner 不做語意相似度判定、不自動套用決策，也不代替人解除 Gate；若要讓等價決策成為可機器判斷的 identity，必須另行設計明確 schema 與 authority policy。

`--max-attempts-per-work` 限制 technical attempts，而不是合法的 Human Decision 次數。Run record 的 `attempts` 仍是所有已啟動 session 的單調序號，另以 `human_waits` 記錄已通過 result protocol 並持久化 outcome 的 `needs_human` session；technical attempts 是兩者之差。啟動前仍先計費，只有合法的 `needs_human` 結果已 checkpoint 後才把該次 reclassify 為 human wait；protocol error、execution failure、中斷與一般 implementation session 都維持 technical charge。選項不足時不製造 Gate，但仍是 human wait；它與所有其他 wait 一樣消耗 step、duration 與 artifact budget，因此不會形成無界迴圈。舊 run record 沒有 `human_waits` 時保守視為零，不重置既有消耗。

這個決定讓 durable Gate 成為 Human Decision 的唯一事實來源，run record 只保存 execution accounting，延續 ADR-0019 的責任分工。它也刻意接受 state 與 run record 無法跨檔原子提交：human-wait reclassification 保存失敗時維持 technical charge；Gate 建立失敗時仍保留已 checkpoint 的 `needs_human` 分類，但不假裝存在一筆沒有成功保存的 Human Decision。

**Falsified if:** `internal/runner/handoff.go` 從 attempt summary 而非 current state 取得已決 Gate、`internal/agent/handoff.go` 允許在沒有完整 question／choice／note 的情況下把決策當作已交接，或 `internal/runner/runner.go` 讓 malformed／failed／interrupted session 逃離 technical attempt budget。
