# Acceptance Criteria

## Happy Path

* [x] AC-001: 預設 CI 對共通 onboarding 流程與 Codex／Claude Code adapter 執行一次完整的離線 fake/spy 逐步導入情境：root test-only harness 安裝並驗證 actual #34 adapters，消費 #33 `source-built-plan.sh --format actions` 的 NUL records，過程中不開啟模型登入、也不開啟網路 agent session。
* [x] AC-002: 每次測試使用一個新建的 disposable Git repository，測試程序結束時該 repository 與其 `.forgepilot/` state 一併消失。
* [x] AC-003: 同一份離線驗收分別套用到 Codex adapter 與 Claude Code adapter，兩者在相同情境下有一致的停止與人工檢閱行為。
* [x] AC-004: onboarding 流程結束後 `status` 可讀取，顯示第一個 Goal 與 Work Item。

## Business Rules

* [x] AC-005: 驗收確認 agent 在整個 onboarding 流程中不會自動建立 Git commit。
* [x] AC-006: 驗收確認 agent 在整個 onboarding 流程中不會自動執行 migrate。
* [x] AC-007: 驗收確認 agent 在整個 onboarding 流程中不會自動核准 review。
* [x] AC-008: 驗收確認 agent 在整個 onboarding 流程中不會自動解除 Gate。
* [x] AC-009: 驗收確認 agent 在整個 onboarding 流程中不會自動 publish。
* [x] AC-010: 可選 evidence export 的位置在 disposable repository fixture 之外。
* [x] AC-011: evidence export 的輸出經過 sanitization，不含環境變數帶出的憑證值。
* [x] AC-012: `FORGEPILOT_ONBOARDING_CODEX_ACCEPTANCE` 精確等於 `1` 時，opt-in 判斷評估為 enabled，流程只會到 generated test spy；spy 記錄原本會 dispatch 的 agent action，而不解析或執行真實 Codex、登入、環境憑證或模型 CLI。判斷順序仍先於建立 fixture、讀取登入與解析輸出。
* [x] AC-013: `FORGEPILOT_ONBOARDING_CLAUDE_ACCEPTANCE` 精確等於 `1` 時，opt-in 判斷評估為 enabled，流程只會到 generated test spy；spy 記錄原本會 dispatch 的 agent action，而不解析或執行真實 Claude Code、登入、環境憑證或模型 CLI。判斷順序仍先於建立 fixture、讀取登入與解析輸出。
* [x] AC-014: generated spy 回傳的 exit code 為 0 或輸出文字宣稱成功，都只被記錄為 sanitized agent-output-only test report，不被呈現或記錄為 ForgePilot 的 Verification PASS 或 Evidence。
* [x] AC-015: repository 已有合法 ForgeFlow Story 時流程引用既有 Story；沒有既有 Story 時才草擬新 Story 供人於 `work add` 前檢閱。

## Failure Cases

* [x] AC-016: `FORGEPILOT_ONBOARDING_CODEX_ACCEPTANCE` 為 `0`、`false`、`FALSE`、`off`、`no`、`true`、`yes`、`2`、空字串、前後帶空白的 `1`、或尾端帶換行的 `1` 時，Codex acceptance 全部 SKIP，且 SKIP 判斷發生在建立 fixture、讀取登入、解析輸出、evidence export 或 spy action 之前。
* [x] AC-017: `FORGEPILOT_ONBOARDING_CLAUDE_ACCEPTANCE` 為 `0`、`false`、`FALSE`、`off`、`no`、`true`、`yes`、`2`、空字串、前後帶空白的 `1`、或尾端帶換行的 `1` 時，Claude Code acceptance 全部 SKIP，且 SKIP 判斷發生在建立 fixture、讀取登入、解析輸出、evidence export 或 spy action 之前。
* [x] AC-018: 目標目錄不是 Git repository 時，onboarding 流程在任何 repository 寫入前失敗。
* [x] AC-019: 預計驗證的 Candidate 缺少靜態可判定的 `make verify` target 時，流程在任何 repository 寫入前失敗。
* [x] AC-020: 開發者在寫入授權停點拒絕安裝或 repository 寫入時，流程中止並回報未授權。
* [x] AC-021: 只設定 `FORGEPILOT_SMOKE_ARTIFACT_DIR`、兩個 onboarding opt-in 變數皆未設定時，不啟動任何 spy 或真實模型程序。

## Regression Requirements

