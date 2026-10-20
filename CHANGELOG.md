# Changelog

**English** | [简体中文](CHANGELOG.zh-CN.md)

## Unreleased

### Signing, and the pair a release carries

- `sign` writes `SHA256SUMS` itself before signing it, so the signature always
  covers the packages that are really in the directory, and it is the last step
  that touches `dist/packages`: pack, sign, verify, publish. `verify` used to run
  before the signing step, where it compared a signature that was not there yet
  and passed: an unsigned list left `dist/packages` without anything saying so.
  It reads the pair on the disk now, and a list with no signature is a fault of
  the release rather than a note.
- The list no longer sums its own signature. It used to, so signing changed the
  file it had just signed, and packing everything again after a release had been
  signed left a `SHA256SUMS` that no longer matched the `.sig` beside it. Nothing
  installs such a release, and nothing in it said why. A list written again with
  different contents now drops the signature rather than shipping one that signs
  something else, and `verify` checks an existing `SHA256SUMS.sig` against the
  list and the key this build carries before it reads the packages back: a pair
  that does not match is a fault of the release, not of the machine reading it.
- The private half of the signing key is written outside the checkout —
  `signing.key` under the per-user configuration directory, or the file
  `SDAUTOLOGIN_SIGNING_KEY_FILE` names — and signing reads it from
  `SDAUTOLOGIN_SIGNING_KEY` alone. It used to fall back on a `signing.key` in the
  working tree, so being able to read a checkout was enough to sign a release
  that every installed copy accepts. **Move an existing `signing.key` out of the
  repository**: it is no longer read from there, and a key left in a tree is a
  key in every copy of it.

- Installing a release reads which archive it wants out of the signed list
  rather than out of the release's own label, and refuses a label the list does
  not agree with. A machine could be moved backwards onto an older release that
  was genuinely signed — the list and the archive both held — because nothing
  compared the version with the one already in place. An update never goes
  backwards now, and a build that cannot name its own version installs nothing.
- The release page is read and the archive is downloaded only over https and
  only from the host the build was published on, and a redirect that leaves that
  host is refused. The addresses the forge reports were followed wherever they
  pointed.

### Defects

- The settings service served the page's own script, stylesheet and mark without
  checking the session, so a program that found the loopback port read them
  without ever being handed the address. They go through the same check as
  everything else now.
- A second copy of the program on a machine without a desktop made the socket it
  asks the running copy for the settings page on before it knew whether it was
  that copy. Started against a running copy that had none of its own, it
  answered its own request and exited: the reader asked for the window and
  nothing opened. The lock is taken first now, and only the copy that holds it
  publishes a socket.
- The deadline for writing a reply was worked out when the settings service
  started, so changing the request timeout on the page kept the old one until the
  program was restarted: it either cut off a reply the reader had just allowed
  more time for, or held a connection open for a wait nobody asked for any more.
  Every answer carries the deadline of the configuration in force when it
  arrived.

- The settings page's one-time token was written into `logs/app.log` every time
  an address was made for it, including one made while serving the page and never
  handed to a browser, and the wording that sent a reader to the newest address
  in the log pointed at the live token. The log outlives the token and is copied
  around, so the address is told to the browser that opens it, or on the console
  of a machine with no tray, and a list of the settings is no longer a way in.
- The portal client was reached through a proxy when `HTTP_PROXY` was set in its
  environment, and it followed a redirect off the host it was told to use, and
  read a reply of any status as an answer. The password is in that URL, so the
  client goes straight to the portal now, refuses to be sent to another host, and
  treats a reply that is not 200 or 204 as no answer at all.
- The container image kept key material out of its top level only. The ignore
  file named `*.key` and `*.pem` without a directory part, and a pattern with no
  slash is matched against the name at the top of the build context alone, so a
  key under `internal/` or `cmd/` was copied into a layer with the sources and
  stayed there, where anyone who can pull the image can read it. Measured with a
  decoy file rather than reasoned about: the patterns carry `**/` now.
