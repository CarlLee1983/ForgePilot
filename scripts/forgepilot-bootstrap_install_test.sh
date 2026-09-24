#!/bin/sh
set -eu
umask 077

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
bootstrap=$script_dir/forgepilot-bootstrap
fixture=$(mktemp -d "${TMPDIR:-/tmp}/forgepilot-bootstrap-install-test.XXXXXX")
fixture=$(CDPATH='' cd -P -- "$fixture" && pwd)
trap 'rm -rf "$fixture"' EXIT HUP INT TERM
fail() { printf 'forgepilot-bootstrap_install_test: %s\n' "$*" >&2; exit 1; }
assert_file() { [ -f "$1" ] || fail "expected file: $1"; }
assert_not_path() { [ ! -e "$1" ] && [ ! -L "$1" ] || fail "unexpected path: $1"; }
plan_id() { printf '%s\n' "$1" | /usr/bin/awk -F': ' '$1 == "Plan ID" { print $2 }'; }

source=$fixture/source
home=$fixture/home
root=$home/.local/share/forgepilot
/bin/mkdir -p "$source/cmd/forgepilot" "$source/scripts" "$source/skills/codex/forgepilot-onboarding" "$source/docs/release" "$home"
printf 'module github.com/CarlLee1983/ForgePilot\n\ngo 1.25.5\n' >"$source/go.mod"
printf 'package main\nimport "fmt"\nfunc main() { fmt.Println("fixture-generation-a") }\n' >"$source/cmd/forgepilot/main.go"
printf 'verify:\n\tgo test ./...\n\t/usr/bin/which go > %s\n' "$fixture/observed-verify-go" >"$source/Makefile"
/bin/cp "$bootstrap" "$source/scripts/forgepilot-bootstrap"
/bin/chmod 700 "$source/scripts/forgepilot-bootstrap"
printf '# Fixture skill A\n' >"$source/skills/codex/forgepilot-onboarding/SKILL.md"
printf '# Fixture procedure A\n' >"$source/docs/release/onboarding.md"
/usr/bin/git -C "$source" init -q
/usr/bin/git -C "$source" -c user.name=Fixture -c user.email=fixture@example.invalid add .
/usr/bin/git -C "$source" -c user.name=Fixture -c user.email=fixture@example.invalid commit -qm 'fixture A'
generation_a=$(/usr/bin/git -C "$source" rev-parse HEAD)
toolbin=$fixture/toolbin
/bin/mkdir "$toolbin"
printf '#!/bin/sh\nprintf executed >>"%s"\nprintf "go version go1.24.0 darwin/arm64\\n"\n' "$fixture/unapproved-adjacent-go-executed" >"$toolbin/go"
/bin/chmod 700 "$toolbin/go"
adjacent_plan=$(PATH="$toolbin:$PATH" HOME="$home" TMPDIR="$home" "$bootstrap" plan --agent codex --source "$source" --commit "$generation_a") || fail 'static adjacent-toolchain plan failed'
assert_not_path "$fixture/unapproved-adjacent-go-executed"
assert_not_path "$root"
[ -z "$(/usr/bin/find "$home" -mindepth 1 -print)" ] || fail 'plan wrote into HOME through TMPDIR'
adjacent_id=$(plan_id "$adjacent_plan")
if PATH="$toolbin:$PATH" HOME="$home" "$bootstrap" install --agent codex --source "$source" --commit "$generation_a" --approve "$adjacent_id" >/dev/null 2>&1; then fail 'incompatible approved toolchain was accepted'; fi
assert_file "$fixture/unapproved-adjacent-go-executed"
assert_not_path "$root"
printf '#!/bin/sh\nprintf changed >>"%s"\nprintf "go version go1.25.5 darwin/arm64\\n"\n' "$fixture/unapproved-adjacent-go-executed" >"$toolbin/go"
if PATH="$toolbin:$PATH" HOME="$home" "$bootstrap" install --agent codex --source "$source" --commit "$generation_a" --approve "$adjacent_id" >/dev/null 2>&1; then fail 'changed toolchain bytes reused old approval'; fi
[ "$(/bin/cat "$fixture/unapproved-adjacent-go-executed")" = executed ] || fail 'stale toolchain approval executed changed bytes'
assert_not_path "$root"
printf '#!/bin/sh\nprintf executed >"%s"\n' "$fixture/unapproved-go-executed" >"$source/go"
/bin/chmod 700 "$source/go"
if PATH="$source:$PATH" HOME="$home" "$bootstrap" plan --agent codex --source "$source" --commit "$generation_a" >/dev/null 2>&1; then fail 'source-controlled Go was accepted before approval'; fi
assert_not_path "$fixture/unapproved-go-executed"
/bin/rm "$source/go"

