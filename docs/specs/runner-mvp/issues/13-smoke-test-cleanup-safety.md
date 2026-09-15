# 13 — smoke 測試的檔案清理安全性

[12](12-smoke-opt-in-closure.md) 補上的入口測試留下兩個副作用，兩個都在測試側，
產品程式碼沒有參與。

## Root cause

`smoke_opt_in_test.go` 的 `requireAbsent()` 是一條「這個路徑不該存在」的斷言，
但它在路徑存在時先 `os.RemoveAll(path)` 才 `t.Fatalf`：

```go
case err == nil:
    _ = os.RemoveAll(path)
    t.Fatalf("%s was created without an opt-in; the export ran before the guard", path)
```

helper 不知道它拿到的路徑是不是本次測試建立的——它拿到的正是「本次測試沒有建立」
的路徑，這才是斷言的意思。所以那一行刪的是別人的東西，而且刪掉的同時也刪掉了
那則失敗訊息所指的證據。

第二個副作用是它被餵進去的路徑。呼叫端把相對 artifact 路徑寫成 checkout 內的固定名稱：

```go
relative := "smoke-opt-in-must-not-create-this"
...
requireAbsent(t, filepath.Join(projectRoot(t), relative))
```

使用者原本在 checkout 裡有同名項目時，測試會對不屬於自己的資料下判斷，
再加上上面那一行，就會把它刪掉。

訊息本身也說得比它檢查的多：「the export ran before the guard」是推論，
`os.Stat` 只告訴它那裡有東西，沒有告訴它東西是誰放的。

`os.Stat` 還有第三個問題：它會跟著 symbolic link 走。target 不存在的 dangling link
在 `Stat` 下回報 `IsNotExist`，helper 因此通過——但那條 link 確實是某個程序建立的，
守門讓它出現就是有副作用。

## Baseline 與版本

| | |
| --- | --- |
| 分支 | `fix/smoke-test-cleanup-safety`（獨立 worktree，未動使用者既有 checkout） |
| Baseline | `41ab93e47c1596041e79be7eb576b31b358d2911`（origin/main，PR #25 合併後） |
| 環境 | macOS 26.5.1（Darwin 25.5.0）、arm64、Go 1.25.5 |

平台就是 repository 宣稱支援的 macOS 本機檔案系統，沒有跨平台代跑。
`internal/` 一行都沒有動；schema、migration、CLI flag、runtime adapter 與相依沒有變動。

## 驗收條件

- `requireAbsent()` 是唯讀斷言：不刪除、不改名、不截斷，也不把刪除移到 defer／Cleanup。
- 路徑存在時回報失敗並完整保留原狀；不存在時通過；其他 filesystem error 據實回報為「無法判定」。
- 以 `os.Lstat` 觀察，symbolic link 本身算存在，包含 dangling link。
- 錯誤訊息只陳述觀察到的事，不宣稱該項目由 smoke export 建立。
- 入口測試不再在真實 checkout 預先建立或斷言固定名稱路徑；
  所有觀察目標都位於本次測試以 `t.TempDir()` 擁有的暫存空間。
- 「opt-in=0 + 無效相對 artifact 路徑」這個有辨識力的案例保留，仍然傳入相對路徑。
- 既有 opt-in 值矩陣、六個入口案例、PATH spy、pre-flight 與環境隔離不變。

## 改了什麼

### `smoke_opt_in_test.go`

`requireAbsent()` 移除 `os.RemoveAll`，改用 `os.Lstat`，並把失敗訊息縮回到觀察本身：

```go
switch _, err := os.Lstat(path); {
case err == nil:
    t.Fatalf("%s: %s; it has been left untouched", path, requireAbsentFoundMessage)
case os.IsNotExist(err):
default:
    t.Fatalf("whether %s exists could not be decided: %v", path, err)
}
```

`requireAbsentFoundMessage` 是常數，讓回歸測試認得出真正的 helper 在報錯，
而不是接受任何非零退出碼。

