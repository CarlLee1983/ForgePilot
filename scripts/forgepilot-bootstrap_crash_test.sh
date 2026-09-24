#!/bin/sh
set -eu
umask 077

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
bootstrap=$script_dir/forgepilot-bootstrap
fixture=$(mktemp -d "${TMPDIR:-/tmp}/forgepilot-bootstrap-crash-test.XXXXXX")
fixture=$(CDPATH='' cd -P -- "$fixture" && pwd)
trap 'rm -rf "$fixture"' EXIT HUP INT TERM
fail() { printf 'forgepilot-bootstrap_crash_test: %s\n' "$*" >&2; exit 1; }
plan_id() { printf '%s\n' "$1" | /usr/bin/awk -F': ' '$1 == "Plan ID" { print $2 }'; }
assert_absent() { [ ! -e "$1" ] && [ ! -L "$1" ] || fail "unexpected path: $1"; }
assert_link() { [ -L "$1" ] && [ "$(/usr/bin/readlink "$1")" = "$2" ] || fail "wrong link: $1"; }

# An exact, unique production statement owns each crash point. The child shell
# kills its immediate parent: the lock-holding shell, rather than the test harness.
instrument() (
	point=$1 anchor=$2 guard=$3 placement=$4
	injected=$fixture/injected-$point
	/usr/bin/awk -v anchor="$anchor" -v guard="$guard" -v placement="$placement" '
		function kill_line() {
			if (guard != "") print "\tif [ " guard " ]; then"
			print "\tprintf crashed >\"$HOME/crash-marker\""
			print "\t/bin/sh -c '\''kill -KILL \"$PPID\"'\''"
			if (guard != "") print "\tfi"
		}
		index($0, anchor) {
			count++
			if (placement == "before") kill_line()
			print
			if (placement == "after") kill_line()
			next
		}
		{ print }
		END { if (count != 1) exit 23 }
	' "$bootstrap" >"$injected" || fail "$point: production anchor is not unique"
	/bin/chmod 700 "$injected"
	/bin/sh -n "$injected" || fail "$point: injected script has invalid shell syntax"
)

source=$fixture/source
/bin/mkdir -p "$source/cmd/forgepilot" "$source/scripts" "$source/skills/codex/forgepilot-onboarding" "$source/docs/release"
printf 'module github.com/CarlLee1983/ForgePilot\n\ngo 1.25.5\n' >"$source/go.mod"
printf 'verify:\n\t@true\n' >"$source/Makefile"
/bin/cp "$bootstrap" "$source/scripts/forgepilot-bootstrap"
/bin/chmod 700 "$source/scripts/forgepilot-bootstrap"
printf '# Fixture procedure\n' >"$source/docs/release/onboarding.md"
/usr/bin/git -C "$source" init -q
for label in a b c d; do
	printf 'package main\nimport "fmt"\nfunc main() { fmt.Println("fixture-%s") }\n' "$label" >"$source/cmd/forgepilot/main.go"
	printf '# Fixture skill %s\n' "$label" >"$source/skills/codex/forgepilot-onboarding/SKILL.md"
	/usr/bin/git -C "$source" -c user.name=Fixture -c user.email=fixture@example.invalid add .
	/usr/bin/git -C "$source" -c user.name=Fixture -c user.email=fixture@example.invalid commit -qm "fixture $label"
	commit=$(/usr/bin/git -C "$source" rev-parse HEAD)
	case "$label" in a) a=$commit ;; b) b=$commit ;; c) c=$commit ;; d) d=$commit ;; esac
done

install_generation() {
	home=$1 commit=$2 mode=$3
	if [ "$mode" = upgrade ]; then
		plan=$(HOME="$home" "$bootstrap" plan --upgrade --agent codex --source "$source" --commit "$commit") || fail 'baseline upgrade plan failed'
		HOME="$home" "$bootstrap" install --upgrade --agent codex --source "$source" --commit "$commit" --approve "$(plan_id "$plan")" >/dev/null || fail 'baseline upgrade failed'
	else
		/bin/mkdir -p "$home"
		plan=$(HOME="$home" "$bootstrap" plan --agent codex --source "$source" --commit "$commit") || fail 'baseline install plan failed'
		HOME="$home" "$bootstrap" install --agent codex --source "$source" --commit "$commit" --approve "$(plan_id "$plan")" >/dev/null || fail 'baseline install failed'
	fi
}

