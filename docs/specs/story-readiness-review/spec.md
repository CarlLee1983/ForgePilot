# Whole-DAG Story readiness review

## Decision status

Accepted design. The Human has accepted the source-of-truth decision: PraxisBound owns
a versioned, machine-readable readiness sidecar and ForgePilot consumes it read-only.
The independent architecture review, including the source-byte binding, is
incorporated below and accepted in ADR-0029. Product implementation requires separate
authorization.

This slice is limited to reviewing one Goal's whole Story/Work Item DAG before a
Runner execution. It does not change high-risk integration checkpoints, runtime
provenance, Agent Session Check Profiles, release/onboarding work, Verification,
Evidence, Gates, Work Item lifecycle, CLI syntax, state schema, or Run Record schema.

## Problem and authority

`work.Item.StoryRef` is deliberately opaque today, and `DependsOn` is the only
machine-readable dependency graph. Existing Story Markdown has useful prose about
inputs, outputs, authority, identities, and follow-up work, but it is not a stable
source from which ForgePilot can prove those facts. Parsing it would contradict
ADR-0013.

PraxisBound therefore owns the Story-readiness schema and validates the relationship
between a Story's `story.md`, `acceptance.md`, and readiness declaration. It generates
one raw-byte source digest for each named Markdown file. ForgePilot owns neither that
schema nor Story approval. It consumes a strict, versioned JSON sidecar at `<Story
directory>/readiness.json`; it never parses Markdown to fill gaps or infer an
obligation that was not declared.
The only initial supported schema is `schema_version: 1`. An absent, malformed,
unsupported, or internally inconsistent sidecar is a readiness defect, never an
implicit empty declaration.

## Domain terms

- **Story Readiness Contract** is the upstream-owned, versioned declaration of one
  PraxisBound Story's machine-checkable inputs, outputs, acceptance-criterion
  operations, future-identity dependencies, and decision follow-up references.
- **Whole-DAG Story Readiness Review** is ForgePilot's read-only comparison of every
  Story Readiness Contract in one Goal against that Goal's Work Item dependency DAG
  and repository artifact facts. Its result is a deterministic, ordered defect report.
- **Delivered Input** is an output declared by a prerequisite Story Readiness Contract
  and consumed by a downstream contract. It is planning information, not Evidence or
  a claim that the output's implementation has been verified.
- **Story Identity** is the stable identity in a Story Readiness Contract that matches
  its repository-relative Story reference. A Goal has exactly one Work Item for each
  v1 Story Identity.
- **Decision Follow-up Obligation** is a declared requirement that an exact RESOLVED
  Gate ID with an exact selected option be followed by a Work Item for an exact Story.
  It never matches a natural-language Gate question or note by similarity.

## Contract v1

The exact JSON grammar and its Markdown-criterion coverage check belong to PraxisBound.
ForgePilot relies on this normalized semantic shape after JSON decoding:

```text
StoryReadinessContract
  schema_version: 1
  story_ref: repository-relative identity matching its owning Story directory
  story_md_digest: SHA-256 digest of the exact raw bytes of story.md
  acceptance_md_digest: SHA-256 digest of the exact raw bytes of acceptance.md
  criteria[]:
    id: non-empty, unique criterion identity
    operations[]: closed set of execution operations
    owner: runner_worker | canonical_verification | integration_final | human | external
    future_identities[]:
      kind: commit | publication | release
      availability: preexisting | produced_by_current_work_item | prerequisite | external
      prerequisite_story_ref: required only when availability is prerequisite
  inputs[]:
    id: non-empty, unique within the Story
    source:
      preexisting_artifact { path: repository-relative path }
      prerequisite_output { output_id: Goal-unique output identity }
      external_preexisting { identity: stable opaque identifier }
  outputs[]:
    id: non-empty, Goal-unique output identity
  decision_follow_ups[]:
    gate_id: exact Gate ID
    choice: exact selected option
    follow_up_story_ref: repository-relative Story reference
```

The operation set is the Story authority vocabulary already used by PraxisBound:
`plan`, `modify`, `add_dependency`, `migration`, `commit`, `push`, `deploy`, and
`publish`. `runner_worker` is fixed to `plan` and `modify` in v1; its criterion must
not request another operation. The other owners describe work that is deliberately
outside the Agent Session's authority, not a permission grant. In particular,
`commit`, `push`, `deploy`, and `publish` never become a Runner worker operation.

A v1 contract must give every required input exactly one source.
`preexisting_artifact` is checked as a contained local file; `prerequisite_output` is
checked against the Goal DAG; `external_preexisting` is an explicit opaque declaration
but has no filesystem, credential, or network probe. An unbound source is invalid. A
later schema may add a source only with an explicit rule for how ForgePilot determines
its readiness; free-text is not an escape hatch.

