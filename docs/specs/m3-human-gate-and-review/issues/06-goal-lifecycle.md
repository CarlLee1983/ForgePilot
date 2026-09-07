# 06: Goal lifecycle

**What to build:** 使用者發現整個 Goal 方向不對時能一次擋下它底下所有工作，之後解除時一切照舊；不再要做的 Goal 能被取消而不是永遠掛在 ACTIVE；全部工作完成後能宣告這個 Goal 到此為止。

被擋住的 Goal 底下，活躍的工作維持原狀——擋住不是丟棄狀態。而 Goal 非 ACTIVE 擋的是「開始新工作」與「到達 DONE」，不是「記錄已發生的事」：某次驗證進行中 Goal 被擋住，那次驗證跑完仍然要記錄它的 Evidence。

`goal complete` 是人手動宣告，不由系統推斷。自動標記會產生一個之後又得退回 ACTIVE 的可逆狀態，與 DONE 不可逆的立場矛盾。

此票同時把 `goal` 指令從單一子指令的寫法改為承載五個子指令。

**Blocked by:** 05

**Status:** ready-for-agent

- [ ] `goal block` 要求理由並把 Goal 轉為 BLOCKED；`goal unblock` 轉回 ACTIVE
- [ ] 被擋住的 Goal 底下，既有的 RUNNING 或 VERIFYING 工作維持原狀
- [ ] Goal 非 ACTIVE 時，其工作無法 `start`、`verify`，也無法到達 DONE，且不被 `next` 選中
- [ ] Goal 在某次 Verification Run 進行中被擋住，該次驗證跑完仍記錄其 Evidence
- [ ] `unblock` 之後工作回到可推進的狀態，先前的進度未受損
- [ ] `goal complete` 在尚有非 DONE 工作時被拒絕
- [ ] `goal complete` 在全部 Work Item 皆為 DONE 時把 Goal 轉為 COMPLETED
- [ ] `goal cancel` 要求理由並把 Goal 轉為 CANCELLED
- [ ] 非 ACTIVE 的 Goal 不接受 `work add`
- [ ] `make verify` 通過
