# FP-57 Verification

## Result

**PASS** — 2026-09-24. Bootstrap has executable install, upgrade, prune,
uninstall, generation, and retention paths. Disposable-home tests cover exact
source installation, one-pointer upgrade, isolation, retention, and removal.
The approved NUL action stream now drives these lifecycle mutators. All 35
transaction-boundary crash cases and both fail-closed staged-candidate cases
pass after the action executor change.
An authenticated native Codex model session in a clean Apple-Silicon disposable
home discovered the managed skill, validated its generation, read that
generation's procedure, and stopped before target writes pending distinct
Repository Onboarding Approval. This supports the scoped source-built Bootstrap
path; it does not claim signing, notarization, or a no-Go prebuilt release.

On 2026-09-24 the user approved the V1 removal boundary: completed-command
recovery with fail-closed manual inspection of an unattributed pre-transaction
stage or a candidate partially deleted inside `rm -rf`. AC-005 now uses that
scope. Native Codex procedure acceptance was completed later the same day.

## Checks

* Before edits, `sh -n scripts/forgepilot-bootstrap scripts/forgepilot-bootstrap_test.sh scripts/forgepilot-bootstrap_install_test.sh` — PASS, exit 0.
* Before edits, `sh scripts/forgepilot-bootstrap_test.sh` and `sh scripts/forgepilot-bootstrap_install_test.sh` — PASS, exit 0.
* `sh -n scripts/forgepilot-bootstrap scripts/forgepilot-bootstrap_test.sh scripts/forgepilot-bootstrap_install_test.sh scripts/forgepilot-bootstrap_isolation_test.sh scripts/forgepilot-bootstrap_crash_test.sh` — PASS, exit 0 after the removal fix.
* `sh scripts/forgepilot-bootstrap_test.sh` — PASS, exit 0 after the removal fix.
* `sh scripts/forgepilot-bootstrap_isolation_test.sh` — PASS, exit 0.
* `sh scripts/onboarding/onboarding_test.sh`, `sh scripts/skills/check_adapters_test.sh`, and `sh scripts/skills/short_prompt_regression_test.sh` — PASS, exit 0 after managed-skill and prompt changes.
* `go test ./... -run 'TestOnboarding' -count=1` — PASS, exit 0 after adapter changes.
* `codex debug prompt-input 'List available skills.'` with disposable HOME/CODEX_HOME and a symlinked skill — PASS, exit 0; `forgepilot-onboarding` appeared in the native skill catalog. This checks discovery only.
* `sh scripts/forgepilot-bootstrap_install_test.sh` — PASS, exit 0 after the removal fix.
* `make verify` — PASS, exit 0, including Go, release, onboarding, and skill gates.
* `go test -race -count=1 ./...` — PASS, exit 0.
* `sh scripts/forgepilot-bootstrap_crash_test.sh` — PASS, exit 0 for all 35 transaction boundaries before the tamper case was added; those cases were unchanged afterward.
* `FORGEPILOT_CRASH_ONLY=uninstall/tampered-candidate sh scripts/forgepilot-bootstrap_crash_test.sh` — PASS, exit 0 for the post-manifest candidate-tamper refusal added after the full run.
* `git diff --check` — PASS, exit 0 before final review.
* On 2026-09-24, `sh -n scripts/forgepilot-bootstrap scripts/forgepilot-bootstrap_test.sh scripts/forgepilot-bootstrap_install_test.sh scripts/forgepilot-bootstrap_isolation_test.sh scripts/forgepilot-bootstrap_crash_test.sh` — PASS, exit 0 after the V1 contract test change.
* On 2026-09-24, `sh scripts/forgepilot-bootstrap_test.sh`, `sh scripts/forgepilot-bootstrap_install_test.sh`, and `sh scripts/forgepilot-bootstrap_isolation_test.sh` — PASS, exit 0.
* On 2026-09-24, `FORGEPILOT_CRASH_ONLY=uninstall/partial-candidate sh scripts/forgepilot-bootstrap_crash_test.sh` — PASS, exit 0. A removed staged CLI makes recovery planning and the formerly fresh approval fail; the transaction, residual candidate, and manifest remain, and status is nonzero. The fixture models a partial tree; it does not interrupt `rm -rf` mid-system-call.
* On 2026-09-24, `sh scripts/forgepilot-bootstrap_test.sh` — PASS, exit 0 after adding the pre-publication `.transaction.ABCDEF` residual fixture. Planning, status, and formerly approved uninstall all refuse; the file and candidate remain.
* On 2026-09-24, `sh scripts/forgepilot-bootstrap_crash_test.sh` — PASS, exit 0 for all 35 completed-statement boundaries and both fail-closed staged-candidate cases, including the partial tree.
* On 2026-09-24, `make verify` — PASS, exit 0 after the contract and regression updates (format, `go vet`, `go test ./...`, release/onboarding/skill regressions, and CLI build).
* On 2026-09-24, `go test -race -count=1 ./...` — PASS, exit 0 for all Go packages.
* On 2026-09-24, PraxisBound `review index` accepted a temporary FP-57/58 manifest without diagnostics. Its `review readiness-digests` command refreshed both Sidecars; after the final acceptance-note edit it refreshed only FP-57. A final rerun returned `updated: []`, and direct digest comparison matched both Stories and acceptance files.
* On 2026-09-24, `codex login status` with the disposable native-discovery `HOME` and `CODEX_HOME` exited 1; no authenticated disposable model session was available for AC-13. Independent read-only review found the refreshed Sidecars and remaining Story status consistent.
* After the NUL action executor change on 2026-09-24, `sh -n scripts/forgepilot-bootstrap scripts/forgepilot-bootstrap_test.sh scripts/forgepilot-bootstrap_install_test.sh scripts/forgepilot-bootstrap_isolation_test.sh scripts/forgepilot-bootstrap_crash_test.sh` and `git diff --check` — PASS, exit 0.
* After that change, `sh scripts/forgepilot-bootstrap_test.sh`, `sh scripts/forgepilot-bootstrap_install_test.sh`, and `sh scripts/forgepilot-bootstrap_isolation_test.sh` — PASS, exit 0. The first includes byte-preserving install/removal action dispatch and callback-failure propagation.
* After that change, `make verify` — PASS, exit 0, including format, `go vet`, Go packages, release, onboarding, skill regressions, and CLI build.
* After that change, `go test -race -count=1 ./...` — PASS, exit 0 for all Go packages.
* After that change, `sh scripts/forgepilot-bootstrap_crash_test.sh` — PASS, exit 0 for all 35 completed-statement boundaries and the two fail-closed staged-candidate cases.
* Independent read-only review of the install/upgrade executor and final integrated delta found no material issue; a separate requirement audit found no material static gap beyond native Codex AC-13.
* AC-13 native run on `Darwin arm64`, Codex CLI `0.155.1`: a private `/tmp/forgepilot-ac13.J4fw27` home and source checkout were populated from the current worktree, then committed only inside that disposable source (`70f44b6814876527994db325545aee522fc9efe7`). `forgepilot-bootstrap plan --agent codex --source /tmp/forgepilot-ac13.J4fw27/source --commit 70f44b6814876527994db325545aee522fc9efe7` returned plan ID `sha256:f02cf88e7a25f040bd261b81ce3aea1d7bb20bf3a478d5acf070401c2907f119`, exit 0. The matching `install --agent codex --source ... --commit ... --approve ...` ran source `make verify`, exited 0, and selected that generation. No ForgePilot repository commit was made.
* In that home, `forgepilot-bootstrap status` exited 0 (`idle`, one generation); `generation-v1 current` exited 0 with generation `70f44b6814876527994db325545aee522fc9efe7`, payload digest `sha256:a05da12a41aa2decbf49c13887007ef5d69ed0cfb6533f03ec1e81af02dbdc71`, and CLI/helper paths in that physical generation. `codex debug prompt-input 'List available skills.'` exited 0 and listed `forgepilot-onboarding` from the managed `.agents/skills` symlink. `codex login status` in the isolated `CODEX_HOME` exited 0 (`Logged in using ChatGPT`) after an authorized browser login; no ambient auth cache or API key was copied.
* First `codex exec --ephemeral --json --sandbox read-only -C /tmp/forgepilot-ac13.J4fw27/target -` model run exited 0 and read the managed skill and matching procedure, but did not invoke `generation-v1 current`; it stopped after assuming the read-only sandbox would prevent the helper's private temporary-directory creation. This run alone did not satisfy AC-13.
* Second `codex exec --ephemeral --json --sandbox workspace-write --skip-git-repo-check -C /tmp/forgepilot-ac13.J4fw27/session -` model run exited 0 with `TMPDIR` inside that separate session workspace. The prompt supplied the absolute target and COMMIT Candidate and explicitly withheld Repository Onboarding Approval. The model read the managed skill, invoked the physical-generation helper's `generation-v1 current` (exit 0), read that same generation's `docs/release/onboarding.md`, inspected target `HEAD` and its committed `Makefile`, showed Story drafts and exact proposed CLI commands, then explicitly stopped for Story approval and distinct Repository Onboarding Approval. The target was outside the writable session workspace; `git status --porcelain` remained empty, its committed `Makefile` remained unchanged, and no `.forgepilot/` or `specs/` appeared. Raw model output and authentication material are omitted from this record.
* The exact second model invocation was `env -i HOME=/tmp/forgepilot-ac13.J4fw27 CODEX_HOME=/tmp/forgepilot-ac13.J4fw27/.codex TMPDIR=/tmp/forgepilot-ac13.J4fw27/session/tmp PATH=/tmp/forgepilot-ac13.J4fw27/.local/bin:/Users/carl/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin /Users/carl/.local/bin/codex exec --ephemeral --json --sandbox workspace-write --skip-git-repo-check -C /tmp/forgepilot-ac13.J4fw27/session - < /tmp/forgepilot-ac13.J4fw27/prompt.txt > /tmp/forgepilot-ac13.J4fw27/model2.jsonl 2> /tmp/forgepilot-ac13.J4fw27/model2.stderr`; the process exited 0 and stderr was empty. The prompt contained only the skill name, target path, COMMIT choice, prior Bootstrap approval, and explicit absence of Repository Onboarding Approval.
* The isolated source `make verify` printed `/bin/sh: gofmt: command not found` from the repository Makefile's format recipe but still exited 0; the recipe masks a missing `gofmt`. Its Go vet, Go tests, release, onboarding, and skill checks ran. A separate host `gofmt -l` over all Go files returned no files. The earlier normal-environment `make verify` and race gate passed on unchanged Go source; this isolated format-subcheck limitation does not establish format validation inside the Bootstrap build.
* After the AC-13 evidence edit, PraxisBound's `review index` accepted a temporary one-Story manifest without issues. `review readiness-digests` refreshed only FP-57 `readiness.json`; a second invocation returned `updated: []`. Direct SHA-256 comparison matched its Story and acceptance digests. The temporary manifest was removed. `git diff --check` and target `git diff --exit-code` passed; the target had no `.forgepilot/` or `specs/` directory.
* `codex logout` under the isolated `HOME`/`CODEX_HOME` exited 0; the disposable home, source, target, session workspace, and raw model logs were then removed with `rm -r`. A final `test ! -e` confirmed removal. No login artifact was placed in the developer's normal Codex home.
* An independent read-only AC-13 review found that the second native run supports the literal discovery, same-generation procedure, and distinct approval-stop criteria without a material spec mismatch. Its limits are one successful model observation, a target outside the writable session workspace, and no retained raw transcript after disposal; the sanitized command/result account above is the durable evidence.

