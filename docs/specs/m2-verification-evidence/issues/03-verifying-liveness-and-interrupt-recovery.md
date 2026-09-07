# 03: VERIFYING 落地、verify 鎖與中斷回收

**What to build:** 驗證可能跑上幾分鐘。使用者在這段期間從另一個終端機執行 `status`，應該看得到那件工作正在 VERIFYING，而且其他查詢指令不會因此被鎖死。如果驗證跑到一半被中斷——按下 Ctrl-C、關掉終端機、機器重開——下一次執行 `verify` 時，那次未完成的執行會被誠實地記為中斷，工作退回 RUNNING，使用者可以直接重跑。中斷永遠不會被寫成通過或失敗。

同一件 Work Item 不能被同時驗證兩次，但不同的 Work Item 之間互不阻擋。

**Blocked by:** 02

**Status:** ready-for-agent

- [x] 驗證進行中，另一個 process 執行 `status` 顯示該 Work Item 為 VERIFYING
- [x] 驗證進行中 `status` 與 `next` 仍可正常執行，不被驗證阻塞
- [x] 對同一件正在驗證的 Work Item 再次執行 `verify` 被明確拒絕
- [x] 不同的 Work Item 可以同時驗證
- [x] 驗證中的 process 被終止後，下一次 `verify` 先 append 一筆 INTERRUPTED Evidence 並將工作退回 RUNNING，然後才進行新的驗證
- [x] INTERRUPTED 的 Evidence 帶著當初那次執行的 commit SHA
- [x] 任何情況下都不由推斷產生 PASS 或 FAIL
- [x] 存活判定使用作業系統在程序死亡時自動釋放的鎖，不使用 pid
- [x] 驗證本身在 state 鎖之外執行；state 只在驗證前後各被短暫鎖住一次
- [x] `make verify` 通過
