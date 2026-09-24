# Changelog

Release notes for every version, newest first. Log files, commit messages and
this file's code stay English; the interface wording is bilingual.

**English** | [简体中文](CHANGELOG.zh-CN.md)

## Unreleased

### The mark

The icon was a blue rounded badge. A university states who it is with a seal, so
the mark is one now: two rings around a roundel, which is the shape a Chinese
university keeps its emblem in, with the padlock kept inside it. Nothing of any
emblem is reproduced: the rings and the padlock are drawn from primitives, there
is no lettering, and the crimson is an approximation of a university red rather
than a value taken from a specification.

- The mark is a round seal: an outer ring in a darker tone of the accent, a gap,
  and the disc inside it in the accent, with the padlock knocked out of the disc
  in white. The proportions are held to the 16 px tray icon, the size that
  decides whether a mark works at all.
- Its fixed colour is a deep crimson under an ink ring, and the tray tints the
  same mark by session state. The states were sorted out while the colours were
  being chosen: being offline shared a colour with being signed in, and there is
  now a lighter grey for the moment before the first check has answered.
- The settings page takes the same crimson as its accent, as it is in the light
  palette and brightened in the dark one. The brightening is not decoration: a
  crimson deep enough to carry white text cannot be read as text on a dark
  surface, which is the other thing the accent is used for.
- The artwork stays generated. `go run ./tools/build icon` redraws the Windows
  icon from the drawing code and `resources` rebuilds the resource files from it,
  so the tray and the file that carries the icon cannot drift apart.

### The page

The settings page was a form with a skin on it: warm grey, a crimson rule across
the top, rounded cards with shadows, and a column of fields pinned to 1040 px in
the middle of whatever window it was opened in. It is an application now — a
sidebar naming its three parts, a header and a strip of readings across the top,
and a content column behind them that fills the window.

- The fields of a panel flow into one, two or three columns as the window has room
  for them, instead of sitting in a single column with a fixed label gutter and
  empty space to the right of every short control. Each field is a label with its
  control under it, which is what makes that possible. One breakpoint is left in
  the stylesheet, and it is only where the sidebar has to become a strip.
- The page is grey and the panels are white, divided by hairlines, and the crimson
  is spent on the primary action, on links, on focus rings and on the current entry
  of the sidebar. The rule across the top, the tick in front of every section title
  and the coloured pill tabs are gone: a control panel is read by its numbers, and
  colour anywhere else competes with them.
- Attention is said with size and weight rather than with more colour. Text comes
  in four sizes, and a field and a button are both 32 px tall, so the two line up
  on a row without anything being measured to make them.
- General is two columns: the accounts, which are a list and want the width, beside
  the two panels of settings, which stack and read as one column of fields each.
  Three wide panels one under the other was the first answer, and it left each of
  them a band of mostly empty box.
- Every button keeps its box. Drawing the secondary ones as bare words left the test
  buttons looking like part of the sentence they sat in, which is how a button stops
  being found.
- The pill in the header says what the session is doing and who it is doing it as —
  "online · 23D001 (China Mobile)" — taken from the entry in use rather than from the
  account alone.
- The action bar is the end of the form rather than a bar floating over it: its
  buttons line up with the column above them, so a panel that has been saved is not
  covered by the buttons that saved it.
- Small things that were missing are in as well: placeholder and selected text take
  colours from the palette, the scrollbars are thin and tinted, a long path wraps
  inside the field that shows it, and a reader whose system asks for less motion is
  given none.
- The first-run wizard can be left for later. It carries a second button that closes
  it onto the settings page behind it, where the account can be filled in whenever
  the machine is next looked at. Finishing it closes the wizard as well, as soon as
  the server has taken what was typed, and it saves nothing on a step of its own any
  more: picking the interface language used to send a configuration with no account
  in it, and the refusal that came back was shown over the step the reader had not
  reached yet.

### The service type a session is on

