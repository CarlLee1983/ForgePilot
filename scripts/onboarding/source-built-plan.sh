#!/bin/sh
# Prints the reviewed source-built onboarding actions. It never fetches,
# builds, verifies, changes an entrypoint, or writes a target repository.
set -eu

fail() { printf 'source_built_plan: %s\n' "$*" >&2; exit 1; }

source=''
commit=''
stage_root=''
entrypoint=''
target=''
candidate=''
candidate_kind=''
goal_id=''
goal_title=''
story=''
while [ "$#" -gt 0 ]; do
	case "$1" in
		--source) source=${2:?}; shift 2 ;;
		--commit) commit=${2:?}; shift 2 ;;
		--stage-root) stage_root=${2:?}; shift 2 ;;
		--entrypoint) entrypoint=${2:?}; shift 2 ;;
		--target) target=${2:?}; shift 2 ;;
		--candidate) candidate=${2:?}; shift 2 ;;
		--candidate-kind) candidate_kind=${2:?}; shift 2 ;;
		--goal-id) goal_id=${2:?}; shift 2 ;;
		--goal-title) goal_title=${2:?}; shift 2 ;;
		--story) story=${2:?}; shift 2 ;;
		*) fail "unknown option: $1" ;;
	esac
done

[ -n "$source" ] || fail '--source is required'
[ -n "$commit" ] || fail '--commit is required'
[ -n "$stage_root" ] || fail '--stage-root is required'
[ -n "$entrypoint" ] || fail '--entrypoint is required'
[ -n "$target" ] || fail '--target is required'
[ -n "$candidate" ] || fail '--candidate is required'
[ -n "$candidate_kind" ] || fail '--candidate-kind is required'
[ -n "$goal_id" ] || fail '--goal-id is required'
[ -n "$goal_title" ] || fail '--goal-title is required'
[ -n "$story" ] || fail '--story is required'
case "$commit" in
	*[!0123456789abcdef]*) fail '--commit must be a full lowercase 40-character SHA' ;;
esac
[ "${#commit}" -eq 40 ] || fail '--commit must be a full lowercase 40-character SHA'
case "$stage_root:$entrypoint:$target" in
	/*:/*:/*) ;;
	*) fail '--stage-root, --entrypoint, and --target must be absolute paths' ;;
esac

stage="$stage_root/$commit"
git -C "$target" rev-parse --show-toplevel >/dev/null 2>&1 || fail 'target must be a Git repository'
case "$candidate_kind" in
	COMMIT)
		candidate_commit=$(git -C "$target" rev-parse --verify "$candidate^{commit}" 2>/dev/null) || fail 'COMMIT Candidate must name a commit'
		candidate_head=$(git -C "$target" rev-parse --verify HEAD 2>/dev/null) || fail 'target has no HEAD commit'
		[ "$candidate_commit" = "$candidate_head" ] || fail 'COMMIT Candidate must match target HEAD; re-plan after HEAD changes'
		git -C "$target" show "$candidate_commit:Makefile" 2>/dev/null | grep -Eq '^verify:' || fail 'Candidate does not contain a make verify target'
		candidate_display="COMMIT $candidate_commit"
		;;
	SNAPSHOT)
		[ -f "$target/Makefile" ] || fail 'SNAPSHOT Candidate does not contain a Makefile'
		if git -C "$target" check-ignore -q Makefile && ! git -C "$target" ls-files --error-unmatch Makefile >/dev/null 2>&1; then fail 'ignored untracked Makefile is absent from SNAPSHOT Candidate'; fi
		grep -Eq '^verify:' "$target/Makefile" || fail 'SNAPSHOT Candidate does not contain a make verify target'
		candidate_display='SNAPSHOT current target workspace (identity captured before verification)'
		;;
	*) fail '--candidate-kind must be COMMIT or SNAPSHOT' ;;
esac
printf '%s\n' 'INSPECTION ONLY — no fetch, build, verify, entrypoint, or repository write'
printf 'target repository: %s\nsource repository: %s\nsource commit: %s\n' "$target" "$source" "$commit"
printf 'target Candidate: %s\n' "$candidate_display"
printf '%s\n' 'Inspect the target Candidate and its Makefile before any write.'
printf '%s\n' 'FIRST EXPLICIT APPROVAL REQUIRED'
printf 'source fetch: git clone --no-checkout %s %s/source\n' "$source" "$stage"
printf 'source checkout: git -C %s/source fetch origin %s && git -C %s/source checkout --detach %s\n' "$stage" "$commit" "$stage" "$commit"
printf 'build: GOBIN=%s/bin go install ./cmd/forgepilot (in %s/source)\n' "$stage" "$stage"
printf 'verification: make verify (in %s/source)\n' "$stage"
printf 'atomic entrypoint switch: ln -s %s/bin/forgepilot %s.new && mv %s.new %s\n' "$stage" "$entrypoint" "$entrypoint" "$entrypoint"
printf '%s\n' 'SECOND EXPLICIT APPROVAL REQUIRED'
if [ -e "$target/.forgepilot" ]; then
	printf 'repository writes: reuse existing state; forgepilot status (in %s)\n' "$target"
else
	printf 'repository writes in %s:\n' "$target"
	printf '  forgepilot init — creates ForgePilot state\n'
	printf '  forgepilot goal create --id %s --title %s — creates the Goal\n' "$goal_id" "$goal_title"
	printf '  forgepilot work add --goal %s --story %s — creates the Work Item\n' "$goal_id" "$story"
	printf '  forgepilot status — reads the resulting state\n'
fi
