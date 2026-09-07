# 02: `review approve` / `reject` 的 `--pr`

**What to build:** 審查者在核准或打回時指明這次審查發生在哪個 pull request 上。`forgepilot review approve WI-001 --pr owner/name#123` 記下一筆同時帶著 PR Reference 與完整 commit SHA 的 APPROVED Evidence，`review reject` 同樣接受。日後回頭讀這筆紀錄的人，找得到當時的討論在哪裡。

`--pr` 是選填的——不帶就是既有那種純 commit review，本機先審、之後才開 PR 是正當流程。ForgePilot 不去 GitHub 查證那個 PR 存在、是否開著、HEAD 是不是它（ADR-0010）；它只驗格式，而格式非法就是輸入無效，整個指令被拒且不寫入任何東西，不是記一筆 REJECTED。

PR Reference 純粹是識別資料，沒有任何規則讀它（ADR-0011）。這張票要正面證明那句話：加上 PR 之後，完成判定與 stale 的結果與不帶 PR 時逐項相同。

**Blocked by:** 01

**Status:** done

- [x] `review approve` 與 `review reject` 都接受選填的 `--pr`，其值存入該筆 Human Review Evidence
- [x] 不帶 `--pr` 時兩個指令的行為與先前完全相同
- [x] PR Reference 只接受 `owner/name#number` 一種形式；完整 URL、缺 `#`、缺 `/`、number 為 0 或帶前導零、含多餘空白皆被拒絕
- [x] 格式非法時整個指令以非零 exit code 失敗，且重新讀回 state 時 Evidence 陣列未增長
- [x] 格式規則屬於 domain 層，載入時的 Evidence 驗證一併把關，手動改過的 `state.json` 夾帶非法值時被拒讀
- [x] verification Evidence 攜帶 PR Reference 時被驗證拒絕，與既有的「review 不得帶 command／exit_code」同屬一套分流規則
- [x] 完成的四項條件與 stale 判定在帶 PR 與不帶 PR 時結果逐項相同
- [x] 同一 revision 上標著不同 PR 的兩筆 review 仍然互相取代，取最新一筆的規則沒有例外
- [x] `--pr` 寫進 development-plan 的指令契約表，flag 名以該表為準
- [x] `make verify` 通過
