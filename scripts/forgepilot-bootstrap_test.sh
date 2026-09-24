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

# Exercise the production action decoder directly, including bytes that a
# NUL-to-newline conversion would split incorrectly.
action_fixture=$fixture/action-parser
mkdir "$action_fixture"
/usr/bin/sed '$d' "$bootstrap" >"$action_fixture/functions.sh"
action_directory=$action_fixture/$(printf 'line\nnext')
action_argument=$(printf 'argument\\backslash\nnext\nX')
action_argument=${action_argument%X}
printf '%s\0' forgepilot-bootstrap-plan-v1 inspect inspect-source "$action_directory" read-exact-source 1 "$action_argument" >"$action_fixture/valid"
(
	. "$action_fixture/functions.sh"
	TEMP_DIR=$action_fixture
	open_action_stream "$action_fixture/valid"
	expect_plan_action inspect inspect-source "$action_directory" read-exact-source "$action_argument"
	finish_action_stream
) || fail 'production action decoder lost an embedded newline'
printf '%s\0' forgepilot-bootstrap-plan-v1 inspect parsed-argv "$action_directory" no-op 1 "$action_argument" >"$action_fixture/dispatch-argv"
(
	. "$action_fixture/functions.sh"
	TEMP_DIR=$action_fixture
	ACTION_MODE=execute
	execute_removal_action() { printf '%s' "$4" >"$action_fixture/observed-argv"; }
	open_action_stream "$action_fixture/dispatch-argv"
	append_plan_action "$ACTION_FIELDS" "$ACTION_FIELDS" inspect parsed-argv "$action_directory" no-op dispatch "$action_argument"
	finish_action_stream
) || fail 'production action dispatcher rejected a valid argv'
printf '%s' "$action_argument" >"$action_fixture/expected-argv"
/usr/bin/cmp -s "$action_fixture/expected-argv" "$action_fixture/observed-argv" || fail 'production action dispatcher changed decoded argv bytes'
reject_action_stream() {
	if (
		. "$action_fixture/functions.sh"
		TEMP_DIR=$action_fixture
		open_action_stream "$action_fixture/$1"
		expect_plan_action inspect inspect-source "$action_directory" read-exact-source "$action_argument"
		finish_action_stream
	) >/dev/null 2>&1; then fail "production action decoder accepted $1"; fi
}
printf '%s\0' unknown-plan-v1 inspect inspect-source "$action_directory" read-exact-source 1 "$action_argument" >"$action_fixture/unknown-version"
reject_action_stream unknown-version
printf '%s\0' forgepilot-bootstrap-plan-v1 inspect inspect-source "$action_directory" read-exact-source 1 >"$action_fixture/truncated"
reject_action_stream truncated
printf '%s\0' forgepilot-bootstrap-plan-v1 inspect inspect-source "$action_directory" unexpected-command 1 "$action_argument" >"$action_fixture/unexpected-command"
reject_action_stream unexpected-command
printf '%s\0' forgepilot-bootstrap-plan-v1 inspect inspect-source "$action_fixture/../escape" read-exact-source 1 "$action_argument" >"$action_fixture/path-escape"
reject_action_stream path-escape
printf '%s\0' forgepilot-bootstrap-plan-v1 inspect inspect-source "$action_directory" read-exact-source 2 "$action_argument" >"$action_fixture/wrong-count"
reject_action_stream wrong-count
printf '%s\0' forgepilot-bootstrap-plan-v1 inspect inspect-source "$action_directory" read-exact-source 1 >"$action_fixture/missing-argv"
reject_action_stream missing-argv
printf '%s\0' forgepilot-bootstrap-plan-v1 inspect inspect-source "$action_directory" read-exact-source 1 >"$action_fixture/missing-final-nul"
printf '%s' "$action_argument" >>"$action_fixture/missing-final-nul"
reject_action_stream missing-final-nul
printf '%s\0' forgepilot-bootstrap-plan-v1 inspect inspect-source "$action_directory" read-exact-source 1 "$action_argument" inspect inspect-source "$action_directory" read-exact-source 1 "$action_argument" >"$action_fixture/duplicate-id"
reject_action_stream duplicate-id
if (
	. "$action_fixture/functions.sh"
	TEMP_DIR=$action_fixture
	open_action_stream "$action_fixture/duplicate-id"
	expect_plan_action inspect inspect-source "$action_directory" read-exact-source "$action_argument"
	expect_plan_action inspect inspect-source "$action_directory" read-exact-source "$action_argument"
	finish_action_stream
) >/dev/null 2>&1; then fail 'production action decoder accepted a repeated action ID'; fi
printf '%s\0' forgepilot-bootstrap-plan-v1 revalidate second "$action_directory" no-op 0 inspect inspect-source "$action_directory" read-exact-source 1 "$action_argument" >"$action_fixture/reordered"
reject_action_stream reordered
printf '%s\0' forgepilot-bootstrap-plan-v1 inspect first "$action_directory" no-op 0 inspect second "$action_directory" no-op 0 >"$action_fixture/failing-action"
if (
	. "$action_fixture/functions.sh"
	TEMP_DIR=$action_fixture
	ACTION_MODE=execute
	execute_removal_action() {
		if [ "$2" = first ]; then return 1; fi
		printf 'unexpected second action\n' >"$action_fixture/second-action-ran"
	}
	open_action_stream "$action_fixture/failing-action"
	append_plan_action "$ACTION_FIELDS" "$ACTION_FIELDS" inspect first "$action_directory" no-op first
	append_plan_action "$ACTION_FIELDS" "$ACTION_FIELDS" inspect second "$action_directory" no-op second
	finish_action_stream
) >/dev/null 2>&1; then fail 'failed action was reported as successful'; fi
assert_not_file "$action_fixture/second-action-ran"

