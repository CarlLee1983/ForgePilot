# Mission: 用 ForgePilot 管自己的開發

> 教材是一份文件：[handbook.html](handbook.html)，涵蓋 `init` 到 `DONE` 的完整操作。分課的形式已取消。

## Why

ForgePilot 是被動的 DAG 帳本（[ADR-0040](../adr/0040-forgepilot-is-a-passive-dag-ledger.md)）：外部 Agent 依 `next` 的建議跑迴圈，ForgePilot 只判定下一個合法動作、保存綁定確切 Candidate 的 Verification Evidence。它已經實作完成、測試通過、文件齊全，但文件裡的使用流程是寫出來的，不等於用出來的。目標是把它接到真正的開發上——包括它自己的下一段開發——讓 Goal Plan 裡的第一個節點是真的，而不是 fixture。

## Success looks like

- 在真實 repository 上跑完 `init → goal import → next → start → verify → DONE → 下游 READY`（Goal 要求 Approval 時中間多一步 `review approve`），而且中途沒有靠 `git` 或手改 `state.json` 繞過任何一步
- 遇到需要人判斷的問題時，反射動作是開一個 Gate 而不是自己選一個繼續
- 看得懂 `status` 每一行在說什麼，尤其是「為什麼不能前進」那一行
- 累積一份「設計在真實使用中哪裡不順手」的清單，而不只是「用得會了」

## Constraints

- 學的人是這個專案的作者：名詞、ADR、程式碼都熟，**不需要重教概念**，重點在操作與手感
- 回覆與教材用繁體中文
- macOS 本機，Go 1.25.5，只有 CLI，沒有 UI 可以點

## Out of scope

- 重新設計產品或新增功能——這是使用課，不是開發課；後續工作來自 dogfood，開在 GitHub Issues
- 教 Go、Git 或 PraxisBound 的 Story schema
