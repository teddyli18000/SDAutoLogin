// The tests of the packaging half of the task runner, which run with nothing
// installed: `verify` needs the tools of every format, and these do not. Each
// container is packed twice from the same input and the two have to match byte
// for byte, and the expected layout is written out in full, so a change shows
// up as a difference.
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"sdautologin/internal/update"
)

// The version and the address the packages below are made of. Neither comes
// from the tree, since a golden that moved with every release would have to be
// written again for every release, and what is held here is the layout of a
// container.
const (
	goldenVersion = "1.2.3"
	goldenRepo    = "https://example.invalid/SDAutoLogin"
)

// goldenPayload is what every container below is packed from: a program with
// the executable bit, a configuration file, and a file that sits alone in its
// directory, so that the directories leading to them are entries of their
// own. It is written here, and the tests need no build of the program.
var goldenPayload = []struct {
	path string
	mode int64
	data string
}{
	{"/usr/bin/SDAutoLogin", 0o755, "an executable\n"},
	{"/etc/config/sdautologin", 0o644, "config sdautologin 'user'\n"},
	{"/etc/init.d/sdautologin", 0o755, "#!/bin/sh\n"},
}

// goldenWindowsTarget is the build the Windows archive is made of.
var goldenWindowsTarget = target{goos: "windows", goarch: "amd64"}

// goldenEntries is that payload in a format's spelling of a path: the Debian
// archives name their members relative to the root of the target and put "./" in
// front of them, the Arch ones name them relative to it and put nothing.
func goldenEntries(prefix string) []pkgEntry {
	entries := make([]pkgEntry, 0, len(goldenPayload))
	for _, f := range goldenPayload {
		entries = append(entries, pkgEntry{
			name: prefix + strings.TrimPrefix(f.path, "/"),
			mode: f.mode,
			data: []byte(f.data),
		})
	}
	return entries
}

// goldenRpmFiles is that payload as the rpm payload names its files: the
// directories they live in first, each after the one it lives in, then the
// files themselves, the order rpm matches them in.
func goldenRpmFiles() []rpmFile {
	seen := map[string]bool{}
	var dirs []string
	for _, f := range goldenPayload {
		for _, d := range parentDirs(f.path) {
			if seen[d] {
				continue
			}
			seen[d] = true
			dirs = append(dirs, strings.TrimSuffix(d, "/"))
		}
	}
	sort.Strings(dirs)

	files := make([]rpmFile, 0, len(dirs)+len(goldenPayload))
	for _, d := range dirs {
		files = append(files, rpmFile{path: d, mode: 0o755, dir: true})
	}
	for _, f := range goldenPayload {
		files = append(files, rpmFile{path: f.path, mode: f.mode, data: []byte(f.data)})
	}
	return files
}

// goldenDebFiles is a Debian package as the task hands it to the layout: the
// files that are installed, the control file, and a maintainer script, which the
// layout reads out of a directory and so is written into a temporary one.
func goldenDebFiles(t *testing.T) debFiles {
	t.Helper()

	scripts := t.TempDir()
	if err := os.WriteFile(filepath.Join(scripts, "postinst"), []byte(goldenPostinst), 0o755); err != nil {
		t.Fatal(err)
	}
	return debFiles{
		entries:  goldenEntries("./"),
		control:  goldenDebControl,
		scripts:  scripts,
		checksum: true,
	}
}

// goldenRoot is a directory shaped like the repository as far as the Windows
// archive is concerned: the build to pack, the licence and the readme, all three
// read from the places the task reads them from.
func goldenRoot(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, distDir), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		filepath.Join(distDir, goldenWindowsTarget.name()): "an executable\n",
		"LICENSE":   "MIT\n",
		"README.md": "# Campus Auto Login\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// goldenRpm builds the rpm of the payload, without the parts that read the tree.
