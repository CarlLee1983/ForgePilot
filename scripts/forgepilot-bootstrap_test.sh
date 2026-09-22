#!/bin/sh
set -eu
umask 077

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
bootstrap=$script_dir/forgepilot-bootstrap
fixture=$(mktemp -d "${TMPDIR:-/tmp}/forgepilot-bootstrap-test.XXXXXX")
cleanup() { rm -rf "$fixture"; }
trap cleanup EXIT HUP INT TERM

fail() { printf 'forgepilot-bootstrap_test: %s\n' "$*" >&2; exit 1; }
assert_file() { [ -f "$1" ] || fail "expected file: $1"; }
assert_not_file() { [ ! -e "$1" ] && [ ! -L "$1" ] || fail "unexpected path: $1"; }
assert_output() { printf '%s\n' "$1" | grep -F -- "$2" >/dev/null || fail "expected output to contain: $2"; }
fingerprint_home() {
	tree=$1
	{
		/usr/bin/find -s "$tree" -exec /usr/bin/stat -f '%N|%HT|%u|%Lp|%d|%i|%z|%m' '{}' \;
		/usr/bin/find -s "$tree" -type f -exec /usr/bin/shasum -a 256 '{}' \;
		/usr/bin/find -s "$tree" -type l -exec /bin/sh -c 'for entry do printf "%s\0" "$entry"; /usr/bin/readlink "$entry"; done' sh '{}' +
	} | /usr/bin/shasum -a 256 | /usr/bin/awk '{ print $1 }'
}

home=$fixture/home
root=$home/.local/share/forgepilot
generation_a=0123456789abcdef0123456789abcdef01234567
generation_b=1123456789abcdef0123456789abcdef01234567
generation_c=2123456789abcdef0123456789abcdef01234567
generation_d=3123456789abcdef0123456789abcdef01234567
generation_e=4123456789abcdef0123456789abcdef01234567
digest_a=sha256:89b7b72858f35c9e13407e80ac040bde1113c634296a55cfa9b0a2e10585ecc6
digest_b=sha256:72b0e3a23acf97c2a0b58ecad206a851d3ca932fa475f21d884bd69a0eac6617
digest_c=$digest_b
digest_d=$digest_b
digest_e=$digest_b
reference_a=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
reference_b=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
reference_c=cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
reference_d=dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
reference_e=eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee
reference_f=ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
mkdir -p "$root/versions/$generation_a/bin" \
	"$root/versions/$generation_a/libexec" \
	"$root/versions/$generation_a/skills/codex/forgepilot-onboarding" \
	"$root/versions/$generation_a/docs/release" \
	"$root/versions/$generation_b/bin" \
	"$root/versions/$generation_b/libexec" \
	"$root/versions/$generation_b/skills/codex/forgepilot-onboarding" \
	"$root/versions/$generation_b/docs/release" \
	"$root/versions/$generation_c/bin" \
	"$root/versions/$generation_c/libexec" \
	"$root/versions/$generation_c/skills/codex/forgepilot-onboarding" \
	"$root/versions/$generation_c/docs/release" \
	"$root/versions/$generation_d/bin" \
	"$root/versions/$generation_d/libexec" \
	"$root/versions/$generation_d/skills/codex/forgepilot-onboarding" \
	"$root/versions/$generation_d/docs/release" \
	"$root/versions/$generation_e/bin" \
	"$root/versions/$generation_e/libexec" \
	"$root/versions/$generation_e/skills/codex/forgepilot-onboarding" \
	"$root/versions/$generation_e/docs/release" \
	"$root/retention/v1" "$home/.local/bin" "$home/.agents/skills"
