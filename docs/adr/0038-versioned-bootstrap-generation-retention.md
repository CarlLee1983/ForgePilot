# Versioned Bootstrap generation retention

ADR-0033 的 immutable generation 以完整 Bootstrap Source commit 與 canonical SHA-256 payload digest 組成 identity；managed path 仍以 source commit 命名，同一 commit 若再次產生不同 payload 必須拒絕覆寫。每代另安裝同版 `forgepilot-bootstrap` helper，讓 CLI、Codex skill 與 retention manager 經同一個 `current` pointer 發佈。

Bootstrap 提供 `retention-v1 acquire|release` machine protocol。呼叫者傳入 exact generation tuple 與 256-bit opaque reference；helper 在和 install、upgrade、prune、uninstall 共用的 exclusive lock 下驗證及更新 user-home retention store。Store 只保存 reference 的 SHA-256 與 generation tuple；無效、缺失或未知格式一律阻擋 generation deletion。Acquire 對相同 tuple 冪等，reference 不得改綁其他 generation；release 只移除完全符合的 reference，且本身不刪 generation。Prune plan 綁定 retention store 的摘要與筆數，不輸出 holder reference，因此任何 acquire/release 都會使舊 approval 失效；generation removal 仍須另外取得 fresh Bootstrap Approval。

這讓未來 ForgePilot 可先 acquire、再持久化 engine reference；清除最後一個 owner 後才 release。兩步之間發生失敗時，保留多餘 pin 是安全的，漏掉 pin 則不是。Bootstrap 不保存 target repository、run 或 holder 的可識別資訊，也不接收 repository path。

**Falsified if:** ForgePilot 可繞過 helper 直接改 retention store；retention 操作與 prune 使用不同鎖；reference raw value、repository identity 或 run identity 寫入 Bootstrap state；changed／unknown retention state 可被當成 unretained；或 pin acquisition/release 可以不經重新計畫而沿用舊 prune approval。
