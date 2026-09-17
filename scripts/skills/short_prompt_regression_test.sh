#!/bin/sh
# The short prompt remains the zero-skill entry point and names the shared
# source-built procedure without consulting either platform adapter.
set -eu

fail() { printf 'short_prompt_regression: %s\n' "$*" >&2; exit 1; }
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
prompt=$root/docs/release/onboarding-prompt.md
procedure=$root/docs/release/onboarding.md

[ -f "$prompt" ] || fail 'missing zero-skill prompt'
[ -f "$procedure" ] || fail 'missing shared procedure'
grep -F -- 'full 40-character commit SHA' "$prompt" >/dev/null || fail 'prompt omits immutable source identity'
grep -F -- 'first explicit approval' "$prompt" >/dev/null || fail 'prompt omits source-side approval'
grep -F -- 'second explicit approval' "$prompt" >/dev/null || fail 'prompt omits repository-write approval'
grep -F -- 'Do not install Go' "$prompt" >/dev/null || fail 'prompt permits Go installation'
grep -F -- 'source-built onboarding procedure' "$procedure" >/dev/null || fail 'shared procedure is not source-built'
! grep -Fi -- 'notarization' "$prompt" >/dev/null || fail 'prompt mentions retired notarization path'

printf 'short_prompt_regression: PASS\n'
