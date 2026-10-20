// The packaging half of the task runner. An ipk and a deb are the same
// container, a plain ar archive of a version marker and two tar archives: that
// layout is written once here, and the two formats describe what goes into it.

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"sdautologin/internal/update"
)

const (
	// pkgDir is where the packages are written, inside distDir.
	pkgDir = "packages"
	// pkgName is the name the packages carry. It is lower case, as OpenWrt and the
	// Debian archive want it, and it is also the name of the UCI configuration and
	// of the init script in the OpenWrt package.
	pkgName = "sdautologin"
	// pkgRelease is the revision of the packaging, bumped when only the files
	// around the binary change. Every one of these formats spells a version
	// "upstream-revision", the upstream half being the constant in internal/app.
	pkgRelease = "1"
	// pkgMaintainer is who the packages name as their packager, and where a
	// Debian package sends a bug report.
	pkgMaintainer = "Zhiqiang Lan <zqlan@suda.edu.cn>"
	// pkgSummary is the one line every format shows as the description. Each
	// format adds its own long description below it.
	pkgSummary = "Campus network auto login"
	// pkgCopyright is who the licence belongs to; the Debian copyright file has to
	// name it.
	pkgCopyright = "2026 Zhiqiang Lan"
	// pkgSection is how Debian and OpenWrt classify the package.
	pkgSection = "net"
	// unitFile is the systemd unit every package installs, and the same file the
	// README tells an administrator to copy by hand, so the two cannot drift.
	unitFile = "packaging/systemd/system/sdautologin.service"
	// sysusersFile declares the system user the service runs as, so no maintainer
	// script has to run adduser. systemd reads the directory it is installed into.
	sysusersFile = "packaging/systemd/sysusers.d/" + pkgName + ".conf"
	// unitTarget and sysusersConf are where the two go on the target, the same
	// place for the Debian and the rpm family.
	unitTarget   = "/usr/lib/systemd/system/" + pkgName + ".service"
	sysusersConf = "/usr/lib/sysusers.d/" + pkgName + ".conf"
	// docDir, licensePath and changelogPath are where Debian, the rpm family and
	// Arch all look for the documentation and the licence of a package.
	docDir        = "/usr/share/doc/" + pkgName
	licensePath   = "/usr/share/licenses/" + pkgName + "/LICENSE"
	changelogPath = docDir + "/CHANGELOG.md"
)

// systemdFiles lists what a package for a distribution that boots with systemd
// installs: the program, the unit, the file declaring the user it runs as, the
// licence and the two documents. Paths are named as they are on the target; the
// Debian layout adds the leading dot of its own archives.
func systemdFiles(root, binary string) ([]pkgEntry, error) {
	bin, err := readBinary(binary)
	if err != nil {
		return nil, err
	}
	// The unit and the file that declares the user are read by systemd on the
	// target, so they are the two that are checked for Windows line endings.
	unit, err := readText(filepath.Join(root, unitFile))
	if err != nil {
		return nil, err
	}
	sysusers, err := readText(filepath.Join(root, sysusersFile))
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
	changelog, err := readBinary(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		return nil, err
	}

	return []pkgEntry{
		{name: "/usr/bin/SDAutoLogin", mode: 0o755, data: bin},
		{name: unitTarget, mode: 0o644, data: unit},
		{name: sysusersConf, mode: 0o644, data: sysusers},
		{name: licensePath, mode: 0o644, data: license},
		{name: docDir + "/README.md", mode: 0o644, data: readme},
		{name: changelogPath, mode: 0o644, data: changelog},
	}, nil
}

// pkgEpoch is the timestamp written on everything inside a package. A fixed
// time keeps the same build packing to the same file twice.
var pkgEpoch = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

// pkgEntry is one file of an archive, held in memory: a package is small enough
// that nothing has to be streamed.
type pkgEntry struct {
	name string
	mode int64
	dir  bool
	data []byte
}

// binaryEntry reads one of the files that is installed unchanged.
func binaryEntry(source, target string, mode int64) (pkgEntry, error) {
	data, err := readBinary(source)
	if err != nil {
		return pkgEntry{}, err
	}
	return pkgEntry{name: target, mode: mode, data: data}, nil
}