touch "$root/lock"
printf '#!/bin/sh\nexit 0\n' >"$root/versions/$generation_a/bin/forgepilot"
printf '#!/bin/sh\nexit 0\n' >"$root/versions/$generation_a/libexec/forgepilot-bootstrap"
printf '# Fixture onboarding procedure\n' >"$root/versions/$generation_a/docs/release/onboarding.md"
printf '# Fixture skill\n' >"$root/versions/$generation_a/skills/codex/forgepilot-onboarding/SKILL.md"
printf '#!/bin/sh\nexit 0\n' >"$root/versions/$generation_b/bin/forgepilot"
printf '#!/bin/sh\nexit 0\n' >"$root/versions/$generation_b/libexec/forgepilot-bootstrap"
printf '# Fixture onboarding procedure B\n' >"$root/versions/$generation_b/docs/release/onboarding.md"
printf '# Fixture skill B\n' >"$root/versions/$generation_b/skills/codex/forgepilot-onboarding/SKILL.md"
printf '#!/bin/sh\nexit 0\n' >"$root/versions/$generation_c/bin/forgepilot"
printf '#!/bin/sh\nexit 0\n' >"$root/versions/$generation_c/libexec/forgepilot-bootstrap"
printf '# Fixture onboarding procedure B\n' >"$root/versions/$generation_c/docs/release/onboarding.md"
printf '# Fixture skill B\n' >"$root/versions/$generation_c/skills/codex/forgepilot-onboarding/SKILL.md"
printf '#!/bin/sh\nexit 0\n' >"$root/versions/$generation_d/bin/forgepilot"
printf '#!/bin/sh\nexit 0\n' >"$root/versions/$generation_d/libexec/forgepilot-bootstrap"
printf '# Fixture onboarding procedure B\n' >"$root/versions/$generation_d/docs/release/onboarding.md"
printf '# Fixture skill B\n' >"$root/versions/$generation_d/skills/codex/forgepilot-onboarding/SKILL.md"
printf '#!/bin/sh\nexit 0\n' >"$root/versions/$generation_e/bin/forgepilot"
printf '#!/bin/sh\nexit 0\n' >"$root/versions/$generation_e/libexec/forgepilot-bootstrap"
printf '# Fixture onboarding procedure B\n' >"$root/versions/$generation_e/docs/release/onboarding.md"
printf '# Fixture skill B\n' >"$root/versions/$generation_e/skills/codex/forgepilot-onboarding/SKILL.md"
chmod 700 "$root/versions/$generation_a/bin/forgepilot" "$root/versions/$generation_a/libexec/forgepilot-bootstrap" \
	"$root/versions/$generation_b/bin/forgepilot" "$root/versions/$generation_b/libexec/forgepilot-bootstrap" \
	"$root/versions/$generation_c/bin/forgepilot" "$root/versions/$generation_c/libexec/forgepilot-bootstrap" \
	"$root/versions/$generation_d/bin/forgepilot" "$root/versions/$generation_d/libexec/forgepilot-bootstrap" \
	"$root/versions/$generation_e/bin/forgepilot" "$root/versions/$generation_e/libexec/forgepilot-bootstrap"
ln -s "versions/$generation_a" "$root/current"
ln -s "versions/$generation_c" "$root/previous"
ln -s "$root/current/bin/forgepilot" "$home/.local/bin/forgepilot"
ln -s "$root/current/libexec/forgepilot-bootstrap" "$home/.local/bin/forgepilot-bootstrap"
ln -s "$root/current/skills/codex/forgepilot-onboarding" "$home/.agents/skills/forgepilot-onboarding"
printf '{"schema_version":1,"generations":{"%s":{"payload_digest":"%s","path":"versions/%s"},"%s":{"payload_digest":"%s","path":"versions/%s"},"%s":{"payload_digest":"%s","path":"versions/%s"},"%s":{"payload_digest":"%s","path":"versions/%s"},"%s":{"payload_digest":"%s","path":"versions/%s"}},"current":"%s","previous":"%s"}\n' \
	"$generation_a" "$digest_a" "$generation_a" \
	"$generation_b" "$digest_b" "$generation_b" \
	"$generation_c" "$digest_c" "$generation_c" \
	"$generation_d" "$digest_d" "$generation_d" \
	"$generation_e" "$digest_e" "$generation_e" \
	"$generation_a" "$generation_c" >"$root/manifest.json"

