# Acceptance Criteria

## Happy Path

* [ ] AC-001: 在受支援的 macOS 15+ arm64 或 amd64 上執行 installer，取得對應架構的固定版本
  資產，並依序通過通道標記、digest、簽章、簽署者與 notarization 閘門後完成安裝；本 Story
  的 notarization 判定以可設定的測試結果驗證，真正由 Apple 核發的 accepted 判定留待 #37。
* [ ] AC-002: 安裝前重新計算下載資產的 SHA-256，且必須與公布的 digest 相符才繼續下一步查驗。
* [ ] AC-003: 通過 digest 核對後驗證資產的 Developer ID 簽章為有效簽章才繼續下一步查驗。
* [ ] AC-004: 通過簽章驗證後核對簽署者身分與設定的預期 Team ID／簽署要求相符才繼續下一步查驗。
* [ ] AC-005: 通過簽署者核對後呼叫 notarization assessment 查驗，並以其結果作為是否進入安裝
  步驟的閘門：查驗結果為 accepted 才繼續安裝，查驗結果為 rejected 則安裝在此步驟停止；本
  Story 以可設定的測試判定結果驗證兩種結果，不宣稱資產本身真的通過 Apple 的 notarization。
* [ ] AC-006: 安裝結果落在使用者擁有的版本化目錄中，且目前入口只在（含上述 notarization
  閘門的）安裝完成後原子切換到該目錄。
* [ ] AC-007: PATH 未涵蓋安裝目錄時，輸出可直接使用的絕對 binary 路徑與人工 PATH 設定指引。

## Business Rules

* [ ] AC-008: 以相同版本重跑 installer 兩次皆安全完成，且兩次結果的入口路徑與安裝目錄一致；
  兩次執行皆使用與 AC-001 相同的測試用 notarization 判定結果。
* [ ] AC-009: Installer 的執行過程不呼叫 `sudo`、不以 curl pipe shell 形式執行，也不修改
  shell profile 檔案。
* [ ] AC-010: Installer 的執行過程不寫入受管理 repository 或 `.forgepilot/` state、不執行
  `migrate`、不讀取或寫入 agent 登入憑證。
* [ ] AC-011: 查驗步驟依通道標記、digest、簽章、簽署者、notarization 的順序執行，任一步驟
  失敗時後續步驟不再執行。
* [ ] AC-012: 使用者可只用 macOS 內建工具（例如 `shasum`）核對已公布資產的 SHA-256，且
  installer 的使用者路徑（下載、核對、安裝、PATH 指引）全程不呼叫 `gh`。

## Failure Cases

* [ ] AC-013: 在不支援的平台或架構上執行時，installer 在下載任何資產之前失敗並說明不支援
  的平台或架構。
* [ ] AC-014: 下載中途中斷時失敗，未進行任何查驗或安裝，且既有可用入口不變。
* [ ] AC-015: 下載資產的 SHA-256 與公布值不符時失敗，不驗證簽章，且既有可用入口不變。
* [ ] AC-016: 資產的 Developer ID 簽章無效時失敗，不核對簽署者，且既有可用入口不變。
* [ ] AC-017: 資產的簽署者與設定的預期身分不符時失敗，不驗證 notarization，且既有可用入口
  不變。
* [ ] AC-018: 預期簽署者身分尚未設定時（例如 #30 尚未提供 Team ID／簽署要求），installer
  在驗證簽章前即失敗並說明必須先完成 #30，不得以任何預設或空白身分繼續。
* [ ] AC-019: Notarization assessment 被系統拒絕時失敗，不進行安裝，且既有可用入口不變。
* [ ] AC-020: 安裝目錄寫入失敗（例如權限不足）時失敗，不留下部分寫入的版本化目錄，且不切換
  入口。
* [ ] AC-021: 下載到的資產通道標記與 installer 實際請求的通道不符時（例如請求正式版卻收到
  試用版資產）失敗，不繼續 digest 核對，且既有可用入口不變。

## Regression Requirements

* [ ] AC-022: `internal/cli`、`internal/app`、`internal/repository`、`internal/work` 的既有
  行為不變。
* [ ] AC-023: `internal/cli`、`internal/app`、`internal/repository`、`internal/work` 未新增
  下載、打包或網路呼叫的相依，未取得下載或打包責任。
* [ ] AC-024: ForgePilot 的 Go 程式碼仍只使用標準函式庫，未新增 Go 相依。
* [ ] AC-025: Repository 的 `make verify` 與 `go test -race -count=1 ./...` 通過。

## Acceptance Evidence

