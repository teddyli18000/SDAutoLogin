# Points the sources of the Debian image at the archive named by DEBIAN_MIRROR,
# and leaves them as they are when that mirror cannot be reached.
#
# This is a file and not a function in the calling step, because every step of a
# job is a shell of its own.
#
# Whether the mirror answers is asked of the mirror: apt-get reports an index it
# cannot fetch as a warning and still exits 0, so a check on that status would
# pass on a runner that cannot reach the mirror. The caller asks apt-get about
# the other failure, a mirror that carries no such suite.

# The files that carry the sources of the image, for a caller that has to put
# them back.
aptSourcesFiles="/etc/apt/sources.list /etc/apt/sources.list.d/debian.sources"

pointAptAtMirror() {
  # The mirror has to be a host name and nothing else. It is written into a
  # command string below and into a sed replacement, so a value carrying a
  # semicolon would run a command of its own on the runner and one carrying an
  # ampersand or a pipe would rewrite the sources into something else. A name
  # that is not one of these characters is refused rather than escaped: there is
  # no such mirror to point at anyway.
  mirror=${DEBIAN_MIRROR:-}
  case "$mirror" in
    "" | *[!A-Za-z0-9.-]*)
      echo "DEBIAN_MIRROR is not a host name: '$mirror'" >&2
      return 1
      ;;
  esac

  # The mirror is named as a host, so the archive is asked for on the port http
  # uses by default. The name goes in as a positional parameter rather than into
  # the command string, so nothing in it is read as shell syntax.
  if ! timeout 10 bash -c 'exec 3<>/dev/tcp/"$1"/80' _ "$mirror" 2>/dev/null; then
    echo "the mirror $mirror does not answer, installing from the image's own sources"
    return 0
  fi

  for f in $aptSourcesFiles; do
    [ -f "$f" ] || continue
    # The value is a host name by the check above: it carries no & or | for the
    # replacement to read as anything but itself.
    sed -i "s|deb.debian.org|$mirror|g; s|security.debian.org|$mirror|g" "$f"
  done
}
