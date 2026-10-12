#!/usr/bin/env bash
# Tag a release of this fork: vX.Y.Z-fork.N.
#
# X.Y.Z is the official version after the newest official release merged
# into HEAD, N counts the fork's releases of it. vX.Y.Z-fork.N sorts above
# the official release it is built on and below the next one, so instances
# and agents move up to it, and later to the next official release.
#
# Pushing the tag makes .github/workflows/docker.yml build
# ghcr.io/<owner>/patchmon-server:<version> and :fork, agents included, and
# publish the GitHub release the instances' update check follows.
#
# Usage: tools/fork-release.sh [--push]
#   Windows: run it with the bash of Git for Windows, not the WSL one.
set -euo pipefail

die() { echo "error: $*" >&2; exit 1; }

cd "$(git rev-parse --show-toplevel)"
[ -z "$(git status --porcelain)" ] || die "the working tree has uncommitted changes"
git remote get-url upstream >/dev/null 2>&1 ||
  die "no 'upstream' remote; add it: git remote add upstream https://github.com/PatchMon/PatchMon.git"

# Official tags from upstream (kept local, never pushed to the fork), fork
# tags from origin.
git fetch -q --tags upstream
git fetch -q --tags origin

git branch -r --contains HEAD | grep -q '^ *origin/' ||
  die "HEAD is not on any branch of origin; push it first"

base_tag="$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' --exclude '*-*' HEAD 2>/dev/null)" ||
  die "no official release tag in the history of HEAD"
printf '%s' "$base_tag" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+$' || die "unexpected base tag $base_tag"
base="${base_tag#v}"
next="${base%.*}.$(( ${base##*.} + 1 ))"

n=0
while read -r t; do
  [ -n "$t" ] || continue
  k="${t##*-fork.}"
  case "$k" in ''|*[!0-9]*) continue ;; esac
  [ "$k" -gt "$n" ] && n="$k"
done < <(git tag -l "v$next-fork.*")
tag="v$next-fork.$((n + 1))"

git tag -a "$tag" -m "PatchMon fork release $tag, based on $base_tag"
echo "Tagged $tag on $(git rev-parse --short HEAD), based on $base_tag."

if [ "${1:-}" = "--push" ]; then
  git push origin "$tag"
  echo "Pushed: the Docker workflow builds the image and publishes the release."
else
  echo "Push it to build the image and publish the release:"
  echo "  git push origin $tag"
fi
