# 12 — 真實 Codex smoke 的啟用條件，以及守住它的回歸測試

[11](11-real-codex-smoke-acceptance.md) 留下一個沒有被測試守住的判斷。
啟用真實 Codex smoke 的檢查寫成「非空即啟用」：

```go
if os.Getenv(SmokeVariable) == "" {
    t.Skipf("set %s=1 to drive the real Codex CLI", SmokeVariable)
}
```

`FORGEPILOT_CODEX_SMOKE=0` 因此**會**啟動真實模型。`false`、`off`、`no`、`2`、
前後帶空白的 `1` 也一樣。也就是說一個人明確打出「關掉」的那些拼法，
剛好都是開啟的拼法——而這個開關的另一側是會消耗額度、無法重播的一輪執行。

本票只做兩件事：把判斷改成完全等於 `1`，以及補上證明**實際入口**受到保護的測試。
沒有新的架構決策，因此沒有新的 ADR；產品程式碼一行都沒有動。

## Baseline 與版本

| | |
| --- | --- |
| 分支 | `fix/codex-smoke-opt-in-closure`（獨立 worktree，未動使用者既有 checkout） |
| Baseline | `3b5332ce26ae75e8bfec9e5177f965b679ca672b`（origin/main，PR #24 合併後），working tree 乾淨 |
| 環境 | macOS 26.5.1（Darwin 25.5.0）、arm64、Go 1.25.5、codex-cli 0.154.0 |

平台就是 repository 宣稱支援的 macOS 本機檔案系統，沒有跨平台代跑。

## 改了什麼

### `runner_codex_smoke_test.go`

判斷改為等價於 `!= "1"`，並抽出一個測試側的單行述詞讓值矩陣能逐一陳述契約：

```go
func smokeOptedIn(value string) bool { return value == "1" }
```

`if !smokeOptedIn(os.Getenv(SmokeVariable))` 是入口唯一的改動。
沒有寬鬆的 boolean parser，沒有 `TrimSpace` 後放行——
`" 1 "` 是 SKIP，因為一個會被空白改變意思的開關無法在腳本之間安全傳遞。
沒有新增產品設定、公開 API 或外部相依；`internal/` 未被觸及。

`FORGEPILOT_SMOKE_ARTIFACT_DIR` 的角色不變：它只決定證據存放位置，
不是第二種啟動真實模型的方式。

### `smoke_opt_in_test.go`（新增，測試專用）

兩條測試，回答兩個不同的問題。

**`TestSmokeOptInAcceptsOnlyTheExactValueOne`** 逐值陳述契約：
未設定、空字串、`0`、`false`、`FALSE`、`off`、`no`、`true`、`yes`、`2`、
`" 1 "`、`"1\n"` 全部 SKIP，只有 `1` 通過。它經由 `os.Getenv` 讀取而不是直接
餵字面值給述詞，因為「未設定」與「設定為空字串」對述詞是同一個參數、
對人是兩件事。環境變數一律走 `t.Setenv`，所以開發者自己的設定既不會被採用、
也不會在測試後消失。

**`TestRealCodexSmokeConsultsTheOptInBeforeAnySideEffect`** 回答矩陣答不了的問題：
真正的入口在那條契約後面嗎。它用 `go test -c` 編一次本套件的測試 binary，
再以各自的環境跑 `-test.run '^TestCodexSmokeDrivesDependentWorkToTheGoalReviewBoundary$'`，
每一輪查三件互相獨立的事：

- 目標測試**回報 SKIP**——退出碼 0 也可能只是它根本沒被選中。
- Codex 沒有被呼叫。PATH 最前面放一個叫 `codex` 的 spy，它只記錄呼叫、拒絕執行、
  以 91 結束，不 fallback 到真的 CLI。本機確實裝有 codex-cli 0.154.0，
  所以這層遮蔽是有作用的，不是形式。
- 證據匯出該寫的地方什麼都沒寫。這一項才是「判斷在副作用之前」的實測。

