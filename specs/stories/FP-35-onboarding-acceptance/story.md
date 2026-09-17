# Story: FP-35 onboarding 與 adapter 的 disposable-repository 驗收

## Goal

以可重跑的 disposable Git repository，驗收 #33 的 source-built onboarding procedure 與 #34 已安裝的 Codex／Claude Code adapters。預設 CI 全程離線；明確 opt-in 也只會到測試生成的 executable spy，絕不解析或使用環境既有模型憑證，更不執行真實模型。真實首次模型驗收屬於 #39。

## Context

[ADR-0024](../../../docs/adr/0024-onboarding-stays-outside-the-offline-cli.md) 把 onboarding 保留在離線治理 CLI 之外：agent 必須先展示動作、取得授權，且不得覆寫既有 `.forgepilot/` state。#33 已提供公開、advisory 的 `scripts/onboarding/source-built-plan.sh`；#34 已提供 `skills/codex/forgepilot-onboarding/SKILL.md` 與 `skills/claude-code/forgepilot-onboarding/SKILL.md`。#35 的擁有邊界是 root 的測試專用 black-box harness：它安裝並消費這些 artifacts，解析 public plan 的 NUL action records，並在暫存 repository 對建置後的 ForgePilot CLI 驗證可觀察行為。

此 harness 沒有 production onboarding API，也不把測試 executor 當成 agent 或模型合規性的證明。它以 deterministic fake 驗證已收到的 public actions、授權停點與 CLI 結果；不能證明真實 Codex 或 Claude Code 會遵循 onboarding prose。

對應 tracker 為 GitHub issue #35（parent #28）。#33 與 #34 已落地；本 Story 不再以 #34 未完成或 opt-in 名稱暫定為前提。

## Classification

* Security sensitive: yes
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

* Level: high
* Reason: `credential-leak`
* Reason: `false-pass-claim`

## Scope

### In Scope

* Root test-only files `onboarding_acceptance_test.go`, `onboarding_plan_test.go`, and `onboarding_opt_in_test.go`, which consume #33's `--format actions` NUL protocol and the actual installed #34 adapters.
* #33 public plan seam 的 hardening 與 structured `--format actions` mode：inspection-only Candidate preflight 以及完整 disclosed source sequence，包含已安裝相容 Go prerequisite、versioned staging、clone、exact detached source checkout、local build、`make verify`、startup check 與 first explicit approval 後的 atomic entrypoint switch。
* Two approval boundaries: before any source fetch/build/verify/entrypoint change, and before target-repository writes. Missing Git 或 Go 都在 writes 前停止；existing Story/state handling and a missing Story's human-review stop remain observable.
* Exact opt-in test variables `FORGEPILOT_ONBOARDING_CODEX_ACCEPTANCE` and `FORGEPILOT_ONBOARDING_CLAUDE_ACCEPTANCE`. They are test-only controls for the generated spy, not product configuration.
* Fixture-external sanitized test evidence and separation of an agent success claim from ForgePilot Verification PASS.

### Out of Scope