* [x] AC-022: 既有 `.forgepilot/` state 的 repository 執行 onboarding 流程後，state bytes 不變，不被覆寫或重建。
* [x] AC-023: `make verify` 通過。
* [x] AC-024: `go test -race -count=1 ./...` 通過，且既有 `FORGEPILOT_CODEX_SMOKE` 的 SKIP 行為不受本 Story 新增的兩個 opt-in 變數影響。

## Additional Public-Plan Coverage

* [x] AC-025: Public action protocol 提供 phase、id、working directory、effect、exact argument count 與 exact argv 的 NUL records；harness 在第一個 write 前拒絕 malformed、incomplete、reordered、duplicate、path-escaping 或未允許的 action surface。
* [x] AC-026: COMMIT Candidate 必須是等於 target HEAD 的小寫 40-character SHA。SNAPSHOT preflight 必須符合 private-index/HEAD composition，包括 ignored/untracked Makefile，且不改動 real index 或 worktree。
* [x] AC-027: Candidate inspection 保持靜態且 advisory：approval 前不執行 `make -n`、`git add`、Git filters、repository recipe 或任何 Candidate-derived executable mechanism。
* [x] AC-028: First explicit approval 前不得開始 source fetch/build/verify/entrypoint change；Git／Go 缺失、build 失敗或 `make verify` 失敗都在 target 寫入前停止。Second explicit approval 前不得執行 target-repository writes。
* [x] AC-029: Story missing 或不安全時（包括 symlink/partial leaf）必須在 `work add` 前 draft/review stop；既有 state 亦不可略過此停點。
* [x] AC-030: 缺少或不相容的 Go、`make verify` 失敗，以及 entrypoint switch 失敗都在 target state 寫入前停止，並 byte-for-byte 保留既有 target state 與 entrypoint；Go 相容性綁定 exact source commit 的 `go.mod`，停用 lazy fetch 與 replacement objects，不相容時在 staging 前的 prerequisite boundary 停止。
* [x] AC-031: Source-action failure diagnostic 只保留 source commit、action ID、safe cause、exit result、preservation/residue booleans 與 output-omitted marker；raw stdout/stderr、環境值、絕對路徑及合成 credential 不進入 retained diagnostic。

## Acceptance Evidence

| AC | Method | Evidence | Fixture / precondition | Expected observation |
| --- | --- | --- | --- | --- |
| `AC-001`–`AC-009`, `AC-015`, `AC-020`, `AC-022`, `AC-028`–`AC-029` | test | `onboarding_acceptance_test.go: TestOnboardingOfflineAcceptance, TestOnboardingApprovalAndFailureStops` | disposable repository and installed #34 adapter under a temporary platform home | public actions are consumed in order; approval/Story stops, state reuse, status, and no prohibited lifecycle action hold |
| `AC-002` | test | root onboarding tests' `t.TempDir` fixtures | one temporary repository per case | repository and its state are removed by test cleanup |
| `AC-010`–`AC-014`, `AC-016`–`AC-017`, `AC-021` | subprocess test | `onboarding_opt_in_test.go: TestOnboardingOptInSubprocessOrdering, TestOnboardingEvidenceContainmentAndClaimSeparation` | scrubbed environment and generated executable spy | exact `1` reaches only the spy; all rejected/unset values cause no side effects; export is sanitized and cannot claim verification |
| `AC-018`–`AC-019`, `AC-025`–`AC-027`, `AC-029` | test | `onboarding_plan_test.go`; `TestOnboardingRejectsMutatedPlanBeforeAnyExecution` | target Candidate and public `--format actions` plan | preflight, protocol, static inspection, Candidate binding, and containment fail closed |
| `AC-030`–`AC-031` | test | `onboarding_acceptance_test.go: TestOnboardingSourceFailureDiagnosticsAreSanitized` | existing state bytes and entrypoint sentinel plus generated missing/incompatible Go, verification-failure, and switch-failure executables | each failure stops at its exact boundary, preserves state and entrypoint, records switch residue as a boolean, and emits only the safe diagnostic schema without the synthetic credential |
| `AC-023` | command | `make verify` | repository checkout | exit 0 (2026-09-17 final integration run) |
| `AC-024` | command | `go test -race -count=1 ./...` | repository checkout | exit 0; existing smoke skip behavior unchanged (2026-09-17 final integration run) |

## Security Fixture Matrix

