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
- A notification when the session was signed back in, or when signing in failed:
  a balloon tip on Windows, the desktop's own notification on Linux and macOS.
- Run at startup, one switch per platform (registry, desktop entry or
  LaunchAgent).
- Detailed per-check logging with rotation, so a dropped session can be
  diagnosed afterwards. The password is never written to the log.

## How it works

The program talks to the same endpoints the portal's own login page uses:

| Step | Request | Purpose |
| --- | --- | --- |
| 1 | `GET /` | read `v4ip` from the portal home page, the IP the portal sees (after NAT, not the local one) |
| 2 | `GET ?c=Portal&a=online_list` | ask whether that IP is authenticated. This endpoint answers from inside the campus network even while the session is down |
| 3 | `GET ?c=Portal&a=login` | submit `user_account` and `user_password` with `login_method=1` |

A login is only attempted when step 2 answered that the portal does *not*
consider the client online. If the portal cannot be reached, or the reply is
not usable, the state is treated as unknown and no login is attempted, so an
outage does not turn into a stream of pointless requests. After a login the
program asks again, so "accepted but still offline" is reported as a failure
instead of a success.

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

The same tasks can be called directly, as the other targets do:

```sh
go run ./tools/build            # list the tasks
go run ./tools/build build      # this platform
go run ./tools/build console    # console build, for scripts and services
go run ./tools/build all        # Windows and Linux, every target below
go run ./tools/build packages   # every package below, into dist/packages/
go run ./tools/build verify     # check the packages with the tools of their formats, then pack them again
go run ./tools/build windows    # the Windows archives and their manifests
go run ./tools/build deb        # the Debian packages
go run ./tools/build rpm        # the RPM packages
go run ./tools/build pacman     # the Arch packages
go run ./tools/build openwrt    # the OpenWrt packages, for a router
go run ./tools/build test       # go vet and go test
go run ./tools/build icon       # redraw the icon from the drawing code
go run ./tools/build resources  # regenerate the Windows resource file
go run ./tools/build clean
```

`verify` needs the tools of the formats, so it is a Linux task: `dpkg-deb` for
the deb and the ipk, `rpm`, and `bsdtar` for the Arch package. What it cannot
find it names and counts rather than passing over, and a run that could not check
a single package fails.

CMake drives the same tasks:

```sh
cmake -S . -B build -G Ninja
cmake --build build                      # builds the program
cmake --build build --target check       # go vet and go test
cmake --build build --target SDAutoLogin-console
cmake --build build --target all-platforms
cmake --build build --target packages
cmake --build build --target verify
cmake --install build --prefix /usr/local
```

CMake compiles nothing itself: the Go toolchain is the compiler, and the task
runner owns the flags, so the three ways in cannot drift apart. The project
declares no languages, so no C compiler is needed; on Windows the generator must
therefore not depend on a C toolchain either, and Ninja is a suitable choice (a
single executable, no installer).

Cross compiling covers Windows and Linux, for `amd64`, `arm64`, `386`, `armv7`,
`mips` and `mipsle`. The last three put in the file name what `GOARCH` alone does
not say: `armv7` is the instruction set of the 32 bit ARM build, and
`mips-softfloat` and `mipsle-softfloat` are for the routers, which have no floating
point unit. Both byte orders of MIPS are there because the boards are: the Atheros
targets (ath79, ar71xx) are big endian, the MediaTek ones (ramips, mt76x8) are
little endian. On a router, or on any machine with no desktop, use `run`: the tray
needs a desktop session, and the settings page is reached over the network with
`--settings`.

Every Linux target is built twice. The second file has `-notray` in its name: it
leaves the tray out, and with it the D-Bus client and everything else only a tray
needs, which makes it about a fifth smaller (10.0 MiB to 7.7 MiB for `mipsle`).
That is the one for a router, where there is no desktop session to put an icon in.
A start with `--app` is refused there rather than ignored, since the tray opens a
window of its own. The build that keeps the tray is the default one, for a
desktop.

