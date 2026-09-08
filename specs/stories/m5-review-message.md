# M5-A：review 的乾淨檢查訊息說出當下動作

來源：GitHub issue #1 / #4。

`review approve` 在工作樹不乾淨時的拒絕訊息，要說出當下在做的是 review 而不是 verify。
`verify` 路徑的訊息維持不變，判準一字不放寬——未追蹤檔案仍然算髒。

驗收：髒工作樹時 review 被拒訊息含 reviewing、不含 verifying；verify 仍說 verifying；
兩條路徑都列出髒檔案且格式一致；乾淨判準只有一份實作；被拒時不寫 Evidence。
