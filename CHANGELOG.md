# Changelog

Release notes for every version, newest first. Log files, commit messages and
this file's code stay English; the interface wording is bilingual.

**English** | [简体中文](CHANGELOG.zh-CN.md)

## Unreleased

What has landed since v1.0.0.

### Where there is no screen

A server, a container and a NAS have no tray and no display, and nobody can
answer the setup wizard on them, so the program had no way in on those machines.

- `run` takes `--settings=ADDR` and serves the settings page on an address that
  was named, from the start and for as long as the process runs, rather than on
  the loopback server that is started on demand and shut down again when idle.
  The page can end a background run, and a warning is written when the address is
  reachable from the network, where the token in its URL is all the protection it
  has.
- `--username`, `--password`, `--isp` and `--save` write the account down without
  the wizard, as a provisioning script needs.
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
  settings and the log, and its health check is the exit code of `status`.

### Watching a session

- `status --json` prints the same check as one line, which a monitoring script
  reads. The log goes to stderr rather than stdout, so stdout carries only the
  result of the command.
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
  the same page the browser opens on its own: no second copy of the form to
  keep in step, and no toolkit brought in for one window. The address is
  optional, and the option belongs to the tray mode, which is the one mode with a
  window.
- Which browsers can do this is not something the program can know, so the ones
  that usually can are tried in turn, and the ones that install into a known
  place are looked for there before the names on PATH. When none of them is
  found, or the one that was found refuses to start, the page goes to the default
  browser and the log says which of the two happened.
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

### Packages

The program could only be installed by copying a file into place. Every system
has a way of saying "install this", so the task runner now makes the packages for
them:

- `go run ./tools/build packages` writes a Debian package, an rpm, an Arch
  package and an OpenWrt one into `dist/packages/`, one file per architecture
  each, and the Windows builds as archives next to the manifests that Scoop and
  winget read.
- Every one of them installs the same thing in the layout of its distribution:
  the build without the tray, the systemd unit from `packaging/systemd`, the
  `sysusers.d` file that declares the `sdautologin` user, and the licence and the
  documents. The settings and the log go to `/var/lib/sdautologin`, which that
  unit was already written for. A package is for a service, so it gets the
  build without the tray; a desktop has the archive.
- Each of them asks systemd for that user, enables the unit and starts it. This
  is the work `dh_installsystemd` generates, and it is needed because nothing
  reads a `sysusers.d` file on its own: the trigger systemd ships only covers the
  users of systemd itself. All of them say so when there is no `config.json` yet,
  a service with no account stopping after a few tries.
- Debian and OpenWrt share the code that writes their container, an ipk being a
  deb with a different control file in it. The OpenWrt one adds a procd init
  script and a UCI configuration instead of a unit, and declares the last of those
  as a configuration file, so an upgrade keeps the account in it. The rpm is
  written tag by tag, since its header is a tagged binary structure rather than an
  archive, with a cpio archive in a gzip stream for a payload. The Arch package is
  the one that needed something from outside the standard library after all: Go
  writes gzip and neither zstd nor xz, so the task packs with `zstd` when it is
  installed, then `xz`, and otherwise falls back to gzip and says which it used.
- The architecture name of the running OpenWrt package follows the boards:
  `mips_24kc` for the big endian ones and `mipsel_24kc` for the little endian
  ones, and no other format is made for MIPS, since no distribution in those
  families runs on it.
- Everything inside a package carries a fixed date rather than the time of the
  build, so packing the same build twice gives the same file. Nothing is signed:
  a local install does not ask for a signature, and a repository would need a key
  that a build task cannot invent.
- The payload of a package is checked for Windows line endings while it is
  packed, and the packaging fails if one has them: a shell script whose shebang
  ends in a carriage return does not run at all, and the platform the sources were
  checked out on is the last thing that should decide this. The repository pins
  the line endings of everything under `packaging/` for the same reason.
- Each format was written against the tool that reads it rather than against the
  documentation: `dpkg` installs the deb, creates the user and starts the service,
  `rpm -K` verifies all four digests and `rpm -i` unpacks it, `pacman -Qkk` checks
  the installed files against the listing the package carries, `opkg` would, and
  the winget command line validates its manifests. Five things were found that way
  and fixed: an rpm signature header whose region tag was the one of the other
  header, a pacman archive whose members were named absolutely where that format
  names them relative to the root, an Arch size field written in KiB where pacman
  reads bytes, and, in a later pass with the same tools, an rpm and an Arch package
  that named no directory at all: both left every directory they had made behind,
  empty, since a package manager takes away only what it was told about.

### Routers

A router has a few megabytes of flash and no desktop, and the program is a single
large binary that runs as a service, so the packaging for it was looked at twice.

- The OpenWrt package installs the program gzipped and a wrapper unpacks it into
  memory on the first start: 2.7 MiB of flash instead of 7.7, at the cost of the
  running copy living in `/tmp`, which is memory too. The name it is unpacked to
  carries the version on purpose: everything inside a package has a fixed
  timestamp, and `/tmp` is not cleared by an upgrade either, so a name that
  changes with the version makes a new package unpack its own program rather
  than the previous one being run until the router is rebooted. The
  program is packed at the best gzip level for the few percent that saves.