# Exact tuple acquisition is idempotent; only a hash of the opaque reference is persisted.
reference_a_hash=$(printf '%s' "$reference_a" | shasum -a 256 | awk '{print $1}')
# Internal implementation details are not a second, unlocked mutation entrypoint.
if FORGEPILOT_BOOTSTRAP_LOCKED=1 HOME="$home" "$bootstrap" __retention_locked \
	acquire "$generation_b" "$digest_b" "$reference_b" >/dev/null 2>&1; then
	fail 'direct internal retention invocation bypassed the shared lock'
fi
assert_not_file "$root/retention/v1/$(printf '%s' "$reference_b" | shasum -a 256 | awk '{print $1}')"

output=$(HOME="$home" "$bootstrap" retention-v1 acquire --generation "$generation_a" --payload-digest "$digest_a" --reference "$reference_a") || fail 'valid acquire failed'
assert_output "$output" '"protocol_version":1'
assert_output "$output" '"result":"acquired"'
assert_output "$output" "\"generation_id\":\"$generation_a\""
assert_output "$output" "\"payload_digest\":\"$digest_a\""
assert_not_file "$root/retention/v1/$reference_a"
assert_file "$root/retention/v1/$reference_a_hash"
! grep -R -F -- "$reference_a" "$root/retention" >/dev/null || fail 'raw reference was persisted'

output=$(HOME="$home" "$bootstrap" retention-v1 acquire --generation "$generation_d" --payload-digest "$digest_d" --reference "$reference_c") || fail 'inactive-generation acquire failed'
assert_output "$output" '"result":"acquired"'

output=$(HOME="$home" "$bootstrap" retention-v1 acquire --generation "$generation_a" --payload-digest "$digest_a" --reference "$reference_a") || fail 'idempotent acquire failed'
assert_output "$output" '"result":"already_acquired"'

output=$(HOME="$home" "$bootstrap" status) || fail 'valid status failed'
assert_output "$output" 'Bootstrap status: idle'
assert_output "$output" 'generations=5'
assert_output "$output" 'retention_refs=2'
case "$output" in *"$reference_a"*) fail 'status exposed the raw retention reference' ;; esac

# A managed caller receives one exact, rechecked generation tuple and the
# generation-local CLI/helper paths. It must never reconstruct this from PATH
# or the current pointer after the helper's read completes.
output=$(HOME="$home" "$bootstrap" generation-v1 current) || fail 'current generation discovery failed'
assert_output "$output" '"protocol_version":1'
assert_output "$output" "\"generation_id\":\"$generation_a\""
assert_output "$output" "\"payload_digest\":\"$digest_a\""
assert_output "$output" "\"forgepilot_path\":\"$root/versions/$generation_a/bin/forgepilot\""
assert_output "$output" "\"helper_path\":\"$root/versions/$generation_a/libexec/forgepilot-bootstrap\""

