#!/usr/bin/env bash
# Exercise the shipped installer in an isolated home with a fake service manager.
set -euo pipefail
test_root=$(mktemp -d "$RUNNER_TEMP/sdautologin-install.XXXXXX")
trap 'rm -rf "$test_root"' EXIT
mkdir -p "$test_root/package" "$test_root/home/桌面" "$test_root/bin"
archive=$(find dist/release-assets -name '*-linux-amd64-portable.tar.gz' -print -quit)
test -n "$archive"
tar -xzf "$archive" -C "$test_root/package" --strip-components=1
printf '#!/bin/sh\nexit 0\n' > "$test_root/bin/systemctl"
printf '#!/bin/sh\nprintf "%%s\\n" "$HOME/桌面"\n' > "$test_root/bin/xdg-user-dir"
chmod +x "$test_root/bin/"*
export HOME="$test_root/home"
export XDG_CONFIG_HOME="$HOME/.config"
export PATH="$test_root/bin:$PATH"
python3 "$test_root/package/install.py"
python3 - <<'PY'
import json
import os
from pathlib import Path
root = Path.home() / "桌面" / "SDAutoLogin"
assert (root / "配置面板.desktop").is_file(), "Visible panel launcher missing"
assert (root / "SDAutoLogin").is_file()
assert os.access(root / "SDAutoLogin", os.X_OK)
assert (root / "LICENSE").is_file()
assert (root / "README.zh-CN.md").is_file()
assert (Path(os.environ["XDG_CONFIG_HOME"]) / "systemd/user/sdautologin.service").resolve() == root / ".startup/sdautologin.service"
config = json.loads((root / "config.json").read_text())
assert not config["accounts"]
assert not config["checkForUpdates"]
assert (root / "config.json").stat().st_mode & 0o777 == 0o600
config["language"] = "en"
(root / "config.json").write_text(json.dumps(config))
PY
python3 "$test_root/package/install.py"
python3 - <<'PY'
import json
from pathlib import Path
assert json.loads((Path.home() / "桌面/SDAutoLogin/config.json").read_text())["language"] == "en", "Existing config was replaced"
PY
docker run --rm --network none --user "$(id -u):$(id -g)" \
  -v "$HOME/桌面/SDAutoLogin:/app" \
  -v "$GITHUB_WORKSPACE/.github/scripts/check-runtime.py:/check-runtime.py:ro" \
  python:3.6-slim python /check-runtime.py
