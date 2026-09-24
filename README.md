# Campus Auto Login

A small tray program that keeps your campus network session alive. It asks the
portal every few seconds whether you are still online, and signs you back in when
you are not. It never logs you out — that is your business.

**English** | [简体中文](README.zh-CN.md)

One self-contained executable per platform, nothing to install. Copy it over,
double-click it, done.

## Contents

- [Features](#features)
- [How it works](#how-it-works)
- [Build](#build)
- [Usage](#usage)
- [Settings](#settings)
- [Accounts, and the order they are tried in](#accounts-and-the-order-they-are-tried-in)
- [Command line](#command-line)
- [Configuration file](#configuration-file)
- [Files](#files)
- [Deployment](#deployment)
- [Interface language](#interface-language)
- [Security notes](#security-notes)
- [Project layout](#project-layout)
- [Troubleshooting](#troubleshooting)

## Features

- Tray icon on Windows, Linux and macOS, drawn by the program itself, its colour
  following the session state.
- The settings page is a local web page, served on loopback and opened in your
  browser: no GUI toolkit linked in, and the same window on every platform.
- Chinese or English interface, following the system language by default. Logs
  stay English.
- Options split into General and Advanced, so the everyday switches (account,
  interval, probes) do not sit next to the portal endpoints.
- A command line mode for machines without a desktop.
- A notification when the session was signed back in, or when signing back in
  failed: a balloon tip on Windows, the desktop's own notification elsewhere.
- Run at startup, one switch per platform (registry, desktop entry, LaunchAgent).
- Per-check logging with rotation, so a dropped session can be diagnosed
  afterwards. The password never goes to the log.

## How it works

The same endpoints the portal's own login page uses:

| Step | Request | Purpose |
| --- | --- | --- |
| 1 | `GET /` | read `v4ip` from the portal home page, the IP the portal sees (after NAT, not your local one) |
| 2 | `GET ?c=Portal&a=online_list` | ask whether that IP is authenticated; this endpoint answers from inside the campus network even while the session is down |
| 3 | `GET ?c=Portal&a=login` | submit `user_account` and `user_password` with `login_method=1` |

A login is only attempted when step 2 said the portal does *not* consider you
online. If the portal cannot be reached, or the reply is unusable, the state is
unknown and nothing is submitted — an outage should not turn into a stream of
pointless requests. After a login the program asks again, so "accepted but still
offline" is reported as a failure rather than a success.

If you turn on the reachability probes, they only feed the status display. The
decision to sign in is always based on the portal itself.

## Build

Go 1.26 or newer. Dependencies are vendored, so the build works offline.
Everything lands in `dist/`; `clean` removes the executables from there but
leaves settings and logs alone, so a build started from `dist/` keeps its
account.

One command, no extra tooling:

```bat
build.cmd
```

```sh
./build.sh
```

The tasks can be called directly too, which is what the scripts and the CMake
targets do:

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
the deb and the ipk, `rpm`, and `bsdtar` for the Arch package. Whatever it
cannot find it names and counts rather than passing over, and a run that could
not check a single package fails.

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

CMake compiles nothing itself — the Go toolchain is the compiler and the task
runner owns the flags, so the three ways in cannot drift apart. The project
declares no languages, so no C compiler is needed; on Windows that also means
the generator must not want one, which is why Ninja (one executable, no
installer) is a good pick.

Cross compiling covers Windows and Linux, for `amd64`, `arm64`, `386`, `armv7`,
`mips` and `mipsle`. The last three put in the file name what `GOARCH` alone
does not say: `armv7` is the instruction set of the 32 bit ARM build, and
`mips-softfloat`/`mipsle-softfloat` are for the routers, which have no floating
point unit. Both MIPS byte orders are there because both kinds of board are: the
Atheros targets (ath79, ar71xx) are big endian, the MediaTek ones (ramips,
mt76x8) little endian. On a router, or any machine without a desktop, use `run`:
the tray needs a desktop session, and the settings page is reached over the
network with `--settings`.

Every Linux target is built twice. The second file has `-notray` in its name: no
tray, and with it no D-Bus client and none of the rest only a tray needs, which
makes it about a fifth smaller (10.0 MiB to 7.7 MiB for `mipsle`). That is the
one for a router. A start with `--app` is refused there rather than ignored,
since the tray is what opens that window. The build that keeps the tray is the
default one, for a desktop.

macOS is the exception to all of it: its tray needs cgo, so that build has to be
made on macOS with the Xcode command line tools installed (`xcode-select
--install`).

The Windows executable carries an icon, version information and a DPI aware
application manifest, embedded from `winres/rsrc_windows_<arch>.syso` — one file
per architecture (`amd64`, `arm64`, `386`), which is how the toolchain tells
which one belongs to which build. The icon and those files are all generated,
from the drawing code and from `winres/winres.json`; the `icon` and `resources`
tasks regenerate them. They are written inside `winres/`, next to the files they
are made from, and they are reproducible, so rerunning the tasks changes nothing
unless the artwork did.

## Usage

Double-click `dist\SDAutoLogin.exe`, or run `dist/SDAutoLogin` elsewhere. The
program puts up a tray icon and keeps checking in the background. On the first
run, with no account stored yet, the settings page opens by itself.

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

The page is served from `127.0.0.1` on a random port, protected by a one-time
token only the tray can hand out, so another local program cannot read or change
your configuration. After 15 minutes without a request it shuts itself down and
releases the port, and starts again the next time the page is opened.

The address handed out is good for **one** opening: a new one is made for every
opening the program hands out and for every one that is used, and each is written
to the log as it is made. The current address is therefore always the newest of
those lines.

On a machine with no tray, `run --settings=ADDR` serves the same page on an
address you choose. That endpoint starts at once and stays up until the program
stops; when ADDR is not a loopback address the page is reachable from the network
with the token in its address as the only protection, so keep it on a trusted
network or behind a tunnel.

**General**

- The login list: one entry per account, each with the service types it may be
  used with — the campus network, China Mobile (`@zgyd`), China Unicom (`@cucc`),
  China Telecom (`@ctc`). Drag the entries, or move them with the arrow keys, into
  the order they are tried in; see below.
- Whether the passwords are written to the config file.
- Interface language.
- Sign in automatically after a disconnection.
- Move a session that is already up onto the first service type of its account,
  and, on the card of the entry that session is on, the controls that move it onto
  any of the other service types that entry lists — the portal's own page among
  them, once the portal has refused to move it.
- Run at startup.
- Check interval, and the reachability probes.

**Advanced**

- Portal home URL and authentication API URL.
- Account prefix, combined with the account and the service type into the
  submitted account.
- Request timeout and the `jsVersion` parameter.
- Verbose logging.

Changing anything marks the page unsaved; **Save** writes `config.json`, restarts
the check loop and runs one check straight away, so the effect is visible at once.
The test button on an entry waits for a save, since the program can only be asked
about the list it is running with.

## Accounts, and the order they are tried in

The portal allows one session per client address, so several accounts are never
online together. The list is for the day the first account is the one you want
and stops working — out of credit, password changed, closed — and the program has
to walk on to the next one:

```
1  s1234567   China Mobile, then China Unicom
2  s7654321   campus network
3  s0000003   China Telecom                      (disabled)
```

1. Entries are tried from the top down, and each one is submitted once per
   service type it names, in the order it names them: line 1 above tries
   `s1234567@zgyd`, then `s1234567@cucc`, before moving on to `s7654321`.
2. An answer that blames the account itself — wrong password, failed
   authentication — takes that entry out of the running for ten minutes and
   carries on with the next one. Long enough not to hammer the portal with a
   password that is wrong, short enough to pick up a fix; saving the
   configuration or asking for a check clears it at once.
3. An answer about the portal rather than the account — busy portal, a timeout,
   an unreadable reply — keeps the entry and ends the round there. Walking the
   list on a busy portal would mark every account broken in one go.
4. The page marks the entry your session is on, and every entry being left alone
   with the reason and the time left.

A disabled entry takes no part at all, so you can take one out of the running
without deleting its password.

**Moving a session that is already up.** The order of an entry's service types is
what the program follows when it signs in itself, and a session that was already
up when the program started — one you signed in by hand, or one a previous run
left — is not something the portal will move. One session per address means the
only way onto another service type is a login that replaces it, so the program
signs in again as the first service type of the entry that session belongs to,
and asks again an hour later while the session stays put.

A portal that will not sign in again while a session is up is reported and left
as it is, and the entry is not taken out of the running for it — the account was
not what was refused. Your session is up and working, so the settings page, the
header pill and the tray say "needs attention" rather than showing an error. The
card of the account in use has the controls for that: the service type the
session is on, the one its entry puts first, and a way to sign in again as any of
the other types the entry lists. It works whether or not the option above is on.
Once a refusal has been reported, the card also offers the portal's own page in a
browser — a portal that refuses any second sign-in while a session is up has to
be answered there, by signing the session out.

## Command line

Useful on a server, over SSH, in a script. The command line stays English.

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

Options are read on every run and apply to that run only, so a container or a
service without a terminal can be provisioned without the setup wizard — add
`--save` to write them down as well. `run` reports a missing account and exits
rather than asking for one; a service has nobody to ask.

The account options describe the list as a whole rather than adding to it, which
is what a script expects:

```sh
# one account, for a service or a container
SDAutoLogin run --account=s1234567:SECRET@zgyd --save

# several, in the order they are to be tried; an entry may name more than one
# service type, and the @ introduces them
SDAutoLogin run --account=s1234567:SECRET@zgyd+cucc --account=s7654321:OTHER --save
```

A password that holds an `@` cannot be written in that form, since the `@`
introduces the service types; `--username` and `--password` take it.

There is a better place for the password when you are deploying this as a
service. A command line is in the process list of every user on the machine, so
`--password-file=PATH` reads it from the first line of a file instead, and a
systemd unit that hands the secret over with
`LoadCredential=password:/etc/sdautologin/password` needs no option at all: the
program reads `$CREDENTIALS_DIRECTORY/password` by itself when nothing else named
a password. Either way only the line ending is dropped, since a password may end
in a space.

The same executable serves both modes: started with no command it puts the tray
icon up and keeps running, started with one it does that one thing and exits.
`-cli` is short for `run`, `-test-login` is short for `login`.

`--app` keeps the tray but stops treating the settings page as a page: it opens
it in a browser window with no address bar, no tabs and no menus, so it looks like
an application of its own. Only the Chromium family can do that, so Chrome, Edge,
Chromium or Brave is started with the window and the rest of its chrome left out;
when none of them is installed, or the one that was found refuses to start, the
page goes to your default browser instead and the log says so.

On Windows the file is linked as a GUI program, so a double click does not open a
console window. Started from a terminal it attaches to that terminal, so the
output is visible there. Where the output has to be piped, or the exit code read
by a script, build the console variant instead — same source, without
`-H=windowsgui`:

```sh
go run ./tools/build console
```

`status` exit codes: `0` online, `1` state unknown or failure, `3` the portal
says the client is not logged in. Handy in a monitoring script. `status --json`
prints the same check as one line of JSON, and the log goes to stderr, so only
the JSON reaches stdout:

```sh
SDAutoLogin status --json | jq -r .state
```

```json
{"portal":"http://10.9.1.3/","state":"online","reachable":true,"known":true,"online":true,"clientIp":"203.0.113.7","account":"s1234567","onlineSince":"2026-01-01 00:00:00","detail":"online"}
```

`state` is one of `online`, `offline`, `unknown` (the portal answered but said
nothing usable) or `unreachable` (it did not answer at all). The last two share
exit code `1`, and the state is what tells them apart.

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
`http://www.baidu.com`, `http://www.msftconnecttest.com/connecttest.txt`. A probe
counts as successful when it answers normally and shows no portal markers, so a
captive portal does not look like a working Internet connection.

Autostart entries: the registry key
`HKCU\Software\Microsoft\Windows\CurrentVersion\Run` on Windows,
`~/.config/autostart/sdautologin.desktop` on Linux, and
`~/Library/LaunchAgents/com.sdautologin.plist` on macOS.

## Files

- `config.json` — settings, readable by its owner alone; on Windows it gets an
  access list for that account instead, since the mode bits mean nothing there.
- `logs/app.log` — the log, in a directory of its own, both readable by their
  owner alone, rotated to `app.log.1` past 4 MiB. `--no-log` and the `logToFile`
  setting leave the file out, which is what a machine with a small flash — a
  router — wants: the same lines go to the console either way.

Both live next to the executable by default, so a build in `dist/` keeps its
settings and log there, and moving the executable means moving `config.json` with
it. `--home=DIR` puts them in DIR instead, which the service and the container
below use so the executable can stay on a read-only `/usr`.

## Deployment

`run` is the mode to use: it keeps checking with no tray and no display, logs to
the standard error, and exits on `SIGTERM`. It refuses to start without an
account, so write one down first — on the command line with `--save`, or from the
settings page when the service is started with `--settings`.

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
`sdautologin` user it runs as; the licence and the documents. Settings and log
live in `/var/lib/sdautologin`, the service is enabled by the package, and every
one of them has to be given an account before it stays up, since a service has
nobody to answer the setup wizard. OpenWrt is the one that differs, having no
systemd and no `/var/lib`: its section below has the details.

The packages are written by the build task itself with the standard library
alone, so none of them needs a packaging toolchain installed, and a deb or an ipk
needs nothing at all. The Arch package is the exception: Go has no zstd or xz
writer, so it is packed with `zstd` when that is installed, with `xz` or `gzip`
when it is not, and the task says which it used.

Nothing is signed, which is fine for a local install: `dpkg -i`, `rpm -i` and
`pacman -U` all take it, pacman only complaining when its configuration asks for
signatures and `LocalFileSigLevel` is not `Optional`. A repository of your own
would need a key, and a key is not something a build task can invent. What is
written instead is `SHA256SUMS` next to the packages, one line per file in the
spelling of the `sha256sum` tool, so a downloaded release can be checked against
it.

`go run ./tools/build verify` reads every package back with the tool that
installs it — `dpkg-deb` for the deb and the ipk, `rpm -K` and `rpm -qpl` for the
rpm, `bsdtar` and the listing inside the package for the Arch one — compares the
Windows archives with the builds they were made from, and checks that the Scoop
and winget manifests carry the digest of the archive that is there. Then it packs
everything a second time and fails if a single byte came out differently: a
release built twice has to be the same release.

All of it runs when a tag is pushed, in
[.gitea/workflows/ci.yml](.gitea/workflows/ci.yml), together with `go vet`,
`go test` and a `gofmt` check, so a package that would not install is caught
before it is published. Nothing else starts it: a branch is checked from the
checkout, with those same tasks, instead of on the runner.

Pushing a tag publishes the release — the same run creates it and attaches the
packages and `SHA256SUMS`, which the two manifests point at. The version is read
from that tag, so the packages are named after it and the manifests agree with
what is attached by construction: the tag is `v` plus the version, `v1.0.0` for
1.0.0. The same version goes into the program itself, which reports it with the
commit it was built from, and into the version resource of the Windows build; the
sources carry it as well, for a tree that is unpacked rather than checked out, and
a test holds them to the tag. A release that already exists is taken as it is, so
a tag pushed twice does not fail the run — each file replaces the attachment that
carries its name, since this instance keeps two attachments of one name instead
of replacing the first. A run that finds files already attached says so: a tag
published twice usually means it was moved, which is a new release wearing the
version of the old one.
[.gitea/scripts/dedupe-release-assets.sh](.gitea/scripts/dedupe-release-assets.sh)
clears up a release that was doubled before that, keeping the newest attachment
of each name.

### Debian, Fedora, RHEL, openSUSE and Arch

Three steps on all of them: install the package, write the account down, restart
the service.

```sh
# Debian and Ubuntu
sudo apt install ./dist/packages/sdautologin_1.0.0-1_amd64.deb

# Fedora, RHEL, openSUSE
sudo rpm -i dist/packages/sdautologin-1.0.0-1.x86_64.rpm

# Arch
sudo pacman -U dist/packages/sdautologin-1.0.0-1-x86_64.pkg.tar.zst

# then, on any of them
sudo -u sdautologin /usr/bin/SDAutoLogin --home=/var/lib/sdautologin \
     --username=s1234567 --password=SECRET --isp=@zgyd --save config --show
sudo systemctl restart sdautologin
```

The package creates the `sdautologin` user, enables the service and starts it;
without an account it stops after a few tries, as the postinst says when it finds
no `config.json` yet. `/usr/share/doc/sdautologin/README.Debian` has the same
steps on the machine. Removing the package leaves the settings and the log where
they are, and so does purging it: the account in them is not the package's to
throw away.

To look at what was installed: `rpm -K` checks the digests and `rpm -qi` the
header, `rpm -e sdautologin` removes it and leaves `/var/lib/sdautologin` alone;
`pacman -Qi` and `pacman -Qkk` work on the Arch package, the second one checking
every file against the listing it carries.

### Windows

The archive holds the program, the readme and the licence, and nothing else:
there is no installer, because there is nothing to install. Unpack it where it is
to stay and start the program; it writes its settings next to itself, so that
folder has to be kept.

`dist/packages/manifests/` holds what the package managers that can install such
an archive want: `scoop/sdautologin.json` and the three files of the winget
manifest. Both name the archives of a release as their download, at
`<project address>/releases/download/v<version>/`, where Gitea — the host this
project is published on — serves the attachments of a release. Should releases
move elsewhere, change `releaseURL` in `tools/build/pack_win.go` and both follow.
The winget command line checks them
(`winget validate --manifest dist/packages/manifests/winget`), and the Scoop one
declares the settings and the log as persistent, so an update keeps the account.

### systemd

Two ready-made units are in [packaging/systemd](packaging/systemd), one for a
system service and one for `systemctl --user`; the user unit opens with its own
installation steps. The system one is what the packages install, so the steps
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
`ssh -L 8088:127.0.0.1:8088 the-server`. Then open the address the log prints —
`journalctl -u sdautologin` shows it — rather than the bare port, because the
address carries the one-time token the page is opened with and a request without
it is refused. It is good for one opening, and each new one goes to the log, so
the newest line is the one that works.

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

The two MIPS builds are packaged, one per byte order:

| Package | For |
| --- | --- |
| `sdautologin_<version>-1_mips_24kc.ipk` | big endian boards, such as the Atheros ones (ath79) |
| `sdautologin_<version>-1_mipsel_24kc.ipk` | little endian boards, such as the MediaTek ones (ramips, mt76x8) |

A router has no systemd, so this package installs a procd init script at
`/etc/init.d/sdautologin` and a UCI configuration at `/etc/config/sdautologin`.
It also has the least flash, so the program is installed compressed as
`/usr/lib/sdautologin/SDAutoLogin.gz`, and `/usr/bin/SDAutoLogin` is a short
script that unpacks it into `/tmp` on the first start and runs it from there:
2.7 MiB of flash instead of 7.7, against the memory it lives in while running.
The copy in `/tmp` is unpacked once per boot and once per upgrade, and the name
it is unpacked to carries the version, so a new package unpacks its own program
rather than running the one the package before it left behind.

```sh
go run ./tools/build openwrt
scp dist/packages/sdautologin_*_mipsel_24kc.ipk root@router:/tmp/
ssh root@router opkg install /tmp/sdautologin_*_mipsel_24kc.ipk
```

Installing enables the service, so it comes up with the router from then on. Give
it the account:

```sh
uci set sdautologin.main.username=s1234567
uci set sdautologin.main.password=SECRET
uci set sdautologin.main.isp=@zgyd
uci commit sdautologin
/etc/init.d/sdautologin restart
```

An option left empty is not passed on, and then `config.json` decides, which the
settings page writes. That page can be turned on for a router with no display,
and reached from the network it serves:

```sh
uci set sdautologin.main.settings=0.0.0.0:8088
uci commit sdautologin
/etc/init.d/sdautologin restart
```

Then open the address `logread -e SDAutoLogin` prints, with `127.0.0.1` in it
replaced by the address the router answers on, since the page is served from
every interface but the log names the loopback one. The port alone is refused:
the address carries the one-time token, and it opens the page once, so the newest
of those lines is the one that works. With an address that is not loopback the
token is the only protection, so keep it on a trusted network or reach it over a
tunnel.

`/etc/config/sdautologin` is declared as a configuration file, so an upgrade keeps
the account in it, and `config.json` lives in `/etc/sdautologin`, which is on the
overlay and survives a reboot and an upgrade.

The log file is off by default here, since `/etc` is on the flash: the program
writes the same lines to the console, procd hands them to `logd`, and
`logread -e SDAutoLogin` reads them back out of memory. Turn it on at the cost of
up to eight megabytes of flash once the log has grown to its limit:

```sh
uci set sdautologin.main.log_file=1
uci commit sdautologin
/etc/init.d/sdautologin restart
```

#### Building it into an image

A router can also carry the program from the start. `packaging/openwrt/Makefile`
is an OpenWrt package for that, and the Go build system it needs comes from the
packages feed:

```sh
cd $OPENWRT
./scripts/feeds update packages
./scripts/feeds install golang
mkdir -p package/sdautologin
cp -r /path/to/SDAutoLogin/packaging/openwrt/. package/sdautologin/
make menuconfig          # Network -> sdautologin
```

It installs the program uncompressed: a squashfs compresses everything it holds,
and better than gzip does (1.9 MiB against 2.7 for this program), so compressing
it there would add a megabyte to the image for nothing. Leaving it alone also
keeps it a file in the read-only filesystem rather than a copy of it in memory.
The init script and the UCI configuration are the same two files the package
above installs.

## Interface language

`auto` follows the system language; `zh` and `en` force one. The setting covers
the tray menu, the notifications and the settings page. Log files, the command
line and the source comments stay English on purpose, so a pasted log is readable
by anyone.

## Security notes

- The settings service listens on `127.0.0.1` only, requires a per-run random
  token, and shuts down when idle. Every phase of a request has a deadline, the
  token is compared in a time that does not depend on how much of it matched, and
  every answer — the refusals included — carries the security headers.
- That token travels in the address the tray hands out, since a navigation is the
  one request a browser makes on its own and the one that cannot carry a header.
  It is exchanged for a session cookie before the page is served, and the browser
  is sent to the same page without it, so the address bar, the history and the
  `Referer` of anything the page links to carry no token. Every request after that
  is checked against the cookie, a request that changes something has to repeat it
  in a header, and the address itself is good for one opening.
- The password has to be there for an unattended login, so it is in `config.json`
  in clear text unless `rememberPassword` is off. Both that file and the log — the
  log names the account too — are `0600` in a `0700` directory. A file an earlier
  version or a copy left readable by anyone else is tightened at startup, and the
  log says what was found and what was done; on Windows, where mode bits do
  nothing, the file gets an access list naming only the account the program runs
  as. The systemd units set `UMask=0077` and the system service keeps its state
  directory at `0700` as well.
- The settings page writes the password field and never reads it: it is told
  whether a password is stored, never what it is. So the secret is not in the
  browser, not in the script the page loads, and not in a request the page makes.
  Leaving the field empty while saving keeps the stored password, which is what
  every entry you did not touch sends; it has to be typed only for an entry that
  has none — a new one, or one whose account was renamed.
- The program never calls the logout endpoint, so quitting cannot leave the
  machine offline.

## Project layout

The usual Go layout: `cmd/` holds the executable, `internal/` the packages it is
built from. Nothing outside the module can import them, so the public surface is
the command line and the settings page.

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

- **Nothing happens after a disconnect.** Look in `logs/app.log` for the
  `session is down, reconnect attempt #N` lines and the portal reply after them.
  A `ret_code` of `1` means the account or password is wrong, `3` means the
  portal is busy.
- **The icon stays grey.** The portal page did not answer, so the state is
  unknown and nothing was submitted. Usually the machine is not in the campus
  network any more.
- **A check reports unknown.** The portal answered, but the reply had no `result`
  field — a captive portal intercepted it, for instance. The last reply is in the
  log.
- **The portal moved.** Update the portal home URL and the API URL on the
  Advanced tab.

## Licence

MIT, see [LICENSE](LICENSE).
