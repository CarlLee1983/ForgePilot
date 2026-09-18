#!/bin/sh
# Prints the reviewed source-built onboarding actions. It never fetches,
# builds, verifies, changes an entrypoint, or writes a target repository.
set -eu
# Read only local objects; inherited Git routing must not redirect the target.
export GIT_NO_LAZY_FETCH=1
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES

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
format=text
while [ "$#" -gt 0 ]; do
	case "$1" in
		--format) format=${2:?}; shift 2 ;;
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
case "$source" in
 /*|*://*|?*:?*) ;;
 *) fail '--source must be an absolute local path, URL, or scp-style repository' ;;
esac
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
for path in "$stage_root" "$entrypoint" "$target"; do
 case "$path" in /*) ;; *) fail '--stage-root, --entrypoint, and --target must be absolute paths' ;; esac
done

host_os=$(uname -s 2>/dev/null) || fail 'cannot determine onboarding host operating system'
host_arch=$(uname -m 2>/dev/null) || fail 'cannot determine onboarding host architecture'
[ "$host_os" = Darwin ] && [ "$host_arch" = arm64 ] ||
	fail 'formal onboarding supports only Apple Silicon macOS (Darwin arm64)'

# This is deliberately a conservative static check, never `make -n`: make can
# run $(shell ...) while parsing. Dynamic/conditional targets need human review.
static_verify() {
 awk '
 continuation { continuation = /\\$/; next }
 /\\$/ { continuation=1; next }
 /^\t/ { next }
 /^[ ]*(override[ ]+|export[ ]+|private[ ]+)?define([ ]|$)/ { def++; next }
 /^[ ]*endef([ ]|$)/ { if (def) def--; next }
 def { next }
 /^[ ]*(ifeq|ifneq|ifdef|ifndef)([ (]|$)/ { cond++; next }
 /^[ ]*endif([ ]|$)/ { if (cond) cond--; next }
 cond { next }
 /^[ ]*verify[ ]*::?([^:]|$)/ && !/=/ { found=1 }
 END { exit !found }
 '
}

stage="$stage_root/$commit"
target_root=$(git -C "$target" rev-parse --show-toplevel 2>/dev/null) || fail 'target must be a Git repository'
target=$(CDPATH= cd -P -- "$target" && pwd)
[ "$target" = "$(CDPATH= cd -P -- "$target_root" && pwd)" ] || fail 'target must be the repository root'
# Source installation has its own approval; it cannot write inside the target.
outside_target() {
 probe=$1
 case "$probe/" in */../*|*/./*) fail 'installation paths must not contain dot components' ;; esac
 while [ ! -d "$probe" ]; do
  [ ! -e "$probe" ] && [ ! -L "$probe" ] || fail 'invalid installation path'
  probe=$(dirname "$probe")
 done
 resolved=$(CDPATH= cd -P -- "$probe" && pwd) || fail 'cannot resolve installation path'
 case "$resolved/" in "$target/"*) fail 'source installation must stay outside the target repository' ;; esac
}
outside_target "$stage_root"
outside_target "$(dirname "$entrypoint")"
for directory in "$stage" "$stage/bin" "$stage/source"; do
 [ ! -L "$directory" ] || fail 'derived source staging directories must not be symlinks'
 outside_target "$directory"
done
[ ! -L "$stage/bin/forgepilot" ] || fail 'staged executable must not be a symlink'
[ ! -e "$entrypoint.new" ] && [ ! -L "$entrypoint.new" ] || fail 'temporary entrypoint already exists'
[ ! -d "$entrypoint" ] || [ -L "$entrypoint" ] || fail 'entrypoint must not be a directory'
case "$candidate_kind" in
	COMMIT)
		case "$candidate" in *[!0123456789abcdef]*) fail 'COMMIT Candidate must be a full lowercase 40-character SHA' ;; esac
		[ "${#candidate}" -eq 40 ] || fail 'COMMIT Candidate must be a full lowercase 40-character SHA'
		candidate_commit=$(git -C "$target" rev-parse --verify "$candidate^{commit}" 2>/dev/null) || fail 'COMMIT Candidate must name a commit'
        [ "$candidate_commit" = "$candidate" ] || fail 'COMMIT Candidate must be the commit SHA, not a tag object'
		candidate_head=$(git -C "$target" rev-parse --verify HEAD 2>/dev/null) || fail 'target has no HEAD commit'
		[ "$candidate_commit" = "$candidate_head" ] || fail 'COMMIT Candidate must match target HEAD; re-plan after HEAD changes'
		case "$(git -C "$target" ls-tree "$candidate_commit" -- Makefile)" in
            100644*|100755*) ;;
            *) fail 'Candidate Makefile must be a regular file' ;;
        esac
        git -C "$target" show "$candidate_commit:Makefile" 2>/dev/null | static_verify || fail 'Candidate does not contain a make verify target'
		candidate_display="COMMIT $candidate_commit"
		;;
	SNAPSHOT)
		[ -f "$target/Makefile" ] && [ ! -L "$target/Makefile" ] || fail 'SNAPSHOT Candidate must contain a regular Makefile'
        # Verification seeds its private index from HEAD, not the real index.
        git -C "$target" rev-parse --verify HEAD >/dev/null 2>&1 || fail 'target has no HEAD commit'
        if [ -z "$(git -C "$target" ls-tree HEAD -- Makefile)" ]; then
            if git -C "$target" check-ignore --no-index -q -- Makefile; then
                fail 'ignored untracked Makefile is absent from SNAPSHOT Candidate'
            else
                result=$?
                [ "$result" -eq 1 ] || fail 'cannot determine snapshot ignore rules'
            fi
        fi
        # Filters/normalization can change the private-index blob. Never run a
        # filter to guess it during inspection; require explicit human review.
        # When .gitattributes is missing, Git falls back to the index. Our
        # verification index starts at HEAD; the real index may have deleted it.
        [ ! -L "$target/.gitattributes" ] || fail 'symlink attributes require human Candidate inspection'
        if [ ! -f "$target/.gitattributes" ] && [ -n "$(git -C "$target" ls-tree HEAD -- .gitattributes)" ]; then
            fail 'HEAD attribute fallback requires human Candidate inspection'
        fi
        sparse=$(git -C "$target" config --get core.sparseCheckout || :)
        case "$sparse" in ''|false) ;; *) fail 'sparse checkout requires human Candidate inspection' ;; esac
        attributes=$(git -C "$target" check-attr filter working-tree-encoding ident text eol crlf -- Makefile) || fail 'cannot inspect Makefile attributes'
        if printf '%s\n' "$attributes" | grep -Ev ': (unspecified|unset)$' >/dev/null; then
            fail 'Makefile transformations require human Candidate inspection'
        fi
        autocrlf=$(git -C "$target" config --get core.autocrlf || :)
        case "$autocrlf" in ''|false) ;; *) fail 'Makefile normalization requires human Candidate inspection' ;; esac
        static_verify <"$target/Makefile" || fail 'SNAPSHOT Candidate does not contain a make verify target'
		candidate_display='SNAPSHOT current target workspace (identity captured before verification)'
		;;
	*) fail '--candidate-kind must be COMMIT or SNAPSHOT' ;;
esac
# A missing Story is a review checkpoint, even when state already exists.
case "$story" in specs/stories/?*) ;; *) fail 'Story must be beneath specs/stories' ;; esac
case "/$story/" in */../*|*/./*|*//*) fail 'invalid Story path' ;; esac
story_exists=true
remaining=$story
part_path=$target
while [ -n "$remaining" ]; do
 component=${remaining%%/*}
 part_path=$part_path/$component
 [ ! -L "$part_path" ] || fail 'Story directories must not be symlinks'
 if [ ! -d "$part_path" ]; then
  [ ! -e "$part_path" ] || fail 'Story path must be a directory'
  story_exists=false
 fi
 case "$remaining" in */*) remaining=${remaining#*/} ;; *) remaining='' ;; esac
done
if [ "$story_exists" = true ]; then
 for leaf in story.md acceptance.md; do
  [ -f "$part_path/$leaf" ] && [ ! -L "$part_path/$leaf" ] || fail 'Story leaves must be contained regular files'
 done
fi

case "$format" in text|actions) ;; *) fail '--format must be text or actions' ;; esac