五個未啟用案例：`0`、`false`、未設定 opt-in 但指定 artifact 目錄、
`" 1 "` 加 artifact 目錄，以及有辨識力的那一個——
`0` 加一個**無效的相對路徑** artifact 目錄。匯出 helper 會拒絕相對路徑，
所以舊程式在這個環境下會進到 `openSmokeExport` 並在那裡 `t.Fatalf`；
只有把 opt-in 判斷放在匯出之前，才可能是 SKIP。

第六個案例把三項斷言全部反過來：`FORGEPILOT_CODEX_SMOKE=1`、不指定 artifact 目錄，
要求目標測試**不是** SKIP、spy log 確實記到一次 `codex` 呼叫、退出碼非 0。
它不是錦上添花，是前五案共同的盲點：把入口換成無條件 `t.Skip` 之後，
述詞矩陣照樣綠、五個未啟用案例照樣綠，smoke test 從此永遠不跑而沒有一條斷言變紅。
它同時是唯一一個示範「spy 真的抓得到呼叫」的案例——否則另外五案的
「Codex 沒有被呼叫」是在檢查一個從未被觸發過的機制。
派送前有一道 pre-flight 以子程序自己的 PATH 解析 `codex`，
沒有落在 spy 上就直接 fail，不讓一輪可能觸及真實模型的執行開始；
spy 在 runtime version probe 上就拒絕，所以 Runner 在規劃 session 之前就停，
不消耗額度（實測 0.8 秒、退出碼 1）。

每個子程序都有明確的 deadline（未啟用案例兩分鐘，反向對照五分鐘），
`-test.timeout` 也一併傳給子程序：直接執行編譯後的 test binary 預設**沒有** timeout，
若守門回歸讓那一輪真的開跑並卡在等認證或等 stdin，這條測試必須變紅而不是掛住。

子程序繼承的環境會先剝掉 `FORGEPILOT_CODEX_SMOKE`、
`FORGEPILOT_SMOKE_ARTIFACT_DIR` 與 `FORGEPILOT_FAKE_AGENT`，
所以真正 opt-in 的開發者跑這條測試也不會意外花額度。
沒有新增通用的 subprocess 測試框架——三個 helper 都在這個檔案裡，只給這條測試用。

## 新增測試與原始 bug 的對應

在已隔離真實 CLI 的環境把判斷暫時還原成「只排除空字串」，跑一次新增測試：

| 案例 | 舊判斷下的結果 |
| --- | --- |
| `FORGEPILOT_CODEX_SMOKE=0` | **FAIL**——目標測試實跑，Runner 退出碼 1 |
| `FORGEPILOT_CODEX_SMOKE=false` | **FAIL**——同上 |
| 未設定 opt-in，設定 artifact 目錄 | PASS——舊判斷在未設定時本來就 SKIP；這個案例守的是另一件事：artifact 變數不得成為第二種啟用方式 |
| `" 1 "` 加 artifact 目錄 | **FAIL**——不只實跑，還匯出了 28 個檔案的證據 |
| `0` 加無效相對路徑 artifact 目錄 | **FAIL**——在 `openSmokeExport` 就爆，證明舊判斷位於副作用之後 |
| `1`（反向對照） | PASS——舊判斷在 `1` 時也會啟動，這一案守的是相反方向的回歸 |

值矩陣那條測試在舊判斷下不會變紅（它測的是述詞本身，述詞是本輪新增的），
所以**入口測試才是抓住原始 bug 的那一條**——這也正是它不能只測一個脫離入口的 helper 的理由。

反向對照則是對另一種回歸做的同一件事。把入口換成無條件
`t.Skip("temporarily disabled")` 再跑一次：只有
`exact opt-in reaches the runtime` 變紅，訊息是
「skipped with FORGEPILOT_CODEX_SMOKE="1"; the opt-in it documents no longer starts it」。
沒有它，那個變更會安靜地通過整套回歸。