plan=$(HOME="$home" "$bootstrap" plan --agent codex --source "$source" --commit "$generation_a") || fail 'initial plan failed'
approval=$(plan_id "$plan")
case "$approval" in sha256:*) ;; *) fail 'initial plan lacks an approval ID' ;; esac
assert_not_path "$root"
if HOME="$home" "$bootstrap" install --agent codex --source "$source" --commit "$generation_a" --approve "sha256:$(printf '%064d' 0)" >/dev/null 2>&1; then fail 'stale initial approval was accepted'; fi
assert_not_path "$root"
HOME="$home" "$bootstrap" install --agent codex --source "$source" --commit "$generation_a" --approve "$approval" || fail 'approved initial install failed'
case "$(/bin/cat "$fixture/observed-verify-go")" in "$home/.local/share/.forgepilot-stage."*/toolchain/go) ;; *) fail 'make verify did not use the staged approved Go executable' ;; esac
assert_file "$root/versions/$generation_a/bin/forgepilot"
assert_file "$root/versions/$generation_a/libexec/forgepilot-bootstrap"
assert_file "$root/versions/$generation_a/skills/codex/forgepilot-onboarding/SKILL.md"
[ "$(/usr/bin/readlink "$root/current")" = "versions/$generation_a" ] || fail 'initial current pointer is wrong'
HOME="$home" "$bootstrap" status >/dev/null || fail 'initial install left an invalid layout'
printf 'unowned sentinel\n' >"$root/.transaction.new"
if HOME="$home" "$bootstrap" plan --agent codex --source "$source" --commit "$generation_a" >/dev/null 2>&1; then fail 'unowned transaction staging was accepted'; fi
[ "$(/bin/cat "$root/.transaction.new")" = 'unowned sentinel' ] || fail 'unowned staging was changed'
/bin/rm "$root/.transaction.new"

