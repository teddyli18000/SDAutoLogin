# Changelog

Release notes, newest first. Log files, commit messages and the code stay English;
the interface wording is bilingual.

**English** | [简体中文](CHANGELOG.zh-CN.md)

## v1.2.0 - 2026-09-24

### The mark

The icon was a blue rounded badge. A university says who it is with a seal, so now
it is one: two rings around a roundel, padlock inside. Nothing of any real emblem
is copied — the rings and the padlock come out of primitives, there is no
lettering, and the crimson is only roughly a university red.

- Outer ring in a darker tone of the accent, a gap, the disc in the accent, the
  padlock knocked out in white; proportions held to the 16 px tray icon, which is
  the size that decides whether a mark works at all.
- Fixed colour is a deep crimson under an ink ring, and the tray tints the same
  mark by session state. Being offline no longer shares a colour with being signed
  in, and the moment before the first check answers has a grey of its own.
- The settings page takes the crimson as its accent too — as it is in the light
  palette, brightened in the dark one, since a crimson that carries white text
  cannot be read as text on a dark surface.
- The artwork stays generated: `go run ./tools/build icon` redraws the Windows
  icon and `resources` rebuilds the resource files from it.

### The page

The settings page was a form with a skin on it: warm grey, a crimson rule across
the top, cards with shadows, one column of fields pinned to 1040 px. It is an
application now — a sidebar naming its three parts, a header and a strip of
readings across the top, and a content column that fills the window.

- Fields flow into one, two or three columns as the window has room; each field is
  a label with its control under it, which is what makes that possible. One
  breakpoint is left, and only where the sidebar has to become a strip.
- Grey page, white panels, hairlines between them, and the crimson spent only on
  the primary action, links, focus rings and the current sidebar entry. The top
  rule, the tick before every section title and the coloured pill tabs are gone: a
  control panel is read by its numbers.
- Attention is said with size and weight instead of more colour: four text sizes,
  and a field and a button are both 32 px tall so they line up on a row.
- General is two columns — the accounts, which are a list and want the width,
  beside the two panels of settings.
- Every button keeps its box. Secondary ones drawn as bare words left the test
  buttons reading like part of the sentence they sat in.
- The header pill says what the session is doing and who it is doing it as:
  "online · 23D001 (China Mobile)", taken from the entry in use.
- The action bar is the end of the form rather than a bar floating over it, so a
  panel that has been saved is not covered by the buttons that saved it.
- Small misses are in as well: placeholder and selected text take colours from the
  palette, the scrollbars are thin and tinted, a long path wraps inside its field,
  and a system that asks for less motion gets none.
- The first-run wizard can be left for later: a second button closes it onto the
  settings page behind it. Finishing it closes as soon as the server has taken what
  was typed, and no step saves on its own any more.

### The service type a session is on

A session that was already up when the program started — signed in by hand, or by
an earlier run with another order — was left where it was, so the order of an
entry's service types was a wish rather than what happened, on exactly the machine
that had been set up by hand.

- Such a session is signed in again as the first service type of its entry. The
  portal keeps one session per client address, so a login that replaces it is the
  only way onto another type. Turning the option off leaves the session alone, and
  so does automatic login being off, since moving a session is a sign-in like any
  other.
- Moving is best effort and never a failure: the session is left as it was, the
  entry is not taken out of the running for it (the account was not what was
  refused), and the move is asked about again an hour later — a login every check
  against a session the portal is happy with is not worth it. A new session or a
  save starts the wait over.
- A refusal is said where the session is, not only in the log: the portal's own
  answer on the card of the account in use ("this IP is already online", not a
  ret_code), "needs attention" on the header pill and the strip of readings, and a
  line in the tray tooltip plus one balloon tip when it arrives. The wording says
  what to do about it, since the program has stopped trying.
- That card signs the session in again as any of the other service types the entry
  lists, not only the first one. Once a refusal has been reported it also offers
  the portal's own page in a browser: a portal that refuses any second sign-in
  while a session is up has to be answered there, by signing the session out. The
  address is the configured portal home page, never one the page asks for.
- Which service type a session is on comes from the account the portal echoes,
  which is the account the login was submitted with. An account with no suffix used
  to be read as the entry's first service type, which named a campus-network
  session "China Mobile" on the page.

### The password

