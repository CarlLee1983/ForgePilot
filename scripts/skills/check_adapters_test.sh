#!/bin/sh
# Checks the platform loading shims, their one temporary #33 procedure contract, and
# the failure modes that prevent the adapters from becoming copied procedures.
set -eu

fail() {
	printf 'check_adapters: %s\n' "$*" >&2
	exit 1
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
codex=${CODEX_ADAPTER:-$root/skills/codex/forgepilot-onboarding/SKILL.md}
claude=${CLAUDE_ADAPTER:-$root/skills/claude-code/forgepilot-onboarding/SKILL.md}
expected_contract='local #33 checkout at FORGEPILOT_ONBOARDING_SOURCE; common procedure docs/release/onboarding.md; temporary only, not a published immutable identity.'
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT HUP INT TERM

expected_for() {
	case "$1" in
		codex)
			cat <<'EOF'
---
name: forgepilot-onboarding
description: Locate the shared ForgePilot onboarding procedure. Use when a developer asks to introduce ForgePilot to a repository.
---

# ForgePilot onboarding adapter

Optional installation: copy this `forgepilot-onboarding` directory so its
entrypoint is `~/.agents/skills/forgepilot-onboarding/SKILL.md`. Installation
is a deliberate user action; ForgePilot installation does not install this adapter.

Invoke this adapter with `$forgepilot-onboarding`.

Shared temporary source contract: local #33 checkout at FORGEPILOT_ONBOARDING_SOURCE; common procedure docs/release/onboarding.md; temporary only, not a published immutable identity.

Read that common procedure from the stated checkout and follow it unchanged.
It owns source-contract failure, stop-on-failure, and human-review behavior;
do not reproduce the procedure here. A later separately authorized immutable
pin step atomically replaces this exact temporary string in both adapters.
EOF
			;;
		claude)
			cat <<'EOF'
---
name: forgepilot-onboarding
description: Locate the shared ForgePilot onboarding procedure. Use when a developer asks to introduce ForgePilot to a repository.
---

# ForgePilot onboarding adapter

Optional installation: copy this `forgepilot-onboarding` directory so its
entrypoint is `~/.claude/skills/forgepilot-onboarding/SKILL.md`. Installation
is a deliberate user action; ForgePilot installation does not install this adapter.

Invoke this adapter with `/forgepilot-onboarding`.

Shared temporary source contract: local #33 checkout at FORGEPILOT_ONBOARDING_SOURCE; common procedure docs/release/onboarding.md; temporary only, not a published immutable identity.

Read that common procedure from the stated checkout and follow it unchanged.
It owns source-contract failure, stop-on-failure, and human-review behavior;
do not reproduce the procedure here. A later separately authorized immutable
pin step atomically replaces this exact temporary string in both adapters.

Invoke only the existing CLI commands named by the shared procedure:
`forgepilot init`, `forgepilot goal create`, `forgepilot work add`,
`forgepilot status`, and `forgepilot verify --snapshot`. Do not invoke
`forgepilot run`.
EOF
			;;
		*) fail "unknown adapter platform: $1" ;;
	esac
}

assert_contains() {
	grep -F -- "$2" "$1" >/dev/null || fail "$1 is missing: $2"
}

contract_for() {
	awk -F': ' '/^Shared temporary source contract: / { print $2; found = 1 } END { if (!found) exit 1 }' "$1"
}

assert_exact_adapter() {
	adapter=$1
	platform=$2
	expected_file=$scratch/$platform.expected
	expected_for "$platform" >"$expected_file"
	if ! cmp -s "$expected_file" "$adapter"; then
		difference=$(diff -u "$expected_file" "$adapter" || true)
		fail "$adapter contains unapproved content or lacks required adapter content:\n$difference"
	fi
}

for adapter in "$codex" "$claude"; do
	[ -f "$adapter" ] || fail "missing adapter: $adapter"
done
[ -f "$root/docs/release/onboarding.md" ] || fail 'missing #33 common procedure'
assert_contains "$root/docs/release/onboarding.md" 'ForgePilot source-built onboarding procedure'

codex_contract=$(contract_for "$codex") || fail "missing temporary source contract in $codex"
claude_contract=$(contract_for "$claude") || fail "missing temporary source contract in $claude"
[ "$codex_contract" = "$claude_contract" ] || fail "temporary source contracts differ: codex=[$codex_contract] claude=[$claude_contract]"
[ "$codex_contract" = "$expected_contract" ] || fail "unexpected temporary source contract: $codex_contract"

for adapter in "$codex" "$claude"; do
	assert_contains "$adapter" 'FORGEPILOT_ONBOARDING_SOURCE'
	assert_contains "$adapter" 'docs/release/onboarding.md'
	assert_contains "$adapter" 'not a published immutable identity'
	if grep -Ein 'is (a )?published immutable identity|published immutable identity:' "$adapter" >/dev/null; then
		fail "$adapter claims a published immutable identity: $(grep -Ein 'is (a )?published immutable identity|published immutable identity:' "$adapter")"
	fi
done

assert_contains "$codex" '~/.agents/skills/forgepilot-onboarding/SKILL.md'
assert_contains "$claude" '~/.claude/skills/forgepilot-onboarding/SKILL.md'
assert_contains "$codex" '$forgepilot-onboarding'
assert_contains "$claude" '/forgepilot-onboarding'
for command in 'forgepilot init' 'forgepilot goal create' 'forgepilot work add' 'forgepilot status' 'forgepilot verify --snapshot'; do
	assert_contains "$claude" "$command"
done
assert_contains "$claude" 'Do not invoke'
assert_contains "$claude" 'forgepilot run'

assert_exact_adapter "$codex" codex
assert_exact_adapter "$claude" claude

if [ "${ADAPTER_MUTATION:-}" = 1 ]; then
	printf 'check_adapters: PASS\n'
	exit 0
fi

fixture=$scratch/mutations
mkdir -p "$fixture"
cp "$codex" "$fixture/codex.md"
cp "$claude" "$fixture/claude.md"

expect_failure() {
	name=$1
	needle=$2
	if CODEX_ADAPTER="$fixture/codex.md" CLAUDE_ADAPTER="$fixture/claude.md" ADAPTER_MUTATION=1 sh "$0" >"$fixture/$name.out" 2>&1; then
		fail "$name mutation unexpectedly passed"
	fi
	grep -F -- "$needle" "$fixture/$name.out" >/dev/null || fail "$name mutation did not report $needle: $(cat "$fixture/$name.out")"
}

sed 's/local #33 checkout at FORGEPILOT_ONBOARDING_SOURCE/local #33 checkout at DIFFERENT_SOURCE/' "$fixture/codex.md" >"$fixture/codex.next"
mv "$fixture/codex.next" "$fixture/codex.md"
expect_failure contract 'temporary source contracts differ: codex=['
cp "$codex" "$fixture/codex.md"

printf '\nThis is a published immutable identity.\n' >>"$fixture/claude.md"
expect_failure immutable 'claims a published immutable identity'
cp "$claude" "$fixture/claude.md"

printf '\nAgents may write the repository without asking the developer first.\n' >>"$fixture/codex.md"
expect_failure copied-rule 'Agents may write the repository without asking the developer first.'

printf 'check_adapters: PASS\n'