printf 'package main\nimport "fmt"\nfunc main() { fmt.Println("fixture-generation-b") }\n' >"$source/cmd/forgepilot/main.go"
printf '# Fixture skill B\n' >"$source/skills/codex/forgepilot-onboarding/SKILL.md"
printf '# Fixture procedure B\n' >"$source/docs/release/onboarding.md"
/usr/bin/git -C "$source" -c user.name=Fixture -c user.email=fixture@example.invalid add .
/usr/bin/git -C "$source" -c user.name=Fixture -c user.email=fixture@example.invalid commit -qm 'fixture B'
generation_b=$(/usr/bin/git -C "$source" rev-parse HEAD)
printf '# Uncommitted working-tree drift\n' >"$source/skills/codex/forgepilot-onboarding/SKILL.md"
plan=$(HOME="$home" "$bootstrap" plan --upgrade --agent codex --source "$source" --commit "$generation_b") || fail 'upgrade plan failed'
approval=$(plan_id "$plan")
HOME="$home" "$bootstrap" install --upgrade --agent codex --source "$source" --commit "$generation_b" --approve "$approval" || fail 'approved upgrade failed'
[ "$(/usr/bin/readlink "$root/current")" = "versions/$generation_b" ] || fail 'upgrade current pointer is wrong'
[ "$(/usr/bin/readlink "$root/previous")" = "versions/$generation_a" ] || fail 'upgrade previous pointer is wrong'
[ "$(/bin/cat "$root/versions/$generation_b/skills/codex/forgepilot-onboarding/SKILL.md")" = '# Fixture skill B' ] || fail 'upgrade used the source working tree instead of the exact commit'
[ "$(/usr/bin/readlink "$home/.local/bin/forgepilot")" = "$root/current/bin/forgepilot" ] || fail 'stable CLI link drifted'
[ "$(/usr/bin/readlink "$home/.local/bin/forgepilot-bootstrap")" = "$root/current/libexec/forgepilot-bootstrap" ] || fail 'stable helper link drifted'
[ "$(/usr/bin/readlink "$home/.agents/skills/forgepilot-onboarding")" = "$root/current/skills/codex/forgepilot-onboarding" ] || fail 'stable skill link drifted'
HOME="$home" "$bootstrap" status >/dev/null || fail 'upgrade left an invalid layout'
physical_skill=$(CDPATH='' cd -P -- "$home/.agents/skills/forgepilot-onboarding" && pwd -P)
[ "$physical_skill" = "$root/versions/$generation_b/skills/codex/forgepilot-onboarding" ] || fail 'managed skill did not resolve to the current physical generation'
generation_record=$(HOME="$home" "$root/versions/$generation_b/libexec/forgepilot-bootstrap" generation-v1 current) || fail 'generation discovery failed'
case "$generation_record" in
  *"\"generation_id\":\"$generation_b\""*"\"forgepilot_path\":\"$root/versions/$generation_b/bin/forgepilot\""*"\"helper_path\":\"$root/versions/$generation_b/libexec/forgepilot-bootstrap\""*) ;;
  *) fail 'generation discovery did not bind skill, CLI, and helper to one generation' ;;
esac
[ "$physical_skill" != "$root/versions/$generation_a/skills/codex/forgepilot-onboarding" ] || fail 'old skill was unexpectedly current'

digest_a=$(printf '%s\n' "$(/bin/cat "$root/manifest.json")" | /usr/bin/sed -n "s/.*\"$generation_a\":{\"payload_digest\":\"\([^\"]*\)\".*/\1/p")
digest_b=$(printf '%s\n' "$(/bin/cat "$root/manifest.json")" | /usr/bin/sed -n "s/.*\"$generation_b\":{\"payload_digest\":\"\([^\"]*\)\".*/\1/p")
[ -n "$digest_a" ] && [ -n "$digest_b" ] || fail 'cannot inspect fixture generation digests'
source_hash=$(printf '%s' "$source" | /usr/bin/shasum -a 256 | /usr/bin/awk '{ print $1 }')
source_tree_a=$(/usr/bin/git -C "$source" rev-parse "$generation_a^{tree}")
source_tree_b=$(/usr/bin/git -C "$source" rev-parse "$generation_b^{tree}")
empty_retention_hash=$(printf '' | /usr/bin/shasum -a 256 | /usr/bin/awk '{ print $1 }')

printf 'committed auxiliary skill\n' >"$source/skills/codex/forgepilot-onboarding/aux.md"
printf 'verify:\n\tgo test ./...\n\tprintf changed > skills/codex/forgepilot-onboarding/aux.md\n' >"$source/Makefile"
/usr/bin/git -C "$source" -c user.name=Fixture -c user.email=fixture@example.invalid add .
/usr/bin/git -C "$source" -c user.name=Fixture -c user.email=fixture@example.invalid commit -qm 'fixture C changes skill during verify'
generation_c=$(/usr/bin/git -C "$source" rev-parse HEAD)
mutation_plan=$(HOME="$home" "$bootstrap" plan --upgrade --agent codex --source "$source" --commit "$generation_c") || fail 'skill-mutation plan failed'
mutation_id=$(plan_id "$mutation_plan")
if HOME="$home" "$bootstrap" install --upgrade --agent codex --source "$source" --commit "$generation_c" --approve "$mutation_id" >/dev/null 2>&1; then fail 'mutated staged skill was published'; fi
[ "$(/usr/bin/readlink "$root/current")" = "versions/$generation_b" ] || fail 'failed verification changed current generation'
HOME="$home" "$bootstrap" status >/dev/null || fail 'failed verification damaged the managed installation'