The order of an entry's service types is the order they are to be used in, and a
session that was already up when the program started — one signed in by hand, or
by a previous run with another order — was simply left where it was. The order
was a wish rather than what happened, on exactly the machine that had been set up
by hand.

- Such a session is signed in again as the first service type of its entry. The
  portal keeps one session per client address, so a login that replaces it is the
  only way onto another service type. Turning the option off leaves the session
  alone; nothing is signed in on its own while automatic login is off either,
  since moving a session is a sign-in like any other and one switch already
  answers whether this program signs in by itself.
- Moving a session is best effort and never a failure. A portal that will not sign
  in again while a session is up is reported with the session left as it was, and
  the entry is not taken out of the running for it: the account was not what was
  refused. The move is asked about again an hour later, which is slow enough not
  to submit a login every check against a session the portal is happy with, and
  the wait starts over for a new session or when the settings are saved.
- What was refused is said where the session is, not only in the log: the card of
  the account in use carries the portal's own answer for it - "this IP is already
  online" rather than a ret_code - the pill in the header and the strip of
  readings say the session needs a reader, and the tray adds that line to its
  tooltip and shows one balloon tip when the refusal arrives. The session is up
  and working, so nothing else says there is anything to do. The wording says the
  move is the reader's to make rather than what happened, since the program has
  stopped trying and the two buttons under it are the ways out.
- The controls on that card sign the session in again as any of the other service
  types the entry lists, not only the first one: when the first is the one the
  portal will not take, the reader can move the session onto another one without
  editing the list. The same card names the service type the session is on and the
  one its entry puts first, and the controls are there only while there is
  somewhere to move it. Besides them, and only once a refusal has been reported,
  the card offers the portal's own page in a browser: a portal that refuses any
  second sign-in while a session is up cannot be talked out of it from here, and
  signing the session out there is the one thing that opens the way again. The
  address is the configured portal home page, so the page cannot ask the shell to
  open an address of its choosing.
- Which service type a session is on is read from the account the portal echoes,
  which is the account a login was submitted with: a suffix the echo does not
  carry is one that was not sent. An account with no suffix on it used to be read
  as the first service type of its entry, which named a session on the campus
  network itself "China Mobile" on the page.

### The password

An unattended login needs the password in a form the program can read by itself,
so it is kept in `config.json` in clear text. What can be done about that is to
close the places it appears besides the file.

- The settings page writes that field and never reads it. It is told whether a
  password is stored rather than what it is, so the secret is not in the browser,
  not in the script the page loads, and not in a request the page makes. Saving
  with the field left empty keeps the password that is there, which is what an
  entry the reader did not touch sends; an entry with nothing to keep — one just
  added, or one whose account was renamed — is asked for a password instead.
- The file is kept to its owner. It is written `0600`, and a file an older
  version or a copy left readable by the group or by everyone else is tightened
  at startup, with a line in the log saying what was found and what was done.
  Windows has no mode bits to lean on, so there the file is given an access list
  of its own naming only the account the program runs as.
- The password does not have to be in the file at all. `--password-file=PATH`
  reads it from the first line of a file, and a systemd unit can hand it over as
  a credential with `LoadCredential=password:/path`, which the program picks up
  from `$CREDENTIALS_DIRECTORY/password` on its own. A command line is in the
  process list of every user on the machine, so a service or a container can now
  keep the secret out of both the file and the arguments it was started with.

### Continuous integration

A tag is what CI runs on now, and nothing else. Every push built the program,
packed all four formats and then verified them by packing them a second time,
which is a lot of the runner's time for a change that is often one line of prose.
What those jobs answer is whether a release is sound, and that is a question worth
asking where it is answered: at the point a release is cut. A branch is checked
from the checkout with the same tasks, `go run ./tools/build test`, `packages` and
`verify`, which is what they are there for.

### Defects

A pass over the program for the ways it could fail, rather than for what it does,
turned up a set of faults that had been shipping. They are fixed here.