decode_action_plan() {
	assert_action_file=$1
	assert_plan_id=$2
	assert_plan_mode=$3
	assert_payload_b=$4
	assert_payload_e=$5
	assert_retention_digest=$6
	assert_manifest_digest=$7
	assert_action_fields=$fixture/action-fields
	/usr/bin/tr '\000' '\n' <"$assert_action_file" >"$assert_action_fields"
	assert_last_byte=$(/usr/bin/tail -c 1 "$assert_action_file" | /usr/bin/od -An -t x1 | /usr/bin/tr -d ' \n')
	[ "$assert_last_byte" = 00 ] || fail 'machine action stream is missing its final NUL delimiter'
	/usr/bin/awk \
		-v root="$root" \
		-v lock="$root/lock" \
		-v manifest="$root/manifest.json" \
		-v transaction="$root/transaction.json" \
		-v generation_b="$generation_b" \
		-v generation_e="$generation_e" \
		-v generation_a="$generation_a" \
		-v generation_c="$generation_c" \
		-v digest_b="$assert_payload_b" \
		-v digest_e="$assert_payload_e" \
		-v retention_digest="$assert_retention_digest" \
		-v manifest_digest="$assert_manifest_digest" \
		-v plan_id="$assert_plan_id" \
		-v mode="$assert_plan_mode" '
		function fail(message) { print "forgepilot-bootstrap_test: " message > "/dev/stderr"; exit 1 }
		function valid_digest(value) { return length(value) == 64 && value !~ /[^0-9a-f]/ }
		function action(phase, id, directory, effect, count, arg1, arg2, arg3, arg4, arg5, arg6, arg7, arg8, actual, expected, i) {
			if (cursor + 4 > total) fail("truncated machine action record")
			if (fields[cursor] != phase || fields[cursor + 1] != id || fields[cursor + 2] != directory || fields[cursor + 3] != effect) fail("machine action identity or order is wrong")
			actual = fields[cursor + 4]
			if (actual !~ /^[0-9]+$/ || actual != count) fail("machine action argument count is wrong")
			for (i = 1; i <= count; i++) {
				expected = (i == 1 ? arg1 : i == 2 ? arg2 : i == 3 ? arg3 : i == 4 ? arg4 : i == 5 ? arg5 : i == 6 ? arg6 : i == 7 ? arg7 : arg8)
				if (fields[cursor + 4 + i] != expected) fail("machine action argument is wrong")
			}
			cursor += 5 + count
		}
		{ fields[NR] = $0 }
		END {
			total = NR
			if (total > 0 && fields[total] == "") total--
			if (fields[1] != "forgepilot-bootstrap-plan-v1") fail("unknown machine action protocol version")
			cursor = 2
			action("inspect", "validate-managed-layout", root, "read-managed-state", 0)
			action("lock", "acquire-bootstrap-lock", root, "exclusive-bootstrap-lock", 1, lock)
			print "validate managed layout"
			print "acquire Bootstrap exclusive lock"
			if (mode == "candidates") {
				if (cursor + 6 > total) fail("missing prune-state revalidation record")
				facts_digest = fields[cursor + 5]
				candidates_digest = fields[cursor + 6]
				if (!valid_digest(facts_digest) || !valid_digest(candidates_digest)) fail("prune revalidation digest is malformed")
				action("revalidate", "compare-prune-facts", root, "compare-prune-state", 2, facts_digest, candidates_digest)
				print "revalidate prune facts"
				action("transaction", "record-removal-transaction", root, "atomic-removal-transaction", 8, "prune", manifest, manifest_digest, candidates_digest, generation_b, digest_b, generation_e, digest_e)
				print "record removal transaction for exact manifest entries"
				action("revalidate", "revalidate-generation-" generation_b, root, "verify-removable-generation", 5, generation_b, digest_b, retention_digest, generation_a, generation_c)
				print "revalidate generation " generation_b
				action("remove", "remove-generation-" generation_b, root, "remove-generation", 2, "versions/" generation_b, digest_b)
				print "remove versions/" generation_b " (payload " digest_b ")"
				action("revalidate", "revalidate-generation-" generation_e, root, "verify-removable-generation", 5, generation_e, digest_e, retention_digest, generation_a, generation_c)
				print "revalidate generation " generation_e
				action("remove", "remove-generation-" generation_e, root, "remove-generation", 2, "versions/" generation_e, digest_e)
				print "remove versions/" generation_e " (payload " digest_e ")"
				action("revalidate", "revalidate-prune-manifest", root, "verify-prune-manifest", 2, manifest_digest, candidates_digest)
				print "revalidate manifest before publication"
				action("publish", "publish-manifest", root, "atomic-manifest-replacement", 2, manifest, candidates_digest)
				print "publish updated manifest"
				action("revalidate", "revalidate-prune-transaction", root, "verify-removal-transaction", 2, "prune", candidates_digest)
				print "revalidate prune transaction before finalizing"
				action("finalize", "remove-transaction", root, "remove-transaction", 1, transaction)
				print "remove prune transaction"
			} else {
				if (cursor + 6 > total) fail("missing no-op prune-state revalidation record")
				facts_digest = fields[cursor + 5]
				candidates_digest = fields[cursor + 6]
				if (!valid_digest(facts_digest) || !valid_digest(candidates_digest)) fail("no-op prune digest is malformed")
				action("revalidate", "compare-prune-facts", root, "compare-prune-state", 2, facts_digest, candidates_digest)
				print "revalidate prune facts"
				action("inspect", "no-removable-generations", root, "no-op", 0)
				print "no inactive generation is eligible for removal"
			}
			action("metadata", "approval-plan-id", root, "bootstrap-approval-id", 1, plan_id)
			if (cursor != total + 1) fail("unexpected or duplicated machine action record")
		}
		' "$assert_action_fields" || fail 'machine action stream does not match the prune plan contract'
}