# An initial transaction interrupted after publishing only the CLI link must
# remain non-idle and finish through a newly approved matching recovery plan.
initial_recovery_home=$fixture/initial-recovery-home
initial_recovery_root=$initial_recovery_home/.local/share/forgepilot
/bin/mkdir -p "$initial_recovery_root/versions" "$initial_recovery_root/retention/v1" "$initial_recovery_home/.local/bin" "$initial_recovery_home/.agents/skills"
/bin/chmod 700 "$initial_recovery_home" "$initial_recovery_home/.local" "$initial_recovery_home/.local/share" "$initial_recovery_home/.local/bin" "$initial_recovery_home/.agents" "$initial_recovery_home/.agents/skills" "$initial_recovery_root" "$initial_recovery_root/versions" "$initial_recovery_root/retention" "$initial_recovery_root/retention/v1"
: >"$initial_recovery_root/lock"
/bin/cp -pR "$root/versions/$generation_a" "$initial_recovery_root/versions/$generation_a"
initial_recovery_stage=$initial_recovery_home/.local/share/.forgepilot-stage.Abc123
/bin/mkdir "$initial_recovery_stage"
initial_recovery_identity=$(/usr/bin/stat -f '%d:%i' "$initial_recovery_stage")
printf '{"schema_version":1,"operation":"install","phase":"prepared","source_sha256":"%s","source_commit":"%s","source_tree":"%s","payload_digest":"%s","old_current":"none","old_previous":"none","old_manifest_sha256":"none","retention_sha256":"%s","retention_count":0,"stage":".forgepilot-stage.Abc123","stage_identity":"%s"}\n' \
	"$source_hash" "$generation_a" "$source_tree_a" "$digest_a" "$empty_retention_hash" "$initial_recovery_identity" >"$initial_recovery_root/transaction.json"
/bin/ln -s "$initial_recovery_root/current/bin/forgepilot" "$initial_recovery_home/.local/bin/forgepilot"
if HOME="$initial_recovery_home" "$bootstrap" status >/dev/null 2>&1; then fail 'interrupted initial install looked idle'; fi
if HOME="$initial_recovery_home" "$bootstrap" plan --upgrade --agent codex --source "$source" --commit "$generation_a" >/dev/null 2>&1; then fail 'upgrade planned through initial transaction'; fi
initial_recovery_plan=$(HOME="$initial_recovery_home" "$bootstrap" plan --agent codex --source "$source" --commit "$generation_a") || fail 'initial recovery planning failed'
initial_recovery_id=$(plan_id "$initial_recovery_plan")
HOME="$initial_recovery_home" "$bootstrap" install --agent codex --source "$source" --commit "$generation_a" --approve "$initial_recovery_id" || fail 'initial recovery failed'
HOME="$initial_recovery_home" "$bootstrap" status >/dev/null || fail 'initial recovery left an invalid layout'
assert_not_path "$initial_recovery_root/transaction.json"

# An upgrade interrupted after generation publication but before the current
# switch keeps the old manifest authoritative until matching recovery.
upgrade_recovery_home=$fixture/upgrade-recovery-home
/bin/cp -pR "$home" "$upgrade_recovery_home"
upgrade_recovery_root=$upgrade_recovery_home/.local/share/forgepilot
rm "$upgrade_recovery_home/.local/bin/forgepilot" "$upgrade_recovery_home/.local/bin/forgepilot-bootstrap" "$upgrade_recovery_home/.agents/skills/forgepilot-onboarding"
/bin/ln -s "$upgrade_recovery_root/current/bin/forgepilot" "$upgrade_recovery_home/.local/bin/forgepilot"
/bin/ln -s "$upgrade_recovery_root/current/libexec/forgepilot-bootstrap" "$upgrade_recovery_home/.local/bin/forgepilot-bootstrap"
/bin/ln -s "$upgrade_recovery_root/current/skills/codex/forgepilot-onboarding" "$upgrade_recovery_home/.agents/skills/forgepilot-onboarding"
rm "$upgrade_recovery_root/current" "$upgrade_recovery_root/previous"
/bin/ln -s "versions/$generation_a" "$upgrade_recovery_root/current"
printf '{"schema_version":1,"generations":{"%s":{"payload_digest":"%s","path":"versions/%s"}},"current":"%s","previous":null}\n' \
	"$generation_a" "$digest_a" "$generation_a" "$generation_a" >"$upgrade_recovery_root/manifest.json"
