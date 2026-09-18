# ForgePilot short source-built onboarding prompt

Copy this prompt after replacing the source repository and full 40-character
commit SHA with values you have independently reviewed:

```text
Introduce ForgePilot to this repository from source repository <repository> at
full 40-character commit SHA <sha> on an Apple Silicon Mac. First follow the inspection-only section of
the source-built onboarding procedure. Print the exact source fetch, build,
make verify, entrypoint, and repository-write plan; wait for my first explicit approval before source work and my second explicit approval before repository
writes. If Go is missing, stop and explain options. Do not install Go, commit,
migrate, approve a review, resolve a Gate, or publish for me.
```

Do not replace the SHA with a branch, tag alias, `main`, or `latest`. If the
procedure cannot be read from the reviewed source commit, stop before any
action and ask the developer for a readable reviewed copy. Stop during
inspection on Intel Macs or any non-`Darwin arm64` host; those platforms are
not part of the supported onboarding contract.