| AC | Method | Evidence | Fixture / precondition | Expected observation |
| --- | --- | --- | --- | --- |
| `AC-001` | test | `scripts/release/installer_test.sh` | `valid signed candidate asset on supported arch with notarization result overridden to accepted for test purposes` | `installer completes and new version is active` |
| `AC-002` | test | `scripts/release/installer_test.sh` | `downloaded asset with published digest` | `recomputed shasum matches published digest before signature step` |
| `AC-003` | test | `scripts/release/installer_test.sh` | `digest-verified asset` | `codesign reports a valid signature before signer step` |
| `AC-004` | test | `scripts/release/installer_test.sh` | `signature-verified asset and configured expected signer` | `signer identity matches expected signer before notarization step` |
| `AC-005` | test | `scripts/release/installer_test.sh` | `signer-verified asset with notarization result overridden to accepted, and the same asset with the override set to rejected` | `install proceeds to the install step only when the override reports accepted; installer stops before the install step when the override reports rejected` |
| `AC-006` | test | `scripts/release/installer_test.sh` | `completed successful install run using the same test-only notarization override as AC-001` | `binary resides under a versioned user-owned directory and entrypoint points to it` |
| `AC-007` | test | `scripts/release/installer_test.sh` | `PATH without the install directory` | `absolute binary path and PATH guidance printed` |
| `AC-008` | test | `scripts/release/installer_test.sh` | `same version run twice in sequence, each using the same test-only notarization override as AC-001` | `identical install directory and entrypoint target on both runs` |
| `AC-009` | test | `scripts/release/installer_test.sh` | `installer script source and run transcript` | `no sudo invocation, no curl pipe shell, no shell profile write` |
| `AC-010` | test | `scripts/release/installer_test.sh` | `repository and .forgepilot state before and after run` | `no diff in repository, .forgepilot state, migrate invocation or credential files` |
| `AC-011` | test | `scripts/release/installer_test.sh` | `asset forced to fail at each verification stage in turn` | `no later stage executes once an earlier stage fails` |
| `AC-012` | test | `scripts/release/installer_test.sh` | `published digest file and installer script source` | `shasum verifies the published digest and the installer invocation contains no gh call` |
| `AC-013` | test | `scripts/release/installer_test.sh` | `unsupported platform or architecture` | `non-zero exit before any download attempt` |
| `AC-014` | test | `scripts/release/installer_test.sh` | `download interrupted mid-transfer` | `non-zero exit and existing entrypoint unchanged` |
| `AC-015` | test | `scripts/release/installer_test.sh` | `asset with mismatched digest` | `non-zero exit and existing entrypoint unchanged` |
| `AC-016` | test | `scripts/release/installer_test.sh` | `asset with invalid signature` | `non-zero exit and existing entrypoint unchanged` |
| `AC-017` | test | `scripts/release/installer_test.sh` | `asset signed by an unexpected signer` | `non-zero exit and existing entrypoint unchanged` |
| `AC-018` | test | `scripts/release/installer_test.sh` | `expected signer left unconfigured (empty/unset)` | `non-zero exit before signature verification, naming the missing #30 configuration, existing entrypoint unchanged` |
| `AC-019` | test | `scripts/release/installer_test.sh` | `asset failing notarization assessment` | `non-zero exit and existing entrypoint unchanged` |
| `AC-020` | test | `scripts/release/installer_test.sh` | `install directory write forced to fail` | `non-zero exit, no partial versioned directory, entrypoint unchanged` |
| `AC-021` | test | `scripts/release/installer_test.sh` | `asset whose channel marker does not match the requested channel` | `non-zero exit before digest verification and existing entrypoint unchanged` |
| `AC-022` | command | `make verify` | `repository checkout` | `exit 0` |
| `AC-023` | command | `go list -deps ./internal/...` | `repository checkout` | `dependency set for internal/cli, internal/app, internal/repository and internal/work unchanged from the pre-change baseline, no download or archive-handling packages added` |
| `AC-024` | command | `go list -deps ./...` | `repository checkout` | `only standard library and ForgePilot module paths listed` |
| `AC-025` | command | `go test -race -count=1 ./...` | `repository checkout` | `exit 0` |

## Security Fixture Matrix

| Source field | Payload | Expected result | Persisted locations | Verification |
| --- | --- | --- | --- | --- |
| `commit_or_asset_location` | `unknown-commit-sha-placeholder` | reject | `installer run log` | `scripts/release/installer_test.sh` |
| `asset.channel` | `trial-channel-asset-requested-as-formal` | reject | `installer run log` | `scripts/release/installer_test.sh` |
| `expected_signer` | `MISMATCHED_TEAM_ID` | reject | `installer run log` | `scripts/release/installer_test.sh` |
| `expected_signer` | `unset` | reject | `installer run log` | `scripts/release/installer_test.sh` |
| `asset.sha256` | `corrupted-asset-bytes` | reject | `installer run log` | `scripts/release/installer_test.sh` |
| `developer_id.signer` | `unexpected-developer-id-signer` | reject | `installer run log` | `scripts/release/installer_test.sh` |
| `notarization.assessment` | `notarization-rejected` | reject | `installer run log` | `scripts/release/installer_test.sh` |
| `entrypoint.state` | `pre-existing v1 entrypoint symlink` | preserve | `versioned install directory` | `scripts/release/installer_test.sh` |

## Verification Notes

* `AC-001` 至 `AC-021` 使用 #30 尚未固定正式簽署者前的可設定測試簽署身分，與 #31 產出格式
  相容的測試資產（含架構推導規則與通道標記兩層命名）；不要求連到正式 GitHub Release。
* `AC-001`、`AC-005`、`AC-006`、`AC-008` 對 notarization 判定使用可設定的測試結果覆寫；真正
  由 Apple 核發的 accepted 判定需要真正簽署且已送件 notarize 的資產，超出本 Story 測試資產
  的能力範圍，不是這張票的完成條件；端到端驗證留給 #37 的受保護 CI release environment
  之後。
* `AC-006` 的原生 Apple Silicon 與 Intel 安裝驗收留給 #38，這份 Story 只驗證版本化目錄與
  原子切換行為本身，不宣稱原生機器上的完整首次使用體驗。
* `make verify` 與 `go test -race -count=1 ./...` 是 repository 的 canonical gate；這份
  Story 的新增測試不取代它們。
* 任何 AC 都不得以「installer 指令回傳 0」單獨作為通過依據，須配合對應查驗步驟的具體觀察。