- A login the portal accepted is no longer reported as failed. The login endpoint
  and the online list are written a moment apart, so a portal answers a login with
  "authentication successful" and, in the same second, answers a query with "the
  online data is empty": the session is not in the list yet. That one read was
  taken as the answer to whether the login had worked, so a login that had in fact
  succeeded was reported as a failure, down to a log line saying the portal had
  accepted it and still reporting offline. The session is read back over the
  following second and a half now, and a portal that cannot be read at all is told
  apart from one that says the client is not signed in: the first leaves the state
  at "signing in" for the next check to settle rather than calling it a failure.
- The account the command line writes always has a service type now.
  `--account=user:pass`, which the usage text offers as the campus network itself,
  and `--username` without `--isp`, both left the entry with none, and a run that
  is not saved never passes through the normalization that would have filled it
  in. An entry without a service type cannot be submitted, so `run` refused to
  start with "no account configured" — the very option the caller had just used —
  and the tray ran with nothing it could sign in with. On a machine whose session
  was already online the same entry crashed the program outright: the first check
  read a service type that was not there, and the whole process went down with the
  tray icon.
- Stopping the check loop waits for it to finish. Saving the settings restarts the
  loop, and the one being replaced used to carry on through whatever check it was
  in the middle of: two loops talking to the portal at once, one of them with the
  old passwords and the new endpoints, and the last one to finish publishing its
  view of a configuration that had already been replaced.
- The log no longer gives up quietly. A rotation it cannot do — on Windows a log
  an editor still has open is enough — used to be retried for every single line
  and never succeed again, so the file grew without bound and every line cost a
  close and a rename; that is reported once now. A log file that cannot be opened
  at all is no longer claimed: the About tab, the log tail and "open log" used to
  point at a file that was not there.
- The page's first run works. The wizard's account is put into the list before it
  is saved, since that list is what a save sends, and a save the server refuses is
  shown in the wizard rather than behind it, where it could not be seen. On a fresh
  installation the button did nothing at all.
- A password typed into a card stays in it. A card is rebuilt from what is on the
  page every time the list changes, and the rebuild put the account name back but
  not the password: moving an account, adding one, changing the language or a
  refused save came back with the field empty, and the next save was refused with
  "Password is required" on an account whose password had just been typed into it.
  The field is filled from what the page reads now, and the stored password is
  still never sent to the page, so an entry the reader did not touch is still an
  empty field that means "keep the one that is there".
- A dropped account keeps its place. The order was read back through the position
  each card came from, which applied the move a second time: two accounts went back
  to where they were, three came out in an order nobody had asked for, and the
  priority the list exists for was wrong until it was put right by hand.
- The shortcuts are held to the rules of the commands they stand for: `-cli
  --json` used to start the background mode with the option dropped, where `run
  --json` is a usage error.
- A background run asks nothing on stdin, so `run --settings` on a machine with no
  account brings up the settings page rather than a wizard nobody is there to
  answer, and no longer writes a half empty account into the file on the way.
  `config` reports a save that failed with a non-zero status instead of reporting
  success.
- The message the portal sends back is cut to length before it reaches the log and
  the page, and `login` masks the credentials in the raw reply it prints, as the
  request in the log already was.
- On Linux the tray runs without an icon when there is no session bus to put one
  on, rather than starting the program and waiting for a quit that nothing can ask
  for.
- A tray whose icon cannot be made at all hands this run over to the background
  mode on the settings server it had already built, so the page that server serves
  stays the way in and its exit button ends the run. That button used to post a
  message to the window the tray would have made, which never got a message loop,
  so a run in that state could only be ended by killing it.
- An autostart entry carries the option that says where the settings live, so a
  run started that way finds the account rather than beginning on a fresh
  configuration beside the executable, which looks from the outside like the
  account was lost. No password is written into an entry, which outlives the run
  in a place anyone can read.
- The instance lock is taken in the user's cache directory when nothing can be
  written to the temporary one, and a run that cannot take it at all says so
  rather than reporting nothing.
- A check interval or a request timeout large enough to wrap the duration into a
  negative one is held to a day and to a quarter of an hour: the timer would
  otherwise have fired at once and turned the loop into a hot loop against the
  portal.