# Prune planning binds exact candidates, protects current/previous/retained generations, and is inspection-only.
plan_home_before=$(fingerprint_home "$home")
plan=$(HOME="$home" "$bootstrap" plan --prune) || fail 'prune planning failed'
assert_output "$plan" 'Operation: prune'
assert_output "$plan" 'Plan ID: sha256:'
assert_output "$plan" "Action: remove versions/$generation_b"
assert_output "$plan" "Action: remove versions/$generation_e"
for protected_generation in "$generation_a" "$generation_c" "$generation_d"; do
	case "$plan" in *"Action: remove versions/$protected_generation"*) fail "prune plan selected protected generation $protected_generation" ;; esac
done
plan_id=$(printf '%s\n' "$plan" | /usr/bin/awk -F': ' '$1 == "Plan ID" { print $2 }')
case "$plan_id" in sha256:*) ;; *) fail 'prune plan ID is not a SHA-256 digest' ;; esac
[ "${#plan_id}" -eq 71 ] || fail 'prune plan ID is not a complete SHA-256 digest'
repeat_plan=$(HOME="$home" "$bootstrap" plan --prune) || fail 'repeat prune planning failed'
repeat_plan_id=$(printf '%s\n' "$repeat_plan" | /usr/bin/awk -F': ' '$1 == "Plan ID" { print $2 }')
[ "$repeat_plan_id" = "$plan_id" ] || fail 'unchanged prune facts produced a different plan ID'
plan_home_after=$(fingerprint_home "$home")
[ "$plan_home_after" = "$plan_home_before" ] || fail 'prune planning changed user-home paths, modes, links, or contents'
approved_plan_id=$plan_id
HOME="$home" "$bootstrap" retention-v1 acquire --generation "$generation_d" --payload-digest "$digest_d" --reference "$reference_f" >/dev/null || fail 'could not add a reference to an already-retained generation'
plan_home_before=$(fingerprint_home "$home")
plan=$(HOME="$home" "$bootstrap" plan --prune) || fail 'prune planning after retention change failed'
plan_id=$(printf '%s\n' "$plan" | /usr/bin/awk -F': ' '$1 == "Plan ID" { print $2 }')
[ "$plan_id" != "$approved_plan_id" ] || fail 'retention acquire did not invalidate the earlier prune plan ID'
plan_home_after=$(fingerprint_home "$home")
[ "$plan_home_after" = "$plan_home_before" ] || fail 'prune planning after retention change modified user-home paths, modes, links, or contents'
assert_not_file "$root/transaction.json"

# The machine renderer exposes the same plan and its approval ID as NUL-framed fields.
actions_file=$fixture/prune-actions
if HOME="$home" "$bootstrap" plan --prune --format actions >"$actions_file"; then
	:
else
	fail 'NUL action planning failed'
fi
candidate_b_digest=$(printf '%s\n' "$plan" | /usr/bin/awk -v target="versions/$generation_b" '$1 == "Action:" && $2 == "remove" && $3 == target { value = $5; gsub(/[()]/, "", value); print value }')
candidate_e_digest=$(printf '%s\n' "$plan" | /usr/bin/awk -v target="versions/$generation_e" '$1 == "Action:" && $2 == "remove" && $3 == target { value = $5; gsub(/[()]/, "", value); print value }')
retention_digest=$(printf '%s\n' "$plan" | /usr/bin/awk '$1 == "Retention" { value = $4; gsub(/[()]/, "", value); sub(/^sha256:/, "", value); print value }')
manifest_digest=$(/usr/bin/shasum -a 256 "$root/manifest.json" | /usr/bin/awk '{ print $1 }')
actual_human_actions=$(printf '%s\n' "$plan" | /usr/bin/sed -n 's/^Action: //p')
machine_human_actions=$(decode_action_plan "$actions_file" "$plan_id" candidates "$candidate_b_digest" "$candidate_e_digest" "$retention_digest" "$manifest_digest")
[ "$actual_human_actions" = "$machine_human_actions" ] || fail 'human renderer does not match the machine action list'
if HOME="$home" "$bootstrap" plan --prune --format human --format actions >/dev/null 2>&1; then
	fail 'plan accepted duplicate renderer options'