printf '%s\0' forgepilot-bootstrap-plan-v1 inspect install-parsed-argv "$action_directory" no-op 1 "$action_argument" >"$action_fixture/install-dispatch-argv"
(
	. "$action_fixture/functions.sh"
	TEMP_DIR=$action_fixture
	ACTION_MODE=execute
	ACTION_DOMAIN=install
	execute_install_action() { INSTALL_OBSERVED_ARGUMENT=$4; }
	open_action_stream "$action_fixture/install-dispatch-argv"
	append_plan_action "$ACTION_FIELDS" "$ACTION_FIELDS" inspect install-parsed-argv "$action_directory" no-op dispatch "$action_argument"
	finish_action_stream
	[ "$INSTALL_OBSERVED_ARGUMENT" = "$action_argument" ]
) || fail 'install action dispatcher lost decoded argv or parent-shell state'
printf '%s\0' forgepilot-bootstrap-plan-v1 inspect install-first "$action_directory" no-op 0 inspect install-second "$action_directory" no-op 0 >"$action_fixture/failing-install-action"
if (
	. "$action_fixture/functions.sh"
	TEMP_DIR=$action_fixture
	ACTION_MODE=execute
	ACTION_DOMAIN=install
	execute_install_action() {
		if [ "$2" = install-first ]; then return 1; fi
		printf 'unexpected second install action\n' >"$action_fixture/second-install-action-ran"
	}
	open_action_stream "$action_fixture/failing-install-action"
	append_plan_action "$ACTION_FIELDS" "$ACTION_FIELDS" inspect install-first "$action_directory" no-op first
	append_plan_action "$ACTION_FIELDS" "$ACTION_FIELDS" inspect install-second "$action_directory" no-op second
	finish_action_stream
) >/dev/null 2>&1; then fail 'failed install action was reported as successful'; fi
assert_not_file "$action_fixture/second-install-action-ran"

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

