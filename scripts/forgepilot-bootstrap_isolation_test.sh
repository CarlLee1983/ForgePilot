#!/bin/sh
set -eu
umask 077

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
bootstrap=$script_dir/forgepilot-bootstrap
fixture=$(/usr/bin/mktemp -d /tmp/forgepilot-bootstrap-isolation.XXXXXX)
trap '/bin/rm -rf "$fixture"' EXIT HUP INT TERM
fail() { printf 'forgepilot-bootstrap_isolation_test: %s\n' "$*" >&2; exit 1; }
plan_id() { printf '%s\n' "$1" | /usr/bin/awk -F': ' '$1 == "Plan ID" { print $2 }'; }

home=$fixture/home
root=$home/.local/share/forgepilot
target=$fixture/target-repository
tripwire=$fixture/tripwire
tripwire_log=$fixture/tripwire.log
output_log=$fixture/bootstrap-output.log
credential_sentinel=isolated-runtime-secret-4edc19
generation_a=0123456789abcdef0123456789abcdef01234567
generation_b=1123456789abcdef1123456789abcdef11234567
generation_c=2123456789abcdef2123456789abcdef21234567
digest_a=sha256:89b7b72858f35c9e13407e80ac040bde1113c634296a55cfa9b0a2e10585ecc6
digest_b=sha256:72b0e3a23acf97c2a0b58ecad206a851d3ca932fa475f21d884bd69a0eac6617
/bin/mkdir -p "$root/retention/v1" "$home/.local/bin" "$home/.agents/skills" \
	"$target/.git" "$target/.forgepilot" "$tripwire"
for generation in "$generation_a" "$generation_b" "$generation_c"; do
	/bin/mkdir -p "$root/versions/$generation/bin" "$root/versions/$generation/libexec" \
		"$root/versions/$generation/skills/codex/forgepilot-onboarding" \
		"$root/versions/$generation/docs/release"
	printf '#!/bin/sh\nexit 0\n' >"$root/versions/$generation/bin/forgepilot"
	printf '#!/bin/sh\nexit 0\n' >"$root/versions/$generation/libexec/forgepilot-bootstrap"
	/bin/chmod 700 "$root/versions/$generation/bin/forgepilot" "$root/versions/$generation/libexec/forgepilot-bootstrap"
	if [ "$generation" = "$generation_a" ]; then
		printf '# Fixture onboarding procedure\n' >"$root/versions/$generation/docs/release/onboarding.md"
		printf '# Fixture skill\n' >"$root/versions/$generation/skills/codex/forgepilot-onboarding/SKILL.md"
	else
		printf '# Fixture onboarding procedure B\n' >"$root/versions/$generation/docs/release/onboarding.md"
		printf '# Fixture skill B\n' >"$root/versions/$generation/skills/codex/forgepilot-onboarding/SKILL.md"
	fi
done
: >"$root/lock"
/bin/ln -s "versions/$generation_a" "$root/current"
/bin/ln -s "$root/current/bin/forgepilot" "$home/.local/bin/forgepilot"
/bin/ln -s "$root/current/libexec/forgepilot-bootstrap" "$home/.local/bin/forgepilot-bootstrap"
/bin/ln -s "$root/current/skills/codex/forgepilot-onboarding" "$home/.agents/skills/forgepilot-onboarding"
printf '{"schema_version":1,"generations":{"%s":{"payload_digest":"%s","path":"versions/%s"},"%s":{"payload_digest":"%s","path":"versions/%s"},"%s":{"payload_digest":"%s","path":"versions/%s"}},"current":"%s","previous":null}\n' \
	"$generation_a" "$digest_a" "$generation_a" \
	"$generation_b" "$digest_b" "$generation_b" \
	"$generation_c" "$digest_b" "$generation_c" "$generation_a" >"$root/manifest.json"

