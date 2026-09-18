#!/bin/sh
# Builds reviewable, unsigned trial binaries. This distribution helper stays
# outside ForgePilot's offline governance CLI (ADR-0024).
set -eu

readonly CHANNEL='unsigned-trial'
readonly TARGETS='arm64 amd64'

usage() {
	cat <<'EOF'
usage: scripts/release/build_trial_assets.sh [--output <directory>] <full-commit-sha>
       scripts/release/build_trial_assets.sh --asset-stem <arm64|amd64>

Build unsigned trial assets for darwin/arm64 and darwin/amd64 from one exact
commit. The default output is dist/trial-assets/<full-commit-sha>.
EOF
}

asset_stem() {
	case "$1" in
		arm64|amd64) printf 'forgepilot-darwin-%s\n' "$1" ;;
		*) printf 'unsupported macOS architecture: %s\n' "$1" >&2; return 1 ;;
	esac
}

asset_name() {
	printf '%s-%s\n' "$(asset_stem "$1")" "$CHANNEL"
}

fail() {
	printf 'build_trial_assets: %s\n' "$*" >&2
	exit 1
}

if [ "${1:-}" = '--asset-stem' ]; then
	[ "$#" -eq 2 ] || { usage >&2; exit 2; }
	asset_stem "$2"
	exit 0
fi

output=''
while [ "$#" -gt 0 ]; do
	case "$1" in
		--output)
			[ "$#" -ge 2 ] || fail '--output requires a directory'
			output=$2
			shift 2
			;;
		--help|-h)
			usage
			exit 0
			;;
		--*) fail "unknown option: $1" ;;
		*)
			[ -z "${commit:-}" ] || fail 'provide exactly one full commit SHA'
			commit=$1
			shift
			;;
	esac
done

[ -n "${commit:-}" ] || fail 'a full commit SHA is required'
case "$commit" in
	*[!0123456789abcdef]*|???????????????????????????????????????)
		fail 'a full, lowercase 40-character commit SHA is required'
		;;
esac
[ "${#commit}" -eq 40 ] || fail 'a full, lowercase 40-character commit SHA is required'

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository=$(CDPATH= cd -P -- "$script_dir/../.." && pwd)
git -C "$repository" cat-file -e "$commit^{commit}" 2>/dev/null || fail "commit does not exist: $commit"

if [ -z "$output" ]; then
	output=$repository/dist/trial-assets/$commit
