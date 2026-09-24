// The OpenWrt package: the two MIPS builds, one for each byte order, as .ipk
// files.

package main

import (
	"bytes"
	"fmt"
	"path/filepath"
)

const (
	// openwrtDir holds what only the OpenWrt package wants: the payload it
	// installs next to the binary, and the maintainer scripts.
	openwrtDir = "packaging/openwrt"
	// The four files the package installs, named as the router sees them. The
	// configuration file is listed as one, so an upgrade keeps the account in it.
	// The program goes to /usr/lib compressed, and what the router runs is the
	// wrapper, which unpacks it into memory.
	uciFile        = "/etc/config/sdautologin"
	initFile       = "/etc/init.d/sdautologin"
	openwrtWrapper = "/usr/bin/SDAutoLogin"
	openwrtBinary  = "/usr/lib/sdautologin/SDAutoLogin.gz"
	// wrapperVersion is the marker the wrapper script carries where the version
	// of the program goes, written over by packageOpenwrt. The script is a file
	// a reader can open, so it holds a marker rather than a number that would go
	// stale there.
	wrapperVersion = "@VERSION@"
)

// taskOpenwrt builds the router builds again and packs them as OpenWrt
// packages, one for each byte order, into dist/packages/. The build without the
// tray is the one that goes in: a router has no desktop session to put an icon
// in, and the tray is by far the largest thing the binary can do without.
func taskOpenwrt(root string) error {
	for _, t := range targets() {
		if t.tags != notrayTag || (t.goarch != "mips" && t.goarch != "mipsle") {
			continue
		}
		if err := buildTarget(root, t); err != nil {
			return err
		}
		if err := packageOpenwrt(root, t); err != nil {
			return err
		}
	}
	// The digests are rewritten here too, this task being registered on its own as
	// well as under taskPackages.
	return writeChecksums(root)
}

// packageOpenwrt writes the .ipk of one target. An ipk is the Debian layout, an
// ar archive of the version of the format, a control archive and a data archive,
// with the control file of opkg in it.
func packageOpenwrt(root string, t target) error {
	version, err := appVersion(root)
	if err != nil {
		return err
	}
	arch := openwrtArch(t.goarch)

	// The program is installed compressed, and a wrapper unpacks it into memory on
	// the first start: a router has little flash and a fair amount of memory, so
	// the package takes about a third of the flash it otherwise would, at the cost
	// of the unpacked copy living in /tmp.
	bin, err := readBinary(filepath.Join(root, distDir, t.name()))
	if err != nil {
		return err
	}
	compressed, err := gzipBytes(bin)
	if err != nil {
		return err
	}
	wrapper, err := openwrtWrapperEntry(root, version)
	if err != nil {
		return err
	}
	payload := filepath.Join(root, openwrtDir, "root")
	uci, err := textEntry(filepath.Join(payload, uciFile), "."+uciFile, 0o644)
	if err != nil {
		return err
	}
	init, err := textEntry(filepath.Join(payload, initFile), "."+initFile, 0o755)
	if err != nil {
		return err
	}

	data, err := debLayout(debFiles{
		entries: []pkgEntry{
			wrapper,
			{name: "." + openwrtBinary, mode: 0o644, data: compressed},
			uci,
			init,
		},
		control:  openwrtControl(version, arch),
		confiles: []string{uciFile},
		scripts:  filepath.Join(root, openwrtDir, "control"),
	})
	if err != nil {
		return err
	}
	return writePackage(root, fmt.Sprintf("%s_%s-%s_%s.ipk", pkgName, version, pkgRelease, arch), data)
}

// openwrtWrapperEntry reads the wrapper script the router runs and writes the
// version of the program into it.
//
// The version ends up in the name the program is unpacked to, and that name tells
// the program of a new package from the copy the previous one left in /tmp. A
// missing marker is an error rather than a versionless package: the script would
// then unpack to a name no upgrade would ever change.
func openwrtWrapperEntry(root, version string) (pkgEntry, error) {
	path := filepath.Join(root, openwrtDir, "root", openwrtWrapper)
	src, err := readText(path)
	if err != nil {
		return pkgEntry{}, err
	}
	if !bytes.Contains(src, []byte(wrapperVersion)) {
		return pkgEntry{}, fmt.Errorf("%s has no %s marker to put the version in", path, wrapperVersion)
	}
	return pkgEntry{
		name: "." + openwrtWrapper,
		mode: 0o755,
		data: bytes.ReplaceAll(src, []byte(wrapperVersion), []byte(version)),
	}, nil
}

// openwrtControl is the control file of an ipk, the fields opkg shows. There is
// no Depends line on purpose: the program is statically linked, and the procd and
// the shell that run the init script are part of the base system.
func openwrtControl(version, arch string) string {
	return fmt.Sprintf(`Package: %s
Version: %s-%s
Architecture: %s
Section: %s
Priority: optional
Maintainer: %s
Description: %s
 Keeps a Dr.COM ePortal session alive on a router, checking it every few
 seconds and signing back in as soon as the session is gone.
 .
 The account is read from %s, as UCI settings are
 expected to be, and the init script hands it to the program on every start.
`, pkgName, version, pkgRelease, arch, pkgSection, pkgMaintainer, pkgSummary, uciFile)
}

// openwrtArch is the architecture name OpenWrt gives a MIPS build: the core the
// binary is compiled for, 24Kc, which both the big endian Atheros boards and the
// little endian MediaTek ones have. Neither of them has a floating point unit,
// which is why these builds are softfloat.
func openwrtArch(goarch string) string {
	if goarch == "mipsle" {
		return "mipsel_24kc"
	}
	return "mips_24kc"
}
