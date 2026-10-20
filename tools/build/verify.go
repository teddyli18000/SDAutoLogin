// Checking what the packaging wrote, with the tool of every format: dpkg reads
// the deb and the ipk, rpm verifies the four digests it carries, bsdtar reads
// the Arch package and its listing, and the Windows archives are compared with
// the builds they came from. A missing tool is named and counted, so a run
// cannot report success without having looked.

package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"sdautologin/internal/update"
)

// wantSystemd, wantDeb and wantOpenwrt list what each package layout has to
// hold, Debian naming the copyright file where the others name the licence.
// They are written out here, since a check that reads them back off the packing
// code would catch nothing.
var (
	wantSystemd = []string{
		"usr/bin/SDAutoLogin",
		"usr/lib/systemd/system/sdautologin.service",
		"usr/lib/sysusers.d/sdautologin.conf",
		"usr/share/licenses/sdautologin/LICENSE",
		"usr/share/doc/sdautologin/README.md",
	}
	wantDeb = []string{
		"usr/bin/SDAutoLogin",
		"usr/lib/systemd/system/sdautologin.service",
		"usr/lib/sysusers.d/sdautologin.conf",
		"usr/share/doc/sdautologin/copyright",
		"usr/share/doc/sdautologin/README.Debian",
		"usr/share/doc/sdautologin/changelog.gz",
	}
	wantOpenwrt = []string{
		"usr/bin/SDAutoLogin",
		"usr/lib/sdautologin/SDAutoLogin.gz",
		"etc/config/sdautologin",
		"etc/init.d/sdautologin",
	}
)

// taskVerify checks every package that was packed, and then that packing the same
// build again gives the same bytes.
func taskVerify(root string) error {
	dir := filepath.Join(root, distDir, pkgDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("%s: %w (the packages task writes them)", describe(root, dir), err)
	}

	// The list and its signature are read first, and together: the signature is
	// over the bytes of SHA256SUMS, so a list written again after it was signed
	// leaves a signature nothing accepts, and the release would carry the pair
	// without anything having noticed. It runs before the per-format checks
	// below, which a machine may not have the tools for: a pair that does not
	// match is a fault of the release, not of the machine reading it. The key is
	// a property of this build, so it is read once.
	key, err := buildKey()
	if err != nil {
		return err
	}
	if err := verifyChecksumsSignature(dir, key); err != nil {
		return err
	}

	notChecked := make([]string, 0, 1)
	checked := 0
	for _, e := range entries {
		// The checksum list and its signature are the two files here that are not
		// packages. They are named, since a package this loop passed over would be
		// one the release shipped without anything having read it back.
		if e.IsDir() || e.Name() == checksumsFile || strings.HasSuffix(e.Name(), ".sig") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		var missing string
		switch {
		case strings.HasSuffix(e.Name(), ".ipk"):
			missing, err = verifyDpkg(path, wantOpenwrt,
				[]string{"control", "conffiles", "postinst", "prerm"}, true)
		case strings.HasSuffix(e.Name(), ".deb"):
			missing, err = verifyDpkg(path, wantDeb,
				[]string{"control", "md5sums", "postinst", "prerm", "postrm"}, false)
		case strings.HasSuffix(e.Name(), ".rpm"):
			missing, err = verifyRpm(path)
		case strings.Contains(e.Name(), ".pkg.tar"):
			missing, err = verifyPacman(path)
		case strings.HasSuffix(e.Name(), ".zip"):
			missing, err = verifyWindows(root, path)
		default:
			fmt.Printf("  ?  %s: no format goes by that name\n", e.Name())
			continue
		}
		if err != nil {
			return fmt.Errorf("%s: %w", e.Name(), err)
		}
		if missing != "" {
			notChecked = append(notChecked, fmt.Sprintf("%s: %s", e.Name(), missing))
			fmt.Printf("  --  %s: %s, not checked\n", e.Name(), missing)
			continue
		}
		checked++
	}

	fmt.Printf("  %d package(s) checked, %d left unchecked\n", checked, len(notChecked))
	// A package of a format whose tool is missing is the failure this task exists
	// for: the release would ship it without anything having read it back. A
	// format this run packed nothing of needs no tool, so that stays quiet.
	if len(notChecked) > 0 {
		return fmt.Errorf("%d package(s) could not be read back: %s (install the tool of every format that was packed)",
			len(notChecked), strings.Join(notChecked, "; "))
	}
	if checked == 0 {
		return fmt.Errorf("no package could be checked: install the tools of the formats")
	}
	return verifyReproducible(root)
}