macOS is the exception to all of this: its tray needs cgo, so that build has to be
made on macOS with the Xcode command line tools installed (`xcode-select --install`).

The Windows executable carries an icon, version information and a DPI aware
application manifest, embedded from `winres/rsrc_windows_<arch>.syso`, one file per
architecture (`amd64`, `arm64` and `386`). The toolchain uses that file name to
tell which one belongs to which build. The icon and those files are all generated,
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

The address handed out is good for one opening, and a new one is made for every
opening and for every one that is used. Each is written to the log as it is made,
so the current address is always the newest of those lines.

On a machine with no tray, `run --settings=ADDR` serves the same page on an
address that was chosen. That endpoint starts at once and stays up until the program
stops, and when ADDR is not a loopback address the page is reachable from the
network with the token in its address as the only protection, so keep it on a
trusted network or behind a tunnel. The address to open is the newest one in the
log, since the one before it was spent by the browser that opened it.

**General**

- The login list: one entry per account, each with the service types it may be
  used with, which are the campus network, China Mobile (`@zgyd`), China Unicom
  (`@cucc`) and China Telecom (`@ctc`). The entries are dragged, or moved with
  the arrow keys, into the order they are to be tried in; see the section below.
- Whether the passwords are written to the config file.
- Interface language.
- Sign in automatically after a disconnection.
- Move a session that was already up onto the first service type of its account,
  and, on the card of the entry the session is on, the controls that move it onto
  any of the other service types that entry lists.
- Run at startup.
- Check interval, and the reachability probes.

**Advanced**

- Portal home URL and authentication API URL.
- Account prefix, which is combined with the account and the service type into
  the submitted account.
- Request timeout and the `jsVersion` parameter.
- Verbose logging.

Changing something marks the page as unsaved; **Save** writes `config.json`,
restarts the check loop and immediately runs one check so the effect is visible
right away. The test button on an entry waits for the settings to be saved,
since the program can only be asked about the list it is running with.

## Accounts, and the order they are tried in

The portal allows one session per client address, so several accounts can never
be online together. The list covers the case where the first account is the one
that should be used but does not work any more: it is out of credit, its password
was changed, it was closed. The program then walks the list and signs in with the
next entry that does work.

```
1  s1234567   China Mobile, then China Unicom
2  s7654321   campus network
3  s0000003   China Telecom                      (disabled)
```

1. The entries are tried from the top down, and an entry is submitted once per
   service type it names, in the order it names them: the third line above would
   try `s1234567@zgyd` and then `s1234567@cucc` before moving on to `s7654321`.
2. An answer that says the account itself is the problem, a wrong password or a
   failed authentication, takes that entry out of the running for ten minutes and
   moves on to the next one. Ten minutes is long enough not to hammer the portal
   with a password that is wrong, and short enough to pick up a fix; a saved
   configuration or a manual check also clears it at once.
3. An answer about the portal rather than the account, a busy portal, a request
   that timed out, an unreadable reply, keeps the entry and ends the round there,
   since walking the list on a busy portal would mark every account as broken in
   one go.
4. The page marks the entry the session is on, and every entry that is being
   left alone with the reason and the time left.

A disabled entry takes no part at all, so an entry can be taken out of the
running without deleting its password.

The order of an entry's service types is what the program follows when it signs
in itself. A session that is already up when the program starts — one signed in
by hand, or by a previous run — is not something the portal moves, and one
session per address means the only way onto another service type is a login that
replaces it. The program signs in again as the first service type of the entry
that session belongs to, and asks about it again an hour later while the session
stays where it is. A portal that will not sign in again while a session is up is
reported and left as it is, and the entry is not taken out of the running for it:
the session is up and working, so the settings page, the pill and the tray say
the session needs a reader rather than showing an error.

The card of the account in use carries the controls for that: the service type
the session is on, the one its entry puts first, and a way to sign in again as
any of the other service types the entry lists. It is the way out when the portal
will not take the first one, and it works whether or not the option above is on.

## Command line

Useful on a server, over SSH or in a script. The command line stays English.