新增 `privateRelativeArtifactPath()`：用 `t.TempDir()` 建一個本次 subtest 擁有的父目錄，
在其下指定一個尚不存在的 private target，再以
`filepath.Rel(childWorkingDir, privateTarget)` 產生要傳進去的相對路徑，
並確認它確實不是絕對路徑。傳入的路徑與事後斷言的路徑由同一個呼叫一起回傳，
兩者不會走岔。

兩端在做 `Rel` 之前都先經過 `filepath.EvalSymlinks`。`filepath.Rel` 是純詞法運算，
它數出來的 `..` 深度假設就是 kernel 會走的深度；而 `os.Getwd` 可能回報 shell
走進來的那條邏輯路徑，所以經由 symlink 進入的 checkout 會讓子程序解析到與
斷言不同的位置。先實體化才讓兩者由構造上就是同一個地方——
code review 抓到這一點（見下方回歸章節的第三次實測）。匯出 helper 拒絕相對路徑的性質沒有變，所以那個案例的辨識力保留；
變的只是它解析到的位置，從 checkout 移到測試自己建立的地方。

沒有新增通用的 filesystem sandbox 或路徑管理框架；child working directory 沒有動，
`projectRoot`、fixture 與編譯流程照舊。

### `require_absent_test.go`（新增，測試專用）

五條 `TestRequireAbsent` 前綴的回歸測試，每一條都呼叫**正式的** `requireAbsent()`：

| 案例 | 斷言 |
| --- | --- |
| `TestRequireAbsentPassesForAMissingPath` | helper 通過；執行後路徑仍不存在，暫存目錄裡沒有多出任何項目 |
| `TestRequireAbsentKeepsAnExistingFile` | helper 失敗；檔案仍在，權限與內容逐 byte 相同 |
| `TestRequireAbsentKeepsAnExistingDirectory` | helper 失敗；sentinel 檔、巢狀子目錄與其內容全部不變 |
| `TestRequireAbsentKeepsASymbolicLinkToAnExistingTarget` | helper 失敗；link 本身、`Readlink` 結果與 target 內容不變 |
| `TestRequireAbsentReportsADanglingSymbolicLink` | helper **失敗**（不得因 target 不存在而通過）；link 不變，target 仍不存在 |

所有測試資料，包含 symlink target，都在父測試的 `t.TempDir()` 底下，由 `t.TempDir` 自行清理；
沒有任何清理函式接收待檢查路徑後刪除它。資料保留的斷言在父測試清理之前完成。

辨識力靠一個最小的 subprocess helper。父測試建資料，子程序只跑
`-test.run '^TestRequireAbsentHelperProcess$'` 並呼叫真的 `requireAbsent()`，
父測試再核對四件事：目標測試確實 **RUN** 過、**不是 SKIP**、恰好印出一條 PASS 或一條 FAIL、
且退出碼與該判定一致；失敗時還要求訊息含 `requireAbsentFoundMessage` 與該路徑本身。
只看「退出碼非零」不算——編譯失敗、`-test.run` 沒選中、timeout 都會非零，
但都不是 `requireAbsent` 正確報錯。子程序有 60 秒 deadline，
`-test.timeout` 一併傳下去（直接執行編譯後的 test binary 預設沒有 timeout）。

待觀察的路徑經由 `-forgepilot.require-absent-target` **flag** 傳給子程序，不用環境變數。
flag 不會被繼承，所以開發者 shell 裡的任何值都無法讓輔助入口
`TestRequireAbsentHelperProcess` 在一般 `go test` 下進入預期失敗模式（flag 未給就 `t.Skip`），
`exec.Cmd` 對 `Env` 去重保留最後一筆的行為也不可能讓繼承值蓋掉父測試指定的目標。
子程序的環境經過 `inheritedEnvironment()` 剝除 smoke 設定，再把
`FORGEPILOT_CODEX_SMOKE=0` 明確附在最後釘死；這條回歸不啟動任何模型。

## 回歸與原始問題的對應

