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
# Open the page at the address the program prints on its console, which
# "docker logs sdautologin" shows, since that is where a container's output goes:
#   docker logs sdautologin 2>&1 | grep 'settings page'
# The address carries a one-time token and is replaced after every opening, so
# take the newest line; the bare port will not open it. The token is deliberately
# not written to logs/app.log, which outlives it.
#
# Binding the page to 0.0.0.0 makes it reachable from the network, so only do
# that on a trusted network: it holds the account in clear text.

# The build goes through the same task runner the scripts and the CMake targets
# use. Dependencies are vendored and the repository metadata stays out of the
# context, so the version reported is the one the sources carry.
#
# What is copied is named here rather than taken as the whole context. A context
# is copied into a layer, and a layer is readable by anyone who can pull the
# image: a signing key that happens to be in the working tree would go in with
# everything else. The list is what the build reads — the module and its vendored
# dependencies, the sources, the task runner and the resources it links.
FROM golang:1.26 AS build

WORKDIR /src
COPY go.mod go.sum ./
COPY vendor/ vendor/
COPY cmd/ cmd/
COPY internal/ internal/
COPY tools/ tools/
COPY winres/ winres/
COPY LICENSE README.md README.zh-CN.md CHANGELOG.md CHANGELOG.zh-CN.md ./
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

# --home belongs in ENTRYPOINT, so that a one-off command keeps the same data
# directory as the service:
#   docker run --rm sdautologin status --json
ENTRYPOINT ["/usr/local/bin/SDAutoLogin", "--home=/data"]
CMD ["run"]

# A container that is up but not signed in is the failure this program exists to
# prevent, so the health check is the status command and its exit code: 0 online,
# 1 unknown, 3 the portal says this client is not signed in.
HEALTHCHECK --interval=60s --timeout=15s --start-period=10s --retries=3 \
	CMD ["/usr/local/bin/SDAutoLogin", "--home=/data", "status"]