```text
SDAutoLogin [command] [options]

run            run in the background, no tray
status         print the current session state once and exit
login          submit one login and print the portal reply (nothing is submitted
               while the portal already shows this client online)
config         interactive setup, writes config.json
config --show  print the effective configuration, password never included
version        print the version
help           print the usage text

--home=DIR         keep config.json and logs/ in DIR instead of next to the
                   executable, for a service or a container
--no-log           keep the log on the console alone, with no logs/app.log, for
                   a machine with a small flash
--account=USER:PASSWORD[@SERVICE[+SERVICE...]]
                   one entry of the login list; repeat the option to write
                   several, in the order they are to be tried
--username=NAME    use this account, as the single entry of the list
--password=SECRET  use this password instead of the configured one, which on its
                   own is the password of the first entry of the list
--password-file=PATH
                   read the password from the first line of PATH instead; a
                   secret on the command line is in the process list of every
                   user on the machine, so this is the form to use from a
                   script, a service or a container
--isp=SUFFIX       service suffix such as @zgyd, empty for the campus network
--save             write what was given here into config.json as well
--settings=ADDR    run: also serve the settings page on ADDR, for a machine
                   with no tray and no display, for example 127.0.0.1:8088;
                   the address to open is the one the log prints
--json             status: print one line of JSON instead of the report
--app[=ADDR]       open the settings page in a window of its own, without the
                   browser around it, on ADDR or on loopback
```

An option can also be written as `--name value`, but the form above is the one to
use when the value holds a character the shell would act on, such as the `@` of a
service suffix. `--app` is the exception: its address is optional and has to be
written with the `=`, so a desktop starts with the bare `--app`.

The options are read on every run and applied to that run only, so a container or
a service without a terminal can be provisioned without the setup wizard; add
`--save` to write them to `config.json` as well. `run` reports a missing account
and exits rather than asking for it, since a service has nobody to ask.

The account options describe the list as a whole rather than adding to it, as a
script expects:

```sh
# one account, for a service or a container
SDAutoLogin run --account=s1234567:SECRET@zgyd --save

# several, in the order they are to be tried; an entry may name more than one
# service type, and the @ introduces them
SDAutoLogin run --account=s1234567:SECRET@zgyd+cucc --account=s7654321:OTHER --save
```

A password that holds an `@` cannot be written in that form, since the `@`
introduces the service types; `--username` and `--password` take it.

There is one more place the password can come from, and it is the better one for
a service. A command line is in the process list of every user on the machine, so
`--password-file=PATH` reads the password from the first line of a file instead,
and a systemd unit that hands the secret over with
`LoadCredential=password:/etc/sdautologin/password` needs no option at all: the
program reads `$CREDENTIALS_DIRECTORY/password` by itself when nothing else named
a password. Either way only the line ending is dropped, since a password may end
in a space.

The same executable serves both modes: started without a command it puts the
tray icon up and keeps running, started with one it does that one thing and
exits. `-cli` is short for `run`, `-test-login` is short for `login`.

`--app` keeps the tray but stops treating the settings page as a page: it opens
it in a browser window with no address bar, no tabs and no menus, so it looks
like an application of its own. Only the Chromium family can do that, so Chrome,
Edge, Chromium or Brave is started with the window and the rest of its chrome
left out. When none of them is installed, or the one that was found refuses to
start, the page goes to the default browser instead and the log says so.

On Windows the file is linked as a GUI program, so a double click does not open
a console window. Started from a terminal it attaches to that terminal, so the
output is visible there. Where the output has to be piped or the exit code read
by a script, build the console variant instead, which is the same source without
`-H=windowsgui`:

