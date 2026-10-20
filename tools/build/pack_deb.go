// The Debian package: one .deb per Linux architecture, for the machines that
// install their software with apt, Ubuntu among them.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// debDir holds what only the Debian package wants: the maintainer scripts
	// and the files that go straight into place under the root of the target.
	debDir = "packaging/deb"
)

// taskDeb builds the Debian packages, one for each architecture, into
// dist/packages/. The build without the tray is the one that goes in: the
// package runs the program as a service, and a desktop uses the archive.
func taskDeb(root string) error {
	for _, t := range targets() {
		// Debian has dropped its MIPS ports, and a router runs OpenWrt, so those
		// builds are not packaged here.
		if t.tags != notrayTag || t.goos != "linux" || t.goarch == "mips" || t.goarch == "mipsle" {
			continue
		}
		if err := buildTarget(root, t); err != nil {
			return err
		}
		if err := packageDeb(root, t); err != nil {
			return err
		}
	}
	// The digests are rewritten here too, this task being registered on its own as
	// well as under taskPackages.
	return writeChecksums(root)
}

func packageDeb(root string, t target) error {
	rel, err := packageVersion(root)
	if err != nil {
		return err
	}
	repo, err := appRepo(root)
	if err != nil {
		return err
	}
	entries, err := debEntries(root, t, repo)
	if err != nil {
		return err
	}
	arch := debArch(t.goarch)

	data, err := debLayout(debFiles{
		entries:  entries,
		control:  debControl(rel.deb(), arch, repo, entries),
		scripts:  filepath.Join(root, debDir, "control"),
		checksum: true,
	})
	if err != nil {
		return err
	}
	return writePackage(root, fmt.Sprintf("%s_%s-%s_%s.deb", pkgName, rel.deb(), pkgRelease, arch), data)
}

// debEntries is what the package installs: the program, the unit that runs it,
// the user it runs as, and the documentation a Debian package is expected to
// carry.
func debEntries(root string, t target, repo string) ([]pkgEntry, error) {
	payload := filepath.Join(root, debDir, "root")

	bin, err := binaryEntry(filepath.Join(root, distDir, t.name()), "./usr/bin/SDAutoLogin", 0o755)
	if err != nil {
		return nil, err
	}
	unit, err := textEntry(filepath.Join(root, unitFile), "."+unitTarget, 0o644)
	if err != nil {
		return nil, err
	}
	sysusers, err := textEntry(filepath.Join(root, sysusersFile), "."+sysusersConf, 0o644)
	if err != nil {
		return nil, err
	}
	readme, err := textEntry(filepath.Join(payload, docDir, "README.Debian"), "."+docDir+"/README.Debian", 0o644)
	if err != nil {
		return nil, err
	}
	// The upstream documents go in as they are, next to the ones Debian wants
	// for itself.
	upstream, err := binaryEntry(filepath.Join(root, "README.md"), "."+docDir+"/README.md", 0o644)
	if err != nil {
		return nil, err
	}
	license, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		return nil, err
	}
	// The changelog goes in compressed, as a package's documentation is
	// stored.
	changelog, err := gzipFile(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		return nil, err
	}

	return []pkgEntry{
		bin,
		unit,
		sysusers,
		readme,
		upstream,
		{name: "." + docDir + "/copyright", mode: 0o644, data: debianCopyright(repo, string(license))},
		{name: "." + docDir + "/changelog.gz", mode: 0o644, data: changelog},
	}, nil
}

// debControl is the control file of a deb, the fields dpkg and the archive tools
// show. There is no Depends line: the program is statically linked, so it needs
// nothing from the system beyond the systemd the package is built for.
func debControl(version, arch, repo string, entries []pkgEntry) string {
	return fmt.Sprintf(`Package: %s
Version: %s-%s
Architecture: %s
Maintainer: %s
Installed-Size: %d
Section: %s
Priority: optional
Homepage: %s
Description: %s
 Keeps a Dr.COM ePortal session alive, checking the portal every few seconds
 and signing back in as soon as the session is gone. It never signs the
 session out, and touches nothing else.
 .
 The service runs as the %s system user, with its settings and its log in
 /var/lib/%s. An account has to be written down once before the service stays
 up; see /usr/share/doc/%s/README.Debian.
`, pkgName, version, pkgRelease, arch, pkgMaintainer, installedSize(entries),
		pkgSection, repo, pkgSummary, pkgName, pkgName, pkgName)
}

// installedSize is what dpkg reports as the size the package takes up. The
// control file gives it in KiB.
func installedSize(entries []pkgEntry) int64 {
	return (installedBytes(entries) + 1023) / 1024
}

// debianCopyright renders the copyright file Debian wants from the licence the
// repository carries. The licence text is indented by one space, and an empty
// line inside it is a single dot.
func debianCopyright(repo, license string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, `Format: https://www.debian.org/doc/packaging-manuals/copyright-format/1.0/
Upstream-Name: SDAutoLogin
Source: %s

Files: *
Copyright: %s
License: MIT
`, repo, pkgCopyright)
	for _, line := range strings.Split(strings.TrimRight(license, "\n"), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			// An empty line would end the text in that format, so it is written
			// as the dot that stands for one.
			b.WriteString(" .\n")
			continue
		}
		b.WriteString(" " + line + "\n")
	}
	return []byte(b.String())
}

// debArch is the architecture name Debian gives a build. GOARCH spells two of
// them differently, and what it calls arm is the one Debian calls armhf, the
// hard float ABI of the 32 bit ARM boards.
func debArch(goarch string) string {
	switch goarch {
	case "386":
		return "i386"
	case "arm":
		return "armhf"
	}
	return goarch
}