- Every packaging task refreshes `SHA256SUMS`, so a package written on its own
  cannot be at odds with the checksums; a `verify` run fails when a manifest it
  checks is missing rather than passing over it in silence.
- The Windows resources are held to the version of the sources: a tag that moved
  without the `resources` task being run leaves the file version Explorer shows
  naming the release before it, while the program itself reports the new one.

## v1.1.0 - 2026-09-24

A release for what v1.0.0 could not reach: the machines that are not a desktop,
a package for every system, a session that can be watched and provisioned from a
script, a window of its own for the settings page, several accounts to fall back
on, and the two things the tray had only done on Windows.

### Where there is no screen

A server, a container and a NAS have no tray and no display, and nobody can
answer the setup wizard on them, so the program had no way in on those machines.

- `run` takes `--settings=ADDR` and serves the settings page on an address that
  was named, from the start and for as long as the process runs, rather than on
  the loopback server that is started on demand and shut down again when idle.
  The address to open, one time token and all, is the one the log prints: the
  bare address is refused, and the newest of those lines is the one that works.
  The page can end a background run, and a warning is written when the address is
  reachable from the network, where the token in its URL is all the protection it
  has.
- `--username`, `--password`, `--isp` and `--save` write the account down without
  the wizard, as a provisioning script needs.
- A run with `--settings` starts even when no account is written down yet, and
  says so in the log with the address to open, since that page is the only way in
  on a machine whose operator has no other one. Without the page a run with
  nothing to sign in with is still refused: a service has nobody to answer the
  setup wizard, and a loop that can only report the same failure is worse than a
  clear one.
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

### Telling the user

The tray reported a reconnect through a balloon tip. That is a Windows feature,
so on Linux and macOS the two pieces of news this program has to give - the
session is back, or signing back in failed - arrived nowhere.

- Linux and macOS are told as well now, through what each desktop already has:
  `notify-send` on Linux and `osascript` on macOS. The toolkit the tray is built
  on has no notification of its own, which is why a helper is started rather
  than an API called, and the wording comes from the string table the tray and
  the settings page share, so the notification cannot say something the tray
  would not.
- Sending one is best effort. A machine with no desktop session, a session with
  no notification daemon, or a helper that is not installed is not a failure of
  the program: the only trace of it is a debug line, and the reconnect it was
  reporting goes on regardless.
- Which transitions are worth interrupting someone for is the pair the Windows
  tray had already chosen: a session that came back, and a reconnect that
  failed. Everything else stays quiet, so a notification still means something.

### Asking the running copy for its page

Starting the program a second time is a request to see the settings page. On
Windows the second copy signals the first through a named event; on Linux and
macOS that request went nowhere, and the second copy only said that another
instance was already running.

- The copy that holds the lock now publishes a socket beside it in the temporary
  directory, and a later copy connects to it to ask for the page, so a double
  start brings the page up on all three systems.
- The socket is made before the lock is taken, so a copy that finds the lock
  taken is certain to find the socket as well: its request cannot be lost in the
  moment between the two.
- A socket file a crash left behind is told apart from one a copy answers by
  connecting to it. One that answers belongs to the copy that is running and is
  left alone; one that answers nothing is removed and made again, since nothing
  else is going to.
- That same probe is why a connection that carries no request opens no page, and
  a connection that says nothing for long enough is closed rather than being
  allowed to hold the loop.
- The request is best effort. A copy that cannot connect records it and exits as
  before, and a copy that holds the lock but could not publish the socket says
  so, since it is the one a later copy would have talked to.

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

### One version

The version was written down in the sources, and the packages, the Windows
version resource, the OpenWrt Makefile and the release step had each been told
about it separately, so cutting a release was a number to change in several
places that nothing compared with one another.

- The version is read from the git tag the checkout is at, and the build writes it
  into the program together with the commit it was built from. A copy that is in
  front of someone can then be named exactly rather than approximately:
  `--version` prints `1.0.0+5ccacb9`, and the same line opens the log and names
  the build on the About tab.