func goldenRpm() ([]byte, error) {
	files := goldenRpmFiles()
	payload, err := rpmPayload(files)
	if err != nil {
		return nil, err
	}
	arch := "x86_64"
	header := rpmPackageHeader(goldenVersion, pkgRelease, arch, goldenRepo, files, payload)
	return rpmLead(pkgName+"-"+goldenVersion+"-"+pkgRelease, arch, header, payload), nil
}

// goldenPacman builds the Arch package of the payload. It is packed with gzip
// although the task prefers zstd and xz when they are installed: which of the
// three a machine has is not something a test can depend on.
func goldenPacman() ([]byte, error) {
	entries := goldenEntries("")
	mtree, err := gzipBytes(pacmanMtree(entries))
	if err != nil {
		return nil, err
	}
	metadata := []pkgEntry{
		{name: ".PKGINFO", mode: 0o644, data: pacmanPkgInfo(goldenVersion, "x86_64", goldenRepo, entries)},
		{name: ".INSTALL", mode: 0o644, data: []byte(pacmanInstall)},
		{name: ".MTREE", mode: 0o644, data: mtree},
	}
	return tarPack(append(metadata, entries...), compressGzip)
}

// TestEveryContainerIsTheSameBytesTwice covers what a release published with a
// digest next to it depends on: packing the same build again gives the same
// file. Nothing in the packing reads the clock, and this test holds that.
func TestEveryContainerIsTheSameBytesTwice(t *testing.T) {
	for _, c := range []struct {
		name string
		pack func() ([]byte, error)
	}{
		{"the Debian container", func() ([]byte, error) { return debLayout(goldenDebFiles(t)) }},
		{"the Arch container", goldenPacman},
		{"the rpm", goldenRpm},
		{"the Windows archive", func() ([]byte, error) {
			return windowsZip(goldenRoot(t), goldenWindowsTarget)
		}},
		{"a compressed file", func() ([]byte, error) {
			return gzipBytes([]byte(goldenPayload[0].data))
		}},
	} {
		first, err := c.pack()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		second, err := c.pack()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !bytes.Equal(first, second) {
			t.Errorf("%s came out differently the second time: %d byte(s) then, %d now, first difference at byte %d",
				c.name, len(first), len(second), firstDifference(first, second))
		}
	}
}

// TestTheChecksumsFileIsWrittenTheSameWayTwice covers the one file of a
// release that is not a package: the list of digests a download is checked
// against. It names every package in name order, passes over anything else, and
// comes out the same twice.
func TestTheChecksumsFileIsWrittenTheSameWayTwice(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, distDir, pkgDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	packages := []string{
		"sdautologin_1.2.3-1_amd64.deb",
		pkgName + "-" + goldenVersion + "-" + pkgRelease + ".x86_64.rpm",
	}
	for _, name := range packages {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The manifests and the file itself are not packages.
	if err := os.MkdirAll(filepath.Join(dir, manifestDir), 0o755); err != nil {
		t.Fatal(err)
	}

	first := checksums(t, root)
	if second := checksums(t, root); second != first {
		t.Errorf("the file came out differently the second time:\n%q\nthen\n%q", first, second)
	}

	want := sha256Hex([]byte(packages[1]+"\n")) + "  " + packages[1] + "\n" +
		sha256Hex([]byte(packages[0]+"\n")) + "  " + packages[0] + "\n"
	if first != want {
		t.Errorf("the file holds:\n%q\nwant\n%q", first, want)
	}
}

// checksums writes the list and reads it back.
func checksums(t *testing.T, root string) string {
	t.Helper()

	if err := writeChecksums(root); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, distDir, pkgDir, checksumsFile))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestTheChecksumsFileLeavesItsOwnSignatureOut covers the file a list must not
