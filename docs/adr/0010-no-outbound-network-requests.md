# 不主動發出網路請求

`docs/development-plan.md` 把 network API 與 database、Web UI、daemon 等並列為各階段不得提前加入的東西。該條讀成嚴格版：ForgePilot 既不對外提供網路介面，也不主動發出對外請求——不自行 HTTP，也不 spawn `gh` 之類的外部客戶端。

M4 是這條界線第一次被真正推擠的地方。PR review 有一條看似自然的路：去 GitHub 讀這個 PR 的 review state，把它轉成 Evidence。那條路被拒絕。

三個理由。其一，Evidence 的 result 集合是封閉的（PASS／FAIL／INTERRUPTED、APPROVED／REJECTED），沒有位置放「遠端不可用所以無法判斷」；要容納它就得改動 M2 就定下的 Evidence 形狀，而那個形狀是整個產品的地基。其二，[ADR-0005](0005-self-asserted-decision-maker.md) 已經決定決策者身分是自述而非認證；從 GitHub 取回 review 會讓產品第一次宣稱一件經過認證的事實，兩種強度的紀錄混在同一個容器裡，讀的人無從分辨手上這筆是哪一種。其三，state 是本機信任資料，目前沒有任何一條規則依賴遠端可用性；加入之後 `verify` 與 `review` 都多出一整類與工程無關的失敗。

**Consequences:** PR review target 的 HEAD 必須是本機 repository 裡真實存在的 commit，`review` 才能記錄。PR reference 是使用者輸入的識別字串，ForgePilot 只驗格式，不查證那個 PR 存在、是否開著、HEAD 是不是它。PR 標錯不會被偵測，也不該假裝會。

**Falsified if:** `internal/repository/` 出現 `net/http` 或任何網路 client，或 `exec.Command` 開始呼叫 `gh`、`curl` 之類的外部客戶端，或 `internal/work/evidence.go` 的 result 集合新增表示「無法判斷」的值。任一項發生，都表示這條界線已被跨過，而上面三個理由需要重新回答。