upgrade_old_manifest_hash=$(/usr/bin/shasum -a 256 "$upgrade_recovery_root/manifest.json" | /usr/bin/awk '{ print $1 }')
upgrade_recovery_stage=$upgrade_recovery_home/.local/share/.forgepilot-stage.Def456
/bin/mkdir "$upgrade_recovery_stage"
upgrade_recovery_identity=$(/usr/bin/stat -f '%d:%i' "$upgrade_recovery_stage")
printf '{"schema_version":1,"operation":"upgrade","phase":"prepared","source_sha256":"%s","source_commit":"%s","source_tree":"%s","payload_digest":"%s","old_current":"%s","old_previous":"none","old_manifest_sha256":"%s","retention_sha256":"%s","retention_count":0,"stage":".forgepilot-stage.Def456","stage_identity":"%s"}\n' \
	"$source_hash" "$generation_b" "$source_tree_b" "$digest_b" "$generation_a" "$upgrade_old_manifest_hash" "$empty_retention_hash" "$upgrade_recovery_identity" >"$upgrade_recovery_root/transaction.json"
if HOME="$upgrade_recovery_home" "$bootstrap" status >/dev/null 2>&1; then fail 'interrupted upgrade looked idle'; fi
upgrade_recovery_plan=$(HOME="$upgrade_recovery_home" "$bootstrap" plan --upgrade --agent codex --source "$source" --commit "$generation_b") || fail 'upgrade recovery planning failed'
upgrade_recovery_id=$(plan_id "$upgrade_recovery_plan")
HOME="$upgrade_recovery_home" "$bootstrap" install --upgrade --agent codex --source "$source" --commit "$generation_b" --approve "$upgrade_recovery_id" || fail 'upgrade recovery failed'
HOME="$upgrade_recovery_home" "$bootstrap" status >/dev/null || fail 'upgrade recovery left an invalid layout'
assert_not_path "$upgrade_recovery_root/transaction.json"

# A missing recorded new generation permits only exact-link containment for
# initial installation, and restoration of the verified old tuple for upgrade.
missing_initial_home=$fixture/missing-initial-home
/bin/cp -pR "$initial_recovery_home" "$missing_initial_home"
missing_initial_root=$missing_initial_home/.local/share/forgepilot
/bin/rm "$missing_initial_home/.local/bin/forgepilot" "$missing_initial_home/.local/bin/forgepilot-bootstrap" "$missing_initial_home/.agents/skills/forgepilot-onboarding"
/bin/ln -s "$missing_initial_root/current/bin/forgepilot" "$missing_initial_home/.local/bin/forgepilot"
/bin/ln -s "$missing_initial_root/current/libexec/forgepilot-bootstrap" "$missing_initial_home/.local/bin/forgepilot-bootstrap"
/bin/ln -s "$missing_initial_root/current/skills/codex/forgepilot-onboarding" "$missing_initial_home/.agents/skills/forgepilot-onboarding"
/bin/rm -rf "$missing_initial_root/versions/$generation_a"
/bin/rm "$missing_initial_root/current" "$missing_initial_root/manifest.json"
missing_stage=$missing_initial_home/.local/share/.forgepilot-stage.Ghi789
/bin/mkdir "$missing_stage"
missing_stage_identity=$(/usr/bin/stat -f '%d:%i' "$missing_stage")
printf '{"schema_version":1,"operation":"install","phase":"switching","source_sha256":"%s","source_commit":"%s","source_tree":"%s","payload_digest":"%s","old_current":"none","old_previous":"none","old_manifest_sha256":"none","retention_sha256":"%s","retention_count":0,"stage":".forgepilot-stage.Ghi789","stage_identity":"%s"}\n' \
	"$source_hash" "$generation_a" "$source_tree_a" "$digest_a" "$empty_retention_hash" "$missing_stage_identity" >"$missing_initial_root/transaction.json"