// buildKey is the key a release of this project is checked with.
//
// A build with no key at all is the state before keygen has run, and a build
// whose key cannot be read is a build that is broken: the two are told apart
// here, since only the second is a fault to report. A build with no key reaches
// verifyChecksumsSignature as nil, which refuses a release that carries a
// signature nothing in this build can judge.
func buildKey() (ed25519.PublicKey, error) {
	pub, err := update.PublicKeyBytes()
	switch {
	case err == nil:
		return pub, nil
	case errors.Is(err, update.ErrUnsigned):
		return nil, nil
	default:
		return nil, fmt.Errorf("%s holds a key that cannot be read: %w", publicKeySource, err)
	}
}

// verifyChecksumsSignature checks the signature of SHA256SUMS.
//
// A signature is required: a package set nothing has signed is not a release
// this project publishes, so a run that finds one fails rather than reporting
// success. The release is signed before this is run, and the pair is read back
// from the disk, so what is checked is what would be published.
//
// pub is the key the build carries, nil when it carries none. It is a parameter
// rather than a call here, so the check can be tried with a key of the test's
// own making.
func verifyChecksumsSignature(dir string, pub ed25519.PublicKey) error {
	sumsPath := filepath.Join(dir, checksumsFile)
	sigPath := sumsPath + update.SignatureSuffix
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s is not there: the packages are not signed, and nothing unsigned is published (run sign)",
				filepath.Base(sigPath))
		}
		return err
	}
	sums, err := os.ReadFile(sumsPath)
	if err != nil {
		return fmt.Errorf("%s is there and %s is not, so the signature signs nothing: %w",
			filepath.Base(sigPath), checksumsFile, err)
	}
	if pub == nil {
		return fmt.Errorf("%s is signed and this build carries no key to check it with: it was signed by a build that has one",
			filepath.Base(sigPath))
	}
	if err := update.VerifySignature(pub, sums, string(sig)); err != nil {
		return fmt.Errorf("%s does not check out against %s (%v): the list was written again after it was signed, so run sign",
			filepath.Base(sigPath), checksumsFile, err)
	}
	fmt.Printf("  ok  %s checks out against %s, key %s\n",
		filepath.Base(sigPath), checksumsFile, update.KeyID(pub))
	return nil
}

// verifyDpkg reads a package with dpkg-deb, which reads an ipk as well as a deb:
// the two are the same container, and dpkg-deb is the tool that reads it.
func verifyDpkg(path string, files, control []string, openwrt bool) (string, error) {
	tool, err := lookTool("dpkg-deb")
	if err != nil {
		return err.Error(), nil
	}

	// The control archive holds the metadata, and the maintainer scripts a
	// package manager runs around an installation.
	info, err := runTool(tool, "--info", path)
	if err != nil {
		return "", err
	}
	for _, want := range control {
		if !strings.Contains(info, want) {
			return "", fmt.Errorf("the control archive holds no %s", want)
		}
	}

	contents, err := runTool(tool, "--contents", path)
	if err != nil {
		return "", err
	}
	for _, want := range files {
		if !strings.Contains(contents, "./"+slash(want)) {
			return "", fmt.Errorf("the payload holds no %s", want)
		}
	}

	kind := "deb"
	if openwrt {
		kind = "ipk"
	}
	fmt.Printf("  ok  %s (%s: %d control member(s), %d file(s))\n",
		filepath.Base(path), kind, len(control), len(files))
	return "", nil
}

// verifyRpm reads a package with rpm. Its header carries four digests of itself
// and of its payload, and rpm is the only thing that can check them.
func verifyRpm(path string) (string, error) {
	tool, err := lookTool("rpm")
	if err != nil {
		return err.Error(), nil
	}

	digests, err := runTool(tool, "-Kv", path)
	if err != nil {
		return "", err
	}
	for _, want := range []string{
		"Header SHA256 digest: OK", "Header SHA1 digest: OK",
		"Payload SHA256 digest: OK", "MD5 digest: OK",
	} {
		if !strings.Contains(digests, want) {
			return "", fmt.Errorf("rpm did not report %q", want)
		}
	}

	list, err := runTool(tool, "-qpl", path)
	if err != nil {
		return "", err
	}
	for _, want := range wantSystemd {
		if !strings.Contains(list, "/"+slash(want)) {
			return "", fmt.Errorf("the payload holds no /%s", want)
		}
	}
	// The directories as well: a package that names none leaves every one of them
	// behind when it is removed.
	for _, want := range []string{"/usr/bin\n", "/usr/share/doc/sdautologin\n"} {
		if !strings.Contains(list, want) {
			return "", fmt.Errorf("the file list does not name the directory %s", strings.TrimSpace(want))
		}
	}

	scripts, err := runTool(tool, "-qp", "--scripts", path)
	if err != nil {
		return "", err
	}
	for _, want := range []string{"postinstall", "preuninstall", "postuninstall"} {
		if !strings.Contains(scripts, want) {
			return "", fmt.Errorf("the package carries no %s scriptlet", want)
		}
	}

	fmt.Printf("  ok  %s (four digests, the scriptlets, %d file(s) and their directories)\n",
		filepath.Base(path), len(wantSystemd))
	return "", nil
}