credential_sentinel=runtime-credential-sentinel-7d53e2
output=$(FORGEPILOT_TEST_RUNTIME_CREDENTIAL="$credential_sentinel" HOME="$home" "$bootstrap" retention-v1 acquire --generation "$generation_a" --payload-digest "$digest_a" --reference "$reference_a") || fail 'valid acquire failed'
assert_output "$output" '"protocol_version":1'
assert_output "$output" '"result":"acquired"'
assert_output "$output" "\"generation_id\":\"$generation_a\""
assert_output "$output" "\"payload_digest\":\"$digest_a\""
assert_not_file "$root/retention/v1/$reference_a"
assert_file "$root/retention/v1/$reference_a_hash"
! grep -R -F -- "$reference_a" "$root/retention" >/dev/null || fail 'raw reference was persisted'
! grep -R -F -- "$credential_sentinel" "$root/retention" >/dev/null || fail 'runtime credential was persisted in a retention marker'

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

# A valid HOME can contain JSON-significant bytes. Use a complete isolated
# managed layout so this tests generation-v1, not a synthetic string encoder.
escaped_home=$fixture/'home"quote\slash'
/bin/cp -pR "$home" "$escaped_home"
escaped_root=$escaped_home/.local/share/forgepilot
rm "$escaped_home/.local/bin/forgepilot" "$escaped_home/.local/bin/forgepilot-bootstrap" "$escaped_home/.agents/skills/forgepilot-onboarding"
ln -s "$escaped_root/current/bin/forgepilot" "$escaped_home/.local/bin/forgepilot"
ln -s "$escaped_root/current/libexec/forgepilot-bootstrap" "$escaped_home/.local/bin/forgepilot-bootstrap"
ln -s "$escaped_root/current/skills/codex/forgepilot-onboarding" "$escaped_home/.agents/skills/forgepilot-onboarding"
output=$(HOME="$escaped_home" "$bootstrap" generation-v1 current) || fail 'escaped-path generation discovery failed'
assert_output "$output" 'home\"quote\\slash'
assert_output "$output" '"forgepilot_path":"'
assert_output "$output" '"helper_path":"'

# Control bytes are valid POSIX path bytes but unsupported by this v1 machine
# string encoder; reject them before layout inspection instead of emitting bad JSON.
control_home=$fixture/'home-control'$(printf '\001')'byte'
if output=$(HOME="$control_home" "$bootstrap" generation-v1 current 2>&1); then
	fail 'generation discovery accepted a control byte in HOME'
fi
assert_output "$output" 'HOME contains a JSON control byte'
invalid_utf8_home=$fixture/'home-invalid'$(printf '\200')'byte'
if output=$(HOME="$invalid_utf8_home" "$bootstrap" generation-v1 current 2>&1); then
	fail 'generation discovery accepted non-UTF-8 HOME bytes'
fi
assert_output "$output" 'HOME is not valid UTF-8'