# Copying a home preserves absolute stable-link targets, so retarget only those
# three links to the clone. No managed manifest or generation bytes are edited.
clone_home() {
	from=$1 to=$2
	/bin/cp -pR "$from" "$to"
	root=$to/.local/share/forgepilot
	/bin/rm "$to/.local/bin/forgepilot" "$to/.local/bin/forgepilot-bootstrap" "$to/.agents/skills/forgepilot-onboarding"
	/bin/ln -s "$root/current/bin/forgepilot" "$to/.local/bin/forgepilot"
	/bin/ln -s "$root/current/libexec/forgepilot-bootstrap" "$to/.local/bin/forgepilot-bootstrap"
	/bin/ln -s "$root/current/skills/codex/forgepilot-onboarding" "$to/.agents/skills/forgepilot-onboarding"
	HOME="$to" "$bootstrap" status >/dev/null || fail 'cloned baseline is invalid'
}

base_a=$fixture/base-a
install_generation "$base_a" "$a" install
base_d=$fixture/base-d
clone_home "$base_a" "$base_d"
for generation in "$b" "$c" "$d"; do install_generation "$base_d" "$generation" upgrade; done

run_case() {
	kind=$1 point=$2 anchor=$3 guard=$4
	case "${FORGEPILOT_CRASH_ONLY:-}" in ''|"$kind/$point") ;; *) return 0 ;; esac
	home=$fixture/case-$kind-$point
	case "$kind" in
		install) /bin/mkdir -p "$home"; command=install; commit=$a; mode='' ;;
		upgrade) clone_home "$base_a" "$home"; command=install; commit=$b; mode=--upgrade ;;
		uninstall|prune) clone_home "$base_d" "$home"; command=$kind; commit=$a; mode='' ;;
	esac
	root=$home/.local/share/forgepilot
	case "$point" in *-b) crash_candidate=$b ;; *) crash_candidate=$a ;; esac
	instrument "$kind-$point" "$anchor" "$guard" "${5:-after}"
	injected=$fixture/injected-$kind-$point
	case "$kind" in
		install|upgrade)
			plan=$(HOME="$home" "$bootstrap" plan $mode --agent codex --source "$source" --commit "$commit") || fail "$kind/$point: plan failed"
			set -- install $mode --agent codex --source "$source" --commit "$commit" ;;
		uninstall)
			plan=$(HOME="$home" "$bootstrap" plan --uninstall --commit "$commit") || fail "$kind/$point: plan failed"
			set -- uninstall --commit "$commit" ;;
		prune)
			plan=$(HOME="$home" "$bootstrap" plan --prune) || fail "$kind/$point: plan failed"
			set -- prune ;;
	esac
	original_id=$(plan_id "$plan")
	case "$original_id" in sha256:*) ;; *) fail "$kind/$point: missing original approval" ;; esac
	if FORGEPILOT_CRASH_CANDIDATE="$crash_candidate" HOME="$home" "$injected" "$@" --approve "$original_id" >"$fixture/crash-output" 2>&1; then
		fail "$kind/$point: instrumented operation unexpectedly succeeded"
	fi
	[ "$(/bin/cat "$home/crash-marker" 2>/dev/null)" = crashed ] || fail "$kind/$point: kill point was not reached"
	[ -f "$root/transaction.json" ] || fail "$kind/$point: authoritative transaction was not present at crash"
	transaction_before=$(/usr/bin/shasum -a 256 "$root/transaction.json" | /usr/bin/awk '{print $1}')
	if HOME="$home" "$bootstrap" status >/dev/null 2>&1; then fail "$kind/$point: status reported idle"; fi
	[ -f "$root/transaction.json" ] || fail "$kind/$point: status repaired transaction"
	[ "$(/usr/bin/shasum -a 256 "$root/transaction.json" | /usr/bin/awk '{print $1}')" = "$transaction_before" ] || fail "$kind/$point: status changed transaction"
	case "$kind" in
		install|upgrade)
			if HOME="$home" "$bootstrap" plan --prune >/dev/null 2>&1; then fail "$kind/$point: unrelated prune planned through transaction"; fi ;;
		uninstall)
			if HOME="$home" "$bootstrap" plan --prune >/dev/null 2>&1; then fail "$kind/$point: unrelated prune planned through transaction"; fi ;;
		prune)
			if HOME="$home" "$bootstrap" plan --uninstall --commit "$a" >/dev/null 2>&1; then fail "$kind/$point: unrelated uninstall planned through transaction"; fi ;;
	esac
	[ "$(/usr/bin/shasum -a 256 "$root/transaction.json" | /usr/bin/awk '{print $1}')" = "$transaction_before" ] || fail "$kind/$point: unrelated plan changed transaction"
	case "$kind/$point" in
		install/link-cli) assert_link "$home/.local/bin/forgepilot" "$root/current/bin/forgepilot" ;;
		install/link-helper) assert_link "$home/.local/bin/forgepilot-bootstrap" "$root/current/libexec/forgepilot-bootstrap" ;;
		install/link-skill) assert_link "$home/.agents/skills/forgepilot-onboarding" "$root/current/skills/codex/forgepilot-onboarding" ;;
		upgrade/current) assert_link "$root/current" "versions/$b" ;;
		upgrade/previous) assert_link "$root/previous" "versions/$a" ;;
		uninstall/move|prune/move-a) [ -d "$root/removal/$a" ] || fail "$kind/$point: first candidate was not staged" ;;
		prune/move-b) [ -d "$root/removal/$b" ] || fail "$kind/$point: second candidate was not staged" ;;
		uninstall/delete|prune/delete-a) assert_absent "$root/removal/$a" ;;
		prune/delete-b) assert_absent "$root/removal/$b" ;;
	esac
	case "$kind" in
		install|upgrade) recovery=$(HOME="$home" "$bootstrap" plan $mode --agent codex --source "$source" --commit "$commit") || fail "$kind/$point: matching recovery plan failed" ;;
		uninstall) recovery=$(HOME="$home" "$bootstrap" plan --uninstall --commit "$commit") || fail "$kind/$point: matching recovery plan failed" ;;
		prune) recovery=$(HOME="$home" "$bootstrap" plan --prune) || fail "$kind/$point: matching recovery plan failed" ;;
	esac
	recovery_id=$(plan_id "$recovery")
	case "$recovery_id" in sha256:*) ;; *) fail "$kind/$point: missing recovery approval" ;; esac
	[ "$recovery_id" != "$original_id" ] || fail "$kind/$point: recovery reused pre-crash approval"
	if [ "$kind/$point" = uninstall/tampered-candidate ]; then
		[ -d "$root/removal/$a" ] || fail 'tamper case has no staged candidate'
		manifest_before=$(/usr/bin/shasum -a 256 "$root/manifest.json" | /usr/bin/awk '{print $1}')
		printf 'unrecorded child\n' >"$root/removal/$a/unrecorded-child"
		if HOME="$home" "$bootstrap" plan --uninstall --commit "$a" >/dev/null 2>&1; then fail 'tampered candidate received a recovery plan'; fi
		if HOME="$home" "$bootstrap" "$@" --approve "$recovery_id" >/dev/null 2>&1; then fail 'tampered candidate was deleted'; fi
		[ -f "$root/removal/$a/unrecorded-child" ] || fail 'tampered child was deleted'
		[ -d "$root/removal/$a" ] || fail 'tampered generation was deleted'
		[ "$(/usr/bin/shasum -a 256 "$root/manifest.json" | /usr/bin/awk '{print $1}')" = "$manifest_before" ] || fail 'tamper rejection changed manifest'
		[ "$(/usr/bin/shasum -a 256 "$root/transaction.json" | /usr/bin/awk '{print $1}')" = "$transaction_before" ] || fail 'tamper rejection changed transaction'
		printf '  %s/%s PASS\n' "$kind" "$point"
		return 0
	fi
	if [ "$kind/$point" = uninstall/partial-candidate ]; then
		[ -f "$root/removal/$a/bin/forgepilot" ] || fail 'partial case has no staged CLI'
		manifest_before=$(/usr/bin/shasum -a 256 "$root/manifest.json" | /usr/bin/awk '{print $1}')
		/bin/rm "$root/removal/$a/bin/forgepilot"
		if HOME="$home" "$bootstrap" plan --uninstall --commit "$a" >/dev/null 2>&1; then fail 'partial candidate received a recovery plan'; fi
		if HOME="$home" "$bootstrap" "$@" --approve "$recovery_id" >/dev/null 2>&1; then fail 'partial candidate was deleted'; fi
		if HOME="$home" "$bootstrap" status >/dev/null 2>&1; then fail 'partial candidate looked idle'; fi
		[ -d "$root/removal/$a" ] || fail 'partial generation was deleted'
		[ "$(/usr/bin/shasum -a 256 "$root/manifest.json" | /usr/bin/awk '{print $1}')" = "$manifest_before" ] || fail 'partial rejection changed manifest'
		[ "$(/usr/bin/shasum -a 256 "$root/transaction.json" | /usr/bin/awk '{print $1}')" = "$transaction_before" ] || fail 'partial rejection changed transaction'
		printf '  %s/%s PASS\n' "$kind" "$point"
		return 0
	fi
	if HOME="$home" "$bootstrap" "$@" --approve "$original_id" >/dev/null 2>&1; then fail "$kind/$point: stale approval was accepted"; fi
	[ -f "$root/transaction.json" ] || fail "$kind/$point: stale approval changed transaction"
	HOME="$home" "$bootstrap" "$@" --approve "$recovery_id" >/dev/null || fail "$kind/$point: approved recovery failed"
	assert_absent "$root/transaction.json"
	HOME="$home" "$bootstrap" status >/dev/null || fail "$kind/$point: recovered layout is invalid"
	case "$kind" in
		install) assert_link "$root/current" "versions/$a" ;;
		upgrade) assert_link "$root/current" "versions/$b"; assert_link "$root/previous" "versions/$a" ;;
		uninstall) assert_absent "$root/versions/$a"; [ -d "$root/versions/$b" ] || fail 'uninstall removed another generation' ;;
		prune) assert_absent "$root/versions/$a"; assert_absent "$root/versions/$b"; [ -d "$root/versions/$c" ] && [ -d "$root/versions/$d" ] || fail 'prune removed a protected generation' ;;
	esac
	printf '  %s/%s PASS\n' "$kind" "$point"
}

