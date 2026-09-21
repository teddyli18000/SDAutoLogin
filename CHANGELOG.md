# Changelog

Release notes for every version, newest first. Log files, commit messages and
this file's code stay English; the interface wording is bilingual.

**English** | [简体中文](CHANGELOG.zh-CN.md)

## v1.0.0 — 2026-09-21

First release. A tray program that keeps a campus network (Dr.COM eportal)
session alive: it polls the portal and signs back in with the stored account as
soon as the session is gone. It never logs the session out.

This release is the result of a round of restructuring: the layout, the build,
the tray and single instance handling, and the first tests. The version reads
1.0.0 on the command line, on the settings page, and in the Windows version
resource.

### Layout and build

- Everything the build produces now lands in `dist/`. The generated resource
  file `rsrc_windows_amd64.syso` moved out of the repository root into `winres/`,
  next to the icon and `winres.json` it is compiled from, so the root holds
  sources only.
- The single `main` package of 29 files in the root was split into
  `cmd/SDAutoLogin` for the entry point and ten packages under `internal/` (app,
  brand, config, i18n, logging, platform, portal, service, tray, webui).
  Dependencies now run one way: logging, i18n and brand at the bottom, then
  config and platform, then portal, then service, then app, then tray. Nothing
  outside the module can import them, so the public surface is the command line
  and the settings page.
- The cleaning task removes only the executables. Settings and logs live in the
  same directory as the executable, so cleaning a build could previously have
  thrown an account away.
- The task runner builds `./cmd/SDAutoLogin`. The wrapper scripts and the CMake
  targets still go through that one runner, so the three ways in cannot drift
  apart.

### Tray and single instance

- A double click on the tray icon opened the settings page twice: Windows sends
  down, up, double click, up, and both ups were handled. A repeat inside the
  double click interval Windows itself reports is now treated as one click.
- A second copy of the program asks the running one for its settings page
  through a named event, but only if the tray had been created successfully. The
  waiter is now independent of the tray: it starts before the icon exists, and
  when no message loop is running it opens the page itself, which needs no tray.
  In the background `run` mode, which serves no page, the request is written to
  the log instead of vanishing silently.
- Half built tray state is cleared when the icon cannot be created, so nothing
  can post messages to a zero window.
- The log is opened before the console is attached, so a console that cannot be
  attached is recorded rather than lost.

### Tests

There were no tests at all, so nothing guarded the behaviour this program is
judged by: signing back in after a disconnection. There are 52 cases now.

- `internal/config` (14) — the defaults, the UTF-8 BOM some Windows editors add,
  what `normalize` clamps, every field `Validate` reports, and probe cleanup. The
  password stays out of both the file and the log summary.
- `internal/portal` (20) — an online and an offline session, both result formats
  the portal uses, a reply without a result field, an error status, and an
  intercepted redirect that must not be followed. The login parameters, the
  `ret_code` wording, and the fact that no logout is ever requested are pinned
  down.
- `internal/service` (14) — every reconnect path: a login that helps, one the
  portal accepts while still reporting the client offline, one it rejects, an
  unreadable reply that must not trigger a reconnect, auto login switched off, a
  missing account, and an unreachable portal.
- `internal/i18n` (4) — every session state has wording in both languages,
  reachable through the same lookup the tray uses, and no `state.` key names a
  state that does not exist.

Removing one wording entry makes all four i18n checks fail, so they are not
vacuous.

### Documentation

Both READMEs describe the new layout, the entry point, and where the build
writes its files.

### Building

```sh
build.cmd                       # Windows, ./build.sh elsewhere
go run ./tools/build            # lists every task
cmake -S . -B build -G Ninja    # the same tasks through CMake
```

Cross compiling covers Windows and Linux. The macOS tray needs cgo, so that
build has to be made on macOS with the Xcode command line tools installed.

### Known limitations

- On Linux and macOS the single instance lock is a file in the temporary
  directory. If the system removes it while the program runs, a second copy
  could start. This was not reproduced, only reasoned about.
- If the browser cannot be opened, the settings page reports it with a modal
  message box on the UI thread, so the tray does not answer until it is
  dismissed.
