// The Windows side of a release: the builds as archives that can be unpacked
// wherever they land, and the manifests that let Scoop and winget install one.
// There is no installer: the program is a single file, and an .msi would need a
// toolchain of its own to build the same one file.

package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// wingetID is what winget calls the package. It has to start with the
	// publisher, the account the project lives under.
	wingetID = "zqlan.SDAutoLogin"
	// wingetManifestVersion is the revision of the manifest format the files are
	// written for. It appears in the files and in the address of the schema that
	// describes them.
	wingetManifestVersion = "1.10.0"
	// manifestDir holds the manifests, inside the directory the packages are
	// written to. Scoop takes one file, winget wants a directory of them.
	manifestDir = "manifests"
)

// windowsBuild is one Windows architecture as a manifest needs it: the build
// itself, the archive it is published in, the digest of that archive, and the two
// names the package managers give the architecture.
type windowsBuild struct {
	target
	// archive is the file name of the zip in the release.
	archive string
	// digest is the SHA-256 of that zip, in upper case, as winget writes it and
	// Scoop accepts it.
	digest string
	// scoop and winget are what the two managers call this architecture.
	scoop, winget string
}

// taskWindows packs the Windows builds into archives and writes the manifests for
// the two package managers into dist/packages/manifests/.
func taskWindows(root string) error {
	version, err := appVersion(root)
	if err != nil {
		return err
	}
	repo, err := appRepo(root)
	if err != nil {
		return err
	}

	builds := []windowsBuild{
		{target: target{goos: "windows", goarch: "amd64"}, scoop: "64bit", winget: "x64"},
		{target: target{goos: "windows", goarch: "arm64"}, scoop: "arm64", winget: "arm64"},
		{target: target{goos: "windows", goarch: "386"}, scoop: "32bit", winget: "x86"},
	}
	for i := range builds {
		b := &builds[i]
		if err := buildTarget(root, b.target); err != nil {
			return err
		}
		archive, err := windowsZip(root, b.target)
		if err != nil {
			return err
		}
		b.archive = fmt.Sprintf("%s-%s-windows-%s.zip", progName, version, b.archName())
		b.digest = strings.ToUpper(sha256Hex(archive))
		if err := writePackage(root, b.archive, archive); err != nil {
			return err
		}
	}

	if err := writeScoopManifest(root, version, repo, builds); err != nil {
		return err
	}
	if err := writeWingetManifests(root, version, repo, builds); err != nil {
		return err
	}
	// The digests are rewritten here too, this task being registered on its own as
	// well as under taskPackages.
	return writeChecksums(root)
}

