# 第一次拿 ForgePilot 管自己，撞到兩個設計摩擦

實跑 `init → goal create → work add → next → start` 時發現兩件文件沒提、但每個新使用者都會撞到的事：

1. **Story reference 必須在 `specs/stories/` 底下**，而 ForgePilot 自己的 repo 沒有這個目錄（它的規格在 `docs/specs/`）。拿它管自己，第一個動作是建一個空目錄。
2. **`init` 會改動 `.gitignore`，使工作樹變髒**。這份紀錄當時的 commit-mode `verify` 與 review 都要求嚴格乾淨（含 untracked）；不先 commit，該路徑之後每一步都被拒絕，錯誤訊息還指著 `init` 自己造成的檔案。

**Evidence**：在 scratchpad 的 temporary git repository 上以實際 binary 跑過，兩者都真的擋住了流程。

**Implications**：這兩點是 MISSION 要蒐集的「設計在真實使用中哪裡不順手」清單的第一批項目，也是未來 M5 的候選輸入——例如 `init` 是否該提示使用者 commit，或 `work add` 的錯誤訊息是否該說明 `specs/stories/` 可以是空的。手冊刻意把它們寫在最前面而不是讓使用者撞。

**後續狀態**：P0-001 已加入 `verify <work-id> --snapshot`，可直接驗證 working tree，不需要 WIP commit；COMMIT review 仍要求乾淨 workspace，SNAPSHOT review 則以 workspace digest 是否未變判定。
