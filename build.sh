#!/usr/bin/env bash
# Build the executable for this platform through the task runner, so the flags
# live in one place. See "go run ./tools/build" for every task.
#
# macOS builds need cgo and the Xcode command line tools, they cannot be cross
# compiled from another system.
set -e
cd "$(dirname "$0")"

go run ./tools/build build

echo
echo "Built: $(pwd)/dist/SDAutoLogin"