```sh
go run ./tools/build console
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
| `accounts` | `[]` | the login list, in the order the entries are tried in |
| `accounts[].username` | | student id, user name or mail address |
| `accounts[].password` | | password, written only when `rememberPassword` is true |
| `accounts[].disabled` | absent | present and true takes the entry out of the running |
| `accounts[].isps` | `[""]` | service suffixes such as `@zgyd`, in the order they are tried |
| `rememberPassword` | `true` | keep the passwords in the file |
| `portalBaseUrl` | `http://10.9.1.3/` | portal home page, read for the client IP |
| `eportalUrl` | `http://10.9.1.3:801/eportal/` | Dr.COM eportal root |
| `accountPrefix` | `,0,` | prefix the portal page uses for a browser login |
| `checkIntervalSeconds` | `15` | how often the session is checked, 5 seconds at the least and a day at the most |
| `httpTimeoutSeconds` | `6` | request timeout, 2 seconds at the least and 15 minutes at the most |
| `autoLogin` | `true` | sign back in when the session is gone |
| `switchToPreferredService` | `true` | sign a session that is already up in again as the first service type of its entry |
| `checkInternet` | `true` | also probe the probe addresses |
| `verboseLog` | `true` | write per-check diagnostics |
| `logToFile` | `true` | also write `logs/app.log`, see `--no-log` |
| `probeUrls` | see below | addresses used for the reachability probe |
| `jsVersion` | `3.3.3` | value the portal page sends |
| `language` | `auto` | `auto`, `zh` or `en` |

```json
{
  "accounts": [
    { "username": "s1234567", "password": "SECRET", "isps": ["@zgyd", "@cucc"] },
    { "username": "s7654321", "password": "OTHER", "isps": [""] },
    { "username": "s0000003", "password": "SECRET", "disabled": true, "isps": ["@ctc"] }
  ],
  "accountPrefix": ",0,",
  "checkIntervalSeconds": 15
}
```

A file written by an earlier version, with a single `username`, `password` and
`accountSuffix`, is read as one entry of the list and saved again in the new
shape, so nothing has to be done by hand.

Default probe addresses: `http://connect.rom.miui.com/generate_204`,
`http://www.baidu.com`, `http://www.msftconnecttest.com/connecttest.txt`.
A probe counts as successful when it answers normally and shows no portal
markers, so a captive portal does not look like a working Internet connection.

Autostart entries: the registry key
`HKCU\Software\Microsoft\Windows\CurrentVersion\Run` on Windows,
`~/.config/autostart/sdautologin.desktop` on Linux, and
`~/Library/LaunchAgents/com.sdautologin.plist` on macOS.

## Files

- `config.json` - settings, written readable by its owner alone; on Windows it is
  given an access list for that account, since the mode bits mean nothing there
- `logs/app.log` - log, in a directory of its own, both readable by their owner
  alone, and rotated to `app.log.1` when it exceeds 4 MiB. `--no-log` and the
  `logToFile` setting leave it out, as a machine with a small flash, such as a
  router, does: the same lines go to the console either way.

Both live next to the executable by default, so a build in `dist/` keeps its
settings and log there, and moving the executable means moving `config.json` with
it. `--home=DIR` puts them in DIR instead, which a service and the
container below use so that the executable can stay on a read-only `/usr`.

## Deployment

`run` is the mode to use: it keeps checking with no tray and no display, logs to
the standard error, and exits on `SIGTERM`. It refuses to start without an
account, so the account is written down once first, either by passing it on the
command line with `--save`, or from the settings page when the service is started
with `--settings`.

### Packages

`go run ./tools/build packages` writes a package for every distribution this
program targets:

| Distribution | Package | Installed with |
| --- | --- | --- |
| Debian, Ubuntu | `sdautologin_1.0.0-1_amd64.deb` | `apt install ./...` |
| Fedora, RHEL, openSUSE | `sdautologin-1.0.0-1.x86_64.rpm` | `dnf install` or `rpm -i` |
| Arch | `sdautologin-1.0.0-1-x86_64.pkg.tar.zst` | `pacman -U` |
| OpenWrt | `sdautologin_1.0.0-1_mipsel_24kc.ipk` | `opkg install` |
| Windows | `SDAutoLogin-1.0.0-windows-amd64.zip` | unpack it |

Each one holds the same thing in the layout of its distribution: the program,
built without the tray; the systemd unit that runs it; the file that declares the
`sdautologin` user it runs as; and the licence and the documents. The settings and
the log live in `/var/lib/sdautologin`, the service is enabled by the package, and
every one of them has to be given an account before it stays up, since a service
has nobody to answer the setup wizard. OpenWrt is the one that differs, having no
systemd and no `/var/lib`: its section below has the details.