# One action list feeds the human disclosure and the test-consumable format.
# NUL framing preserves spaces, quotes and newlines without shell evaluation.
quote() {
 case "$1" in
  ''|*[!a-zA-Z0-9_./:=@+-]*) printf "'"; printf '%s' "$1" | sed "s/'/'\\\\''/g"; printf "'" ;;
  *) printf '%s' "$1" ;;
 esac
}
last_phase=''
action() {
 phase=$1; id=$2; directory=$3; effect=$4; shift 4
 if [ "$format" = actions ]; then
  printf '%s\000' "$phase" "$id" "$directory" "$effect" "$#" "$@"
 else
  if [ "$phase" != "$last_phase" ]; then
   case "$phase" in
    source) printf '%s\n' 'FIRST EXPLICIT APPROVAL REQUIRED' ;;
    story) printf '%s\n' 'STORY REVIEW REQUIRED — authorize any draft writes, then stop for human review' ;;
    repository) printf '%s\n' 'SECOND EXPLICIT APPROVAL REQUIRED' ;;
   esac
  fi
  printf '%s: ' "$id"
  for argument do quote "$argument"; printf ' '; done
  printf '— %s (in ' "$effect"; quote "$directory"; printf ')\n'
 fi
 last_phase=$phase
}

if [ "$format" = actions ]; then
 printf 'forgepilot-onboarding-plan-v1\000'