// textEntry reads one of the files that is installed and then read on the
// target, refusing one that has Windows line endings.
func textEntry(source, target string, mode int64) (pkgEntry, error) {
	data, err := readText(source)
	if err != nil {
		return pkgEntry{}, err
	}
	return pkgEntry{name: target, mode: mode, data: data}, nil
}

// readBinary reads a file whose bytes are its own business.
func readBinary(source string) ([]byte, error) {
	return os.ReadFile(source)
}

// readText reads a file that a program on the target will read, refusing one with
// Windows line endings: a shell script whose shebang ends in a carriage return
// does not run, and the checkout platform should not decide that.
func readText(source string) ([]byte, error) {
	data, err := os.ReadFile(source)
	if err != nil {
		return nil, err
	}
	if bytes.Contains(data, []byte("\r\n")) {
		return nil, fmt.Errorf("%s has Windows line endings, which would break on the target", source)
	}
	return data, nil
}

// compression is how a tar archive inside a package is packed.
type compression string

const (
	// compressGzip is the only one the standard library can write.
	compressGzip compression = "gz"
	// compressXz and compressZstd go through the tools of those names, the
	// standard library having neither. pacman accepts both, and the official
	// repositories use zstd.
	compressXz   compression = "xz"
	compressZstd compression = "zst"
)

// ext is the file name ending an archive with this compression carries.
func (c compression) ext() string { return "tar." + string(c) }

// tarPack packs entries into a tar archive with the named compression, writing
// the directories the files live in as entries of their own. A package manager
// takes away only what it was told about, so an archive must name them.
func tarPack(entries []pkgEntry, c compression) ([]byte, error) {
	return tarData(entries, c)
}

// tarData packs entries into a tar archive and compresses it afterwards. Doing it
// in that order keeps the compression in one place, and an archive small enough
// to fit in memory does not need a streaming writer.
func tarData(entries []pkgEntry, c compression) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	seen := map[string]bool{}
	for _, e := range entries {
		for _, d := range parentDirs(e.name) {
			if seen[d] {
				continue
			}
			seen[d] = true
			if err := writeTarEntry(tw, pkgEntry{name: d, mode: 0o755, dir: true}); err != nil {
				return nil, err
			}
		}
		if err := writeTarEntry(tw, e); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}

	switch c {
	case compressGzip:
		var out bytes.Buffer
		gz := gzip.NewWriter(&out)
		if _, err := gz.Write(buf.Bytes()); err != nil {
			return nil, err
		}
		if err := gz.Close(); err != nil {
			return nil, err
		}
		return out.Bytes(), nil
	case compressXz:
		return compressWith("xz", []string{"-z", "-c", "-9", "-T1"}, buf.Bytes())
	case compressZstd:
		return compressWith("zstd", []string{"-z", "-c", "-19", "-T1"}, buf.Bytes())
	default:
		return nil, fmt.Errorf("no compressor for %q", c)
	}
}

// compressWith runs an external compressor over an archive. A missing tool is
// named in the error, since the reader has to know what to install.
func compressWith(tool string, args []string, data []byte) ([]byte, error) {
	path, err := exec.LookPath(tool)
	if err != nil {
		return nil, fmt.Errorf("%s is needed to write this package and is not installed", tool)
	}
	cmd := exec.Command(path, args...)
	cmd.Stdin = bytes.NewReader(data)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	fmt.Printf("  %s %s\n", tool, quoteArgs(args))
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w", tool, err)
	}
	return out.Bytes(), nil
}

