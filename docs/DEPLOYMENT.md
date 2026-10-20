# Deployment

`run` is the mode to use: it keeps checking with no tray and no display, logs
to the standard error, and exits on `SIGTERM`. It refuses to start without an
account, so write one down first, on the command line with `--save` or from
the settings page when the service is started with `--settings`.

**English** | [简体中文](DEPLOYMENT.zh-CN.md)

A release ships the same program, built without the tray: a package for every
distribution, a Windows archive, `SHA256SUMS` and `SHA256SUMS.sig` next to the
packages, and a container image built from the [Dockerfile](Dockerfile).

## Contents

- [Cross compiling](#cross-compiling)
- [Packages](#packages)
- [Debian, Fedora, RHEL, openSUSE and Arch](#debian-fedora-rhel-opensuse-and-arch)
- [Windows](#windows)
- [systemd](#systemd)
- [Container](#container)
- [OpenWrt](#openwrt)

## Cross compiling

Cross compiling covers Windows and Linux, for `amd64`, `arm64`, `386`,
`armv7`, `mips` and `mipsle`. The last three put in the file name what `GOARCH`
alone does not say: `armv7` is the instruction set of the 32 bit ARM build, and
`mips-softfloat`/`mipsle-softfloat` are for the routers, which have no floating
point unit. Both MIPS byte orders are built: the Atheros targets (ath79,
ar71xx) are big endian, the MediaTek ones (ramips, mt76x8) little endian.

Every Linux target is built twice. The second file has `-notray` in its name:
no tray, and with it no D-Bus client and none of the rest only a tray needs. It
is about a fifth smaller (10.2 MiB to 8.3 MiB for `mipsle`), and it is the one
for a router. A start with `--app` is refused there instead of being ignored.
The build that keeps the tray is the default one, for a desktop.

On a router, or any machine without a desktop, use `run`: the tray needs a
desktop session, and the settings page is reached over the network with
`--settings`.

macOS is the exception: its tray needs cgo, so that build has to be made on
macOS with the Xcode command line tools installed
(`xcode-select --install`).

The Windows executable carries an icon, version information and a DPI aware
application manifest, embedded from `winres/rsrc_windows_<arch>.syso`, one file
per architecture (`amd64`, `arm64`, `386`). The toolchain picks the file that
matches the build. The icon and those files are generated, from the drawing
code and from `winres/winres.json`; the `icon` and `resources` tasks regenerate
them, inside `winres/`.

## Packages

`go run ./tools/build packages` writes a package for every distribution this
program targets:

| Distribution | Package | Installed with |
| --- | --- | --- |
| Debian, Ubuntu | `sdautologin_1.0.0-1_amd64.deb` | `apt install ./...` |
| Fedora, RHEL, openSUSE | `sdautologin-1.0.0-1.x86_64.rpm` | `dnf install` or `rpm -i` |
| Arch | `sdautologin-1.0.0-1-x86_64.pkg.tar.zst` | `pacman -U` |
| OpenWrt | `sdautologin_1.0.0-1_mips_24kc.ipk`, `..._mipsel_24kc.ipk` | `opkg install`, see [OpenWrt](#openwrt) |
| Windows | `SDAutoLogin-1.0.0-windows-amd64.zip` | unpack it |

Each one holds the same thing in the layout of its distribution: the program,
built without the tray; the systemd unit that runs it; the file that declares
the `sdautologin` user it runs as; the licence and the documents. Settings and
log live in `/var/lib/sdautologin`, and the package enables its service. Each
one has to be given an account before it stays up. OpenWrt is the one that
differs, having no systemd and no `/var/lib`; its section below has the
details.

None of these needs a packaging toolchain installed. The Arch package is the
exception: it is packed with `zstd` when that is installed, with `xz` or `gzip`
when it is not, and the task says which it used.

No package is signed. `dpkg -i`, `rpm -i` and `pacman -U` all take an unsigned
package; pacman complains only when its configuration asks for signatures and
`LocalFileSigLevel` is not `Optional`. A repository of your own needs a key of
your own, which the build does not make. `SHA256SUMS` is written next to the
packages, one line per file in the spelling of the `sha256sum` tool.

That list is signed, and the program installs a release only when the signature
holds. The run signs `SHA256SUMS` with an ed25519 key and attaches
`SHA256SUMS.sig` beside it; the program checks the signature against a key
compiled into it and then reads the digest of the archive out of the verified
list. A build that carries no key looks for a release, reports it and installs
nothing.

### The signing key

The key is made once, on a machine that keeps it:

```sh
go run ./tools/build keygen
```

It writes the private half to `signing.key` (`0600`) in this user's configuration
directory (`~/.config/sdautologin` on Linux, `%AppData%\sdautologin` on Windows),
or to the file `SDAUTOLOGIN_SIGNING_KEY_FILE` names, and the public half into
`internal/update/publickey.go`, which is committed. The private half stays outside
the checkout on purpose: a key beside the sources is a key in every copy of the
tree and in whatever a backup sweeps up. The contents of that file go into the
Gitea repository secret `SDAUTOLOGIN_SIGNING_KEY`, which the packaging job reads
on a tag.

Signing reads the key from `SDAUTOLOGIN_SIGNING_KEY` and from nothing else. No
file in the working tree is read, so being able to read a checkout is not enough
to sign a release that every installed copy accepts.

The signing step runs on every tag and stops the run when the secret does not
hold the key `publickey.go` carries, so a tag without a working key produces no
release at all: one nobody can check is worse than none. The step can also be
started by hand from the Actions page, which builds every package, signs the
checksum list and then checks the packages against it, and stops before the
steps that publish. A build made before a key was compiled in reports the newer
version and installs nothing.

The same key signs every release. `keygen` refuses to run when a key is already
there, and `sign` refuses a key that is not the one `publickey.go` carries, so a
release cannot go out signed with a key nothing accepts.

Signing is the last step that touches `dist/packages`, and `verify` runs after
it: `sign` writes `SHA256SUMS` itself, so the signature covers the packages that
are really in the directory, and nothing may be packed between the two. A list
that is written again after it was signed does not keep the signature — it is
removed rather than shipped next to a file it does not sign — and `verify` fails
when it finds a pair that does not match. The order is the point: `verify`
compares the signature with the list beside it on the disk, so it checks
something only when a signature is there. Run before the signing step it found
no signature at all and passed, which is why it now runs after it, and why a
list that carries no signature is a failure rather than a note.

To rotate, move the old `signing.key` out of the way, make a new pair, commit
the new public half and put the new private half into the secret. Releases
signed with the old key are then refused by the new build, and the refusal names
both key ids. A copy that still carries the old key refuses the new releases,
so those machines need one update by hand.

`go run ./tools/build verify` reads every package back with the tool that
installs it (`dpkg-deb` for the deb and the ipk, `rpm -K` and `rpm -qpl` for
the rpm, `bsdtar` and the listing inside the package for the Arch one),
compares the Windows archives with the builds they were made from, and checks
that the Scoop and winget manifests carry the digest of the archive that is
there. It packs everything a second time and fails if a single byte came out
differently. The tools of the formats have to be installed, so it runs on Linux:
a missing one is named, and a run that could not check a single package fails.

All of it runs when a tag is pushed, in
[.gitea/workflows/ci.yml](.gitea/workflows/ci.yml), together with `go vet`,
`go test` and a `gofmt` check. No other event starts the run: a branch is
checked in the checkout, with those same tasks, instead of on the runner.

Pushing a tag publishes the release: the same run creates it and attaches the
packages, `SHA256SUMS` and `SHA256SUMS.sig`. The version is read from that tag,
so the packages are named after it and the manifests agree with what is
attached: the tag is `v` plus the version, `v1.0.0` for 1.0.0. The same version
goes into the program itself, which reports it with the commit it was built
from, into the version resource of the Windows build and into the sources, where
a tree unpacked without checkout metadata falls back on it; a test holds them to
the tag.

A release that already exists is taken as it is, so a tag pushed twice does not
fail the run: each file replaces the attachment that carries its name. This
instance keeps two attachments when a name repeats, so the run deletes the ones
already there before it uploads. A run that finds files already attached says
so.
[dedupe-release-assets.sh](.gitea/scripts/dedupe-release-assets.sh) clears up a
release that was doubled before that, keeping the newest attachment of each
name.

The release body is the section of `CHANGELOG.md` for that tag; the run reads
it out of the file and refuses to publish without it. That section is what
somebody deciding whether to upgrade reads, so it says what changed for them and
what they have to do about it. What the release changed about the project's own
machinery, how many tests there are or which documents were edited, is in the
commit history and needs no heading here.

The wiki is written from the repository at the same time: the readme, the
changelog and this guide are copied into it, and `docs/wiki/` holds the pages
that are for the wiki alone.

## Debian, Fedora, RHEL, openSUSE and Arch

Three steps on all of them: install the package, write the account down,
restart the service.

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

The package creates the `sdautologin` user, enables the service and starts it.
Without an account it stops after a few tries, as the postinst says when it
finds no `config.json` yet. `/usr/share/doc/sdautologin/README.Debian` has the
same steps on the machine. Removing the package leaves the settings and the log
where they are, and the account in the settings; purging it does the same.

To look at what was installed: `rpm -K` checks the digests and `rpm -qi` the
header, `rpm -e sdautologin` removes it and leaves `/var/lib/sdautologin`
alone; `pacman -Qi` and `pacman -Qkk` work on the Arch package, the second one
checking every file against the listing it carries.

## Windows

The archive holds the program, the readme and the licence, and nothing else.
There is no installer. Unpack it where it is to stay and start the program; it
writes its settings next to itself, so that folder has to be kept.

This archive is also the only release that can install an update for itself,
with `checkForUpdates` on. Whether it does is decided by who owns the file, not
by the platform: a copy that can write the folder it runs from, and that no
package manager put there, replaces itself. A Scoop or winget install is
recognised (an `install.json` beside the program, or a path under `scoop/apps`
or `windowsapps`) and left alone. Everywhere else the program arrives in a
package the package manager owns, so a check reports the release and names that
manager as the thing that installs it. The Linux service runs under
`ProtectSystem=strict` and cannot write the file it was started from.

`dist/packages/manifests/` holds what the package managers that can install
such an archive want: `scoop/sdautologin.json` and the three files of the
winget manifest. Both name the archives of a release as their download, at
`<project address>/releases/download/v<version>/`. Should releases move
elsewhere, change `releaseURL` in `tools/build/pack_win.go` and both follow.
The winget command line checks them
(`winget validate --manifest dist/packages/manifests/winget`); the Scoop one
declares the settings and the log as persistent; an update keeps the account.

## systemd

Two ready-made units are in [packaging/systemd](packaging/systemd), one for a
system service and one for `systemctl --user`; the user unit opens with its own
installation steps. The packages install the system one, and these are the
steps it performs:

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
systemctl reload sdautologin
journalctl -u sdautologin -f
```

The unit carries `ExecReload=/bin/kill -HUP $MAINPID`. `run` answers `SIGHUP`
by re-reading `config.json` and restarting the check loop, so `systemctl reload
sdautologin` applies an edited file without stopping the program. `SIGINT` and
`SIGTERM` still quit.

The unit keeps the settings and the log in `/var/lib/sdautologin` and confines
the process to the network it needs. To configure that machine from a browser,
add `--settings=127.0.0.1:8088` to `ExecStart` and forward the port over SSH:
`ssh -L 8088:127.0.0.1:8088 the-server`. Then open the address printed on the
console when the page starts (`journalctl -u sdautologin` shows it, since that is
where the service's console output goes); the bare port is refused, and the
newest line is the one that works. The address is deliberately not written to
`logs/app.log`: that file outlives the token and is copied around, so an address
kept there would be a way in that outlives its one opening.

A unit can hand the password over, so that it stays out of `config.json`:

```ini
LoadCredential=password:/etc/sdautologin/password
```

The file it names holds one `account:password` per line, for a login list with
more than one entry, or a single password for the first entry of the list.
`/etc/sdautologin/password` is the path to recommend for a service: the one
place a service password lives, and not the file anyone copies around. Set up as
a systemd credential it never lands in a persistent file the program reads; the
source file persists, and systemd copies it into a private tmpfs at each start.

## Container

The [Dockerfile](Dockerfile) builds the same static binary and puts it in an
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

`--home=/data` is part of the entry point, so a one-off command such as
`status` reads the same settings as the service. The container is marked
unhealthy when `status` reports the session is not up.

## OpenWrt

The two MIPS builds are packaged, one per byte order:

| Package | For |
| --- | --- |
| `sdautologin_<version>-1_mips_24kc.ipk` | big endian boards, such as the Atheros ones (ath79) |
| `sdautologin_<version>-1_mipsel_24kc.ipk` | little endian boards, such as the MediaTek ones (ramips, mt76x8) |

The package installs a procd init script at `/etc/init.d/sdautologin` and a
UCI configuration at `/etc/config/sdautologin`. The program is installed
compressed as `/usr/lib/sdautologin/SDAutoLogin.gz`, and `/usr/bin/SDAutoLogin`
is a short script that unpacks it into `/tmp` on the first start and runs it
from there: about 2.9 MiB of flash instead of 8.3, and the same amount of memory
while it runs. The copy in `/tmp` is unpacked once per boot and once per
upgrade, and the name it is unpacked to carries the version.

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

`password` puts the secret in the process list of every user on the router.
`password_file` names a file to read it from instead:

```sh
printf '%s\n' SECRET > /etc/sdautologin/password
chmod 600 /etc/sdautologin/password
uci set sdautologin.main.password_file=/etc/sdautologin/password
uci commit sdautologin
/etc/init.d/sdautologin restart
```

The package does not create the file. It holds one `account:password` per line,
or a single password for the first entry, and is `0600`. The init script passes
it on as `--password-file`, and `password` as `--password` only when no file is
set, so a configuration that sets only `password` keeps working.
`/etc/config/sdautologin` is installed `0600`.

An option left empty is not passed on, and then `config.json` decides, which
the settings page writes. That page can be turned on for a router with no
display, and reached from the network it serves:

```sh
uci set sdautologin.main.settings=0.0.0.0:8088
uci commit sdautologin
/etc/init.d/sdautologin restart
```

Then open the address `logread -e SDAutoLogin` prints, with `127.0.0.1` in it
replaced by the address the router answers on, since the page is served from
every interface but the log names the loopback one. The port alone is refused,
and the newest of those lines is the one that works. With an address that is not
loopback the token is the only protection; keep it on a trusted network or reach
it over a tunnel.

`/etc/config/sdautologin` is declared as a configuration file, so an upgrade
keeps the account in it, and `config.json` lives in `/etc/sdautologin`, on the
overlay, where it survives a reboot and an upgrade.

The log file is off by default. The program writes the same lines to the
console, procd hands them to `logd`, and `logread -e SDAutoLogin` reads them
back out of memory. Turn it on at the cost of up to eight megabytes of flash
once the log has grown to its limit:

```sh
uci set sdautologin.main.log_file=1
uci commit sdautologin
/etc/init.d/sdautologin restart
```

The per-check detail is on by default as well, and can be turned off the same
way. `quiet` leaves out the line a check writes every fifteen seconds and keeps
the ones that report a change of state, which is what a router wants once it
works: the detail fills logd's ring within the hour and pushes the other
services out of it. Set it to `0` while working out why a sign-in fails.

```sh
uci set sdautologin.main.quiet=0
uci commit sdautologin
/etc/init.d/sdautologin restart
```

### Building it into an image

A router can also carry the program from the start.
`packaging/openwrt/Makefile` is an OpenWrt package for that, and the Go build
system it needs comes from the packages feed:

```sh
cd $OPENWRT
./scripts/feeds update packages
./scripts/feeds install golang
mkdir -p package/sdautologin
cp -r /path/to/SDAutoLogin/packaging/openwrt/. package/sdautologin/
make menuconfig          # Network -> sdautologin
```

It installs the program uncompressed: a squashfs compresses what it holds,
better than gzip does (about 2.1 MiB against 2.9 for this program). The init
script and the UCI configuration are the same two files the package above
installs.
