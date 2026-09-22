# Candidate-level canonical verification fan-out

## Decision status

這份設計已完成 code trace、三個獨立 interface 方案比較，以及 Sol/high 對 atomicity、freshness、Gate、prerequisite、failure、recovery、compatibility 與 rollback 的複審。Human 已明確接受這項 concurrency、schema 與 Evidence provenance 取捨；決定記於 [ADR-0027](../../adr/0027-candidate-verification-pass-fans-out-by-run.md)，implementation 依本文件以 TDD 進行。

本 slice 只處理 Candidate-level canonical verification fan-out。Runner-worker verification profile、永久 verification cache、跨 Goal fan-out、Goal completion transition 與 distribution/release 工作全部不在範圍內。

## Outcome

一次由 Work Item 觸發的 Verification，對同一 Goal 中已經有 PASS、只是相對本次 immutable Candidate stale 的合法 REVIEW／VERIFIED 工作，只執行一次 repository canonical check。PASS 在一次受鎖的 state transaction 內，為 anchor 與仍合法的 fan-out recipients 各建立一筆 Work Item-specific Evidence；這些 Evidence 有不同 ID 與 Story association，但共享一個 Verification Run ID、Candidate、Resolved Runtime、command、result、完成時間與 Verification Log。

FAIL 與 INTERRUPTED 維持既有 anchor-only 語意。它們不把整批 stale VERIFIED 工作退回 RUNNING，也不讓 Runner 為一個全域 canonical failure 啟動多個不必要的 repair sessions。

本次 dogfood run 的上界是把 23 次 canonical executions 壓到 7 次 immutable Candidate executions；這是量測上界，不是寫進產品的固定假設。

## Interface and seam

外部 seam 保留在 `internal/app`，CLI 與 Runner 仍呼叫同一個 deep module：

```go
func Verify(
	ctx context.Context,
	root string,
	anchorWorkItemID string,
	output io.Writer,
	options VerifyOptions,
) (VerifyResult, error)
```

callers 不傳 recipient IDs、不解析 Candidate、不建立 Resolved Runtime，也不逐筆 append Evidence。這些規則全部留在 `internal/app` orchestration 與 `internal/work` 的純 domain query／batch transition 裡，避免 CLI 與 Runner 產生兩份 eligibility 或 transaction ordering。

`VerifyResult` additive 擴充：

```go
type FanoutSkip struct {
	WorkItemID string
	Reason     string
}

type VerifyResult struct {
	// 既有欄位維持 anchor 語意。
	Reclaimed   *work.Evidence
	Evidence    work.Evidence
	HasEvidence bool

	// 新欄位描述同一個 Verification Run 的完整結果。
	VerificationRunID string
	EvidenceSet       []work.Evidence
	ReclaimedSet      []work.Evidence
	Skipped           []FanoutSkip

	// 其他既有欄位不變。
}
```

- `Evidence` 永遠是 anchor Evidence。
- 有結果時 `EvidenceSet[0] == Evidence`；其後依 Work Item `created_at`、再依數字 ID 排序。
- `ReclaimedSet` 此 slice 只可能含 anchor 的 reclaimed INTERRUPTED Evidence。
- CLI 明確呈現被跳過的 optional recipients；Runner 把 `EvidenceSet` 的所有 ID 寫進 Run Record，然後重新向 typed query 取下一個合法動作。

這不是永久 cache。使用者之後明確重跑相同 Candidate 仍會建立新的 Verification Run；「一次」指一個 frozen fan-out operation 只有一個 canonical subprocess 與一個 log。

## Fan-out cohort

### Anchor

anchor 完整沿用既有 `CanBeginVerification`／`Verifiable` 規則與 failure behavior：先回收 orphan，再判斷 Gate、Goal、workspace、runtime 與 canonical target。它可以是 RUNNING、REVIEW 或 VERIFIED。

### Optional recipients

optional recipient 必須同時符合：

- 與 anchor 屬於同一個 Goal。
- 狀態為 REVIEW 或 VERIFIED。
- latest Verification 是 PASS，且相對本次 Candidate stale。
- Goal 為 ACTIVE，沒有 OPEN Gate，並通過既有 verifiability 規則。
- 在本次 simultaneous PASS set 下，prerequisites 仍依該 Goal 的 progression policy 滿足。

明確排除：

