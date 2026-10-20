"""Exercise the real settings runner without external network access."""
import json
import os
import subprocess
import sys
import time
import urllib.request
from pathlib import Path

root = Path("/app")
entry = root / "logs/runtime/settings-entry.url"
config = json.loads((root / "config.json").read_text())
config["autoLogin"] = False
(root / "config.json").write_text(json.dumps(config))
process = subprocess.Popen([sys.executable, str(root / ".startup/run-with-panel.py")])
try:
    for _ in range(200):
        assert process.poll() is None, "Settings runner exited before readiness"
        if entry.is_file():
            break
        time.sleep(0.1)
    assert entry.is_file(), "Settings entry was not captured"
    assert entry.stat().st_mode & 0o777 == 0o600
    # Do not echo the private URL, even when a request fails.
    try:
        with urllib.request.urlopen(entry.read_text().strip(), timeout=5) as response:
            assert response.status == 200
    except Exception:
        raise AssertionError("Settings page request failed") from None
finally:
    process.terminate()
    process.wait(timeout=10)
assert not entry.exists(), "Settings entry survived service shutdown"
print("Python 3.6 settings runner and private entry lifecycle: OK")