## Observed behavior

* Planning leaves an absent managed root absent even with `TMPDIR=$HOME`,
  rejects a source-local `go` before running it, and binds the approved Go
  executable path and hash without executing it. After approval, compatibility
  is checked before staging; build and source `make verify` use a private copy
  of those exact Go bytes. A stale approval or changed Go executable is
  refused before the managed-root scaffold. Unrecorded root staging blocks
  planning.
* Initial install and upgrade build a detached exact commit, run source
  `make verify`, compare the complete staged skill tree with the commit, and
  switch the current generation. A source `make verify` that changes an
  auxiliary skill file cannot publish the generation.
* Healthy interrupted initial and upgrade transactions finish with fresh
  matching approvals. A missing initial payload causes exact entrypoint
  containment; a subsequent approval rebuilds and publishes only if its
  payload digest matches the recorded tuple. A missing upgrade payload
  restores the old generation. Drifted published upgrade payload restores
  old selection while retaining the non-idle transaction and suspect path.

* A mutator recomputes the plan while holding the existing Bootstrap lock and
  rejects a stale approval after a retention change.
* Exact-commit uninstall removes one eligible generation. A two-candidate
  prune removes both eligible generations while current, previous, and an
  actively retained generation survive.
* Missing or malformed retention state blocks already approved prune and
  uninstall before transaction publication or deletion. Unrecorded removal
  staging also blocks planning and status. A recorded removal manifest stage
  uses `.manifest.new`; recovery accepts only an empty or exact final staged
  manifest, and binds its bytes and path facts to the approval. Every staged
  candidate still present must match the recorded payload digest before
  deletion.
