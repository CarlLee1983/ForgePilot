# M3 — Human Gate & Review

前置決策已全部定案，見 [architecture.md](../../architecture.md#m3-開工前定案已完成) 與 `docs/adr/0005`–`0008`。本文件是實作規格；拆出的工作見 [issues/](issues/)。

## Problem Statement

M2 之後，一件工作可以被驗證，通過就停在 REVIEW。流程到此再次斷掉，而這次斷在最要緊的地方。

沒有任何路徑能讓工作到達 DONE，所以依賴永遠解不開：`WI-002` 依賴 `WI-001` 時，就算 `WI-001` 已經驗證通過，`WI-002` 仍然是 PENDING，而且會永遠是 PENDING。整個佇列因此只能跑完第一件工作。M1 當初就承認了這個缺口，把「A 完成後 B 可執行」的真實驗收延後到本階段。

第二個缺口是，工程過程中會冒出 ForgePilot 與 Agent 都無權決定的問題——架構取捨、範圍變更、有安全影響的選擇、規格本身有歧義。目前產品裡沒有地方表達「這件事卡住了，等人決定」，於是這類問題只能留在對話紀錄裡，換一個 session 就消失，而 Agent 面對它們時最容易做的事就是自己選一個然後繼續往下寫。

第三，Verification PASS 不等於可以完成。程式碼通過機器檢查，不代表有人看過它、認為它解決了對的問題。

## Solution

引入三組能力，合起來讓工作第一次能夠合法地完成。

**Gate** 讓任何人或 Agent 把一個需要人判斷的問題掛在特定的 Work Item 上，並列出至少兩個選項。只要那件工作還有未解除的 Gate，它就無法推進，也不會被 `next` 選中。人以 `gate resolve` 選定其中一個選項，或在問題本身不成立時以 `gate cancel` 附理由撤銷。決策連同自述的決策者身分永久保存。

**Human Review** 讓人對特定 revision 記錄 APPROVED 或 REJECTED。它和 Verification 一樣是 Evidence，綁定同樣的 repository、Story 與確切 commit。REJECTED 把工作退回 RUNNING。

**完成**不是一個指令。`review approve` 在記錄審查的同一次交易內檢查條件：同一 revision 的最新 Verification 為 PASS、最新 Human Review 為 APPROVED、沒有未解除的 Gate、Goal 為 ACTIVE。條件滿足就進入 DONE，並在同一次交易內把因此滿足依賴的下游工作從 PENDING 轉為 READY。

另外補上 Goal 的 lifecycle：`block` 用來擋住整個 Goal，`complete` 與 `cancel` 讓 Goal 有終點。

## User Stories

1. 身為使用者，我想在遇到 ForgePilot 與 Agent 都無權決定的問題時開一個 Gate，以便那個問題被記錄下來而不是留在某次對話裡。
2. 身為使用者，我想讓開 Gate 的人必須列出至少兩個選項，以便問題被想清楚，而不是把一段敘述丟出來。
3. 身為 Agent，我想在規格有歧義時開一個 Gate 而不是自己選一個繼續寫，以便我的猜測不會被當成已獲授權的決定。
4. 身為使用者，我想讓有未解除 Gate 的工作無法推進，以便決定真的擋得住工作，而不只是一則提醒。
5. 身為使用者，我想讓有未解除 Gate 的工作不會被 `next` 選中，以便我不會被指派到一件其實卡住的工作。
6. 身為使用者，我想在一件工作上同時掛多個 Gate，以便兩個無關的問題不必被硬塞進同一個問句。
7. 身為使用者，我想讓全部 Gate 都關閉之後工作才解除阻擋，以便沒有任何一個問題被漏掉。
8. 身為使用者，我想以 `gate resolve` 從列出的選項中選定一個，以便決策是結構化的，而不是一段要再解讀一次的文字。
9. 身為使用者，我想在 resolve 時附上自由文字的理由，以便選擇背後的判斷不會遺失。
10. 身為使用者，我想在選項全都不對時撤銷這個 Gate 並附上理由，以便我不必勉強選一個再用備註解釋其實不是那樣。
11. 身為使用者，我想讓撤銷同樣留下不可變的紀錄並在 `status` 看得見，以便撤銷無法悄悄發生。
12. 身為使用者，我想讓 Gate 一旦解除或撤銷就不能再被更動或刪除，以便決策紀錄是可信的歷史。
13. 身為使用者，我想讓決策連同決策者身分一起保存，以便日後回頭看時知道是誰做的判斷。
14. 身為使用者，我想讓那個身分預設取自 Git 設定，以便日常使用不需要每次多打一個參數。
15. 身為使用者，我想讓那個身分被明確標示為自述而非認證，以便沒有人誤以為它有保證。
16. 身為使用者，我想對通過驗證的工作記錄 APPROVED，以便「有人看過並認可」成為產品裡的事實而不是口頭默契。
17. 身為使用者，我想對通過驗證但我不認可的工作記錄 REJECTED 並附理由，以便只能核准的審查不會假裝成審查。
18. 身為使用者，我想讓 REJECTED 把工作退回 RUNNING，以便 Agent 直接回到修復狀態。
19. 身為使用者，我想讓審查結果綁定確切的 commit，以便它不會被套用到別的版本。
20. 身為使用者，我想讓審查紀錄和驗證紀錄放在同一個地方，以便「這個 revision 上發生過什麼」能一次讀完。
21. 身為使用者，我想在條件滿足時讓 approve 直接完成工作，以便 DONE 是條件被滿足的結果，而不是一個可以下達的命令。
22. 身為使用者，我想讓產品裡不存在任何完成指令，以便沒有人能在條件不足時想辦法讓它成功。
23. 身為使用者，我想在未經驗證的工作上被拒絕完成，以便 PASS 不能被跳過。
24. 身為使用者，我想在未經審查的工作上被拒絕完成，以便人的判斷不能被跳過。
25. 身為使用者，我想讓不同 revision 的 PASS 與 APPROVED 無法湊成完成，以便兩者必須指向同一份程式碼。
26. 身為使用者，我想在同一 revision 上較新的 FAIL 勝過較舊的 PASS，以便重跑驗證確認穩定性這件事有真實效果。
27. 身為使用者，我想在同一 revision 上較新的 REJECTED 勝過較舊的 APPROVED，以便審查者能改變心意。
28. 身為使用者，我想在還有未解除 Gate 時被拒絕完成，以便待決的問題不會被完成動作繞過。
29. 身為使用者，我想在 approve 之後看到依賴這件工作的下游立刻變成 READY，以便佇列真的會前進。
30. 身為使用者，我想讓完成與解鎖發生在同一次交易內，以便任何時候讀取狀態都不會看到「A 已完成但 B 還沒解鎖」的中間狀態。
31. 身為使用者，我想讓只有全部依賴都完成的下游才解鎖，以便還缺其他前置的工作不會被提早放行。
32. 身為使用者，我想在 approve 當下的 HEAD 與最新 PASS 對不上時仍能記錄審查，以便先審後驗這種正當流程不被禁止。
33. 身為使用者，我想在那種情況下從 `status` 看到工作為什麼沒有完成，以便我不會對著一個沉默的結果猜測。
34. 身為使用者，我想讓 DONE 之後不會因為新的 commit 而重開，以便完成是穩定的歷史事實而不是隨時會被收回的狀態。
35. 身為使用者，我想讓產品裡不存在 reopen，以便需要重做時我會新增一件工作，讓重做的理由有地方被記錄。
36. 身為使用者，我想讓 DONE 的工作不被標示 stale，以便沒有行動意義的警示不會累積到我開始忽略所有警示。
37. 身為使用者，我想仍然看得到 DONE 工作完成時的 revision，以便我知道它是在哪一版上完成的。
38. 身為使用者，我想擋住整個 Goal，以便發現方向不對時能一次停下它底下所有工作。
39. 身為使用者，我想在擋住 Goal 時底下正在跑的工作維持原狀，以便我不會因為按下暫停而丟失狀態。
40. 身為使用者，我想讓被擋住的 Goal 解除之後一切照舊，以便暫停是可逆的。
41. 身為使用者，我想讓 Goal 被擋住時，正在進行的驗證跑完仍然記錄它的結果，以便已經發生的事實不會被丟棄。
42. 身為使用者，我想在全部工作完成後手動宣告 Goal 完成，以便「到此為止」是我說的而不是系統推斷的。
43. 身為使用者，我想在還有未完成工作時被拒絕宣告 Goal 完成，以便那個宣告有實質意義。
44. 身為使用者，我想取消一個不再要做的 Goal 並附理由，以便廢棄的工作有終點而不是永遠掛在 ACTIVE。
45. 身為使用者，我想在 `status` 看到每件工作有幾個未解除的 Gate，以便一眼看出哪些工作卡住了。
46. 身為使用者，我想在 `status` 看到最新一筆審查結果，以便掌握它離完成還差什麼。
47. 身為使用者，我想在 M2 的 state 上執行升級後保留全部既有資料，以便先前累積的佇列與 Evidence 可以直接沿用。

## Implementation Decisions

**新增九個指令**：`gate open`、`gate resolve`、`gate cancel`、`review approve`、`review reject`、`goal block`、`goal unblock`、`goal complete`、`goal cancel`。不提供 `done` 或任何等價指令（[ADR-0008](../../adr/0008-approval-completes-work.md)），不提供 Gate 查詢指令，不提供 `--json`。`status` 擴充輸出，`next` 的選取規則加入「無未解除 Gate」這一條——該條件在 M1 就已寫入設計，本階段才啟用。

**`internal/work` 承接全部新規則，仍然不碰 filesystem、Git 或 subprocess。** 外部事實依舊以參數傳入，沿用 `now time.Time` 的既有模式；決策者身分由 CLI 解析後傳入，domain 不去讀 Git 設定。M3 不需要 `internal/repository` 的任何新能力。

**Gate 依附單一 Work Item**，不設 Goal 層級 Gate；擋整個 Goal 用 `Goal.BLOCKED`。Gate 保存在同一份 state snapshot 內，與 Work Item 共用同一次受鎖的原子替換，理由同 [ADR-0001](../../adr/0001-evidence-in-state-snapshot.md)。ID 沿用既有的配發方式，由受鎖操作發出遞增序號。

**Gate 集合為 append-only**：Gate 可從 OPEN 轉為 RESOLVED 或 CANCELLED，此後不可再變更也不可刪除。這是它不需要另外複製成 Evidence 的前提（[ADR-0007](../../adr/0007-blocking-is-not-a-status.md) 之外的獨立決定，記於 architecture 的 M3 定案第 2 點）。

**阻擋不佔用狀態欄。** Work Item 狀態集合為 PENDING、READY、RUNNING、VERIFYING、REVIEW、DONE，沒有 `WAITING_HUMAN` 也沒有 `BLOCKED`。開啟或關閉 Gate 不是 transition，不改變狀態，只改變工作能否推進。Gate 可以開在任何非 DONE 的工作上。

**Human Review 是 Evidence 的第二個 type**，沿用同一個容器與同一條 ID 序列。M2 已經為此預留了 type 欄位，且已因 INTERRUPTED 把 exit code 改為可空，因此新增 review 專用的可空欄位不引入新的形狀問題。

**完成的判準**：同一 revision 的**最新**一筆 Verification 為 PASS，且**最新**一筆 Human Review 為 APPROVED，且該 Work Item 無未解除 Gate，且其 Goal 為 ACTIVE。「曾經出現過即算數」被明確排除——那會讓同 revision 重跑驗證這個能力反過來變成漏洞。

**完成與解鎖在同一次 state 交易內**。進入 DONE 之後，於同一交易重新計算受影響的 PENDING 工作，只有全部依賴皆為 DONE 者才轉為 READY。既有的 `RefreshReady` 至今沒有任何產品路徑呼叫，本階段是它第一次被真正接上。

**Schema 升至 v3**：新增 Gate 集合與其 ID 配發計數，Evidence 加入 review 專用的可空欄位。沿用 M2 建立的升級契約——不自動升級，由 `migrate` 備份後升級，備份已存在時拒絕，對已是最新版本者回報並成功結束，不提供 downgrade。備份檔名沿用以來源版本命名的規則。

**Goal 非 ACTIVE 的語意**：擋住的是「開始新工作」與「到達 DONE」，不是「記錄已發生的事」。Goal 在某次驗證進行中被 block，該次驗證跑完仍須記錄其 Evidence。

## Testing Decisions

好的測試只驗外部可觀察的行為——CLI 輸出、exit code、以及重新讀回的 state——不驗內部呼叫順序或私有結構。M1 與 M2 已確立此取向，M3 沿用。

**不新增任何測試 seam。** M3 沒有引入任何新的外部依賴，所以連 M2 那種「要不要為 Git 造 port」的問題都不存在。

**重心在既有的 `internal/work` domain seam。** M3 的規則幾乎全是純資料運算，因此多 Gate 的阻擋語意、`options` 的約束、resolve 只接受列出的選項、cancel 解除阻擋、Gate 終態不可變更、同一 revision 取最新一筆的判定、完成的四項條件、以及依賴解鎖該解開哪些與不該解開哪些，全部在這裡以純函式驗證。這是最便宜也最精準的位置。

**既有的 CLI process seam 負責兩件事。** 一是九個新指令的介面契約與錯誤行為。二是 M1 分段驗收時明確延後至此的端到端流程：在 temporary Git repository 中，以獨立 process 完成 `A start → verify → approve → DONE → B READY`。那條流程的價值正在於它跨越所有層，不能只在 domain 驗證。

**原子性的分工**：domain seam 驗規則（什麼條件下解鎖、解鎖哪些），process seam 藉端到端流程順帶覆蓋原子性——新 process 讀回時只會看到解鎖前或解鎖後。不另外建立注入失敗的測試，因為 M1 的受鎖交易與原子替換已有專門測試涵蓋，且該保證與 M3 的新規則無關。

**必測行為**：未解除 Gate 阻擋推進且不被 `next` 選中；多 Gate 需全部關閉；resolve 拒絕未列出的選項；cancel 解除阻擋並留下理由；Gate 終態不可再變更；開關 Gate 不改變 Work Item 狀態；REJECTED 退回 RUNNING；未驗證或未審查不可完成；不同 revision 的 PASS 與 APPROVED 不可組合；同 revision 較新的 FAIL 勝過較舊的 PASS，較新的 REJECTED 勝過較舊的 APPROVED；有未解除 Gate 時不可完成；完成後下游在同一交易內解鎖，且僅解鎖全部依賴皆 DONE 者；DONE 無任何 reopen 路徑；DONE 工作不標示 stale 但仍顯示完成時的 revision；approve 與 PASS revision 不符時仍記錄審查、不完成、且 `status` 說明原因；Goal 非 ACTIVE 時活躍工作維持原狀但無法推進，進行中的驗證跑完仍記錄 Evidence；`goal complete` 在尚有非 DONE 工作時被拒；v2 state 被拒讀並指示升級，升級後資料完整。

## Out of Scope

PR number 與 HEAD SHA 的 review target 屬於 M4。

不提供 `done`、`complete <work-id>` 或任何能直接寫入 DONE 的路徑；不提供 reopen；不提供 Gate 查詢指令、`--json`、任意 status mutation。不提供逾時同意或任何形式的預設核准。不做決策者身分的認證——記錄的是聲明（[ADR-0005](../../adr/0005-self-asserted-decision-maker.md)）。

不擴充平台支援。macOS 本機檔案系統仍是唯一驗證過的範圍。

不加入 database、Web UI、daemon、scheduler、agent runtime、plugin framework 或 network API。

## Further Notes

M3 的指令面是九個，遠大於 M2 的兩個。這是 Gate、Human Review 與 Goal lifecycle 三塊同時落地的必然結果，也意味著切片數會多於 M2。Gate 與 Human Review 彼此獨立，可平行推進；完成與解鎖則同時依賴兩者。

`RefreshReady` 自 M1 就存在卻從未被任何產品路徑呼叫，`DONE` 也只有測試 fixture 能設定。M3 是這兩者第一次真正接上，因此實作時要留意 M1 留下的假設是否仍然成立，而不是假定它們已經被驗證過——既有的測試只覆蓋了規則本身，沒有覆蓋它在真實流程中的接線。

M2 把 Evidence 的 exit code 改成可空以容納 INTERRUPTED，這件事恰好讓 M3 增加 review 專用的可空欄位變得自然。反過來說，如果當初把 INTERRUPTED 排除在 Evidence 之外，這裡就會面臨第二次同樣的抉擇。