// writeTarEntry writes one tar header, and the contents when there are any.
func writeTarEntry(tw *tar.Writer, e pkgEntry) error {
	hdr := &tar.Header{Name: e.name, Mode: e.mode, ModTime: pkgEpoch}
	if e.dir {
		hdr.Typeflag = tar.TypeDir
	} else {
		hdr.Typeflag = tar.TypeReg
		hdr.Size = int64(len(e.data))
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if e.dir {
		return nil
	}
	_, err := tw.Write(e.data)
	return err
}

// parentDirs is the directories leading to a file, from the top down, each with
// a trailing slash.
func parentDirs(name string) []string {
	var dirs []string
	for i := 1; i < len(name); i++ {
		if name[i] == '/' {
			dirs = append(dirs, name[:i+1])
		}
	}
	return dirs
}

// arMember is one member of an ar archive.
type arMember struct {
	name string
	data []byte
}

// writeAr writes an ar archive, the container an ipk and a deb both use. The
// members carry their name padded with spaces and an odd sized one is followed
// by a newline, the format dpkg-deb writes and dpkg and opkg read.
func writeAr(w io.Writer, members []arMember) error {
	if _, err := io.WriteString(w, "!<arch>\n"); err != nil {
		return err
	}
	for _, m := range members {
		hdr := fmt.Sprintf("%-16s%-12d%-6d%-6d%-8s%-10d`\n",
			m.name, pkgEpoch.Unix(), 0, 0, "100644", len(m.data))
		if _, err := io.WriteString(w, hdr); err != nil {
			return err
		}
		if _, err := w.Write(m.data); err != nil {
			return err
		}
		if len(m.data)%2 == 1 {
			if _, err := io.WriteString(w, "\n"); err != nil {
				return err
			}
		}
	}
	return nil
}

// debFiles is everything the Debian layout needs: the installed files, the
// control file, the configuration files an upgrade keeps, the maintainer script
// directory, and whether the checksums file is expected. OpenWrt packages come
// through here too, an ipk being the same container with another control file.
type debFiles struct {
	entries  []pkgEntry
	control  string
	confiles []string
	scripts  string
	checksum bool
}

// debLayout writes a package in the Debian layout: the version of the format,
// then the control archive and the data archive.
func debLayout(f debFiles) ([]byte, error) {
	control, err := controlTar(f)
	if err != nil {
		return nil, err
	}
	data, err := tarPack(f.entries, compressGzip)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	members := []arMember{
		{name: "debian-binary", data: []byte("2.0\n")},
		{name: "control.tar.gz", data: control},
		{name: "data.tar.gz", data: data},
	}
	if err := writeAr(&buf, members); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// controlTar is the archive of the package metadata: the control file itself,
// the list of configuration files, the checksums when the format verifies them,
// and the maintainer scripts that sit in the packaging directory.
func controlTar(f debFiles) ([]byte, error) {
	entries := []pkgEntry{{name: "./control", mode: 0o644, data: []byte(f.control)}}
	if len(f.confiles) > 0 {
		list := strings.Join(f.confiles, "\n") + "\n"
		entries = append(entries, pkgEntry{name: "./conffiles", mode: 0o644, data: []byte(list)})
	}
	if f.checksum {
		entries = append(entries, pkgEntry{name: "./md5sums", mode: 0o644, data: md5sums(f.entries)})
	}

	if f.scripts != "" {
		scripts, err := os.ReadDir(f.scripts)
		if err != nil {
			return nil, err
		}
		for _, s := range scripts {
			if s.IsDir() {
				continue
			}
			e, err := textEntry(filepath.Join(f.scripts, s.Name()), "./"+s.Name(), 0o755)
			if err != nil {
				return nil, err
			}
			entries = append(entries, e)
		}
	}
	return tarPack(entries, compressGzip)
}

// md5sums is the list dpkg checks the installed files against: the path as it is
// on the target, two spaces, and the digest, sorted the way dpkg writes it.
func md5sums(entries []pkgEntry) []byte {
	var lines []string
	for _, e := range entries {
		if e.dir {
			continue
		}
		lines = append(lines, md5Hex(e.data)+"  "+strings.TrimPrefix(e.name, "./"))
	}
	sort.Strings(lines)
	return []byte(strings.Join(lines, "\n") + "\n")
}

func writePackage(root, name string, data []byte) error {
	dir := filepath.Join(root, distDir, pkgDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	out := filepath.Join(dir, name)
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("  -> %s\n", describe(root, out))
	return nil
}

// checksumsFile is the list of digests written next to the packages, in the
// spelling the sha256sum tool reads and writes: the digest, two spaces and the
// name. A downloaded release is checked against it, signed or not.
const checksumsFile = "SHA256SUMS"

// writeChecksums writes the digest of every package into SHA256SUMS. Names
// sort the order, so the file comes out the same on every run. Every task that
// writes packages calls this as it finishes, so that an archive and the file
// describing it cannot disagree.
//
// The signature of the list is not a package and is left out of it. A list that
// named its own signature would change the moment it was signed, and the
// signature would then be over bytes that are no longer on disk: the digest of a
// file cannot contain the file that holds the digest of the file.
func writeChecksums(root string) error {
	dir := filepath.Join(root, distDir, pkgDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	var listing strings.Builder
	for _, e := range entries {
		if e.IsDir() || e.Name() == checksumsFile || strings.HasSuffix(e.Name(), update.SignatureSuffix) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		fmt.Fprintf(&listing, "%s  %s\n", sha256Hex(data), e.Name())
	}

	out := filepath.Join(dir, checksumsFile)
	// A list that comes out different than the one a signature was made over is
	// not the list that was signed. The signature goes with it, and sign is run
	// again: a release carries the two of them together or neither, since a
	// stale signature is one every reader refuses to install, with nothing in
	// the release saying why.
	if previous, err := os.ReadFile(out); err == nil && !bytes.Equal(previous, []byte(listing.String())) {
		removeSignature(root, out)
	}
	if err := os.WriteFile(out, []byte(listing.String()), 0o644); err != nil {
		return err
	}
	fmt.Printf("  -> %s\n", describe(root, out))
	return nil
}

// removeSignature deletes the signature of a checksum list that is about to be
// written again with different contents. A missing one is the ordinary state
// before sign has run, so nothing is said about it.
func removeSignature(root, sumsPath string) {
	sigPath := sumsPath + update.SignatureSuffix
	if _, err := os.Stat(sigPath); err != nil {
		return
	}
	if err := os.Remove(sigPath); err != nil {
		fmt.Printf("  !  %s could not be removed: %v; run sign again before publishing\n",
			describe(root, sigPath), err)
		return
	}
	fmt.Printf("  -- %s removed: the list it signs was written again, run sign\n", describe(root, sigPath))
}

// installedBytes is the size of what a package installs. Some formats want it in
// bytes and others in KiB.
func installedBytes(entries []pkgEntry) int64 {
	var total int64
	for _, e := range entries {
		if e.dir {
			continue
		}
		total += int64(len(e.data))
	}
	return total
}

// sha256Hex and md5Hex are the digests the formats ask for, in the spelling they
// all ask for them: lower case hexadecimal.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func md5Hex(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

// gzipFile compresses one file, for the documents a package ships compressed.
func gzipFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return gzipBytes(data)
}

// gzipBytes compresses data with gzip, the one compression the standard library
// writes. The largest thing compressed here is the program a router keeps on
// its flash, so it is packed at the best level.
func gzipBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := gz.Write(data); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// appVersion is the version of the release being built, read from the git tag
// the checkout is at, so every package is named after the tag that published
// it. A tree with no checkout falls back on the Version variable in the
// sources. A build past the tag keeps that name, and the commit written in
// beside it says which one it is.
func appVersion(root string) (string, error) {
	if tag, ok := gitTag(root); ok {
		return tag, nil
	}
	return appSourceString(root, "Version")
}

// appCommit is the commit the release is built from, empty when there is no
// checkout to read it from.
func appCommit(root string) string {
	out, err := git(root, "rev-parse", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

// appRepo reads the repository address out of the source of internal/app, the
// same place the program shows it from, so the two cannot disagree about it.
func appRepo(root string) (string, error) { return appSourceString(root, "Repo") }

// gitTag is the last tag in front of the checkout, without the v this project
// puts in front of it. Reading the last tag makes a build between two releases
// name the release it is past.
func gitTag(root string) (string, bool) {
	out, err := git(root, "describe", "--tags", "--abbrev=0")
	if err != nil || out == "" {
		return "", false
	}
	return strings.TrimPrefix(out, "v"), true
}

// git runs one git command in the repository and returns what it wrote, without
// the newline. A tree that is not a checkout has no git to ask, and the caller
// falls back on what the sources say.
func git(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// appSourceString reads one string out of the payload file of internal/app,
// which the program and the packages both take their version from. The value is
// written as a constant of its own or in a block of variables, so both shapes
// are matched; an empty value is valid, and Commit is one.
func appSourceString(root, name string) (string, error) {
	path := filepath.Join(root, "internal", "app", "payload.go")
	src, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	re := regexp.MustCompile(`(?m)^\s*(?:const\s+|var\s+)?` + name + `\s*=\s*"([^"]*)"`)
	m := re.FindSubmatch(src)
	if m == nil {
		return "", fmt.Errorf("no %s string in %s", name, path)
	}
	return string(m[1]), nil
}
