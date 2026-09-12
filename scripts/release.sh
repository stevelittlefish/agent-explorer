#!/usr/bin/env bash
#
# Cut a release by pushing a version tag.
#
# The Build workflow triggers on pushed v* tags: it tests, builds the Linux and
# Windows executables, creates a GitHub Release named after the tag, and attaches
# the downloads. This script just creates and pushes the tag.
#
# Usage:
#   scripts/release.sh            Show the latest release tag and the suggested next one.
#   scripts/release.sh vX.Y.Z     Create and push that tag, cutting the release.
#   scripts/release.sh X.Y.Z      Same; a leading "v" is added for you.

set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

# Newest v* tag by version order, or empty if there are none yet.
latest_tag() {
	git tag --list 'v*' --sort=-version:refname | head -n1
}

# Given vX.Y.Z, print the next patch version (vX.Y.Z+1).
suggest_next() {
	local tag="${1#v}"
	local major minor patch
	IFS=. read -r major minor patch <<<"$tag"
	# Fall back gracefully if the latest tag is not plain X.Y.Z.
	if [[ ! $major =~ ^[0-9]+$ || ! $minor =~ ^[0-9]+$ || ! $patch =~ ^[0-9]+$ ]]; then
		echo "v0.0.1"
		return
	fi
	echo "v${major}.${minor}.$((patch + 1))"
}

# No arguments: report current state and suggest the next tag, then exit.
if [[ $# -eq 0 ]]; then
	current="$(latest_tag)"
	if [[ -z $current ]]; then
		echo "No release tags yet."
		echo "Suggested first release: v0.1.0"
		echo
		echo "Cut it with: scripts/release.sh v0.1.0"
	else
		next="$(suggest_next "$current")"
		echo "Latest release tag: $current"
		echo "Suggested next tag: $next"
		echo
		echo "Cut it with: scripts/release.sh $next"
	fi
	exit 0
fi

if [[ $# -gt 1 ]]; then
	echo "error: expected a single version argument (e.g. v1.2.3)" >&2
	exit 2
fi

# Normalise: accept both "1.2.3" and "v1.2.3".
version="$1"
[[ $version == v* ]] || version="v$version"

if [[ ! $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	echo "error: '$version' is not a valid version tag (expected vX.Y.Z)" >&2
	exit 2
fi

if git rev-parse -q --verify "refs/tags/$version" >/dev/null; then
	echo "error: tag $version already exists" >&2
	exit 1
fi

# Warn, but do not block, if releasing from something other than main.
branch="$(git rev-parse --abbrev-ref HEAD)"
if [[ $branch != main ]]; then
	echo "warning: you are on '$branch', not 'main'." >&2
fi

if [[ -n "$(git status --porcelain)" ]]; then
	echo "warning: working tree has uncommitted changes; the tag will point at the last commit." >&2
fi

echo "Tagging $(git rev-parse --short HEAD) as $version and pushing to origin..."
git tag -a "$version" -m "Release $version"
git push origin "$version"

echo
echo "Pushed $version. Watch the release build here:"
echo "  https://github.com/stevelittlefish/agent-explorer/actions/workflows/build.yml"
