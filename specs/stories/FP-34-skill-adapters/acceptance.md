# Acceptance Criteria

## Happy Path

* [x] AC-001: Codex adapter 宣告的安裝路徑符合 Codex 官方文件記載的個人 skill 目錄樣式，adapter 內容只含平台載入位置與呼叫方式。
* [x] AC-002: Claude Code adapter 宣告的安裝路徑符合 Claude Code 官方文件記載的個人 skill 目錄樣式，adapter 內容只含平台載入位置與呼叫方式。
* [x] AC-003: 兩個 adapter 對同一份 #33 `docs/release/onboarding.md` common procedure 的暫時引用
  逐字相同，且兩者都未宣稱那是 published immutable identity。

## Business Rules

* [x] AC-004: 兩個 adapter 檔案都不含 Story、Candidate、授權或 ForgePilot state 規則的原文或摘要。
* [x] AC-005: 零 skill 短 prompt 入口在任一 adapter 缺席時，仍會解析到同一份共通 onboarding
  source contract 且維持可獨立運作。
* [x] AC-006: Skill 安裝是明確、可選的使用者動作，不隨 ForgePilot 安裝自動發生。
* [x] AC-007: Claude Code adapter 只逐步呼叫既有 `forgepilot` CLI 命令，不擴張 `forgepilot run` 的 runtime。
* [x] AC-008: 兩個 adapter 對暫時 source contract、失敗即停止與人工檢閱行為的敘述一致；另行授權
  的 immutable pin step 會原子且逐字相同地替換兩個引用，但本 Story 不執行該 step 或任何 publish。

## Failure Cases

* [x] AC-009: 兩個 adapter 的暫時 source contract 字串不同、或任一 adapter 將它宣稱為 published
  immutable identity 時，anti-drift 檢查失敗並指出兩邊各自讀到的字串或違規內容。
* [x] AC-010: Adapter 檔案含有禁止複製的規則關鍵字或段落時，anti-drift 檢查失敗並指出命中的檔案與內容。

## Regression Requirements

* [x] AC-011: `internal/cli`、`internal/app`、`internal/repository`、`internal/work` 的既有行為不變，且 `forgepilot run` 的 runtime 不變。
* [x] AC-012: ForgePilot 核心套件（`internal/cli`、`internal/app`、`internal/repository`、`internal/work`）未因本 Story 新增下載或封裝相關的相依或匯入，仍只使用標準函式庫。
* [x] AC-013: `go test -race -count=1 ./...` 通過。

## Acceptance Evidence

| AC | Method | Evidence | Fixture / precondition | Expected observation |
| --- | --- | --- | --- | --- |
| `AC-001` | test | `scripts/skills/check_adapters_test.sh` | `Codex adapter skill definition file, declared install path only, not actually installed` | `declared install path conforms to Codex official skill directory pattern and content is limited to loading location and invocation` |
| `AC-002` | test | `scripts/skills/check_adapters_test.sh` | `Claude Code adapter skill definition file, declared install path only, not actually installed` | `declared install path conforms to Claude Code official skill directory pattern and content is limited to loading location and invocation` |
| `AC-003` | test | `scripts/skills/check_adapters_test.sh` | `both adapter files present with the #33 common procedure contract` | `temporary source-contract reference identical byte-for-byte in both adapters; neither claims it is a published immutable identity` |
| `AC-004` | test | `scripts/skills/check_adapters_test.sh` | `both adapter files present` | `no forbidden rule keyword or excerpt found in either adapter` |
| `AC-005` | test | `scripts/skills/short_prompt_regression_test.sh` | `repository with no adapter installed` | `short prompt onboarding entry resolves to the same shared onboarding source contract and remains usable with no adapter file present` |
| `AC-006` | human | `review record` | `adapter installation instructions in both adapter files` | `installation described as an explicit, optional user action, not bundled with forgepilot install` |
| `AC-007` | human | `review record` | `Claude Code adapter content` | `adapter only issues existing forgepilot CLI commands, no forgepilot run runtime extension` |
| `AC-008` | test | `scripts/skills/check_adapters_test.sh` | `both adapter files present; later immutable pin is documented` | `temporary source-contract, stop-on-failure and human-review wording matches; contract records that the separately authorized immutable pin step atomically replaces both references` |
| `AC-009` | test | `scripts/skills/check_adapters_test.sh` | `adapters edited to diverge in source contract or to claim a published immutable identity` | `non-zero exit naming both differing source-contract strings or the offending immutable-identity claim` |
| `AC-010` | test | `scripts/skills/check_adapters_test.sh` | `adapter edited to embed a Story rule excerpt` | `non-zero exit naming the offending file and matched excerpt` |
| `AC-011` | command | `make verify` | `repository checkout` | `exit 0` |
| `AC-012` | command | `go list -deps ./...` | `repository checkout` | `only standard library and existing ForgePilot module paths listed, no new download or packaging dependency added` |
| `AC-013` | command | `go test -race -count=1 ./...` | `repository checkout` | `exit 0` |

## Verification Notes

* `AC-005` 只確認短 prompt 入口在完全沒有 adapter 的情況下仍能解析到同一份共通 onboarding source contract
  並維持可獨立運作；以真實 Codex 或 Claude Code agent 跑完整導入情境，屬於 #39 的驗收範圍，不在
  此 Story 宣稱。
* `AC-011` 與 `AC-012` 分別對應行為面與架構面兩個不同主張：`make verify` 只能證明既有行為未回
  歸，證明不了核心套件是否偷偷長出新的下載或封裝相依；後者改用 `go list -deps ./...` 檢查匯入
  清單。
* `AC-006` 與 `AC-007` 是人工檢閱兩個 adapter 檔案的措辭與呼叫方式，不涉及把 adapter 實際放進
  `~/.claude/skills/` 或 Codex 個人 skill 目錄——那是 #35 的 opt-in 安裝驗收。
* `AC-003`、`AC-008` 與 `AC-009` 驗證的是 GATE-004 已決定的本機可執行暫時 contract，不是
  published immutable identity。production immutable pin、其產物 commit、upload 與 Release publish
  仍須另行授權；該 step 原子且逐字相同地替換兩個 adapter 引用。
* `make verify` 與 `go test -race -count=1 ./...` 是 repository 的 canonical gate；這份 Story
  的新增測試不取代它們。
* 任何 AC 都不得只以「腳本回傳 0」單獨作為通過依據，仍須對照 Fixture 與 Expected observation
  描述的具體內容。
