# Runner 執行，ForgePilot 判定

Long-running Runner 最容易長成的形狀，是一個自己記得「做到哪裡」的第二套工作狀態機：它讀一份啟動時的工作清單、自己標記完成、自己決定下一張。那個形狀被拒絕。

Runner 只保存 **execution history**——run id、step、attempt、預算消耗、程序 ownership、停止原因、引用到的 Evidence IDs。它不保存 Work Item 的 lifecycle，不保存「這張做完了」，也不保存自己算出來的 readiness。每一步之前，下一個合法動作都重新向 `internal/work` 的 typed query 取得，並且重新讀取 state 與必要的 repository facts。

這帶出三條具體約束。

**取得的是型別，不是文字。** Runner 呼叫 `State.ActionableNextForGoal`，拿到 `NextAction`；它不解析 `forgepilot next` 的 `Next:`／`Action:` 輸出，也不解析錯誤訊息或 exit code 去推測發生什麼事。CLI 的文字輸出是給人看的呈現，改動它不該讓 Runner 壞掉，而如果 Runner 解析它，改動它就會讓 Runner 壞掉。

**先限定 Goal，再套用既有規則。** Goal-scoped query 與全域 query 共用同一份 priority 與 legality 實作，差別只有「候選集合限定在這個 Goal」。它不是「取全域第一項，然後因為屬於別的 Goal 而停止」——那會讓同一個 repository 裡的第二個 Goal 製造假的停滯。篩掉的只有候選項，判斷依賴時讀到的仍是完整 state。

**lifecycle 更新只走 typed transition。** Runner 不寫 VERIFIED、不寫 DONE、不核准 review、不解除 Gate，也不直接改 Goal。它能做的寫入包含 `StartWithRepository` 開始一件工作、`ReconcileGoalReadiness` 重算 readiness、既有 verification orchestration 產生 Evidence、`completion_policy=VERIFIED` 的 `CompleteVerifiedGoal`，以及 session 回報 `needs_human` 且列出至少兩個選項時，用既有 `OpenGate` 記下那個問題。除此之外它只讀；Goal completion 的判定、Candidate facts 與 aggregate evidence 仍由 application／domain transaction 擁有。

第四種寫入值得單獨說，因為它是模型的文字唯一一次變成持久化的治理狀態。它被接受的理由是方向：開 Gate 只會擋住工作，不會放行任何東西——`OpenGateCount > 0` 會讓 `advanceable`、`Verifiable` 與 Goal completion-readiness projection 一致地拒絕前進。幻覺出來的問題因此最多造成一次需要人來 `gate cancel` 的停頓，不會造成一次不該發生的推進。代價是那個停頓確實需要人，所以 question 與 options 有長度與數量上限，Runner 不代答也不自行解除。Goal completion itself follows [ADR-0037](0037-goal-completion-has-no-human-final-review.md) and does not add a final Human Review stop.

`--dry-run` 是同一條原則的檢驗：它跑得完整個判定路徑而不寫任何東西，因為判定本來就不屬於 Runner。

**Consequences:** Runner 的 run record 損毀或遺失時，工作進度不會遺失——它從來不在那裡。恢復時以 ForgePilot 最新 domain state 為準，run record 落後只影響預算與 ownership 的核對，不能讓已經 VERIFIED 的工作被重新實作一次。Runner 也因此無法「加速」：它沒有任何比重新查詢更快的捷徑。

**信任邊界。** Runner 交給 coding CLI 的是 workspace 寫入權限，而 `.forgepilot/` 就在 workspace 裡。交接內容裡「不要碰 `.forgepilot/`」是對未受信任模型提出的請求，不是機制。coding session 前後會比對整份 `state.json` digest；不同即以 `AGENT_WROTE_FORGEPILOT_STATE` 停止，並且不採信該次 attempt 的任何回報。這個選擇會把 session 期間任何人類 ForgePilot command 也當成可疑寫入；人必須先讓 session 停止，再操作治理狀態。

canonical check 是第二個不受 Runner 控制的執行窗口：agent 可以在 session 內修改 project check，之後它會在 Candidate checkout 執行。這個窗口不能使用整份 state digest，因為 Runner 刻意不長持 state transaction lock，人在檢查期間對不相關 Goal 或 Gate 的合法治理寫入不該讓已誠實取得的 Evidence 消失。因此只比對正在驗證的完整 Work Item（含 `current_run`）、owning Goal 的 repository／review policy 與完整 latest Verification Evidence；Goal 的 status、reason、timestamps 刻意不納入，已開始的 verification 會依既有 lifecycle 契約在 Goal 被 block/cancel 後保存 Evidence。比較不只在 check 結束後做一次，也會在同一個 state transaction 寫入 PASS／FAIL 或 timeout 的 INTERRUPTED Evidence 前重做，避免兩者之間插入改寫。代價是明確接受：這不是針對全域 state 的完整性保證，canonical check 若改寫 sibling Work Item、其他 Goal 或 Gate，這個 guard 不一定會偵測。兩個 guard 都只能在寫入後停止，不能隔離仍在執行的惡意程序或阻止 transaction 後的寫入；workspace ownership lock 也只協調 Runner。這一版選擇保留人類並行治理與局部 Evidence 保護，而不宣稱 untrusted project code 的全域治理安全。

**Falsified if:** `internal/runner/` 出現 Work Item 狀態欄位、自己的 readiness 計算、或任何對 `internal/work` transition 以外的 state 寫入；或 `internal/runner/` 不再於 session 前後比對 `state.json` digest；或 `internal/runner/` 開始 import `internal/cli` 或解析其輸出；或 `ActionableNextForGoal` 與 `ActionableNext` 的優先順序實作分岔成兩份。任一項發生，Runner 已經變成第二套狀態機。
