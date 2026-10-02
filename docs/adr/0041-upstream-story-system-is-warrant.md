---
status: accepted
---

# 上游 Story 體系由 Warrant 取代 PraxisBound

[ADR-0013](0013-forgepilot-self-adoption-of-forgeflow.md) 把上游 Story 體系定為 PraxisBound，
並讓 ForgePilot 自身借用它的 Story 目錄契約（`specs/stories/<story-id>/` 下的 `story.md` 與
`acceptance.md`）。PraxisBound 已被 [Warrant](https://github.com/CarlLee1983/Warrant) 取代，
ForgePilot 的文件與 CLI 訊息若繼續寫 PraxisBound，會把讀者指向一個不再使用的體系。

Warrant 的 Story 是單一檔案 `specs/stories/<slug>.md`，只有 Goal、Out of Scope、Acceptance
Criteria 三節；核准由人把 Story commit 進預設分支，或在當次 session 明確指派；完成由 repository
自己宣告的驗證命令證明。Warrant 明定 `specs/stories/` 底下的目錄是舊協定的歷史紀錄，不是待辦工作。

決定如下：

- 上游 Story 體系改稱 Warrant；現行文件、CLI help 與錯誤訊息一律以 Warrant 稱呼，
  「PraxisBound Story」改為「Warrant Story」。
- `internal/repository/repository.go` 的 `ValidateStory` 不改：它本來就只驗路徑位於
  `specs/stories/` 底下且是檔案或目錄，不解析內容。Warrant 的單檔 Story 與既有的目錄 Story 都能被參照。
- ForgePilot 自身 `specs/stories/` 底下既有的目錄（`FP-*`）與兩份 `m5-*.md` 保留為歷史紀錄，
  不改寫成 Warrant 格式；新的 Story 寫成 Warrant 的 `specs/stories/<slug>.md`。
- ForgePilot 仍不新增 Story schema、不新增 Story 的核准狀態，也不把 Warrant 接進 `make verify`。
  Story 是否核准、驗收條件是否成立，屬於 Warrant 與人，不屬於帳本。

ADR-0013 的「只借格式、不交出治理所有權」與「不執行 bootstrap」兩點對 Warrant 仍然成立：
ForgePilot 的 `AGENTS.md` 記的是踩過的坑，不以模板覆寫。被取代的只是 PraxisBound 的名稱、
目錄格式與 `scripts/story-check`。被取代的 ADR 與 `docs/specs/` 底下的歷史規格保留 PraxisBound
的寫法，因為那是當時的事實。

**Consequences:** 一個 Goal Plan 可以同時參照舊的目錄 Story 與新的 Warrant 單檔 Story，
ForgePilot 不區分兩者。Story 結構不再有任何自動檢查——Warrant 沒有 `story-check` 的對應物，
三節是否齊全由人審閱。

## Superseded

本 ADR 取代 ADR-0013。

**Falsified if:** `internal/repository/repository.go` 的 `ValidateStory` 開始讀取 Story 內容或
只接受目錄（Warrant 單檔 Story 會被拒絕）；`Makefile` 的 canonical check 開始依賴 Warrant；
或 `internal/cli/cli.go` 的 help 與錯誤訊息重新指名 PraxisBound。任一項發生，上游體系的邊界已經移動，
必須重新決定。