- The Windows version resource takes its number from that tag as well, handed to
  go-winres by the task runner rather than written into `winres/winres.json`, so
  the file a person opens holds no version that could go stale.
- The OpenWrt package names the git ref it fetches, so its `PKG_VERSION` has to be
  written in its Makefile: what it fetches cannot be asked before it is fetched.
  That build does not go through the task runner, so the number reaches the
  program through `GO_PKG_LDFLAGS` instead, and a test compares it with the
  sources.
- What the sources carry is now what a tree without a checkout falls back on,
  which is what a tarball and the container build are. A test holds it to the tag,
  so the fallback cannot drift into naming a release the sources are not.

### Continuous integration

Nothing checked a change before it was merged, and the packages were only ever
looked at by hand, once.

- `.gitea/workflows/ci.yml` runs on every push and pull request. One job checks
  the sources, `gofmt`, `go vet` and `go test`, and the other packs every format
  and then checks what it packed. Both run inside the `golang` image, so the
  toolchain is the one the module asks for rather than whatever the runner
  happens to have, and the second one installs the tools of the formats before it
  starts.
- Those tools come from the mirror the runner reaches over the network of the
  machine it runs on, rather than from the archive the image names, which
  answered from far enough away here that four small packages took minutes.
  Whether the mirror answers is asked of the mirror rather than of
  `apt-get update`: an index that could not be fetched at all is a warning there
  and its exit status stays 0, so a runner that cannot reach the mirror would
  have gone on and installed from an empty index. A mirror that answers and
  carries no such suite is an error rather than a warning, and that is caught as
  well. Either way the image's own sources are put back, so a runner outside the
  network of the mirror is neither slower nor broken.
- Every step is a command of that image, rather than an action fetched from
  elsewhere: the familiar `actions/checkout` and `actions/upload-artifact` are
  JavaScript ones and need node in the container, which the Go image has not, so
  a job that used them failed before it checked anything. The sources are fetched
  with git from the instance itself, and what was packed is kept by being
  published, so a run needs nothing of github.com and nothing but the image.
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
  what a downloaded release can be checked against, and a tag run attaches it and
  the packages to the release it publishes.
- Pushing a tag publishes the release: the same run creates it over the Gitea API
  and attaches the packages and `SHA256SUMS`, where the two manifests say they
  can be downloaded from. The version comes from that tag, so the manifests agree
  with what is attached by construction; the step still refuses to publish when
  the tag it ran for is not the one the packages came out named after, which is
  what a derivation that had stopped reading the tag would look like. A release
  that already exists is taken as it is so that a tag pushed twice does not fail
  the run.
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
- The test button submits nothing while the portal already reports this client as
  online. The portal allows one session per address, and a second login on top of
  one comes back as a refusal that blames the account, so testing a session that
  was up and a password that was fine was answered with "wrong account or
  password".
- The page's grid had one row too few for its five children, so the free space a
  short panel leaves went to the tab strip rather than to the panel: the tabs grew
  to several times their height on the panel that did not fill the window.
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

### The settings service

The page is reachable by anything that can reach its address, and the token in
that address is all that stands in front of it, so the service was held to what
that implies.

- Every phase of a request has a deadline now: reading the header, reading the
  request, writing the reply, and keeping an idle connection. The reply deadline
  is counted from the configured request timeout rather than pinned to a value,
  because a handler that talks to the portal waits that long on each request it
  makes, and a deadline below that would cut off a reply the program was still
  working on.
- The token is compared in a time that does not depend on how many bytes of one
  match the other, so it cannot be read out a byte at a time by timing the
  answers.
- Every answer carries the security headers, the refusals included, since a
  caller without the token reads one of those. They name what the page may load
  rather than leaving it to the page's own markup, stop the browser from
  guessing a type it was not given, refuse to be framed by another page, and
  keep the address out of the Referer of anything the page links to.
