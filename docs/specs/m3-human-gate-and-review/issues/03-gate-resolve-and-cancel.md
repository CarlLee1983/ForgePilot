# 03: Gate 的 resolve 與 cancel

**What to build:** 人回答一個 Gate：從列出的選項中選定一個，可附自由文字說明判斷，決策連同自述的決策者身分與時間永久保存。若問題本身不成立——選項全都不對、或當初就問錯了——則附理由撤銷它，而不是勉強選一個再用備註解釋其實不是那樣。

撤銷同樣解除阻擋。這不是後門：同一個人本來就能選任何一個選項，撤銷沒有打開選擇沒打開的門，差別只在它記錄的是「這個問題問錯了」。真正的防護是撤銷無法悄悄發生——它留下不可變紀錄並在 `status` 看得見。

一件工作的全部 Gate 都關閉之後，阻擋解除，它重新可以推進、也重新會被 `next` 選中。

**Blocked by:** 02

**Status:** done

- [x] `gate resolve` 只接受該 Gate 列出的選項之一，其他輸入被拒絕
- [x] resolve 保存選定的選項、可選的自由文字說明、自述決策者與時間
- [x] 決策者身分預設取自 Git 設定，可由參數覆寫
- [x] `gate cancel` 要求理由，缺少時被拒絕
- [x] cancel 解除阻擋，並在 `status` 中可見
- [x] 全部 Gate 關閉後，該工作重新可 `start`／`verify` 並重新被 `next` 選中
- [x] 尚有其他未解除 Gate 時，關閉其中一個不解除阻擋
- [x] 已進入 RESOLVED 或 CANCELLED 的 Gate 不可再 resolve、cancel 或以任何方式變更
- [x] 關閉 Gate 不改變 Work Item 的狀態
- [x] `make verify` 通過
