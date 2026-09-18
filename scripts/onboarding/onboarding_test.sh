#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
document=$repository_root/docs/release/onboarding.md
prompt=$repository_root/docs/release/onboarding-prompt.md
plan=$script_dir/source-built-plan.sh
fixture=$(mktemp -d "${TMPDIR:-/tmp}/forgepilot-onboarding-test.XXXXXX")
cleanup() { rm -rf "$fixture"; }
trap cleanup EXIT HUP INT TERM

fail() { printf 'onboarding_test: %s\n' "$*" >&2; exit 1; }
assert_file() { [ -f "$1" ] || fail "expected file: $1"; }
assert_contains() { grep -F -- "$2" "$1" >/dev/null || fail "expected $1 to contain: $2"; }
assert_not_contains() { ! grep -F -- "$2" "$1" >/dev/null || fail "expected $1 not to contain: $2"; }

assert_file "$document"
assert_file "$prompt"
assert_file "$plan"

# A local immutable source commit is the only identity accepted by the action
# disclosure. Generating the plan must not fetch, build, verify, or mutate it.
mkdir -p "$fixture/source/cmd/forgepilot" "$fixture/target"
printf 'package main\nfunc main() {}\n' >"$fixture/source/cmd/forgepilot/main.go"
git -C "$fixture/source" init -q
git -C "$fixture/source" config user.email test@example.com
git -C "$fixture/source" config user.name test
git -C "$fixture/source" add .
git -C "$fixture/source" commit -qm fixture
source_commit=$(git -C "$fixture/source" rev-parse HEAD)
source_head_before=$(git -C "$fixture/source" rev-parse HEAD)
printf 'verify:\n\ttrue\n' >"$fixture/target/Makefile"
git -C "$fixture/target" init -q
git -C "$fixture/target" config user.email test@example.com
git -C "$fixture/target" config user.name test
git -C "$fixture/target" add Makefile && git -C "$fixture/target" commit -qm target
candidate=$(git -C "$fixture/target" rev-parse HEAD)

mkdir -p "$fixture/target/specs/stories/ONBOARD-1"
printf '# Story\n' >"$fixture/target/specs/stories/ONBOARD-1/story.md"
printf 'Run make verify\n' >"$fixture/target/specs/stories/ONBOARD-1/acceptance.md"

"$plan" --source "$fixture/source" --commit "$source_commit" \
	--stage-root "$fixture/stage" --entrypoint "$fixture/bin/forgepilot" \
	--target "$fixture/target" --candidate-kind COMMIT --candidate "$candidate" --goal-id ONBOARD-1 --goal-title 'onboarding goal' --story 'specs/stories/ONBOARD-1' >"$fixture/plan.txt"
[ "$(git -C "$fixture/source" rev-parse HEAD)" = "$source_head_before" ] || fail 'plan changed source commit'
[ -z "$(git -C "$fixture/source" status --porcelain)" ] || fail 'plan changed source worktree'

# Formal onboarding is intentionally Apple Silicon-only. The platform guard is
# inspection-only and rejects an Intel host before it can disclose executable actions.
mkdir -p "$fixture/intel-bin"
printf '%s\n' '#!/bin/sh' 'case "$1" in' \
	'  -s) [ "${TEST_UNAME_FAIL:-}" != s ] || exit 1; printf "%s\\n" "${TEST_UNAME_S:-Darwin}" ;;' \
	'  -m) [ "${TEST_UNAME_FAIL:-}" != m ] || exit 1; printf "%s\\n" "${TEST_UNAME_M:-arm64}" ;;' \
	'  *) exit 1 ;;' \
	'esac' >"$fixture/intel-bin/uname"