- The log file is off by default on OpenWrt, where `/etc` is on the flash. The
  same lines already go to the console, which procd hands to `logd`, the in-memory
  ring `logread` reads back, so the rotating file was that text written a second
  time, up to eight megabytes of it. `--no-log` turns the file off anywhere by
  setting `logToFile`, and the init script passes it from the `log_file` UCI
  option.
- `packaging/openwrt/Makefile` is an OpenWrt package for a firmware image, for a
  router that should carry the program from the start rather than installing it
  afterwards. It installs the program as it is: a squashfs compresses what it
  holds better than gzip does, 1.9 MiB against 2.7 for this program, so
  compressing it there would add a megabyte to the image and cost it its place in
  the read only filesystem.
- Removing the package stops the service first, in the .ipk and in the image
  package both. It is this program that needs it: the router runs it out of
  `/tmp`, unpacked from the archive in `/usr/lib`, so a process left behind by a
  removal would be one nothing could reach again, neither the init script that has
  just been deleted nor the program that was taken out from under it. An upgrade
  stops it for the same reason, and the postinst of the package that follows
  starts it again.

### Secrets on disk

The account has to be on disk for the program to sign in unattended, and it was
written in a file that anyone who could read it could use.

- `config.json` is written `0600`, and so is the log in its own `0700` directory:
  the log names the account and the addresses it was signed in from as well. A
  file an earlier version left readable by everyone is tightened the next time it
  is written, so an installation that is already there is brought in line without
  anyone having to do anything.
- The system service keeps its state directory to `0700`
  (`StateDirectoryMode=0700`), both units set `UMask=0077`, and the container
  image makes `/data` `0700`. The program sets the modes itself, so the file's
  mode does not depend on the units.
- The settings page, the tray and the command line all run as the user the
  password belongs to, so nothing about the program needed the file to be readable
  by anybody else.

### Continuous integration

Nothing checked a change before it was merged, and the packages were only ever
looked at by hand, once.

- `.gitea/workflows/ci.yml` runs on every push and pull request. One job checks
  the sources, `gofmt`, `go vet` and `go test`, and the other packs every format
  and then checks what it packed. Both run inside the `golang` image, so the
  toolchain is the one the module asks for rather than whatever the runner
  happens to have, and the second one installs the tools of the formats before it
  starts.
- `go run ./tools/build verify` is the task that second job runs, and it is the
  automated form of the checks the formats were written against: every package is
  read back with the tool that installs it (`dpkg-deb` for the deb and the ipk,
  `rpm -K` and `rpm -qpl` for the rpm, `bsdtar` and the listing inside the package
  for the Arch one), the Windows archives are compared with the builds they were
  made from, and the Scoop and winget manifests with the archives they name.
  Everything is then packed a second time, and a single byte that comes out
  differently fails the task, since a release built twice has to be the same
  release. The expected contents are written down in the task rather than asked of
  the code that packs them: a check that asks the code what it wrote cannot catch
  a file that was never written.
- A tool that is not installed is named and counted as unchecked rather than
  passed over, and a run that could not check a single package fails: reporting
  success on the strength of having looked at nothing is worse than saying what
  could not be looked at. On a machine with none of those tools, `verify` says so.
- `SHA256SUMS` is written next to the packages by the packaging itself, one line
  per file in the spelling of the `sha256sum` tool. Nothing is signed, so this is
  what a downloaded release can be checked against, and the packages and the file
  are kept as an artifact of the CI run.
- Pushing a tag publishes the release: the same run creates it over the Gitea API
  and attaches the packages and `SHA256SUMS`, where the two manifests say they
  can be downloaded from. A tag that does not name the version inside
  the packages is refused instead of published, since the manifests would then
  name files that are not there, and a release that already exists is taken as it
  is so that a tag pushed twice does not fail the run.
- The Windows packaging lived in a file called `pack_windows.go`, which the
  toolchain reads as "compile this on Windows only": the task could not run on
  Linux at all, and Linux is where both CI and a release build pack. The file is
  `pack_win.go` now, and the whole set packs and verifies there.
- The line endings of every text file are pinned in `.gitattributes` rather than
  only those under `packaging/`: the packaging refuses a payload file that has
  Windows line endings, and which of the two a checkout produces is the last thing
  that should depend on the platform it was checked out on. `*.cmd` keeps CRLF,
  where cmd.exe reads it line by line.

### Several accounts, tried in order

The program kept one account, which does not help when that account stops
working: the portal is left alone and the machine stays offline with nothing to
fall back on.

- The settings hold a login list now: one entry per account, each with the
  service types it may be used with, in the order they are to be tried. The order
  of the entries is the priority, and it is set by dragging them or by moving
  them with the arrow keys; there are no weight numbers to keep in step, since
  the order itself is unambiguous.