在隔離的暫存資料上做兩次單點 mutation，兩次都只改 `requireAbsent()` 一行，
資料一律在 `t.TempDir()` 內，沒有碰真實 checkout、使用者檔案或歷史 evidence。

**mutation 1 — 把 `_ = os.RemoveAll(path)` 加回去：**

| 案例 | 結果 |
| --- | --- |
| `KeepsAnExistingFile` | **FAIL** — `smoke-opt-in-must-not-create-this was removed (was file 0644 "data the test did not create and must not destroy\n")` |
| `KeepsAnExistingDirectory` | **FAIL** — 5 個 entry 全數 removed，含 `evidence/nested/deeper/record.json` |
| `KeepsASymbolicLinkToAnExistingTarget` | **FAIL** — `link was removed` |
| `ReportsADanglingSymbolicLink` | **FAIL** — `dangling was removed` |
| `PassesForAMissingPath` | PASS — 沒有東西可刪，這一案守的是相反方向 |

**mutation 2 — 把 `os.Lstat` 換回 `os.Stat`：**
只有 `ReportsADanglingSymbolicLink` 變紅，訊息是
`requireAbsent accepted .../dangling, which exists`。
其餘四案照樣綠——這正是 `Lstat` 那一條要求存在的理由。

兩次 mutation 都已還原，交付的樹上沒有留下它們。

**第三次實測 — symlink checkout 下的詞法／實體分岔**（code review 的 HIGH）：
在 `linkedRoot -> real/deeper` 的佈局下，以一支程式重現子程序的行為
（`chdir` 到父測試交給 `exec.Cmd` 的 working directory，再建立那個相對路徑）：

| 版本 | 產出的相對路徑 | 結果 |
| --- | --- | --- |
| `EvalSymlinks` 之前 | `../../tgt/smoke-opt-in-must-not-create-this` | 解析到不存在的目錄；斷言的位置不是實際落點，守門回歸時會無聲通過 |
| `EvalSymlinks` 之後 | `../../../tgt/smoke-opt-in-must-not-create-this` | 斷言的路徑就是實際出現的那一個 |

另外兩項 code review 的 WARNING（繼承環境變數蓋掉目標路徑、
輔助入口在既有環境變數下於一般 `go test` 進入失敗模式）
以改用 flag 傳遞一併關閉；review 的重現指令
`FORGEPILOT_TEST_REQUIRE_ABSENT_PATH=/etc go test -run TestRequireAbsent .`
在修正後五條案例全綠、輔助入口 SKIP。

## 離線驗證

一律明確移除三個 smoke 變數，避免繼承呼叫者設定。指令、平台與退出碼見交付報告。

## 真實 Codex smoke

**NOT RUN — no explicit quota authorization for this task.**

本輪的開工指令明確表示不授權透過測試啟動真實 Codex 或消耗真實模型額度，
所以沒有執行。沒有 TESTED_SHA、沒有 Run ID、沒有新的證據目錄。
[11](11-real-codex-smoke-acceptance.md) 與 [12](12-smoke-opt-in-closure.md)
的歷史驗收結果沒有被改寫。

## 已知限制與未驗收範圍

- 修正只在測試側。`internal/` 未被觸及，Runner loop、recovery、ownership、
  budget 與 Goal review 都沒有碰。
- 沒有新的架構決策，因此沒有新增 ADR。
- 入口測試的六個案例、`runner_codex_smoke_test.go` 的驗收斷言都沒有修改或放寬。
- 回歸測試每執行一條案例會跑一次子程序，`compileRootTestBinary` 在五條案例各編一次
  本套件的 test binary（實測整組約 2.9 秒）。它會拖長 `make verify`，這是它的成本。
- 驗收只在一台 macOS arm64 機器上做。沒有其他平台的結果，也不代跑。
- `requireAbsent` 對 `Lstat` 回傳非 `IsNotExist` 錯誤的路徑仍只報「無法判定」，
  這是刻意的：它沒有材料去分辨權限問題與洩漏。