- A reply from the portal was written into the log as it arrived, so a reply that
  contained a newline wrote a second line that looked like the program's own.
  What any of it wrote is escaped now, in one place, for every caller.
- A portal address that the program cannot use was carried on being used:
  `config.json` was read without checking the addresses in it, so an address that
  was not a URL, or one that named another host, decided where the password went.
  A file that is read is checked, and an address the program cannot use is put
  back to the one it ships with.
- `config.json` was written in place, so a write that failed half way left a
  file that would not parse, and a symbolic link in its place was followed and
  written through. It is written to a file of its own and moved into place, a
  link is refused, a directory anyone but its owner can write is refused, and a
  password file that is not a regular file, or is larger than 64 KiB, or is
  readable by others, is named instead of used.
- A password given for one run by `--password` was written into `config.json`
  by a save from the settings page, and a mis-typed option such as `--pasword=`
  was echoed back into the log with the password in it. A run's password is left
  for the run, and a word that is not an option is named without its value.
- The lock that keeps one copy of the program running was held in a file in
  `/tmp` at a name anyone could guess, so any account on the machine could take
  it first and keep the owner's copy from starting. It is kept where only its
  owner writes now, and the socket a second copy asks for the window on is
  answered only by the same account.
- Two accounts were submitted one after the other in the same check with nothing
  between them, so a portal that was collecting passwords was handed the whole
  list within a round trip of itself. There is a wait between two entries now,
  and a portal that cannot be reached is asked less often instead of every check.

### Tests

- The test task type-checks the Windows build as well, so the files that only
  build there are read by the compiler in continuous integration instead of only
  when a package is packed.
- The drawing behind the icon, the arithmetic of the tray's double click, and the
  pixel layout the Windows icon is built from have tests, which they did not.

## v1.3.0-alpha.2 - 2026-10-13

### A newer release

The program can look for a newer release of itself and, on Windows, replace
itself with it. Both settings default to the value that changes nothing.

- `checkForUpdates` is `false` unless it is turned on, and `updateChannel` is
  `stable`, which passes over every release labelled a pre-release. Both are on
  the settings page.
- A check writes what it found to the log. A copy that can write the folder it
  runs from, and that no package manager put there, replaces itself; one from a
  package or an image reports the release and names the package manager as the
  thing that updates it. The Linux service cannot write the file it runs from.
- Nothing is installed unless the release is signed. `SHA256SUMS` is signed with
  ed25519, and the signature is checked against a key compiled into the program;
  the digest of the archive comes out of that verified list. Nothing is written
  over the program before both hold.
- A build with no signing key compiled in reports the release and installs
  nothing, and says so in the log.
- A release page that cannot be reached is a line in the log: the login check is
  not affected.

An existing configuration needs nothing done to it.

### Upgrading

- This is a pre-release, so the `stable` channel passes over it. Turn on
  `checkForUpdates` and set `updateChannel` to `prerelease`, or install it by
  hand. The project's current release stays v1.2.0.
- No earlier release can move to this one, because none of them can look for a
  newer release at all. Install this one by hand, and later releases can be
  installed over it.
- This is the first release signed with a key, so it is the first one a later
  release can be verified against. A release signed with any other key is
  refused.

### Defects

- A login that timed out or could not be sent wrote the password into the log,
  the settings page and the notification: the error the transport makes repeats
  the address the request went to, and the password is in that address.
- A portal that answered without saying anything usable about the client was
  reported as an error. The session is neither known up nor known down, so the
  state is its own now and the tray no longer paints it red.
- The log measured the offline time from the last reading that found the session
  online, so every round whose answer could not be read claimed an outage that
  never happened, and the round that found the session again announced one.

## v1.3.0-alpha.1 - 2026-10-07

The first pre-release: the new work can be tried before it becomes the release
everyone gets.