fi

# A plan with no removable generations reports the same no-op in both renderers.
HOME="$home" "$bootstrap" retention-v1 acquire --generation "$generation_b" --payload-digest "$digest_b" --reference "$reference_d" >/dev/null || fail 'could not retain generation B for no-op plan'
HOME="$home" "$bootstrap" retention-v1 acquire --generation "$generation_e" --payload-digest "$digest_e" --reference "$reference_e" >/dev/null || fail 'could not retain generation E for no-op plan'
no_op_plan=$(HOME="$home" "$bootstrap" plan --prune) || fail 'no-op prune planning failed'
no_op_id=$(printf '%s\n' "$no_op_plan" | /usr/bin/awk -F': ' '$1 == "Plan ID" { print $2 }')
no_op_actions=$fixture/prune-noop-actions
HOME="$home" "$bootstrap" plan --prune --format actions >"$no_op_actions" || fail 'no-op machine prune planning failed'
no_op_human_actions=$(printf '%s\n' "$no_op_plan" | /usr/bin/sed -n 's/^Action: //p')
machine_no_op_human_actions=$(decode_action_plan "$no_op_actions" "$no_op_id" no-op '' '' '' '')
[ "$no_op_human_actions" = "$machine_no_op_human_actions" ] || fail 'no-op renderers do not share the same action list'

# External entrypoint parents are managed paths too; unsafe modes and symlinked parents fail closed.
chmod g+w "$home/.agents/skills"
if HOME="$home" "$bootstrap" status >/dev/null 2>&1; then fail 'status accepted a group-writable entrypoint directory'; fi
chmod g-w "$home/.agents/skills"
mv "$home/.agents" "$home/.agents.saved"
ln -s "$home/.agents.saved" "$home/.agents"
if HOME="$home" "$bootstrap" status >/dev/null 2>&1; then fail 'status accepted a symlinked entrypoint parent'; fi
rm "$home/.agents"
mv "$home/.agents.saved" "$home/.agents"

# A reference cannot be rebound, and release must present the exact tuple.
if error=$(HOME="$home" "$bootstrap" retention-v1 acquire --generation "$generation_b" --payload-digest "$digest_b" --reference "$reference_a" 2>&1); then
	fail 'acquire rebound an existing reference'
fi
case "$error" in *"$reference_a"*) fail 'error output exposed the raw retention reference' ;; esac
if HOME="$home" "$bootstrap" retention-v1 release --generation "$generation_b" --payload-digest "$digest_b" --reference "$reference_a" >/dev/null 2>&1; then
	fail 'release accepted a mismatched generation tuple'
fi
if HOME="$home" "$bootstrap" retention-v1 acquire --generation "$generation_a" --payload-digest "$digest_a" --reference "$reference_b" --target "$fixture/unexpected-target" >/dev/null 2>&1; then
	fail 'retention protocol accepted a target repository argument'
fi

output=$(HOME="$home" "$bootstrap" retention-v1 release --generation "$generation_a" --payload-digest "$digest_a" --reference "$reference_a") || fail 'valid release failed'
assert_output "$output" '"result":"released"'
assert_not_file "$root/retention/v1/$reference_a_hash"
output=$(HOME="$home" "$bootstrap" retention-v1 release --generation "$generation_a" --payload-digest "$digest_a" --reference "$reference_a") || fail 'idempotent release failed'
assert_output "$output" '"result":"already_released"'

# Unknown retention entries are uncertainty, never an empty retention set.
printf 'unexpected\n' >"$root/retention/v1/not-a-reference-hash"
if HOME="$home" "$bootstrap" retention-v1 acquire --generation "$generation_a" --payload-digest "$digest_a" --reference "$reference_b" >/dev/null 2>&1; then
	fail 'acquire proceeded with malformed retention state'
