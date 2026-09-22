#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
workflow=$root/.github/workflows/publish-trial-assets.yml
docs=$root/docs/release/trial-assets.md
wizard=$root/scripts/release/configure_trial_publish.sh

fail() { printf 'publish_trial_assets_workflow_test: %s\n' "$*" >&2; exit 1; }
contains() { grep -F -- "$2" "$1" >/dev/null || fail "expected $1 to contain: $2"; }
absent() { ! grep -F -- "$2" "$1" >/dev/null || fail "must not contain: $2"; }
absent_upload_clobber() { ! grep -E 'gh release upload.*--clobber' "$workflow" >/dev/null || fail 'release upload must not replace an asset'; }
contains_text() { printf '%s\n' "$1" | grep -F -- "$2" >/dev/null || fail "expected text to contain: $2"; }
workflow_job() {
  awk -v job="$1" '
    $0 == "  " job ":" { found = 1; next }
    found && /^  [[:alnum:]_-]+:/ { exit }
    found { print }
    END { if (!found) exit 1 }
  ' "$workflow"
}
line_number() { grep -nF -- "$2" "$1" | head -n1 | cut -d: -f1; }

[ -f "$workflow" ] || fail "missing workflow: $workflow"
[ -x "$wizard" ] || fail "missing executable wizard: $wizard"
bash -n "$wizard" || fail "wizard has invalid bash syntax"
contains "$workflow" 'workflow_dispatch:'
absent "$workflow" 'push:'
triggers=$(awk '
  $0 == "on:" { in_trigger = 1; next }
  in_trigger && /^[^[:space:]]/ { exit }
  in_trigger && /^  [^[:space:]#][^:]*:/ { sub(/^  /, ""); sub(/:.*/, ""); print }
' "$workflow")
[ "$triggers" = workflow_dispatch ] || fail "workflow must have only workflow_dispatch trigger, found: $triggers"
contains "$workflow" "github.ref == format('refs/heads/{0}', github.event.repository.default_branch)"
contains "$workflow" 'Lowercase full 40-character commit SHA reachable from main'
contains "$workflow" 'group: unsigned-trial-${{ inputs.commit }}'
contains "$workflow" 'cancel-in-progress: false'
contains "$workflow" 'contents: read'
absent "$workflow" 'contents: write'
contains "$workflow" 'runs-on: macos-15'
contains "$workflow" 'make verify'
contains "$workflow" 'go test -race -count=1 ./...'
contains "$workflow" 'unsigned-trial/$COMMIT'
contains "$workflow" 'immutable-releases'
contains "$workflow" 'RELEASE_WRITE_TOKEN'
contains "$workflow" 'RELEASE_GUARD_TOKEN'
contains "$workflow" '--draft --prerelease --latest=false'
contains "$workflow" 'environment:'
contains "$workflow" 'name: unsigned-trial-publish'
contains "$workflow" 'name: unsigned-trial-stage'
contains "$workflow" 'required_reviewers'
contains "$workflow" 'isImmutable'
contains "$workflow" 'No --clobber'
absent_upload_clobber
absent "$workflow" 'gh release delete'
for job_environment in 'prepare:' 'stage:unsigned-trial-stage' 'publish:unsigned-trial-publish'; do
  job=${job_environment%%:*}
  environment=${job_environment#*:}
  body=$(workflow_job "$job") || fail "missing $job job"
  contains_text "$body" "if: github.ref == format('refs/heads/{0}', github.event.repository.default_branch)"
  if [ "$job" != prepare ]; then
    contains_text "$body" "name: $environment"
  fi
done

# The preflight is deliberately before release creation. An absence check alone
# is racy, so stage must atomically create the exact tag ref, or (on a
# same-name create conflict) fetch and prove the tag target before any Release
# write. This is a static contract check; it does not simulate GitHub's API.
tag_preflight=$(line_number "$workflow" 'git ls-remote --exit-code --refs origin "refs/tags/$tag"')
tag_ref_create=$(line_number "$workflow" 'gh api --method POST "repos/$GH_REPO/git/refs" -f "ref=refs/tags/$tag" -f "sha=$COMMIT"')
release_create=$(line_number "$workflow" 'gh release create "$tag"')
[ -n "$tag_preflight" ] && [ -n "$tag_ref_create" ] && [ -n "$release_create" ] && [ "$tag_preflight" -lt "$tag_ref_create" ] && [ "$tag_ref_create" -lt "$release_create" ] || fail 'absent tag is not atomically bound before draft creation'
contains "$workflow" 'verify_tag_target() {'
contains "$workflow" 'if ! gh api --method POST "repos/$GH_REPO/git/refs"'
contains "$workflow" 'a concurrent creator won the ref creation'
tag_conflict_verify=$(awk -v start="$tag_ref_create" 'NR > start && /verify_tag_target/ { print NR; exit }' "$workflow")
[ -n "$tag_conflict_verify" ] && [ "$tag_conflict_verify" -lt "$release_create" ] || fail 'tag creation conflict is not re-fetched and verified before draft creation'
contains "$workflow" 'existing unsigned-trial tag targets another commit'
contains "$workflow" 'refs/tags/$tag:refs/tags/$tag'
contains "$workflow" 'verify_environment_branch_policy unsigned-trial-stage'
contains "$workflow" 'verify_environment_branch_policy unsigned-trial-publish'
contains "$workflow" '.deployment_branch_policy.custom_branch_policies'
contains "$workflow" 'deployment-branch-policies'
contains "$workflow" '. == ["main"]'
[ "$(grep -Ec '^[[:space:]]*verify_release_environment_policies$' "$workflow")" -eq 3 ] || fail 'each release write must re-check both environment branch policies'
[ "$(grep -c '^          validate_bundle$' "$workflow")" -eq 2 ] || fail 'stage and publish must both validate the transferred bundle'
[ "$(grep -c 'expected_files=.SHA256SUMS forgepilot-darwin-amd64-unsigned-trial' "$workflow")" -eq 2 ] || fail 'stage and publish must enumerate the exact six bundle files'
[ "$(grep -c 'SHA256SUMS does not exactly describe the two binaries' "$workflow")" -eq 2 ] || fail 'stage and publish must validate SHA256SUMS semantics'
[ "$(grep -c 'startup-evidence.json.*>/dev/null' "$workflow")" -ge 2 ] || fail 'stage and publish must revalidate startup evidence JSON semantics'
contains "$workflow" 'not Developer ID signed, notarized, formal onboarding'
contains "$docs" 'unsigned-trial-publish'
contains "$docs" 'immutable'
contains "$docs" 'releases'
contains "$docs" 'workflow_dispatch'
contains "$docs" 'configure_trial_publish.sh'
contains "$wizard" 'RELEASE_WRITE_TOKEN'
contains "$wizard" 'RELEASE_GUARD_TOKEN'
contains "$wizard" 'immutable-releases'
contains "$wizard" 'Selected branches'
contains "$wizard" 'deployment-branch-policies'
contains "$wizard" 'ask_fresh_secret RELEASE_WRITE_TOKEN'
contains "$wizard" 'ask_fresh_secret RELEASE_GUARD_TOKEN'
absent "$wizard" 'ask_secret RELEASE_WRITE_TOKEN'
absent "$wizard" 'ask_secret RELEASE_GUARD_TOKEN'

# A saved ambient token must never turn an empty fresh prompt into a GitHub
# secret write. The mock is entirely local; it only supplies the setup API
# responses needed to reach the token stage.
fixture=$(mktemp -d "${TMPDIR:-/tmp}/forgepilot-trial-publish-test.XXXXXX")
cleanup() { rm -rf "$fixture"; }
trap cleanup EXIT HUP INT TERM
mkdir -p "$fixture/bin"
printf 'RELEASE_WRITE_TOKEN=ambient-write-token\nRELEASE_GUARD_TOKEN=ambient-guard-token\n' >"$fixture/.env"
cat >"$fixture/bin/gh" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$GH_LOG"
case "$*" in
  *'auth status'*) exit 0 ;;
  *'immutable-releases'*'.enabled'*) printf 'true\n' ;;
  *'custom_branch_policies'*) printf 'true\n' ;;
  *'branch_policies | length'*) printf '1\n' ;;
  *'branch_policies[0].name'*) printf 'main\n' ;;
  *'required_reviewers'*) printf '1\n' ;;
esac
EOF
cat >"$fixture/bin/open" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod +x "$fixture/bin/gh" "$fixture/bin/open"
if printf '\n\nY\n\n\n\n\n' | PATH="$fixture/bin:$PATH" GH_LOG="$fixture/gh.log" ENV_FILE="$fixture/.env" REPOSITORY=example/ForgePilot "$wizard" >"$fixture/wizard.out" 2>"$fixture/wizard.err"; then
  fail 'wizard accepted empty fresh release-token input'
fi
grep -F 'secret set' "$fixture/gh.log" >/dev/null && fail 'wizard wrote an ambient release token to GitHub'
grep -F 'ambient-write-token' "$fixture/gh.log" "$fixture/wizard.out" "$fixture/wizard.err" >/dev/null && fail 'wizard exposed an ambient write token'
grep -F 'ambient-guard-token' "$fixture/gh.log" "$fixture/wizard.out" "$fixture/wizard.err" >/dev/null && fail 'wizard exposed an ambient guard token'

printf 'publish_trial_assets_workflow_test: PASS\n'