else
 printf '%s\n' 'INSPECTION ONLY — no fetch, build, verify, entrypoint, or repository write'
 printf 'target repository: %s\nsource repository: %s\nsource commit: %s\n' "$target" "$source" "$commit"
 printf 'target Candidate: %s\n' "$candidate_display"
 printf '%s\n' 'Inspect the target Candidate and its Makefile before any write.'
fi
action source go-prerequisite "$target" 'requires an already installed compatible Go; stop if unavailable' env GOTOOLCHAIN=local go version
action source stage-directories "$target" 'creates user-owned staging and entrypoint parent directories' mkdir -p "$stage/bin" "$(dirname "$entrypoint")"
action source source-fetch "$target" 'source fetch into the versioned staging directory' git clone --no-checkout -- "$source" "$stage/source"
action source source-commit "$stage/source" 'fetches the exact source commit' git fetch origin "$commit"
action source source-checkout "$stage/source" 'checks out the exact source commit detached' git checkout --detach "$commit"
action source build "$stage/source" 'builds with GOBIN in versioned staging; no toolchain downloads' env "GOBIN=$stage/bin" GOTOOLCHAIN=local go install ./cmd/forgepilot
action source verification "$stage/source" 'runs the approved source canonical check' env GOTOOLCHAIN=local make verify
action source startup "$stage/source" 'confirms the staged CLI starts' "$stage/bin/forgepilot" --help
action source entrypoint-link "$target" 'prepares an atomic entrypoint switch; refuses an existing temporary path' ln -s "$stage/bin/forgepilot" "$entrypoint.new"
action source entrypoint-switch "$target" 'atomic entrypoint switch after build, verification and startup succeed' mv -fh "$entrypoint.new" "$entrypoint"
action story review-story "$target" 'reuse contained regular Story files; otherwise authorize a draft and stop for review before work add' "$story"
if [ "$story_exists" != true ]; then
 : # No repository commands until the draft has been reviewed and re-planned.
elif [ -e "$target/.forgepilot" ] || [ -L "$target/.forgepilot" ]; then
 action repository status "$target" 'reuse existing state; reads the resulting state' "$entrypoint" status
else
 action repository init "$target" 'creates ForgePilot state' "$entrypoint" init
 action repository goal-create "$target" 'creates the Goal' "$entrypoint" goal create --id "$goal_id" --title "$goal_title"
 action repository work-add "$target" 'creates the Work Item after Story review' "$entrypoint" work add --goal "$goal_id" --story "$story"
 action repository status "$target" 'reads the resulting state' "$entrypoint" status
fi
