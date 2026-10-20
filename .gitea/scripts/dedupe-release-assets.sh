#!/bin/sh
# Remove the older of the attachments a release carries under the same name: the
# newest of each name is kept and every older one is removed.
#
# This instance keeps an attachment uploaded twice under one name as two
# attachments and does not replace the first, so a tag that was pushed twice
# leaves the release with two of every file.
#
# Usage:
#   .gitea/scripts/dedupe-release-assets.sh <tag> [--dry-run|--apply]
#
# Nothing is removed without --apply, and a dry run is the same listing without
# it. The script prints, for each name, the attachment that is kept and the
# ones that go with their upload time and size. Removing needs a token:
#
#   GITEA_TOKEN=<token> .gitea/scripts/dedupe-release-assets.sh v1.1.0 --apply
#
# INSTANCE and REPO name the instance and the repository, and default to the ones
# this project is published on.
# pipefail as well as -e: most of what this reads goes through jq, and without it
# a curl that failed inside a pipeline is a pipeline that succeeded. That is how
# a run comes to report "nothing to remove" over a release it never read.
set -euo pipefail

instance=${INSTANCE:-https://git.zqlhub.com}
repo=${REPO:-zqlan/SDAutoLogin}
token=${GITEA_TOKEN:-}
tag=${1:-}
option=${2:-}

usage() {
  echo "usage: [GITEA_TOKEN=<token>] $0 <tag> [--dry-run|--apply]" >&2
}

case "$option" in
  "") apply=false ;;
  --dry-run) apply=false ;;
  --apply) apply=true ;;
  *)
    echo "unknown option: $option" >&2
    usage
    exit 2
    ;;
esac

if [ -z "$tag" ] || [ "${tag#-}" != "$tag" ]; then
  usage
  exit 2
fi
if [ -n "${3:-}" ]; then
  echo "unexpected argument: $3" >&2
  usage
  exit 2
fi
if [ "$apply" = true ] && [ -z "$token" ]; then
  echo "GITEA_TOKEN is not set: removing an attachment needs a token that may write to $repo" >&2
  exit 2
fi

# The token goes into a file only this user can read, and curl is told to read
# the header out of it: a token on a command line is readable by every user of
# the machine in /proc/*/cmdline.
auth=""
if [ -n "$token" ]; then
  header=$(mktemp)
  chmod 600 "$header"
  trap 'rm -f "$header"' EXIT
  printf 'Authorization: token %s\n' "$token" > "$header"
  auth="@$header"
fi

# No token is sent when there is none: that is enough to read the release of a
# repository anyone can read, and is what a dry run needs. A curl that failed is
# a failure here rather than an empty answer read as "there is no such release".
if ! release=$(if [ -n "$auth" ]; then
  curl -sS -H "$auth" "$instance/api/v1/repos/$repo/releases/tags/$tag"
else
  curl -sS "$instance/api/v1/repos/$repo/releases/tags/$tag"
fi); then
  echo "the release of $tag could not be read from $instance: check the address and the network" >&2
  exit 1
fi

id=$(printf '%s' "$release" | jq -r '.id // empty')
if [ -z "$id" ]; then
  echo "there is no release of $tag in $repo:" >&2
  printf '%s' "$release" | jq -r '"  " + (.message // "the answer did not name a release")' >&2
  exit 1
fi

# How many attachments the release carries comes first: none is the ordinary
# answer, and an answer that is not a number is a reply that could not be read,
# which must not be reported as "nothing to remove" and a success.
attached=$(printf '%s' "$release" | jq -r '.assets | length')
case "$attached" in
  '' | *[!0-9]*)
    echo "$tag: the answer of $instance could not be read as a release: nothing was removed" >&2
    exit 1
    ;;
  0)
    echo "$tag carries no attachments, nothing to do"
    exit 0
    ;;
esac

# One line per attachment, grouped by name and newest first within the group, so
# the first line of each group is the one that is kept. The line carries the
# name, the upload time and the size that the choice between two files turns on.
# jq is asked to fail rather than to answer nothing when the reply is not the
# shape this reads.
if ! plan=$(printf '%s' "$release" | jq -er '
  .assets
  | group_by(.name)
  | map(sort_by(.created_at) | reverse)
  | .[]
  | (.[0].id) as $newest
  | .[]
  | "\(if .id == $newest then "keep" else "trim" end) \(.id) \(.name) \(.created_at) \(.size)"'); then
  echo "$tag: the attachments of the release could not be listed: nothing was removed" >&2
  exit 1
fi

names=$(printf '%s' "$release" | jq -r '[.assets[].name] | unique | length')
trimming=$(printf '%s' "$release" | jq -r '[.assets | group_by(.name)[] | length - 1] | add // 0')
for count in "$names" "$trimming"; do
  case "$count" in
    '' | *[!0-9]*)
      echo "$tag: the release could not be counted ($names name(s), $trimming to remove): nothing was removed" >&2
      exit 1
      ;;
  esac
done

if [ "$trimming" = "0" ]; then
  echo "$tag carries one attachment per name ($attached in $names name(s)), nothing to remove"
  exit 0
fi

if [ "$apply" = true ]; then
  echo "$tag: removing $trimming of $attached attachment(s), keeping the newest of each of the $names name(s)"
else
  echo "$tag: would remove $trimming of $attached attachment(s), keeping the newest of each of the $names name(s)"
fi

removed=0
failed=0
while read -r action asset name created size; do
  case "$action" in
    keep)
      echo "  $name"
      echo "    keep   #$asset  uploaded $created  $size bytes"
      ;;
    trim)
      if [ "$apply" = false ]; then
        echo "    remove #$asset  uploaded $created  $size bytes"
        continue
      fi
      code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE \
        -H "$auth" \
        "$instance/api/v1/repos/$repo/releases/$id/assets/$asset")
      case "$code" in
        200|204) removed=$((removed + 1)); echo "    removed #$asset  uploaded $created  $size bytes" ;;
        *) failed=$((failed + 1)); echo "    could not remove #$asset  uploaded $created: HTTP $code" >&2 ;;
      esac
      ;;
  esac
done <<PLAN
$plan
PLAN

if [ "$apply" = true ] && [ "$failed" != "0" ]; then
  # A removal that did not happen is a failure of this run: leaving it as a line
  # of output over an exit status of 0 is how a release keeps two of a file and
  # nobody is told.
  echo "removed $removed attachment(s), $failed could not be removed" >&2
  exit 1
elif [ "$apply" = true ]; then
  echo "removed $removed attachment(s), $failed could not be removed"
else
  echo "nothing was removed: run it again with --apply to remove the lines marked remove"
fi
