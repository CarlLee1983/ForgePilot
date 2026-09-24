---
name: forgepilot-onboarding
description: Introduce an installed ForgePilot generation to an explicitly named repository after a separate repository approval.
---

# ForgePilot managed onboarding

Invoke with `$forgepilot-onboarding`, an absolute target repository path, and a
COMMIT or SNAPSHOT Candidate choice. Ask for missing inputs before proceeding.

Resolve this installed skill's physical directory with
`cd -P "$HOME/.agents/skills/forgepilot-onboarding" && pwd -P`. It must be
`$HOME/.local/share/forgepilot/versions/<full lowercase commit>/skills/codex/forgepilot-onboarding`.
From that directory, use the helper at the same generation's
`libexec/forgepilot-bootstrap` to run `generation-v1 current`. Require its
`generation_id`, `forgepilot_path`, and `helper_path` to match that physical
generation and require successful managed-state validation. If they differ,
stop and ask the developer to retry after the Bootstrap transaction settles.
Keep the returned absolute CLI and helper paths for this invocation; do not
resolve `current`, `$PATH`, or the skill link again mid-flow.

Read `docs/release/onboarding.md` from that exact generation's root and follow
its **Managed Repository Onboarding** section. Bootstrap approval grants no
target-repository writes. Show the target preflight, Candidate, Story, and
proposed commands, then obtain a distinct Repository Onboarding Approval before
running any command that writes the target. If the Story needs drafting, show
the draft and obtain approval before writing it.