- The portal allows one session per client address, so the entries are a failover
  order rather than sessions running side by side. The first entry that signs in
  is the one the session keeps.
- The two ways a refusal can read are told apart, which keeps the list useful. An
  answer that says the account itself is the problem (a wrong password, a failed
  authentication) takes that entry out of the running for ten minutes and carries
  on with the next one; anything else, a busy portal, a request that timed out, a
  reply that could not be read, leaves the entry alone and ends the round there,
  because walking the list on a busy portal would mark every account as broken in
  one go. A saved configuration or a manual check clears the wait at once, so a
  password that was just fixed is picked up on the next check.
- The settings page was laid out again around that list: an ordered card per
  account, with the service types as chips that are dragged into order, a switch
  that takes an entry out of the running without deleting it, a test button per
  entry, and badges for the entry the session is on and the entries the portal
  refused, with the reason and the time left. The test button waits for the
  settings to be saved, since the program can only be asked about the list it is
  running with. The page as a whole got a design pass: one spacing and colour
  scale, a switch rather than a checkbox where a state is meant, in-page dialogs
  instead of the browser's own, and a layout that folds to one column on a phone.
- A configuration file written before the list existed is read as one entry of it,
  and it is only rewritten in the new shape when something is saved, so an
  installation that is already there needs nobody to touch it and the upgrade can
  still be undone until the first save. The account string submitted for such a
  file is the one the older version submitted, down to the service suffix it
  carried.
- The command line takes `--account=USER:PASSWORD[@SERVICE[+SERVICE...]]`, which
  may be repeated, and the older `--username`, `--password` and `--isp` describe
  the list as a single entry rather than adding to it. A password that holds an
  `@` cannot be written in the one line form, since the `@` introduces the
  service types; the two older options take it.
- A save from the settings page that is refused leaves the running configuration
  alone. The page posts the whole configuration back, so the request is decoded
  onto a copy, and that copy has to be one down to the lists inside it: the
  version before the list let the decoder write into the very arrays the program
  was checking with, so a password that had been typed wrong took the session
  down at the same moment the page said the save had not been accepted.

### Tests

104 cases, up from 52.

- `internal/app` (5): the settings server serves on the address it was given,
  refuses a request without the token, reports an address a browser can use,
  knows what counts as loopback, and leaves the running configuration and the
  file it was read from untouched when a save is refused.
- `cmd/SDAutoLogin` (24): both forms of every option, the address that is
  optional for `--app`, which mode refuses which option, that `--app` is refused in
  a build without a tray, help and version answering regardless of what else is on
  the command line, the way each status is named, the set of JSON fields, that
  `--no-log` is applied to the configuration, and an empty answer staying empty
  instead of becoming the dash the text form prints. The account options are
  covered as well: the one line form of an entry with and without service types, a
  password holding a colon, the values that cannot be read at all, that an error
  never repeats the password, and that the older options replace the whole list.
- `internal/config` (25): the settings file and its defaults, what `normalize`
  clamps, the validation of every field, the mode the file is saved with, and the
  list itself: which entries and service types are tried, in which order, the key
  that tells two attempts apart, and matching an account the portal names back to
  the entry it belongs to. The upgrade path is pinned as well: a file written by
  the version that kept one account has to yield the same account string and the
  same verdict on whether it can sign in, and reading such a file must not
  rewrite it, so that the upgrade stays reversible until something is saved. The
  copy a settings request is decoded onto is checked down to the storage inside
  it, walking the type rather than the fields it happens to have, since a list
  added later has to be copied as well and nothing else would say so.
- `internal/logging` (4): a log that writes to a file and one that only writes
  to the console, with the path it reports saying which it is, a second start
  replacing the first rather than leaving a file behind, and the modes the file
  and its directory are made with.
- `internal/platform` (3): the command each system is started with, the order
  the browsers are preferred in, and the places each system looks in.
- `internal/service` (19): the check loop against a portal the test steers: an
  online session, a dropped one that is signed back into, a reply that cannot be
  read, auto login switched off, a missing account, and the failover the list is
  for: an entry refused and the next one signing in, a refused entry being
  walked past on the next round, a busy portal *not* walking the list, and the
  wait being cut short by a check that was asked for.

## v1.0.0 - 2026-09-21

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

- `internal/config` (14): the defaults, the UTF-8 BOM some Windows editors add,
  what `normalize` clamps, every field `Validate` reports, and probe cleanup. The
  password stays out of both the file and the log summary.
- `internal/portal` (20): an online and an offline session, both result formats
  the portal uses, a reply without a result field, an error status, and an
  intercepted redirect that must not be followed. The login parameters, the
  `ret_code` wording, and the fact that no logout is ever requested are pinned
  down.
- `internal/service` (14): every reconnect path: a login that helps, one the
  portal accepts while still reporting the client offline, one it rejects, an
  unreadable reply that must not trigger a reconnect, auto login switched off, a
  missing account, and an unreachable portal.
- `internal/i18n` (4): every session state has wording in both languages,
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
