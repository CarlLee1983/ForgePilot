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

"$plan" --source "$fixture/source" --commit "$source_commit" \
	--stage-root "$fixture/stage" --entrypoint "$fixture/bin/forgepilot" \
	--target "$fixture/target" --candidate-kind COMMIT --candidate "$candidate" --goal-id ONBOARD-1 --goal-title 'onboarding goal' --story 'specs/stories/ONBOARD-1' >"$fixture/plan.txt"
[ "$(git -C "$fixture/source" rev-parse HEAD)" = "$source_head_before" ] || fail 'plan changed source commit'
[ -z "$(git -C "$fixture/source" status --porcelain)" ] || fail 'plan changed source worktree'

for bad_commit in '' "${source_commit%?}" main latest branch 0123456789abcdef0123456789abcdef0123456g; do
	if "$plan" --source "$fixture/source" --commit "$bad_commit" --stage-root "$fixture/stage" --entrypoint "$fixture/bin/forgepilot" --target "$fixture/target" >/dev/null 2>&1; then
		fail "plan accepted invalid commit: ${bad_commit:-missing}"
	fi
done

# The plan has two observable authorization boundaries. The first names all
# source-side writes; the second names only target-repository writes.
assert_contains "$fixture/plan.txt" 'INSPECTION ONLY — no fetch, build, verify, entrypoint, or repository write'
assert_contains "$fixture/plan.txt" "source commit: $source_commit"
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
assert_contains "$document" 'COMMIT Candidate'
assert_contains "$document" 'SNAPSHOT Candidate'
assert_contains "$document" 'ignored, untracked Makefile'
assert_contains "$document" 'full 40-character commit SHA'
assert_contains "$document" 'FIRST EXPLICIT APPROVAL'
assert_contains "$document" 'SECOND EXPLICIT APPROVAL'
assert_contains "$document" 'Go is missing'
assert_contains "$document" 'do not install Go'
assert_contains "$document" 'atomic entrypoint switch'
assert_contains "$document" 'read and reuse that ForgePilot state'
assert_contains "$document" 'Do not run `forgepilot work add`'
assert_contains "$document" 'forgepilot verify <work-id> --snapshot'
assert_contains "$document" 'commit the intended change and keep the worktree clean'
assert_contains "$document" 'never commits on the developer’s behalf'
assert_not_contains "$document" 'Developer ID'
assert_not_contains "$document" 'notarization'
assert_not_contains "$document" 'quarantine'

assert_contains "$prompt" 'full 40-character commit SHA'
assert_contains "$prompt" 'first explicit approval'
assert_contains "$prompt" 'second explicit approval'
assert_contains "$prompt" 'Do not install Go'

printf 'onboarding_test: PASS\n'