Unattended login needs a password the program can read by itself, so it sits in
`config.json` in clear text. What can be done is to close the other places it
appears.

- The settings page writes that field and never reads it: it is told whether a
  password is stored, never the value. Saving with the field empty keeps the stored
  one; an entry with nothing to keep — new, or renamed — is asked for a password.
- The file is kept to its owner: written `0600`, and a file an older version or a
  copy left group- or world-readable is tightened at startup, with a log line
  saying what was found and what was done. Windows has no mode bits, so there the
  file gets an access list naming only the account the program runs as.
- The password does not have to be in the file at all. `--password-file=PATH` reads
  the first line of a file, and a systemd unit can hand it over as a credential
  (`LoadCredential=password:/path`), which the program picks up from
  `$CREDENTIALS_DIRECTORY/password` on its own — a command line is in the process
  list of every user on the machine.

### Continuous integration

CI runs on a tag now, and nothing else. Every push used to build the program, pack
all four formats and verify them by packing a second time, which is a lot of the
runner's time for a change that is often one line of prose. A branch is checked
from the checkout with the same tasks: `go run ./tools/build test`, `packages`,
`verify`.

- What a release says is read out of `CHANGELOG.md`: the section for the tag becomes
  its body, and a tag with no section is refused rather than published with nothing
  to read.
- The wiki is written from the repository on every release. The manual and the
  changelog are copied into it with their relative links pointed at the repository,
  which is what a page outside it can resolve, and the landing page and the sidebar
  come from `docs/wiki/`.

### Defects

A pass over the ways the program can fail, rather than over what it does, turned up
a set of faults that had been shipping.

- A login the portal accepted is no longer reported as failed. The login endpoint
  and the online list are written a moment apart, so the session is read back over
  the following second and a half now, and a portal that cannot be read at all is
  told apart from one that says the client is not signed in — the first leaves the
  state at "signing in" for the next check to settle.
- The account the command line writes always has a service type.
  `--account=user:pass` and `--username` without `--isp` both left the entry with
  none, which made `run` refuse to start with "no account configured" and crashed
  the program on a machine whose session was already online.
- Stopping the check loop waits for it to finish, so saving the settings cannot
  leave two loops talking to the portal at once, one of them with the old passwords
  and the new endpoints.
- The log no longer gives up quietly: a rotation it cannot do is reported once
  instead of being retried for every single line, and a log file that cannot be
  opened at all is no longer claimed by the About tab, the log tail and "open log".
- The page's first run works: the wizard's account goes into the list before the
  save that sends it, and a refused save is shown inside the wizard. On a fresh
  installation the button did nothing at all.
- A password typed into a card stays in it when the card is rebuilt — a drag, a new
  entry, a language change, a refused save — and the stored password is still never
  sent to the page.
- A dropped account keeps its place: the order is read back out of the page rather
  than through the position each card came from, which applied the move a second
  time.
- The shortcuts are held to the rules of the commands they stand for: `-cli --json`
  used to start the background mode with the option dropped, where `run --json` is
  a usage error.
- A background run asks nothing on stdin, so `run --settings` with no account brings
  up the page rather than a wizard nobody is there to answer, and `config` reports a
  failed save with a non-zero status instead of success.
- The message the portal sends back is cut to length before it reaches the log and
  the page, and `login` masks the credentials in the raw reply it prints.
- On Linux the tray runs without an icon when there is no session bus to put one on,
  rather than starting and waiting for a quit that nothing can ask for. A tray whose
  icon cannot be made at all hands the run to the background mode on the settings
  server it had already built, so the page stays the way in and its exit button ends
  the run.
- An autostart entry carries the option that says where the settings live, so a run
  started that way finds the account. No password is written into it, which outlives
  the run in a place anyone can read.
- The instance lock is taken in the user's cache directory when nothing can be
  written to the temporary one, and a run that cannot take it at all says so.
- A check interval or a request timeout large enough to wrap the duration negative
  is held to a day and to a quarter of an hour, rather than turning the loop hot
  against the portal.
- Every packaging task refreshes `SHA256SUMS`, so a package written on its own
  cannot be at odds with the checksums, and `verify` fails on a manifest it cannot
  find rather than passing over it.
- The Windows resources are held to the version of the sources, so a tag that moved
  without the `resources` task cannot leave Explorer naming the release before it.

