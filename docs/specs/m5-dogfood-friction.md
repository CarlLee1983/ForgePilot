# M5 dogfood 摩擦紀錄

**日期**：2026-09-08　**repo**：ForgePilot 自身　**commit**：`47acc92`
**做法**：M5 交付完成後，用 ForgePilot 自己駕駛這次工作——一個 Goal、兩個 Work Item
（WI-001 review 訊息、WI-002 verification log），走完 init → goal create → work add →
start → verify → review approve → goal complete。

## 驗證到的事（M5 的交付在真實流程裡成立）

- **#4 的訊息修正生效**：同一個髒工作樹，`verify` 說 `commit or stash the following
  before verifying`，`review approve` 說 `before reviewing`，兩者都列出 `?? specs/`，格式一致。
- **log 串流與呈現契約成立**：兩次 verify 各自先印 `Log: <絕對路徑>`，結果行 `EV-001 PASS
  at <sha>` 不重複路徑；log 內容是 canonical 檢查的純輸出（`go vet` / `go test` 逐行），沒有 header。
- **log 不進版控**：兩份 log 都在 `.forgepilot/logs/` 底下，跑完 `git status` 乾淨。
- **不自動清理**：WI-002 的執行沒有動到 WI-001 留下的那份。
- **Evidence 上沒有指向 log 的欄位**：完成後的 `state.json` 裡 `log_path` 出現 0 次
  ——它只活在 `current_run`，隨執行結束消滅（ADR-0012）。

## 這一輪新撞到的摩擦

### 摩擦一：story 只能放在 `specs/stories/`，而這個 repo 的規格不在那裡

`work add --story` 經 `repository.ValidateStory` 硬性要求路徑落在 `<root>/specs/stories/`
底下。ForgePilot 自己的規格散在 GitHub issue 與 `docs/specs/`，目錄根本不存在，第一次
`work add` 直接失敗：

```
forgepilot: resolve story directory: lstat /Users/carl/Dev/CMG/ForgePilot/specs: no such file or directory
```

訊息說的是 `lstat` 的內部失敗，沒說「story 必須放在 specs/stories/」——那個規則只寫在
另一條錯誤分支的字串裡，使用者要讀原始碼才知道。要繼續 dogfood 就得為了工具本身新建
一個目錄與兩份檔案（`47acc92`）。

### 摩擦二：把 story 加進來這件事本身讓工作樹變髒，擋住 verify

新增 story 檔案 → `?? specs/` → `verify` 被拒。ForgePilot 要求的輸入必須先 commit 才能
用 ForgePilot 驗證，開場就是一次「為了用工具而先繞過工具」。這跟 seed 記載的摩擦二
同源，但這次是**工具自己的必要輸入**造成的，不是平行工作造成的。

### 摩擦三：兩個 Work Item 的 Evidence 指向同一個 revision

M5 已經完成後才回頭補跑，四筆 Evidence 全部落在 `47acc92`，看不出兩件工作是分開交付的。
事後補跑的紀錄與邊做邊跑的紀錄在 state 裡長得一模一樣，state 本身分辨不出來。

## 未解的問題

- 摩擦一該往哪邊修：放寬 story 的位置限制（讓專案自訂），或讓錯誤訊息直接說出規則？
  前者動的是契約，後者只動措辭——但只改措辭，ForgePilot 就仍然規定所有專案的目錄佈局。
- 摩擦二有沒有不放寬乾淨判準的解法。判準本身在 grill 中被明確否決放寬。
