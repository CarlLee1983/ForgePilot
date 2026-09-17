---
status: accepted
---

# Story readiness contract is upstream-owned and ForgePilot consumes it read-only

Whole-DAG readiness needs facts about Story inputs, delivered outputs, criterion
operations, execution owners, future identities, and decision follow-up work. The current Work Item DAG
does not contain them, and extracting them from Story Markdown would violate
ADR-0013's deliberate separation of Story ownership. We therefore propose a
PraxisBound-owned, versioned `readiness.json` sidecar in each Story directory.
PraxisBound validates it against the Story and acceptance contract, and generates
SHA-256 digests for the exact raw bytes of `story.md` and `acceptance.md`; ForgePilot
reads only the strict normalized values and raw bytes needed to compare those digests
while reviewing a Goal before and during `forgepilot run`.

ForgePilot's review is a read-only app orchestration over the whole Goal: it combines
the upstream contract with the existing Work Item dependency DAG, exact resolved Gate
choices, fixed Runner-worker capability policy, and local artifact existence facts,
returning every stable defect at once. It does not parse Markdown, change Work Item
lifecycle/readiness, create Gate or Evidence, add a CLI command, persist a report,
spawn a subprocess, make a network request, or let Runner calculate semantic rules.
The existing Goal scope fingerprint includes sidecar and recomputed source digests so a
valid sidecar or source change stops the run; a source change without a regenerated
matching sidecar is a readiness defect. The Runner asks the app before start and before
each action, and a defect refuses a new run or operationally stops an active run.

This narrowly amends ADR-0013: ForgePilot still neither owns Story schema nor approval,
but it may read the one upstream-owned readiness sidecar. Any new sidecar version,
input source, operation, identity kind, lifecycle effect, or persisted result requires
a new decision.

**Falsified if:** ForgePilot parses Story Markdown beyond hashing the raw bytes named
by the upstream digest binding, `internal/runner` calculates readiness defects or
stores a readiness verdict, Story facts are duplicated into Work Item state, a report
produces PASS Evidence/Gate/lifecycle truth, missing contracts or source bindings are
treated as empty, arbitrary Gate prose is treated as a follow-up obligation, or product
code uses a network/subprocess to obtain a contract. Any of these moves the ownership
or trust boundary and must be decided again.