## v1.1.0 - 2026-09-24

For what v1.0.0 could not reach: the machines that are not a desktop, a package for
every system, a session you can watch and provision from a script, a window of its
own for the settings page, several accounts to fall back on, and the two things the
tray had only done on Windows.

### Runs where there is no screen

- `run --settings=ADDR` serves the settings page on an address you name, from the
  start and for as long as the process runs, instead of the loopback server that
  comes and goes while idle. Open the newest address in the log — the bare address
  is refused. The page can end a background run, and a warning is written when the
  address is reachable from the network, where the token in its URL is all the
  protection there is.
- `--username`, `--password`, `--isp` and `--save` write the account down without
  the wizard, which is what a provisioning script needs.
- A run with `--settings` starts even with no account written down yet, and says so
  in the log with the address to open, since that page is the only way in. Without
  the page, a run with nothing to sign in with is still refused.
- `--home=DIR` keeps `config.json` and `logs/` out of the directory the executable
  was dropped in, so that one can stay read-only.
- `packaging/systemd` holds two units, one for a system service and one for
  `systemctl --user`, each opening with its install steps. The user unit leaves out
  the mount namespace options on purpose: a user manager needs unprivileged user
  namespaces for those, and a unit that refuses to start is worse than one that is a
  little less confined.
- The `Dockerfile` builds the same static binary through the task runner, from the
  vendored sources. It runs unprivileged, keeps settings and log in `/data`, and its
  health check is the exit code of `status`.

### Watching a session

- `status --json` prints the same check as one line for a monitoring script, and the
  log goes to stderr so stdout carries only the result.
- Exit codes in both forms: `0` online, `1` unknown or unreachable, `3` the portal
  says this client is not signed in. The JSON adds a state that tells the two
  sharing an exit code apart.
- An option that was never defined is refused with exit code 2 instead of being
  passed on, since a misspelled one used to be ignored and a script would read a
  report it never asked for. The failure also goes to the log: the Windows build has
  no console to look at.

### What only Windows had

- The settings page opens in a browser window of its own with `--app[=ADDR]`: no
  address bar, no tabs, no menus, and the same page underneath — no second copy of
  the form to keep in step, no toolkit brought in for one window. The address is
  optional, and the option belongs to the tray mode.
- Which browsers can do this is not something the program can know, so the usual
  ones are tried in turn, the ones that install into a known place looked for there
  before the names on PATH. If none is found, or the one that was refuses to start,
  the page goes to the default browser and the log says which of the two happened.
  The window is opened when the tray icon could not be created as well.
- Linux and macOS are told about a reconnect now, through what each desktop has:
  `notify-send` and `osascript`, started as a helper because the tray toolkit has
  no notification of its own. The wording comes from the string table the tray and
  the page share, sending one is best effort (no daemon is a debug line, and the
  reconnect goes on), and only the pair the Windows tray had already chosen
  interrupts anyone: a session that came back, and a reconnect that failed.
- Starting the program a second time asks the running copy for the settings page on
  all three systems now; it used to work on Windows alone, through a named event,
  and elsewhere only said that another instance was running. The copy that holds
  the lock publishes a socket beside it in the temporary directory, made before the
  lock is taken, so a copy that finds the lock taken is certain to find the socket
  too. A socket file a crash left behind is told apart from one a copy answers by
  connecting to it, and the request is best effort: a copy that cannot connect
  records it and exits as before.

### Building, packages, routers

- `go run ./tools/build all` also produces `windows/386`, `linux/386`,
  `linux/armv7` and the two softfloat MIPS builds for the routers. The instruction
  set of the ARM build and the floating point unit of the MIPS ones are part of the
  file names, because neither `arm` nor `mips` on its own says which boards they run
  on. Both MIPS byte orders are there because the boards are. The Windows resource
  file is generated per architecture — `amd64`, `arm64`, `386`.
- Every Linux target is built twice: the second file has `-notray` in its name and
  leaves the tray, its D-Bus client and the rest of what only a tray needs out of
  the binary, about a fifth of it. A router has no desktop session to put an icon
  in, and that build refuses `--app` instead of ignoring it.
- `go run ./tools/build packages` writes a Debian package, an rpm, an Arch package
  and an OpenWrt one into `dist/packages/`, one file per architecture each, plus the
  Windows builds as archives next to the Scoop and winget manifests.
