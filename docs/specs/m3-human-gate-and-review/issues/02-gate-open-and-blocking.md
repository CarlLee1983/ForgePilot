# 02: Gate 的開啟與阻擋

**What to build:** 使用者或 Agent 遇到 ForgePilot 與 Agent 都無權決定的問題時，把它掛在特定的 Work Item 上並列出至少兩個選項。那件工作隨即無法開始也無法驗證，`next` 不再選中它，`status` 顯示它有幾個未解除的 Gate。

關鍵在於開啟 Gate **不改變** Work Item 的狀態：阻擋是另一個維度的條件，不佔用狀態欄。一件 RUNNING 的工作被開了 Gate 之後仍然是 RUNNING，只是不能再往前。

此票尚無解除 Gate 的方法，因此被擋住的工作要等票 03 才能前進。

**Blocked by:** 01

**Status:** ready-for-agent

- [ ] `gate open` 配發 Gate ID 並保存問題、選項與可選的理由
- [ ] 少於兩個選項時被拒絕
- [ ] 未知的 Work Item、或已 DONE 的 Work Item 被拒絕
- [ ] 有未解除 Gate 的工作無法 `start` 也無法 `verify`，錯誤訊息指出被 Gate 阻擋
- [ ] 有未解除 Gate 的工作不被 `next` 選中
- [ ] 開啟 Gate 不改變 Work Item 的狀態
- [ ] 同一件 Work Item 可同時掛多個 Gate
- [ ] `status` 顯示每件工作未解除 Gate 的數量
- [ ] Gate 與 Work Item 共用同一次受鎖的原子替換，並行開啟不遺失更新或重複配發 ID
- [ ] `make verify` 通過
