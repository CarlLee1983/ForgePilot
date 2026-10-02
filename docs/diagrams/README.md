# 架構圖

四張圖，內容以 `internal/` 底下的實際程式碼為準而非文件的理想版本。每一張都是自帶樣式與互動的單一 HTML，直接用瀏覽器開啟即可（GitHub 不會在網頁上渲染它們，請下載或 clone 後開啟）。

| 圖 | 回答的問題 | 規格 |
|---|---|---|
| [Work Item 狀態機](work-item-lifecycle.html) | 五個持久化狀態怎麼走，PASS 何時直接 DONE、何時進 REVIEW，Gate 為什麼不是其中之一 | [json](work-item-lifecycle.json) |
| [分層與依賴方向](package-layering.html) | 六個 package 誰依賴誰，Git 與子程序從哪裡進來 | [json](package-layering.json) |
| [一次受鎖的原子替換](state-transaction.html) | 交易邊界涵蓋什麼，為什麼完成與解鎖不能分開 | [json](state-transaction.json) |
| [verify 與 review approve 的順序](verify-and-approve.html) | 為什麼先回收孤兒才判斷能否開始，PASS 如何結算，完成如何由條件決定 | [json](verify-and-approve.json) |

每張圖內建深淺色切換、搜尋、焦點、關聯追蹤與導覽章節，右上角可匯出。

## 怎麼重新產生

圖由 `archify` skill 從同名的 `.json` 規格渲染，該工具不在本 repo 內。改圖時改 `.json`，再在 repository root 依序執行（`<type>` 是規格的 `diagram_type`：`lifecycle`、`architecture` 或 `workflow`；`archify` 指該 skill 的 `bin/archify.mjs`）：

```bash
node archify finalize <type> docs/diagrams/<name>.json docs/diagrams/<name>.html --quality showcase --repo-root . --json
node archify visual-check docs/diagrams/<name>.html --summary --require-provenance
```

`finalize` 要求 validate、deliver、strict check 與 browser-check 四道 gate 全過；`visual-check` 產生四張截圖與 contact sheet，再人工看過截圖，確認連線沒有互相穿越、標籤沒有遮蔽。`*.visual-check.*` 證據檔不進版控，`*.delivery.json`、`*.finalize*.json`、`*.browser-check.json` 這些 sidecar 也不要提交。目前的圖由 archify 3.0.1 產生。

文字契約仍以 [architecture.md](../architecture.md) 為準——圖是它的視覺化，不是它的替代品。兩者衝突時，以文件與程式碼為準，並把圖修正回來。
