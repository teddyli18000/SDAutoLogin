# Changelog

Release notes for every version, newest first. Log files, commit messages and
this file's code stay English; the interface wording is bilingual.

**English** | [简体中文](CHANGELOG.zh-CN.md)

## Unreleased

What has landed since v1.0.0.

### Where there is no screen

A server, a container and a NAS have no tray and no display, and the setup wizard
needs somebody to answer it, so the program had no way in on those machines.

- `run` takes `--settings=ADDR` and serves the settings page on an address that
  was named, from the start and for as long as the process runs, rather than on
  the loopback server that is started on demand and shut down again when idle.
  The page can end a background run, and a warning is written when the address is
  reachable from the network, where the token in its URL is all the protection it
  has.
- `--username`, `--password`, `--isp` and `--save` write the account down without
  the wizard, which is what a provisioning script needs.
- `--home=DIR` keeps `config.json` and `logs/` outside the directory the
  executable was dropped in, so that directory can stay read only.
- `packaging/systemd` holds two units, one for a system service and one for
  `systemctl --user`, each opening with its installation steps, the provisioning
  step and the commands to follow the log. The system unit keeps the settings in
  `/var/lib/sdautologin` and cuts the process off from everything but the
  network. The user unit leaves the mount namespace options out on purpose,
  because a user manager needs unprivileged user namespaces for those, some
  kernels turn them off, and a unit that refuses to start over that is worse
  than one that is a little less confined.
- The `Dockerfile` builds the same static binary through the task runner, so the
  container gets the flags a local build gets, and from the vendored sources, so
  nothing is downloaded. It runs as an unprivileged user with `/data` for the
  settings and the log, and its health check is the exit code of `status`, which
  is the one thing worth noticing about such a container.

### Watching a session

- `status --json` prints the same check as one line, which is what a monitoring
  script reads. The log goes to stderr rather than stdout, so stdout carries only
  the result of the command.
- The exit code is the same in both forms: `0` online, `1` unknown or
  unreachable, `3` the portal says this client is not signed in. The JSON adds a
  state that tells the two that share an exit code apart.
- An option that was never defined is refused with exit code 2 instead of being
  passed on, since a misspelled one used to be ignored and a script would read a
  report it never asked for. The shortcuts and the flag of the config command are
  named so that a typo can be told apart from them, and the failure is written to
  the log as well, because the Windows build is linked as a GUI program and has
  nothing on screen to look at.

### A window of its own

- `--app[=ADDR]` opens the settings page in a browser window with no address bar,
  no tabs and no menus, so it looks like an application of its own while staying
  the very same page the browser opens on its own: no second copy of the form to
  keep in step, and no toolkit brought in for one window. The address is
  optional, and the option belongs to the tray mode, which is the one mode with a
  window.
- Which browsers can do this is not something the program can know, so the ones
  that usually can are tried in turn, and the ones that install into a known
  place are looked for there before the names on PATH. When none of them is
  found, or the one that was found refuses to start, the page goes to the default
  browser and the log says which of the two happened: a tab is worse than a
  window of its own, but far better than a program that appears to do nothing.
- The window is also opened when the tray icon could not be created, since the
  page does not depend on the tray.

### Building

- `go run ./tools/build all` produces the 32 bit builds and the small machine ones
  as well: `windows/386`, `linux/386`, `linux/armv7`, `linux/mips-softfloat` and
  `linux/mipsle-softfloat`, the last two for the routers, which have no floating
  point unit. The instruction set of the ARM build and the floating point unit of
  the MIPS ones are part of the file names, because neither `arm` nor `mips` on its
  own would say which boards they run on. Both byte orders of MIPS are there
  because the boards are.
- The Windows resource file is generated per architecture, for `amd64`, `arm64` and
  `386`, so every Windows build carries the icon and the version information rather
  than only the ones that happened to have a file.
- Every Linux target is built twice: the second file carries `-notray` in its name
  and leaves the tray, the D-Bus client behind it and everything else only a tray
  needs out of the binary, which is about a fifth of it. A router has no desktop
  session to put an icon in, and that build refuses `--app` instead of ignoring it.

### Tests

78 cases, up from 52.

- `internal/app` (4) — the settings server serves on the address it was given,
  refuses a request without the token, reports an address a browser can use, and
  knows what counts as loopback.
- `cmd/SDAutoLogin` (19) — both forms of every option, the address that is
  optional for `--app`, which mode refuses which option, that `--app` is refused in
  a build without a tray, help and version answering regardless of what else is on
  the command line, the way each status is named, the set of JSON fields, and an
  empty answer staying empty instead of becoming the dash the text form prints.
- `internal/platform` (3) — the command each system is started with, the order
  the browsers are preferred in, and the places each system looks in.

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