The packages are written by the build task itself, with the standard library
alone: none of them needs a packaging toolchain installed, and an ipk or a deb
needs nothing at all. The Arch package is the exception, because Go has no zstd
or xz writer: it is packed with `zstd` when that is installed, and with `xz` or
`gzip` when it is not, and the task says which of them it used.

Nothing is signed. That is fine for a local install: `dpkg -i`, `rpm -i` and
`pacman -U` all take a file that is not signed, pacman only complaining when its
configuration asks for signatures and `LocalFileSigLevel` is not `Optional`. A
repository of its own would need a key, and a key is not something a build task
can invent. What the task writes instead is `SHA256SUMS` next to the packages,
one line per file in the spelling of the `sha256sum` tool, so that a release that
was downloaded can be checked against it.

`go run ./tools/build verify` reads every package back with the tool that
installs it, `dpkg-deb` for the deb and the ipk, `rpm -K` and `rpm -qpl` for the
rpm, `bsdtar` and the listing inside the package for the Arch one, compares the
Windows archives with the builds they were made from, and checks that the Scoop
and winget manifests carry the digest of the archive that is there. It then packs
everything a second time and fails if a single byte came out differently, since a
release built twice has to be the same release.

All of this runs when a tag is pushed, in
[.gitea/workflows/ci.yml](.gitea/workflows/ci.yml), together with `go vet`,
`go test` and a `gofmt` check, so a package that would not install is caught
before it is published rather than after. Nothing else starts it: a branch is
checked from the checkout, with those same tasks, instead of on the runner.

Pushing a tag publishes the release: the same run creates it and attaches the
packages and `SHA256SUMS` to it, which the two manifests point at. The version is
read from that tag, so the packages are named after it and the manifests agree
with what is attached by construction: the tag is `v` followed by the version,
`v1.0.0` for 1.0.0. The same version goes into the program itself, which reports
it with the commit it was built from, and into the version resource of the
Windows build. The sources carry the number as well, for a tree that is unpacked
rather than checked out, and a test holds them to the tag. A release that already
exists is taken as it is, so a tag pushed twice does not fail the run.

### Debian and Ubuntu

```sh
sudo apt install ./dist/packages/sdautologin_1.0.0-1_amd64.deb
sudo -u sdautologin /usr/bin/SDAutoLogin --home=/var/lib/sdautologin \
     --username=s1234567 --password=SECRET --isp=@zgyd --save config --show
sudo systemctl restart sdautologin
```

The package creates the `sdautologin` user, enables the service and starts it;
without an account it stops after a few tries, as the postinst says when it finds
no `config.json` yet. `/usr/share/doc/sdautologin/README.Debian`
has the same steps on the machine. Removing the package leaves the settings and
the log where they are, and so does purging it: the account in them is not the
package's to throw away.

### Fedora, RHEL and openSUSE

```sh
sudo rpm -i dist/packages/sdautologin-1.0.0-1.x86_64.rpm
sudo -u sdautologin /usr/bin/SDAutoLogin --home=/var/lib/sdautologin \
     --username=s1234567 --password=SECRET --isp=@zgyd --save config --show
sudo systemctl restart sdautologin
```

`rpm -K` checks the digests of the package, `rpm -qi` shows what the header says
about it, and `rpm -e sdautologin` removes it, leaving `/var/lib/sdautologin`
alone.

### Arch

```sh
sudo pacman -U dist/packages/sdautologin-1.0.0-1-x86_64.pkg.tar.zst
sudo -u sdautologin /usr/bin/SDAutoLogin --home=/var/lib/sdautologin \
     --username=s1234567 --password=SECRET --isp=@zgyd --save config --show
sudo systemctl restart sdautologin
```

`pacman -Qi` and `pacman -Qkk` work on what it installs, the second one checking
every file against the listing the package carries.

### Windows

