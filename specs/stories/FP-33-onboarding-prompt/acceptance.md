# Acceptance Criteria

* [ ] The common procedure starts inspection-only and fails closed for a
  non-repository target, uncertain Candidate, absent `make verify`, or ignored
  untracked SNAPSHOT Makefile.
* [ ] The action-plan tool accepts only a full 40-hex SHA and prints source
  repository, SHA, paths, fetch, detached checkout, build, `make verify`, and
  atomic entrypoint effects without performing them.
* [ ] The first explicit approval is required before every source-side write;
  missing Go stops with options and does not install software or change a
  profile.
* [ ] Existing ForgePilot state is reused. A Story is reused or drafted and
  human-reviewed before the second explicit approval permits `init`, `goal
  create`, `work add`, and `status`.
* [ ] Uncommitted work is guided to `verify --snapshot`; the procedure never
  commits, migrates, approves review, resolves Gates, or publishes.
* [ ] The procedure and prompt do not describe unsigned prebuilt binaries,
  Developer ID, notarization, or Gatekeeper/quarantine bypasses as onboarding.
* [ ] `scripts/onboarding/onboarding_test.sh`, `make verify`, and `go test
  -race -count=1 ./...` pass.
