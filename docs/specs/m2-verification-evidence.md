# M2 — Verification Evidence

前置決策已全部定案，見 [architecture.md](../architecture.md#m2-開工前定案已完成) 與 `docs/adr/0001`–`0004`。本文件是實作規格。

## Problem Statement

M1 之後，使用者可以建立 Goal、排入 Work Item、選出下一件並開始執行，但流程到 RUNNING 就斷了。產品無法表達「這件工作已經通過檢查」，也沒有任何地方記錄檢查發生過。

具體的困擾有三個。使用者看著一件 RUNNING 的工作，無從得知它有沒有被驗證過、驗的是哪一版程式碼。即使記得自己跑過 `make verify` 並通過，那個結果只存在於終端機的捲動紀錄裡，換一個 process 就消失，更不可能成為 M3 判斷能否進入 DONE 的依據。而就算把結果記下來，只要之後又 commit 了新東西，舊的通過紀錄是否還算數，使用者也沒有辦法判斷——這正是最危險的一種錯誤，因為它看起來像是通過了。

## Solution

新增 `forgepilot verify <work-id>`。它把受管理專案在某個確切 commit 上的 canonical 檢查跑完，並把結果保存成 Evidence。

執行時，ForgePilot 先確認工作樹乾淨，解析出目前的 HEAD SHA，在一個隔離的 detached worktree 裡對那個 SHA 執行專案自己的 `make verify`，然後把 repository、Work Item、ForgeFlow Story、完整 SHA、實際執行的 command、exit code、result 與時間一併 append 成一筆 Evidence。PASS 讓工作進入 REVIEW，FAIL 讓它退回 RUNNING。無論成敗，Evidence 都保留，既有的不被覆寫。

`status` 因此能顯示每件工作最新一筆 Evidence 的 result 與 SHA。當那個 SHA 不再等於目前的 HEAD，`status` 標示為 stale——舊的 PASS 保留為歷史，但不適用於現在。

M2 不提供 DONE。REVIEW 是這個階段的終點。

## User Stories

1. 身為使用者，我想對一件 RUNNING 的 Work Item 執行 `forgepilot verify`，以便讓專案自己定義的檢查決定它是否通過，而不是由我或 Agent 自己宣稱。
2. 身為使用者，我想讓驗證跑的是 repository 的 `make verify`，以便驗證契約由專案擁有，而不是由 ForgePilot 決定要跑什麼。
3. 身為使用者，我想在驗證通過後看到工作進入 REVIEW，以便知道它已經完成工程檢查、正在等待後續的人工審查。
4. 身為使用者，我想在驗證失敗後看到工作退回 RUNNING，以便直接回到修復狀態而不需要手動改狀態。
5. 身為使用者，我想讓失敗的驗證同樣留下 Evidence，以便日後能看出這件工作曾經在哪個版本上失敗過。
6. 身為使用者，我想讓每一筆 Evidence 都帶著完整的 commit SHA，以便「通過了什麼」永遠指向一個確切的版本而不是「最新版」。
7. 身為使用者，我想讓 Evidence 記錄實際執行的 command 與 exit code，以便日後能重現當時的判斷依據。
8. 身為使用者，我想讓既有的 Evidence 永不被覆寫，以便驗證歷史是可以累積閱讀的紀錄而不是一個會被蓋掉的欄位。
9. 身為使用者，我想在工作樹不乾淨時被明確拒絕執行驗證，以便不會拿到一個其實沒有涵蓋我未提交變更的 PASS。
10. 身為使用者，我想讓「乾淨」也把未追蹤檔案算進去，以便新增但還沒 commit 的實作檔案不會被驗證悄悄略過。
11. 身為使用者，我想在被拒絕時看到明確說明是工作樹不乾淨，以便知道下一步是 commit 而不是去查 ForgePilot 的問題。
12. 身為使用者，我想讓驗證在隔離的 worktree 執行，以便驗證期間我或 Agent 繼續編輯檔案不會污染那次驗證的結果。
13. 身為使用者，我想讓隔離的 worktree 在驗證結束後自動清除，以便不會在專案裡累積殘骸。
14. 身為使用者，我想讓失敗的驗證也清除 worktree，以便磁碟不會隨著失敗次數無限成長。
15. 身為使用者，我想讓 ForgePilot 在每次驗證前清掉先前被強制終止所留下的 worktree 註冊，以便 Git 不會累積 prunable 的殘骸。
16. 身為使用者，我想在受管理專案沒有 `make verify` 時被拒絕執行且不留下任何 Evidence，以便「這個專案還沒有驗證契約」不會被誤記成一次驗證失敗。
17. 身為使用者，我想在驗證進行中從另一個終端機執行 `status`，以便看到那件工作正在 VERIFYING 而不必猜。
18. 身為使用者，我想讓 `status` 與 `next` 在驗證進行中依然可用，以便長時間的驗證不會把整個工具鎖死。
19. 身為使用者，我想讓同一件 Work Item 不能被同時驗證兩次，以便兩次執行不會互相覆寫彼此的結論。
20. 身為使用者，我想讓不同的 Work Item 可以同時驗證，以便平行進行的工作不會互相排隊等待。
21. 身為使用者，我想在驗證跑到一半按下 Ctrl-C 之後，下一次執行 `verify` 時看到上一次被記為 INTERRUPTED，以便中斷不會被偽造成失敗或通過。
22. 身為使用者，我想讓被中斷的工作退回 RUNNING，以便我可以直接重跑而不必手動修復狀態。
23. 身為使用者，我想讓 `status` 在跑者已經消失時顯示這個事實，以便我知道那個 VERIFYING 是殘留而不是正在進行。
24. 身為使用者，我想讓 `status` 與 `next` 在做這件事時完全不改寫 state，以便查詢指令永遠是安全的。
25. 身為使用者，我想在 `status` 看到每件工作最新一筆 Evidence 的 result 與 SHA，以便一眼判斷它現在的驗證狀況。
26. 身為使用者，我想在最新 Evidence 的 SHA 不等於目前 HEAD 時看到 stale 標示，以便不會把舊的 PASS 當成現在仍然成立。
27. 身為使用者，我想讓 stale 本身不改變任何狀態，以便新增一個 commit 不會突然把一堆工作打回去。
28. 身為使用者，我想能對已經 REVIEW 的工作再次執行 `verify`，以便在新的 commit 上重新取得適用的 Evidence。
29. 身為使用者，我想能對同一個 SHA 重複執行 `verify`，以便驗證一次僥倖通過的結果是否穩定。
30. 身為使用者，我想在 M2 binary 讀到 M1 留下的 state 時被明確拒絕而不是看到損毀錯誤，以便知道該做的是升級而不是重建。
31. 身為使用者，我想透過明確的 `forgepilot migrate` 來升級 state，以便這個單向動作是我主動做的，而不是在某次無關的指令中悄悄發生。
32. 身為使用者，我想讓 `migrate` 在升級前自動備份，以便升級出錯時我還有原本的資料。
33. 身為使用者，我想讓 `migrate` 在備份檔已存在時拒絕執行，以便先前的備份不會被無聲蓋掉。
34. 身為使用者，我想讓 `migrate` 對已經是新版的 state 回報「已是最新」並正常結束，以便重複執行是安全的。
35. 身為使用者，我想讓升級不會遺失任何既有的 Goal、Work Item 或依賴關係，以便 M1 累積的佇列可以直接沿用。
36. 身為使用者，我想在任何失敗情況下都留下可讀的 state，以便驗證流程出錯不會連帶弄壞我的工作佇列。

## Implementation Decisions

**新增的指令只有兩個**：`forgepilot verify <work-id>` 與 `forgepilot migrate`。不新增 Evidence 查詢指令，不新增 `--json`，不新增任何 status mutation。`status` 擴充輸出，`next` 的選取規則不變。

**`internal/work` 保持純粹。** M2 的所有判斷邏輯都是不碰 filesystem、Git 或 subprocess 的函式，沿用 M1 既有的模式：外部事實透過參數傳入，就像 `now time.Time` 那樣。傳入的新事實是一次 Verification 的結果（SHA、command、exit code、result）與「該 Work Item 的 verify 鎖是否仍被持有」這個布林。`internal/work` 不知道 Git 存在。

**Schema 從 1 升到 2，只有新增。** 根層加入 Evidence 陣列與 Evidence 的 ID 配發計數；Work Item 加入 Verification Run，閒置時為空。沒有欄位被刪除，也沒有既有欄位改變語意。既有的版本檢查是嚴格相等，因此 M2 binary 同樣拒讀 v1——升級只由 `migrate` 觸發，備份為固定命名的檔案，備份已存在時拒絕執行，已是 v2 時回報並以成功結束，不提供 downgrade。

**Evidence 保存在同一份 state snapshot 內**（ADR-0001），與 Work Item 共用同一次受鎖的原子替換。因此不存在「Evidence 寫了但狀態沒更新」的中間態，crash consistency 不是需要處理的問題。Evidence ID 沿用 Work Item 的配發方式，由受鎖操作發出遞增序號。Evidence 帶 type 欄位，M2 只有 verification 一種，但欄位現在就存在——不可變歷史缺少辨別欄位是事後補不回來的。

**Evidence 的 result 有三個值**：PASS、FAIL、INTERRUPTED。INTERRUPTED 表示未產生結果，不是失敗。任何情況下都不得以推斷產生 PASS 或 FAIL。

**Work Item 不保存 target_revision**（ADR-0003）。revision 只存在於 Evidence 與 Verification Run 之中。

**`internal/repository` 承接所有 Git 與 subprocess 操作**：判斷工作樹是否乾淨、解析 HEAD、建立與移除隔離 worktree、執行 canonical 檢查。它是薄的一層，錯誤直接向上傳遞，不做決策。這一層不定義 port 介面也不被 mock，理由見 Testing Decisions。

**驗證在 detached worktree 執行**（ADR-0002）。以完整 SHA 加 detach 建立，路徑在 state 目錄底下由 ForgePilot 掌控；建立前先清除殘骸註冊，結束後強制移除，PASS 與 FAIL 皆移除。這對受管理專案強加一條契約：其 `make verify` 必須能在全新 checkout 上執行。ForgePilot 自身的驗證 worktree 是短暫且 detached 的，不含自己的 state 目錄，state 永遠留在主工作樹。

**存活性以 flock 判定，不用 pid**（ADR-0004）。Verification Run 期間額外持有一個以 Work Item 命名的鎖，作業系統在程序死亡時自動釋放。它同時排除了同一件工作被並行驗證兩次，而不同 Work Item 之間互不阻擋。這個鎖與 state 鎖是分開的兩把——驗證本身在 state 鎖之外執行，否則長時間的驗證會擋住所有查詢。

**`verify` 的執行順序**是：取得該 Work Item 的 verify 鎖；第一次 state 交易，回收孤兒並把狀態改為 VERIFYING、記下 Verification Run；在鎖外執行隔離驗證；第二次 state 交易，append Evidence、清除 Verification Run、依 result 決定 transition。

**`verify` 的前置條件**：Goal 為 ACTIVE、Work Item 為 RUNNING 或 REVIEW、repository 有 HEAD、工作樹乾淨、受管理專案有 `make verify`。對同一個 SHA 重跑不受限制，對已 REVIEW 的工作重驗也不要求它必須是 stale。

**Stale 的定義**是最新一筆 Verification Evidence 的 SHA 不等於目前 HEAD。它是純粹的呈現，不造成任何自動 transition；REVIEW 回到 VERIFYING 只由 `verify` 命令推動。`next` 與 `status` 呈現 stale 但不寫入 state。

**孤兒回收由 `verify` 的第一次交易執行。** `status` 與 `next` 撞見跑者已消失的 VERIFYING 時只呈現這個事實，不寫入。

## Testing Decisions

好的測試只驗外部可觀察的行為——CLI 的輸出、exit code、以及重新讀回的 state——不驗內部呼叫順序或私有結構。M1 已經確立這個取向，M2 沿用。

**不新增任何測試 seam。** M2 引進 Git 與 subprocess，直覺會想為它們造 port 並 mock，但既有的兩個 seam 已經足夠高，而且 M1 已示範如何在沒有 mock 的情況下測這類東西。

**第一個 seam 是既有的 CLI process seam**（根目錄的 integration test）。它建出真的 binary，在 temporary Git repository 裡以獨立 process 驅動。M2 的所有 Git、worktree 與 canonical 檢查行為都在這裡驗證。fixture 需要擴充：加入真實 commit，以及一個 Makefile。PASS 用正常通過的 Makefile；FAIL 用故意以非零 exit code 結束的 Makefile；INTERRUPTED 用一個會持續執行的 Makefile，測試在驗證進行中終止該 process。prior art 直接可用——既有的 storage 測試已經以真 subprocess 驗證「程序死亡後鎖被釋放」，M2 的中斷回收是同一個機制的延伸。

**第二個 seam 是既有的 `internal/work` domain seam。** transition 規則、Evidence append、stale 判斷、孤兒回收的決策全部在這裡以純函式測試，外部事實以參數傳入。這與既有的 domain 測試同一形式。

**`internal/repository` 新增的 Git 與 exec 函式不單獨以 mock 測試。** 它們是薄的傳遞層，沒有值得隔離的邏輯，覆蓋由 process seam 提供。硬要為它們造介面只會增加 seam 數量並讓測試驗到 mock 而非真實的 Git 行為。

**必測行為**：PASS 進 REVIEW；FAIL 回 RUNNING；髒工作樹被拒且不留 Evidence；未追蹤檔案也算髒；缺少 `make verify` 被拒且不留 Evidence；新 commit 之後舊 PASS 顯示為 stale 且不被沿用；中斷後回收為 INTERRUPTED 並退回 RUNNING，且絕不產生 PASS；隔離 worktree 看不見主工作樹的未提交內容；驗證進行中 `status` 仍可執行；同一 Work Item 的並行驗證被拒；不同 Work Item 可並行；worktree 殘骸被清除；既有 M1 state 被 v2 binary 拒讀；`migrate` 正確備份、重複執行安全、備份已存在時拒絕、升級後資料完整。

## Out of Scope

DONE 狀態與依賴解鎖、Gate、Human Decision、Human Review、`review approve`——全部屬於 M3。M2 的終點是 REVIEW。

PR number 與 HEAD SHA 的 review target 屬於 M4。

不提供 Evidence 查詢指令、`--json`、任意 status mutation、identity framework。不提供驗證逾時上限——卡住的驗證由使用者終止，該路徑已由中斷回收正確處理。不提供 schema downgrade。不開放自訂驗證 command——canonical 檢查固定為受管理專案的 `make verify`。

不擴充平台支援。macOS 本機檔案系統仍是唯一驗證過的範圍，不得宣稱跨平台的鎖行為。

不加入 database、Web UI、daemon、scheduler、agent runtime、plugin framework 或 network API。

## Further Notes

既有的 storage 測試目前以 `schema_version` 為 2 的內容作為「較新 schema 應被拒讀」的 fixture。M2 把版本升到 2 之後這個 fixture 會失去意義，必須改為 3，否則該測試會在無人察覺的情況下改為驗證一個合法的 state。

隔離 worktree 帶來的實測成本在本 repository 約為多出三秒：Go 的 build cache 跨 worktree 共用，但 test cache 因執行路徑改變而全部落空。這個成本會隨受管理專案的測試規模放大，屬於已知且已接受的取捨。

自動化 Git worktree 有三個已實測的必要條件：必須以 detach 加完整 SHA 建立，使用 branch name 會因為該 branch 已被主工作樹佔用而失敗；移除時必須強制，因為驗證過程留下的未追蹤檔案會讓普通移除失敗；每次執行前必須先清除殘骸，因為被強制終止的程序會留下 prunable 的註冊。
