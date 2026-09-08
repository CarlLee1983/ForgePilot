# Seed — 操作摩擦（尚未成為 spec）

這不是 spec，也不是 milestone。它是一份給 `/grill-with-docs` 的起點：把 dbcli
dogfood 期間實際撞到的三件摩擦，連同已經查證過的程式碼現況與相關 ADR 記下來，
免得 grill 的第一個小時花在重查我已經查過的東西。

**來源**：2026-09-08，dbcli（`/Users/carl/Dev/CMG/Dbcli`）用 ForgePilot 駕駛
DBCLI-014 與 DBCLI-015 兩件工作的過程。兩件都已 DONE 並合併。

**證據等級**：下面每一條的「現況」都是這次讀 ForgePilot 原始碼確認的，不是回憶
或推論；「代價」是實際付過的，不是估計。開放問題是問題，不是提案。

---

## 摩擦一：FAIL 的 Evidence 沒有留下任何輸出

**症狀**：一次 `forgepilot verify` FAIL 之後，沒有任何地方可以讀到 `make verify`
到底哪一步壞了。要診斷就得自己重跑一次完整驗證。

**代價**：dbcli 的 `make verify` 一輪約 15 分鐘（6,700+ 測試、perf、六個整合服務
容器）。這次付了一次。

**現況**（已查證）：

- `internal/cli/verify.go:78` 拿到 `runOutput`，`:95-97` 只在 `Result != Pass`
  時 `fmt.Fprint(output, runOutput)` 印到 stdout。
- `internal/cli/verify.go:69-73` 的 `defer` 在函式結束時 `RemoveWorktree`，
  worktree 連同它產生的任何檔案一起消失。
- `internal/work/evidence.go:37-58` 的 `Evidence` struct 沒有任何欄位指向輸出：
  只有 `ExitCode`、`Result`、`Command`、`Revision`，加上 Human Review 專用的
  `Reviewer` / `Note` / `PR`。

也就是說輸出既不落檔，也不進 state；它只存在於那一次終端機的 scrollback。

**為什麼這是邊界決定而不是實作細節**：

- ADR-0001（Evidence 保存在單一 state snapshot 內）的 falsification 條件寫的是
  「單次 `state.json` 寫入的體積或延遲成為實際問題」。把完整 `make verify` 輸出
  塞進 snapshot，正是往那個條件推——dbcli 一次 verify 的輸出是數百 KB，而每次
  寫入都要重寫整份歷史。這條路等於主動去踩自己寫下的失效條件。
- 走外部檔案則要回答：檔案放哪（`.forgepilot/` 底下？）、誰負責清理、保留幾份、
  Evidence 上要不要有欄位指過去。最後一個問題本身撞 ADR-0003 的形狀——Work Item
  上不加 revision 欄位是刻意的，Evidence 上加一個「沒有任何規則讀取的路徑欄位」
  需要跟 ADR-0011 對 `pr` 欄位的處理方式對齊過。
- 也可以什麼都不存，改成讓 FAIL 保留 worktree 不刪。那要回答 `defer` 註解寫的
  那條承諾（清理不得否決結果）怎麼調整，以及誰在什麼時候刪它。

**開放問題**：輸出要不要是 Evidence 的一部分？如果是，是內容還是指標？如果不是，
那 FAIL 之後可重讀的東西由誰擁有？

---

## 摩擦二：`review approve` 也要求乾淨工作樹

**症狀**：任何平行的未提交工作都會擋住完成，唯一繞法是 `git stash`。錯誤訊息還
寫著 `before verifying`，但當下並不是在 verify。

**現況**（已查證）：

- `internal/repository/verification.go:19-27` 的 `EnsureClean`，訊息字串在 `:24`：
  `worktree is not clean; commit or stash the following before verifying`。
- 呼叫者有兩個：`internal/cli/verify.go:44` 與 `internal/cli/review.go:70`。
- `review.go:67-69` 的註解說明了理由：「A review must point at content a commit
  describes, exactly as a verification must」。

**現況的理由是成立的**，這不是漏寫——review 綁 revision，而髒的工作樹意味著那個
revision 描述不了被審的內容。問題在判準的範圍：verify 要在隔離 worktree 跑
`make verify`，所以整棵樹必須乾淨；review 只需要「被審的內容等於 HEAD 描述的
內容」，未追蹤的無關檔案是否真的破壞這件事，是可以分開問的。

注意 AGENTS.md 的地雷第一條：「前置檢查與它把關的交易必須用同一個判準」，而且這個
專案已經因為判準比交易寬鬆踩過兩次。任何放寬都要先答：放寬之後，review 綁的
revision 還描述得了被審的內容嗎。

**開放問題**：review 的乾淨判準要不要與 verify 分開？如果不分，錯誤訊息至少該說
出當下在做什麼；如果分，分在哪一條線上（未追蹤檔案？story_owned_paths 之外的
改動？），而那條線怎麼不變成「寬鬆的前置檢查」。

---

## 摩擦三（已在別處解決，記著免得重開）

**交付紀錄的無窮回歸**：handoff 記下驗證結果 → 產生新 commit → 該 Evidence 立刻
stale。dbcli 這邊的處理方式是 handoff 只寫結果、不複述 Evidence ID，權威留在
ForgePilot state。

這是使用慣例，不是 ForgePilot 的程式問題。列在這裡是因為它看起來像功能缺口
（「為什麼不能匯出一份 Evidence 摘要」），而那個方向會把回歸重新製造出來。

---

## 與這件事無關的一項

`main` 目前領先 `origin/main` 九個 commit（七份 Pages／手冊文件，加上
`0e7030e` module path 修正與 `884144e` help 不需 state）。在推上去之前，README
寫的 `go install github.com/CarlLee1983/ForgePilot/cmd/forgepilot@latest` 不會動。
這跟上面三件摩擦沒有關係，可以獨立先推。
