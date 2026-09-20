# Acceptance Criteria

## Happy Path

* [ ] AC-001: Bootstrap stages and verifies a complete CLI, helper, and Codex skill/procedure payload;
  its generation tuple is the full source commit plus canonical payload digest, and one atomic pointer
  makes the tuple current.
* [ ] AC-002: A caller using `retention-v1` can acquire and release an opaque reference to an exact
  generation tuple without recording or reading a target-repository or holder identity.

## Business Rules

* [ ] AC-003: Prune removes only conclusively unretained non-current generations; uninstall preserves
  retained or uncertain generations and reports why removal is blocked.
* [ ] AC-004: Bootstrap-controlled status, prune, uninstall, and retention-v1 operations make no network
  call and consume no credentials. Approved source build/verification may have only its declared
  repository-defined effects and is not represented as sandboxed.

## Failure Cases

* [ ] AC-005: A pre-commit stage/verification/pointer failure leaves the prior generation selected. A
  post-commit failure leaves an authoritative transaction, both identities retained, and status
  nonzero until a freshly approved matching recovery completes or restores it.
* [ ] AC-006: Malformed, missing, or uncertain retention state fails closed and cannot authorize
  deletion.

## Regression Requirements

* [ ] AC-007: Source-built Bootstrap remains separate from Repository Onboarding and does not scan or
  register target repositories.

## Acceptance Evidence

| AC | Method | Evidence | Fixture / precondition | Expected observation |
| --- | --- | --- | --- | --- |
| `AC-001` | command | `./scripts/forgepilot-bootstrap plan` | `repository checkout` | `reports lifecycle unavailable; AC remains pending` |
| `AC-002` | test | `scripts/forgepilot-bootstrap_test.sh` | `prepublished local generations and opaque references` | `exact-tuple acquire/release, idempotence, hashed-only persistence, and shared-lock serialization pass; target/holder identity is absent` |
| `AC-003` | command | `./scripts/forgepilot-bootstrap prune --approve placeholder` | `repository checkout` | `reports lifecycle unavailable; no generation-removal claim` |
| `AC-004` | human | `implementation inspection` | `current development slice` | `network/credential tripwire coverage is absent; AC remains pending` |
| `AC-005` | command | `./scripts/forgepilot-bootstrap upgrade` | `repository checkout` | `reports lifecycle unavailable; no transaction/recovery claim` |
| `AC-006` | test | `scripts/forgepilot-bootstrap_test.sh` | `malformed marker, missing store, and non-idle transaction fixtures` | `acquire/status fail closed; destructive-removal behavior remains unimplemented and unproven` |
| `AC-007` | test | `scripts/forgepilot-bootstrap_test.sh` | `retention invocation with an extra --target argument` | `protocol rejects a target path; target sentinel/no-scan behavior still needs end-to-end coverage` |

## Security Fixture Matrix

| Source field | Payload | Expected result | Persisted locations | Verification |
| --- | --- | --- | --- | --- |
| `bootstrap.generation_id` | `0123456789abcdef0123456789abcdef01234567` | preserve | `manifest and generation path` | `scripts/forgepilot-bootstrap_test.sh` |
| `bootstrap.payload_digest` | `sha256:<64 lowercase hex>` | preserve | `manifest and retention tuple` | `scripts/forgepilot-bootstrap_test.sh` |
| `retention.reference_id` | `64 lowercase hex characters` | redact | `SHA-256-only retention marker filename` | `scripts/forgepilot-bootstrap_test.sh` |

## Verification Notes

The current executable slice covers `status` validation and the installed `retention-v1` protocol only;
the human lifecycle operations are still pending. Run the focused Bootstrap test during implementation;
full and race gates belong to the integrated closure in FP-61.