- 另一張 RUNNING 工作，即使 repository-wide check 會 PASS；否則可能跳過它自己的 implementation。
- PENDING、READY、VERIFYING、DONE。
- 已經 fresh 的 REVIEW／VERIFIED；不製造重複 Evidence。
- 其他 Goal；Runner 的單 Goal ownership 不因 global command 而擴張。

### Prerequisite closure

GOAL policy 下，一個 recipient 的 prerequisite 可由以下任一方式滿足：

- 已 DONE；
- 已有相對本次 Candidate fresh 的 VERIFIED PASS；
- 也是本次 surviving PASS set 的成員，會在同一 transaction 變成 VERIFIED。

WORK_ITEM policy 下，PASS 後仍回到 RUNNING，不能被當成同交易內已滿足的 prerequisite；其 prerequisites 仍必須是 DONE。

selection 與 completion 都用同一個 pure closure query。completion 若移除一個 changed／ineligible recipient，必須反覆重算 closure 到 fixed point；依賴該 recipient 的 transitive recipients 也要移除，除非它們的 prerequisite 已由其他方式獨立滿足。

## Temporal eligibility and concurrent governance

「每個 eligible Work Item」精確定義為：

> 在 begin transaction 被選入，並且在 completion transaction 仍未改變、仍合法、且仍落在 prerequisite-closed surviving set 的同 Goal optional recipient。

begin transaction 產生一個只存在於執行程序記憶體的 typed `FanoutPlan`。plan 捕捉所有 eligibility inputs：

- Work Item identity、Story reference、status 與完整 relevant state；
- owning Goal 的 status、repository 與 review policy；
- relevant Gate records；
- latest Verification Evidence；
- recursive prerequisite statuses、latest Verification Evidence 與 selected membership；
- 本次 Candidate freshness facts。

optional recipient 在 canonical check 期間發生任何 relevant change 時，completion 會明確把它列入 `Skipped`，再重算 transitive closure。這不否決 anchor 已誠實取得的 PASS。

anchor 的 concurrency contract 不變：它仍使用目前的 verdict fingerprint。Goal lifecycle 或 Gate 在合法 run 開始後改變，不抹去 anchor 已取得的 Evidence，但會阻擋後續 progression；optional recipient 則因 eligibility inputs 改變而被跳過。

## Verification Run identity and schema v9

schema v9 增加：

- root `next_verification_run_id`；格式 `VR-001`。
- `Run.verification_run_id`，對所有新／migrated `CurrentRun` 必填。
- `Evidence.verification_run_id`；Verification Evidence 必填，Review Evidence 必須為空。

Verification Run ID 是 execution identity，不是 `LogPath`，但新 log 會用它做 durable lookup。這一點會由 ADR-0027 明確修正 ADR-0012 的「Evidence 不指向輸出」決定：Evidence 仍不保存 filesystem path，卻會保存並使用 run ID，同時回答 shared execution provenance 與 shared log lookup。這不是沒有規則讀取的 metadata；Runner repair handoff、CLI diagnostics 與 state validation 都會讀它。

v8→v9 migration 為每筆歷史 Verification Evidence 配發不同的 `LVR-001` 形式 legacy Verification Run ID，因為舊版每筆 Evidence 確實各自來自一次 per-Work-Item execution；Review Evidence 不配發。每個 orphan `CurrentRun` 也配發自己的 LVR ID。LVR ID 不表示存在以該 ID 命名的 log，舊 `LogPath` 或 legacy lookup 仍是唯一可信位置；v9 之後的新 execution 只配發 `VR-*`，永不配發或重用 `LVR-*`。

新 log 以 run 為鍵，例如：

```text
.forgepilot/logs/VR-001-<short-sha>-<started-at>-<exclusive-attempt>.log
```

log creation 必須使用 exclusive create，不得以 `O_TRUNC` 開啟一個可能屬於其他 attempt 的檔案。`exclusive-attempt` 是不可預測、只為避免 collision 的 suffix，不參與 Verification Run identity；實際 path 仍在 `CurrentRun.LogPath` 保存。Runner 查找 `VR-*` Evidence 的 log 時必須用 `verificationRunID + "-"` 建立 token-bounded prefix（或解析 filename 的完整第一個 token），再要求唯一 match；不得直接用裸 run ID 做字串 prefix，否則 `VR-100` 會誤配 `VR-1000-*`。`LVR-*` Evidence 才沿用 `<work-id>-<short-sha>-*` legacy lookup。找不到或找到多筆都明確回報，不猜其中一份，也不讓遺失的新 log 誤配到舊檔。ADR-0027 必須記錄這個 lookup contract 與它對 ADR-0012 的修正。

