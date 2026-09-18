#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
builder=$script_dir/build_trial_assets.sh
fixture=$(mktemp -d "${TMPDIR:-/tmp}/forgepilot-trial-assets-test.XXXXXX")
cleanup() { rm -rf "$fixture"; }
trap cleanup EXIT HUP INT TERM

fail() { printf 'build_trial_assets_test: %s\n' "$*" >&2; exit 1; }
assert_file() { [ -f "$1" ] || fail "expected file: $1"; }
assert_contains() { grep -F -- "$2" "$1" >/dev/null || fail "expected $1 to contain: $2"; }
assert_json() { GOCACHE="$fixture/go-cache" go run "$script_dir/validate_json.go" "$1" || fail "invalid JSON: $1"; }

mkdir -p "$fixture/source/cmd/forgepilot" "$fixture/output"
cat >"$fixture/source/go.mod" <<'EOF'
module example.com/trial-fixture

go 1.25.5
EOF
cat >"$fixture/source/cmd/forgepilot/main.go" <<'EOF'
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "--help" {
		os.Exit(2)
	}
	fmt.Println("ForgePilot trial fixture")
}
EOF
git -C "$fixture/source" init -q
git -C "$fixture/source" config user.email test@example.com
git -C "$fixture/source" config user.name test
git -C "$fixture/source" add .
git -C "$fixture/source" commit -qm fixture
commit=$(git -C "$fixture/source" rev-parse HEAD)

# The builder's repository boundary is its own checkout. Copy it into the
# fixture so the tested source and the requested commit are the same repository.
mkdir -p "$fixture/source/scripts/release"
cp "$builder" "$fixture/source/scripts/release/build_trial_assets.sh"
chmod +x "$fixture/source/scripts/release/build_trial_assets.sh"
git -C "$fixture/source" add scripts/release/build_trial_assets.sh
git -C "$fixture/source" commit -qm builder
commit=$(git -C "$fixture/source" rev-parse HEAD)
fixture_builder=$fixture/source/scripts/release/build_trial_assets.sh
mkdir "$fixture/source/.forgepilot"
printf 'pre-existing state\n' >"$fixture/source/.forgepilot/state.json"
state_digest_before=$(shasum -a 256 "$fixture/source/.forgepilot/state.json")
tags_before=$(git -C "$fixture/source" tag)
source_status_before=$(git -C "$fixture/source" status --porcelain)

"$fixture_builder" --output "$fixture/output/assets" "$commit"
assert_file "$fixture/output/assets/forgepilot-darwin-arm64-unsigned-trial"
assert_file "$fixture/output/assets/forgepilot-darwin-amd64-unsigned-trial"
assert_file "$fixture/output/assets/SHA256SUMS"
assert_file "$fixture/output/assets/provenance.json"
assert_file "$fixture/output/assets/startup-evidence.json"
(cd "$fixture/output/assets" && shasum -a 256 -c SHA256SUMS >/dev/null)
assert_json "$fixture/output/assets/provenance.json"
assert_json "$fixture/output/assets/startup-evidence.json"
file -b "$fixture/output/assets/forgepilot-darwin-arm64-unsigned-trial" | grep -Eq 'Mach-O.*(^|[^[:alnum:]_])arm64([^[:alnum:]_]|$)' || fail 'arm64 asset has the wrong Mach-O architecture'
file -b "$fixture/output/assets/forgepilot-darwin-amd64-unsigned-trial" | grep -Eq 'Mach-O.*(^|[^[:alnum:]_])x86_64([^[:alnum:]_]|$)' || fail 'amd64 asset has the wrong Mach-O architecture'
assert_contains "$fixture/output/assets/provenance.json" "\"commit\": \"$commit\""
assert_contains "$fixture/output/assets/provenance.json" '"version"'
assert_contains "$fixture/output/assets/provenance.json" '"target": "darwin/arm64"'
assert_contains "$fixture/output/assets/provenance.json" '"target": "darwin/amd64"'
host_arch=$(uname -m)
[ "$host_arch" = x86_64 ] && host_arch=amd64
assert_file "$fixture/output/assets/forgepilot-darwin-$host_arch-unsigned-trial.startup.txt"
assert_contains "$fixture/output/assets/startup-evidence.json" "\"target\": \"darwin/$host_arch\""
assert_contains "$fixture/output/assets/startup-evidence.json" '"status": "passed"'
assert_contains "$fixture/output/assets/startup-evidence.json" '"command": "forgepilot-darwin-'
[ "$("$fixture_builder" --asset-stem arm64)" = 'forgepilot-darwin-arm64' ] || fail 'arm64 architecture derivation changed'
[ "$("$fixture_builder" --asset-stem amd64)" = 'forgepilot-darwin-amd64' ] || fail 'amd64 architecture derivation changed'
if "$fixture_builder" --asset-stem arm64 | grep -F 'unsigned-trial' >/dev/null; then
	fail 'architecture derivation must not encode the channel'