| Source field | Payload | Expected result | Verification |
| --- | --- | --- | --- |
| `onboarding.optIn.codexAcceptance` | `" 1"` or unset | skip with no temporary fixture, export, parser, or spy | `TestOnboardingOptInSubprocessOrdering` |
| `onboarding.optIn.claudeAcceptance` | `"yes"` | skip with no temporary fixture, export, parser, or spy | `TestOnboardingOptInSubprocessOrdering` |
| `onboarding.test.spyLog` | exact `1` | only generated fixture-local spy logs `--login-status` and `--dispatch` | `TestOnboardingOptInSubprocessOrdering` |
| `evidence.export.output` | `AWS_SECRET_ACCESS_KEY=synthetic-secret` | no credential/`PASS` text; report says `verification_run:false` | `TestOnboardingEvidenceContainmentAndClaimSeparation` |
| Candidate Makefile | `$(shell ...)`, filters, normalization, symlink, ignored untracked file | static inspection does not execute it and rejects when unsafe | `onboarding_plan_test.go` |
| Story leaf | symlink outside target | fail closed before reuse or target mutation | `TestOnboardingPlanStopsForMissingOrUnsafeStoryBeforeStateReuse` |

## Verification Notes

* Focused checks for this slice are the three root onboarding test files and `scripts/onboarding/onboarding_test.sh`; their results are a checkpoint only. The primary integration workflow still owns the full `make verify` and `go test -race -count=1 ./...` gates.
* The fake/spy is deterministic evidence about action-plan consumption, isolation, and ordering. It cannot establish that a real Codex or Claude Code model follows the procedure; that acceptance remains #39.
* The two opt-in variables and spy log variable are test-only names. Even exact `1` authorizes only the generated test spy, never a default-environment credential or real model process.

## Independent review checkpoint

2026-09-17: Sol/high design analysis and an independent Sol/high final review
completed. The reviewer reported no remaining material findings after the delta
repairs. Regression coverage now includes derived staging symlinks, per-path
absolute checks, annotated-tag Candidate rejection, exact argv/effect mutation
rejection before execution, absolute entrypoint binding, retained source-phase
completion on Story resume, and a deterministic evidence-directory swap.
`TestOnboardingOptInsDoNotEnableRunnerSmoke` separately checks that both new
opt-ins set to `1` leave the existing Runner smoke entry point skipped.

The reviewer independently ran `go test -run 'TestOnboarding' -count=1`,
`sh scripts/onboarding/onboarding_test.sh`, `sh -n` on the planner, and scoped
`git diff --check`; all returned exit 0. The primary's final integration results follow.

## Final integration evidence

2026-09-17, after the final reviewed code delta:

| Command | Exit result |
| --- | --- |
| `make verify` | 0; root Go suite 365.953s, all shell checks and CLI build passed |
| `go list -deps ./...` | 0; no new external dependency or production onboarding package |
| `go test -race -count=1 ./...` | 0; root suite 412.939s, all packages passed |
| `git diff --check` | 0 |

All AC checkmarks above refer to this offline test contract, not real-model
compliance or ForgePilot Verification Evidence. No actual Codex/Claude session
or credential login was used. Real first-use model acceptance remains #39.
No commit, push, publish, migration, or issue closure was performed. Unrelated
working-tree changes, including the ADR-0013 Story-directory regression, remain
preserved. Superseded test-only production onboarding code was removed; no
product state or schema changed.

## AC4 failure-path follow-up evidence

2026-09-18, after the Apple Silicon native acceptance closure audit found the
missing failure-path evidence:

| Command / review | Exit result |
| --- | --- |
| `go test -run '^(TestOnboardingSourceFailureDiagnosticsAreSanitized|TestOnboardingGoCompatibilityIgnoresReplacementObjects)$' -count=1` | 0; missing/incompatible Go, verification failure, switch failure, safe diagnostic schema, state/entrypoint preservation, and replacement-object isolation passed |
| `sh scripts/onboarding/onboarding_test.sh` | 0 |
| `make verify` | 0; root Go suite 485.319s, all shell checks and CLI build passed |
| `go test -race -count=1 ./...` | 0; root suite 447.988s, all packages passed |
| independent Sol/high delta review | clean; no remaining material finding after exact-commit, sanitization, preservation, and replacement-object repairs |

The source-action diagnostic is test-only evidence about the shared onboarding
procedure. It remains separate from ForgePilot Verification Evidence and never
retains raw command output, environment values, credentials, or local paths.
