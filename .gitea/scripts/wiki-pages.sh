#!/bin/sh
# Write the wiki pages from the files in the repository.
#
# The wiki is a repository of its own, and a page there is a file at its root:
# this writes them into a directory, which the workflow then commits and pushes.
#
# The manual, the deployment guide and the changelog come from the files the
# repository already holds, in both languages, and the landing page and the
# sidebar from docs/wiki/.
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
	"docs/DEPLOYMENT.md" "docs/DEPLOYMENT.zh-CN.md" \
	"docs/wiki/Home.md" "docs/wiki/Home.zh-CN.md" "docs/wiki/_Sidebar.md" \
	"logo.png"; do
  if [ ! -f "$src/$page" ]; then
    echo "$src/$page is missing: a wiki page is written from it" >&2
    exit 1
  fi
done

# mkdir -p follows a symbolic link, so a page directory that is one would have
# every page below written through it into wherever it points. Nothing here
# needs that, and the directory is named by the caller: a link is refused rather
# than followed.
if [ -L "$dst" ]; then
  echo "$dst is a symbolic link: refusing to write the pages through it" >&2
  exit 1
fi
mkdir -p "$dst"
cp "$src/docs/wiki/Home.md" "$dst/Home.md"
cp "$src/docs/wiki/Home.zh-CN.md" "$dst/Home.zh-CN.md"
cp "$src/docs/wiki/_Sidebar.md" "$dst/_Sidebar.md"
# The mark the manual shows. A page here is a file at the root of the wiki, so
# the image is copied to the root as well and the name in the manual is already
# the one it has there.
cp "$src/logo.png" "$dst/logo.png"

# A relative link in the manual points at a file in the repository, while the
# page carrying it lives in the wiki, so the target becomes the address of that
# file on the default branch. A link to a file that has a page of its own here
# (the manual, the deployment guide and the changelog, in either language), and
# the image that is copied in beside them, keep the name they have; one that is
# already absolute, or points at a heading on the page itself, is left alone.
page() {
  awk -v base="$instance/$repo/blob/main/" '
    BEGIN {
      own["README.md"] = "Manual"
      own["README.zh-CN.md"] = "Manual.zh-CN"
      own["CHANGELOG.md"] = "Changelog"
      own["CHANGELOG.zh-CN.md"] = "Changelog.zh-CN"
      own["docs/DEPLOYMENT.md"] = "Deployment"
      own["docs/DEPLOYMENT.zh-CN.md"] = "Deployment.zh-CN"
      own["logo.png"] = "logo.png"
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
page "$src/docs/DEPLOYMENT.md" > "$dst/Deployment.md"
page "$src/docs/DEPLOYMENT.zh-CN.md" > "$dst/Deployment.zh-CN.md"

echo "wrote $(find "$dst" -maxdepth 1 -type f | wc -l) file(s) into $dst"
