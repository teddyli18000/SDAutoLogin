![Campus Auto Login: a padlock in a round seal](logo.png)

Keeps a campus network session alive from the tray. The portal is asked every
few seconds whether the session is still up, and a session that is gone is
signed back in. The program never logs you out.

**English** | [简体中文](README.zh-CN.md)

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
- [Interface language](#interface-language)
- [Security notes](#security-notes)
- [Project layout](#project-layout)
- [Troubleshooting](#troubleshooting)
- [Deployment](#deployment)
- [Licence](#licence)

## Features

- Tray icon on Windows, Linux and macOS, coloured by session state.
- Settings page served on loopback as a local web page, opened in your browser.
- Chinese or English interface, following the system language by default. Logs
  stay English.
- Options on three tabs: the everyday switches under General, the portal
  endpoints under Advanced, and the version, the update check and the tail of
  the log under About.
- A command line mode for machines without a desktop.
- Notification on a successful or failed sign-in: a balloon tip on Windows, the
  desktop's own notification elsewhere.
- Run at startup, one switch per platform (registry, desktop entry,
  LaunchAgent).
- Per-check logging with rotation. The password never goes to the log.
- Look for a newer release, off by default. A copy that can replace itself is
  updated; one a package manager put there is only told.

Notifications cover a successful sign-in, a failed one, and a portal that
refuses to move the session onto the preferred service type; a failed check is
announced once per outage and reminded at most every five minutes while it
lasts. There is no setting to turn any of them off, and none in the tray menu:
the operating system's own notification settings are the only control.

## How it works

The same endpoints the portal's own login page uses:

| Step | Request | Purpose |
| --- | --- | --- |
| 1 | `GET /` | read `v4ip` from the portal home page, the IP the portal sees (after NAT, not your local one) |
| 2 | `GET ?c=Portal&a=online_list` | ask whether that IP is authenticated; this endpoint answers from inside the campus network even while the session is down |
| 3 | `GET ?c=Portal&a=login` | submit `user_account` and `user_password` with `login_method=1` |

A login is only attempted when step 2 said the portal does *not* consider you
online. If the portal cannot be reached, or the reply is unusable, the state is
unknown and nothing is submitted. After a login the program asks again:
"accepted but still offline" is reported as a failure.

If you turn on the reachability probes, they only feed the status display. The
decision to sign in is always based on the portal itself.

With `checkForUpdates` on, the release page is asked about newer versions as
well. The button on the About tab and the `update` command ask it on demand, and
neither waits for the switch. A release page that cannot be reached is a line in
the log, and the login check goes on as before.

## Build

Go 1.26 or newer. Dependencies are vendored, so the build works offline.
Everything lands in `dist/`; `clean` removes the executables from there and
leaves settings and logs alone.

One command, no extra tooling:

```bat
build.cmd
```

```sh
./build.sh
```

The tasks can be called directly as well, the way the scripts and the CMake
targets call them:

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
go run ./tools/build test       # go vet, the same for Windows, and go test
go run ./tools/build icon       # redraw the icon from the drawing code
go run ./tools/build resources  # regenerate the Windows resource file
go run ./tools/build clean
```

CMake drives the same tasks:

```sh
cmake -S . -B build -G Ninja
cmake --build build                      # builds the program
cmake --build build --target check       # go vet, the same for Windows, and go test
cmake --build build --target SDAutoLogin-console
cmake --build build --target all-platforms
cmake --build build --target packages
cmake --build build --target verify
cmake --install build --prefix /usr/local
```

CMake compiles nothing itself: the Go toolchain is the compiler and the task
runner owns the flags.

Cross compiling and the per-target file names:
[docs/DEPLOYMENT.md](docs/DEPLOYMENT.md).

## Usage

Double-click `dist\SDAutoLogin.exe`, or run `dist/SDAutoLogin` elsewhere. The
program puts up a tray icon and keeps checking in the background. On the first
run, with no account stored yet, the settings page opens by itself, and a
three-step wizard in front of it asks for the language, the account and the
service type; **Not now** leaves the page behind it, where every one of them can
be set as well.

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

`run` reloads the same way on `SIGHUP`.

## Settings

The page is served from `127.0.0.1` on a random port, protected by a one-time
token only the tray can hand out. After 15 minutes without a request it shuts
down and releases the port (the idle check runs once a minute, so it is closed
within a minute of that), and starts again the next time the page is opened.
An address is good for **one** opening. It is not written to the log: the log
outlives the token and is copied around, so an address kept there would be a way
in that outlives the one opening it was made for. A tray hands the address to
the browser it opens; a machine with no tray is told it on the console instead.

On a machine with no tray, `run --settings=ADDR` serves the same page on an
address you choose, from the start until the program stops. The address to open
is printed on the console when the page starts. When ADDR is not a loopback
address the token in the address is the only protection, so keep it on a trusted
network or behind a tunnel.

**General**

- The login list: one entry per account, each with the service types it may be
  used with: the campus network, China Mobile (`@zgyd`), China Unicom (`@cucc`),
  China Telecom (`@ctc`). Drag the entries, or move them with the arrow keys,
  into the order they are tried in; see below.
- Whether the passwords are written to the config file.
- Interface language.
- Sign in automatically after a disconnection.
- Move a session that is already up onto the first service type of its account.
  Any of the other service types that entry lists can be chosen from the card,
  the portal's own page among them once the portal has refused to move it.
- Run at startup.
- Check interval, and the reachability probes.
- Whether a newer release is looked for, and which releases it may move to
  (`stable`, or `prerelease` to try a version before it is one).

**Advanced**

- Portal home URL and authentication API URL.
- Account prefix, combined with the account and the service type into the
  submitted account.
- Request timeout and the `jsVersion` parameter.
- Verbose logging.

**About**

- The version, the platform, the project and the licence.
- Where `config.json` and the log are, and the buttons that open them.
- The update check, run on demand, and the release it last found.
- The last lines of the log.

Changing anything marks the page unsaved; **Save** writes `config.json`,
restarts the check loop and runs one check straight away. The test button on an
entry becomes available after a save.

## Accounts, and the order they are tried in

The portal allows one session per client address, so several accounts are never
online together. The list is for the day the first account stops working (out of
credit, password changed, closed), when the program walks on to the next one:

```
1  s1234567   China Mobile, then China Unicom
2  s7654321   campus network
3  s0000003   China Telecom                      (disabled)
```

1. Entries are tried from the top down, and each one is submitted once per
   service type it names, in the order it names them: line 1 above tries
   `s1234567@zgyd`, then `s1234567@cucc`, before moving on to `s7654321`.
2. An answer that blames the account itself (wrong password, failed
   authentication) takes that entry out of the running for ten minutes and
   carries on with the next one. Saving or asking for a check clears it.
3. An answer about the portal (busy portal, a timeout, an unreadable reply)
   keeps the entry and ends the round there.
4. The portal's `ret_code` decides which group an answer falls in: `1` and `7`
   are the account's own, everything else the portal's, `11` and any code this
   program has never seen among them. So the list is a failover order for `1`
   and `7` alone: an account problem reported under another code keeps that
   entry in place and no later entry is tried.
5. The page marks the entry your session is on, and every entry being left
   alone with the reason and the time left.

A disabled entry takes no part in the rotation. Its password stays in the file.

Moving a session that is already up: one session per address means the only way
onto another service type is a login that replaces it, so the program signs in
again as the first service type of the entry, and asks again an hour later while
the session stays put. A portal that refuses is reported as "needs attention",
not as an error, and the entry stays in the rotation. The card of the account in
use carries the controls: the service type the session is on, the one its entry
puts first, any other type the entry lists, and the portal's own page once a
refusal has been reported.

## Command line

The command line is useful on a server, over SSH and in a script. It stays
English.

```text
SDAutoLogin [command] [options]

run            run in the background, no tray
status         print the current session state once and exit
update         look for a newer release and print what was found; --install
               installs it when this copy can replace itself, --json prints the
               same as one line for a script
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
--quiet            leave out the per-check detail, for a service whose console
                   goes into the system log
--account=USER:PASSWORD[@SERVICE[+SERVICE...]]
                   one entry of the login list; repeat the option to write
                   several, in the order they are to be tried
--username=NAME    use this account, as the single entry of the list
--password=SECRET  use this password instead of the configured one, which on its
                   own is the password of the first entry of the list
--password-file=PATH
                   read the passwords from PATH instead, in the two shapes the
                   passwordFile setting takes; a secret on the command line is
                   in the process list of every user on the machine, so this is
                   the form to use from a script, a service or a container
--isp=SUFFIX       service suffix such as @zgyd, empty for the campus network
--save             write what was given here into config.json as well
--settings=ADDR    run: also serve the settings page on ADDR, for a machine
                   with no tray and no display, for example 127.0.0.1:8088;
                   the address to open is printed on the console
--json             status: print one line of JSON instead of the report
--app[=ADDR]       open the settings page in a window of its own, without the
                   browser around it, on ADDR or on loopback
```

An option can also be written as `--name value`, but the form above is the one
to use when the value holds a character the shell would act on, such as the `@`
of a service suffix. `--app` is the exception: its address is optional and has
to be written with the `=`, so a desktop starts with the bare `--app`.

The account options replace the whole list instead of adding to it:

```sh
# one account, for a service or a container
SDAutoLogin run --account=s1234567:SECRET@zgyd --save

# several, in the order they are to be tried; an entry may name more than one
# service type, and the @ introduces them
SDAutoLogin run --account=s1234567:SECRET@zgyd+cucc --account=s7654321:OTHER --save
```

A password that holds an `@` cannot be written in that form: the `@` introduces
the service types. `--username` and `--password` take it.

Options are read on every run and apply to that run only; `--save` writes them
down as well. `run` refuses to start with no account, unless `--settings` gave it
a page to be configured from, and says why: a machine with no terminal cannot
answer a question.

`--password-file=PATH` takes the passwords from a file, in the two shapes the
`passwordFile` setting below takes. A systemd unit that hands the secret over
with
`LoadCredential=password:/etc/sdautologin/password` needs no option: the
program reads `$CREDENTIALS_DIRECTORY/password` by itself when nothing else
named a password.

The same executable serves both modes: started with no command it puts the tray
icon up and keeps running, started with one it does that one thing and exits.
`-cli` is short for `run`, `-test-login` is short for `login`.

`--app` keeps the tray but opens the settings page in a browser window with no
address bar, no tabs and no menus. Only the Chromium family can do that:
Chrome, Edge, Chromium or Brave is started with the window and the rest of its
chrome left out. When none of them is installed, or the one that was found
refuses to start, the page goes to your default browser and the log records it.

On Windows the file is linked as a GUI program, so a double click does not open
a console window. Started from a terminal it attaches to that terminal. Where
the output has to be piped, or the exit code read by a script, build the
console variant instead: same source, without `-H=windowsgui`:

```sh
go run ./tools/build console
```

`status` exit codes, for a monitoring script: `0` online, `1` state unknown or
failure, `3` the portal says the client is not logged in. `status --json`
prints the same check as one line of JSON, and the log goes to stderr, so only
the JSON reaches stdout:

```sh
SDAutoLogin status --json | jq -r .state
```

```json
{"portal":"http://10.9.1.3/","state":"online","reachable":true,"known":true,"online":true,"clientIp":"203.0.113.7","account":"s1234567","onlineSince":"2026-01-01 00:00:00","detail":"online"}
```

`state` is one of `online`, `offline`, `unknown` (the portal answered but said
nothing usable) or `unreachable` (it did not answer). The last two share
exit code `1`, and the state tells them apart.

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
| `passwordFile` | | file the passwords are read from |
| `portalBaseUrl` | `http://10.9.1.3/` | portal home page, read for the client IP |
| `eportalUrl` | `http://10.9.1.3:801/eportal/` | Dr.COM eportal root |
| `accountPrefix` | `,0,` | prefix the portal page uses for a browser login |
| `checkIntervalSeconds` | `15` | how often the session is checked; anything below 5 seconds or above a day is brought back into that range |
| `httpTimeoutSeconds` | `6` | request timeout; anything below 2 seconds or above 15 minutes is brought back into that range |
| `autoLogin` | `true` | sign back in when the session is gone |
| `switchToPreferredService` | `true` | sign a session that is already up in again as the first service type of its entry |
| `checkInternet` | `true` | also probe the probe addresses |
| `verboseLog` | `true` | write per-check diagnostics |
| `logToFile` | `true` | also write `logs/app.log`, see `--no-log`; this one is not on the settings page, since a machine with a small flash decides it once |
| `probeUrls` | see below | addresses used for the reachability probe |
| `jsVersion` | `3.3.3` | value the portal page sends |
| `language` | `auto` | `auto`, `zh` or `en` |
| `checkForUpdates` | `false` | look for a newer release on the release page |
| `updateChannel` | `stable` | `stable`, or `prerelease` to try a version before it is one |

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

`passwordFile` names a file the passwords are read from, so they need not be in
`config.json`. No colon on any line makes it one password, for the first entry of
the login list; a colon makes it one `account:password` per line, split on the
first, filling the entries it names, and an entry it does not name keeps the
password in `config.json`. A name no entry uses is reported in the log, not
guessed at. Of a password only the line ending is dropped, since one may end in
a space. What is read is never written back; with `--save`, the path is.

A password comes from the first of these that answers:

1. `--password=SECRET`, for the first entry, this run.
2. `--password-file=PATH`, the list, this run.
3. `$CREDENTIALS_DIRECTORY/password`, the systemd credential.
4. `passwordFile` in `config.json`.
5. `password` in `config.json`.

A `--password` on the command line is the more specific instruction and wins
over the credential.

A file written by an earlier version, with a single `username`, `password` and
`accountSuffix`, is read as one entry of the list and saved again in the new
shape.

Default probe addresses: `http://connect.rom.miui.com/generate_204`,
`http://www.baidu.com`,
`http://www.msftconnecttest.com/connecttest.txt`. A probe counts as successful
when it answers normally and shows no portal markers, so a captive portal does
not look like a working Internet connection.

Autostart entries: the registry key
`HKCU\Software\Microsoft\Windows\CurrentVersion\Run` on Windows,
`~/.config/autostart/sdautologin.desktop` on Linux, and
`~/Library/LaunchAgents/com.sdautologin.plist` on macOS.

## Files

- `config.json`: settings.
- `logs/app.log`: the log, in a directory of its own, rotated to `app.log.1`
  past 4 MiB. `--no-log` and the `logToFile`
  setting leave the file out, for a machine with a small flash (a router): the
  same lines go to the console either way.

Both live next to the executable by default, so a build in `dist/` keeps its
settings and log there, and moving the executable means moving `config.json`
with it. `--home=DIR` puts them in DIR instead, which the service and the
container use.

## Interface language

`auto` follows the system language; `zh` and `en` force one. The setting covers
the tray menu, the notifications and the settings page. Log files, the command
line, the source comments and the commit messages stay English, so a pasted log
or a linked commit is readable by anyone.

## Security notes

### The settings service

The service listens on `127.0.0.1` only, requires a per-run random token, and
shuts down when idle. Every phase of a request has a deadline, the token
comparison does not depend on how much of the token matched, and every answer,
the refusals included, carries the security headers.

That token travels in the address the tray hands out. It is exchanged for a
session cookie before the page is served, and the browser is sent to the same
page without it, so the address bar, the history and the `Referer` of anything
the page links to carry no token. Every request after that is checked against
the cookie, the script, the stylesheet and the mark the page loads for itself
included: nothing is served on this port to a program that was not handed an
address. A request that changes something has to repeat the cookie in a header,
and the address itself is good for one opening.

### Where the password is kept

The password has to be there for an unattended login, so it is in `config.json`
in clear text unless `rememberPassword` is off. Both that file and the log (the
log names the account too) are `0600` in a `0700` directory. A file an earlier
version or a copy left readable by anyone else is tightened at startup, and the
log says what was found and what was done; on Windows, where mode bits do
nothing, the file gets an access list naming only the account the program runs
as. The systemd units also set `UMask=0077`, and the state directory of the
system service is `0700`.

A password can live in `config.json`, in the file `passwordFile` names, or in
neither, supplied by the settings page or by the command line for one run. A
password file keeps the secret out of the file that gets copied, synced and
pasted into an issue. It does not encrypt `config.json`: the program signs in
with nobody present, so it has to recover the password by itself. It does not
defend against a process running as the same user, or against root. The password
is still on the filesystem, in the file that was named. Nothing needs setting up
again after a reboot: the file persists, and for the systemd credential the
source file persists while systemd copies it into a private tmpfs at each
service start. The plaintext `password` field stays supported and unchanged, so
an existing configuration keeps working with no migration.

The operating system's own credential store is not used, and the reason was
measured. Of the six shapes the program ships for, only the Windows desktop has
a store that works by itself: DPAPI is part of the system. The macOS keychain is
locked for a launchd daemon or an SSH session, a Linux desktop needs a running
secret service provider and `libsecret-tools` installed, a Linux service starts
before the user's session, and a container and an OpenWrt router have no session
bus. It would also fight the way the program is handed out: a folder copied to
another machine would carry a password that exists and cannot be read there.

### What the page is told

The settings page writes the password field and never reads it: it is told
whether a password is stored, never what it is. The secret stays out of the
browser, the script the page loads and every request the page makes. An entry
you did not touch sends an empty field, which keeps the stored password. A
password has to be typed for an entry that has none, and for one whose account
name changed, since a password is matched to its account by name.

### Updates

A release is installed only after it has proved that this project published it.
`SHA256SUMS` is signed with ed25519, and the signature is checked against a key
compiled into the program; the digest of the archive is then read out of that
verified list. Nothing is written over the program until both hold.

That key lives in `internal/update/publickey.go`, written by
`go run ./tools/build keygen`. A build that carries no key, which is a fresh
clone before that has been run, looks for a release and writes what it found
to the log: it installs nothing.

The private half is written by the same command to `signing.key` under the
per-user configuration directory, or to whatever file
`SDAUTOLOGIN_SIGNING_KEY_FILE` names, and never into the checkout. Signing a
release reads it from the environment variable `SDAUTOLOGIN_SIGNING_KEY` alone:
no file in the working tree is read, so being able to read a checkout is not
enough to sign a release that every installed copy accepts. The release
procedure is in [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md).

### Signing out

The program never calls the logout endpoint, so quitting cannot leave the
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
| `docs/` | deployment guide, and the wiki pages that are not a copy of a file here |
| `.gitea/workflows/` | what Gitea Actions runs on a tag: the tests, the packages and the checks on them |
| `.gitea/scripts/` | the scripts those steps run: the wiki pages and the release cleanup |
| `Dockerfile` | container image, built from the vendored sources |
| `CMakeLists.txt` | CMake front end for the task runner |
| `dist/` | build results, plus the settings and log of a build started from there |
| `build/` | CMake build tree, when CMake is used |
| `vendor/` | vendored dependencies |

## Troubleshooting

- **Nothing happens after a disconnect.** Look in `logs/app.log` for the
  `session is down, reconnect attempt #N` lines and the portal reply after
  them. A `ret_code` of `1` means the account or password is wrong, `3` means
  the portal is busy.
- **The icon stays grey.** The portal page did not answer, so the state is
  unknown and nothing was submitted. Usually the machine is not in the campus
  network any more.
- **A check reports unknown.** The portal answered, but the reply had no
  `result` field, for instance because a captive portal intercepted it. The
  last reply is in the log.
- **The portal moved.** Update the portal home URL and the API URL on the
  Advanced tab.
- **Windows will not start the program at all**, with a message about an
  application control policy, or nothing happens when the file is
  double-clicked. Windows 11 may have Smart App Control on, and it refuses a
  program it has not seen before: the releases are not signed with a Windows
  code-signing certificate, so a fresh download has no reputation to be judged
  by. A copy that has been run once from the same folder usually starts after
  that, which is why a build run in place keeps working while the same file
  copied elsewhere does not. Signing the release is the cure; turning Smart App
  Control off is the alternative.

## Deployment

Packages, systemd, the container and OpenWrt: [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md).

Publishing runs from [.gitea/workflows/ci.yml](.gitea/workflows/ci.yml); the
systemd units are in [packaging/systemd](packaging/systemd), the container in
[Dockerfile](Dockerfile), and a doubled release is cleaned up by
[dedupe-release-assets.sh](.gitea/scripts/dedupe-release-assets.sh).

## Licence

MIT, see [LICENSE](LICENSE).
