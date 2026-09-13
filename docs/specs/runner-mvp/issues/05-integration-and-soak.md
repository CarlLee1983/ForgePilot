# 05 — 整合測試、回歸測試、soak test 與文件

## 驗收

- [x] 多依賴／匯合依賴：必要依賴 stale 時先重驗。
- [x] verification 命令正常退出但 Evidence 為 FAIL：有限 repair，不當成 PASS。
- [x] 認證／runtime／toolchain 問題正確分類，不產生假的工程 FAIL。
- [x] 可恢復 PENDING readiness 經既有 reconcile 推進。
- [x] Evidence 已保存後 Runner 崩潰：依最新 domain state 恢復，不重複實作。
- [x] Resume 建立新 session，預算與 deadline 不重置。
- [x] 較多 Work Items 的 deterministic soak test，驗證跨 session 調度、預算與容量上限；不以 sleep 冒充長跑。
- [x] 既有功能回歸：全域 `next`、WORK_ITEM policy、`verify`、`review`、`status` 保持相容。
- [x] 真實 Codex smoke test 為 opt-in，預設 CI 不跑；未執行時如實記錄。