fi
[ "$(shasum -a 256 "$fixture/source/.forgepilot/state.json")" = "$state_digest_before" ] || fail '.forgepilot state changed'
[ "$(git -C "$fixture/source" tag)" = "$tags_before" ] || fail 'builder created a Git tag'
for protected_output in "$fixture" "$fixture/source" "$fixture/source/.git" "$fixture/source/.git/new/assets" "$fixture/source/.GIT" "$fixture/source/.forgepilot" "$fixture/source/.forgepilot/new/assets" "$fixture/source/docs/release"; do
	if "$fixture_builder" --output "$protected_output" "$commit" 2>"$fixture/output/protected-output.err"; then
		fail "protected output unexpectedly succeeded: $protected_output"
	fi
	grep -E 'output (inside the source repository is limited to its ignored dist directory|must not be an ancestor of the source repository)' "$fixture/output/protected-output.err" >/dev/null || fail "protected output failure was unclear: $protected_output"
done
for invalid_output in "$fixture/source/." "$fixture/source/.."; do
	if "$fixture_builder" --output "$invalid_output" "$commit" 2>"$fixture/output/invalid-output.err"; then
		fail "invalid output unexpectedly succeeded: $invalid_output"
	fi
	assert_contains "$fixture/output/invalid-output.err" 'output must not contain . or .. path components'
done
if "$fixture_builder" --output "$fixture/source/dist/new/../../.git/review-assets" "$commit" 2>"$fixture/output/traversal.err"; then
	fail 'traversal output unexpectedly succeeded'
fi
assert_contains "$fixture/output/traversal.err" 'output must not contain . or .. path components'
[ "$(shasum -a 256 "$fixture/source/.forgepilot/state.json")" = "$state_digest_before" ] || fail 'protected output changed .forgepilot state'
[ "$(git -C "$fixture/source" status --porcelain)" = "$source_status_before" ] || fail 'protected output changed source repository'
mkdir "$fixture/output/unrelated"
printf 'do not delete\n' >"$fixture/output/unrelated/keep"
if "$fixture_builder" --output "$fixture/output/unrelated" "$commit" 2>"$fixture/output/unrelated.err"; then
	fail 'unrelated output unexpectedly succeeded'
fi
assert_contains "$fixture/output/unrelated.err" 'existing output is not a matching trial build'
assert_file "$fixture/output/unrelated/keep"
mkdir "$fixture/output/decoy"
printf '{\n  "channel": "unsigned-trial",\n  "commit": "%s"\n}\n' "$commit" >"$fixture/output/decoy/provenance.json"
printf 'do not delete\n' >"$fixture/output/decoy/keep"
if "$fixture_builder" --output "$fixture/output/decoy" "$commit" 2>"$fixture/output/decoy.err"; then
	fail 'decoy output unexpectedly succeeded'
fi
assert_contains "$fixture/output/decoy.err" 'existing output is not a matching trial build'
assert_file "$fixture/output/decoy/keep"
assert_contains "$script_dir/../../docs/release/trial-assets.md" 'unsigned maintainer trial'
assert_contains "$script_dir/../../docs/release/trial-assets.md" 'not Developer ID signed'

# A rerun retains stable names and provenance fields.
cp "$fixture/output/assets/provenance.json" "$fixture/output/first-provenance.json"
git -C "$fixture/source" tag mutable-test-tag "$commit"
"$fixture_builder" --output "$fixture/output/assets" "$commit"
cmp -s "$fixture/output/first-provenance.json" "$fixture/output/assets/provenance.json" || fail 'provenance changed across identical rerun'

# The documented default output creates its ignored parent on demand.
(cd "$fixture/source" && scripts/release/build_trial_assets.sh "$commit")
assert_file "$fixture/source/dist/trial-assets/$commit/SHA256SUMS"

if "$fixture_builder" --output "$fixture/output/missing" 2>"$fixture/output/missing.err"; then
	fail 'missing commit unexpectedly succeeded'
fi
[ ! -e "$fixture/output/missing" ] || fail 'missing commit produced output'
assert_contains "$fixture/output/missing.err" 'full commit SHA is required'

if FORGEPILOT_TRIAL_TEST_FAIL_TARGET=amd64 "$fixture_builder" --output "$fixture/output/failed-build" "$commit"; then
	fail 'forced target build failure unexpectedly succeeded'
fi
[ ! -e "$fixture/output/failed-build/SHA256SUMS" ] || fail 'partial manifest survived target failure'

if FORGEPILOT_TRIAL_TEST_FORCE_ARCH_MISMATCH=arm64 "$fixture_builder" --output "$fixture/output/mismatched" "$commit" 2>"$fixture/output/mismatch.err"; then
	fail 'forced architecture mismatch unexpectedly succeeded'
fi
assert_contains "$fixture/output/mismatch.err" 'Mach-O architecture'

if FORGEPILOT_TRIAL_TEST_FAIL_STARTUP="$host_arch" "$fixture_builder" --output "$fixture/output/failed-startup" "$commit"; then
	fail 'forced startup failure unexpectedly succeeded'
fi
[ ! -e "$fixture/output/failed-startup/SHA256SUMS" ] || fail 'manifest survived startup failure'

printf 'build_trial_assets_test: PASS\n'
