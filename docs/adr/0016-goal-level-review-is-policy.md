# Goal-level review is a policy, not a review bypass

Goal 的 `review_policy` 以 `WORK_ITEM` 為預設，保留既有每件 Work Item 的 Human Review → DONE 契約；選擇 `GOAL` 時，machine PASS 只形成 `VERIFIED`，可在原有 Gate、Goal ACTIVE、verification failure／interruption 與 Candidate freshness 規則下推進依賴。這不是 `--skip-review`：VERIFIED 不是 Human acceptance 或 DONE，Work Item review 也不適用於 GOAL policy。

Progression 與 final acceptance 必須分開。Goal final readiness 是 ACTIVE、非空 Goal 上所有 Work Item VERIFIED、各自 latest PASS 仍匹配目前 Candidate、且沒有 OPEN Gate 的 fail-closed projection，並保留構成它的 Verification Evidence IDs；它不是持久化狀態，也不自動完成 Goal。持久化 READY 同樣 fail closed：只有帶 current repository facts 的 transition 可用 VERIFIED promotion downstream，facts 在 state transaction 的受鎖 callback 內解析，缺少時就保留 PENDING；refresh 只觸及已改變 prerequisite 的 dependents 或被 unblock 的 Goal。現階段沒有 goal review approve／evidence 或 Runner，`goal complete` 對 GOAL policy 因此拒絕。日後可在此 projection 上加入接受 exact aggregate Verification Evidence 的 Goal Evidence 與 Runner，不能把 VERIFIED 回溯解釋為 DONE。

**Falsified if:** `internal/cli/cli.go` 接受 `--skip-review` 或把 GOAL policy 當成單次權限，`internal/work/evidence.go` 將 GOAL-policy PASS 直接設為 DONE 或允許 Work Item review，`internal/work/work.go` 讓 VERIFIED 繞過 Gate／Goal／freshness 規則，或 `internal/work/summary.go` 在沒有完整 current PASS Evidence set 時宣告 Goal final readiness。