`story_md_digest` and `acceptance_md_digest` are required `sha256:<64 lowercase hex>`
values. PraxisBound computes each over the exact raw bytes of its named file and must
reject a contract whose `criteria` do not exactly cover the accepted criterion
identities in `acceptance.md`. ForgePilot intentionally does not redo that Markdown
check. It reads the two contained regular files as raw bytes, recomputes SHA-256, and
rejects a mismatch without interpreting their contents. It validates the JSON grammar,
the closed values above, path safety, the source-byte binding, and the whole-Goal
relationships below.

## Interface and seam

`internal/app` owns the single orchestration entry point conceptually named
`ReviewGoalStoryReadiness(ctx, root, goalID)`. It loads one durable state snapshot,
selects the named Goal's Work Items, resolves each already-validated Story directory,
loads its sidecar and the two named source files through `internal/repository` without
following an escape symlink, and passes only pure values to one deep readiness-review
module.

`internal/repository` only locates and reads contained sidecar or source bytes; it
makes no semantic decision. The pure `internal/readiness` module decodes supported
versions and receives Work Item IDs, Story references, `DependsOn`, normalized
contracts, declared and recomputed source digests, resolved Gates, and artifact-
existence facts. It returns an ordered `ReadinessReport`. It does not read files, Git,
state, CLI text, Runner history or Evidence; it does not decide a lifecycle
transition. `internal/work` remains the sole owner of Work Item lifecycle and
dependency-progression rules. `internal/runner` asks the app for the typed review
result and never parses Story files or calculates defects itself.

The app loader must apply the same repository-root and symlink containment rules as
Story reference validation. A `preexisting_artifact` may name only a repository-
relative, contained regular file; existence is read at review time. The review makes
no writes and starts no subprocesses.

## Timing and failure semantics

`forgepilot run --dry-run` runs the review after its existing Goal eligibility check
and before runtime resolution. `forgepilot run` and `forgepilot run resume` first
acquire workspace ownership and finish their mandatory recovery checks, then run the
review before runtime resolution, new run-record creation, snapshot capture, Agent
Session launch, canonical Verification, reconciliation, or any lifecycle write.
Recovery stays first because accounting for a possibly surviving process is a safety
obligation, not a Goal action.

The Runner repeats the typed app review before each action, and the app includes each
sidecar's content digest plus the recomputed raw-byte digests of `story.md` and
`acceptance.md` in the existing persisted Goal scope fingerprint. Thus a changed valid
declaration or source contract is a scope change, a changed source without regenerated
sidecar is rejected as a mismatch, and a changed/removed required artifact is observed
by the repeated review. This changes the meaning of existing scope strings but adds no
state or Run Record field; a legacy run record without sidecar and source identities
fails closed on resume rather than claiming an unobserved contract was authorized. A
clean report does not itself permit an action: the existing typed next-action query,
Gate checks, Candidate freshness, budgets, and all transitions still decide that.

For a new run or dry run, any defect is a preflight refusal with a stable, complete
report and no run artifact. For an already-recorded run, a newly observed defect stops
the run with a distinct readiness-preflight stop reason and records only that
operational stop; it does not create Evidence, mutate readiness, open a Gate, or
reinterpret previous Agent output. Repository/I/O failure while loading a sidecar or
artifact fails closed as a defect/refusal, never as a missing declaration guessed from
Markdown.

## Required defect rules

| Code | Rule | Report location |
| --- | --- | --- |
| `MISSING_STORY_READINESS_CONTRACT` | A Work Item's Story has no sidecar. | Work Item and Story ref |
| `INVALID_STORY_READINESS_CONTRACT` | The sidecar is malformed, unsupported, unsafe, or violates v1 local invariants. | Work Item and Story ref |
| `STORY_READINESS_SOURCE_DIGEST_MISMATCH` | A required source file is absent, not a contained regular file, or its raw-byte SHA-256 does not equal the sidecar's declared digest. | Work Item, Story ref, source file, declared and observed digest when available |
| `MISSING_INPUT_SOURCE` | A declared required input has no exactly-one valid source, or its local preexisting artifact does not exist as a contained regular file at review time. | Work Item, Story ref, input ID and path |
| `OUT_OF_SCOPE_OPERATION` | A `runner_worker` criterion declares an operation outside the fixed Runner worker capability policy. | Work Item, Story ref, criterion ID and operation |
| `FUTURE_IDENTITY_DEPENDENCY` | A criterion requires a commit, publication, or release identity produced by its own current Work Item, or names a prerequisite identity producer outside its transitive dependency closure. | Work Item, Story ref, criterion ID and identity kind |
| `UNDELIVERED_PREREQUISITE_INPUT` | A declared prerequisite output is not uniquely delivered by any transitive `DependsOn` Work Item in the same Goal. | Work Item, Story ref and input/output ID |
| `UNPLANNED_DECISION_FOLLOWUP` | A declared exact RESOLVED Gate ID and selected choice requires a follow-up Story that has no Work Item in the same Goal. | Work Item, Story ref, Gate ID, choice and follow-up Story ref |