### The password file

A configuration can name a file the passwords are read from, so they need not
be in `config.json`.

- `passwordFile` names the file, and `--password-file=PATH` names one for a
  single run. A systemd unit can hand the same file over as a credential.
- A file with no colon on any line holds one password, for the first entry of
  the login list.
- A file with a colon holds one `account:password` per line, split on the first
  colon, filling the entries it names. An entry it does not name keeps its
  password in `config.json`.
- A name no entry uses is reported in the log.
- What the file gives is never written back. `--save` writes the path down.
- The settings page has a field for the path, and a card whose password comes
  from it names the file and closes its password field.

### Defects

- The portal's reply is masked before the log or the settings page sees it, so
  a portal or a proxy that echoes the login request cannot put the password
  there.
- A save stops the check loop and waits for it. Two saves at once could leave
  two loops, or lose the later one.
- Saving creates the settings directory, so a fresh OpenWrt install starts. It
  failed on a missing `/etc/sdautologin`, and procd gave up after five
  restarts.
- A timeout while the session is moved onto its preferred service type is
  retried on the next check. The program reported it as a portal refusal, and a
  refusal waits an hour.
- A login the portal accepted and has not listed yet is unconfirmed, not a
  failure. It counted toward the failure streak, and the recovery that followed
  never happened.
- `--save config` keeps the password it was given, and the wizard no longer
  reports success over a configuration that cannot sign in.
- The wizard takes the password without echoing it to the terminal.
- The settings page reports a refused save, and asks before a reload discards
  unsaved edits.
- A check that fails raises one notification per outage, with a reminder every
  five minutes. It raised one every fifteen seconds.
- A program installed on the system path keeps its settings in the per-user
  directory, not in `/usr/bin`.
- The service and a copy started by hand take the same instance lock and find
  the same socket, so two copies do not run at once.
- The OpenWrt configuration can hold the password; it is `0600` now, and a
  named password file wins over the `password` option.
- Answers 9 and 10 are about a logout the program never performs, and no longer
  drop an account from the rotation for ten minutes.

### The log and the reload

- `--quiet` leaves out the per-check detail and keeps the lines that report a
  change of state. A check every fifteen seconds filled a router's log ring
  within the hour.
- `SIGHUP` re-reads `config.json` and restarts the check loop without stopping
  the program. The systemd units carry `ExecReload`, so `systemctl reload
  sdautologin` applies an edited file.
- The OpenWrt package passes `--quiet` by default.

### Upgrading

- This release is marked as a pre-release, so it has to be opted into. The
  project's current release stays v1.2.0.
- An existing configuration needs nothing done to it. `password` in
  `config.json` is still read, and a run that names no password file behaves as
  before.
- The program does not create a password file. Keep it `0600`; it is read and
  never written back.

## v1.2.0 - 2026-09-24

### The mark

The mark is a seal: two rings around a roundel, a padlock inside.

- Outer ring darker, a gap, the accent disc, and a white padlock at 16 px.
- Fixed colour is deep crimson under an ink ring; the tray tints it per state.
- The settings page takes the crimson as its accent, brightened in the dark
  palette.
- The artwork stays generated: `go run ./tools/build icon` redraws the Windows
  icon, `resources` builds the resource files.

### The page

The settings page is an application now: sidebar, header, readings strip.

- Fields flow into one, two or three columns, past the 1040 px column; one
  breakpoint is left, where the sidebar becomes a strip.
- The page is grey, the panels white, with hairlines; the crimson goes on the
  primary action, links and focus rings.
- Four text sizes carry attention; a field and a button are both 32 px tall.
- General is two columns: the account list beside the two panels of settings.
- Every button keeps its box, the secondary ones included.
- The header pill says what the session is doing and as whom: "online · 23D001
  (China Mobile)".
