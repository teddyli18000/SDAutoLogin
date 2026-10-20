#!/usr/bin/env python3
"""Open the settings page, consuming its one-time address after dispatch."""

from __future__ import print_function

import os
import stat
import subprocess
import sys
from pathlib import Path
from urllib.parse import parse_qs, urlsplit


BASE = Path(__file__).resolve().parent.parent
ENTRY_FILE = BASE / "logs" / "runtime" / "settings-entry.url"
PLAIN_URL = "http://127.0.0.1:8088/"


def valid_url(url, token_required):
    try:
        parsed = urlsplit(url)
        query = parse_qs(parsed.query, strict_parsing=token_required)
    except (TypeError, ValueError):
        return False
    if (
        parsed.scheme != "http"
        or parsed.hostname != "127.0.0.1"
        or parsed.port != 8088
        or parsed.path != "/"
        or parsed.fragment
        or parsed.username
        or parsed.password
    ):
        return False
    if token_required:
        return len(query.get("token", [])) == 1 and bool(query["token"][0])
    return not parsed.query


def read_entry():
    try:
        info = ENTRY_FILE.lstat()
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid():
            return None, None
        if stat.S_IMODE(info.st_mode) & 0o077:
            return None, None
        data = ENTRY_FILE.read_bytes()
        url = data.rstrip(b"\r\n").decode("ascii")
    except (OSError, UnicodeDecodeError):
        return None, None
    if not valid_url(url, True):
        return None, None
    return data, url


def consume_if_unchanged(data):
    if data is None:
        return
    try:
        current = ENTRY_FILE.lstat()
        if not stat.S_ISREG(current.st_mode) or current.st_uid != os.geteuid():
            return
        if stat.S_IMODE(current.st_mode) & 0o077:
            return
        if ENTRY_FILE.read_bytes() != data:
            return
        ENTRY_FILE.unlink()
    except OSError:
        pass


def main():
    data, url = read_entry()
    if url is None:
        url = PLAIN_URL
    if not valid_url(url, data is not None):
        return 1
    try:
        result = subprocess.call(
            ["xdg-open", url],
            stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
    except OSError:
        return 1
    if result == 0:
        consume_if_unchanged(data)
        return 0
    return 1


if __name__ == "__main__":
    sys.exit(main())
