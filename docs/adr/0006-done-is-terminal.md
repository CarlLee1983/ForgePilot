# DONE 是終態，不提供 reopen

Work Item 進入 DONE 之後不會因為 repository 新增 commit 而重開，也沒有任何指令能讓它離開 DONE。需要重做的工作以新增一件 Work Item 表達。

DONE 的意思是「在某個確切 revision 上完成」，那是已經發生的事實，後來的 commit 不會讓它變成假的。讓新 revision 自動重開會使佇列失去意義——每 commit 一次就重開所有已完成工作。提供明確的 `reopen` 指令則要處理一整串 cascade：DONE 是依賴解鎖的依據，重開一件就得把下游已經 READY 甚至 RUNNING 的工作往回收，並產生「已經開始做的工作被系統收回」這種難以解釋的狀態。

**Consequences:** DONE 工作的 Evidence 遲早都會對不上 HEAD，因此 `status` 不對 DONE 工作標示 stale——那個提示不對應任何應該發生的動作。改以新 Work Item 表達重做，還有一個附帶好處：「為什麼要重做」有地方被記錄，而 reopen 會把那個理由沖掉。

**Falsified if:** 出現必須讓已完成工作回到未完成、且無法以新增 Work Item 表達的需求。屆時要重新設計的是 `internal/work/work.go` 的依賴解鎖規則，因為 cascade 才是這個決定真正的成本所在。
