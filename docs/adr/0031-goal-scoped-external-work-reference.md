# External Work Reference 是 Goal-scoped 的 idempotency key

外部 Agent 只能透過公開 CLI 建立 Work Item，不能安全讀寫 `.forgepilot/`。在一批
`work add` 中途失敗或結果不明時，僅靠 ForgePilot 配發的 `WI-*` ID 無法判定哪一筆已經落盤；用
Story path 猜測又會改寫既有「同一 Story 可有多張 Work Item」的語意。因此 schema v10 在 Work
Item 上加入選填的 `external_ref`：它是 opaque、大小寫敏感、Goal-scoped 的 immutable idempotency
key，不是 Story identity、revision、PR 或 lifecycle rule。

同一 `(goal_id, external_ref)` 的 request 僅在 normalized `story_ref` 與 dependency **set** 相同時
回傳原 Work Item；回應中的 `created: false` 讓 caller 知道那是重試。任何不同 request 都被拒絕，
不改動 state；沒有 key 的舊 Work Item 不會被自動認領。查找與建立在同一把 state lock 下完成，且
先查到既有 key 才讀取對新建項目才需要的 Candidate facts，因此不會因無關的 repository fact 讓安全
重試變成失敗。Goal 後來被 block 或 Work Item 已 DONE 也不會改寫這個 historical association。

公開 JSON 也刻意不序列化 durable State。`goal create --json`、`work add --json`、
`work list --goal <id> --json` 各自提供最小、版本固定的輸出 DTO；它們不洩漏 Evidence、Run、工作樹
路徑或後續 state 欄位。成功 document 的 `format_version` 為 `forgepilot.cli/v1`，破壞性變更必須
開新 format version。非零 exit 仍使用既有診斷，不提供半套 JSON error envelope。

**Consequences:** external ref 必須是有效 UTF-8、無控制字元、非空且無前後空白；不 trim、lowercase
或 normalize。v9 migration 為所有歷史項目寫入空 reference，rollback 必須還原 `state.json.v9.bak`，
因此會失去 migration 後的全部治理 state，而非只失去 ref。`work list` 只讀、依持久化 creation
order 回傳一個 Goal；`depends_on` 永遠是 array，keyless historical item 的 `external_ref` 為 null。

**Falsified if:** `ExternalRef` 開始參與 Work Item lifecycle、Evidence／Candidate／PR 判定，或 keyless
Work Item 被用 Story path 自動認領；`internal/cli` 直接 marshal `work.State`／`work.Item` 作 public JSON；
或同一 key 的查找與建立不再在 `storage.Update` 的同一交易內。任一情況都要重新回答 ownership、
compatibility 與安全重試的取捨。
