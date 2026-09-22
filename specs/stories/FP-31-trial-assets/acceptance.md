# Acceptance Criteria

## Happy Path

* [x] AC-001: 以一個完整 commit SHA 執行建置入口一次，產生 darwin/arm64 與 darwin/amd64 兩份資產。
* [x] AC-002: 輸出含一份涵蓋每個資產的 SHA-256 manifest，且每筆 digest 與實際檔案 bytes 相符。
* [x] AC-003: 輸出含 provenance，記錄輸入 commit SHA、版本與每個資產的目標架構。

## Business Rules

* [x] AC-004: 資產命名的架構推導規則可由 Mac 架構單獨推導，arm64 與 amd64 各對應唯一一個資產名稱。
* [x] AC-005: unsigned trial 通道標記出現在資產名稱的通道標記成分、provenance 輸出與對應文件三處；架構推導規則本身不編碼通道。
* [x] AC-006: 架構推導規則可獨立於通道標記表達與驗證，使 #36 的正式簽署候選得以原樣重用該規則，而不繼承 unsigned trial 通道標記。
* [x] AC-007: 每個資產的實際 Mach-O 架構與其宣告的目標架構相符。
* [x] AC-008: 每個資產產生基本 CLI 啟動證據，而非只證明建置成功。
* [x] AC-009: 以相同 commit SHA 重跑入口安全完成，資產命名與 provenance 一致。
* [x] AC-010: 執行入口後 `.forgepilot/` state 未被寫入，且未建立 Git tag 或 commit。

## Failure Cases

* [x] AC-011: 未提供完整 commit SHA 時，入口在建置任何資產前失敗並指出缺少的輸入。
* [x] AC-012: 任一目標架構建置失敗時整體失敗，不留下只含單一架構的 manifest。
* [x] AC-013: 資產的實際 Mach-O 架構與宣告不符時失敗，並回報實際讀到的架構。
* [x] AC-014: CLI 啟動檢查失敗時失敗，該資產不被列入 manifest 為可用。

## Regression Requirements

* [x] AC-015: Repository 的 race gate 通過，無 data race 回報。
* [x] AC-016: `internal/cli`、`internal/app`、`internal/repository`、`internal/work` 的既有測試行為不變。
* [x] AC-017: `internal/cli`、`internal/app`、`internal/repository`、`internal/work` 未新增下載或打包相關的 import 或相依。
* [x] AC-018: ForgePilot 的 Go 程式碼仍只使用標準函式庫，未新增 Go 相依。
* [x] AC-019: 輸出與文件不含 Developer ID 簽章、notarization、原生驗收或正式支援的宣稱。

## Acceptance Evidence

| AC | Method | Evidence | Fixture / precondition | Expected observation |
| --- | --- | --- | --- | --- |
| `AC-001` | test | `scripts/release/build_trial_assets_test.sh` | `clean checkout at a known commit SHA` | `two assets exist, one per target architecture` |
| `AC-002` | test | `scripts/release/build_trial_assets_test.sh` | `completed trial build output` | `recomputed shasum matches every manifest entry` |
| `AC-003` | test | `scripts/release/build_trial_assets_test.sh` | `completed trial build output` | `provenance records commit SHA, version and target architecture` |
| `AC-004` | test | `scripts/release/build_trial_assets_test.sh` | `completed trial build output` | `architecture-derivation rule maps arm64 and amd64 to distinct asset names` |
| `AC-005` | test | `scripts/release/build_trial_assets_test.sh` | `completed trial build output and docs` | `unsigned trial channel marker present in name channel component, provenance and docs; architecture-derivation rule alone carries no channel marker` |
| `AC-006` | test | `scripts/release/build_trial_assets_test.sh` | `architecture-derivation rule extracted without the channel marker` | `rule reproduces the same architecture mapping independent of the channel marker` |
| `AC-007` | test | `scripts/release/build_trial_assets_test.sh` | `completed trial build output` | `file reports Mach-O arm64 and x86_64 respectively` |
| `AC-008` | test | `scripts/release/build_trial_assets_test.sh` | `host architecture asset` | `CLI startup evidence captured for each asset` |
| `AC-009` | test | `scripts/release/build_trial_assets_test.sh` | `same commit SHA run twice` | `identical asset names and provenance fields` |
| `AC-010` | test | `scripts/release/build_trial_assets_test.sh` | `repository with existing .forgepilot state` | `state unchanged and no new tag or commit` |
| `AC-011` | test | `scripts/release/build_trial_assets_test.sh` | `invocation without a commit SHA` | `non-zero exit before any asset is produced` |
| `AC-012` | test | `scripts/release/build_trial_assets_test.sh` | `one architecture forced to fail` | `non-zero exit and no single-architecture manifest` |
| `AC-013` | test | `scripts/release/build_trial_assets_test.sh` | `asset with mismatched architecture` | `non-zero exit naming the observed architecture` |
| `AC-014` | test | `scripts/release/build_trial_assets_test.sh` | `asset whose startup check fails` | `non-zero exit and asset absent from usable manifest` |
| `AC-015` | command | `go test -race -count=1 ./...` | `repository checkout` | `exit 0` |
| `AC-016` | command | `make verify` | `repository checkout` | `exit 0` |
| `AC-017` | command | `go list -deps ./internal/cli/... ./internal/app/... ./internal/repository/... ./internal/work/...` | `repository checkout` | `no archive, compress or net/http package present among listed dependencies` |
| `AC-018` | command | `go list -deps ./...` | `repository checkout` | `only standard library and ForgePilot module paths listed` |
| `AC-019` | human | `review record` | `trial build output and docs diff` | `no signing, notarization, native acceptance or formal support claim` |

## Verification Notes

* `AC-008` 只能在與資產相符的主機架構上直接執行；跨架構的啟動證據留給 #38 的原生驗收，不在此 Story 宣稱。
* `make verify`（AC-016）與 `go test -race -count=1 ./...`（AC-015）是 repository 的 canonical gate；這份 Story 的新增測試不取代它們。
* 任何 AC 都不得以「建置指令回傳 0」單獨作為通過依據。
