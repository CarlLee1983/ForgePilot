# Acceptance Criteria

## Happy Path

* [ ] AC-001: Workflow 契約宣告以 `workflow_dispatch` 提供一個完整 40 字元 hex
  commit SHA 啟動一次，只輸出 darwin/arm64 與 darwin/amd64 兩份候選資產作為
  Actions candidate artifact，不含其他 artifact 輸出。
* [ ] AC-002: 候選資產命名沿用 FP-31 已驗收的架構推導命名規則與 manifest 契約，
  但不帶有 FP-31 的 trial 通道標記。
* [ ] AC-003: Provenance 的 schema 要求記錄輸入 commit SHA、workflow run id/URL、
  簽署者欄位、每個資產的目標架構與 digest；credential-free fixture 執行時，
  這些欄位被實際填入（簽署者欄位以 fixture 值填入）。

## Business Rules

* [ ] AC-004: Workflow 定義只宣告 `workflow_dispatch` 觸發，不存在 push 或 tag
  觸發路徑。
* [ ] AC-005: 契約宣告簽署與 notarization 步驟只在受保護 environment 執行，並固定
  該 environment 之後要配置的公開變數名稱、secret 名稱與 reviewer 邊界（見
  story.md `## Provisional Configuration Names`，尚未與 #30、#37 確認）。
* [ ] AC-006: `pull_request` 事件觸發的 job 定義（靜態檢查）本身不含任何對受保護
  environment、其變數或 secret 的引用。
* [ ] AC-007: 契約包含對每個候選資產重新驗證 SHA-256、Mach-O 架構、Developer ID
  簽章、與 #30 固定的預期簽署者變數相符、以及 notarization acceptance 的步驟。
* [ ] AC-008: Workflow 輸出範圍只有本次 run 專屬的 Actions candidate artifact；
  定義中不含建立 draft、上傳 Release asset、建立或搬動 tag，或呼叫 publish 端點
  的步驟。
* [ ] AC-009: 任何 log 或 artifact 只記錄 secret 名稱與是否成功取得，不含 secret
  實際值。
* [ ] AC-010: 控制流以不需要真實 Apple credentials 的 credential-free fixtures
  與靜態檢查驗證，且驗證輸出不被標記或宣稱為正式 Candidate。

## Failure Cases

* [ ] AC-011: 未以 `workflow_dispatch` 啟動，或未提供完整 40 字元 hex commit SHA
  時，在啟動任何建置 job 前失敗並說明缺少或格式錯誤的輸入。
* [ ] AC-012: 以 fixture 在本機模擬 `pull_request` 觸發的 job 邏輯執行路徑
  （行為檢查）時，該模擬執行在任何受保護變數／secret 值被取得之前就失敗。
* [ ] AC-013: 候選資產帶有自簽 fixture 簽章、其簽署者與 #30 固定的預期簽署者
  不符時整支 run 失敗，並指出實際讀到的簽署者，不標記該資產為有效候選。
* [ ] AC-014: 候選資產配上一個模擬失敗的 notarization 回應 fixture 時整支 run
  失敗，不產生標示為候選的 artifact。
* [ ] AC-015: 任一目標架構建置或重新驗證失敗時整支 run 失敗，不留下單一架構的
  candidate artifact。
* [ ] AC-016: Provenance 缺少 commit、workflow run、簽署者、架構或 digest 任一
  欄位時整支 run 失敗，不留下宣稱完整但缺項的 provenance。

## Regression Requirements

* [ ] AC-017: `internal/cli`、`internal/app`、`internal/repository`、`internal/work`
  的既有行為不變。
* [ ] AC-018: ForgePilot 的 Go 程式碼仍只使用標準函式庫，未新增 Go 相依。
* [ ] AC-019: `make verify` 與 `go test -race -count=1 ./...` 通過。

## Acceptance Evidence

| AC | Method | Evidence | Fixture / precondition | Expected observation |
| --- | --- | --- | --- | --- |
| `AC-001` | test | `scripts/release/candidate_workflow_static_check.sh` | `full workflow definition` | `workflow declares exactly two artifact outputs, one per target architecture, and no Release-asset or draft step` |
| `AC-002` | test | `scripts/release/candidate_workflow_fixture_test.sh` | `credential-free fixture build producing two unsigned candidate assets` | `asset names reuse the FP-31 architecture-derivation rule and manifest fields match the FP-31 manifest contract, with no FP-31 trial channel marker present` |
| `AC-003` | test | `scripts/release/candidate_workflow_fixture_test.sh` | `credential-free fixture run with a fixture signer identity value` | `provenance schema requires a signer field, and the fixture-populated provenance output records commit SHA, workflow run id/URL, the fixture signer value, architecture and digest for each asset` |
| `AC-004` | test | `scripts/release/candidate_workflow_static_check.sh` | `workflow trigger definition` | `only workflow_dispatch is declared, no push or tag trigger present` |
| `AC-005` | test | `scripts/release/candidate_workflow_static_check.sh` | `signing and notarization job definition` | `job runs under a declared protected environment naming its variables, secrets and required reviewers` |
| `AC-006` | test | `scripts/release/candidate_workflow_static_check.sh` | `pull_request-triggered job definition (structural check)` | `job definition contains no reference to the protected environment, its variables or its secrets` |
| `AC-007` | test | `scripts/release/candidate_workflow_static_check.sh` | `candidate verification job definition` | `steps re-verify SHA-256, Mach-O architecture, Developer ID signature, expected signer variable and notarization acceptance` |
| `AC-008` | test | `scripts/release/candidate_workflow_static_check.sh` | `full workflow definition` | `no step creates a draft, uploads a Release asset, creates or moves a tag, or calls a publish endpoint` |
| `AC-009` | test | `scripts/release/candidate_workflow_fixture_test.sh` | `credential-free fixture run with fixture secret values` | `run log and artifacts contain secret names only, never fixture secret values` |
| `AC-010` | test | `scripts/release/candidate_workflow_fixture_test.sh` | `credential-free fixture run without real Apple credentials` | `run completes and its output is labeled fixture verification, not a formal Candidate` |
| `AC-011` | test | `scripts/release/candidate_workflow_fixture_test.sh` | `invocation without a commit SHA input` | `non-zero exit before any build job starts` |
| `AC-012` | test | `scripts/release/candidate_workflow_fixture_test.sh` | `pull_request event simulated against a local run of the job logic (behavioral check)` | `simulated job run fails before any protected variable or secret value would be obtained` |
| `AC-013` | test | `scripts/release/candidate_workflow_fixture_test.sh` | `candidate asset carrying a self-signed fixture signature with an unexpected signer identity` | `non-zero exit naming the observed signer, asset not marked a valid candidate` |
| `AC-014` | test | `scripts/release/candidate_workflow_fixture_test.sh` | `candidate asset paired with a mocked failing notarization response fixture` | `non-zero exit and no artifact labeled candidate` |
| `AC-015` | test | `scripts/release/candidate_workflow_fixture_test.sh` | `one architecture forced to fail` | `non-zero exit and no single-architecture candidate artifact` |
| `AC-016` | test | `scripts/release/candidate_workflow_fixture_test.sh` | `provenance missing one required field` | `non-zero exit and no incomplete provenance left behind` |
| `AC-017` | command | `go test ./internal/...` | `repository checkout` | `exit 0` |
| `AC-018` | command | `go list -deps ./...` | `repository checkout` | `only standard library and ForgePilot module paths listed` |
| `AC-019` | command | `make verify` | `repository checkout` | `exit 0` |

