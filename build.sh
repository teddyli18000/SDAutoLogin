#!/usr/bin/env bash
# Build the executable for the current platform. The result is a single file
# with no runtime dependency.
#
# Cross compiling:
#   GOOS=windows GOARCH=amd64 ./build.sh     Linux with mingw is not required
#   GOOS=linux   GOARCH=arm64 ./build.sh
# macOS builds need cgo and therefore the Xcode command line tools, so they
# cannot be cross compiled from another OS.
set -e
cd "$(dirname "$0")"

echo "[1/2] go vet ..."
go vet ./...

echo "[2/2] go build ..."
go build -trimpath -ldflags "-s -w" -o SDAutoLogin .

echo
echo "Built: $(pwd)/SDAutoLogin"