fi
case "$output" in
	/*) ;;
	*) output=$repository/$output ;;
esac
case "/$output/" in
	*'/./'*|*'/../'*) fail 'output must not contain . or .. path components' ;;
esac
output_name=$(basename -- "$output")
case "$output_name" in
	.|..) fail 'output must name a directory, not . or ..' ;;
esac

# Resolve only an existing ancestor before creating anything. That makes a
# case-insensitive alias or a symlink into the source repository subject to the
# same boundary rule as a spelling that names it directly.
output_parent=$(dirname -- "$output")
unresolved_suffix=''
while [ ! -d "$output_parent" ]; do
	parent_name=$(basename -- "$output_parent")
	unresolved_suffix=/$parent_name$unresolved_suffix
	next_parent=$(dirname -- "$output_parent")
	[ "$next_parent" != "$output_parent" ] || fail "could not resolve output parent: $output_parent"
	output_parent=$next_parent
done
output_parent=$(CDPATH= cd -P -- "$output_parent" && pwd) || fail "could not resolve output parent: $output_parent"
output_parent=$output_parent$unresolved_suffix
output=$output_parent/$output_name
if [ -e "$output" ]; then
	output=$(CDPATH= cd -P -- "$output" && pwd) || fail "output must be a directory: $output"
fi
case "$output" in
	"$repository"|"$repository"/*)
		case "$output" in
			"$repository/dist"|"$repository/dist"/*) ;;
			*) fail 'output inside the source repository is limited to its ignored dist directory' ;;
		esac
		;;
esac
case "$repository" in
	"$output"/*) fail 'output must not be an ancestor of the source repository' ;;
esac
[ "$output" != / ] || fail 'output must not be an ancestor of the source repository'
mkdir -p "$output_parent" || fail "could not create output parent: $output_parent"
if [ -e "$output" ]; then
	[ -d "$output" ] || fail "existing output is not a directory: $output"
	case "$(uname -m)" in
		arm64) existing_host_arch=arm64 ;;
		x86_64|i386) existing_host_arch=amd64 ;;
		*) fail 'unsupported build host architecture' ;;
	esac
	for required in "$(asset_name arm64)" "$(asset_name amd64)" SHA256SUMS provenance.json startup-evidence.json "$(asset_name "$existing_host_arch").startup.txt"; do
		[ -f "$output/$required" ] || fail "existing output is not a matching trial build: $output"
	done
	[ "$(find "$output" -type f | wc -l | tr -d ' ')" = 6 ] || fail "existing output is not a matching trial build: $output"
	if find "$output" -mindepth 1 ! -type f -print -quit | grep -q .; then
		fail "existing output is not a matching trial build: $output"
	fi
	expected_provenance=$(printf '%s\n' \
		'{' \
		"  \"channel\": \"$CHANNEL\"," \
		"  \"commit\": \"$commit\"," \
		"  \"version\": \"0.0.0+commit.$commit\"," \
		'  "assets": [' \
		"    {\"name\": \"$(asset_name arm64)\", \"target\": \"darwin/arm64\"}," \
		"    {\"name\": \"$(asset_name amd64)\", \"target\": \"darwin/amd64\"}" \
		'  ]' \
		'}')
	[ "$(cat "$output/provenance.json")" = "$expected_provenance" ] || fail "existing output is not a matching trial build: $output"
	(cd "$output" && shasum -a 256 -c SHA256SUMS >/dev/null) || fail "existing output is not a matching trial build: $output"
fi

checkout=$(mktemp -d "${TMPDIR:-/tmp}/forgepilot-trial-checkout.XXXXXX")
stage=$(mktemp -d "$output_parent/.trial-assets.XXXXXX")
cleanup() {
	rm -rf "$checkout" "$stage"
}
trap cleanup EXIT HUP INT TERM

# Archive the requested tree instead of building from the caller's working
# files. Unlike a worktree this has no Git metadata side effect in the source
# repository, while still making the exact commit the only build input.
git -C "$repository" archive --format=tar "$commit" | tar -x -C "$checkout" || fail 'could not extract requested source commit'
# Trial assets have no release-tag authority. Derive their version only from
# the immutable input, not from the repository's mutable tag namespace.
version=0.0.0+commit.$commit

for architecture in $TARGETS; do
	if [ "${FORGEPILOT_TRIAL_TEST_FAIL_TARGET:-}" = "$architecture" ]; then
		fail "test hook forced $architecture build failure"
	fi
	asset=$stage/$(asset_name "$architecture")
	GOCACHE="$checkout/.go-build-cache" GOOS=darwin GOARCH=$architecture CGO_ENABLED=0 go -C "$checkout" build -trimpath -buildvcs=false -o "$asset" ./cmd/forgepilot \
		2>/dev/null || fail "darwin/$architecture build failed"
	description=$(file -b "$asset") || fail "could not inspect darwin/$architecture asset"
	case "$architecture" in
		arm64) expected_macho='arm64' ;;
		amd64) expected_macho='x86_64' ;;
	esac
	if [ "${FORGEPILOT_TRIAL_TEST_FORCE_ARCH_MISMATCH:-}" = "$architecture" ] || ! printf '%s\n' "$description" | grep -Eq "Mach-O.*(^|[^[:alnum:]_])$expected_macho([^[:alnum:]_]|$)"; then
		fail "darwin/$architecture asset reports Mach-O architecture: $description"
	fi
done

host_arch=$(uname -m)
case "$host_arch" in
	arm64|x86_64) ;;
	i386) host_arch=amd64 ;;
	*) fail "unsupported build host architecture: $host_arch" ;;
esac

startup_entries=''
startup_separator=''
for architecture in $TARGETS; do
	asset=$(asset_name "$architecture")
	if [ "$architecture" = "$host_arch" ]; then
		if [ "${FORGEPILOT_TRIAL_TEST_FAIL_STARTUP:-}" = "$architecture" ] || ! "$stage/$asset" --help >"$stage/$asset.startup.txt" 2>&1; then
			fail "darwin/$architecture CLI startup check failed"
		fi
		startup_entries="$startup_entries$startup_separator
    {\"target\": \"darwin/$architecture\", \"status\": \"passed\", \"command\": \"$asset --help\", \"output\": \"$asset.startup.txt\"}"
	else
		startup_entries="$startup_entries$startup_separator
    {\"target\": \"darwin/$architecture\", \"status\": \"not-run-cross-architecture\", \"reason\": \"requires native $architecture host\"}"
	fi
	startup_separator=','
done

(
	cd "$stage"
	shasum -a 256 "$(asset_name arm64)" "$(asset_name amd64)" > SHA256SUMS
)

cat >"$stage/provenance.json" <<EOF
{
  "channel": "$CHANNEL",
  "commit": "$commit",
  "version": "$version",
  "assets": [
    {"name": "$(asset_name arm64)", "target": "darwin/arm64"},
    {"name": "$(asset_name amd64)", "target": "darwin/amd64"}
  ]
}
EOF
printf '[\n%s\n  ]\n' "$startup_entries" >"$stage/startup-evidence.json"

# A completed staging directory is the only thing ever published. Replacing an
# earlier generated directory makes an identical-commit rerun safe.
if [ -e "$output" ]; then
	backup=$(mktemp -d "$output_parent/.trial-assets-previous.XXXXXX") || fail "could not prepare replacement backup: $output"
	mv "$output" "$backup/previous" || fail "could not replace existing output: $output"
	mv "$stage" "$output" || { mv "$backup/previous" "$output"; fail "could not publish output: $output"; }
	rm -rf "$backup"
	stage=''
else
	mv "$stage" "$output" || fail "could not publish output: $output"
	stage=''
fi

printf 'unsigned maintainer trial assets: %s\n' "$output"
