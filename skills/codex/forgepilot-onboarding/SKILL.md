---
name: forgepilot-onboarding
description: Locate the shared ForgePilot onboarding procedure. Use when a developer asks to introduce ForgePilot to a repository.
---

# ForgePilot onboarding adapter

Optional installation: copy this `forgepilot-onboarding` directory so its
entrypoint is `~/.agents/skills/forgepilot-onboarding/SKILL.md`. Installation
is a deliberate user action; ForgePilot installation does not install this adapter.

Invoke this adapter with `$forgepilot-onboarding`.

Shared temporary source contract: local #33 checkout at FORGEPILOT_ONBOARDING_SOURCE; common procedure docs/release/onboarding.md; temporary only, not a published immutable identity.

Read that common procedure from the stated checkout and follow it unchanged.
It owns source-contract failure, stop-on-failure, and human-review behavior;
do not reproduce the procedure here. A later separately authorized immutable
pin step atomically replaces this exact temporary string in both adapters.
