# ADR-0037：Goal completion 不等待人工終審

## Status

accepted

## Context

`forgepilot run --goal <id>` 的目的，是在有界預算內推進整個 Goal。當所有 Work Item 都已
完成實作、current Candidate 上的 canonical verification 全數 PASS，且沒有 OPEN Gate 時，
再要求一個獨立的 Goal 人工終審會讓已達成工程條件的 Goal 停在等待狀態。這不應是
GOAL policy 的合法終點；需要人決定的工程問題仍由 Gate 表達。

這項決策只涉及 Goal 終點，不改變 `WORK_ITEM` policy 的 per-Work-Item Human Review，
也不代表 merge、release、production operation 或外部產品接受已獲授權。

## Decision

- `review_policy=GOAL` 只有一種完成行為：所有 Work Item 均為 `VERIFIED`，每件最新
  Verification 是目前 COMMIT／SNAPSHOT Candidate 上的 PASS，且沒有 OPEN Gate 時，
  typed `COMPLETE_GOAL` transaction 保存 aggregate Goal Completion Evidence 並將 Goal
  設為 `COMPLETED`。
- GOAL 不接受 `--completion-policy human|verified`，也不會產生新的
  `WAIT_GOAL_REVIEW`／`AWAITING_GOAL_REVIEW` stop。`next` 仍只回報 `COMPLETE_GOAL` 建議；
  Runner 透過 application／domain transaction 重驗條件後完成 Goal。
- `completion_policy` 暫留在 persisted Goal 與 `forgepilot.cli/v1` JSON 作相容性欄位，
  但它由 Review Policy 唯一推導：`WORK_ITEM` 對應 `HUMAN`，`GOAL` 對應 `VERIFIED`。
  CLI 不把它當成可選設定。Work Item Review Policy、Review Evidence 與 `review approve`
  契約保持不變。
- Schema v12 的明確 migration 將 v11 的 GOAL/HUMAN 改為 GOAL/VERIFIED，建立既有
  `state.json.v11.bak` 備份，並保留 Goal 的原 lifecycle 狀態。原本已是 COMPLETED 的
  HUMAN Goal 會攜帶獨立的 legacy provenance（來源 schema 11、來源 policy HUMAN）；它不
  是 aggregate completion evidence，也不聲稱有 current Candidate、PASS 或 Gate 證據。尚未
  完成的 Goal 只正規化 policy，不會因 migration 變成 COMPLETED；只有取得 current
  repository facts 並核對 exact Verification Evidence IDs 的交易能新完成 Goal。
- 舊 Run Record 的 `AWAITING_GOAL_REVIEW` 保留為歷史 stop 值，供 status 與 exit-code mapping
  讀取；migration 不改寫 run history，新 Runner 不會產生此值。舊 active run 在 state
  migration 後採用遷移後的唯一 GOAL completion policy，接著按原 run 預算與 current state
  續跑。

## Consequences

- 一旦 Goal 的 current verification 條件成立，Runner 可直接得到 `GOAL_COMPLETED`，不需
  人工批准第二次。
- Machine PASS 只代表指定 immutable Candidate 通過 repository canonical check；它不是
  Human acceptance、merge／release authorization 或產品／營運核准。
- Schema v11 的 HUMAN Goal 在明確 migrate 後採用唯一的 GOAL completion policy。既有
  COMPLETED 狀態會保留，但只帶 legacy provenance；它不能通過自動完成的冪等回傳、Runner
  crash recovery 的 Candidate-read 證明，或 summary 的 Verification Evidence 宣稱。
- v12 尚未發布，因此本次以 optional Goal 欄位擴充 v12，不升到 v13，也不改寫 marker-free
  的現有 v12 state。舊版 v12 binary 會因 strict decoding 拒絕帶有此欄位的 migrated state；
  回退需還原精確的 `state.json.v11.bak`，不能只回退 binary。
- `WORK_ITEM` policy 的既有 Human Review 不受影響。要在個別工作上保留人工審查，使用
  `--review-policy work-item`，不是新增 Goal final-review 停止模式。

## Alternatives rejected

- 保留 `--completion-policy human`：同一個 GOAL 執行模式會有兩種相互衝突的完成含義，
  並讓長時間 Goal 在全部工程條件通過後仍等待人工。
- 在 schema v11 原地重新解釋 HUMAN：舊 binary 與新 binary 會對同一份 state 得出不同
  語意，且沒有 migration backup；因此升 schema 至 v12。
- Migration 直接把已 ready 的 Goal 改為 COMPLETED：storage migration 沒有鎖內 current
  Candidate facts 與完整 transaction 判準，不能代替正常 completion transition。
- 將已完成 v11 HUMAN Goal 的 legacy 標記塞入 `GoalCompletionEvidence`：該 record 是目前
  Candidate／Verification 的 aggregate proof，Runner recovery 也會以其存在作為 facts-read
  transaction 已提交的證據；重用它會混淆互斥的 provenance。
- 拒絕所有已完成 v11 HUMAN Goal：這會違反保留 lifecycle status 的 migration 契約，並使
  合法舊 state 無法升級。

**Falsified if:** `goal create` 能替 GOAL 選擇 HUMAN completion、current GOAL workflow 會
產生新的 `AWAITING_GOAL_REVIEW`、v11→v12 migration 將未完成 Goal 標成 `COMPLETED`、legacy
provenance 被當成 current Candidate／Verification proof，或 WORK_ITEM policy 的 Human Review
行為因此改變。