配發順序必須保留「log open failure 不寫 state」：

1. read-only 取得 proposed next run ID；
2. 以 exclusive create 開啟帶 unique attempt suffix 的 run-keyed log；collision 只換 suffix，不截斷既有檔案；
3. begin transaction 重新比對 counter，成功才 increment、建立 anchor `CurrentRun` 並回傳 `FanoutPlan`；
4. counter 已改變時拒絕 begin，關閉並刪除**本 attempt 自己 exclusive-created 且尚未進入任何 `CurrentRun`** 的 empty log；不得刪除或覆寫其他檔案。

不能先 reserve ID 再開 log；也不能先建立 `CurrentRun` 再嘗試開 log。兩種順序都會破壞現有的 pre-run failure contract。

## Atomic PASS settlement

PASS completion 在一個 `storage.Update` callback 內依序完成：

1. 以既有 fingerprint 重驗 anchor verdict。
2. 以 begin-time `FanoutPlan` 與 current state 重驗 optional recipients。
3. 移除 changed／ineligible recipients，記錄 `Skipped`。
4. 重算 prerequisite closure 到 fixed point。
5. 在任何 mutation 前驗證完整 surviving set。
6. 為 anchor 與每個 survivor 配發 distinct Evidence ID；全部共享 run ID、Candidate、Resolved Runtime、command、PASS、timestamp。
7. 清除 anchor `CurrentRun`，依每張 Work Item 自己的 Goal Review Policy 套用既有 post-PASS status。
8. 所有 status 都到 final value 後，才做一次 dependency readiness refresh。

readiness refresh 必須在同一 transaction 內解析 live repository facts；不能拿 Verification Run 的 Candidate 代替目前 workspace。facts 讀取失敗時，整組 PASS Evidence 仍原子保存，promotion fail closed，並透過既有 `RefreshWarning` 回報。這與目前 single-item Evidence-first contract 相同。

任何 validation 或 state save 失敗都不得留下 partial Evidence set。sequentially 呼叫現有 single-item `RecordVerification` 不合格，因為它會在每張工作後 refresh dependents，使結果依 cohort iteration order 改變。

optional PASS 必須走專用的 batch-only domain transition：只有 matching run ID 的 VERIFYING anchor 可以啟動它，optional members 必須仍是 REVIEW／VERIFIED 且符合 plan。不得把一般 `appendEvidence` 放寬成任意非 VERIFYING 工作皆可寫 Verification Evidence。

## FAIL, interruption, and recovery

- canonical exit non-zero：只為 anchor 建立 FAIL Evidence，optional recipients 不變。
- timeout／signal：只為 anchor 建立 INTERRUPTED Evidence，optional recipients 不變。
- pre-run Refusal 或 operational failure：沒有新 Evidence；已回收的舊 anchor run 仍照既有契約回傳。
- crash after begin：只有 anchor 留在 VERIFYING；下一次 invocation 以既有 per-Work-Item lock 證明 orphan，回收成一筆 anchor INTERRUPTED Evidence。
- crash after canonical PASS、before settlement：PASS 沒有 durable Evidence，仍按 orphan 回收；不從 process output 猜結果。
- cleanup 無法確認：已保存 Evidence 照常成立，Runner 保存 pending execution 並停止，不啟動下一個 writer。

existing per-anchor verification flock 必須包住完整 call，且 orphan reclaim 發生在任何 Candidate/runtime/refusal 判斷之前。另加一個 repository-wide canonical-execution flock，固定 lock order 為 anchor lock → repository lock；它在同一 repository 內序列化正式 verification，避免兩個 anchors 同時跑同一個 global canonical command。per-item liveness probe 與 Runner orphan 判定不變。

