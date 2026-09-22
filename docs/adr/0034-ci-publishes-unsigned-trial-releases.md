# CI 只發佈經核准的 unsigned maintainer trial prerelease

**Status relation:** 此 ADR 延伸 [ADR-0025](0025-formal-macos-release-trust.md) 的「draft 後人工
publish」邊界，但不改變它對正式 signed binary 的門檻；也遵守 [ADR-0030](0030-source-built-onboarding-without-apple-developer.md)
與 [ADR-0032](0032-formal-macos-onboarding-is-apple-silicon-only.md)：這些 binary 不是正式 onboarding
或 Intel 支援證據。

未簽署 trial 的人工建置已能產生可檢閱的雙架構 bundle，卻把來源綁定、完整 gate、checksum、draft
審閱與可恢復上傳留給維護者手動操作。這些是容易漏掉、又不該由 push tag 隱含授權的發佈責任。

新增的 `publish-trial-assets.yml` 只能以 `workflow_dispatch` 接受一個 lowercase full 40-character
commit SHA。它拒絕不在 `main` 歷史上的 commit，從 SHA 衍生唯一 tag
`unsigned-trial/<commit>`，不接受另一個可混淆的 tag input。Prepare job 在 Apple Silicon macOS runner
對 exact checkout 跑 `make verify` 與 race gate，建置並重驗完整六檔 bundle；之後把短期 Actions artifact
交給 stage。stage 只從受 branch-policy 保護的 environment secret 取得 Release write authority，先確認
repository 已啟用 immutable releases，才建立或安全續跑同一個 draft prerelease。它只接受既有同名且 digest 完全一致的 asset，補上缺檔但從不
`--clobber`、取代、刪除 asset 或 Release。publish job 由 `unsigned-trial-publish` protected environment
卡住；核准後以 Environment API 確認存在 required reviewer，再重驗 bundle、draft notes 與每個 digest，
才 publish，並確認 GitHub 回報 immutable。三個 job 都拒絕非 default-branch workflow ref，避免未審閱
分支在此 workflow revision 中把 release control plane 與已驗證來源拆開；這個 guard 不是對 repository
writer 的獨立授權邊界，相關限制與 operating assumption 見下段。

Repository administrator 必須在合併前啟用 immutable releases，建立 `unsigned-trial-publish` environment
並要求 maintainer reviewer（依 single-maintainer policy 決定是否可 self-review），建立
`unsigned-trial-stage` environment 並將兩者的 deployment branch policy 設為「Selected branches → 僅 `main`」，而非
「protected branches only」，以及為
`unsigned-trial/*` 設定禁止移動／刪除的 tag ruleset。workflow 不自行變更這些 repository settings，也
不持有 Apple signing、notarization、OIDC 或 attestation 權限。administrator 將兩個 environment secrets
配置到這兩個 environment，而不是 repository scope：`RELEASE_WRITE_TOKEN` 是只具 Contents-read/write 的
fine-grained token，`RELEASE_GUARD_TOKEN` 只具 Administration-read 與 Actions-read，前者才可操作 Release，
後者只作 immutable-release／environment protection preflight。GitHub `GITHUB_TOKEN` 必須維持 repository
設定的 read-only，workflow 也不請求 `contents: write`，作為 accidental release 的 defense in depth。這不是
repository writer 的權限分離：GitHub 允許能修改並 dispatch workflow 的 writer 在另一 ref 要求 write token。
因此本 ADR 的 operating assumption 是所有 repository write／Actions-dispatch authority 均屬 release-authorized
maintainer；若要讓一般 writer 不能發佈，必須把 publisher 移到另一個受控 release repository，或使用組織的
workflow execution protection，不能宣稱本 repository 的 YAML／Environment 可單獨提供該隔離。若
immutable-release preflight、environment protection 或 environment secret 未配置，發佈必須停止而非退化成
mutable release。

**Consequences:** 同一 commit 的 retry 對 matching partial draft 是安全的；digest 衝突、額外 asset、移動
的來源、非 draft／非 prerelease release 或 mutable publish 都 fail closed，且不自動刪除任何外部物件。
公開後只能以新 commit／新 tag 發佈新的 trial，舊 release 保留為證據。Hosted CI 的 native arm64 startup
check 只補足 trial bundle evidence，絕不提升為正式 macOS execution trust 或 source-built onboarding
acceptance。

**Falsified if:** workflow 被 tag push 自動觸發、接受可分離的 tag 與 commit、在未核准 protected
environment 前 publish、以 `--clobber`／delete 修改既有 release asset、在 immutable releases 關閉時照樣
publish、將 unsigned artifact 寫成正式安裝入口，或對 ForgePilot core CLI 新增網路請求。任一項發生，
發佈授權、完整性或信任邊界已被放寬，必須重新決定。
