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
# The tag message names the PatchMon code the release is really built on:
# the official tag when only official releases were merged (the rule), or
# "v2.1.3-113-gc8f42251" when commits PatchMon has not released yet are in,
# with the database migrations they add. Such a release needs
# --unreleased-base: going back to the official image of that base then
# needs a database restore.
#
# Usage: tools/fork-release.sh [--push] [--unreleased-base]
#   Windows: run it with the bash of Git for Windows, not the WSL one.
set -euo pipefail

die() { echo "error: $*" >&2; exit 1; }

push=false
allow_unreleased=false
for arg in "$@"; do
  case "$arg" in
    --push) push=true ;;
    --unreleased-base) allow_unreleased=true ;;
    *) die "unknown option $arg (use --push, --unreleased-base)" ;;
  esac
done

cd "$(git rev-parse --show-toplevel)"
[ -z "$(git status --porcelain)" ] || die "the working tree has uncommitted changes"
git remote get-url upstream >/dev/null 2>&1 ||
  die "no 'upstream' remote; add it: git remote add upstream https://github.com/PatchMon/PatchMon.git"

# Official tags from upstream (kept local, never pushed to the fork), fork
# tags from origin.
git fetch -q --tags upstream
git fetch -q upstream main
git fetch -q --tags origin

git branch -r --contains HEAD | grep -q '^ *origin/' ||
  die "HEAD is not on any branch of origin; push it first"

# Tagging a commit already released publishes the same code under a new
# version, typically because the pull request was not merged yet.
released="$(git tag --points-at HEAD -l 'v*-fork.*' | tr '\n' ' ')"
[ -z "$released" ] ||
  die "HEAD ($(git rev-parse --short HEAD)) is already released as ${released% }: merge the pending pull requests, pull, then run this again"

base_tag="$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' --exclude '*-*' HEAD 2>/dev/null)" ||
  die "no official release tag in the history of HEAD"
printf '%s' "$base_tag" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+$' || die "unexpected base tag $base_tag"
base="${base_tag#v}"
next="${base%.*}.$(( ${base##*.} + 1 ))"

# The newest PatchMon commit merged into HEAD, described from its official
# tag: "v2.1.3" when it is the release itself.
upstream_base="$(git merge-base HEAD upstream/main)" || die "HEAD shares no history with upstream/main"
upstream_desc="$(git describe --tags --match 'v[0-9]*.[0-9]*.[0-9]*' --exclude '*-*' "$upstream_base")" ||
  die "no official release tag before the PatchMon commits merged into HEAD"
message="PatchMon fork release vNEXT, based on PatchMon $upstream_desc"
if [ "$upstream_desc" != "$base_tag" ]; then
  ahead="$(git rev-list --count "$base_tag..$upstream_base")"
  migrations="$(git diff --name-only --diff-filter=AM "$base_tag" "$upstream_base" -- \
    server-source-code/internal/migrate/migrations/ | sed -n 's#.*/\([0-9]*_[^/]*\)\.up\.sql$#\1#p' | tr '\n' ' ')"
  warning="It contains $ahead PatchMon commits after $base_tag that PatchMon has not released yet"
  if [ -n "$migrations" ]; then
    warning="$warning, with these database migrations: ${migrations% }. Going back to the official $base_tag image needs a database restore"
  fi
  if ! $allow_unreleased; then
    echo "$warning." >&2
    die "merge official tags only (git merge vX.Y.Z), or pass --unreleased-base to release it anyway"
  fi
  echo "warning: $warning." >&2
  message="$message.

$warning."
fi

n=0
while read -r t; do
  [ -n "$t" ] || continue
  k="${t##*-fork.}"
  case "$k" in ''|*[!0-9]*) continue ;; esac
  [ "$k" -gt "$n" ] && n="$k"
done < <(git tag -l "v$next-fork.*")
tag="v$next-fork.$((n + 1))"

git tag -a "$tag" -m "${message/vNEXT/${tag}}"
echo "Tagged $tag on $(git rev-parse --short HEAD), based on PatchMon $upstream_desc."

if $push; then
  git push origin "$tag"
  echo "Pushed: the Docker workflow builds the image and publishes the release."
else
  echo "Push it to build the image and publish the release:"
  echo "  git push origin $tag"
fi