// windowsZip is the archive of one Windows build: the program, the licence and
// the readme, all at the top of the archive, so that unpacking it is the whole
// installation and a manifest can name the program directly.
func windowsZip(root string, t target) ([]byte, error) {
	exe, err := readBinary(filepath.Join(root, distDir, t.name()))
	if err != nil {
		return nil, err
	}
	license, err := readBinary(filepath.Join(root, "LICENSE"))
	if err != nil {
		return nil, err
	}
	readme, err := readBinary(filepath.Join(root, "README.md"))
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entries := []pkgEntry{
		{name: progName + ".exe", mode: 0o755, data: exe},
		{name: "LICENSE", mode: 0o644, data: license},
		{name: "README.md", mode: 0o644, data: readme},
	}
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.name, Method: zip.Deflate, Modified: pkgEpoch}
		hdr.SetMode(os.FileMode(e.mode))
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(e.data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// releaseURL is where the archive of a release is published. A manifest needs
// an address the build cannot work out for itself, so it is derived from the
// address the program carries and the layout of the release host, Gitea serving
// the attachment of a release as
//
//	<address>/releases/download/<tag>/<file>
//
// where the tag is the version with the v this project puts in front of it.
// GitHub uses the same shape; GitLab does not.
func releaseURL(repo, version, file string) string {
	return fmt.Sprintf("%s/releases/download/v%s/%s", repo, version, file)
}

// scoopArch is one architecture of the Scoop manifest.
type scoopArch struct {
	URL  string `json:"url"`
	Hash string `json:"hash"`
}

// scoopManifest is the manifest of the Scoop package. The fields are the ones
// that matter for a program that is a single file: where to get it, how to check
// it, and what to do with it once it is there.
type scoopManifest struct {
	Version      string               `json:"version"`
	Description  string               `json:"description"`
	Homepage     string               `json:"homepage"`
	License      string               `json:"license"`
	Architecture map[string]scoopArch `json:"architecture"`
	// Bin puts the program on the path, for its command line mode, and the
	// shortcut is for a desktop.
	Bin       []string   `json:"bin"`
	Shortcuts [][]string `json:"shortcuts"`
	// Persist keeps the settings and the log across an update, since the
	// program writes them next to itself by default.
	Persist []string `json:"persist"`
}

func writeScoopManifest(root, version, repo string, builds []windowsBuild) error {
	m := scoopManifest{
		Version:      version,
		Description:  pkgSummary,
		Homepage:     repo,
		License:      "MIT",
		Architecture: map[string]scoopArch{},
		Bin:          []string{progName + ".exe"},
		Shortcuts:    [][]string{{progName + ".exe", progName}},
		Persist:      []string{"config.json", "logs"},
	}
	for _, b := range builds {
		m.Architecture[b.scoop] = scoopArch{
			URL:  releaseURL(repo, version, b.archive),
			Hash: b.digest,
		}
	}
	data, err := json.MarshalIndent(m, "", "    ")
	if err != nil {
		return err
	}
	return writeManifest(root, filepath.Join("scoop", pkgName+".json"), append(data, '\n'))
}

// writeWingetManifests writes the three files winget wants: what the package
// is, where the archives are, and the description of it. They are written here
// for the winget command line to check.
func writeWingetManifests(root, version, repo string, builds []windowsBuild) error {
	files := []struct {
		// kind is the part of the manifest, which names both its file and the
		// schema it is written against.
		kind string
		body string
	}{
		{"version", fmt.Sprintf(`PackageIdentifier: %s
PackageVersion: %s
DefaultLocale: en-US
ManifestType: version
ManifestVersion: %s
`, wingetID, version, wingetManifestVersion)},
		{"installer", wingetInstaller(version, repo, builds)},
		{"defaultLocale", fmt.Sprintf(`PackageIdentifier: %s
PackageVersion: %s
PackageLocale: en-US
Publisher: Zhiqiang Lan
PackageName: %s
PackageUrl: %s
License: MIT
Copyright: %s
ShortDescription: %s
Description: %s
Moniker: %s
Tags:
- campus-network
- auto-login
- tray
ManifestType: defaultLocale
ManifestVersion: %s
`, wingetID, version, progName, repo, pkgCopyright, pkgSummary, wingetDescription(), pkgName, wingetManifestVersion)},
	}
	for _, f := range files {
		// The files are named the way the winget repository names them: the
		// package, then the part of the manifest, the locale one being the only
		// one that carries a locale in its name.
		name := wingetID + "." + f.kind + ".yaml"
		switch f.kind {
		case "version":
			name = wingetID + ".yaml"
		case "defaultLocale":
			name = wingetID + ".locale.en-US.yaml"
		}
		if err := writeManifest(root, filepath.Join("winget", name), []byte(wingetSchema(f.kind)+f.body)); err != nil {
			return err
		}
	}
	return nil
}

// wingetSchema is the first line of a winget manifest: the address of the
// schema the file is written against. Nothing needs it to install a package,
// but the winget command line warns about every manifest missing it.
func wingetSchema(kind string) string {
	return "# yaml-language-server: $schema=https://aka.ms/winget-manifest." +
		kind + "." + wingetManifestVersion + ".schema.json\n"
}

// wingetInstaller is the part of the winget manifest that says where to get the
// package and what to do with it: the archives are unpacked in place and the
// program in them is what gets installed.
func wingetInstaller(version, repo string, builds []windowsBuild) string {
	var b strings.Builder
	fmt.Fprintf(&b, `PackageIdentifier: %s
PackageVersion: %s
InstallerType: zip
NestedInstallerType: portable
NestedInstallerFiles:
- RelativeFilePath: %s.exe
  PortableCommandAlias: %s
Installers:
`, wingetID, version, progName, pkgName)
	for _, build := range builds {
		fmt.Fprintf(&b, `- Architecture: %s
  InstallerUrl: %s
  InstallerSha256: %s
`, build.winget, releaseURL(repo, version, build.archive), build.digest)
	}
	fmt.Fprintf(&b, "ManifestType: installer\nManifestVersion: %s\n", wingetManifestVersion)
	return b.String()
}

// wingetDescription is the longer description of the winget manifest, one line
// per paragraph.
func wingetDescription() string {
	return "Keeps a Dr.COM ePortal session alive, checking the portal every few seconds " +
		"and signing back in as soon as the session is gone. It never signs the session " +
		"out, and touches nothing else. It sits in the notification area and is " +
		"configured from a settings page in the browser."
}

func writeManifest(root string, name string, data []byte) error {
	path := filepath.Join(root, distDir, pkgDir, manifestDir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("  -> %s\n", describe(root, path))
	return nil
}