- Each installs the same thing in the layout of its distribution: the build without
  the tray, the systemd unit, the `sysusers.d` file declaring the `sdautologin` user,
  the licence and the documents, with settings and log in `/var/lib/sdautologin`.
  Each asks systemd for that user, enables the unit and starts it — nothing reads a
  `sysusers.d` file on its own — and says so when there is no `config.json` yet.
- Debian and OpenWrt share the code that writes their container, an ipk being a deb
  with a different control file. The OpenWrt one adds a procd init script and a UCI
  configuration, declared as a configuration file so an upgrade keeps the account.
  The rpm is written tag by tag, with a cpio archive in a gzip stream for a payload.
  The Arch package needed something outside the standard library after all: Go writes
  gzip and neither zstd nor xz, so it packs with `zstd` when that is installed, then
  `xz`, and otherwise falls back to gzip and says which it used.
- The architecture name of the OpenWrt package follows the boards — `mips_24kc` and
  `mipsel_24kc` — and no other format is made for MIPS, since no distribution in
  those families runs on it.
- Everything inside a package carries a fixed date rather than the time of the
  build, so packing the same build twice gives the same file. Nothing is signed: a
  local install does not ask for a signature, and a repository would need a key that
  a build task cannot invent.
- The payload is checked for Windows line endings while it is packed, and the
  packaging fails if one has them: a shell script whose shebang ends in a carriage
  return does not run at all. `.gitattributes` pins the line endings under
  `packaging/` for the same reason.
- Each format was written against the tool that reads it rather than against the
  documentation — `dpkg`, `rpm -K`/`rpm -i`, `pacman -Qkk`, `opkg`, the winget
  command line — and five things were found that way: an rpm signature header whose
  region tag belonged to the other header, a pacman archive with absolute member
  names, an Arch size field written in KiB where pacman reads bytes, and an rpm and
  an Arch package that named no directory at all, leaving every one they had made
  behind, empty.
- On the routers, the OpenWrt package installs the program gzipped and unpacks it
  into memory on the first start: 2.7 MiB of flash instead of 7.7. The name it
  unpacks to carries the version on purpose, since everything in a package has a
  fixed timestamp and `/tmp` is not cleared by an upgrade either. The log file is off
  by default there, where `/etc` is on the flash: the same lines already go to
  `logd`, so the rotating file was that text written a second time, up to eight
  megabytes of it. `--no-log` turns the file off anywhere, and the init script passes
  it from the `log_file` UCI option.
- `packaging/openwrt/Makefile` is a package for a firmware image, for a router that
  should carry the program from the start. It installs the program as it is, since
  squashfs compresses what it holds better than gzip does — 1.9 MiB against 2.7.
  Removing the package stops the service first, in the ipk and in the image package
  both: the router runs it out of `/tmp`, so a process left behind by a removal would
  be one nothing could reach again.

### Secrets on disk

- `config.json` is written `0600`, and so is the log in its own `0700` directory:
  the log names the account and the addresses it was signed in from as well.
- The system service keeps its state directory at `0700` (`StateDirectoryMode=0700`),
  both units set `UMask=0077`, and the container image makes `/data` `0700`. The
  program sets the modes itself, so the file's mode does not depend on the units.
- The settings page, the tray and the command line all run as the user the password
  belongs to, so nothing about the program needed the file to be readable by anybody
  else.

### One version

- The version is read from the git tag the checkout is at, and the build writes it
  into the program together with the commit it was built from: `--version` prints
  `1.0.0+5ccacb9`, and the same line opens the log and names the build on the About
  tab.
- The Windows version resource takes its number from that tag as well, handed to
  go-winres by the task runner rather than written into `winres/winres.json`, so the
  file a person opens holds no version that could go stale.
- The OpenWrt package names the git ref it fetches, so its `PKG_VERSION` has to be
  written in its Makefile: what it fetches cannot be asked before it is fetched. That
  build does not go through the task runner, so the number reaches the program
  through `GO_PKG_LDFLAGS`, and a test compares it with the sources.
- What the sources carry is what a tree without a checkout falls back on, which is
  what a tarball and the container build are, and a test holds it to the tag.

### Continuous integration

