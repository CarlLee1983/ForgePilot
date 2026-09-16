---
status: accepted
---

# Agent Session checks are diagnostic; Runner owns Verification

ForgePilot 的 `Verification` 專指對 immutable Candidate 執行 repository canonical check 並保存
Evidence。Runner 啟動的 Agent Session 需要自行測試變更，卻在 dogfood 中因 root instructions 重複
執行 broad／race gates，並在 Codex `workspace-write` sandbox 內因 `/bin/ps` 被拒而得到與外層正式
PASS 相反的環境結果。這不是兩種 PASS，而是兩個 owner 與兩種執行能力沒有在 briefing 中分開。

因此 Runner 的每份 handoff 必須帶 required **Agent Session Check Profile**。Agent Session 擁有最低
有用的 focused checks；其輸出與 Agent Result 永遠只是診斷材料。Runner 在
`implementation_finished` 後仍只透過 `internal/app.Verify` 執行 canonical `make verify`，只有該
Candidate Verification Run 能產生 PASS Evidence。repository rules 另列但不在 canonical command
內的 broad／race gates，由 project instructions 指定的 integration/final owner 在 Human final
acceptance 前執行；profile 不假裝執行或保存那份證明。

profile 由 `internal/agent.Handoff` 擁有，因為那個 module 已經擁有 required context、byte priority、
prohibitions 與 result contract。Runner 只把 `RESUME`／`REPAIR` 與 coding runtime 的 typed environment
facts 傳入；Codex 明說 ForgePilot 配置 `workspace-write`，fake 明說 sandbox
`not_configured_by_forgepilot`，兩者都不宣稱未知的 OS、network、credential 或 system-command 能力。
同一個 typed sandbox constant 必須同時驅動 Codex launch argument 與 handoff wording，避免兩份能力
陳述漂移。repair profile 必須帶最新正式
FAIL 的完整 log path，讓新 session 從受信任的 Verification failure 開始，而不是從前一個 agent
summary 猜測。

這個 profile 是 instruction-only；它不新增 command manifest、第二套 executor、Evidence type、state
或 Run Record 欄位，也不解析 worker log 來執法。真正被 enforce 的仍是既有信任邊界：Agent Result
沒有 PASS，Runner 不直接寫 lifecycle，Verification orchestration 只有 `internal/app` 一份。如果某個
non-canonical gate 未來必須阻擋 lifecycle，需另行設計正式 Evidence contract，不能把它藏進 handoff。

**Consequences:** existing repositories 無需 config 或 migration，但 project instructions 必須把 Runner
worker、Runner canonical 與 integration/final owner 的責任寫成不衝突的 scoped rules。Runner session
中的 repository-wide Story AC 執行責任會轉交給後兩者，不會被免除；其他 repository 若有更高優先級
規則，profile 不會偷偷覆蓋。sandbox denial
只代表「這個 session 沒跑成」，不能變成 code FAIL、PASS 或提高權限的理由。這個選擇不保證模型
遵守 focused guidance；安全性依靠正式 Verification，而成本改善需由後續 dogfood 觀察，不得寫成
deterministic acceptance。

**Falsified if:** Agent Session 的 output／exit code／summary 能建立 PASS Evidence；`internal/runner`
自行執行另一套正式 checks 或保存 lifecycle truth；handoff 省略 check ownership 或對 runtime sandbox
做出 adapter 未實際提供的能力聲明；repair session 可在沒有 latest formal FAIL log 的情況下猜測失敗；
或 repository-wide／race gate 被 handoff 靜默免除而沒有明確 integration/final owner。任一項發生，
focused diagnostic checks 與 formal Verification 的信任邊界已經移動，必須重新決定。