// verifyPacman reads an Arch package with bsdtar, and compares the files it holds
// with the digests its own listing carries, as "pacman -Qkk" does for an installed
// package.
func verifyPacman(path string) (string, error) {
	tool, err := lookTool("bsdtar")
	if err != nil {
		return err.Error(), nil
	}

	list, err := runTool(tool, "-tf", path)
	if err != nil {
		return "", err
	}
	for _, want := range append([]string{".PKGINFO", ".INSTALL", ".MTREE"}, wantSystemd...) {
		if !strings.Contains(list, want) {
			return "", fmt.Errorf("the archive holds no %s", want)
		}
	}
	if strings.Contains(list, "\n/") {
		return "", fmt.Errorf("a member is named absolutely")
	}

	work, err := os.MkdirTemp("", "sdautologin-verify-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	if _, err := runTool(tool, "-xf", path, "-C", work); err != nil {
		return "", err
	}
	entries, err := checkMtree(work)
	if err != nil {
		return "", err
	}

	fmt.Printf("  ok  %s (%d member(s), %d digest(s) match the listing)\n",
		filepath.Base(path), len(wantSystemd)+3, entries)
	return "", nil
}

// checkMtree compares every file of the extracted package with the digest and the
// mode the listing inside it gives, and returns how many entries it looked at.
func checkMtree(dir string) (int, error) {
	compressed, err := os.ReadFile(filepath.Join(dir, ".MTREE"))
	if err != nil {
		return 0, err
	}
	gz, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return 0, fmt.Errorf(".MTREE: %w", err)
	}
	plain, err := io.ReadAll(gz)
	if err != nil {
		return 0, fmt.Errorf(".MTREE: %w", err)
	}

	seen := 0
	for _, line := range strings.Split(string(plain), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "./") {
			continue
		}
		name := strings.TrimPrefix(fields[0], "./")
		path := filepath.Join(dir, filepath.FromSlash(name))

		var mode, digest string
		isDir := false
		for _, f := range fields[1:] {
			switch {
			case strings.HasPrefix(f, "mode="):
				mode = strings.TrimPrefix(f, "mode=")
			case strings.HasPrefix(f, "sha256digest="):
				digest = strings.TrimPrefix(f, "sha256digest=")
			case f == "type=dir":
				isDir = true
			}
		}

		info, err := os.Stat(path)
		if err != nil {
			return 0, fmt.Errorf("%s is in the listing but not in the archive", name)
		}
		if got := fmt.Sprintf("%o", info.Mode().Perm()); got != mode {
			return 0, fmt.Errorf("%s is %s, the listing says %s", name, got, mode)
		}
		if isDir {
			seen++
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return 0, err
		}
		if sha256Hex(data) != digest {
			return 0, fmt.Errorf("%s does not match the digest in the listing", name)
		}
		seen++
	}
	return seen, nil
}

// verifyWindows checks an archive of a Windows build: the program inside has to
// be the build that was packed, and the manifests next to it have to carry the
// digest of the file that was written, so a manifest cannot point at the build
// before this one.
func verifyWindows(root, path string) (string, error) {
	version, err := appVersion(root)
	if err != nil {
		return "", err
	}
	name := filepath.Base(path)
	prefix := progName + "-" + version + "-windows-"
	if !strings.HasPrefix(name, prefix) {
		return "", fmt.Errorf("the name does not start with %s", prefix)
	}
	arch := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".zip")

	built := filepath.Join(root, distDir, progName+"-windows-"+arch+".exe")
	want, err := os.ReadFile(built)
	if err != nil {
		return "", err
	}

	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer zr.Close()

	found := false
	for _, f := range zr.File {
		if f.Name != progName+".exe" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		inside, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return "", err
		}
		if !bytes.Equal(inside, want) {
			return "", fmt.Errorf("the %s in the archive is not the build in dist/", f.Name)
		}
		found = true
	}
	if !found {
		return "", fmt.Errorf("the archive holds no %s", progName+".exe")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256Hex(data)
	if err := verifyManifests(root, name, digest, arch); err != nil {
		return "", err
	}

	fmt.Printf("  ok  %s (the build inside, and the manifests point at it)\n", name)
	return "", nil
}

