#!/usr/bin/env python3
"""Run SDAutoLogin quietly and keep its one-time settings URL private."""

from __future__ import print_function

import os
import signal
import subprocess
import sys
from pathlib import Path
from urllib.parse import parse_qs, urlsplit


BASE = Path(__file__).resolve().parent.parent
ENTRY_DIR = BASE / "logs" / "runtime"
ENTRY_FILE = ENTRY_DIR / "settings-entry.url"
SETTINGS_PREFIX = b"settings page at "
XDG_DIRS = {
    "XDG_CONFIG_HOME": BASE / "logs" / "config",
    "XDG_DATA_HOME": BASE / "logs" / "data",
    "XDG_STATE_HOME": BASE / "logs" / "state",
    "XDG_CACHE_HOME": BASE / "logs" / "cache",
    "XDG_RUNTIME_DIR": BASE / "logs" / "runtime",
}


def private_directory(path):
    if path.is_symlink():
        raise OSError("private directory is a symbolic link")
    if path.exists() and not path.is_dir():
        raise OSError("private directory is not a directory")
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(str(path), 0o700)


def valid_settings_url(url):
    try:
        parsed = urlsplit(url)
        query = parse_qs(parsed.query, strict_parsing=True)
    except (TypeError, ValueError):
        return False
    return (
        parsed.scheme == "http"
        and parsed.hostname == "127.0.0.1"
        and parsed.port == 8088
        and parsed.path == "/"
        and not parsed.fragment
        and not parsed.username
        and not parsed.password
        and len(query.get("token", [])) == 1
        and bool(query["token"][0])
    )


def write_private_entry(url):
    if not valid_settings_url(url):
        return
    temporary = None
    try:
        import tempfile

        fd, name = tempfile.mkstemp(prefix=".settings-entry.", dir=str(ENTRY_DIR))
        temporary = Path(name)
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "wb") as output:
            output.write(url.encode("ascii") + b"\n")
            output.flush()
            os.fsync(output.fileno())
        os.replace(str(temporary), str(ENTRY_FILE))
        temporary = None
    except (OSError, UnicodeEncodeError):
        # The URL is intentionally never printed or written to another log.
        pass
    finally:
        if temporary is not None:
            try:
                temporary.unlink()
            except OSError:
                pass


def remove_entry():
    try:
        if ENTRY_FILE.is_symlink():
            return
        ENTRY_FILE.unlink()
    except FileNotFoundError:
        pass
    except OSError:
        pass


def url_from_stderr(line):
    line = line.rstrip(b"\r\n")
    if not line.startswith(SETTINGS_PREFIX):
        return None
    try:
        return line[len(SETTINGS_PREFIX):].decode("ascii")
    except UnicodeDecodeError:
        return None


def main():
    os.umask(0o077)
    try:
        private_directory(BASE)
        private_directory(BASE / "logs")
        private_directory(ENTRY_DIR)
        for name, path in XDG_DIRS.items():
            private_directory(path)
            os.environ[name] = str(path)
        temporary_dir = BASE / "logs" / "tmp"
        private_directory(temporary_dir)
        os.environ["TMPDIR"] = str(temporary_dir)
        os.environ["TMP"] = str(temporary_dir)
        os.environ["TEMP"] = str(temporary_dir)
        os.environ["HOME"] = str(BASE)
    except OSError:
        return 1

    remove_entry()
    executable = BASE / "SDAutoLogin"
    if not executable.is_file() or not os.access(str(executable), os.X_OK):
        return 127

    child = [None]

    def forward(signum, _frame):
        process = child[0]
        if process is not None and process.poll() is None:
            try:
                process.send_signal(signum)
            except OSError:
                pass

    for signum in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
        signal.signal(signum, forward)

    try:
        child[0] = subprocess.Popen(
            [
                str(executable),
                "run",
                "--home=" + str(BASE),
                "--quiet",
                "--settings=127.0.0.1:8088",
            ],
            stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.PIPE,
            close_fds=True,
        )
        for line in child[0].stderr:
            url = url_from_stderr(line)
            if url is not None:
                write_private_entry(url)
        return child[0].wait()
    except OSError:
        return 127
    finally:
        process = child[0]
        if process is not None:
            if process.poll() is None:
                try:
                    process.terminate()
                except OSError:
                    pass
                try:
                    process.wait()
                except OSError:
                    pass
            try:
                process.stderr.close()
            except (AttributeError, OSError):
                pass
        remove_entry()


if __name__ == "__main__":
    sys.exit(main())
