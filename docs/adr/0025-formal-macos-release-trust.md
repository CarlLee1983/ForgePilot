# 正式 macOS Release 先建立簽署與可驗證的發佈鏈

**Status relation:** [ADR-0030](0030-source-built-onboarding-without-apple-developer.md) 改定目前
supported onboarding 為 source-built，因為維護者不使用 Apple Developer Program；
[ADR-0032](0032-formal-macos-onboarding-is-apple-silicon-only.md) 再把正式 macOS 支援面限定為
Apple Silicon；[ADR-0033](0033-source-built-bootstrap-separates-distribution-from-onboarding.md)
新增的 source-built Bootstrap 不改變本 ADR 的 signed-prebuilt 門檻。本 ADR 保留為未來若要重新提供
signed prebuilt macOS binary 時不可放寬的信任門檻，不再是目前導入的前提；其中原本的雙架構要求由
ADR-0032 取代。

預編譯 binary 解除了 Go 的安裝前提，也使開發者與 agent 必須判斷下載到的是不是預期版本。未簽署資產加上 checksum 可以較快試用，但 checksum 無法單獨證明發佈者身分，macOS 也可能阻擋首次執行。因此正式的「貼 prompt 導入」承諾只適用於 Developer ID 簽署、與預先固定的 ForgePilot 簽署者 Team ID／明確簽署要求相符、且 notarized 的 Apple Silicon `arm64` macOS 15+ Release；未簽署試用流程不得稱為正式導入。[Apple 的 code-signing requirements 說明](https://developer.apple.com/documentation/technotes/tn3127-inside-code-signing-requirements)可用於定義與查驗預期簽署者。

正式 Apple Silicon 資產須來自已檢閱的 commit，並在原生 Apple Silicon Mac 測試新安裝、首次啟動與 `init → goal create → work add → status`，測試使用者環境不預裝 Go。正式發佈前驗證下載後的簽章、預期簽署者與 notarization acceptance，公布資產的 SHA-256 與 [GitHub asset digest](https://docs.github.com/en/rest/releases/assets)，並讓使用者能以 macOS 內建的 `shasum` 核對，無需先裝 `gh`。Installer 必須在 digest、簽章、簽署者或查驗失敗時保留既有可用版本。依 [Apple notarization 指引](https://developer.apple.com/documentation/security/customizing-the-notarization-workflow)，獨立 CLI／ZIP 無法直接 staple ticket，故第一次線上查驗屬於本期承諾；離線首次啟動需要另一種包裝與驗收。

短 prompt 與導入文件指向該 Release 的完整 commit SHA，不指向會移動的 `main` 或 `latest`。資產、checksum 與對應文件全數備妥後才形成可檢閱的 draft；建立 GitHub draft 與上傳資產本身也須取得外部寫入授權。Repository 啟用 [immutable releases](https://docs.github.com/en/code-security/concepts/supply-chain-security/immutable-releases)，正式 publish 後 tag 與資產不得移動。維護者明確核准正式 publish，而不是以推送 tag 隱含授權。Developer ID 與 notary 資源由維護者持有，簽署在受保護的 CI release environment 執行；agent 不讀取憑證值，環境配置與發佈分別取得授權。這份 ADR 是發佈門檻的定案，不宣稱目前已有簽署資產或 release workflow。

**Consequences:** 正式 binary 發佈需先取得 Apple 簽署與 notarization 資源、受保護的 CI environment，以及 Apple Silicon 原生測試結果。既有 source／`go install` 路徑可繼續使用，但不能冒充免 Go 的正式安裝體驗。Binary 回退與單向 state migration 分開處理；遷移後要使用舊版，須由人還原 `migrate` 保存的 state 備份。

**Falsified if:** 正式 Release 包含未簽署、簽署者不符或未 notarize 的 macOS binary、只靠同一個可變 Release 中的 checksum 宣稱來源可信、缺少 Apple Silicon 原生安裝驗收、文件連到移動中的版本，或 release workflow 在未經維護者核准時 publish。任一項發生，正式導入的信任承諾已被放寬，必須重新決定並記錄理由。