## Security Fixture Matrix

| Source field | Payload | Expected result | Persisted locations | Verification |
| --- | --- | --- | --- | --- |
| `workflow_dispatch.inputs.commit_sha` | `4b825dc642cb6eb9a060e54bf8d69288fbee4904` | preserve | `provenance.json#commit` | `scripts/release/candidate_workflow_fixture_test.sh` |
| `workflow_dispatch.inputs.commit_sha` | `4b825dc642cb6eb9a060e54bf8d69288fbee490` | reject | `(rejected before persistence)` | `scripts/release/candidate_workflow_fixture_test.sh` |
| `secrets.APPLE_DEVELOPER_ID_CERTIFICATE_P12` | `-----BEGIN PRIVATE KEY-----FIXTUREVALUE-----END PRIVATE KEY-----` | omit | `workflow run log and candidate artifacts` | `scripts/release/candidate_workflow_fixture_test.sh` |
| `secrets.APPLE_NOTARIZATION_API_KEY` | `fixture-notary-api-key-000000` | omit | `provenance.json` | `scripts/release/candidate_workflow_fixture_test.sh` |
| `candidate_provenance.signer_identity` | `Developer ID Application: ForgePilot LLC (FIXTUREID1)` | preserve | `provenance.json#signer` | `scripts/release/candidate_workflow_fixture_test.sh` |
| `candidate_provenance.workflow_run_url` | `https://github.com/CarlLee1983/ForgePilot/actions/runs/000000000` | preserve | `provenance.json#workflow_run` | `scripts/release/candidate_workflow_fixture_test.sh` |
| `candidate_provenance.asset_digest` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | preserve | `provenance.json#digest` | `scripts/release/candidate_workflow_fixture_test.sh` |
| `job.event_name` | `pull_request` | reject | `workflow job definition` | `scripts/release/candidate_workflow_static_check.sh` |

## Verification Notes

* `AC-010` 只驗證 credential-free 控制流；成功簽署與 notarize 的真實候選資產由
  #37 在已配置的受保護信任環境中驗收，不在此 Story 宣稱。
* `make verify` 與 `go test -race -count=1 ./...` 是 repository 的 canonical
  gate；這份 Story 的新增測試不取代它們。
* 任何 AC 都不得以「workflow 定義存在」單獨作為通過依據；契約項目一律以靜態檢查
  或 fixture 執行結果為證據。
* `AC-001` 只能由 credential-free 的靜態檢查建立，因為本機腳本無法得知
  GitHub Actions 實際產出了什麼；它驗證的是 workflow 契約宣告的 artifact 輸出
  範圍，而不是某次真實 Actions run 的產物。真實 run 產生兩份 Actions artifact
  這件事，留給 #37 在有受保護信任環境時驗收。
* `AC-006` 與 `AC-012` 都在驗證「`pull_request` job 不得取得受保護值」，但方法
  刻意不同：`AC-006` 是靜態檢查 job 定義本身有沒有引用受保護 environment；
  `AC-012` 是在本機以 fixture 模擬該 job 邏輯的執行路徑，確認它會在任何受保護
  值被讀取之前就失敗。兩者互補，不是重複。
* `AC-013`、`AC-014` 使用的簽章與 notarization 結果都是本機產生的 fixture（
  自簽憑證、模擬的 notary 回應），不涉及真實 Apple Developer ID 憑證或呼叫
  真實 notary API，因此可以在沒有受保護 environment 的情況下驗證失敗路徑。
* Security Fixture Matrix 中的 secret 名稱（`APPLE_DEVELOPER_ID_CERTIFICATE_P12`、
  `APPLE_NOTARIZATION_API_KEY`）與 story.md `## Provisional Configuration Names`
  所列名稱一致，但同樣是暫定名稱，尚未與 #30、#37 確認；#37 配置受保護
  environment 時可以沿用或重新命名。