* A production `internal/onboarding` package or any production onboarding API.
* 撰寫獨立於 #33 的 onboarding procedure，或改寫 #34 adapter 的 installation/call syntax。
* Real Codex/Claude execution, login, or first-use acceptance (#39).
* Reading, copying, importing, or relying on ambient credentials, login files, model CLIs, shell profiles, or network sessions.
* `make -n`, `git add`, filters, or any other executable Candidate inspection before approval. The public plan must perform only static Candidate inspection and fail closed when content cannot safely be determined.
* Changes to the existing `FORGEPILOT_CODEX_SMOKE` behavior, review policy, Gate logic, or Runner runtime.

## Inputs

* `scripts/onboarding/source-built-plan.sh --format actions`, with NUL-delimited `forgepilot-onboarding-plan-v1` action records.
* `docs/release/onboarding.md` and the installed copies of both #34 `SKILL.md` adapters.
* A disposable Git target, an already-built ForgePilot CLI, and generated fake executables.
* Test-only variables `FORGEPILOT_ONBOARDING_CODEX_ACCEPTANCE`, `FORGEPILOT_ONBOARDING_CLAUDE_ACCEPTANCE`, `FORGEPILOT_ONBOARDING_TEST_SPY_LOG`, and optional `FORGEPILOT_SMOKE_ARTIFACT_DIR`.

## Outputs

* Offline black-box acceptance for both installed adapters and the public action plan.
* Exact opt-in ordering coverage and a positive control that reaches only the generated spy.
* Sanitized, fixture-external test report evidence that cannot claim ForgePilot verification.

## Rules

* R1: 預設 CI 只使用離線 fake/spy 流程完成 onboarding 驗收；不得開啟模型登入、網路 agent session 或私密 evidence export。
* R2: Codex acceptance 只有在 `FORGEPILOT_ONBOARDING_CODEX_ACCEPTANCE` 的值精確等於 `1` 才啟用；`0`、`false`、`FALSE`、`off`、`no`、`true`、`yes`、`2`、空字串、前後帶空白的 `1`、尾端帶換行的 `1` 全部 SKIP。啟用時僅到測試生成的 Codex spy，絕不啟動真實模型。
* R3: Claude Code acceptance 只有在 `FORGEPILOT_ONBOARDING_CLAUDE_ACCEPTANCE` 的值精確等於 `1` 才啟用；拒絕值集合與 R2 相同。啟用時僅到測試生成的 Claude Code spy，絕不啟動真實模型。
* R4: R2 與 R3 的判斷必須發生在建立 fixture、讀取登入或解析 agent 輸出之前；不得先執行其中一步再檢查變數。
* R5: `FORGEPILOT_SMOKE_ARTIFACT_DIR` 或任何對應的 evidence export 設定是 fixture 外的測試專用設定；單獨設定它不得啟動任何 spy 或真實模型。
* R6: evidence export 的輸出必須經過 sanitization，不得保留任何環境變數帶出的憑證值。
* R7: spy 或真實模型的 exit code 或輸出文字宣稱成功，不得被記錄或呈現為 ForgePilot 的 Verification PASS 或 Evidence；PASS 只能來自 ForgePilot 自己的 canonical check 與既有 Verification 流程。
* R8: 每次測試使用一個新建的 disposable Git repository；測試不得寫入或影響呼叫者既有的 repository 或 `.forgepilot/` state。
* R9: 驗收必須確認 agent 在整個 onboarding 流程中不自動 commit、migrate、核准 review、解除 Gate 或 publish。
* R10: 此 Story 不得修改既有 `FORGEPILOT_CODEX_SMOKE` 的判斷邏輯或其 fixture。
* R11: The public plan is advisory. It statically inspects the Candidate and emits exact actions/effects; it never fetches, builds, verifies, changes the entrypoint, writes the target, invokes `make -n`, or executes Git filters during inspection.
* R12: A COMMIT Candidate is a lowercase full 40-character SHA that equals current target `HEAD`; a stale or symbolic value fails. SNAPSHOT follows the private-index/HEAD composition rules, including ignored/untracked Makefile behavior, and rejects transformations that need execution to inspect.
* R13: The harness must install and validate each real #34 adapter under its temporary Codex or Claude home, then consume #33's action records. It must not replace the artifacts with an adapter enum or a duplicate command list.
* R14: First explicit approval precedes every source action. The declared sequence is Go prerequisite → staging → clone → exact fetch/detached checkout → local build → `make verify` → startup → entrypoint switch. Missing Git, missing Go, or a failed source step leaves the entrypoint and target state untouched.
* R15: A valid existing Story is reused. A missing or unsafe Story causes a draft/review stop before target writes, including when `.forgepilot/` state already exists. Story components and leaf files must be contained non-symlinks; leaves must be regular files.
* R16: Second explicit approval precedes `forgepilot init`, `goal create`, and `work add`. The plan and harness never install Go or edit a shell profile.
* R17: Each opt-in is enabled only when its test-only variable is exactly `1`. All other values, including unset, skip before fixture creation, login access, agent-output parsing, artifact export, or spy execution.
* R18: When enabled, #35 reaches only a generated fixture-local spy with a scrubbed environment. It must neither resolve nor execute an ambient Codex/Claude binary. This does not satisfy #39's real-model compliance acceptance.
* R19: Exported test reports omit raw agent output and credentials and set `verification_run:false`.

## Expected Errors

* Any opt-in value other than exact `1` skips without side effects.
* A non-Git target, missing Git, unknown Candidate kind, non-HEAD COMMIT, unsafe/indeterminate SNAPSHOT, missing static `verify` target, or unsafe Story fails closed before target writes.
* Missing Go, a failed build/verification, declined first approval, declined second approval, or declined Story-draft authorization stops at that boundary.
* Invalid or fixture-contained export paths, duplicate export reports, or non-sanitized reports fail without overwriting a prior report.

## Dependencies

* #33 public procedure and action-plan seam.
* #34 adapters and `scripts/skills/check_adapters_test.sh` anti-drift checker.
* [ADR-0024](../../../docs/adr/0024-onboarding-stays-outside-the-offline-cli.md).
* #39 for real model execution and model-compliance acceptance.

## Constraints

* The final variable names above are test-only. They do not enable a production model path and do not authorize use of default-environment credentials.
* Every fixture is disposable; evidence is optional and lives outside it.
* Canonical full gates remain `make verify` and `go test -race -count=1 ./...`; focused FP-35 checks do not replace them.

## Guidance

Relevant:

* decision: ADR-0024 keeps onboarding external to ForgePilot's offline CLI.
* practice: `AGENTS.md` exact-value smoke-test opt-in pattern, with an earlier side-effect boundary.
* decision: ADR-0025 keeps trial/model claims distinct from ForgePilot verification.

Not applicable:

* Production onboarding API design. This Story owns a test executor only.

## Trust Boundary Fields

* `onboarding.optIn.codexAcceptance` — raw value of the test-only `FORGEPILOT_ONBOARDING_CODEX_ACCEPTANCE`.
* `onboarding.optIn.claudeAcceptance` — raw value of the test-only `FORGEPILOT_ONBOARDING_CLAUDE_ACCEPTANCE`.
* `onboarding.test.spyLog` — test-only generated-spy observation; never a model transcript.
* `evidence.export.output` — sanitized test report under `FORGEPILOT_SMOKE_ARTIFACT_DIR`.
* `agent.output.text` — deliberately omitted from persisted reports.
