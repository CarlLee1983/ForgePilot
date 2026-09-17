# ForgePilot 借用 ForgeFlowV2 的 Story 格式，不交出 repository 的治理所有權

ForgePilot 的定義是「story 由 ForgeFlowV2 管理」，但 ForgePilot 自身的 `work add --story`
一直指向 `specs/stories/m5-*.md`——為了滿足 `repository.ValidateStory` 的路徑檢查而手寫
的臨時契約。這個矛盾在 M5 dogfood 中被撞到（`docs/specs/m5-dogfood-friction.md` 摩擦一），
當時記錄為一個開放問題：一個不知道脈絡的讀者，看到「story 由 ForgeFlowV2 管理」的定義
卻看到手寫的 story 檔案，很可能會「順手修正」它。

問題現在被回答了。ForgeFlowV2 確實存在，位於 `/Users/carl/Dev/CMG/ForgeFlowV2`，並已更名為
PraxisBound（remote `CarlLee1983/PraxisBound`，npm `@praxisbound/core`、`@praxisbound/cli`，
尚未發佈）。查證的結果改變了問題的形狀：**PraxisBound 沒有 Story 產生器**。它的流程是
複製 `templates/story/` 到 `specs/stories/<story-id>/`、由 agent 草擬、由人核准，再以
`scripts/story-check` 做靜態結構檢查。這與 ForgePilot 已有的 Story 生命週期是同一個形狀，
不產生另一份 Story schema 或核准狀態。原本以為的取捨——「接上一個產生器」對「繼續手寫」
——並不存在。

因此採取部分套用：ForgePilot 採用 PraxisBound 的 **Story 目錄契約**——`specs/stories/<story-id>/`
底下的 `story.md` 與 `acceptance.md`，Story ID 依 PraxisBound 的 grammar——並以 PraxisBound
repo 的 `scripts/story-check` 驗證結構。不執行 `scripts/bootstrap`。bootstrap 會寫入
`guidance/` 與 `specs/.praxisbound-adoption`，並覆寫 `AGENTS.md`；ForgePilot 的 `AGENTS.md`
記的是踩過的坑這類不可重建的知識，用它換一份模板不划算。

`ValidateStory` 不需要改：`internal/repository/repository.go` 的路徑檢查已經接受目錄，
也仍然只驗路徑、不解析內容。Story 內容的所有權留在 ForgeFlowV2／PraxisBound 那一側，
ForgePilot 不新增 Story schema、不新增 Story 的核准狀態。
`scripts/story-check` 以 PraxisBound repo 的絕對路徑呼叫，不接進 `Makefile` 的 canonical
check；ForgePilot 的 `make verify` 不應該依賴另一個 repo 的工作樹是否存在於這台機器上。

既有的 `specs/stories/m5-review-message.md` 與 `specs/stories/m5-verification-log.md` 保留為
刻意的歷史偏離，不改寫成目錄格式。它們記錄的是 M5 當時的契約；把它們補成事後的格式，
等於宣稱當時就是這樣做的。

命名上，本 ADR 之後 ForgeFlowV2 與 PraxisBound 指同一個專案。`CONTEXT.md` 與既有 ADR 的
措辭暫時維持 ForgeFlowV2，因為 issue #28–#41 的 body 已經 publish 並以該詞寫成；改名是另一個
決定，不在這一份裡順手做掉。

**Consequences:** 新的 Story 要寫成符合 PraxisBound grammar 的目錄，比一個平面 `.md` 貴——
`acceptance.md` 要求每一條 AC 有唯一編號，且 `--ready` 要求每條 AC 在 Acceptance Evidence
表裡有對應的 Method／Evidence／Fixture／Expected observation。這個成本是刻意付的：它讓
「驗收條件是否可觀察」變成可檢查的，而不是讀者的判斷。`story-check` 是外部工具，這台機器
上沒有 PraxisBound 的工作樹時無法執行；`make verify` 不受影響，但 Story 結構的把關會退回
人工檢閱。

**Falsified if:** （由 accepted ADR-0029 所列、含其由 upstream 產生的 `story.md`／`acceptance.md`
raw-byte digest binding 的 versioned readiness sidecar 明確例外除外）`specs/stories/` 底下出現不符合 PraxisBound Story 目錄契約的新 Story（既有
兩個 `m5-*.md` 是明列的例外）；ForgePilot 的工作樹出現 `guidance/` 或
`specs/.praxisbound-adoption`；`internal/repository/repository.go` 的 `ValidateStory` 開始讀取
Story Markdown 的內容而不只是路徑；或 `Makefile` 的 canonical check 開始呼叫 PraxisBound repo 的
`scripts/story-check`。後者是對外部 PraxisBound 工作樹的依賴，與 ForgePilot repository 內其他
腳本無關。任一項發生，「只借格式、不交出所有權」這條邊界已經移動，必須重新決定。