// name: the signature is written after the list and signs its bytes, so a list
// that summed its own signature would change as it was signed and leave a
// signature over bytes that are no longer on disk.
//
// It also covers the pair surviving a second pack: with the signature left out,
// a list written again from the same packages comes out identical, so the
// signature made over it still checks out and a release is not lost to having
// packed twice.
func TestTheChecksumsFileLeavesItsOwnSignatureOut(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, distDir, pkgDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pkg := "sdautologin_1.2.3-1_amd64.deb"
	if err := os.WriteFile(filepath.Join(dir, pkg), []byte(pkg+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := checksums(t, root)
	want := sha256Hex([]byte(pkg+"\n")) + "  " + pkg + "\n"
	if before != want {
		t.Fatalf("the file holds:\n%q\nwant\n%q", before, want)
	}

	// The signature of that list, as sign writes it, and then the list again.
	sig := filepath.Join(dir, checksumsFile+update.SignatureSuffix)
	if err := os.WriteFile(sig, []byte("signature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if after := checksums(t, root); after != before {
		t.Errorf("a signature in the directory changed the list it signs:\n%q\nthen\n%q", before, after)
	}
	if _, err := os.Stat(sig); err != nil {
		t.Errorf("the signature was removed although the list it signs did not change: %v", err)
	}
}

// TestASignatureGoesWhenTheListItSignsChanges covers the other half of that
// pairing: a release carries a list and its signature together or neither, so a
// list that is written again with different contents drops the signature beside
// it rather than shipping one that signs something else.
func TestASignatureGoesWhenTheListItSignsChanges(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, distDir, pkgDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pkg := "sdautologin_1.2.3-1_amd64.deb"
	if err := os.WriteFile(filepath.Join(dir, pkg), []byte(pkg+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	checksums(t, root)
	sig := filepath.Join(dir, checksumsFile+update.SignatureSuffix)
	if err := os.WriteFile(sig, []byte("signature\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A package that was built again with different bytes: the list changes, and
	// what signed the old one cannot stay.
	if err := os.WriteFile(filepath.Join(dir, pkg), []byte("other bytes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	checksums(t, root)
	if _, err := os.Stat(sig); !os.IsNotExist(err) {
		t.Errorf("the signature of the list before is still there: %v", err)
	}
}

// TestTheChecksumsFileOnlyNamesWhatIsThere covers the rewrite a single-format
// task ends with: the file describes the directory as it is now, so a package
// that has gone is not left in it carrying the digest of a build that is no
// longer there.
func TestTheChecksumsFileOnlyNamesWhatIsThere(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, distDir, pkgDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := "sdautologin_1.2.3-1_amd64.deb"
	gone := pkgName + "-" + goldenVersion + "-" + pkgRelease + ".x86_64.rpm"
	for _, name := range []string{keep, gone} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeChecksums(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, gone)); err != nil {
		t.Fatal(err)
	}

	want := sha256Hex([]byte(keep+"\n")) + "  " + keep + "\n"
	if got := checksums(t, root); got != want {
		t.Errorf("the file holds:\n%q\nwant\n%q", got, want)
	}
}

// TestTheGoldenLayoutOfTheDebianContainer pins the container dpkg and opkg
// reads: the ar archive with its three members in order, and the entries of the
// two tar archives inside it. The golden text holds everything of that layout
// no library writes.
func TestTheGoldenLayoutOfTheDebianContainer(t *testing.T) {
	data, err := debLayout(goldenDebFiles(t))
	if err != nil {
		t.Fatal(err)
	}

	members := readAr(t, data)
	var names []string
	for _, m := range members {
		names = append(names, m.name)
	}
	if got := strings.Join(names, " "); got != "debian-binary control.tar.gz data.tar.gz" {
		t.Fatalf("the members are %q", got)
	}
	for _, m := range members {
		if m.mtime != pkgEpoch.Unix() {
			t.Errorf("%s carries the time %d, want the fixed one (%d)", m.name, m.mtime, pkgEpoch.Unix())
		}
		if m.uid != "0" || m.gid != "0" || m.mode != "100644" {
			t.Errorf("%s carries mode %q owned by %q/%q", m.name, m.mode, m.uid, m.gid)
		}
	}
	// The first member is the version of the format, read before anything else.
	if got := string(members[0].data); got != "2.0\n" {
		t.Errorf("debian-binary holds %q, want the version of the format", got)
	}

	if got := strings.Join(readTar(t, members[1].data), "\n") + "\n"; got != wantDebControlLayout {
		t.Errorf("the control archive holds:\n%s", got)
	}
	if got := strings.Join(readTar(t, members[2].data), "\n") + "\n"; got != wantDebDataLayout {
		t.Errorf("the data archive holds:\n%s", got)
	}
}

// TestTheGoldenMetadataOfAnArchPackage pins what pacman reads out of the package
// without unpacking it: what it is and where it came from, and the listing every
// installed file is checked against afterwards.
func TestTheGoldenMetadataOfAnArchPackage(t *testing.T) {
	data, err := goldenPacman()
	if err != nil {
		t.Fatal(err)
	}

	archive := readTarEntries(t, data)
	if len(archive) < 3 {
		t.Fatalf("the archive holds %d entr(y/ies), too few for the three files of metadata", len(archive))
	}
	var names []string
	for _, e := range archive {
		names = append(names, e.hdr.Name)
	}
	if got := strings.Join(names, " "); got != wantPacmanArchive {
		t.Errorf("the archive holds %q", got)
	}

	if got := string(archive[0].data); got != wantPkgInfo {
		t.Errorf("the .PKGINFO holds:\n%s", got)
	}
	if got := string(archive[1].data); got != pacmanInstall {
		t.Errorf("the .INSTALL does not hold the functions pacman runs:\n%s", got)
	}
	if got := string(gunzip(t, archive[2].data)); got != wantMtree {
		t.Errorf("the .MTREE holds:\n%s", got)
	}
}

// TestTheGoldenLayoutOfARpmHeader pins the header of an rpm tag by tag: the set
// of tags, the type and the count of each, and the offset it sits at inside the
// header's data. Nothing but the standard library checks this part of the
// format.
func TestTheGoldenLayoutOfARpmHeader(t *testing.T) {
	pkg, err := goldenRpm()
	if err != nil {
		t.Fatal(err)
	}

	index, data, payload := readRpmHeader(t, pkg)
	if index[0].tag != rpmTagHeaderImmutable || index[0].kind != rpmBin || index[0].count != 16 {
		t.Errorf("the header opens with tag %d of kind %d, which is not the region it has to be",
			index[0].tag, index[0].kind)
	}
	// The digest of the payload ties the header to what follows it: where the
	// payload starts is decided by the length the header declares, so a header
	// that declares the wrong length cannot carry the digest of the rest.
	if got, want := rpmStringValue(t, index, data, rpmTagPayloadDigest), sha256Hex(payload); got != want {
		t.Errorf("the header carries the payload digest %q, and the payload hashes to %q", got, want)
	}

	var lines []string
	previous := 0
	for i, e := range index {
		lines = append(lines, fmt.Sprintf("%d %d %d", e.tag, e.kind, e.count))
		// The entries after the region come in ascending order, the order rpm
		// looks one up in, and every one of them sits inside the data at an
		// offset aligned to the width of its type.
		if i == 0 {
			continue
		}
		if e.tag <= previous {
			t.Errorf("tag %d comes after %d, and the entries are not in ascending order", e.tag, previous)
		}
		previous = e.tag
		width := 1
		switch e.kind {
		case rpmInt16:
			width = 2
		case rpmInt32:
			width = 4
		}
		if e.offset%width != 0 {
			t.Errorf("tag %d of kind %d sits at %d, which is not aligned to %d",
				e.tag, e.kind, e.offset, width)
		}
	}
	if got := strings.Join(lines, "\n") + "\n"; got != wantRpmHeader {
		t.Errorf("the header holds:\n%s", got)
	}
}

// TestTheGoldenLayoutOfAWindowsArchive pins the archive of a Windows build: the
// three files, in the order the manifest and the user expect to find them, with
// the modes the archive records and the fixed time on all of them.
func TestTheGoldenLayoutOfAWindowsArchive(t *testing.T) {
	data, err := windowsZip(goldenRoot(t), goldenWindowsTarget)
	if err != nil {
		t.Fatal(err)
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, f := range zr.File {
		lines = append(lines, fmt.Sprintf("%s %04o %d %d",
			f.Name, f.Mode().Perm(), f.Modified.UTC().Unix(), f.UncompressedSize64))
	}
	if got := strings.Join(lines, "\n") + "\n"; got != wantWindowsArchive {
		t.Errorf("the archive holds:\n%s", got)
	}
}

// arHeader is one member of an ar archive, read back the way dpkg reads it.
type arHeader struct {
	name     string
	mtime    int64
	uid, gid string
	mode     string
	data     []byte
}

// readAr reads an ar archive, checking as it goes the parts of the layout a
// reader depends on: the magic, the marker at the end of every header, the size
// field, and the newline that follows an odd sized member.
func readAr(t *testing.T, archive []byte) []arHeader {
	t.Helper()

	const magic = "!<arch>\n"
	if !bytes.HasPrefix(archive, []byte(magic)) {
		t.Fatalf("the archive does not open with %q", magic)
	}
	rest := archive[len(magic):]

	var members []arHeader
	for len(rest) > 0 {
		if len(rest) < 60 {
			t.Fatalf("%d byte(s) are left, which is too few for a header", len(rest))
		}
		h := string(rest[:60])
		if h[58:] != "`\n" {
			t.Fatalf("a member header does not end with the marker: %q", h)
		}
		mtime, err := strconv.ParseInt(strings.TrimSpace(h[16:28]), 10, 64)
		if err != nil {
			t.Fatalf("a member header carries no time: %q", h)
		}
		size, err := strconv.Atoi(strings.TrimSpace(h[48:58]))
		if err != nil {
			t.Fatalf("a member header carries no size: %q", h)
		}
		if len(rest) < 60+size {
			t.Fatalf("%q says it is %d byte(s) long, and %d are left after its header",
				strings.TrimSpace(h[:16]), size, len(rest)-60)
		}
		member := arHeader{
			name:  strings.TrimSpace(h[:16]),
			mtime: mtime,
			uid:   strings.TrimSpace(h[28:34]),
			gid:   strings.TrimSpace(h[34:40]),
			mode:  strings.TrimSpace(h[40:48]),
			data:  rest[60 : 60+size],
		}
		members = append(members, member)

		rest = rest[60+size:]
		if size%2 == 1 {
			if len(rest) == 0 || rest[0] != '\n' {
				t.Fatalf("%s is of an odd size and is not followed by the padding newline", member.name)
			}
			rest = rest[1:]
		}
	}
	return members
}

// readTar reads a gzipped tar archive and lists its entries as one line each:
// the kind, the name, the mode, the time and the size. That is all this
// repository decides when it writes an entry; the library fills in the rest of
// the header.
func readTar(t *testing.T, data []byte) []string {
	t.Helper()

	var lines []string
	for _, e := range readTarEntries(t, data) {
		kind := "file"
		if e.hdr.Typeflag == tar.TypeDir {
			kind = "dir"
		}
		lines = append(lines, fmt.Sprintf("%s %s %04o %d %d",
			kind, e.hdr.Name, e.hdr.Mode, e.hdr.ModTime.Unix(), e.hdr.Size))
	}
	return lines
}

// tarEntry is one entry of an archive, with the contents that were read with it.
type tarEntry struct {
	hdr  *tar.Header
	data []byte
}

// readTarEntries reads a gzipped tar archive into memory. The archives here are
// small enough for that.
func readTarEntries(t *testing.T, data []byte) []tarEntry {
	t.Helper()

	tr := tar.NewReader(bytes.NewReader(gunzip(t, data)))
	var entries []tarEntry
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return entries
		}
		if err != nil {
			t.Fatalf("reading the archive: %v", err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("reading %s: %v", hdr.Name, err)
		}
		entries = append(entries, tarEntry{hdr: hdr, data: body})
	}
}

func gunzip(t *testing.T, data []byte) []byte {
	t.Helper()

	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("the archive is not gzipped: %v", err)
	}
	plain, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("decompressing: %v", err)
	}
	return plain
}

// rpmIndex is one entry of an rpm header index, read back the way rpm reads it.
type rpmIndex struct {
	tag, kind, count, offset int
}

// readRpmHeader reads the header of a package out of the file, together with
// the data its tags point into and the payload that follows it. After the lead
// sits the signature header on an eight byte boundary, then the header of the
// package. Both are read through the length they declare, so a length one byte
// out is caught here.
func readRpmHeader(t *testing.T, pkg []byte) ([]rpmIndex, []byte, []byte) {
	t.Helper()

	const leadLen = 96
	const magicLen = 8
	magic := []byte{0x8e, 0xad, 0xe8, 0x01, 0, 0, 0, 0}
	if len(pkg) < leadLen+16 {
		t.Fatalf("the file is %d byte(s) long, which is too short for an rpm", len(pkg))
	}

	signature := pkg[leadLen:]
	sigCount := int(binary.BigEndian.Uint32(signature[8:12]))
	sigData := int(binary.BigEndian.Uint32(signature[12:16]))
	start := leadLen + 16 + sigCount*16 + sigData
	start += (magicLen - start%magicLen) % magicLen
	if len(pkg) < start+16 {
		t.Fatalf("the header of the package would start at %d, past the end of a %d byte file", start, len(pkg))
	}
	header := pkg[start:]
	if !bytes.HasPrefix(header, magic) {
		t.Fatalf("the signature header says the package header starts at %d, and what is there is not one", start)
	}

	count := int(binary.BigEndian.Uint32(header[8:12]))
	dataLen := int(binary.BigEndian.Uint32(header[12:16]))
	if len(header) < 16+count*16+dataLen {
		t.Fatalf("the header says it has %d entries over %d byte(s) of data, and %d byte(s) follow it",
			count, dataLen, len(header)-16)
	}

	index := make([]rpmIndex, count)
	for i := range index {
		e := header[16+16*i:]
		index[i] = rpmIndex{
			tag:    int(binary.BigEndian.Uint32(e[0:4])),
			kind:   int(binary.BigEndian.Uint32(e[4:8])),
			offset: int(binary.BigEndian.Uint32(e[8:12])),
			count:  int(binary.BigEndian.Uint32(e[12:16])),
		}
		if index[i].offset < 0 || index[i].offset >= dataLen {
			t.Errorf("tag %d sits at %d, outside the %d byte(s) of data its header declares",
				index[i].tag, index[i].offset, dataLen)
		}
	}
	body := 16 + count*16
	return index, header[body : body+dataLen], header[body+dataLen:]
}

// rpmStringValue is the value of a tag that holds one string. The value is
// terminated by a zero, which the count of the tag does not count.
func rpmStringValue(t *testing.T, index []rpmIndex, data []byte, tag int) string {
	t.Helper()

	for _, e := range index {
		if e.tag != tag {
			continue
		}
		rest := data[e.offset:]
		end := bytes.IndexByte(rest, 0)
		if end < 0 {
			t.Fatalf("tag %d is not terminated", tag)
		}
		return string(rest[:end])
	}
	t.Fatalf("the header holds no tag %d", tag)
	return ""
}

// firstDifference is where two byte strings start to differ, so that a failure
// says more than that the two are not equal.
func firstDifference(a, b []byte) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

// The control file and the maintainer script the golden Debian container is
// made of. They are short, since what is held here is the layout of the archive
// around them.
const (
	goldenDebControl = "Package: sdautologin\nVersion: 1.2.3-1\n"
	goldenPostinst   = "#!/bin/sh\nexit 0\n"
)

// The expected layout of every container, in the reading of the helpers above.
const (
	wantDebControlLayout = `dir ./ 0755 1577836800 0
file ./control 0644 1577836800 38
file ./md5sums 0644 1577836800 168
file ./postinst 0755 1577836800 17
`
	wantDebDataLayout = `dir ./ 0755 1577836800 0
dir ./usr/ 0755 1577836800 0
dir ./usr/bin/ 0755 1577836800 0
file ./usr/bin/SDAutoLogin 0755 1577836800 14
dir ./etc/ 0755 1577836800 0
dir ./etc/config/ 0755 1577836800 0
file ./etc/config/sdautologin 0644 1577836800 26
dir ./etc/init.d/ 0755 1577836800 0
file ./etc/init.d/sdautologin 0755 1577836800 10
`
	wantPacmanArchive = ".PKGINFO .INSTALL .MTREE usr/ usr/bin/ usr/bin/SDAutoLogin " +
		"etc/ etc/config/ etc/config/sdautologin etc/init.d/ etc/init.d/sdautologin"
	wantPkgInfo = `# Generated by the build task of SDAutoLogin
pkgname = sdautologin
pkgbase = sdautologin
pkgver = 1.2.3-1
pkgdesc = Campus network auto login
url = https://example.invalid/SDAutoLogin
builddate = 1577836800
packager = Zhiqiang Lan <zqlan@suda.edu.cn>
size = 50
arch = x86_64
license = MIT
`
	wantMtree = `#mtree
/set type=file uid=0 gid=0 mode=644
./usr mode=755 time=1577836800.0 type=dir
./usr/bin mode=755 time=1577836800.0 type=dir
./usr/bin/SDAutoLogin mode=755 time=1577836800.0 size=14 md5digest=d9629c4790289655847d3bfc213b9d13 sha256digest=7c63bdb27d4efed9c75a7760601602146171dba674c459dde948c3f830dcc4f9
./etc mode=755 time=1577836800.0 type=dir
./etc/config mode=755 time=1577836800.0 type=dir
./etc/config/sdautologin mode=644 time=1577836800.0 size=26 md5digest=d1a246b30101367c3de4278fec37bf73 sha256digest=a87904c6093e1ae602d6bbe018d55104538281d861fdbd76c820e84a1401c302
./etc/init.d mode=755 time=1577836800.0 type=dir
./etc/init.d/sdautologin mode=755 time=1577836800.0 size=10 md5digest=3e2b31c72181b87149ff995e7202c0e3 sha256digest=a8076d3d28d21e02012b20eaf7dbf75409a6277134439025f282e368e3305abf
`
	wantRpmHeader = `63 7 16
100 8 1
1000 6 1
1001 6 1
1002 6 1
1004 9 1
1005 9 1
1006 4 1
1007 6 1
1009 4 1
1014 6 1
1015 6 1
1016 9 1
1020 6 1
1021 6 1
1022 6 1
1024 6 1
1025 6 1
1026 6 1
1028 4 8
1030 3 8
1033 3 8
1034 4 8
1035 8 8
1036 8 8
1037 4 8
1039 8 8
1040 8 8
1047 8 1
1048 4 1
1049 8 1
1050 8 1
1086 6 1
1087 6 1
1088 6 1
1095 4 8
1096 4 8
1097 8 8
1112 4 1
1113 8 1
1116 4 8
1117 8 8
1118 8 6
1124 6 1
1125 6 1
1126 6 1
5011 4 1
5062 6 1
5092 8 1
5093 4 1
`
	wantWindowsArchive = `SDAutoLogin.exe 0755 1577836800 14
LICENSE 0644 1577836800 4
README.md 0644 1577836800 20
`
)