精確順序是：取得 anchor lock、完成 anchor orphan reclaim，接著在任何新的 `CurrentRun`、Candidate capture、checkout、runtime preflight、log 或 canonical process 之前，以 non-blocking 方式取得 repository lock；lock 一直持有到本次 cleanup 結束。取得失敗是 pre-run in-flight refusal，不得留下新 `CurrentRun`、Evidence、checkout、log 或 subprocess。這個順序保留「reclaim before refusing」，也不會讓被 repository lock 拒絕的第二個 anchor 製造需要下一輪回收的假 orphan。

repository lock 不是歷史 cache，也不承諾 coalesce 兩個明確 requests：第二個 concurrent request 收到 in-flight refusal；之後的明確 rerun仍合法。這個 coarse lock 是有意的 concurrency trade-off，避免以 runtime version map 充當不完整 identity——沒有 declaration 時環境是 caller passthrough，而不同 executable 也可能回報相同版本。

## Runner and Goal completion

Runner 仍只保存 execution history：

- 一個 fan-out operation 消耗一個 step，建立一筆 pending canonical execution。
- `EvidenceSet` 中每個 Evidence ID 都加入 Run Record。
- Runner 不保存 cohort、不自行計算 eligibility、不直接改 lifecycle。
- 下一輪重新呼叫 typed query；沒有 cached progress。

每張 Work Item 仍有自己的 latest PASS Evidence ID；同一 shared run 產生的 IDs 只是共享 provenance。任何 skipped stale item 都會阻止 `COMPLETE_GOAL`；只有全部 latest PASS 仍對應 current Candidate 且沒有 OPEN Gate 時，typed transaction 才完成 Goal。

## Acceptance matrix

| Scenario | Observable result |
|---|---|
| anchor，沒有 stale peer | 一個 canonical process、一個 log、一筆 anchor Evidence；CLI 既有語意不變 |
| anchor + 兩個合法 stale REVIEW／VERIFIED | 一個 process、一個 log、三筆 distinct Evidence；共用 run ID／Candidate／runtime／timestamp |
| 另一張 RUNNING，沒有 fresh PASS | 明確排除，不因全域 check 跳過 implementation |
| PENDING／READY／VERIFYING／DONE／fresh peer | 排除，不新增 Evidence |
| optional 有 OPEN Gate 或 Goal 非 ACTIVE | 排除並回報 reason；同條件發生在 anchor 時依既有規則 Refusal |
| 其他 Goal 有 stale Evidence | 排除；不擴張 Runner Goal scope |
| stale GOAL-policy dependency chain 全部合法 | 由 simultaneous closure 納入同一 PASS transaction |
| ancestor 在 check 中改變或失去 eligibility | ancestor 與所有不再 closed 的 transitive dependents 都列入 `Skipped` |
| WORK_ITEM-policy member 是另一張工作的 prerequisite | 其本次 PASS 不算 simultaneous dependency satisfaction |
| PASS | anchor + surviving recipients 原子 append；先完成全部 status，再 refresh readiness 一次 |
| FAIL | 只有 anchor FAIL；optional recipients 維持原 Evidence/status |
| timeout／signal | 只有 anchor INTERRUPTED；不推論 FAIL |
| optional relevant facts 在 check 中改變 | 跳過該 item；anchor PASS 不被無關改動否決 |
| anchor verdict inputs 在 check 中改變 | `ErrStateWrittenDuringCheck`；不相信 execution result |
| Goal/Gate 在 anchor run 開始後改變 | anchor Evidence 保留；progression 仍受 current governance 阻擋 |
| Candidate 在執行期間移動 | Evidence 綁 immutable tested Candidate，並可立即投影為 stale |
| live repository facts 在 settlement 讀取失敗 | 整組 PASS Evidence 保存；readiness fail closed；回傳 `RefreshWarning` |
| state save 在 settlement 失敗 | 沒有 partial fan-out；anchor `CurrentRun` 仍可回收 |
| crash after begin | anchor-only atomic reclaim；optional recipients 沒有假 INTERRUPTED |
| cleanup 未確認 | Evidence stands；Runner 保存 recovery blocker 並停止 |
| concurrent verify on another anchor | repository-wide lock 在任何新 artifact/state 前拒絕第二個 execution；沒有新 `CurrentRun`、Evidence、checkout、log 或 process |
| 兩個 verifier 使用相同 clock／proposed run ID | exclusive log create 不截斷勝者；loser 沒有 state/run，且只清理自己的 unused empty log |
| anchor FAIL 後 Runner 產生 repair handoff | `VR-*` 以 run ID 唯一找到新 shared-run log；`LVR-*` 才走舊 lookup；failure excerpt 不消失或誤配 |
| `VR-100` 與 `VR-1000` logs 同時存在 | token-bounded lookup 各自只找到自己的檔案 |
| explicit same-Candidate rerun after completion | 合法，取得新 run ID；沒有永久 cache |
| Runner integration | 一個 step／pending execution，記錄全部 Evidence IDs，不保存 cohort lifecycle |
| Goal completion | 仍要求每張工作的 latest distinct PASS fresh 且無 OPEN Gate；通過後以 typed transaction 完成，不等待人工終審 |
| v8 migration | 每筆 legacy Verification Evidence／orphan Run 取得 distinct `LVR-*` ID；Review 無 ID；新 execution 只用 `VR-*`；兩處 newer-schema fixtures 同步升版 |
| rollback | first v9 write 前可還原 `state.json.v8.bak`；之後還原會遺失 v9 後 Evidence，沒有自動 downgrade |