// verifyManifests checks that the two manifests naming a Windows archive carry
// the digest of the archive that is there now. A missing manifest is an error,
// since a check that skips what it cannot find reports success for a release
// that wrote none.
func verifyManifests(root, archive, digest, arch string) error {
	scoop := filepath.Join(root, distDir, pkgDir, manifestDir, "scoop", pkgName+".json")
	data, err := os.ReadFile(scoop)
	if err != nil {
		return fmt.Errorf("%s: %w (the windows task writes it)", describe(root, scoop), err)
	}
	var manifest struct {
		Architecture map[string]struct {
			URL  string `json:"url"`
			Hash string `json:"hash"`
		} `json:"architecture"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(scoop), err)
	}
	key := map[string]string{"amd64": "64bit", "arm64": "arm64", "386": "32bit"}[arch]
	entry, ok := manifest.Architecture[key]
	if !ok {
		return fmt.Errorf("%s names no %s build", filepath.Base(scoop), key)
	}
	if entry.Hash != strings.ToUpper(digest) && entry.Hash != digest {
		return fmt.Errorf("%s carries another digest for %s", filepath.Base(scoop), archive)
	}
	if !strings.HasSuffix(entry.URL, "/"+archive) {
		return fmt.Errorf("%s points at %q", filepath.Base(scoop), entry.URL)
	}

	winget := filepath.Join(root, distDir, pkgDir, manifestDir, "winget", wingetID+".installer.yaml")
	text, err := os.ReadFile(winget)
	if err != nil {
		return fmt.Errorf("%s: %w (the windows task writes it)", describe(root, winget), err)
	}
	if !strings.Contains(string(text), archive) {
		return fmt.Errorf("%s names no %s", filepath.Base(winget), archive)
	}
	if !strings.Contains(strings.ToUpper(string(text)), strings.ToUpper(digest)) {
		return fmt.Errorf("%s carries no digest for %s", filepath.Base(winget), archive)
	}
	return nil
}

// verifyReproducible packs everything a second time and compares what it wrote
// with what was there before.
func verifyReproducible(root string) error {
	dir := filepath.Join(root, distDir, pkgDir)
	before, err := digestDir(dir)
	if err != nil {
		return err
	}

	fmt.Println("  packing everything again to see whether it comes out the same")
	if err := taskPackages(root); err != nil {
		return err
	}
	after, err := digestDir(dir)
	if err != nil {
		return err
	}

	// The set first: a run that left a file behind, or that packed on a machine
	// where another compressor was installed, is told which file it is.
	for name := range after {
		if _, ok := before[name]; !ok {
			return fmt.Errorf("%s was not there before, and is now", name)
		}
	}
	for name, sum := range before {
		other, ok := after[name]
		if !ok {
			return fmt.Errorf("%s was there before and is not now", name)
		}
		if other != sum {
			return fmt.Errorf("%s came out different the second time", name)
		}
	}
	fmt.Printf("  ok  %d file(s) came out byte for byte the same\n", len(after))
	return nil
}

// digestDir is the digest of every file of a directory, by name. Subdirectories
// are left out, and one of them is there: dist/packages/manifests holds the
// Scoop and winget manifests. They are written from the archives rather than
// shipped beside them — the publish step walks the files of dist/packages and
// nothing under it — so what is compared here is what a release carries.
func digestDir(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sums := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		sums[e.Name()] = sha256Hex(data)
	}
	return sums, nil
}

// lookTool finds a tool of one of the formats. Not being installed is not an
// error here: what was not looked at is reported instead.
func lookTool(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("this machine has no %s", name)
	}
	return path, nil
}

// runTool runs one of the tools of the formats and hands back what it printed.
// The output goes into the error as well, since that is where a tool says what it
// did not like.
func runTool(tool string, args ...string) (string, error) {
	cmd := exec.Command(tool, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%s %s failed: %w: %s",
			filepath.Base(tool), quoteArgs(args), err, firstLines(out.String(), 3))
	}
	return out.String(), nil
}

// firstLines is the beginning of what a tool printed, for an error message.
func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "; ")
}

// slash is a path with forward slashes, the way the archives name things whatever
// system they were written on.
func slash(name string) string { return strings.ReplaceAll(name, "\\", "/") }
