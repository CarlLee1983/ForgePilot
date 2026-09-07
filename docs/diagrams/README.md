# 架構圖

四張圖，內容以 `internal/` 底下的實際程式碼為準而非文件的理想版本。每一張都是自帶樣式與互動的單一 HTML，直接用瀏覽器開啟即可（GitHub 不會在網頁上渲染它們，請下載或 clone 後開啟）。

| 圖 | 回答的問題 | 規格 |
|---|---|---|
| [Work Item 狀態機](work-item-lifecycle.html) | 六個狀態怎麼走，Gate 為什麼不是其中之一 | [json](work-item-lifecycle.json) |
| [分層與依賴方向](package-layering.html) | 四個 package 誰依賴誰，外部世界從哪裡進來 | [json](package-layering.json) |
| [一次受鎖的原子替換](state-transaction.html) | 交易邊界涵蓋什麼，為什麼完成與解鎖不能分開 | [json](state-transaction.json) |
| [verify 與 review approve 的順序](verify-and-approve.html) | 為什麼先回收孤兒才判斷能否開始，完成如何由條件決定 | [json](verify-and-approve.json) |

每張圖內建深淺色切換、搜尋、焦點、關聯追蹤與導覽章節，右上角可匯出。

## 怎麼重新產生

圖由 `archify` skill 從同名的 `.json` 規格渲染，該工具不在本 repo 內。改圖時改 `.json`，再以 showcase 品質等級重新 deliver 並跑 visual-check；產生的 `*.visual-check.*` 證據檔不進版控。

文字契約仍以 [architecture.md](../architecture.md) 為準——圖是它的視覺化，不是它的替代品。兩者衝突時，以文件與程式碼為準，並把圖修正回來。
