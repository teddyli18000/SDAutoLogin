#!/bin/sh
# Write the wiki pages from the files in the repository.
#
# The wiki is a repository of its own, and a page there is a file at its root:
# this writes them into a directory, which the workflow then commits and pushes.
#
# The manual and the changelog are what the repository already holds, in both
# languages, with their relative links pointed at the repository - a page that is
# no longer in the repository has nothing to resolve them against. The landing
# page and the sidebar are for the wiki alone and live under docs/wiki/.
#
# Usage:
#   [INSTANCE=<url>] [REPO=<owner/name>] .gitea/scripts/wiki-pages.sh <source root> <page directory>
#
# INSTANCE and REPO name the instance and the repository, and default to the ones
# this project is published on: they are what a relative link is rewritten to.
set -eu

instance=${INSTANCE:-https://git.zqlhub.com}
repo=${REPO:-zqlan/SDAutoLogin}
src=${1:-}
dst=${2:-}

usage() {
  echo "usage: [INSTANCE=<url>] [REPO=<owner/name>] $0 <source root> <page directory>" >&2
}

if [ -z "$src" ] || [ -z "$dst" ]; then
  usage
  exit 2
fi

for page in "README.md" "README.zh-CN.md" "CHANGELOG.md" "CHANGELOG.zh-CN.md" \
	"docs/wiki/Home.md" "docs/wiki/Home.zh-CN.md" "docs/wiki/_Sidebar.md"; do
  if [ ! -f "$src/$page" ]; then
    echo "$src/$page is missing: a wiki page is written from it" >&2
    exit 1
  fi
done

mkdir -p "$dst"
cp "$src/docs/wiki/Home.md" "$dst/Home.md"
cp "$src/docs/wiki/Home.zh-CN.md" "$dst/Home.zh-CN.md"
cp "$src/docs/wiki/_Sidebar.md" "$dst/_Sidebar.md"

# A relative link in the manual points at a file in the repository, and the page
# that carries it is not in the repository any more: the target becomes the
# address of that file on the default branch. Two kinds are not that: a link to
# one of the files that has a page of its own here - the manual and the changelog
# in either language, which is how a reader switches between them - becomes that
# page, and a link that is already absolute, or one to a heading on the page
# itself, is left alone.
page() {
  awk -v base="$instance/$repo/blob/main/" '
    BEGIN {
      own["README.md"] = "Manual"
      own["README.zh-CN.md"] = "Manual.zh-CN"
      own["CHANGELOG.md"] = "Changelog"
      own["CHANGELOG.zh-CN.md"] = "Changelog.zh-CN"
    }
    {
      line = $0
      out = ""
      while (match(line, /\]\([^)]+\)/)) {
        target = substr(line, RSTART + 2, RLENGTH - 3)
        if (target in own) {
          target = own[target]
        } else if (target !~ /:\/\// && target !~ /^#/ && target !~ /^\//) {
          target = base target
        }
        out = out substr(line, 1, RSTART - 1) "](" target ")"
        line = substr(line, RSTART + RLENGTH)
      }
      print out line
    }' "$1"
}

page "$src/README.md" > "$dst/Manual.md"
page "$src/README.zh-CN.md" > "$dst/Manual.zh-CN.md"
page "$src/CHANGELOG.md" > "$dst/Changelog.md"
page "$src/CHANGELOG.zh-CN.md" > "$dst/Changelog.zh-CN.md"

echo "wrote $(find "$dst" -maxdepth 1 -type f | wc -l) page(s) into $dst"