run_case install transaction 'cannot publish installation transaction' ''
run_case install generation '/bin/mv "$INSTALL_STAGE/payload" "$VERSIONS/$INSTALL_COMMIT" || fail' ''
run_case install link-cli '/bin/ln -s "$install_link_target" "$install_link" || fail' '"$install_link_kind" = cli'
run_case install link-helper '/bin/ln -s "$install_link_target" "$install_link" || fail' '"$install_link_kind" = helper'
run_case install link-skill '/bin/ln -s "$install_link_target" "$install_link" || fail' '"$install_link_kind" = skill'
run_case install switching 'cannot publish installation transaction' '"$INSTALL_TXN_PHASE" = switching'
run_case install current-stage 'cannot stage generation pointer' '"$install_pointer" = current'
run_case install current '/bin/mv -fh "$install_temp" "$ROOT/$install_pointer" || fail' '"$install_pointer" = current'
run_case install manifest-stage 'write_manifest_from_tuples "$INSTALL_TUPLES" "$PENDING_MANIFEST"' ''
run_case install manifest 'cannot publish generation manifest' ''
run_case install finalize 'cannot finalize installation transaction' '' before

run_case upgrade transaction 'cannot publish installation transaction' ''
run_case upgrade generation '/bin/mv "$INSTALL_STAGE/payload" "$VERSIONS/$INSTALL_COMMIT" || fail' ''
run_case upgrade switching 'cannot publish installation transaction' '"$INSTALL_TXN_PHASE" = switching'
run_case upgrade current-stage 'cannot stage generation pointer' '"$install_pointer" = current'
run_case upgrade current '/bin/mv -fh "$install_temp" "$ROOT/$install_pointer" || fail' '"$install_pointer" = current'
run_case upgrade previous-stage 'cannot stage generation pointer' '"$install_pointer" = previous'
run_case upgrade previous '/bin/mv -fh "$install_temp" "$ROOT/$install_pointer" || fail' '"$install_pointer" = previous'
run_case upgrade manifest-stage 'write_manifest_from_tuples "$INSTALL_TUPLES" "$PENDING_MANIFEST"' ''
run_case upgrade manifest 'cannot publish generation manifest' ''
run_case upgrade finalize 'cannot finalize installation transaction' '' before

