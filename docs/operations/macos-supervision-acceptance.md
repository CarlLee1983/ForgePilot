# macOS supervision acceptance record

> 已由 ADR-0040 移除，以下為歷史紀錄。

This record separates automated evidence from observations that require a native macOS login, reboot, or sleep/wake session. A LaunchAgent is scoped to a logged-in user session. The design does not claim execution while the user is logged out, before login after reboot, or while the Mac is asleep.

## Automated evidence

Record the command and exit result from the integration run here. Package tests use a temporary home and a local launchctl test executable; they do not install a real user LaunchAgent. Runner and agent tests must separately establish authorization checks, workspace ownership, Pending execution handling, process-group cleanup, durable stop intent, and charged recovery.

| Command | Exit result | Evidence |
| --- | --- | --- |
| `go test -count=1 ./internal/supervision` | PASS (2026-09-24) | Versioned job record, plist arguments, binding mismatch, event persistence, pause, uninstall |
| `go test -race -count=1 ./internal/supervision` | PASS (2026-09-24) | Concurrent event recording in the package |
| `go test ./internal/runner ./internal/agent ./internal/storage` | PASS through `make verify` (2026-09-24) | Runner admission, process ownership, cleanup, and recovery ledger |
| `go test -race -count=1 ./internal/runner` | PASS (2026-09-24) | Stop/launch race and real subprocess cleanup at runner boundary |
| `make verify` | PASS (2026-09-24) | Repository format, vet, tests, CLI build, release, onboarding, and skill checks |

## Native Apple Silicon session

No native login, reboot, or sleep/wake acceptance observation has been recorded for this change. A person conducting the session should capture the host macOS version and architecture, job ID, authorization digest or safe redacted identity, timestamped command results, and relevant supervision events. Do not put credentials or worker environment values in this document.

1. Install a job bound to a managed, immutable ForgePilot executable. Confirm `launchctl print gui/<uid>/com.forgepilot.supervision.<job-id>` and the exact `ProgramArguments` in `~/Library/LaunchAgents/com.forgepilot.supervision.<job-id>.plist`.
2. Close the terminal or read-only observer while a controlled job is running. Record whether its run continues and whether an explicit stop writes durable pause intent before cancellation and confirmed cleanup.
3. Log out and back in. Record the persisted job state before login, the login-time LaunchAgent start, and the admission outcome after authorization, Worker Profile, engine, deadline, workspace ownership, and Pending execution checks.
4. Reboot and log in. Record the same facts. Do not infer execution during shutdown or before login from a later completed run.
5. Put the Mac to sleep past a short test deadline and wake it. Record the wake-time observation and verify that no new launch occurs after the deadline.
6. Exercise an uncertain worker identity or unresolved cleanup fixture and record the blocked result. Confirm that an explicit resume or repair, rather than a LaunchAgent timer, is required before a new writer.
7. Uninstall the LaunchAgent. Confirm `launchctl print` no longer finds the service, the plist is gone, and the durable job record remains for audit.

| Event | Observation time | Result | Evidence path or command output | Limitations |
| --- | --- | --- | --- | --- |
| Login | not observed | pending | — | Requires native user session |
| Reboot and later login | not observed | pending | — | No execution promised before login |
| Sleep and wake | not observed | pending | — | No execution promised during sleep |

The native observations are required for FP-59 AC-004 and AC-008. Automated tests alone cannot mark those criteria complete.