printf 'target sentinel\n' >"$target/README.md"
printf 'do not scan or write this target\n' >"$target/.forgepilot/state.json"
printf 'unrelated Git state\n' >"$target/.git/config"
target_before=$(
	/usr/bin/find -s "$target" -exec /usr/bin/stat -f '%N|%HT|%Lp|%z|%m' '{}' \;
	/usr/bin/find -s "$target" -type f -exec /usr/bin/shasum -a 256 '{}' \;
)

for command_name in curl git gh ssh nc codex go make xcrun; do
	printf '#!/bin/sh\nprintf "%%s\\n" "%s" >>"%s"\nexit 97\n' "$command_name" "$tripwire_log" >"$tripwire/$command_name"
	/bin/chmod 700 "$tripwire/$command_name"
done
printf '#!/bin/sh\nprintf "credential-helper\\n" >>"%s"\nexit 97\n' "$tripwire_log" >"$tripwire/credential-helper"
printf '#!/bin/sh\nprintf "askpass\\n" >>"%s"\nexit 97\n' "$tripwire_log" >"$tripwire/askpass"
/bin/chmod 700 "$tripwire/credential-helper" "$tripwire/askpass"

run_bootstrap() {
	(
		cd "$target" || exit 1
		PATH="$tripwire:/usr/bin:/bin" HOME="$home" \
			FORGEPILOT_TEST_RUNTIME_CREDENTIAL="$credential_sentinel" \
			GIT_ASKPASS="$tripwire/askpass" \
			GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=credential.helper \
			GIT_CONFIG_VALUE_0="$tripwire/credential-helper" \
			"$bootstrap" "$@"
	)
}

run_bootstrap status >>"$output_log" 2>&1 || fail 'status failed under isolated control plane'
reference=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
run_bootstrap retention-v1 acquire --generation "$generation_a" --payload-digest "$digest_a" --reference "$reference" >>"$output_log" 2>&1 || fail 'retention acquire failed'
run_bootstrap retention-v1 release --generation "$generation_a" --payload-digest "$digest_a" --reference "$reference" >>"$output_log" 2>&1 || fail 'retention release failed'
uninstall_plan=$(run_bootstrap plan --uninstall --commit "$generation_b" 2>>"$output_log") || fail 'uninstall plan failed'
printf '%s\n' "$uninstall_plan" >>"$output_log"
run_bootstrap uninstall --commit "$generation_b" --approve "$(plan_id "$uninstall_plan")" >>"$output_log" 2>&1 || fail 'approved uninstall failed'
prune_plan=$(run_bootstrap plan --prune 2>>"$output_log") || fail 'prune plan failed'
printf '%s\n' "$prune_plan" >>"$output_log"
run_bootstrap prune --approve "$(plan_id "$prune_plan")" >>"$output_log" 2>&1 || fail 'approved prune failed'
run_bootstrap status >>"$output_log" 2>&1 || fail 'final status failed'
if run_bootstrap prune --target "$target" --approve "sha256:$(printf '%064d' 0)" >>"$output_log" 2>&1; then fail 'target argument was accepted'; fi

[ ! -e "$tripwire_log" ] || fail 'network, Git, runtime, or credential tripwire was invoked'
target_after=$(
	/usr/bin/find -s "$target" -exec /usr/bin/stat -f '%N|%HT|%Lp|%z|%m' '{}' \;
	/usr/bin/find -s "$target" -type f -exec /usr/bin/shasum -a 256 '{}' \;
)
[ "$target_before" = "$target_after" ] || fail 'target repository bytes or metadata changed'
if /usr/bin/grep -R -F -- "$credential_sentinel" "$home" "$output_log" >/dev/null; then fail 'runtime credential leaked into Bootstrap state or output'; fi
if /usr/bin/grep -F -- "$target" "$output_log" >/dev/null; then fail 'target path leaked into Bootstrap output'; fi
[ ! -e "$root/versions/$generation_b" ] && [ ! -e "$root/versions/$generation_c" ] || fail 'removal did not complete'
printf 'forgepilot-bootstrap_isolation_test: PASS\n'