兩次還原期間都沒有任何一輪碰到真實模型：spy 在 PATH 最前面，
Runner 一律在解析 runtime 時就失敗。
暫時還原的程式碼已全部恢復，交付的樹上沒有留下它。

## 離線驗證

一律明確移除兩個 smoke 變數，避免繼承呼叫者設定。

```
env -u FORGEPILOT_CODEX_SMOKE -u FORGEPILOT_SMOKE_ARTIFACT_DIR \
    go test -count=1 -run 'TestSmokeOptIn|TestRealCodexSmokeConsults' .   → exit 0
env -u FORGEPILOT_CODEX_SMOKE -u FORGEPILOT_SMOKE_ARTIFACT_DIR make verify
                                                                      → exit 0（root 套件 247.6s）
env -u FORGEPILOT_CODEX_SMOKE -u FORGEPILOT_SMOKE_ARTIFACT_DIR \
    go test -race -count=1 ./...                                      → exit 0（root 套件 262.0s）
```

既有的 smoke 斷言一條都沒有刪除或放寬：VERIFIED 而非 DONE、
不得有 Review Evidence、Goal 維持 ACTIVE、停在 `AWAITING_GOAL_REVIEW`、
readiness 與 Evidence IDs 一致、不留 Worker 或未解除的 Pending、
不留 OPEN Gate、每張工作一次 attempt 的期待——全部原封不動。
`git diff` 對 `runner_codex_smoke_test.go` 只有那一行判斷與其上的註解。

## 真實 Codex smoke

**NOT RUN — awaiting explicit quota authorization。**

本輪的開工指令明確表示它不授權消耗真實模型額度，所以沒有執行。
沒有 TESTED_SHA、沒有 Run ID、沒有新的證據目錄、沒有 manifest 核對、
沒有 bundle 還原——這些欄位留空，不是待補的形式，是本輪確實沒有做的事。

因此本票**不得**被讀成「真實驗收全部完成」。它的結論是：
啟用條件已修正、離線回歸通過，而修正後的入口尚未在真實模型下跑過一次。
[11](11-real-codex-smoke-acceptance.md) 的那一輪（run `run-20260915t034856-f0e621`，
於 `2deaf14`）是舊版本的結果，不是本輪的結果，也沒有被改寫。

## 實際受測版本與最終提交版本

沒有真實 smoke，所以沒有「實際受測 SHA」可言。
離線回歸是在本票的最終提交內容上跑的——同一棵樹，沒有跑完再改。

## 已知限制與未驗收範圍

- 修正後的 opt-in 入口**沒有**在真實模型下走過一次。
  反向對照證明 `1` 會啟動那一輪並觸及 runtime，但它到的是 spy，不是模型；
  session 規劃、handoff 與驗證之後的整條路徑本輪沒有實測。
- 本票沒有碰 Runner loop、recovery、ownership、預算、schema、
  runtime adapter 或任何產品 CLI flag，也沒有把真實模型測試加進預設 CI。
- 不是 Goal-level review：沒有 Goal approval、沒有 Goal Evidence、
  沒有 Goal completion。
- 入口測試每次執行會 `go test -c` 編一次本套件，並在六個子程序裡各跑一次 binary；
  反向對照那一案還會建一次 fixture（含 `go build ./cmd/forgepilot`）。
  實測整條測試約 1.5 秒，但它確實會拖長 `make verify`，這是它的成本。
- Codex spy 只遮蔽 PATH 上的裸名 `codex`（`internal/agent/codex.go` 只在
  `Command` 為空時走 `LookPath`，而 smoke 的 argv 沒有指定執行檔）。
  哪天 smoke test 改以其他方式指定執行檔，這層遮蔽會靜默失效；
  pre-flight 檢查的是 PATH 解析，不是 argv。
- 驗收只在一台 macOS arm64 機器上做。沒有其他平台的結果，也不代跑。