The archive holds the program, the readme and the licence, and nothing else:
there is no installer, because there is nothing to install. Unpack it where it is
to stay and start the program; it writes its settings next to itself, so that
folder has to be kept.

`dist/packages/manifests/` holds what the package managers that can install such
an archive want, `scoop/sdautologin.json` and the three files of the winget
manifest. Both name the archives of a release as their download, at
`<project address>/releases/download/v<version>/`, where Gitea, the host this
project is published on, serves the attachments of a release; change
`releaseURL` in `tools/build/pack_win.go` when releases move elsewhere, and
both follow. The winget command line checks them
(`winget validate --manifest dist/packages/manifests/winget`), and the Scoop one
declares the settings and the log as persistent, so an update keeps the account.

### systemd

Two ready made units are in [packaging/systemd](packaging/systemd), one for a
system service and one for `systemctl --user`; the user unit opens with its own
installation steps. The system one is the file the packages install, so the steps
below are the ones it performs:

```sh
install -m 0755 dist/SDAutoLogin-linux-amd64 /usr/bin/SDAutoLogin
useradd --system --create-home --home-dir /var/lib/sdautologin \
        --shell /usr/sbin/nologin sdautologin
install -D -m 0644 packaging/systemd/system/sdautologin.service \
        /usr/lib/systemd/system/sdautologin.service
sudo -u sdautologin /usr/bin/SDAutoLogin --home=/var/lib/sdautologin \
     --username=s1234567 --password=SECRET --isp=@zgyd --save config --show
systemctl daemon-reload
systemctl enable --now sdautologin
journalctl -u sdautologin -f
```

The unit keeps the settings and the log in `/var/lib/sdautologin` and confines
the process to the network it needs. To configure that machine from a browser,
add `--settings=127.0.0.1:8088` to `ExecStart` and forward the port over SSH:
`ssh -L 8088:127.0.0.1:8088 the-server`. Then open the address the log prints,
which `journalctl -u sdautologin` shows, rather than the bare port: it carries
the one time token the page is opened with, and a request without it is refused.
The address is good for one opening, and the next one goes to the log as it is
made, so the newest of those lines is the one that works.

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
`status` reports the session is not up.

### OpenWrt

The two MIPS builds are packaged, one for each byte order:

| Package | For |
| --- | --- |
| `sdautologin_<version>-1_mips_24kc.ipk` | big endian boards, such as the Atheros ones (ath79) |
| `sdautologin_<version>-1_mipsel_24kc.ipk` | little endian boards, such as the MediaTek ones (ramips, mt76x8) |

A router has no systemd, so this package installs a procd init script at
`/etc/init.d/sdautologin` and a UCI configuration at `/etc/config/sdautologin`.

A router has least flash, so the program is installed compressed as
`/usr/lib/sdautologin/SDAutoLogin.gz`, and `/usr/bin/SDAutoLogin` is a short
script that unpacks it into `/tmp` on the first start and runs it from there.
That is 2.7 MiB of flash instead of 7.7, against 7.7 MiB of memory while it runs.
The copy in `/tmp` is unpacked once per boot and once per upgrade, and the name it
is unpacked to carries the version, so a new package unpacks its own program
rather than running the one the package before it left behind.

```sh
go run ./tools/build openwrt
scp dist/packages/sdautologin_*_mipsel_24kc.ipk root@router:/tmp/
ssh root@router opkg install /tmp/sdautologin_*_mipsel_24kc.ipk
```

Installing enables the service, so it comes up with the router from then on.
Give it the account:

```sh
uci set sdautologin.main.username=s1234567
uci set sdautologin.main.password=SECRET
uci set sdautologin.main.isp=@zgyd
uci commit sdautologin
/etc/init.d/sdautologin restart
```

An option that is left empty is not passed on, and then `config.json` decides,
which the settings page writes. That page can be turned on for a router
with no display, and reached from the network it serves:

```sh
uci set sdautologin.main.settings=0.0.0.0:8088
uci commit sdautologin
/etc/init.d/sdautologin restart
```

