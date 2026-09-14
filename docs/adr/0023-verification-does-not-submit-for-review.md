# Verification records evidence; explicit submission opens Human Review

`forgepilot verify` records the result of the canonical automated check. For a
`WORK_ITEM` Goal, PASS returns `VERIFYING` work to `RUNNING`; it does not place
the item in `REVIEW`. `forgepilot review request <work-id>` is the separate,
explicit `RUNNING → REVIEW` transition.

The old combined transition made routine red-green-refactor verification look
like an implementation stop: every passing check turned `next` into a
human-only wait even when the agent still had ACs, review findings, or other
implementation work to finish. Machine evidence and a request for human time
are different intentions, so the lifecycle records them separately.

Submission is fail-closed. It requires a `WORK_ITEM` policy, ACTIVE Goal, no
OPEN Gate, a latest PASS that is newer than any Human Review, and a current
Candidate. COMMIT submission requires a clean workspace at that same HEAD;
SNAPSHOT submission recomputes and matches the verified digest. The CLI reads
the Candidate facts first, then the locked state transaction rechecks the
expected Verification Evidence ID and all domain conditions. No Evidence is
added by submission, and no dependency is unlocked.

`review approve` remains the only route to `DONE` when its existing matching
PASS, Gate, and Goal conditions hold (ADR-0008). `review reject` still returns
to `RUNNING`, and a later verification must precede another submission. There
is no schema change: RUNNING with immutable PASS Evidence was already valid.

**Falsified if:** `internal/work/evidence.go` moves a WORK_ITEM PASS directly
to REVIEW, `internal/cli/review.go` submits without rechecking the exact,
current Candidate, or any command other than `review approve` can set DONE.