if HOME="$missing_initial_home" "$bootstrap" status >/dev/null 2>&1; then fail 'missing initial generation looked idle'; fi
missing_initial_plan=$(HOME="$missing_initial_home" "$bootstrap" plan --agent codex --source "$source" --commit "$generation_a") || fail 'missing initial recovery plan failed'
missing_initial_id=$(plan_id "$missing_initial_plan")
HOME="$missing_initial_home" "$bootstrap" install --agent codex --source "$source" --commit "$generation_a" --approve "$missing_initial_id" || fail 'missing initial containment failed'
assert_not_path "$missing_initial_home/.local/bin/forgepilot"
assert_not_path "$missing_initial_home/.local/bin/forgepilot-bootstrap"
assert_not_path "$missing_initial_home/.agents/skills/forgepilot-onboarding"
assert_file "$missing_initial_root/transaction.json"
if HOME="$missing_initial_home" "$bootstrap" status >/dev/null 2>&1; then fail 'contained initial transaction looked idle'; fi
repair_plan=$(HOME="$missing_initial_home" "$bootstrap" plan --agent codex --source "$source" --commit "$generation_a") || fail 'contained initial repair plan failed'
repair_id=$(plan_id "$repair_plan")
HOME="$missing_initial_home" "$bootstrap" install --agent codex --source "$source" --commit "$generation_a" --approve "$repair_id" || fail 'contained initial repair failed'
HOME="$missing_initial_home" "$bootstrap" status >/dev/null || fail 'repaired initial installation is invalid'
assert_not_path "$missing_initial_root/transaction.json"

missing_upgrade_home=$fixture/missing-upgrade-home
/bin/cp -pR "$upgrade_recovery_home" "$missing_upgrade_home"
missing_upgrade_root=$missing_upgrade_home/.local/share/forgepilot
/bin/rm "$missing_upgrade_home/.local/bin/forgepilot" "$missing_upgrade_home/.local/bin/forgepilot-bootstrap" "$missing_upgrade_home/.agents/skills/forgepilot-onboarding"
/bin/ln -s "$missing_upgrade_root/current/bin/forgepilot" "$missing_upgrade_home/.local/bin/forgepilot"
/bin/ln -s "$missing_upgrade_root/current/libexec/forgepilot-bootstrap" "$missing_upgrade_home/.local/bin/forgepilot-bootstrap"
/bin/ln -s "$missing_upgrade_root/current/skills/codex/forgepilot-onboarding" "$missing_upgrade_home/.agents/skills/forgepilot-onboarding"
/bin/rm -rf "$missing_upgrade_root/versions/$generation_b"
missing_upgrade_stage=$missing_upgrade_home/.local/share/.forgepilot-stage.Jkl012
/bin/mkdir "$missing_upgrade_stage"
missing_upgrade_identity=$(/usr/bin/stat -f '%d:%i' "$missing_upgrade_stage")
printf '{"schema_version":1,"operation":"upgrade","phase":"switching","source_sha256":"%s","source_commit":"%s","source_tree":"%s","payload_digest":"%s","old_current":"%s","old_previous":"none","old_manifest_sha256":"%s","retention_sha256":"%s","retention_count":0,"stage":".forgepilot-stage.Jkl012","stage_identity":"%s"}\n' \
	"$source_hash" "$generation_b" "$source_tree_b" "$digest_b" "$generation_a" "$upgrade_old_manifest_hash" "$empty_retention_hash" "$missing_upgrade_identity" >"$missing_upgrade_root/transaction.json"
