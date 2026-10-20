#!/usr/bin/env bash
# Package an already-built notray executable for an Ubuntu 18.04 desktop.
# Usage: build.sh BINARY amd64|arm64 VERSION OUTPUT_DIR
set -euo pipefail

if [[ $# -ne 4 ]]; then
    echo "usage: $0 BINARY amd64|arm64 VERSION OUTPUT_DIR" >&2
    exit 2
fi

binary=$1
arch=$2
version=$3
output_dir=$4
case "$arch" in
    amd64|arm64) ;;
    *) echo "unsupported architecture: $arch" >&2; exit 2 ;;
esac
if [[ ! "$version" =~ ^[0-9A-Za-z][0-9A-Za-z.+_-]*$ ]]; then
    echo "version must be a simple version string: $version" >&2
    exit 2
fi
if [[ ! -f "$binary" ]]; then
    echo "binary not found: $binary" >&2
    exit 1
fi

repo_root=$(cd "$(dirname "$0")/../.." && pwd -P)
mkdir -p "$output_dir"
output_dir=$(cd "$output_dir" && pwd -P)
archive="$output_dir/SDAutoLogin-${version}-linux-${arch}-portable.tar.gz"
staging=$(mktemp -d "$output_dir/.portable-linux.XXXXXX")
trap 'rm -rf "$staging"' EXIT

package_name=$(basename "$archive" .tar.gz)
package_root="$staging/$package_name"
app_root="$package_root/SDAutoLogin"
mkdir -p "$app_root/.startup"
install -m 0755 "$binary" "$app_root/SDAutoLogin"
install -m 0755 "$repo_root/packaging/portable-linux/run-with-panel.py" "$app_root/.startup/run-with-panel.py"
install -m 0755 "$repo_root/packaging/portable-linux/open-panel.py" "$app_root/.startup/open-panel.py"
install -m 0644 "$repo_root/packaging/portable-linux/config.json.in" "$app_root/.startup/config.json.in"
install -m 0644 "$repo_root/packaging/portable-linux/sdautologin.service.in" "$app_root/.startup/sdautologin.service.in"
install -m 0644 "$repo_root/packaging/portable-linux/配置面板.desktop.in" "$app_root/.startup/配置面板.desktop.in"
install -m 0644 "$repo_root/LICENSE" "$package_root/LICENSE"
install -m 0644 "$repo_root/docs/LINUX_PORTABLE.zh-CN.md" "$package_root/README.zh-CN.md"
install -m 0644 "$repo_root/packaging/portable-linux/install.py" "$package_root/install.py"

tar -czf "$archive" \
    --exclude='__pycache__' \
    --exclude='*.pyc' \
    -C "$staging" "$package_name"
printf 'Built: %s\n' "$archive"
