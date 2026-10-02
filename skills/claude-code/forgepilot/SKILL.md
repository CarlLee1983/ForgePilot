---
name: forgepilot
description: 依 ForgePilot 的 next 建議，把一張 Goal DAG 上的工作一件件做完（start、實作、verify），需要人判斷時開 Gate 並停下。Use when a repository has a .forgepilot/ directory, or the user asks to advance a Goal, work through the next Work Item, or import a Goal Plan.
---

# ForgePilot Agent 迴圈

ForgePilot 是被動的帳本：它不啟動 Agent，只判定下一個合法動作、保存綁定確切 Candidate 的 Verification Evidence。迴圈由你來跑。指令與旗標以 `forgepilot --help` 為準；以 `/forgepilot` 呼叫本 skill。

## 迴圈

每一輪從 `forgepilot next --json` 開始，依 `action` 行動，做完再問一次 `next`——不要自己維護清單，也不要跳過它挑工作。

| action | 做什麼 |
|---|---|
| `START` | `forgepilot start <work_id>`，讀 `story_ref` 目錄下的 `story.md` 與 `acceptance.md`，依 Story 實作 |
| `RESUME` | 工作已在進行：讀 Story 與目前工作樹，接著實作 |
| `REPAIR` | 最新一次 verify 為 FAIL：讀該次輸出的 `Log:` 路徑（`.forgepilot/logs/` 底下），修復後再 verify |
| `RECOVER`、`REVERIFY` | 執行 `instruction` 欄位給的指令（`forgepilot verify <id>`，必要時已含 `--snapshot`） |
| `WAIT` | 停下。逐項告知使用者 `waiting[]` 在等什麼：`GATE` 等人 `gate resolve`、`REVIEW` 等人 `review approve`、`VERIFICATION` 是另一個 verify 正在跑 |
| `GOAL_ALREADY_COMPLETED`、`GOAL_CANCELLED`、`NONE` | 迴圈結束，向使用者回報 |

實作完成後驗證：

```bash
forgepilot verify <work_id>              # 驗已 commit 的 HEAD；工作樹必須乾淨（含未追蹤檔）
forgepilot verify <work_id> --snapshot   # 驗未 commit 的工作樹，不需要 WIP commit
```

驗證在隔離的 detached checkout 跑 repository 自己的 `make verify`，所以它必須能在全新 checkout 上通過。PASS 之後：Goal 沒有 Approval Requirement 時工作直接成為 DONE，下游同一交易解鎖，下一輪 `next` 給下一件；有 Approval Requirement 時工作進入 REVIEW，下一輪 `next` 會是 `WAIT`。FAIL 時工作留在 RUNNING，下一輪 `next` 給 `REPAIR`。

## 邊界

- 同一時間只有一件工作 RUNNING 或 VERIFYING。`start` 被拒時，錯誤訊息指出佔位的工作；先處理它，不要繞過。
- 沒有完成指令。DONE 只會是 `verify` PASS（或人 `review approve`）的結果，不要自行宣稱完成，也不要編輯 `.forgepilot/state.json`。
- `gate resolve`、`gate cancel`、`review approve`、`review reject` 是人的決定。只在使用者明確指示時代為執行，並用 `--by` 記下使用者的名字。
- 通過的是 `make verify`，不是你的自我測試；不要為了讓它綠燈而改弱檢查。

## 需要人判斷時

架構取捨、範圍變更、有安全影響的選擇、Story 本身有歧義——不要自己選一個繼續寫，開一個 Gate 擋住該工作，然後停下告知使用者：

```bash
forgepilot gate open --work <work_id> \
  --question "<要決定的事>" --option "<選項一>" --option "<選項二>" \
  --reason "<為什麼需要人決定>"
```

至少兩個選項。Gate 開著時該工作不能 `start` 或 `verify`，`next` 會回 `WAIT` 並指出 Gate ID；使用者 resolve 之後，下次 `next` 才會再推薦它。

## Goal Plan 與匯入

DAG 由一份 Goal Plan（JSON）建立；拆單與拆得對不對是使用者與上游的責任，你只在使用者交付計畫檔時匯入：

```bash
forgepilot goal import <plan-path>
```

```json
{
  "goal": { "id": "billing-sync", "title": "...", "require_approval": false },
  "nodes": [
    { "id": "schema", "story": "specs/stories/schema", "depends_on": [] },
    { "id": "sync-job", "story": "specs/stories/sync-job", "depends_on": ["schema"] }
  ]
}
```

節點 `id` 就是 Work Item ID，`start`、`verify` 都用它；`story` 必須是 repository 內存在的目錄。計畫有任何錯（環、未知或重複依賴、不存在的 Story 路徑、未知欄位）整份不寫入，錯誤訊息指出節點與欄位。補單時改計畫檔再匯入同一個 Goal：只能新增節點，既有節點必須全數列出且內容不變。

不確定現況或不知道某件工作為何不能前進時，執行 `forgepilot status`（`--goal`、`--work`、`--json`）：每件未完成工作都會說明阻擋的原因。