if HOME="$missing_upgrade_home" "$bootstrap" status >/dev/null 2>&1; then fail 'missing upgrade generation looked idle'; fi
missing_upgrade_plan=$(HOME="$missing_upgrade_home" "$bootstrap" plan --upgrade --agent codex --source "$source" --commit "$generation_b") || fail 'missing upgrade recovery plan failed'
missing_upgrade_id=$(plan_id "$missing_upgrade_plan")
HOME="$missing_upgrade_home" "$bootstrap" install --upgrade --agent codex --source "$source" --commit "$generation_b" --approve "$missing_upgrade_id" || fail 'missing upgrade restoration failed'
[ "$(/usr/bin/readlink "$missing_upgrade_root/current")" = "versions/$generation_a" ] || fail 'missing upgrade did not restore old current'
assert_not_path "$missing_upgrade_root/transaction.json"
HOME="$missing_upgrade_home" "$bootstrap" status >/dev/null || fail 'missing upgrade restoration left an invalid layout'

drifted_upgrade_home=$fixture/drifted-upgrade-home
/bin/cp -pR "$upgrade_recovery_home" "$drifted_upgrade_home"
drifted_upgrade_root=$drifted_upgrade_home/.local/share/forgepilot
/bin/rm "$drifted_upgrade_home/.local/bin/forgepilot" "$drifted_upgrade_home/.local/bin/forgepilot-bootstrap" "$drifted_upgrade_home/.agents/skills/forgepilot-onboarding"
/bin/ln -s "$drifted_upgrade_root/current/bin/forgepilot" "$drifted_upgrade_home/.local/bin/forgepilot"
/bin/ln -s "$drifted_upgrade_root/current/libexec/forgepilot-bootstrap" "$drifted_upgrade_home/.local/bin/forgepilot-bootstrap"
/bin/ln -s "$drifted_upgrade_root/current/skills/codex/forgepilot-onboarding" "$drifted_upgrade_home/.agents/skills/forgepilot-onboarding"
printf 'drift\n' >>"$drifted_upgrade_root/versions/$generation_b/bin/forgepilot"
drifted_stage=$drifted_upgrade_home/.local/share/.forgepilot-stage.Mno345
/bin/mkdir "$drifted_stage"
drifted_identity=$(/usr/bin/stat -f '%d:%i' "$drifted_stage")
printf '{"schema_version":1,"operation":"upgrade","phase":"switching","source_sha256":"%s","source_commit":"%s","source_tree":"%s","payload_digest":"%s","old_current":"%s","old_previous":"none","old_manifest_sha256":"%s","retention_sha256":"%s","retention_count":0,"stage":".forgepilot-stage.Mno345","stage_identity":"%s"}\n' \
	"$source_hash" "$generation_b" "$source_tree_b" "$digest_b" "$generation_a" "$upgrade_old_manifest_hash" "$empty_retention_hash" "$drifted_identity" >"$drifted_upgrade_root/transaction.json"
drifted_plan=$(HOME="$drifted_upgrade_home" "$bootstrap" plan --upgrade --agent codex --source "$source" --commit "$generation_b") || fail 'drifted upgrade containment plan failed'
drifted_id=$(plan_id "$drifted_plan")
HOME="$drifted_upgrade_home" "$bootstrap" install --upgrade --agent codex --source "$source" --commit "$generation_b" --approve "$drifted_id" || fail 'drifted upgrade containment failed'
[ "$(/usr/bin/readlink "$drifted_upgrade_root/current")" = "versions/$generation_a" ] || fail 'drifted upgrade did not restore old current'
assert_file "$drifted_upgrade_root/transaction.json"
if HOME="$drifted_upgrade_home" "$bootstrap" status >/dev/null 2>&1; then fail 'drifted upgrade looked idle'; fi
printf 'forgepilot-bootstrap_install_test: PASS\n'