run_case uninstall transaction 'cannot publish removal transaction' ''
run_case uninstall move '/bin/mv "$VERSIONS/$execution_candidate_id" "$ROOT/removal/$execution_candidate_id" || fail' ''
run_case uninstall manifest-stage '/bin/cat "$final_manifest" >"$PENDING_MANIFEST" || fail' ''
run_case uninstall manifest 'cannot publish removal manifest' ''
run_case uninstall tampered-candidate 'cannot publish removal manifest' ''
run_case uninstall partial-candidate 'cannot publish removal manifest' ''
run_case uninstall delete '/bin/rm -rf "$ROOT/removal/$execution_candidate_id" || fail' ''
run_case uninstall finalize 'cannot finalize removal transaction' '' before

run_case prune transaction 'cannot publish removal transaction' ''
run_case prune move-a '/bin/mv "$VERSIONS/$execution_candidate_id" "$ROOT/removal/$execution_candidate_id" || fail' '"$execution_candidate_id" = "$FORGEPILOT_CRASH_CANDIDATE"'
run_case prune move-b '/bin/mv "$VERSIONS/$execution_candidate_id" "$ROOT/removal/$execution_candidate_id" || fail' '"$execution_candidate_id" = "$FORGEPILOT_CRASH_CANDIDATE"'
run_case prune manifest-stage '/bin/cat "$final_manifest" >"$PENDING_MANIFEST" || fail' ''
run_case prune manifest 'cannot publish removal manifest' ''
run_case prune delete-a '/bin/rm -rf "$ROOT/removal/$execution_candidate_id" || fail' '"$execution_candidate_id" = "$FORGEPILOT_CRASH_CANDIDATE"'
run_case prune delete-b '/bin/rm -rf "$ROOT/removal/$execution_candidate_id" || fail' '"$execution_candidate_id" = "$FORGEPILOT_CRASH_CANDIDATE"'
run_case prune finalize 'cannot finalize removal transaction' '' before

printf 'forgepilot-bootstrap_crash_test: PASS (35 crash boundaries, 2 fail-closed candidate cases)\n'