- `.gitea/workflows/ci.yml` runs on every push and pull request. One job checks the
  sources, `gofmt`, `go vet` and `go test`; the other packs every format and then
  checks what it packed. Both run inside the `golang` image, so the toolchain is the
  one the module asks for, and the second installs the tools of the formats first.
- Those tools come from the mirror the runner reaches over the network of the
  machine it runs on, and whether the mirror answers is asked of the mirror rather
  than of `apt-get update`: an index that could not be fetched at all is a warning
  there and its exit status stays 0, which would have installed from an empty index.
- Every step is a command of that image rather than an action fetched from
  elsewhere: `actions/checkout` and the like are JavaScript, need node in the
  container, and failed before they checked anything. The sources are fetched with
  git from the instance itself.
- `go run ./tools/build verify` is what the second job runs: every package is read
  back with the tool that installs it (`dpkg-deb` for the deb and the ipk, `rpm -K`
  and `rpm -qpl`, `bsdtar` and the listing inside the Arch one), the Windows archives
  are compared with the builds they were made from, and the Scoop and winget
  manifests with the archives they name. Everything is then packed a second time,
  and a single byte that comes out differently fails the task.
- A tool that is not installed is named and counted as unchecked rather than passed
  over, and a run that could not check a single package fails.
- `SHA256SUMS` is written next to the packages by the packaging itself, one line per
  file in the spelling of the `sha256sum` tool, and a tag run attaches it and the
  packages to the release.
- Pushing a tag publishes the release over the Gitea API, and each file replaces the
  attachment that carries its name, so a tag pushed twice neither doubles the files
  nor fails the run.
- The Windows packaging lived in `pack_windows.go`, which the toolchain reads as
  "compile this on Windows only": the task could not run on Linux at all, which is
  where CI and a release build pack. It is `pack_win.go` now.
- The line endings of every text file are pinned in `.gitattributes` rather than only
  those under `packaging/`. `*.cmd` keeps CRLF, where cmd.exe reads it line by line.

### Several accounts, tried in order

- The settings hold a login list now: one entry per account, with the service types
  it may be used with, in the order they are to be tried. The order of the entries is
  the priority, set by dragging them or with the arrow keys; there are no weight
  numbers to keep in step.
- The portal allows one session per client address, so the entries are a failover
  order rather than sessions running side by side.
- The two ways a refusal can read are told apart. An answer that blames the account
  (a wrong password, a failed authentication) takes that entry out of the running for
  ten minutes and carries on with the next one; anything else — a busy portal, a
  timeout, a reply that could not be read — leaves the entry alone and ends the round
  there, because walking the list on a busy portal would mark every account broken at
  once. A save or a manual check clears the wait at once.
- The settings page was laid out around that list: a card per account with the
  service types as draggable chips, a switch that takes an entry out of the running
  without deleting it, a test button per entry, and badges for the entry in use and
  the refused ones, with the reason and the time left. The test button waits for the
  settings to be saved, since the program can only be asked about the list it is
  running with.
- The test button submits nothing while the portal already reports this client as
  online: a second login on top of a session comes back as a refusal that blames the
  account, so testing a session that was up answered "wrong account or password".
- A configuration file written before the list existed is read as one entry of it,
  and only rewritten in the new shape when something is saved, so the upgrade can
  still be undone until then. The account string submitted for such a file is the one
  the older version submitted, down to the service suffix.
- The command line takes `--account=USER:PASSWORD[@SERVICE[+SERVICE...]]`, which may
  be repeated; the older `--username`, `--password` and `--isp` describe the list as a
  single entry rather than adding to it. A password holding an `@` cannot be written
  in the one line form, since the `@` introduces the service types.
- A save from the settings page that is refused leaves the running configuration
  alone: the request is decoded onto a copy, and that copy has to be one down to the
  lists inside it.

### The settings service

- Every phase of a request has a deadline now — reading the header, reading the
  request, writing the reply, keeping an idle connection. The reply deadline follows
  the configured request timeout rather than being pinned, because a handler that
  talks to the portal waits that long on each request it makes.
- The token is compared in a time that does not depend on how many bytes of one match
  the other, so it cannot be read out a byte at a time by timing the answers.
- Every answer carries the security headers, the refusals included: what the page may
  load, no guessing a type it was not given, no framing by another page, and no
  address in the Referer of anything the page links to.
