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
# The tag message is the release notes. It opens with what changed since the
# previous fork release: the title of every pull request merged into the
# fork branch (and of every commit pushed to it directly), as new features,
# fixes and other changes. Write pull request titles for the people who
# read them there: "feat(activity): show the agent update live" becomes
# "Activity: show the agent update live (#9)" under "New".
#
# It then names the PatchMon code the release is really built on: the
# official tag when only official releases were merged (the rule), or
# "v2.1.3-113-gc8f42251" when commits PatchMon has not released yet are in,
# with the database migrations they add. Such a release needs
# --unreleased-base: going back to the official image of that base then
# needs a database restore.
#
# Usage: tools/fork-release.sh [--push] [--unreleased-base] [--dry-run]
#   --dry-run prints the tag and its message, and creates nothing.
#   Windows: run it with the bash of Git for Windows, not the WSL one.
set -euo pipefail

die() { echo "error: $*" >&2; exit 1; }

# describe_official REV: REV from the official release before it, as
# "v2.1.3" or "v2.1.3-113-gc8f42251".
describe_official() {
  git describe --tags --match 'v[0-9]*.[0-9]*.[0-9]*' --exclude '*-*' "$1"
}

# capitalize TEXT: TEXT with its first letter in upper case (bash 3 has no
# ${x^}).
capitalize() {
  printf '%s%s' "$(printf '%s' "${1:0:1}" | tr '[:lower:]' '[:upper:]')" "${1:1}"
}

# release_notes PREV UPSTREAM_BASE: what changed on the first-parent line of
# HEAD since PREV, the previous fork release (since the PatchMon code when
# empty), in groups split by a blank line, or nothing. A merged pull request
# is named by its title, a commit pushed to the branch by its subject; a
# merge of PatchMon code is told as the move of the PatchMon base, from the
# one of PREV to UPSTREAM_BASE.
release_notes() {
  local prev="$1" upstream_base="$2"
  local new="" fixes="" other="" prev_base
  local sha parents subject body title type scope text
  local cc='^([a-z]+)(\(([^)]+)\))?!?: (.+)$'
  local pr='^Merge pull request #([0-9]+) from ([^ ]+)$'
  local -a range
  if [ -n "$prev" ]; then range=("$prev..HEAD"); else range=(HEAD --not upstream/main); fi

  # One record per commit: fields split by \x1f, records ended by \x1e.
  while IFS=$'\x1f' read -r -d $'\x1e' sha parents subject body; do
    sha="${sha#$'\n'}"
    if [[ "$subject" =~ $pr ]]; then
      title="$(printf '%s\n' "$body" | sed -n '/[^[:space:]]/{p;q;}')"
      [ -n "$title" ] || title="${BASH_REMATCH[2]#*/}"
      title="$title (#${BASH_REMATCH[1]})"
    elif [ "${parents#* }" != "$parents" ] &&
      git merge-base --is-ancestor "${parents#* }" upstream/main; then
      continue
    else
      title="$subject"
    fi

    type=other
    text="$title"
    if [[ "$title" =~ $cc ]]; then
      case "${BASH_REMATCH[1]}" in
        feat | fix)
          type="${BASH_REMATCH[1]}"
          scope="${BASH_REMATCH[3]}"
          text="${BASH_REMATCH[4]}"
          if [ -n "$scope" ]; then text="$(capitalize "$scope"): $text"; else text="$(capitalize "$text")"; fi
          ;;
      esac
    fi
    case "$type" in
      feat) new="$new- $text"$'\n' ;;
      fix) fixes="$fixes- $text"$'\n' ;;
      *) other="$other- $text"$'\n' ;;
    esac
  done < <(git log --reverse --first-parent --format='%H%x1f%P%x1f%s%x1f%b%x1e' "${range[@]}")

  if [ -n "$prev" ]; then
    prev_base="$(git merge-base "$prev" upstream/main || true)"
    if [ -n "$prev_base" ] && [ "$prev_base" != "$upstream_base" ]; then
      other="- PatchMon code updated from $(describe_official "$prev_base") to $(describe_official "$upstream_base")"$'\n'"$other"
    fi
  fi

  [ -z "$new" ] || printf 'New:\n%s\n' "$new"
  [ -z "$fixes" ] || printf 'Fixes:\n%s\n' "$fixes"
  [ -z "$other" ] || printf 'Other changes:\n%s\n' "$other"
}

push=false
allow_unreleased=false
dry_run=false
for arg in "$@"; do
  case "$arg" in
    --push) push=true ;;
    --unreleased-base) allow_unreleased=true ;;
    --dry-run) dry_run=true ;;
    *) die "unknown option $arg (use --push, --unreleased-base, --dry-run)" ;;
  esac
done

cd "$(git rev-parse --show-toplevel)"
$dry_run || [ -z "$(git status --porcelain)" ] || die "the working tree has uncommitted changes"
git remote get-url upstream >/dev/null 2>&1 ||
  die "no 'upstream' remote; add it: git remote add upstream https://github.com/PatchMon/PatchMon.git"

# Official tags from upstream (kept local, never pushed to the fork), fork
# tags from origin.
git fetch -q --tags upstream
git fetch -q upstream main
git fetch -q --tags origin

$dry_run || git branch -r --contains HEAD | grep -q '^ *origin/' ||
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
upstream_desc="$(describe_official "$upstream_base")" ||
  die "no official release tag before the PatchMon commits merged into HEAD"

# The previous fork release in the history of HEAD, if any.
prev_tag="$(git describe --tags --abbrev=0 --match 'v*-fork.*' HEAD 2>/dev/null || true)"
notes="$(release_notes "$prev_tag" "$upstream_base")"
[ -z "$notes" ] || notes="$notes"$'\n\n'

message="${notes}PatchMon fork release vNEXT, based on PatchMon $upstream_desc"
if [ -n "$prev_tag" ]; then
  message="$message, with the changes since $prev_tag listed above"
fi
message="$message."
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
  message="$message

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
message="${message/vNEXT/${tag}}"

if $dry_run; then
  echo "Would tag $tag on $(git rev-parse --short HEAD), with this message:"
  echo
  printf '%s\n' "$message"
  exit 0
fi

# verbatim: the default cleanup drops lines that start with "#".
git tag -a "$tag" --cleanup=verbatim -m "$message"
echo "Tagged $tag on $(git rev-parse --short HEAD), based on PatchMon $upstream_desc."

if $push; then
  git push origin "$tag"
  echo "Pushed: the Docker workflow builds the image and publishes the release."
else
  echo "Push it to build the image and publish the release:"
  echo "  git push origin $tag"
fi
