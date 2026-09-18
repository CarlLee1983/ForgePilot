# 正式 macOS onboarding 僅支援 Apple Silicon

ForgePilot 的正式 macOS source-built onboarding 支援面限定為 Apple Silicon
（Darwin `arm64`）。維護者不維持原生 Intel Mac 驗收環境；在沒有同等原生證據的情況下承諾
Intel 支援，會把無法持續驗證的路徑變成產品契約。正式 planner 因此在 inspection-only 階段
拒絕非 Darwin `arm64` host，且公開文件不得把跨編譯或 unsigned `amd64` trial asset 當成 Intel
支援證據。

這項決定只縮小平台支援面，不放寬 ADR-0030 與 ADR-0033 的固定 source SHA、明確核准、canonical
verification 與原子 entrypoint 切換，也不改變 ADR-0025 對未來 signed prebuilt binary 的簽署、
notarization 與 immutable publication 門檻。ADR-0025 與 ADR-0030 中要求 Intel 與 Apple Silicon
雙架構原生驗收的部分由本 ADR 取代；若未來恢復 Intel 支援，必須另行決定並以原生 Intel Mac
對同一條正式 onboarding path 補齊證據。

**Consequences:** #38 只保存 Apple Silicon 原生驗收；#39 的真實 Agent 驗收也只對正式
Apple Silicon path 作出支援主張。既有 `darwin/amd64` unsigned maintainer trial 可以保留，因為它
不是正式 onboarding、Release 或支援承諾。未來若恢復 signed prebuilt path，在沒有新決定前也只
能宣稱 Apple Silicon 支援。

**Falsified if:** 正式 onboarding planner 在非 Darwin `arm64` host 繼續產生可執行計畫、文件或
agent 宣稱 Intel Mac 受支援、以跨編譯／hosted CI／unsigned `amd64` trial 取代原生 Intel 驗收，
或未經新決定就重新擴大正式平台支援面。