- The token leaves the address as soon as the page is served. A navigation cannot
  carry a header, so the address the tray hands out carries the token; the server
  exchanges it for a cookie and sends the browser to the same page without it. Every
  later request is checked against that cookie, and one that changes something has to
  repeat the value in a header as well, which a page from another site cannot set.
- An address is good for one opening: a new one is made for each opening handed out
  and for each one used, so what a browser was left with is not a way in a second
  time. Each new address goes to the log as it is made, which leaves a machine that
  serves the page for remote management one to read — the current address is always
  the newest of those lines.

### Tests

135 cases, up from 52.

- `internal/app` (16) and `cmd/SDAutoLogin` (25): the settings server with its token,
  cookie and security-header rules, the one-opening addresses, the login self test on
  both sides of its rule, both forms of every command line option, which mode refuses
  which, the JSON fields, and the exit codes.
- `internal/config` (25): the file and its defaults, what `normalize` clamps, the
  validation of every field, the mode it is saved with, the login list and the order
  it is tried in, and the upgrade from a single-account file, reading included.
- `internal/service` (21): the check loop against a portal the test steers — an online
  session, a dropped one that is signed back into, a reply that cannot be read, auto
  login off, no account, and the failover the list is for, with its ten-minute wait.
- `tools/build` (12) and the small ones — `internal/logging` (4), `internal/platform`
  (7), `internal/i18n` (4), `internal/webui` (1): the version the sources carry
  against the latest tag, the OpenWrt Makefile against the same one, the link flags,
  the layout of every package container written out in full and compared with each
  container packed twice, and the page's markup literals held to the string table.

## v1.0.0 - 2026-09-21

First release: a tray program that keeps a campus network (Dr.COM eportal) session
alive — it polls the portal and signs back in with the stored account as soon as the
session is gone, and never logs the session out. This is also the release that
restructured everything: the layout, the build, the tray and single instance
handling, and the first tests.

### Layout and build

- Everything the build produces lands in `dist/`. The generated resource file moved
  out of the root into `winres/`, next to the icon and `winres.json` it is compiled
  from, so the root holds sources only.
- The single `main` package of 29 files in the root was split into `cmd/SDAutoLogin`
  and ten packages under `internal/`, with dependencies running one way: logging,
  i18n and brand at the bottom, then config and platform, then portal, then service,
  then app, then tray.
- The cleaning task removes only the executables — settings and logs live beside the
  program, so cleaning a build could have thrown an account away.
- The task runner builds `./cmd/SDAutoLogin`, and the wrapper scripts and the CMake
  targets go through that one runner, so the three ways in cannot drift apart.

### Tray and single instance

- A double click on the tray icon opened the settings page twice, since Windows sends
  down, up, double click, up and both ups were handled. A repeat inside the double
  click interval Windows itself reports is one click now.
- A second copy asks the running one for its settings page through a named event, but
  only if the tray had been created. The waiter is independent of the tray now: it
  starts before the icon exists, and when no message loop is running it opens the page
  itself. In the background `run` mode the request is written to the log instead of
  vanishing.
- Half built tray state is cleared when the icon cannot be created, so nothing can
  post messages to a zero window, and the log is opened before the console is
  attached, so a console that cannot be attached is recorded rather than lost.

### Tests

There were no tests at all, so nothing guarded the behaviour this program is judged
by. There are 52 cases now: `internal/config` (14), `internal/portal` (20),
`internal/service` (14) and `internal/i18n` (4) — the defaults and the clamps, both
result formats the portal uses, every reconnect path, and every session state having
wording in both languages.

### Documentation

Both READMEs describe the new layout, the entry point, and where the build writes its
files.

### Building

```sh
build.cmd                       # Windows, ./build.sh elsewhere
go run ./tools/build            # lists every task
cmake -S . -B build -G Ninja    # the same tasks through CMake
```

Cross compiling covers Windows and Linux. The macOS tray needs cgo, so that build has
to be made on macOS with the Xcode command line tools installed.

### Known limitations

- On Linux and macOS the single instance lock is a file in the temporary directory. If
  the system removes it while the program runs, a second copy could start. Reasoned
  about, not reproduced.
- If the browser cannot be opened, the settings page reports it with a modal message
  box on the UI thread, so the tray does not answer until it is dismissed.