- The token leaves the address as soon as the page is served. A navigation is the
  one request a browser makes on its own, and the one request that cannot carry a
  header, so the address the tray hands out carries the token; the server
  exchanges it for a cookie and sends the browser to the same page without it, so
  the address bar, the history and the Referer of anything the page links to carry
  no token at all. That cookie is what every later request is checked against, and
  a request that changes something has to repeat the value in a header as well,
  which a page from another site cannot set.
- An address is good for one opening. A new one is made for each opening the
  program hands out and for each one that is used, so what a browser was left
  with, and what someone read out of the log, is not a way in a second time. Each
  new address goes to the log as it is made, which leaves a machine that serves
  the page for remote management and has no tray with one to read: the current
  address is always the newest of those lines. The second opening in a row is what
  this had to keep working, so the tray rolls an address for every opening rather
  than handing out the one it gave last time.

### Tests

135 cases, up from 52.

- `internal/app` (16): the settings server serves on the address it was given,
  refuses a request without the token, reports an address a browser can use,
  knows what counts as loopback, and leaves the running configuration and the
  file it was read from untouched when a save is refused. The login self test is
  covered on both sides of its rule: it submits one attempt while the portal
  reports the client offline, and nothing while it reports it online. The service
  itself is held to a deadline on every phase of a request, to a reply deadline
  that follows the configured request timeout, to the security headers on the
  page and on a refusal, and to refusing the empty token of a server that was
  never started, which an unwary constant time comparison would read as a match.
  The address it hands out is exchanged for a cookie that carries another value,
  an address with the token in it is refused on the API, and a write has to repeat
  the cookie in a header. An address is good for one opening and is replaced by
  the one made as it is spent, and every opening the tray makes gets an address of
  its own, so the opening after it is not sent to one that has been used.
- `cmd/SDAutoLogin` (25): both forms of every option, the address that is
  optional for `--app`, which mode refuses which option, that `--app` is refused in
  a build without a tray, help and version answering regardless of what else is on
  the command line, the way each status is named, the set of JSON fields, that
  `--no-log` is applied to the configuration, and an empty answer staying empty
  instead of becoming the dash the text form prints. The account options are
  covered as well: the one line form of an entry with and without service types, a
  password holding a colon, the values that cannot be read at all, that an error
  never repeats the password, and that the older options replace the whole list.
  A run with nothing to sign in with is checked too: refused on its own, allowed
  when it serves the settings page, since that page is the way in.
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
- `internal/platform` (7): the command each system is started with, the order
  the browsers are preferred in, the places each system looks in, the
  notification each desktop is asked for, and the socket a later copy asks the
  running one for its settings page through.
- `internal/service` (21): the check loop against a portal the test steers: an
  online session, a dropped one that is signed back into, a reply that cannot be
  read, auto login switched off, a missing account, and the failover the list is
  for: an entry refused and the next one signing in, a refused entry being
  walked past on the next round, a busy portal *not* walking the list, and the
  wait being cut short by a check that was asked for. A wait that ends on its own
  is covered twice over, since the status renderer and the check loop each reach
  that expiry by a path of their own.
- `internal/webui` (1): every literal in the page markup that the script
  replaces from the string table is compared with that table, so the two copies
  of a sentence cannot drift apart unnoticed.
- `tools/build` (12): the version the sources carry is the one the latest tag
  names, the OpenWrt Makefile names the same one, a tree with no checkout falls
  back on the sources, a value is read from a constant of its own and from a
  block of variables alike, the link flags carry the version and the commit, and
  the variables the linker is told to write into are the ones the program reads.
  The packaging half is held the same way: the layout of every container is
  compared with the one a reader of its format sees, written out in full, so that
  a change to one of them shows up as a difference and not as a package that
  installs wrongly. That covers the ar archive and its two tar archives, the
  header of the rpm tag by tag with the digest of its payload, the metadata of an
  Arch package and the entries of a Windows archive. Every container is also
  packed twice from the same input and has to come out the same bytes, which is
  what the digest published next to a release depends on. It is the first test
  this package has.

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
