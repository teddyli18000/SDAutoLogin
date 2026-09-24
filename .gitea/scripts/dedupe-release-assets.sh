#!/bin/sh
# Remove the older of the attachments a release carries under the same name.
#
# This instance keeps an attachment that is uploaded twice under one name as two
# attachments rather than replacing the first, and the release step used to upload
# every file on every run, so a tag that was pushed twice left the release with two
# of everything. The step replaces by name now; this is for a release that was
# already doubled.
#
# Of the attachments sharing a name the newest is kept, which is the one the last
# run of the release step put there, and every older one is removed.
#
# Usage:
#   .gitea/scripts/dedupe-release-assets.sh <tag> [--dry-run|--apply]
#
# Nothing is removed without --apply, and --dry-run is that same reading with the
# name said out loud. Either way it goes through the release name by name and
# prints, for each name, the attachment that is kept and the ones that go with
# their upload time and size. Removing needs a token:
#
#   GITEA_TOKEN=<token> .gitea/scripts/dedupe-release-assets.sh v1.1.0 --apply
#
# INSTANCE and REPO name the instance and the repository, and default to the ones
# this project is published on.
set -eu

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

# No token is sent when there is none, which is enough to read the release of a
# repository anyone can read, and is what a dry run needs.
release=$(if [ -n "$token" ]; then
  curl -sS -H "Authorization: token $token" "$instance/api/v1/repos/$repo/releases/tags/$tag"
else
  curl -sS "$instance/api/v1/repos/$repo/releases/tags/$tag"
fi)

id=$(printf '%s' "$release" | jq -r '.id // empty')
if [ -z "$id" ]; then
  echo "there is no release of $tag in $repo:" >&2
  printf '%s' "$release" | jq -r '"  " + (.message // "the answer did not name a release")' >&2
  exit 1
fi

# One line per attachment, grouped by name and newest first within the group, so
# the first line of each group is the one that is kept. Everything the listing
# says about an attachment is on its line, because that is what the reader has to
# decide on: which of two files of the same name is the one the tag points at.
plan=$(printf '%s' "$release" | jq -r '
  .assets
  | group_by(.name)
  | map(sort_by(.created_at) | reverse)
  | .[]
  | (.[0].id) as $newest
  | .[]
  | "\(if .id == $newest then "keep" else "trim" end) \(.id) \(.name) \(.created_at) \(.size)"')

if [ -z "$plan" ]; then
  echo "$tag carries no attachments, nothing to do"
  exit 0
fi

attached=$(printf '%s' "$release" | jq -r '.assets | length')
names=$(printf '%s' "$release" | jq -r '[.assets[].name] | unique | length')
trimming=$(printf '%s' "$release" | jq -r '[.assets | group_by(.name)[] | length - 1] | add // 0')

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
        -H "Authorization: token $token" \
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

if [ "$apply" = true ]; then
  echo "removed $removed attachment(s), $failed could not be removed"
else
  echo "nothing was removed: run it again with --apply to remove the lines marked remove"
fi