* An interrupted first candidate move leaves status non-idle. An idle approval
  and another operation are refused; a fresh matching recovery approval
  finishes only the recorded candidates.
* After manifest publication, adding an unrecorded child to a staged candidate
  makes both re-planning and execution with the formerly fresh approval fail;
  the child, candidate, manifest, and transaction remain intact.
* A disposable-home target fixture with `.git/` and `.forgepilot/` retains its
  bytes, modes, and mtimes after Bootstrap-controlled status, retention, prune,
  and uninstall. PATH and Git credential tripwires record no invocation;
  static command inspection found no target traversal in Bootstrap. Approved
  source `Makefile` effects remain outside this controlled-path guarantee.
* The managed Codex skill now resolves its physical generation, checks
  `generation-v1 current`, and selects that generation's procedure and CLI.
  The zero-install short prompt and Claude adapter retain their source-built
  walkthrough. Independent review found and prompted fixes for SNAPSHOT
  Makefile inclusion and Story symlink containment.

## Scope and limits

The 35 completed-statement crash boundaries pass under the approved V1 scope.
A drifted published generation remains blocked for human inspection because
Bootstrap cannot safely replace or delete that occupied path. A crash before
removal `transaction.json` publication can leave an unauthoritative temporary
file; a crash inside `rm -rf` can leave a partial payload. Both fail closed and
need manual inspection rather than automatic deletion. The boundary suite
injects after completed shell statements and does not prove mid-command
recovery.
The native observation is one real model session on one disposable Apple-Silicon
home. The first read-only attempt stopped conservatively before generation
validation; the second run completed preapproval inspection with private
temporary writes confined to a separate workspace. Neither run granted
Repository Onboarding Approval or performed a target write.

The NUL-delimited action stream is now consumed by the install, upgrade,
prune, and uninstall mutators. Each validates the complete versioned stream
and exact ordered records before executing decoded argv under the Bootstrap
lock; initial install validates it once more before creating its scaffold.
Install and upgrade dispatch checkout, build, source verification, payload,
transaction, publication, links, pointer, manifest, and finalization actions;
recorded recovery dispatches those publication steps or the exact containment
or restoration branch. Removal dispatches staging, manifest publication,
candidate deletion, and finalization per recorded candidate. The parser tests
exercise embedded and trailing newline bytes, wrong or truncated fields,
duplicate and reordered IDs, command and path substitution, parent-shell
state, and immediate failure propagation. Existing approvals must be replanned
because the action list and therefore plan IDs changed.

PraxisBound owns `readiness.json`. Its `review readiness-digests` command is now
implemented and refreshed both Sidecars. JSON comparison against HEAD found
only `acceptance_md_digest` changed for FP-57 and only the Story and acceptance
digest fields changed for FP-58. The temporary manifest was removed after the
run. This record does not claim Story completion.
