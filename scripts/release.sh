#!/usr/bin/env bash
# Cut a Tosk release.
#
#   scripts/release.sh patch|minor|major|X.Y.Z [--dry-run] [--yes] [--no-watch]
#
# 1. computes the version from the latest vX.Y.Z tag (or takes it as given);
# 2. checks: on main, clean tree, in sync with origin, CHANGELOG has notes
#    under "## [Unreleased]";
# 3. runs the linters and tests;
# 4. turns "## [Unreleased]" into "## [X.Y.Z] - <date>" in CHANGELOG.md and
#    updates the links at the bottom;
# 5. commits "Release X.Y.Z" and creates the annotated tag vX.Y.Z;
# 6. asks before pushing main and the tag (--yes skips the question): the
#    tag starts .github/workflows/release.yml, which publishes the binaries
#    with the CHANGELOG section as release notes;
# 7. follows that workflow and prints the release URL (--no-watch skips it).
#
# --dry-run does steps 1 to 4, shows the CHANGELOG change and puts it back.

set -euo pipefail

usage() {
	sed -n '2,19p' "$0" | sed 's/^# \{0,1\}//'
	exit "${1:-2}"
}

die() {
	echo "release: $*" >&2
	exit 1
}

say() { echo "release: $*"; }

bump="" dry_run=false yes=false watch=true
for arg in "$@"; do
	case "$arg" in
	--dry-run) dry_run=true ;;
	--yes) yes=true ;;
	--no-watch) watch=false ;;
	-h | --help) usage 0 ;;
	-*) die "unknown option $arg" ;;
	*)
		[ -z "$bump" ] || die "one version argument only"
		bump="$arg"
		;;
	esac
done
[ -n "$bump" ] || usage

cd "$(git rev-parse --show-toplevel)"
changelog=CHANGELOG.md
[ -f "$changelog" ] || die "no $changelog"

# --- 1. version --------------------------------------------------------------

semver='^[0-9]+\.[0-9]+\.[0-9]+$'
previous=$(git tag --list 'v*' --sort=-v:refname | grep -E "^v[0-9]+\.[0-9]+\.[0-9]+$" | head -n 1 || true)
previous=${previous#v}

case "$bump" in
patch | minor | major)
	[ -n "$previous" ] || die "no previous vX.Y.Z tag: give the version explicitly"
	IFS=. read -r major minor patch <<<"$previous"
	case "$bump" in
	patch) patch=$((patch + 1)) ;;
	minor) minor=$((minor + 1)) patch=0 ;;
	major) major=$((major + 1)) minor=0 patch=0 ;;
	esac
	version="$major.$minor.$patch"
	;;
*)
	version=${bump#v}
	[[ $version =~ $semver ]] || die "$bump is not patch, minor, major or X.Y.Z"
	;;
esac
tag="v$version"
git rev-parse -q --verify "refs/tags/$tag" >/dev/null && die "$tag already exists"
if [ -n "$previous" ]; then
	newest=$(printf '%s\n%s\n' "$previous" "$version" | sort -V | tail -n 1)
	[ "$newest" = "$version" ] || die "$version is not newer than $previous"
fi
say "version ${previous:-none} -> $version"

# --- 2. checks ---------------------------------------------------------------

branch=$(git symbolic-ref --short HEAD 2>/dev/null || true)
[ "$branch" = main ] || die "releases are cut from main (on ${branch:-a detached HEAD})"
[ -z "$(git status --porcelain)" ] || die "the working tree has uncommitted changes"
git fetch --quiet origin main --tags
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] ||
	die "main and origin/main differ: pull or push first"

notes=$(awk '
	/^## \[Unreleased\]/ { found = 1; next }
	found && /^## / { exit }
	found && NF { print }
' "$changelog")
[ -n "$notes" ] || die "nothing under \"## [Unreleased]\" in $changelog: write the release notes first"

# --- 3. lint and tests -------------------------------------------------------

say "running lint and tests"
make lint >/dev/null
if command -v golangci-lint >/dev/null 2>&1; then
	golangci-lint run ./...
else
	say "golangci-lint not installed: CI will run it"
fi
go test ./... >/dev/null
if command -v goreleaser >/dev/null 2>&1; then
	goreleaser check >/dev/null 2>&1 || die "goreleaser check failed"
fi

# --- 4. CHANGELOG ------------------------------------------------------------

url=$(git remote get-url origin)
url=${url%.git}
url=${url/#git@github.com:/https:\/\/github.com\/}
today=$(date +%Y-%m-%d)
if [ -n "$previous" ]; then
	link="[$version]: $url/compare/v$previous...$tag"
else
	link="[$version]: $url/releases/tag/$tag"
fi

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
awk -v version="$version" -v today="$today" -v link="$link" \
	-v unreleased="[Unreleased]: $url/compare/$tag...HEAD" '
	/^## \[Unreleased\]/ && !done {
		print "## [Unreleased]"
		print ""
		print "## [" version "] - " today
		done = 1
		next
	}
	/^\[Unreleased\]: / { print unreleased; print link; linked = 1; next }
	{ print }
	END {
		if (!linked) { print ""; print unreleased; print link }
	}
' "$changelog" >"$tmp"

if $dry_run; then
	say "dry run: CHANGELOG.md would change like this"
	diff -u "$changelog" "$tmp" || true
	say "dry run: nothing was changed"
	exit 0
fi
cp "$tmp" "$changelog"

# --- 5. commit and tag -------------------------------------------------------

git add "$changelog"
git commit --quiet -m "Release $version"
git tag -a "$tag" -m "Tosk $version"
say "committed \"Release $version\" and tagged $tag"

# --- 6. push -----------------------------------------------------------------

undo="git tag -d $tag && git reset --hard HEAD~1"
if ! $yes; then
	printf 'release: push main and %s to origin? This publishes the release. [y/N] ' "$tag"
	read -r answer
	case "$answer" in
	y | Y | yes) ;;
	*)
		say "not pushed. To undo: $undo"
		exit 1
		;;
	esac
fi
git push --quiet origin main
git push --quiet origin "$tag"
say "pushed main and $tag"

# --- 7. follow the release workflow ------------------------------------------

if ! $watch || ! command -v gh >/dev/null 2>&1; then
	say "the release workflow is running: $url/actions"
	exit 0
fi
say "waiting for the release workflow"
run=""
for _ in $(seq 1 20); do
	run=$(gh run list --workflow release.yml --branch "$tag" --limit 1 --json databaseId --jq '.[0].databaseId' 2>/dev/null || true)
	[ -n "$run" ] && break
	sleep 3
done
[ -n "$run" ] || die "release workflow not found: check $url/actions"
if gh run watch "$run" --exit-status --interval 10 >/dev/null; then
	say "released: $(gh release view "$tag" --json url --jq .url)"
else
	die "the release workflow failed: gh run view $run --log-failed"
fi