chmod +x "$fixture/intel-bin/uname"
target_status_before=$(git -C "$fixture/target" status --porcelain)
source_status_before=$(git -C "$fixture/source" status --porcelain)
assert_platform_rejected() {
	label=$1 os=$2 arch=$3 failure=$4 expected=$5
	rejected_stage=$fixture/rejected-$label-stage
	rejected_entrypoint=$fixture/rejected-$label-bin/forgepilot
	if rejected_output=$(TEST_UNAME_S="$os" TEST_UNAME_M="$arch" TEST_UNAME_FAIL="$failure" PATH="$fixture/intel-bin:$PATH" "$plan" --source "$fixture/source" --commit "$source_commit" --stage-root "$rejected_stage" --entrypoint "$rejected_entrypoint" --target "$fixture/target" --candidate-kind COMMIT --candidate "$candidate" --goal-id ONBOARD-1 --goal-title onboarding --story specs/stories/ONBOARD-1 2>&1); then
		fail "plan accepted unsupported platform case: $label"
	fi
	case "$rejected_output" in *"$expected"*) ;; *) fail "platform case $label failed for the wrong reason" ;; esac
	case "$rejected_output" in *'FIRST EXPLICIT APPROVAL REQUIRED'*) fail "platform case $label disclosed executable actions" ;; esac
	[ ! -e "$rejected_stage" ] && [ ! -L "$rejected_stage" ] || fail "platform case $label changed staging"
	[ ! -e "$(dirname "$rejected_entrypoint")" ] && [ ! -L "$(dirname "$rejected_entrypoint")" ] || fail "platform case $label changed entrypoint paths"
	[ "$(git -C "$fixture/target" status --porcelain)" = "$target_status_before" ] || fail "platform case $label changed target"
	[ "$(git -C "$fixture/source" status --porcelain)" = "$source_status_before" ] || fail "platform case $label changed source"
}
assert_platform_rejected intel Darwin x86_64 '' 'formal onboarding supports only Apple Silicon macOS (Darwin arm64)'
assert_platform_rejected non-darwin Linux arm64 '' 'formal onboarding supports only Apple Silicon macOS (Darwin arm64)'
assert_platform_rejected unknown-arch Darwin ppc64 '' 'formal onboarding supports only Apple Silicon macOS (Darwin arm64)'
assert_platform_rejected uname-os-failure Darwin arm64 s 'cannot determine onboarding host operating system'
assert_platform_rejected uname-arch-failure Darwin arm64 m 'cannot determine onboarding host architecture'

for bad_commit in "${source_commit%?}" main latest branch 0123456789abcdef0123456789abcdef0123456g; do
	if invalid_output=$("$plan" --source "$fixture/source" --commit "$bad_commit" --stage-root "$fixture/stage" --entrypoint "$fixture/bin/forgepilot" --target "$fixture/target" --candidate-kind COMMIT --candidate "$candidate" --goal-id ONBOARD-1 --goal-title onboarding --story specs/stories/ONBOARD-1 2>&1); then
		fail "plan accepted invalid commit: ${bad_commit:-missing}"
	fi
	case "$invalid_output" in
		*'--commit must be a full lowercase 40-character SHA') ;;
		*) fail "plan rejected invalid commit for the wrong reason: ${bad_commit:-missing}" ;;
	esac
done

# The plan has two observable authorization boundaries. The first names all
# source-side writes; the second names only target-repository writes.
assert_contains "$fixture/plan.txt" 'INSPECTION ONLY — no fetch, build, verify, entrypoint, or repository write'
assert_contains "$fixture/plan.txt" "source commit: $source_commit"
assert_contains "$fixture/plan.txt" "target Candidate: COMMIT $candidate"
assert_contains "$fixture/plan.txt" 'FIRST EXPLICIT APPROVAL REQUIRED'
assert_contains "$fixture/plan.txt" 'git clone --no-checkout'
assert_contains "$fixture/plan.txt" 'go install ./cmd/forgepilot'
assert_contains "$fixture/plan.txt" 'make verify'
assert_contains "$fixture/plan.txt" 'atomic entrypoint switch'
assert_contains "$fixture/plan.txt" 'SECOND EXPLICIT APPROVAL REQUIRED'
assert_contains "$fixture/plan.txt" 'forgepilot init'
assert_contains "$fixture/plan.txt" 'forgepilot goal create'
assert_contains "$fixture/plan.txt" 'forgepilot work add'
assert_contains "$fixture/plan.txt" 'forgepilot status'
assert_contains "$fixture/plan.txt" 'creates ForgePilot state'
assert_contains "$fixture/plan.txt" 'creates the Goal'
assert_contains "$fixture/plan.txt" 'creates the Work Item'
assert_contains "$fixture/plan.txt" 'reads the resulting state'
assert_not_contains "$fixture/plan.txt" 'curl '
assert_not_contains "$fixture/plan.txt" 'brew '

