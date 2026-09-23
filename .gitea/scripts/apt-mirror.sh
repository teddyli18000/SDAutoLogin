# Points the sources of the Debian image at the archive named by DEBIAN_MIRROR,
# and leaves them as they are when that mirror cannot be reached.
#
# This is a file rather than a function in the step that calls it because every
# step of a job is a shell of its own: a function defined in one step is gone by
# the next one, so a step that installs from the mirror sources this first.
#
# Whether the mirror answers is asked of the mirror itself rather than of
# apt-get: an index that could not be fetched at all is a warning there and the
# exit status stays 0, so a check on that status would pass on a runner that
# cannot reach the mirror and leave the install to fail with nothing to install
# from. The caller asks apt-get about the other way this can go wrong, a mirror
# that answers but carries no such suite, which is an error.

# The files that carry the sources of the image, for a caller that has to put
# them back.
aptSourcesFiles="/etc/apt/sources.list /etc/apt/sources.list.d/debian.sources"

pointAptAtMirror() {
  # The mirror is named as a host, so the archive is asked for on the port http
  # uses by default.
  if ! timeout 10 bash -c "exec 3<>/dev/tcp/${DEBIAN_MIRROR}/80" 2>/dev/null; then
    echo "the mirror $DEBIAN_MIRROR does not answer, installing from the image's own sources"
    return 0
  fi

  for f in $aptSourcesFiles; do
    [ -f "$f" ] || continue
    sed -i "s|deb.debian.org|$DEBIAN_MIRROR|g; s|security.debian.org|$DEBIAN_MIRROR|g" "$f"
  done
}