- The action bar sits at the end of the form, clear of the panel it saves.
- Placeholder text and selected text take palette colours, the scrollbars are
  thin, a long path wraps, and less motion is honoured.
- The first-run wizard can be left for later; no step saves on its own.

### The service type a session is on

A session already up at start is moved to its entry's configured service type.

- Such a session is signed in again as the entry's first service type; with
  the option off, or automatic login off, it is left alone.
- A refused move leaves the session alone and is retried an hour later.
- A refusal is shown on the card, the header pill and the tray tooltip, with one
  balloon tip.
- That card can sign in again as any other service type the entry lists, and
  offers the configured portal home page in a browser, never a page the portal
  asks for.
- The service type comes from the account the portal echoes back.

### The password

The password sits in `config.json` in clear text.

- The settings page writes the password field and never reads it; an empty field
  on save keeps the stored one.
- The file is written `0600`, and a readable one is tightened at startup with a
  line in the log; on Windows an access list names the run account alone.
- `--password-file=PATH` reads the first line of a file; a systemd unit can pass
  `LoadCredential=password:/path`, from `$CREDENTIALS_DIRECTORY/password`.

### Continuous integration

CI runs on a tag; a branch gets `go run ./tools/build test`, `packages` and
`verify`.

- The release body is the `CHANGELOG.md` section for the tag; a tag without one
  is refused.
- The wiki is written from the repository on every release, with `docs/wiki/`
  for the landing page.

### Defects

- An accepted login is read back a second and a half before it is called
  failed, and an unreadable portal keeps the state at "signing in".
- The account written by `--account=user:pass`, or by `--username` without
  `--isp`, always has a service type, so `run` starts.
- Stopping the check loop waits for it, so a save cannot leave two loops.
- A failed rotation is reported once, and a log that cannot be opened is
  reported by the About tab, the log tail and "open log".
- The wizard's account goes into the list before the save that sends it.
- A password typed into a card stays in it when the card is rebuilt: a drag, a
  new entry, a language change.
- A dropped account keeps its place: the order is read back out of the page.
- The shortcuts follow the rules of the commands: `-cli --json` is refused, as
  `run --json` is a usage error.
- A background run asks nothing on stdin: `run --settings` with no account
  brings up the page, and `config` reports a failed save as non-zero.
- The portal's message is cut to length, and `login` masks the credentials.
- On Linux the tray runs without an icon when there is no session bus, and one
  that cannot be made hands the run to the background mode.
- An autostart entry carries the settings path option and no password.
- The instance lock falls back to the user's cache directory when needed, and
  a run that cannot take it says so.
- A check interval or request timeout that wraps negative is held to a day, and
  to a quarter of an hour.
- Packing refreshes `SHA256SUMS`; `verify` fails on a missing manifest.
- The `resources` task holds the Windows resources to the sources' version, so
  a tag that moved without it cannot leave Explorer behind.

## v1.1.0 - 2026-09-24

This release covers what v1.0.0 could not reach: machines with no desktop, a
package for every system, and several accounts to fall back on.

### Runs where there is no screen

- `run --settings=ADDR` serves the settings page on a named address while the
  process runs, and the page can end that run; a bare address is refused, and
  one reachable from the network is warned about.
- `--username`, `--password`, `--isp` and `--save` write the account down.
- A run with `--settings` starts with no account and logs the address to open;
  without the page, a run with no account is refused.
- `--home=DIR` keeps `config.json` and `logs/` out of the directory the
  executable sits in.
- `packaging/systemd` holds a system service and a `systemctl --user` one.
- The `Dockerfile` builds the static binary from the vendored sources, runs
  unprivileged, and health-checks on `status` with its state in `/data`.

### Watching a session

- `status --json` writes one line: the log on stderr, the result on stdout.
- Both forms report the same exit codes: `0` online, `1` unknown, `3` not
  signed in; the JSON adds a state that separates the two.
- An option that was never defined is refused with exit code 2.