fi
if HOME="$home" "$bootstrap" plan --prune >/dev/null 2>&1; then
	fail 'prune planning accepted malformed retention state'
fi
if HOME="$home" "$bootstrap" status >/dev/null 2>&1; then fail 'status accepted malformed retention state'; fi
rm "$root/retention/v1/not-a-reference-hash"

printf '{"operation":"prune"}\n' >"$root/transaction.json"
if HOME="$home" "$bootstrap" status >/dev/null 2>&1; then fail 'status declared a non-idle transaction healthy'; fi
rm "$root/transaction.json"

# The same advisory lock serializes marker changes with layout/removal operations.
holder_ready=$fixture/holder-ready
/usr/bin/lockf -k "$root/lock" sh -c "touch \"\$1\"; sleep 1" sh "$holder_ready" &
holder_pid=$!
attempt=0
while [ ! -f "$holder_ready" ]; do
	attempt=$((attempt + 1))
	[ "$attempt" -lt 40 ] || fail 'lock holder did not start'
	sleep 0.05
done
reference_b_hash=$(printf '%s' "$reference_b" | shasum -a 256 | awk '{print $1}')
HOME="$home" "$bootstrap" retention-v1 acquire --generation "$generation_b" --payload-digest "$digest_b" --reference "$reference_b" >"$fixture/lock-result" &
acquire_pid=$!
lock_waiting=0
attempt=0
while [ "$attempt" -lt 40 ]; do
	if /bin/ps -axo command= | /usr/bin/awk '$1 == "/usr/bin/lockf" && $2 == "-s" && $3 == "-t" && $4 == "30" && $5 == "9" { found = 1 } END { exit !found }'; then
		lock_waiting=1
		break
	fi
	attempt=$((attempt + 1))
	sleep 0.05
done
[ "$lock_waiting" -eq 1 ] || fail 'retention operation did not reach the shared lock wait'
assert_not_file "$root/retention/v1/$reference_b_hash"
wait "$holder_pid"
wait "$acquire_pid" || fail 'acquire did not continue after the shared lock was released'
assert_file "$root/retention/v1/$reference_b_hash"
HOME="$home" "$bootstrap" retention-v1 release --generation "$generation_b" --payload-digest "$digest_b" --reference "$reference_b" >/dev/null || fail 'cleanup release failed'
HOME="$home" "$bootstrap" retention-v1 release --generation "$generation_d" --payload-digest "$digest_d" --reference "$reference_c" >/dev/null || fail 'retained previous fixture cleanup release failed'
HOME="$home" "$bootstrap" retention-v1 release --generation "$generation_d" --payload-digest "$digest_d" --reference "$reference_f" >/dev/null || fail 'additional retained previous fixture cleanup release failed'
HOME="$home" "$bootstrap" retention-v1 release --generation "$generation_b" --payload-digest "$digest_b" --reference "$reference_d" >/dev/null || fail 'no-op generation B cleanup release failed'
HOME="$home" "$bootstrap" retention-v1 release --generation "$generation_e" --payload-digest "$digest_e" --reference "$reference_e" >/dev/null || fail 'no-op generation E cleanup release failed'

/bin/rmdir "$root/retention/v1"
if HOME="$home" "$bootstrap" status >/dev/null 2>&1; then fail 'status accepted a missing retention store'; fi
if HOME="$home" "$bootstrap" plan --prune >/dev/null 2>&1; then
	fail 'prune planning accepted a missing retention store'
fi

# lockf must never recreate a missing lock marker.
mv "$root/lock" "$root/lock.saved"
if HOME="$home" "$bootstrap" retention-v1 acquire --generation "$generation_a" --payload-digest "$digest_a" --reference "$reference_a" >/dev/null 2>&1; then
	fail 'retention operation accepted a missing lock marker'
fi
assert_not_file "$root/lock"
mv "$root/lock.saved" "$root/lock"

printf '%s\n' 'forgepilot-bootstrap_test: retention protocol PASS'