The `UNDELIVERED_PREREQUISITE_INPUT` rule deliberately uses the transitive Work Item
closure, not prose references or another Goal. Duplicate Goal output identities,
duplicate Story identities, or more than one Work Item for a v1 Story identity are an
invalid contract set because a consumer could not name one deterministic producer. The
follow-up rule evaluates only declared, exact resolved Gate ID/choice pairs. It neither
predicts a future Gate nor decides from a question/note that a follow-up is implied;
doing either requires a separate Gate schema and authority decision. Requiring a
follow-up dependency edge is also separately scoped.

Reports are sorted by Work Item creation order, then Story reference, defect code, and
local identity. Every defect is reported in one pass; the review does not stop after
the first one, so a human can repair the whole declared plan coherently.

## Acceptance matrix

| Scenario | Observable result |
| --- | --- |
| complete three-Work-Item Goal | valid v1 sidecars, existing artifacts, in-scope Runner criteria, unique prerequisite deliveries, and listed decision follow-up Work Item yield an empty report; existing Runner legality still determines the first action |
| legacy or absent sidecar | `run` and `run --dry-run` refuse before runtime or run-record creation with `MISSING_STORY_READINESS_CONTRACT` for every affected Work Item |
| malformed, unsupported, unsafe, duplicate Story/output ID, or duplicate Work Item for one Story | read-only review emits `INVALID_STORY_READINESS_CONTRACT`; no Story Markdown fallback is attempted |
| `story.md` or `acceptance.md` changes without a regenerated matching sidecar, or either named source is missing, non-regular, or escapes the repository | review emits `STORY_READINESS_SOURCE_DIGEST_MISMATCH`; ForgePilot only hashes raw bytes and does not parse Markdown |
| matching sidecar and source content changes during a run | the recomputed sidecar and source digests change scope and stop the existing run; no state, Evidence, Gate, or lifecycle result is written |
| declared repository artifact absent, directory, symlink escape, or unbound input | report has `MISSING_INPUT_SOURCE` or invalid-contract defect; no Agent Session starts |
| Runner-worker criterion requires `push` | report has `OUT_OF_SCOPE_OPERATION` naming the criterion and operation; a criterion owned by Human/external is not falsely treated as an Agent Session action |
| criterion requires its own future commit, publication, or release | report has `FUTURE_IDENTITY_DEPENDENCY`; an external or transitive-prerequisite identity is not rejected by this rule |
| downstream consumes an output no transitive prerequisite declares | report has `UNDELIVERED_PREREQUISITE_INPUT`; a same-named output in a sibling or another Goal does not satisfy it |
| downstream consumes an output delivered by one transitive prerequisite | no undelivered-input defect; this does not claim implementation, PASS, or Evidence |
| exact RESOLVED Gate ID/choice declares a follow-up Story absent from Goal Work Items | report has `UNPLANNED_DECISION_FOLLOWUP`; similar prose or an unresolved Gate is ignored |
| required artifact disappears during a run | the next pre-action review stops the existing run; it does not write state, Evidence, Gate, or a second lifecycle result |
| open Gate, stale Candidate, exhausted budget, or no actionable Work Item with a clean report | existing behavior is unchanged; readiness review never authorizes a transition |
| Agent Session report claims success | it has no effect on the review or PASS Evidence; ADR-0028 remains unchanged |
| offline execution | review uses only local filesystem and in-memory values; product code has no HTTP client and spawns neither `gh` nor other subprocesses |

## Compatibility and rollback

Existing state snapshots, Evidence, Candidate identities, CLI arguments, and run
record fields require no migration. Existing Goals remain readable and may use all
non-Runner commands, but a Runner invocation for a Goal whose Stories lack v1
contracts or matching source digests fails closed until those upstream artifacts are
added or regenerated. A legacy run record cannot resume across this feature because
its old scope lacks sidecar and source digests; it remains readable but fails closed as
an unrecognized scope. That deliberate Runner compatibility boundary is observable
and documented; treating missing contracts, source bindings, or legacy scope as empty
would silently disable the feature.

Rollback removes the app preflight and pure review module. It does not rewrite state
or historical run records. Story sidecars remain upstream artifacts and can be ignored
by the older ForgePilot binary.

## Rejected shapes

- **Parse `story.md` / `acceptance.md` in ForgePilot:** ForgePilot may hash their raw
  bytes to enforce the upstream sidecar's source binding, but it may not parse their
  prose; semantic parsing is not a deterministic contract and directly violates
  ADR-0013.
- **Put inputs, outputs, or authority on Work Items:** duplicates upstream Story
  requirements in ForgePilot state and turns a reference into a competing schema.
- **Let Runner parse contracts or cache a startup answer:** duplicates app/domain
  decision ownership and misses mutations during a long execution.
- **Store report/PASS/Gate/Evidence in state or Run Record:** a planning defect is not
  lifecycle truth, a Human Decision, or verification provenance. The existing scope
  fingerprint is the sole persisted binding to accepted sidecar bytes.
- **Have the Agent infer readiness from prose:** model output is untrusted and cannot
  be a deterministic preflight or PASS source.