## Compatibility and rollback

- CLI syntax 不變：`forgepilot verify <work-id> [--snapshot]`。
- `internal/app.Verify` signature 不變；result additive 擴充。
- `Evidence` 與 `HasEvidence` 維持 anchor meaning。
- final-review Evidence set、Story association、Candidate freshness 與 latest-result rules 不變。
- schema v9 使用既有 explicit migration／backup contract；舊 binary 拒讀新版 state。
- validation 要求 `next_verification_run_id` 為正整數且大於所有 CurrentRun／Verification Evidence 已配置的 `VR-*` number；Review Evidence 不得有 run ID；Verification Evidence 與 CurrentRun 的 ID 必須是 canonical `VR-%03d` 或 migration-only `LVR-%03d`，而產品 transition 不得新配發 LVR。
- 同一 run ID 不得同時出現在 active `CurrentRun` 與 settled Evidence；同一 run 的 Evidence 不得重複 Work Item。共享同一 ID 的所有 Verification Evidence 必須有完全相同的 Candidate、Resolved Runtime、command、result 與 `created_at`，否則 state 拒讀。repository 與 Story association 仍按各 Work Item 驗證。
- migration 為 legacy records 配發不重用的 ID，最後把 counter 設為大於最大值；malformed／reused／mixed-provenance fixtures 必須 fail closed。
- migration 必須同步提高 `internal/storage/storage_test.go` 與 `internal/work/work_test.go` 的 newer-schema fixtures，並檢查字串替換 fixture 仍命中正確版本。
- behavioral rollback 可把 cohort query 收窄成只回 anchor；既有 shared-run Evidence 仍 truthful、可讀。
- binary rollback 只能手動還原 v8 backup；first v9 write 後不是 lossless。

## Rejected shapes

- caller 傳 `[]WorkItemID`：把 eligibility、ordering 與 race handling 洩漏給 CLI／Runner，module 變 shallow。
- `Plan → Execute → Commit` public protocol：擴大 interface，讓每個 caller 都要正確處理 stale plan、cleanup 與 reclaim。
- 把所有 same-Goal RUNNING 都 fan-out：可能把尚未完成的工作標成 VERIFIED。
- FAIL／INTERRUPTED fan-out：會把整批 stale VERIFIED 退回 RUNNING，放大 repair sessions，且超出 handoff 的 PASS-only contract。
- persistent Candidate/runtime result cache：改變明確 rerun與最新 Evidence chronology，需另立 execution history 與 applied-at 語意。
- 複製 verification orchestration 到 `internal/runner`：違反 ADR-0019 的 single implementation seam。
- Evidence 保存 `LogPath`：仍不保存 path；run ID 同時表達 shared execution 並供 log lookup，ADR-0027 會明確修正 ADR-0012 的舊 lookup contract。

## Implementation checkpoint

實作順序：

1. 以 TDD 先建立 pure cohort／closure 與 batch settlement tests。
2. 實作 schema v9 migration、run identity、log naming 與 storage lock。
3. 深化 `internal/app.Verify`，保持 CLI thin shell 與 Runner single orchestration。
4. 跑 focused tests、`make verify`、`go test -race -count=1 ./...`。
5. 對固定 checkpoint 做獨立 Standards／Spec review，修正後只讓同一 reviewer review delta。
