---
status: proposed
---

# ForgePilot 是否自我套用 ForgeFlowV2

ForgePilot 的定義是「story 由 ForgeFlowV2 管理」，但 ForgePilot 自身的 `work add --story`
目前指向 `specs/stories/m5-*.md`——為了滿足 `repository.ValidateStory` 的路徑檢查而手寫
的臨時契約，並非 ForgeFlowV2 的產物。這個矛盾在 M5 dogfood 中被撞到
（`docs/specs/m5-dogfood-friction.md` 摩擦一）：一個不知道脈絡的讀者，看到「story 由
ForgeFlowV2 管理」的定義卻看到手寫的 story 檔案，很可能會「順手修正」它——要嘛去接
ForgeFlowV2，要嘛覺得定義寫錯了而去改 `CONTEXT.md`。兩個動作都不該在沒有決定之前發生。

「ForgePilot 要不要自我套用 ForgeFlowV2」尚未回答。這不是待辦事項，也不是已完成的
決定，而是一個開放的空位——先記錄它存在，避免它只活在某個人的記憶裡。

**Consequences:** 在這個問題被回答之前，`specs/stories/` 底下的手寫 story 是刻意的偏離，
不是待修的錯誤；後續讀者看到它時不需要自行判斷要不要修正。

**Falsified if:** `specs/stories/` 底下出現由 ForgeFlowV2 產出的檔案——那就是這個問題被
實際行動回答掉的那一刻，屆時本 ADR 需要更新為已接受或已拒絕的決定。
