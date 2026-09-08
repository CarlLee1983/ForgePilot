# ForgePilot 使用資源

這個主題的特殊之處：**最高信任的來源就是這個 repository 本身**。沒有第三方教材，也不該有——任何外部文章都會比程式碼舊。

## Knowledge

- [CONTEXT.md](../../CONTEXT.md)
  詞彙的唯一定義處，每個詞附「不是什麼」。用於：任何時候名詞開始鬆動。
- [docs/architecture.md](../architecture.md)
  責任邊界、資料模型、狀態規則、四個階段的開工前定案。用於：判斷某個行為是刻意的還是 bug。
- [docs/adr/](../adr/README.md)
  11 份不易反轉的決定與其失效條件。用於：碰到「這裡為什麼不能這樣」的時候。
- [docs/diagrams/](../diagrams/README.md)
  狀態機、分層、交易邊界、verify 與 approve 的順序。用於：需要先看懂形狀。
- `internal/cli/*.go`
  指令的實際行為與錯誤訊息。用於：文件與實際不符時——**以程式碼為準**。
- `integration_test.go`
  每一條使用流程都有一個以獨立 process 跑真實 git repository 的測試。用於：想知道某個情境到底會怎樣，最快的答案是找對應的測試。

## Wisdom (Communities)

這是一個單人專案，沒有使用者社群，短期內也不會有。可替代的「真實回饋」來源：

- **拿它管一段真實的工作**。這是唯一能證偽設計的方法，也是 MISSION 的內容本身。
- **`docs/learn/learning-records/`**：把每次「用起來不順手」的地方記下來。這些紀錄是未來 M5 的輸入。

## Gaps

- 沒有第二個使用者，因此沒有任何「別人怎麼用」的訊號。所有 usability 判斷都來自作者一個人，這是已知的偏誤來源。
