# Evidence 保存在單一 state snapshot 內

Evidence 在 domain 上是 append-only 的不可變事實，直覺做法是獨立的 append-only log（例如 `evidence.jsonl`）。我們仍選擇把 Evidence 放進既有的 `state.json`，沿用 M1 已建立的「單一受鎖 read-modify-write 加原子替換」一致性邊界。

理由是這樣一來「Evidence 已 append 但 state 未更新」或反之的 crash 中間態根本不可能存在，開發計畫中列為 M2 前置的 crash-consistency 問題直接消滅，而不是被解決。代價是每次寫入都要重寫整份歷史；以本工具的規模（單人、單 repo、數十個 Work Item、每件累積數次驗證）這個成本在可見的未來都不構成問題，而獨立 log 買到的固定寫入成本目前沒有人需要。

**Falsified if:** 單次 `state.json` 寫入的體積或延遲成為實際問題——亦即 `internal/storage/storage.go` 的原子替換不再是廉價操作。屆時應改為獨立 append-only log，並補上 evidence-first 的寫入順序，使中途崩潰只留下無人引用的孤兒 Evidence，而非引用不存在 Evidence 的 state。
