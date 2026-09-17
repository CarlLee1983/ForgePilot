# Story: FP-34 Codex 與 Claude Code 的薄 skill adapter

## Goal

Codex 使用者與 Claude Code 使用者能各自選擇在自己工具官方支援的個人 skill 位置安裝一份極薄
adapter，讓自己的 coding agent 找到同一份共通 onboarding 程序；不論裝哪一個 adapter，或完全
不裝，實際的導入規則只存在一個版本，兩邊不會因為各自維護一份而在版本或行為上分岔。

## Context

ForgePilot 目前只提供 `go install` 與原始碼建置。#28 訂出的分發與導入 spec 把「零 skill 的短
prompt 入口」定為首次使用的基準路徑，並允許 Codex 與 Claude Code 使用者另外選擇安裝各自可發現
的個人 skill；#33 產出 `docs/release/onboarding.md` 這一份共通 onboarding procedure（前置檢查、Story
草擬與人工檢閱、`init → goal create → work add → status`、snapshot verification 的教學），是
這個 Story 的本機輸入；published immutable identity 不必在本 Story 開始前存在。

[ADR-0024](../../../docs/adr/0024-onboarding-stays-outside-the-offline-cli.md) 已經定案：
兩個 skill 目錄要依各自的
[Codex 官方文件](https://learn.chatgpt.com/docs/customization/overview#skills)與
[Claude Code 官方文件](https://code.claude.com/docs/en/skills)適配，不能假設一個安裝位置兩
邊都會載入；而且兩者引用同一份程序，不複製工作規則。這份 Story 要把「不複製」從一句敘述變成
可自動檢查的東西：adapter 之間的 source contract 是否同步、adapter 有沒有偷偷長出自己的一份規
則，都要有工具判定，不是留給人肉比對。

對應的 tracker 票是 GitHub issue #34，parent spec 是 #28；它依賴 #33 的本機共通 procedure，
但不等待 production immutable pin 或 publish。

## Classification

* Security sensitive: no
* Baseline conformance: no
* Task mode: execution

## Authority

* plan: yes
* modify: yes
* add_dependency: no
* migration: no
* commit: no
* push: no
* deploy: no

## Risk

* Level: medium
* Reason: `adapter-drift`

## Scope

### In Scope

* 一份 Codex 個人 skill adapter，安裝在 Codex 官方文件記載的個人 skill 位置，內容只含平台載入
  方式與如何呼叫共通 onboarding 程序。
* 一份 Claude Code 個人 skill adapter，安裝在 Claude Code 官方文件記載的個人 skill 位置，內容
  只含平台載入方式與如何呼叫共通 onboarding 程序。
* 兩個 adapter 對同一份 #33 `docs/release/onboarding.md` procedure 的暫時引用，且兩邊逐字相同；
  在另行授權的 immutable pin step 產生 published immutable
  identity 前，不得把此引用宣稱為已發佈的 immutable identity。
* 一個可重跑的 anti-drift 檢查，比對兩個 adapter 的暫時 source contract 是否相同、是否錯誤宣稱
  published immutable identity，並掃描兩個 adapter 是否含有 Story、Candidate、授權或 ForgePilot
  state 規則的原文或摘要。
* Claude Code adapter 第一版對既有 `forgepilot` CLI 的逐步呼叫方式（`init`、`goal create`、
  `work add`、`status`、`verify --snapshot`），不引入新的呼叫介面。

### Out of Scope

* 共通 onboarding 程序本身的內容、前置檢查邏輯與 Story 草擬教學——屬於 #33，這個 Story 只消費
  已提供的 common procedure，不重寫或擴充它。
* 使用正式 reviewed source commit 執行 production immutable pin、將兩個 adapter 引用改為
  published immutable identity，及其產物 commit、upload 或 Release publish——都屬於 #33 後另行
  授權的步驟；本 Story 不執行或宣稱那些外部結果。
* 兩個 adapter 的 opt-in 安裝流程驗收（實際把 adapter 放進使用者的
  `~/.claude/skills/` 或 Codex 個人 skill 目錄並確認被工具發現）——屬於 #35。
* 以真實 Codex 或 Claude Code agent 跑完整導入情境的驗收——屬於 #39。
* 擴張 `forgepilot run` 的 runtime 讓 Claude Code 被納入既有的本機 Codex runtime；Claude Code
  第一版只逐步操作既有 CLI 命令。
* Developer ID 簽署、notarization、trial/正式 binary 資產——屬於 #31 與 #36／#37 那條分支，與
  skill adapter 無關。
* 修改 `internal/cli`、`internal/app`、`internal/repository` 或 `internal/work` 的任何行為。

## Inputs

* #33 已檢閱的 `docs/release/onboarding.md` common procedure；這是本 Story 現在可本機消費的共同
  引用，尚不是 published immutable identity。
* Codex 官方 skill 文件記載的個人 skill 目錄結構與載入規則。
* Claude Code 官方 skill 文件記載的個人 skill 目錄結構與載入規則。

## Outputs

* 一份 Codex adapter skill 定義檔，只含平台載入位置說明、呼叫方式與相同的暫時 source contract
  引用。
* 一份 Claude Code adapter skill 定義檔，只含平台載入位置說明、呼叫方式與相同的暫時 source
  contract 引用。
* 一個 anti-drift 檢查工具（可重跑的 script），輸出兩個 adapter source contract 是否一致、是否
  錯誤聲稱 published immutable identity，以及是否偵測到被禁止複製的規則內容。
* 檢查結果的通過證據，涵蓋 source contract 一致性、未誤稱 immutable identity 與內容掃描。

## Rules

* R1: 兩個 adapter skill 檔案只能包含平台載入位置與呼叫方式，不得含有 Story、Candidate、授權
  或 ForgePilot state 規則的原文或摘要。
* R2: 在 production immutable pin 前，兩個 adapter 必須以逐字相同的字串引用同一份 #33 common
  procedure，並明確表示這不是 published immutable identity；不得各自複製
  或改寫程序內容。
* R3: Codex adapter 必須安裝在 Codex 官方文件記載的個人 skill 位置；Claude Code adapter 必須
  安裝在 Claude Code 官方文件記載的位置；不得假設兩邊共用同一個安裝路徑。
* R4: Skill 安裝是明確、可選的使用者動作；零 skill 的短 prompt 入口不因任一 adapter 是否存在
  而改變行為，必須維持能獨立運作。
* R5: Claude Code 第一版 adapter 只能逐步呼叫既有 `forgepilot` CLI 命令，不得擴張或修改
  `forgepilot run` 的 runtime。
* R6: 兩個 adapter 對暫時 source contract、失敗即停止與需人工檢閱等行為的敘述必須一致，不得
  其中一份鬆綁或省略共通程序已定的行為。
* R7: 兩個 adapter 都不得自行定義 onboarding 文件抓取失敗時的處理邏輯，一律委派給共通程序本
  身的規則。
* R8: Anti-drift 檢查必須能自動判定兩個 adapter 的暫時 source contract 是否相同、兩者均未聲稱
  published immutable identity，以及是否含有禁止複製的規則內容；任一項不符即為失敗，不得只靠
  人工比對通過。
* R9: 在另行授權的 publication step 取得已檢閱 source commit 的 published immutable identity 後，
  必須原子且逐字相同地替換兩個 adapter 的暫時引用；本 Story 不執行該 step、commit、upload 或
  publish。

## Expected Errors

* 兩個 adapter 的暫時 source contract 字串不同時，anti-drift 檢查失敗並指出兩邊各自讀到的字串。
* 任一 adapter 將暫時 source contract 宣稱為 published immutable identity 時，anti-drift 檢查失敗
  並指出違規檔案與宣稱內容。
* Adapter 檔案內出現被禁止複製的規則關鍵字或段落時，檢查失敗並指出違規的檔案與命中內容。
* Adapter 未依對應官方文件安裝在正確的個人 skill 位置時視為失敗，不得以「兩邊裝在同一個路徑」
  蒙混過關。
* 移除或停用任一 adapter 後，若零 skill 短 prompt 入口無法獨立完成導入，視為 regression 失敗。

## Dependencies

* GitHub issue #33 的共通 `docs/release/onboarding.md` procedure 必須可於本機讀取；它足以讓本
  Story 以共同暫時引用完成與驗證，毋須等待 production publish。
* 另行授權的 immutable pin step 才使用已檢閱 source commit 的 published immutable identity，並
  原子替換兩個 adapter 引用；這個後續步驟不是本 Story 的 blocker，也不在本 Story 的 authority 內。
* [ADR-0024](../../../docs/adr/0024-onboarding-stays-outside-the-offline-cli.md)。
* Parent spec GitHub issue #28。

## Constraints

* Skill 安裝是明確且可選的使用者動作；零 skill 的短 prompt 入口必須維持能獨立運作，不依賴任一
  adapter 存在。
* ForgePilot 的 Go 程式碼不變；這個 Story 不修改 `internal/cli`、`internal/app`、
  `internal/repository` 或 `internal/work`，也不為其加入新的 Go 相依。
* 暫時 source contract 不得偽裝成 commit SHA、release tag 或其他 published immutable identity；
  最終 immutable pin 仍必須由另行授權的 publication step 原子地寫入兩個 adapter。
* Repository 的 canonical check 是 `make verify`，另跑 `go test -race -count=1 ./...`。

## Guidance

Relevant:

* decision: `ADR-0024` — 兩個 skill 目錄依各自官方文件適配，引用同一份程序而不複製工作規則
* practice: [Codex 官方 skill 文件](https://learn.chatgpt.com/docs/customization/overview#skills)
* practice: [Claude Code 官方 skill 文件](https://code.claude.com/docs/en/skills)

Not applicable:

* #33 共通 onboarding 程序本身的前置檢查與 Story 草擬邏輯：這個 Story 只消費它的 common procedure。
