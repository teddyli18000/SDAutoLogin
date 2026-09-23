# SDAutoLogin in a container, for a server or a NAS where nothing is installed
# on the host. Build and run:
#
#   docker build -t sdautologin .
#   docker run -d --name sdautologin --restart unless-stopped \
#       -e TZ=Asia/Shanghai -v sdautologin:/data sdautologin
#   docker exec sdautologin SDAutoLogin --home=/data status --json
#
# The account is written down once, either with --save on a one-off run, or from
# the settings page by adding --settings=0.0.0.0:8088 to the port publishing
# and to the command:
#
#   docker run -d ... -p 8088:8088 sdautologin run --settings=0.0.0.0:8088
#
# The page is then opened with the address "docker logs sdautologin" prints,
# token and all, and not with the bare port: the address carries a one time
# token, it opens the page once, and the newest line is the one that works.
#
# Binding the page to 0.0.0.0 makes it reachable from the network, so only do
# that on a trusted network: it holds the account in clear text.

# The build goes through the same task runner the scripts and the CMake targets
# use, so the container gets exactly the flags a local build gets. The
# dependencies are vendored, so nothing is downloaded. The repository metadata is
# kept out of the context, so this build has no tag to read the version from and
# the version it reports is the one the sources carry.
FROM golang:1.26 AS build

WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go run ./tools/build build

# The program is one static binary, so the image only needs the data a Go
# program may still read at run time: the certificate authorities for an HTTPS
# portal, and the time zone database so the log timestamps match the host.
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata \
 && install -d -o 65534 -g 65534 -m 0700 /data

COPY --from=build /src/dist/SDAutoLogin /usr/local/bin/SDAutoLogin

# config.json and logs/ live in /data, owned by the account the program runs as,
# and the directory belongs to that account alone: the settings in it are an
# account. A named volume inherits that ownership, a bind mount does not: a host
# directory has to be chowned to 65534:65534 first.
VOLUME ["/data"]
USER 65534:65534
WORKDIR /data

# --home belongs in ENTRYPOINT rather than CMD, so that a one-off command keeps
# the same data directory as the service:
#   docker run --rm sdautologin status --json
ENTRYPOINT ["/usr/local/bin/SDAutoLogin", "--home=/data"]
CMD ["run"]

# A container that is up but not signed in is the failure this program exists to
# prevent, so the health check is the status command and its exit code: 0 online,
# 1 unknown, 3 the portal says this client is not signed in.
HEALTHCHECK --interval=60s --timeout=15s --start-period=10s --retries=3 \
	CMD ["/usr/local/bin/SDAutoLogin", "--home=/data", "status"]
