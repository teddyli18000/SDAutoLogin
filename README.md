# Campus Auto Login

A small tray program that keeps a campus network session alive. It polls the
Dr.COM eportal every few seconds, and as soon as the session is gone it signs
back in with the stored account. Nothing else is touched: the program never
logs the session out.

**English** | [简体中文](README.zh-CN.md)

The result is one self-contained executable per platform with no runtime
dependency, so it can be copied to another machine and started by double click.

## Contents

- [Features](#features)
- [How it works](#how-it-works)
- [Build](#build)
- [Usage](#usage)
- [Settings](#settings)
- [Command line](#command-line)
- [Configuration file](#configuration-file)
- [Files](#files)
- [Deployment](#deployment)
- [Interface language](#interface-language)
- [Security notes](#security-notes)
- [Project layout](#project-layout)
- [Troubleshooting](#troubleshooting)

## Features

- Tray icon on Windows, Linux and macOS, drawn by the program itself; its
  colour follows the session state.
- The settings page is a local web page served on loopback and opened in the
  default browser, so no GUI toolkit is linked in and the window looks the
  same on every platform.
- Interface in Chinese or English, following the system language by default.
  Log files stay English.
- Options are split into General and Advanced, so the everyday switches
  (account, interval, probes) are not mixed up with the portal endpoints.
- A separate command line mode for machines without a desktop.
- Balloon tip when the session was signed back in, or when signing in failed.
- Run at startup, one switch per platform (registry, desktop entry or
  LaunchAgent).
- Detailed per-check logging with rotation, which is what makes a dropped
  session diagnosable afterwards. The password is never written to the log.

## How it works

The program talks to the same endpoints the portal's own login page uses:

| Step | Request | Purpose |
| --- | --- | --- |
| 1 | `GET /` | read `v4ip` from the portal home page, which is the IP the portal sees (after NAT, not the local one) |
| 2 | `GET ?c=Portal&a=online_list` | ask whether that IP is authenticated. This endpoint answers from inside the campus network even while the session is down |
| 3 | `GET ?c=Portal&a=login` | submit `user_account` and `user_password` with `login_method=1` |

A login is only attempted when step 2 answered that the portal does *not*
consider the client online. If the portal cannot be reached, or the reply is
not usable, the state is treated as unknown and no login is attempted, which
avoids hammering the portal with pointless requests during a network outage.
After a login the program asks again, so "accepted but still offline" is
reported as a failure instead of a success.

Optionally the program also probes a few public addresses. That result only
feeds the status display; the decision to sign in is always based on the
portal itself.

## Build

Requires Go 1.26 or newer. All dependencies are vendored, so the build works
offline. Everything is written to `dist/`; the `clean` task removes the
executables from there but leaves settings and logs alone, so a build that was
started from `dist/` keeps its account.

One command, no extra tooling:

```bat
build.cmd
```

```sh
./build.sh
```

The same tasks are available directly, which is what the other targets need:

```sh
go run ./tools/build            # list the tasks
go run ./tools/build build      # this platform
go run ./tools/build console    # console build, for scripts and services
go run ./tools/build all        # Windows and Linux, amd64, arm64, 386 and armv7
go run ./tools/build test       # go vet and go test
go run ./tools/build icon       # redraw the icon from the drawing code
go run ./tools/build resources  # regenerate the Windows resource file
go run ./tools/build clean
```

CMake drives the same tasks, if you prefer that:

```sh
cmake -S . -B build -G Ninja
cmake --build build                      # builds the program
cmake --build build --target check       # go vet and go test
cmake --build build --target SDAutoLogin-console
cmake --build build --target all-platforms
cmake --install build --prefix /usr/local
```

CMake compiles nothing itself: the Go toolchain is the compiler, and the task
runner owns the flags, so the three ways in cannot drift apart. The project
declares no languages, which means no C compiler is needed; on Windows the
generator must therefore not depend on a C toolchain either, and Ninja is a good
choice for that (a single executable, no installer).

Cross compiling covers Windows and Linux, for `amd64`, `arm64`, `386` and `armv7`.
The last one is the 32 bit ARM build, and its file name carries the instruction set
version, because `arm` on its own would not say which boards it runs on. macOS is
the exception: its tray needs cgo, so that build has to be made on macOS with the
Xcode command line tools installed (`xcode-select --install`).

The Windows executable carries an icon, version information and a DPI aware
application manifest, embedded from `winres/rsrc_windows_<arch>.syso`, one file per
architecture (`amd64`, `arm64` and `386`). That file name is also how the toolchain
knows which one belongs to which build. The icon and those files are all generated,
from the drawing code and from `winres/winres.json`; the `icon` and `resources`
tasks above regenerate them. All of them are written inside `winres/`, next to the
files they are made from, so the repository root holds sources only. They are
reproducible, so rerunning the tasks does not change the files unless the artwork
changed.

## Usage

Double click `dist\SDAutoLogin.exe`, or run `dist/SDAutoLogin` elsewhere. The
program adds a tray icon and keeps checking in the background. On the first run,
when no account is stored yet, the settings page opens automatically.

Tray menu:

| Entry | Effect |
| --- | --- |
| Status | current state, also shown in the tooltip |
| Last login | time of the last automatic sign in |
| Check and log in now | run one check immediately |
| Settings... | open the settings page in the browser |
| Run at startup | add or remove the autostart entry |
| Reload config | re-read `config.json` and restart the check loop |
| Open config file | open `config.json` in the default editor |
| Open log | open `logs/app.log` |
| Exit | quit the program; the campus session stays online |

A click on the icon opens the settings page, a right click opens the menu.

## Settings

The page is served from `127.0.0.1` on a random port and is protected by a one
time token that only the tray can hand out, so another local program cannot
read or change the configuration. After 15 minutes without a request the
service shuts itself down and releases the port; it starts again the next time
the page is opened.

On a machine with no tray, `run --settings=ADDR` serves the same page on an
address you choose. That endpoint starts at once and stays up until the program
stops, and when ADDR is not a loopback address the page is reachable from the
network with the token in its address as the only protection, so keep it on a
trusted network or behind a tunnel.

**General**

- Account: student id, user name or full mail address.
- Password, and whether it is written to the config file.
- Service: campus network, China Mobile (`@zgyd`), China Unicom (`@cucc`),
  China Telecom (`@ctc`).
- Interface language.
- Sign in automatically after a disconnection.
- Run at startup.
- Check interval, and the reachability probes.

**Advanced**

- Portal home URL and authentication API URL.
- Account prefix and suffix, which are combined into the submitted account.
- Request timeout and the `jsVersion` parameter.
- Verbose logging.

Changing something marks the page as unsaved; **Save** writes `config.json`,
restarts the check loop and immediately runs one check so the effect is visible
right away.

## Command line

Useful on a server, over SSH or in a script. The command line stays English.

```text
SDAutoLogin [command] [options]

run            run in the background, no tray
status         print the current session state once and exit
login          submit one login and print the portal reply
config         interactive setup, writes config.json
config --show  print the effective configuration, password never included
version        print the version
help           print the usage text

--home=DIR         keep config.json and logs/ in DIR instead of next to the
                   executable, for a service or a container
--username=NAME    use this account instead of the configured one
--password=SECRET  use this password instead of the configured one
--isp=SUFFIX       service suffix such as @zgyd, empty for the campus network
--save             write what was given here into config.json as well
--settings=ADDR    run: also serve the settings page on ADDR, for a machine
                   with no tray and no display, for example 127.0.0.1:8088
--json             status: print one line of JSON instead of the report
--app[=ADDR]       open the settings page in a window of its own, without the
                   browser around it, on ADDR or on loopback
```

An option can also be written as `--name value`, but the form above is the one to
use when the value holds a character the shell would act on, such as the `@` of a
service suffix. `--app` is the exception: its address is optional and has to be
written with the `=`, so the bare `--app` is how a desktop starts.

The options are read on every run and applied to that run only, so a container or
a service without a terminal can be provisioned without the setup wizard; add
`--save` to write them to `config.json` as well. `run` reports a missing account
and exits rather than asking for it, since a service has nobody to ask.

The same executable serves both modes: started without a command it puts the
tray icon up and keeps running, started with one it does that one thing and
exits. `-cli` is short for `run`, `-test-login` is short for `login`.

`--app` keeps the tray but stops treating the settings page as a page: it opens
it in a browser window with no address bar, no tabs and no menus, so it looks
like an application of its own. Only the Chromium family can do that, so Chrome,
Edge, Chromium or Brave is started with the window and the rest of its chrome
left out. When none of them is installed, or the one that was found refuses to
start, the page goes to the default browser instead and the log says so; a tab is
worse than a window of its own, but better than nothing happening at all.

On Windows the file is linked as a GUI program, so a double click does not open
a console window. Started from a terminal it attaches to that terminal, so the
output is visible there. Where the output has to be piped or the exit code read
by a script, build the console variant instead, which is the same source
without `-H=windowsgui`:

```sh
go build -trimpath -ldflags "-s -w" -o SDAutoLogin-console.exe ./cmd/SDAutoLogin
```

`status` exit codes: `0` online, `1` state unknown or failure, `3` the portal
says the client is not logged in. That makes it usable in a monitoring script.

`status --json` prints the same check as one line of JSON. The log goes to
stderr, so only the JSON reaches stdout and a script can read it without
filtering anything out:

```sh
SDAutoLogin status --json | jq -r .state
```

```json
{"portal":"http://10.9.1.3/","state":"online","reachable":true,"known":true,"online":true,"clientIp":"203.0.113.7","account":"s1234567","onlineSince":"2026-01-01 00:00:00","detail":"online"}
```

`state` is one of `online`, `offline`, `unknown` (the portal answered, but said
nothing usable) or `unreachable` (it did not answer at all). The last two are
both exit code `1`, so the state tells them apart.

## Configuration file

`config.json` sits next to the executable and is created with defaults on the
first run.

| Field | Default | Meaning |
| --- | --- | --- |
| `username` | | student id, user name or mail address |
| `password` | | password, written only when `rememberPassword` is true |
| `rememberPassword` | `true` | keep the password in the file |
| `portalBaseUrl` | `http://10.9.1.3/` | portal home page, read for the client IP |
| `eportalUrl` | `http://10.9.1.3:801/eportal/` | Dr.COM eportal root |
| `accountPrefix` | `,0,` | prefix the portal page uses for a browser login |
| `accountSuffix` | | ISP suffix such as `@zgyd` |
| `checkIntervalSeconds` | `15` | how often the session is checked, minimum 5 |
| `httpTimeoutSeconds` | `6` | request timeout, minimum 2 |
| `autoLogin` | `true` | sign back in when the session is gone |
| `checkInternet` | `true` | also probe the probe addresses |
| `verboseLog` | `true` | write per-check diagnostics |
| `probeUrls` | see below | addresses used for the reachability probe |
| `jsVersion` | `3.3.3` | value the portal page sends |
| `language` | `auto` | `auto`, `zh` or `en` |

Default probe addresses: `http://connect.rom.miui.com/generate_204`,
`http://www.baidu.com`, `http://www.msftconnecttest.com/connecttest.txt`.
A probe counts as successful when it answers normally and shows no portal
markers, so a captive portal does not look like a working Internet connection.

Autostart entries: the registry key
`HKCU\Software\Microsoft\Windows\CurrentVersion\Run` on Windows,
`~/.config/autostart/sdautologin.desktop` on Linux, and
`~/Library/LaunchAgents/com.sdautologin.plist` on macOS.

## Files

- `config.json` - settings
- `logs/app.log` - log, rotated to `app.log.1` when it exceeds 4 MiB

Both live next to the executable by default, so a build in `dist/` keeps its
settings and log there, and moving the executable means moving `config.json` with
it. `--home=DIR` puts them in DIR instead, which is what a service and the
container below use so that the executable can stay on a read-only `/usr`.

## Deployment

`run` is the mode to use: it keeps checking with no tray and no display, logs to
the standard error, and exits on `SIGTERM`. It refuses to start without an
account, so the account is written down once first, either by passing it on the
command line with `--save`, or from the settings page when the service is started
with `--settings`.

### systemd

Two ready made units are in [packaging/systemd](packaging/systemd), one for a
system service and one for `systemctl --user`. Each file starts with the
installation commands, the provisioning step and the commands to follow the log.
The short version, for a server:

```sh
install -m 0755 dist/SDAutoLogin-linux-amd64 /usr/local/bin/SDAutoLogin
useradd --system --create-home --home-dir /var/lib/sdautologin \
        --shell /usr/sbin/nologin sdautologin
install -m 0644 packaging/systemd/system/sdautologin.service \
        /etc/systemd/system/sdautologin.service
sudo -u sdautologin /usr/local/bin/SDAutoLogin --home=/var/lib/sdautologin \
     --username=s1234567 --password=SECRET --isp=@zgyd --save config --show
systemctl daemon-reload
systemctl enable --now sdautologin
journalctl -u sdautologin -f
```

The unit keeps the settings and the log in `/var/lib/sdautologin` and confines
the process to the network it needs. To configure that machine from a browser,
add `--settings=127.0.0.1:8088` to `ExecStart` and forward the port over SSH:
`ssh -L 8088:127.0.0.1:8088 the-server`, then open `http://127.0.0.1:8088/`.

### Container

The [Dockerfile](Dockerfile) builds the same static binary and puts it in a small
image that runs as an unprivileged user, with `/data` for the settings and the
log:

```sh
docker build -t sdautologin .
docker run -d --name sdautologin --restart unless-stopped \
    -e TZ=Asia/Shanghai -v sdautologin:/data sdautologin
docker exec sdautologin SDAutoLogin --home=/data status --json
```

The account is written down the same way, with a one-off run:

```sh
docker run --rm -v sdautologin:/data sdautologin \
    --username=s1234567 --password=SECRET --isp=@zgyd --save config --show
```

`--home=/data` is part of the entry point, so a one-off command such as `status`
reads the same settings as the service. The container is marked unhealthy when
`status` reports the session is not up, which is the one thing worth noticing
about it.

## Interface language

`auto` follows the system language, `zh` and `en` force one. The setting
applies to the tray menu, the balloon tips and the settings page. Log files,
the command line and the source comments stay English on purpose, so a pasted
log is readable by anyone.

## Security notes

- The settings service listens on `127.0.0.1` only, requires a per-run random
  token, and shuts down when idle.
- The password has to be available to sign in unattended, so it is stored in
  `config.json` in clear text unless `rememberPassword` is turned off. Keep the
  file readable by your own user only.
- The program never calls the logout endpoint, so quitting cannot leave the
  machine offline.

## Project layout

The code follows the usual Go layout: `cmd/` holds the executable, `internal/`
holds the packages it is built from. Nothing outside the module can import them,
which is what keeps the public surface at the command line and the settings page.

| Path | Content |
| --- | --- |
| `cmd/SDAutoLogin/` | entry point: mode selection, the tray and headless runners, the command line, console attachment |
| `internal/portal/` | Dr.COM eportal client |
| `internal/service/` | check and reconnect loop, session state |
| `internal/config/` | configuration file, operator list, validation |
| `internal/app/` | configuration and service shared by the tray, the page and the command line, plus the local settings service |
| `internal/webui/` | settings page and the assets embedded from it |
| `internal/i18n/` | interface strings and system language detection |
| `internal/logging/` | logger with rotation |
| `internal/tray/` | tray per platform, with the Win32 primitives and the icon conversion |
| `internal/platform/` | operating system glue: open a file, a URL or an application window, autostart entry, single instance |
| `internal/brand/` | icon drawing |
| `winres/` | Windows icon, version and manifest, and the generated resource file compiled from them |
| `tools/icogen/` | icon generator |
| `tools/build/` | task runner, used by the scripts and the CMake targets |
| `packaging/systemd/` | systemd units, for a system service and for `systemctl --user` |
| `Dockerfile` | container image, built from the vendored sources |
| `CMakeLists.txt` | CMake front end for the task runner |
| `dist/` | build results, plus the settings and log of a build started from there |
| `build/` | CMake build tree, when CMake is used |
| `vendor/` | vendored dependencies |

## Troubleshooting

- **Nothing happens after a disconnect.** Check `logs/app.log` for the
  `session is down, reconnect attempt #N` lines and the portal reply that
  follows. A `ret_code` of `1` means the account or password is wrong, `3`
  means the portal is busy.
- **The icon stays grey.** The portal page did not answer, so the state is
  unknown and no login is attempted. Usually the machine is not in the campus
  network any more.
- **A check reports unknown.** The portal answered but the reply had no
  `result` field, for example because a captive portal intercepted it. The
  last reply is written to the log.
- **The portal moved.** Update the portal home URL and the API URL in the
  Advanced tab.

## Licence

MIT, see [LICENSE](LICENSE).