### What only Windows had

- The settings page opens in a window of its own with `--app[=ADDR]`, no address
  bar, tabs or menus.
- The usual browsers are tried in turn, installed ones before PATH; if none
  works the page goes to the default browser and the log says why.
- Reconnects reach Linux and macOS through `notify-send` and `osascript`, with
  the wording from the string table the tray and the page share.
- A second start asks the running copy for the settings page on all three.

### Building, packages, routers

- `go run ./tools/build all` also produces `windows/386`, `linux/386`,
  `linux/armv7` and the two softfloat MIPS builds, since `arm` and `mips` alone
  name no board; the Windows resource file covers `amd64`, `arm64`, `386`.
- Every Linux target is built twice, the `-notray` one refusing `--app`.
- `go run ./tools/build packages` writes a deb, an rpm, an Arch and an OpenWrt
  package into `dist/packages/`, one per architecture.
- Each installs the tray-less build, the systemd unit, the `sysusers.d` file
  declaring the `sdautologin` user, and settings and log in
  `/var/lib/sdautologin`; each asks systemd for that user and starts the unit,
  since nothing reads a `sysusers.d` file on its own, so a missing `config.json`
  is reported.
- Debian and OpenWrt share the container writer; OpenWrt adds a procd init
  script and UCI configuration, and the Arch package packs with `zstd`, then
  `xz`, then gzip.
- The OpenWrt package's architecture follows the boards (`mips_24kc` and
  `mipsel_24kc`); no other format is made for MIPS.
- Packages carry fixed dates, so packing twice gives the same file; nothing is
  signed.
- The payload is checked for Windows line endings, and `.gitattributes` pins
  them under `packaging/`.
- Each format was written against the tool that reads it: `dpkg`, `rpm -K` and
  `rpm -i`, `pacman -Qkk`, `opkg`, which turned up five defects: an rpm
  signature header, absolute member names in a pacman archive, an Arch size
  field in KiB, and two packages that named no directory.
- On the routers the OpenWrt package installs the program gzipped, unpacked at
  first start into memory: 2.7 MiB of flash in place of 7.7, under a versioned
  name, since `/tmp` is not cleared by an upgrade. The log file is off there,
  where `/etc` is on the flash and `logd` has the lines; `--no-log` turns it off
  anywhere, from the `log_file` option.
- `packaging/openwrt/Makefile` packs the program for a firmware image, where
  squashfs holds it at 1.9 MiB against 2.7; removing the package stops the
  service first, since the router runs it out of `/tmp`.

### Secrets on disk

- `config.json` is written `0600`, and the log in its own `0700` directory,
  which names the account and the addresses it was signed in from.
- The state directory is `0700` (`StateDirectoryMode=0700`), both units set
  `UMask=0077`, and the container image makes `/data` `0700`.
- The settings page, the tray and the command line run as the password's user.

### One version

- The version comes from the git tag: `--version` prints `1.0.0+5ccacb9`.
- The Windows version resource takes its number from the tag, and
  `winres/winres.json` holds none.
- The OpenWrt package writes `PKG_VERSION` in its Makefile, and the build passes
  it through `GO_PKG_LDFLAGS`.
- A tree without a checkout falls back on what the sources carry.

### Continuous integration

- `.gitea/workflows/ci.yml` runs on every push and pull request: one job runs
  `gofmt`, `go vet` and `go test`, the other packs and checks every format, both
  inside the `golang` image.
- The format tools come from the runner's mirror, asked directly whether it
  answers: a failed fetch is a warning from `apt-get update`, exit status 0.
- `actions/checkout` and the like need node, so every step is a command.
- The second job runs `go run ./tools/build verify`: each package is checked by
  the tool for its format (`dpkg-deb`, `rpm -K` and `rpm -qpl`, `bsdtar`), and
  everything is packed a second time, where one byte out of place fails it.
