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

home=$fixture/home
root=$home/.local/share/forgepilot
generation_a=0123456789abcdef0123456789abcdef01234567
generation_b=1123456789abcdef0123456789abcdef01234567
digest_a=sha256:89b7b72858f35c9e13407e80ac040bde1113c634296a55cfa9b0a2e10585ecc6
digest_b=sha256:72b0e3a23acf97c2a0b58ecad206a851d3ca932fa475f21d884bd69a0eac6617
reference_a=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
reference_b=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
mkdir -p "$root/versions/$generation_a/bin" \
	"$root/versions/$generation_a/libexec" \
	"$root/versions/$generation_a/skills/codex/forgepilot-onboarding" \
	"$root/versions/$generation_a/docs/release" \
	"$root/versions/$generation_b/bin" \
	"$root/versions/$generation_b/libexec" \
	"$root/versions/$generation_b/skills/codex/forgepilot-onboarding" \
	"$root/versions/$generation_b/docs/release" \
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
chmod 700 "$root/versions/$generation_a/bin/forgepilot" "$root/versions/$generation_a/libexec/forgepilot-bootstrap" \
	"$root/versions/$generation_b/bin/forgepilot" "$root/versions/$generation_b/libexec/forgepilot-bootstrap"
ln -s "versions/$generation_a" "$root/current"
ln -s "$root/current/bin/forgepilot" "$home/.local/bin/forgepilot"
ln -s "$root/current/libexec/forgepilot-bootstrap" "$home/.local/bin/forgepilot-bootstrap"
ln -s "$root/current/skills/codex/forgepilot-onboarding" "$home/.agents/skills/forgepilot-onboarding"
printf '{"schema_version":1,"generations":{"%s":{"payload_digest":"%s","path":"versions/%s"},"%s":{"payload_digest":"%s","path":"versions/%s"}},"current":"%s","previous":null}\n' \
	"$generation_a" "$digest_a" "$generation_a" "$generation_b" "$digest_b" "$generation_b" "$generation_a" >"$root/manifest.json"

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

output=$(HOME="$home" "$bootstrap" retention-v1 acquire --generation "$generation_a" --payload-digest "$digest_a" --reference "$reference_a") || fail 'idempotent acquire failed'
assert_output "$output" '"result":"already_acquired"'

output=$(HOME="$home" "$bootstrap" status) || fail 'valid status failed'
assert_output "$output" 'Bootstrap status: idle'
assert_output "$output" 'generations=2'
assert_output "$output" 'retention_refs=1'
case "$output" in *"$reference_a"*) fail 'status exposed the raw retention reference' ;; esac

# Prune planning binds the exact inactive generation and is inspection-only.
plan_paths_before=$(/usr/bin/find -s "$root" -print | /usr/bin/shasum -a 256 | /usr/bin/awk '{print $1}')
plan_contents_before=$(/usr/bin/find -s "$root" -type f -exec /usr/bin/shasum -a 256 '{}' \; | /usr/bin/sort | /usr/bin/shasum -a 256 | /usr/bin/awk '{print $1}')
plan_current_before=$(/usr/bin/readlink "$root/current")
plan=$(HOME="$home" "$bootstrap" plan --prune) || fail 'prune planning failed'
assert_output "$plan" 'Operation: prune'
assert_output "$plan" 'Plan ID: sha256:'
assert_output "$plan" "Action: remove versions/$generation_b"
case "$plan" in *"Action: remove versions/$generation_a"*) fail 'prune plan selected the current generation' ;; esac
plan_id=$(printf '%s\n' "$plan" | /usr/bin/awk -F': ' '$1 == "Plan ID" { print $2 }')
case "$plan_id" in sha256:*) ;; *) fail 'prune plan ID is not a SHA-256 digest' ;; esac
[ "${#plan_id}" -eq 71 ] || fail 'prune plan ID is not a complete SHA-256 digest'
repeat_plan=$(HOME="$home" "$bootstrap" plan --prune) || fail 'repeat prune planning failed'
repeat_plan_id=$(printf '%s\n' "$repeat_plan" | /usr/bin/awk -F': ' '$1 == "Plan ID" { print $2 }')
[ "$repeat_plan_id" = "$plan_id" ] || fail 'unchanged prune facts produced a different plan ID'
plan_paths_after=$(/usr/bin/find -s "$root" -print | /usr/bin/shasum -a 256 | /usr/bin/awk '{print $1}')
plan_contents_after=$(/usr/bin/find -s "$root" -type f -exec /usr/bin/shasum -a 256 '{}' \; | /usr/bin/sort | /usr/bin/shasum -a 256 | /usr/bin/awk '{print $1}')
plan_current_after=$(/usr/bin/readlink "$root/current")
[ "$plan_paths_after" = "$plan_paths_before" ] || fail 'prune planning changed the managed path set'
[ "$plan_contents_after" = "$plan_contents_before" ] || fail 'prune planning changed managed file content'
[ "$plan_current_after" = "$plan_current_before" ] || fail 'prune planning changed the current pointer'
assert_not_file "$root/transaction.json"

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

/bin/rmdir "$root/retention/v1"
if HOME="$home" "$bootstrap" status >/dev/null 2>&1; then fail 'status accepted a missing retention store'; fi

# lockf must never recreate a missing lock marker.
mv "$root/lock" "$root/lock.saved"
if HOME="$home" "$bootstrap" retention-v1 acquire --generation "$generation_a" --payload-digest "$digest_a" --reference "$reference_a" >/dev/null 2>&1; then
	fail 'retention operation accepted a missing lock marker'
fi
assert_not_file "$root/lock"
mv "$root/lock.saved" "$root/lock"

printf '%s\n' 'forgepilot-bootstrap_test: retention protocol PASS'