# A COMMIT plan is tied to the current immutable HEAD. A later HEAD cannot
# inherit this preflight or its approval disclosure.
git -C "$fixture/target" commit --allow-empty -qm later
if "$plan" --source "$fixture/source" --commit "$source_commit" --stage-root "$fixture/stage" --entrypoint "$fixture/bin/forgepilot" --target "$fixture/target" --candidate-kind COMMIT --candidate "$candidate" --goal-id ONBOARD-1 --goal-title onboarding --story specs/stories/ONBOARD-1 >/dev/null 2>&1; then
	fail 'plan accepted a COMMIT Candidate that no longer matches HEAD'
fi

# A SNAPSHOT includes tracked files but excludes ignored, untracked files.
"$plan" --source "$fixture/source" --commit "$source_commit" --stage-root "$fixture/stage" --entrypoint "$fixture/bin/forgepilot" --target "$fixture/target" --candidate-kind SNAPSHOT --candidate current --goal-id ONBOARD-1 --goal-title onboarding --story specs/stories/ONBOARD-1 >/dev/null
mkdir -p "$fixture/ignored"
printf 'Makefile\n' >"$fixture/ignored/.gitignore"
printf 'verify:\n\ttrue\n' >"$fixture/ignored/Makefile"
git -C "$fixture/ignored" init -q && git -C "$fixture/ignored" config user.email test@example.com && git -C "$fixture/ignored" config user.name test && git -C "$fixture/ignored" add .gitignore && git -C "$fixture/ignored" commit -qm ignored
if "$plan" --source "$fixture/source" --commit "$source_commit" --stage-root "$fixture/stage" --entrypoint "$fixture/bin/forgepilot" --target "$fixture/ignored" --candidate-kind SNAPSHOT --candidate current --goal-id ONBOARD-1 --goal-title onboarding --story specs/stories/ONBOARD-1 >/dev/null 2>&1; then fail 'plan accepted ignored untracked snapshot Makefile'; fi

# The common procedure independently covers the target Candidate precheck,
# source build, two approvals, state reuse, Story review, and snapshot advice.
assert_contains "$document" 'inspection-only'
assert_contains "$document" 'Apple Silicon macOS (`Darwin arm64`) only'
assert_contains "$document" 'COMMIT Candidate'
assert_contains "$document" 'SNAPSHOT Candidate'
assert_contains "$document" 'Re-run the plan if HEAD changes'
assert_contains "$document" 'ignored, untracked Makefile'
assert_contains "$document" 'full 40-character commit SHA'
assert_contains "$document" 'FIRST EXPLICIT APPROVAL'
assert_contains "$document" 'SECOND EXPLICIT APPROVAL'
assert_contains "$document" 'Go is missing'
assert_contains "$document" 'do not install Go'
assert_contains "$document" 'incompatible version stops at `go-prerequisite`'
assert_contains "$document" 'exact reviewed ForgePilot'
assert_contains "$document" '`source_commit`; do not read it from ambient worktree content'
assert_contains "$document" 'Disable Git lazy fetch and replacement objects'
assert_contains "$document" 'atomic entrypoint switch'
assert_contains "$document" '`source_commit`, `failed_action`, `cause`'
assert_contains "$document" '`entrypoint_preserved`, `target_state_preserved`'
assert_contains "$document" '`temporary_entrypoint_present`'
assert_contains "$document" '`raw_command_output=omitted`'
assert_contains "$document" 'Omit raw stdout/stderr, environment values'
assert_contains "$document" 'the old entrypoint'
assert_contains "$document" 'remains authoritative'
assert_contains "$document" 'read and reuse that ForgePilot state'
assert_contains "$document" 'Do not run `forgepilot work add`'
assert_contains "$document" 'forgepilot verify <work-id> --snapshot'
assert_contains "$document" 'commit the intended change and keep the worktree clean'
assert_contains "$document" 'never commits on the developer’s behalf'
assert_not_contains "$document" 'Developer ID'
assert_not_contains "$document" 'notarization'
assert_not_contains "$document" 'quarantine'

assert_contains "$prompt" 'full 40-character commit SHA'
assert_contains "$prompt" 'Apple Silicon Mac'
assert_contains "$prompt" 'first explicit approval'
assert_contains "$prompt" 'second explicit approval'
assert_contains "$prompt" 'Do not install Go'
assert_contains "$prompt" 'missing or incompatible'
assert_contains "$prompt" 'sanitized action/exit summary'
assert_contains "$prompt" 'retain raw command output or environment values'

printf 'onboarding_test: PASS\n'