decode_action_plan() {
	assert_action_file=$1
	assert_plan_id=$2
	assert_plan_mode=$3
	assert_payload_b=$4
	assert_payload_e=$5
	assert_retention_digest=$6
	assert_manifest_digest=$7
	assert_action_fields=$fixture/action-fields
	assert_identity_b=$(/usr/bin/stat -f '%d,%i' "$root/versions/$generation_b")
	assert_identity_e=$(/usr/bin/stat -f '%d,%i' "$root/versions/$generation_e")
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
		-v identity_b="$assert_identity_b" \
		-v identity_e="$assert_identity_e" \
		-v retention_digest="$assert_retention_digest" \
		-v manifest_digest="$assert_manifest_digest" \
		-v plan_id="$assert_plan_id" \
		-v mode="$assert_plan_mode" '
		function fail(message) { print "forgepilot-bootstrap_test: " message > "/dev/stderr"; exit 1 }
		function valid_digest(value) { return length(value) == 64 && value !~ /[^0-9a-f]/ }
		function action(phase, id, directory, effect, count, arg1, arg2, arg3, arg4, arg5, arg6, arg7, arg8, arg9, arg10, actual, expected, i) {
			if (cursor + 4 > total) fail("truncated machine action record")
			if (fields[cursor] != phase || fields[cursor + 1] != id || fields[cursor + 2] != directory || fields[cursor + 3] != effect) fail("machine action identity or order is wrong")
			actual = fields[cursor + 4]
			if (actual !~ /^[0-9]+$/ || actual != count) fail("machine action argument count is wrong")
			for (i = 1; i <= count; i++) {
				expected = (i == 1 ? arg1 : i == 2 ? arg2 : i == 3 ? arg3 : i == 4 ? arg4 : i == 5 ? arg5 : i == 6 ? arg6 : i == 7 ? arg7 : i == 8 ? arg8 : i == 9 ? arg9 : arg10)
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
				action("transaction", "record-removal-transaction", root, "atomic-removal-transaction", 10, "prune", manifest, manifest_digest, candidates_digest, generation_b, digest_b, identity_b, generation_e, digest_e, identity_e)
				print "record removal transaction for exact manifest entries"
				action("revalidate", "revalidate-stage-" generation_b, root, "verify-removable-generation", 5, generation_b, digest_b, retention_digest, generation_a, generation_c)
				print "revalidate generation " generation_b " before staging"
				action("stage", "stage-generation-" generation_b, root, "stage-generation", 2, "versions/" generation_b, digest_b)
				print "stage versions/" generation_b " (payload " digest_b ")"
				action("revalidate", "revalidate-stage-" generation_e, root, "verify-removable-generation", 5, generation_e, digest_e, retention_digest, generation_a, generation_c)
				print "revalidate generation " generation_e " before staging"
				action("stage", "stage-generation-" generation_e, root, "stage-generation", 2, "versions/" generation_e, digest_e)
				print "stage versions/" generation_e " (payload " digest_e ")"
				action("revalidate", "revalidate-prune-manifest", root, "verify-prune-manifest", 2, manifest_digest, candidates_digest)
				print "revalidate manifest before publication"
				action("publish", "publish-manifest", root, "atomic-manifest-replacement", 2, manifest, candidates_digest)
				print "publish updated manifest"
				action("revalidate", "revalidate-remove-" generation_b, root, "verify-staged-generation", 5, generation_b, digest_b, retention_digest, generation_a, generation_c)
				print "revalidate staged generation " generation_b " before deletion"
				action("remove", "remove-generation-" generation_b, root, "remove-generation", 2, "versions/" generation_b, digest_b)
				print "remove versions/" generation_b " (payload " digest_b ")"
				action("revalidate", "revalidate-remove-" generation_e, root, "verify-staged-generation", 5, generation_e, digest_e, retention_digest, generation_a, generation_c)
				print "revalidate staged generation " generation_e " before deletion"
				action("remove", "remove-generation-" generation_e, root, "remove-generation", 2, "versions/" generation_e, digest_e)
				print "remove versions/" generation_e " (payload " digest_e ")"
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

# A same-payload path substituted after planning cannot acquire a fresh
# transaction identity from the new inode.
original_b_identity=$(/usr/bin/stat -f '%d,%i' "$root/versions/$generation_b")
substitution_backup=$fixture/original-generation-b
if (
	. "$action_fixture/functions.sh"
	HOME=$home
	export HOME
	REMOVAL_OPERATION=prune
	REMOVAL_COMMIT=''
	setup_paths
	setup_temp
	trap cleanup EXIT HUP INT TERM
	build_prune_plan
	approved_b_identity=$(/usr/bin/stat -f '%d,%i' "$VERSIONS/$generation_b")
	approved_e_identity=$(/usr/bin/stat -f '%d,%i' "$VERSIONS/$generation_e")
	/bin/mv "$VERSIONS/$generation_b" "$substitution_backup"
	/bin/cp -pR "$substitution_backup" "$VERSIONS/$generation_b"
	[ "$(payload_digest "$VERSIONS/$generation_b")" = "$digest_b" ] || fail 'replacement changed candidate payload'
	write_removal_transaction prune "$MANIFEST" "$PRUNE_MANIFEST_DIGEST" "$PRUNE_CANDIDATES_DIGEST" \
		"$generation_b" "$digest_b" "$approved_b_identity" "$generation_e" "$digest_e" "$approved_e_identity"
) >/dev/null 2>&1; then fail 'transaction accepted a same-payload substituted candidate inode'; fi
[ -d "$substitution_backup" ] || fail 'candidate substitution fixture did not reach the path swap'
[ "$(/usr/bin/stat -f '%d,%i' "$root/versions/$generation_b")" != "$original_b_identity" ] || fail 'candidate substitution reused the original inode'
assert_not_file "$root/transaction.json"
/bin/rm -r "$root/versions/$generation_b"
/bin/mv "$substitution_backup" "$root/versions/$generation_b"
[ "$(/usr/bin/stat -f '%d,%i' "$root/versions/$generation_b")" = "$original_b_identity" ] || fail 'candidate substitution fixture did not restore the original path'

approved_plan_id=$plan_id
HOME="$home" "$bootstrap" retention-v1 acquire --generation "$generation_d" --payload-digest "$digest_d" --reference "$reference_f" >/dev/null || fail 'could not add a reference to an already-retained generation'
plan_home_before=$(fingerprint_home "$home")
plan=$(HOME="$home" "$bootstrap" plan --prune) || fail 'prune planning after retention change failed'
plan_id=$(printf '%s\n' "$plan" | /usr/bin/awk -F': ' '$1 == "Plan ID" { print $2 }')
[ "$plan_id" != "$approved_plan_id" ] || fail 'retention acquire did not invalidate the earlier prune plan ID'
if HOME="$home" "$bootstrap" prune --approve "$approved_plan_id" >/dev/null 2>&1; then
	fail 'prune accepted an approval invalidated by a retention change'
fi
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
HOME="$home" "$bootstrap" retention-v1 release --generation "$generation_d" --payload-digest "$digest_d" --reference "$reference_f" >/dev/null || fail 'additional retained previous fixture cleanup release failed'
HOME="$home" "$bootstrap" retention-v1 release --generation "$generation_b" --payload-digest "$digest_b" --reference "$reference_d" >/dev/null || fail 'no-op generation B cleanup release failed'
HOME="$home" "$bootstrap" retention-v1 release --generation "$generation_e" --payload-digest "$digest_e" --reference "$reference_e" >/dev/null || fail 'no-op generation E cleanup release failed'

# Exact-generation uninstall and prune execute only newly approved removals.
multi_home=$fixture/multi-home
/bin/cp -pR "$home" "$multi_home"
multi_root=$multi_home/.local/share/forgepilot
rm "$multi_home/.local/bin/forgepilot" "$multi_home/.local/bin/forgepilot-bootstrap" "$multi_home/.agents/skills/forgepilot-onboarding"
ln -s "$multi_root/current/bin/forgepilot" "$multi_home/.local/bin/forgepilot"
ln -s "$multi_root/current/libexec/forgepilot-bootstrap" "$multi_home/.local/bin/forgepilot-bootstrap"
ln -s "$multi_root/current/skills/codex/forgepilot-onboarding" "$multi_home/.agents/skills/forgepilot-onboarding"
multi_plan=$(HOME="$multi_home" "$bootstrap" plan --prune) || fail 'multi-candidate prune planning failed'
multi_id=$(printf '%s\n' "$multi_plan" | /usr/bin/awk -F': ' '$1 == "Plan ID" { print $2 }')
HOME="$multi_home" "$bootstrap" prune --approve "$multi_id" || fail 'approved multi-candidate prune failed'
assert_not_file "$multi_root/versions/$generation_b"
assert_not_file "$multi_root/versions/$generation_e"
for protected_generation in "$generation_a" "$generation_c" "$generation_d"; do
	assert_file "$multi_root/versions/$protected_generation/bin/forgepilot"
done
HOME="$multi_home" "$bootstrap" status >/dev/null || fail 'multi-candidate prune left an invalid layout'

# Resume only the recorded candidates after an interrupted first generation move.
recovery_home=$fixture/recovery-home
/bin/cp -pR "$home" "$recovery_home"
recovery_root=$recovery_home/.local/share/forgepilot
rm "$recovery_home/.local/bin/forgepilot" "$recovery_home/.local/bin/forgepilot-bootstrap" "$recovery_home/.agents/skills/forgepilot-onboarding"
ln -s "$recovery_root/current/bin/forgepilot" "$recovery_home/.local/bin/forgepilot"
ln -s "$recovery_root/current/libexec/forgepilot-bootstrap" "$recovery_home/.local/bin/forgepilot-bootstrap"
ln -s "$recovery_root/current/skills/codex/forgepilot-onboarding" "$recovery_home/.agents/skills/forgepilot-onboarding"
recovery_plan=$(HOME="$recovery_home" "$bootstrap" plan --prune) || fail 'recovery fixture planning failed'
recovery_idle_id=$(printf '%s\n' "$recovery_plan" | /usr/bin/awk -F': ' '$1 == "Plan ID" { print $2 }')
recovery_retention_count=$(printf '%s\n' "$recovery_plan" | /usr/bin/awk '$1 == "Retention" { print $3 }')
recovery_retention_digest=$(printf '%s\n' "$recovery_plan" | /usr/bin/awk '$1 == "Retention" { value = $4; gsub(/[()]/, "", value); sub(/^sha256:/, "", value); print value }')
recovery_manifest_digest=$(/usr/bin/shasum -a 256 "$recovery_root/manifest.json" | /usr/bin/awk '{ print $1 }')
recovery_b_identity=$(/usr/bin/stat -f '%d,%i' "$recovery_root/versions/$generation_b")
recovery_e_identity=$(/usr/bin/stat -f '%d,%i' "$recovery_root/versions/$generation_e")
printf '{"schema_version":1,"operation":"prune","current":"%s","previous":"%s","manifest_sha256":"%s","retention_sha256":"%s","retention_count":%s,"candidates":"%s,%s,%s;%s,%s,%s"}\n' \
	"$generation_a" "$generation_c" "$recovery_manifest_digest" "$recovery_retention_digest" "$recovery_retention_count" \
	"$generation_b" "${digest_b#sha256:}" "$recovery_b_identity" "$generation_e" "${digest_e#sha256:}" "$recovery_e_identity" >"$recovery_root/transaction.json"
if HOME="$recovery_home" "$bootstrap" status >/dev/null 2>&1; then fail 'status declared interrupted removal idle'; fi
if HOME="$recovery_home" "$bootstrap" prune --approve "$recovery_idle_id" >/dev/null 2>&1; then fail 'idle approval resumed a transaction'; fi
if HOME="$recovery_home" "$bootstrap" plan --uninstall --commit "$generation_b" >/dev/null 2>&1; then fail 'another operation planned through a prune transaction'; fi
mkdir "$recovery_root/removal"
mv "$recovery_root/versions/$generation_b" "$recovery_root/removal/$generation_b"
recovery_plan=$(HOME="$recovery_home" "$bootstrap" plan --prune) || fail 'interrupted removal recovery planning failed'
recovery_id=$(printf '%s\n' "$recovery_plan" | /usr/bin/awk -F': ' '$1 == "Plan ID" { print $2 }')
HOME="$recovery_home" "$bootstrap" prune --approve "$recovery_id" || fail 'approved interrupted removal recovery failed'
assert_not_file "$recovery_root/versions/$generation_b"
assert_not_file "$recovery_root/versions/$generation_e"
assert_not_file "$recovery_root/transaction.json"
HOME="$recovery_home" "$bootstrap" status >/dev/null || fail 'recovered removal left an invalid layout'

mkdir "$root/removal"
if HOME="$home" "$bootstrap" plan --uninstall --commit "$generation_b" >/dev/null 2>&1; then
	fail 'uninstall planned through unrecorded removal staging'
fi
if HOME="$home" "$bootstrap" status >/dev/null 2>&1; then
	fail 'status accepted unrecorded removal staging'
fi
rmdir "$root/removal"
prepublication_plan=$(HOME="$home" "$bootstrap" plan --uninstall --commit "$generation_b") || fail 'pre-publication removal plan failed'
prepublication_id=$(printf '%s\n' "$prepublication_plan" | /usr/bin/awk -F': ' '$1 == "Plan ID" { print $2 }')
printf 'incomplete removal transaction\n' >"$root/.transaction.ABCDEF"
if HOME="$home" "$bootstrap" plan --uninstall --commit "$generation_b" >/dev/null 2>&1; then fail 'uninstall planned through unrecorded transaction staging'; fi
if HOME="$home" "$bootstrap" status >/dev/null 2>&1; then fail 'status accepted unrecorded transaction staging'; fi
if HOME="$home" "$bootstrap" uninstall --commit "$generation_b" --approve "$prepublication_id" >/dev/null 2>&1; then fail 'approved uninstall ignored unrecorded transaction staging'; fi
[ -f "$root/.transaction.ABCDEF" ] || fail 'unrecorded transaction staging was deleted'
[ -d "$root/versions/$generation_b" ] || fail 'unrecorded transaction staging allowed generation deletion'
rm "$root/.transaction.ABCDEF"
uninstall_plan=$(HOME="$home" "$bootstrap" plan --uninstall --commit "$generation_b") || fail 'uninstall planning failed'
uninstall_id=$(printf '%s\n' "$uninstall_plan" | /usr/bin/awk -F': ' '$1 == "Plan ID" { print $2 }')
HOME="$home" "$bootstrap" uninstall --commit "$generation_b" --approve "$uninstall_id" || fail 'approved uninstall failed'
assert_not_file "$root/versions/$generation_b"
for protected_generation in "$generation_a" "$generation_c" "$generation_d"; do
	if HOME="$home" "$bootstrap" plan --uninstall --commit "$protected_generation" >/dev/null 2>&1; then
		fail "uninstall planned protected generation $protected_generation"
	fi
done
prune_plan=$(HOME="$home" "$bootstrap" plan --prune) || fail 'post-uninstall prune planning failed'
prune_id=$(printf '%s\n' "$prune_plan" | /usr/bin/awk -F': ' '$1 == "Plan ID" { print $2 }')
uncertain_uninstall_plan=$(HOME="$home" "$bootstrap" plan --uninstall --commit "$generation_e") || fail 'uncertainty fixture uninstall planning failed'
uncertain_uninstall_id=$(printf '%s\n' "$uncertain_uninstall_plan" | /usr/bin/awk -F': ' '$1 == "Plan ID" { print $2 }')
printf 'uncertain\n' >"$root/retention/v1/not-a-reference-hash"
if HOME="$home" "$bootstrap" prune --approve "$prune_id" >/dev/null 2>&1; then
	fail 'approved prune ignored an uncertain retention marker'
fi
if HOME="$home" "$bootstrap" uninstall --commit "$generation_e" --approve "$uncertain_uninstall_id" >/dev/null 2>&1; then
	fail 'approved uninstall ignored an uncertain retention marker'
fi
assert_file "$root/versions/$generation_e/bin/forgepilot"
assert_not_file "$root/transaction.json"
rm "$root/retention/v1/not-a-reference-hash"
mv "$root/retention/v1" "$root/retention/v1.saved"
if HOME="$home" "$bootstrap" prune --approve "$prune_id" >/dev/null 2>&1; then
	fail 'approved prune ignored a missing retention store'
fi
if HOME="$home" "$bootstrap" uninstall --commit "$generation_e" --approve "$uncertain_uninstall_id" >/dev/null 2>&1; then
	fail 'approved uninstall ignored a missing retention store'
fi
assert_file "$root/versions/$generation_e/bin/forgepilot"
assert_not_file "$root/transaction.json"
mv "$root/retention/v1.saved" "$root/retention/v1"
HOME="$home" "$bootstrap" prune --approve "$prune_id" || fail 'approved prune failed'
assert_not_file "$root/versions/$generation_e"
for protected_generation in "$generation_a" "$generation_c" "$generation_d"; do
	assert_file "$root/versions/$protected_generation/bin/forgepilot"
done
HOME="$home" "$bootstrap" status >/dev/null || fail 'approved removals left an invalid managed layout'
HOME="$home" "$bootstrap" retention-v1 release --generation "$generation_d" --payload-digest "$digest_d" --reference "$reference_c" >/dev/null || fail 'retained generation cleanup release failed'

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
