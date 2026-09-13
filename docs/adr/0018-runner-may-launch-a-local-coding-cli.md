# Runner 可以啟動本機 coding CLI，治理判定仍不依賴模型

[ADR-0010](0010-no-outbound-network-requests.md) 把界線畫在「ForgePilot 不主動發出對外請求——不自行 HTTP，也不 spawn `gh`」。Long-running Runner 需要啟動 Codex 之類的本機 coding CLI，而那個 CLI 會連線到模型服務。這一份記錄界線如何被重新描述，以及哪一半沒有改變。

沒有改變的那一半是重點：**核心治理命令與狀態判定不依賴模型服務**。`init`、`goal`、`work`、`next`、`start`、`reconcile`、`verify`、`gate`、`review`、`status` 全部維持離線可用，判定 PASS、VERIFIED、DONE、readiness 與 Goal final-review 的規則一行都不經過模型。ForgePilot 自己仍然沒有 HTTP client，`internal/repository` 仍然只呼叫 `git`。

改變的那一半是：使用者明確執行 `forgepilot run` 時，Runner 可以以 executable 加 argument array 啟動一個指定的本機 coding CLI。那個 CLI 自己的網路行為屬於它，不屬於 ForgePilot；ForgePilot 不轉發、不代理、不解析模型回應以外的任何遠端狀態。

三個理由讓這個例外可以成立而不侵蝕原本的界線。其一，觸發點是使用者的明確指令，不是任何既有查詢的副作用——沒有一條既有命令因此多出「遠端不可用」這類失敗。其二，模型輸出在這裡不是 Evidence：Runner 收下的結構化結果只表示「這次實作結束」，PASS 仍然只能由 Candidate checkout 上的 canonical check 產生。其三，ForgePilot 不取得任何憑證——登入、額度、權限都在那個 CLI 自己的設定裡，ForgePilot 不讀、不寫、不保存。

**Consequences:** `forgepilot run` 需要本機已安裝並已登入的 coding CLI；ForgePilot 不自動安裝、不自動登入、不提高權限。權限或憑證問題是停止條件，不是可以繞過的設定步驟。預設測試不得依賴真實模型或網路：`--runtime fake` 的 subprocess adapter 存在就是為了這件事。模型輸出是未受信任內容，只能作為摘要與診斷材料。

**Falsified if:** `internal/repository/` 或 `internal/work/` 出現 `net/http` 或任何網路 client；或 `internal/cli/cli.go` 的 `next`、`status`、`start`、`reconcile`、`verify` 任何一條開始需要 agent runtime；或 `internal/agent/` 開始自行呼叫模型 API 而不是啟動使用者指定的本機 executable；或 `internal/work/evidence.go` 開始接受模型宣稱作為 Verification 結果。任一項發生，這條例外已經變成一般規則，上面三個理由需要重新回答。