- A tool that is not installed is named and counted as unchecked, and a run that
  checked no package fails.
- `SHA256SUMS` is written next to the packages, one line per file in the
  `sha256sum` spelling.
- Pushing a tag publishes the release over the Gitea API and replaces the
  attachment of that name.
- The Windows packaging is `pack_win.go` now, not `pack_windows.go`.
- Every text file's line endings are pinned in `.gitattributes`, `packaging/`
  included; `*.cmd` keeps CRLF.

### Several accounts, tried in order

- The settings hold a login list, one entry per account, ordered by dragging.
- The entries are a failover order: one session per client address.
- An answer that blames the account drops it from the rotation for ten minutes.
- The page is laid out around that list: a card per account, the service types
  as draggable chips, a switch, a test button, and badges for refused entries.
- The test button submits nothing while the portal reports this client online.
- A file written before the list existed is read as one entry of it, and
  rewritten in the new shape only when something is saved.
- `--account=USER:PASSWORD[@SERVICE[+SERVICE...]]` may be repeated; the older
  `--username`, `--password` and `--isp` describe one entry, and a password
  holding an `@` cannot use the one line form, where the `@` marks the types.
- A refused save from the settings page leaves the running configuration alone.

### The settings service

- A request has a deadline at every phase: header, request, reply, idle
  connection, following the configured request timeout.
- The token is compared in constant time, so timing cannot read it out.
- Security headers go out on every answer, refusals included.
- The token leaves the address as soon as the page is served, exchanged for a
  cookie; a changing request repeats the value in a header.
- An address is good for one opening; a fresh one is made for every address
  handed out and every one opened, and the newest log line is the current one.

### Tests

There are 135 cases, up from 52.

- `internal/app` (16) and `cmd/SDAutoLogin` (25): the settings server with its
  token, cookie and security-header rules, and both forms of every option.
- `internal/config` (25): the file, its defaults, what `normalize` clamps, and
  the login list order.
- `internal/service` (21): the check loop against a steered portal, and the
  failover with its ten-minute wait.
- `tools/build` (12) and the small ones, `internal/logging` (4),
  `internal/platform` (7), `internal/i18n` (4), `internal/webui` (1): the
  version against the latest tag, every package container packed twice.

## v1.0.0 - 2026-09-21

First release: a tray program that keeps a campus network (Dr.COM eportal)
session alive, signs back in with the stored account, and never logs it out.

### Layout and build

- Everything the build produces lands in `dist/`, and the resource file moved
  into `winres/`, next to the icon and `winres.json`.
- The single `main` package of 29 files in the root became `cmd/SDAutoLogin`
  and ten packages under `internal/`: logging, i18n and brand, then config and
  platform, then portal, service, app and tray.
- The cleaning task removes only the executables, never the settings and logs.
- The task runner builds `./cmd/SDAutoLogin`, wrappers and CMake included.

### Tray and single instance

- A repeat inside the double click interval Windows reports is one click.
- A second copy asks the running one for its settings page through a named
  event, in the background `run` mode through the log.
- Half built tray state is cleared when the icon cannot be created.

### Tests

There are 52 cases: `internal/config` (14), `internal/portal` (20),
`internal/service` (14) and `internal/i18n` (4), covering the defaults, the
clamps, both result formats and every reconnect path.

### Documentation

Both READMEs describe the layout, the entry point and where the build writes its
files.

### Building

```sh
build.cmd                       # Windows, ./build.sh elsewhere
go run ./tools/build            # lists every task
cmake -S . -B build -G Ninja    # the same tasks through CMake
```

Cross compiling covers Windows and Linux; the macOS tray needs cgo and the Xcode
command line tools.

### Known limitations

- On Linux and macOS the single instance lock is a file in the temporary
  directory; if the system removes it, a second copy could start.
- If the browser cannot be opened, the settings page reports it with a modal
  message box on the UI thread; the tray does not answer until it is dismissed.
