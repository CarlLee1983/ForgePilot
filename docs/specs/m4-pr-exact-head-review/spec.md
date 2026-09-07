# M4 — PR exact-head review integration

前置決策已全部定案，見 [architecture.md](../../architecture.md#m4-開工前定案已完成) 與 `docs/adr/0010`–`0011`。本文件是實作規格；拆出的工作見 [issues/](issues/)。

## Problem Statement

M3 之後，一件工作可以被驗證、被審查，並在條件滿足時合法地完成。Human Review Evidence 綁定 repository、Story 與完整 commit SHA，這在單機、單人、直接在 branch 上工作的情境裡已經足夠。

但實際的工程審查不發生在 commit 上，發生在 pull request 上。有人在 PR 頁面上看完 diff、留下意見、按下 approve，然後回到終端機執行 `forgepilot review approve`。ForgePilot 記下來的那筆 Evidence 只知道一個 SHA，不知道那次審查的現場在哪裡。

差別在日後有人回頭讀這筆紀錄的時候才顯現。`EV-012 / APPROVED / a1b2c3d / carl@example.com` 說得出結論、說得出版本、說得出是誰聲稱的，卻說不出去哪裡看那次審查的討論。而那份討論——為什麼這樣改、當時考慮過什麼、哪一條意見被接受了——正是 Evidence 存在的理由：程式碼可以重新推導，判斷的過程不能。

development-plan 對 M4 的硬約束也正是這件事：Evidence 必須包含 repository、PR number 與 exact HEAD SHA。目前三者只有兩者。

## Solution

`review approve` 與 `review reject` 各增加一個選填的 `--pr`，值的形式是 `owner/name#number`。帶了就把這個 PR Reference 存進該筆 Human Review Evidence，`status` 顯示最新一筆審查時一併顯示它。

刻意小。M4 不引入任何新的狀態機行為：DONE 的四項條件一字不改，`Stale` 的定義一字不改，指令數不變。「新 HEAD 必須重新取得適用 review Evidence」這條 exit criteria 由 M2 就存在的 SHA 比對天然成立——PR Reference 在其中不扮演任何角色（[ADR-0011](../../adr/0011-pr-identity-does-not-gate-completion.md)）。

同樣刻意的是它不去 GitHub。原本被列為 M4 前置決策的「PR metadata 來源、授權與 read-only integration 邊界」三問，答案是同一個：不從外部取得。PR Reference 是使用者輸入的識別字串，ForgePilot 只驗格式，不查證那個 PR 存在、是否開著、HEAD 是不是它（[ADR-0010](../../adr/0010-no-outbound-network-requests.md)）。因此沒有來源可談、沒有 token 要讀、沒有網路失敗要處理，而 PR review target 的 HEAD 必然是本機 repository 裡真實存在的 commit——因為 `review` 本來就只能記錄本機當下的 HEAD。

## User Stories

1. 身為使用者，我想在核准時指明這次審查發生在哪個 pull request 上，以便日後回頭讀這筆紀錄時找得到當時的討論。
2. 身為使用者，我想在打回時同樣指明 pull request，以便被打回的理由和被核准的理由一樣有現場可回溯。
3. 身為使用者，我想讓 PR Reference 自帶 repository 與 number，以便這筆紀錄離開我這台機器之後仍然可以解讀。
4. 身為使用者，我想讓 PR Reference 只接受一種形式，以便不會出現兩筆指向同一個 PR 卻寫法不同的紀錄。
5. 身為使用者，我想在 `--pr` 格式非法時整個指令被拒絕，以便錯字不會被靜靜存成一筆無法解讀的識別資料。
6. 身為使用者，我想在那種拒絕發生時什麼都沒被寫入，以便一次失敗的指令不會留下半筆審查。
7. 身為使用者，我想在不帶 `--pr` 時仍然能正常核准，以便本機先審、之後才開 PR 這種正當流程不被禁止。
8. 身為使用者，我想讓 M3 時代記下的、沒有 PR 的審查繼續有效，以便升級不會讓已經完成的工作變成讀不進來的狀態。
9. 身為使用者，我想讓 ForgePilot 不去 GitHub 查證我輸入的 PR，以便這個工具在沒有網路、沒有 token、GitHub 掛掉的時候照樣可用。
10. 身為使用者，我想讓產品不宣稱它查證過那個 PR，以便我不會誤以為 Evidence 上的 PR 是經過確認的事實。
11. 身為使用者，我想讓 PR Reference 明確地是自述的識別資料，以便它和自述的決策者身分有一致的可信度標示。
12. 身為使用者，我想讓 verification Evidence 不能攜帶 PR Reference，以便一種 Evidence 不會被讀成另一種。
13. 身為使用者，我想在載入手動改過的 `state.json` 時，非法的 PR Reference 被拒讀，以便 state 的完整性不只靠 CLI 把關。
14. 身為使用者，我想讓 PR Reference 不影響工作能不能完成，以便一筆記錯的識別資料不會使一次真實的審查失效。
15. 身為使用者，我想讓同一個 SHA 上標著不同 PR 的兩筆審查仍然互相取代，以便「取最新一筆」這條規則不因為多了一個欄位而出現例外。
16. 身為使用者，我想讓 PR Reference 不影響 stale 的判定，以便 stale 仍然只回答「這個 Evidence 是不是綁在目前的 revision 上」這一個問題。
17. 身為使用者，我想在 HEAD 改變之後舊的核准不再適用，以便 PR 上追加的 commit 不會沿用先前的核准。
18. 身為使用者，我想在那種情況下重新驗證與重新核准，並讓新的那筆記下新的 HEAD，以便每一次核准都指向它實際看過的那份程式碼。
19. 身為使用者，我想讓舊的 PASS 與 APPROVED 保留為歷史，以便我看得到這件工作經歷過哪些版本。
20. 身為使用者，我想在 `status` 看到最新一筆審查的 PR Reference，以便存下來的東西有讀出來的地方。
21. 身為使用者，我想讓 `status` 在沒有 PR 時什麼都不印，以便缺少 PR 不被呈現成一個需要處理的問題。
22. 身為使用者，我想讓 `status` 對 PR 不帶警告語氣，以便沒有行動意義的提示不會累積到我開始忽略所有提示。
23. 身為使用者，我想在 v3 state 上執行升級後保留全部既有資料，以便先前累積的佇列、Evidence 與 Gate 可以直接沿用。
24. 身為使用者，我想讓升級照樣先備份，即使這次沒有資料要改，以便「升級就是備份加改版號」這個契約沒有例外。
25. 身為使用者，我想讓舊版 binary 拒讀 v4 state，以便它不會在重新保存時丟掉它不認得的 `pr` 欄位。
26. 身為使用者，我想在重複執行 `migrate` 時安全地得到「已是最新版本」，以便我不必記得自己升過沒有。
27. 身為使用者，我想讓備份檔已存在時 `migrate` 拒絕執行，以便上一次的備份不會被覆寫。
28. 身為使用者，我想讓 M4 不新增任何指令，以便我已經學會的操作方式不變。
29. 身為使用者，我想讓 ForgePilot 仍然不做 merge 或 release，以便核准一次審查不會意外地授予那個權限。

## Implementation Decisions

**不新增指令。** `review approve` 與 `review reject` 各增加一個選填的 `--pr`。flag 名以 development-plan 的契約表為準（M3 曾自行命名成 `--as`／`--rationale` 後改回文件寫的名字）。不提供 PR 查詢指令，不提供 `--json`。

**PR Reference 是單一字串，形式為 `owner/name#number`。** 只接受這一種形式：不接受完整 URL，不做正規化。多一種輸入法就多一組解析錯誤，以及一個「這兩筆是不是同一個 PR」的比較問題。既有的 `repository` 欄位不變（它存的是本機 state root 路徑，從 Goal 複製而來），PR Reference 自帶完整識別，因此離開這台機器仍可解讀——development-plan 那條「Evidence 必須包含 repository、PR number 與 exact HEAD SHA」由 PR Reference 與既有的完整 SHA 欄位共同滿足。

**格式規則屬於 `internal/work`。** PR Reference 的格式純粹是字串規則，不碰 filesystem、Git 或任何外部系統，因此它與其他 Evidence 欄位規則同處，由既有的 evidence 驗證路徑一併把關。只在 CLI 驗的話，手動改過的 `state.json` 會夾帶非法值進來。`internal/repository` 不需要任何新能力——這次是真的，M3 的偏離源於「身分預設取自 Git 設定」本身就需要讀 Git，而 PR Reference 完全來自使用者輸入。

**`pr` 是 review 專用的選填欄位，verification 禁止攜帶。** 這與既有的嚴格分流一致：review 不得帶 `command`／`exit_code`，verification 不得帶 `reviewer`／`note`。M4 的 `pr` 加入 review 那一側。選填意味著「空字串」是合法值，而不是需要另一個可空型別——`Reviewer`／`Note` 已經是這個形狀，沿用它。

**`--pr` 選填，且格式非法時拒絕整個指令。** 不帶 `--pr` 就是 M3 那種純 commit review。強制必填會讓 M3 時代合法完成的 DONE 變成讀不進來的 state，正是 [ADR-0006](../../adr/0006-done-is-terminal.md) 要避免的事。格式非法時的正確行為是什麼都不寫——那是「輸入無效」，不是「一次結果為 REJECTED 的審查」，同 M2 對「受驗 revision 沒有 `make verify`」的處理。

**PR 不參與任何判定。** DONE 的四項條件與 `Stale` 一字不改（[ADR-0011](../../adr/0011-pr-identity-does-not-gate-completion.md)）。同一 revision 上取最新一筆的規則不因 PR 而分岔：標著不同 PR 的兩筆 review 仍然互相取代。

**不主動發出網路請求**（[ADR-0010](../../adr/0010-no-outbound-network-requests.md)）。不自行 HTTP，不 spawn `gh`。推論是 PR review target 的 HEAD 必須是本機真實存在的 commit——這由 `review` 既有的行為天然成立，它記錄的本來就是本機當下的 HEAD，且工作樹不乾淨時拒絕記錄。

**Schema 升至 v4**：Evidence 加入選填的 `pr`，無其他變更。版本檢查是嚴格相等，所以即使升級步驟不動任何資料，仍必須升版並提供 v3→v4 這一步，且照走完整儀式——備份為 `state.json.v3.bak`、備份已存在時拒絕、對已是最新版本者回報並以 exit 0 結束。不為「這次沒有資料要動」開特例。

**`status` 顯示最新一筆 Human Review 時一併顯示其 PR Reference（若有），沒有就什麼都不印。** 它是描述而非警告，不因缺少 PR 而提示任何事。[ADR-0008](../../adr/0008-approval-completes-work.md) 要求 `status` 不對未完成的原因沉默，而 PR 不是完成條件之一，因此不在該要求範圍內。

## Testing Decisions

好的測試只驗外部可觀察的行為——CLI 輸出、exit code、以及重新讀回的 state——不驗內部呼叫順序或私有結構。M1–M3 已確立此取向，M4 沿用。

**不新增任何測試 seam。** M4 沒有引入任何新的外部依賴，這是 [ADR-0010](../../adr/0010-no-outbound-network-requests.md) 的直接後果：若當初選了「去 GitHub 讀 review state」，這裡就必須為外部 API 造一個 port 並在測試中偽造它，而那個 port 會是這個專案的第一個 domain 介面。現在不需要。兩個既有 seam 各自承擔：`internal/work` 的 domain seam 與 CLI 的 process seam。

**domain seam 驗規則。** PR Reference 的格式接受與拒絕（含大小寫、`.`／`_`／`-`、number 不得為 0 或帶前導零、缺 `#`、缺 `/`、多餘空白）、verification 攜帶 `pr` 時 `validateEvidence` 拒讀、review 不帶 `pr` 時合法、以及「加了 `pr` 之後完成判定與 stale 完全不變」——最後這條要正面測，因為它是 ADR-0011 唯一的可執行保證：同一 SHA 上標不同 PR 的兩筆 review 仍互相取代，且 `CompletionBlockers` 與 `Stale` 的結果與不帶 PR 時逐項相同。

**process seam 驗介面契約與端到端流程。** 介面契約包含 `--pr` 選填、格式非法時 exit code 非零且不寫入任何 Evidence（重新讀回 state 確認 evidence 陣列未增長）、reject 同樣接受 `--pr`。端到端流程在 temporary Git repository 中以獨立 process 跑：在某個 HEAD 上 verify PASS、`review approve --pr owner/name#123` 進入 DONE，讀回確認 Evidence 同時保存 PR Reference 與完整 SHA；接著新增一個 commit 使 HEAD 改變，確認舊的 PASS／APPROVED 保留為歷史但不適用，必須重新 verify 與重新 approve，且新的那筆記的是新的 HEAD。這條同時驗到硬約束的三個欄位與「HEAD 一變就是新 target」。

**migration 測試沿用 M2／M3 的既有形狀**：v3 state 被拒讀並指示升級、`migrate` 備份後升級且不遺失資料（既有的 Gate 與 Evidence 逐筆比對）、重複執行安全、備份已存在時拒絕。整合測試建立舊版快照的方式維持操作解碼後的文件，不用字串替換。

**兩處版本號 fixture 必須跟著調到 v4**：`internal/storage` 中驗證「較新 schema 應被拒讀」的測試，與 `internal/work` 中區分較舊／較新 schema 錯誤的測試。它們失效的方式不是變紅，而是在無人察覺下改為驗證一個合法的 state。M2 踩過一次，M3 記得調了。

**`status` 輸出的斷言會被打到**，那是刻意的——ADR-0008 要求 `status` 的呈現受測。新增的斷言只有兩條：有 PR 時顯示、無 PR 時不顯示且不出現任何提示字樣。

## Out of Scope

不去 GitHub 或任何外部系統讀取 PR 狀態、review state、CI 結果或 HEAD SHA。不做認證，不讀 token，不發出任何網路請求（[ADR-0010](../../adr/0010-no-outbound-network-requests.md)）。

不在 Work Item 上保存 PR 欄位，不提供 PR 與 Work Item 的宣告式對應，不提供修改既有 Evidence 之 PR Reference 的操作——Evidence 是 append-only。

不讓 PR 參與完成或 stale 判定（[ADR-0011](../../adr/0011-pr-identity-does-not-gate-completion.md)）。不讓 verification Evidence 攜帶 PR Reference。

不提供 PR 查詢指令、`--json`、URL 形式的輸入或其正規化。不新增任何指令。

不加入 automatic merge 或 release，Human Review 的 APPROVED 不授予該權限。不擴充平台支援，macOS 本機檔案系統仍是唯一驗證過的範圍。不加入 database、Web UI、daemon、scheduler、agent runtime、plugin framework 或 network API。

## Further Notes

M4 是四個 milestone 裡最小的一個：一個選填欄位、一次不動資料的 schema 升版、兩個指令各多一個 flag。這個規模是前置決策的結果而非巧合——M4 原本被寫成「PR metadata 的來源、授權與 read-only integration 邊界」，聽起來像一個 integration 專案；定案時發現那三問的答案都是「不從外部取得」，於是整個 milestone 塌縮成識別資料的記錄。

因此本階段最需要提防的不是實作難度，是規模膨脹。Evidence 上會有一個沒有任何規則讀取的欄位，這在讀者眼中很像一條沒接完的線。ADR-0011 存在的唯一理由就是讓那條線的缺席是可查的決定，而不是看起來像疏漏的空白。

M3 的 spec 曾宣稱「不需要 `internal/repository` 的任何新能力」而在實作中不成立，原因是漏看了「身分預設取自 Git 設定」本身就需要讀 Git。M4 再次做出同樣形狀的宣稱，這次的依據是 PR Reference 完全來自使用者輸入、格式驗證不碰任何外部系統——但實作時仍應主動檢查，而不是因為文件這樣寫就假定成立。

M2 為了 INTERRUPTED 把 exit code 改為可空，M3 因此得以自然地加入 review 專用的可空欄位，M4 又沿用同一個位置加上第三個。這個容器已經承載了兩種 Evidence 與各自的專用欄位；下一次要加第三種 type 之前，值得先問它是不是還適合當同一個容器。