Then open the address `logread -e SDAutoLogin` prints, with `127.0.0.1` in it
replaced by the address the router answers on, since the page is served from
every interface but the log names the loopback one. The port alone is refused:
the address carries the one time token the page is opened with, and it opens the
page once, so the newest of those lines is the one that works. With an address
that is not loopback the token in the address handed out is the only protection,
so keep it on a trusted network or reach it over a tunnel.

`/etc/config/sdautologin` is declared as a configuration file, so an upgrade
keeps the account that is in it, and `config.json` lives in `/etc/sdautologin`,
which is on the overlay and survives a reboot and an upgrade.

The log file is off by default here, since `/etc` is on the flash: the program
writes the same lines to the console, procd hands them to `logd`, and `logread -e
SDAutoLogin` reads them back out of memory. Turn it on for a file, which costs up
to eight megabytes of flash once the log has grown to its limit:

```sh
uci set sdautologin.main.log_file=1
uci commit sdautologin
/etc/init.d/sdautologin restart
```

#### Building it into an image

A router can also carry the program from the start, rather than installing it
afterwards. `packaging/openwrt/Makefile` is an OpenWrt package for that, and the
Go build system it needs comes from the packages feed:

```sh
cd $OPENWRT
./scripts/feeds update packages
./scripts/feeds install golang
mkdir -p package/sdautologin
cp -r /path/to/SDAutoLogin/packaging/openwrt/. package/sdautologin/
make menuconfig          # Network -> sdautologin
```

It installs the program uncompressed, since compressing it there would not save
anything: a squashfs compresses everything it holds and does that better than
gzip does (1.9 MiB against 2.7 for this program). Leaving it alone also keeps it
a file in the read only filesystem, rather than a copy of it in memory. The init
script and the UCI configuration are the same two files the package above
installs.

## Interface language

`auto` follows the system language, `zh` and `en` force one. The setting
applies to the tray menu, the balloon tips and the settings page. Log files,
the command line and the source comments stay English on purpose, so a pasted
log is readable by anyone.

## Security notes

- The settings service listens on `127.0.0.1` only, requires a per-run random
  token, and shuts down when idle. Every phase of a request has a deadline, the
  token is compared in a time that does not depend on how much of it matched, and
  every answer carries the security headers, the refusals included.
- That token travels in the address the tray hands out, since a navigation is the
  one request a browser makes on its own and the one request that cannot carry a
  header. It is exchanged for a session cookie before the page is served, and the
  browser is sent to the same page without it, so the address bar, the history and
  the `Referer` of anything the page links to carry no token. Every request after
  that is checked against the cookie, a request that changes something has to
  repeat it in a header, and the address itself is good for one opening.
- The password has to be available to sign in unattended, so it is stored in
  `config.json` in clear text unless `rememberPassword` is turned off. The file is
  written readable by its owner alone, and so is the log in its directory, since
  the log names the account as well: `0600` for the two files, `0700` for the
  directory. A file an earlier version or a copy left readable by anyone else is
  tightened at startup, and the log says what was found and what was done; on
  Windows, where the mode bits do nothing, the file gets an access list of its
  own naming only the account the program runs as. The systemd units set
  `UMask=0077` and the system service keeps its state directory to `0700` as well.
- The settings page writes the password field and never reads it. It is told
  whether a password is stored rather than what it is, so the secret is not in the
  browser, not in the script the page loads, and not in a request the page makes,
  and what a save answers with is the same empty field. Leaving the field empty
  while saving keeps the password that is stored, which is what every entry the
  reader did not touch sends; a password has to be typed only for an entry that has
  none, which is a new one or one whose account was renamed.
- The program never calls the logout endpoint, so quitting cannot leave the
  machine offline.

## Project layout

The code follows the usual Go layout: `cmd/` holds the executable, `internal/`
holds the packages it is built from. Nothing outside the module can import them,
so the public surface is the command line and the settings page.

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
| `packaging/openwrt/` | the OpenWrt package, its procd init script and its UCI configuration |
| `.gitea/workflows/` | what Gitea Actions runs on a push: the tests, the packages and the checks on them |
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
