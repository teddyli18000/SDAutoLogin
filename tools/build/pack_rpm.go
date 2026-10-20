// The RPM package: one .rpm per architecture, for the distributions that read
// this format. The header is a tagged binary structure, so it is written here
// tag by tag, and the payload is a cpio archive inside a gzip stream. The tag
// numbers come from a package built by rpmbuild.

package main

import (
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// The types a tag can have. They decide how its data is laid out and how the
// tag is aligned inside the header.
const (
	rpmInt16   = 3
	rpmInt32   = 4
	rpmString  = 6
	rpmBin     = 7
	rpmStrings = 8
	rpmI18n    = 9
)

// The tags of the format. The ones in the low hundreds and the four shared
// with the header namespace mean something else in a signature header than in
// the package header, so the same number appears twice in this file.
const (
	rpmTagHeaderSignatures  = 62
	rpmTagHeaderImmutable   = 63
	rpmTagHeaderI18nTable   = 100
	rpmTagSigSize           = 1000 // size of the header and the payload
	rpmTagSigMD5            = 1004 // md5 of the header and the payload
	rpmTagSigPayloadSize    = 1007
	rpmTagSigReservedSpace  = 1008
	rpmTagSHA1Header        = 269
	rpmTagSHA256Header      = 273
	rpmTagName              = 1000
	rpmTagVersion           = 1001
	rpmTagRelease           = 1002
	rpmTagSummary           = 1004
	rpmTagDescription       = 1005
	rpmTagBuildTime         = 1006
	rpmTagBuildHost         = 1007
	rpmTagSize              = 1009
	rpmTagLicense           = 1014
	rpmTagPackager          = 1015
	rpmTagGroup             = 1016
	rpmTagURL               = 1020
	rpmTagOS                = 1021
	rpmTagArch              = 1022
	rpmTagPostIn            = 1024
	rpmTagPreUn             = 1025
	rpmTagPostUn            = 1026
	rpmTagFileSizes         = 1028
	rpmTagFileModes         = 1030
	rpmTagFileRDevs         = 1033
	rpmTagFileMTimes        = 1034
	rpmTagFileDigests       = 1035
	rpmTagFileLinkTos       = 1036
	rpmTagFileFlags         = 1037
	rpmTagFileUserName      = 1039
	rpmTagFileGroupName     = 1040
	rpmTagProvideName       = 1047
	rpmTagRequireFlags      = 1048
	rpmTagRequireName       = 1049
	rpmTagRequireVersion    = 1050
	rpmTagPostInProg        = 1086
	rpmTagPreUnProg         = 1087
	rpmTagPostUnProg        = 1088
	rpmTagFileDevices       = 1095
	rpmTagFileInodes        = 1096
	rpmTagFileLangs         = 1097
	rpmTagProvideFlags      = 1112
	rpmTagProvideVersion    = 1113
	rpmTagDirIndexes        = 1116
	rpmTagBaseNames         = 1117
	rpmTagDirNames          = 1118
	rpmTagPayloadFormat     = 1124
	rpmTagPayloadCompressor = 1125
	rpmTagPayloadFlags      = 1126
	rpmTagFileDigestAlgo    = 5011
	rpmTagEncoding          = 5062
	rpmTagPayloadDigest     = 5092
	rpmTagPayloadDigestAlgo = 5093
)

// The bits of a dependency flag that this file needs: a package depends on the
// shell that runs its scriptlets, and it provides its own name and version.
const (
	rpmSenseEqual  = 1 << 3
	rpmSenseInterp = 1 << 8
)

// rpmDigestSHA256 is the number the format uses for a SHA-256 digest, in the
// tag that says which algorithm the file digests use and in the one that says
// which the payload digest uses.
const rpmDigestSHA256 = 8

// isRegular and isDirectory are the top bits of a Unix mode, marking a plain
// file and a directory. The payload repeats the file list, and rpm takes a
// directory from this bit.
const (
	isRegular   = 0o100000
	isDirectory = 0o040000
)

// rpmFile is one entry the package installs: a file, or a directory the package
// owns, which lets rpm take it away again.
type rpmFile struct {
	path string
	mode int64
	dir  bool
	data []byte
}

// modeBits is what goes into the file list and into the payload for this entry.
func (f rpmFile) modeBits() int {
	if f.dir {
		return int(f.mode) | isDirectory
	}
	return int(f.mode) | isRegular
}

// taskRpm builds the RPM packages, one for each architecture, into
// dist/packages/. The build without the tray is the one that goes in: the
// package runs the program as a service, and a desktop uses the archive.
func taskRpm(root string) error {
	for _, t := range targets() {
		// No distribution in this family runs on MIPS: those builds belong to
		// the routers, and they have their own package.
		if t.tags != notrayTag || t.goos != "linux" || t.goarch == "mips" || t.goarch == "mipsle" {
			continue
		}
		if err := buildTarget(root, t); err != nil {
			return err
		}
		if err := packageRpm(root, t); err != nil {
			return err
		}
	}
	// The digests are rewritten here too, this task being registered on its own as
	// well as under taskPackages.
	return writeChecksums(root)
}

func packageRpm(root string, t target) error {
	rel, err := packageVersion(root)
	if err != nil {
		return err
	}
	repo, err := appRepo(root)
	if err != nil {
		return err
	}
	arch := rpmArch(t.goarch)
	files, err := rpmFiles(root, t)
	if err != nil {
		return err
	}

	payload, err := rpmPayload(files)
	if err != nil {
		return err
	}
	version, release := rel.rpm()
	header := rpmPackageHeader(version, release, arch, repo, files, payload)
	name := fmt.Sprintf("%s-%s-%s.%s.rpm", pkgName, version, release, arch)
	// The lead names the package the way a file name does, without the
	// architecture and the ending.
	leadName := fmt.Sprintf("%s-%s-%s", pkgName, version, release)
	return writePackage(root, name, rpmLead(leadName, arch, header, payload))
}

// rpmFiles is what the package installs, in the spelling of the rpm payload: the
// directories the files live in, then the files themselves. The directories are
// listed because rpm takes away only what it knows it installed, and `rpm -ql`
// shows them. A directory the system already has is named as well.
func rpmFiles(root string, t target) ([]rpmFile, error) {
	entries, err := systemdFiles(root, filepath.Join(root, distDir, t.name()))
	if err != nil {
		return nil, err
	}

	var dirs []string
	seen := map[string]bool{}
	for _, e := range entries {
		for _, d := range parentDirs(e.name) {
			if seen[d] {
				continue
			}
			seen[d] = true
			// parentDirs keeps the trailing slash, as a tar entry of a directory
			// carries one, and rpm writes the path without it.
			dirs = append(dirs, strings.TrimSuffix(d, "/"))
		}
	}
	// Sorting puts every directory after the one it lives in, the order an
	// archive is read in.
	sort.Strings(dirs)

	files := make([]rpmFile, 0, len(entries)+len(dirs))
	for _, d := range dirs {
		files = append(files, rpmFile{path: d, mode: 0o755, dir: true})
	}
	for _, e := range entries {
		files = append(files, rpmFile{path: e.name, mode: e.mode, data: e.data})
	}
	return files, nil
}

// rpmLead is the fixed 96 byte block an rpm starts with. The name and the
// architecture come from the header, but rpm will not read a file without it.
func rpmLead(name, arch string, header, payload []byte) []byte {
	var b bytes.Buffer
	b.Write([]byte{0xed, 0xab, 0xee, 0xdb})
	b.Write([]byte{3, 0}) // version of the format
	b.Write([]byte{0, 0}) // type: a binary package
	b.Write([]byte{0, rpmLeadArch(arch)})
	b.Write(fixedString(name, 66))
	b.Write([]byte{0, 1}) // the operating system: linux
	b.Write([]byte{0, 5}) // the signature is a header of its own
	b.Write(make([]byte, 16))

	b.Write(rpmSignatureHeader(header, payload))
	// The signature header is padded so that what follows starts on an eight
	// byte boundary.
	if pad := (8 - b.Len()%8) % 8; pad > 0 {
		b.Write(make([]byte, pad))
	}
	b.Write(header)
	b.Write(payload)
	return b.Bytes()
}

// rpmLeadArch is the architecture number of the lead. rpm takes the
// architecture from the header instead, so the builds that came later share the
// number of the one they grew out of.
func rpmLeadArch(arch string) byte {
	switch arch {
	case "x86_64":
		return 1
	case "aarch64":
		return 12
	case "i686":
		return 1
	case "armv7hl":
		return 12
	}
	return 1
}

// rpmSignatureHeader is the header that carries the digests of what follows it.
// rpm checks them before it looks at anything else, and refuses a package whose
// header digest does not match.
func rpmSignatureHeader(header, payload []byte) []byte {
	sum256 := sha256.Sum256(header)
	sum1 := sha1.Sum(header)
	both := md5.New()
	both.Write(header)
	both.Write(payload)

	// The space a signature would need is reserved here, so that signing a
	// package later does not have to rewrite it.
	reserved := make([]byte, 4128)
	return rpmHeader(rpmTagHeaderSignatures, []rpmEntry{
		rpmInt32Entry(rpmTagSigSize, int32(len(header)+len(payload))),
		rpmBinEntry(rpmTagSigMD5, both.Sum(nil)),
		rpmInt32Entry(rpmTagSigPayloadSize, int32(len(payload))),
		rpmBinEntry(rpmTagSigReservedSpace, reserved),
		rpmStringEntry(rpmTagSHA1Header, hex.EncodeToString(sum1[:])),
		rpmStringEntry(rpmTagSHA256Header, hex.EncodeToString(sum256[:])),
	})
}

// rpmPackageHeader is the header that describes the package: what it is, what it
// installs, and what has to be run around that.
func rpmPackageHeader(version, release, arch, repo string, files []rpmFile, payload []byte) []byte {
	dirs, indexes := rpmDirs(files)

	var entries []rpmEntry
	entries = append(entries,
		rpmStringsEntry(rpmTagHeaderI18nTable, []string{"C"}),
		rpmStringEntry(rpmTagName, pkgName),
		rpmStringEntry(rpmTagVersion, version),
		rpmStringEntry(rpmTagRelease, release),
		rpmI18nEntry(rpmTagSummary, pkgSummary),
		rpmI18nEntry(rpmTagDescription, rpmDescription()),
		rpmInt32Entry(rpmTagBuildTime, int32(pkgEpoch.Unix())),
		rpmStringEntry(rpmTagBuildHost, "localhost"),
		rpmInt32Entry(rpmTagSize, int32(rpmInstalledSize(files))),
		rpmStringEntry(rpmTagLicense, "MIT"),
		rpmStringEntry(rpmTagPackager, pkgMaintainer),
		rpmI18nEntry(rpmTagGroup, "Unspecified"),
		rpmStringEntry(rpmTagURL, repo),
		rpmStringEntry(rpmTagOS, "linux"),
		rpmStringEntry(rpmTagArch, arch),
		rpmStringEntry(rpmTagPostIn, rpmPostIn),
		rpmStringEntry(rpmTagPreUn, rpmPreUn),
		rpmStringEntry(rpmTagPostUn, rpmPostUn),
		rpmInt32sEntry(rpmTagFileSizes, rpmFileSizes(files)),
		rpmInt16sEntry(rpmTagFileModes, rpmFileModes(files)),
		rpmInt16sEntry(rpmTagFileRDevs, make([]int16, len(files))),
		rpmInt32sEntry(rpmTagFileMTimes, rpmRepeated(len(files), int32(pkgEpoch.Unix()))),
		rpmStringsEntry(rpmTagFileDigests, rpmFileDigests(files)),
		rpmStringsEntry(rpmTagFileLinkTos, make([]string, len(files))),
		rpmInt32sEntry(rpmTagFileFlags, make([]int32, len(files))),
		rpmStringsEntry(rpmTagFileUserName, rpmRepeatedString(len(files), "root")),
		rpmStringsEntry(rpmTagFileGroupName, rpmRepeatedString(len(files), "root")),
		rpmStringsEntry(rpmTagProvideName, []string{pkgName}),
		rpmInt32sEntry(rpmTagRequireFlags, []int32{rpmSenseInterp}),
		rpmStringsEntry(rpmTagRequireName, []string{"/bin/sh"}),
		rpmStringsEntry(rpmTagRequireVersion, []string{""}),
		rpmStringEntry(rpmTagPostInProg, "/bin/sh"),
		rpmStringEntry(rpmTagPreUnProg, "/bin/sh"),
		rpmStringEntry(rpmTagPostUnProg, "/bin/sh"),
		rpmInt32sEntry(rpmTagFileDevices, rpmRepeated(len(files), 1)),
		rpmInt32sEntry(rpmTagFileInodes, rpmInodes(len(files))),
		rpmStringsEntry(rpmTagFileLangs, make([]string, len(files))),
		rpmInt32sEntry(rpmTagProvideFlags, []int32{rpmSenseEqual}),
		rpmStringsEntry(rpmTagProvideVersion, []string{version + "-" + release}),
		rpmInt32sEntry(rpmTagDirIndexes, indexes),
		rpmStringsEntry(rpmTagBaseNames, rpmBaseNames(files)),
		rpmStringsEntry(rpmTagDirNames, dirs),
		rpmStringEntry(rpmTagPayloadFormat, "cpio"),
		rpmStringEntry(rpmTagPayloadCompressor, "gzip"),
		rpmStringEntry(rpmTagPayloadFlags, "9"),
		rpmInt32Entry(rpmTagFileDigestAlgo, rpmDigestSHA256),
		rpmStringEntry(rpmTagEncoding, "utf-8"),
		rpmStringsEntry(rpmTagPayloadDigest, []string{sha256Hex(payload)}),
		rpmInt32Entry(rpmTagPayloadDigestAlgo, rpmDigestSHA256),
	)
	return rpmHeader(rpmTagHeaderImmutable, entries)
}

// rpmDescription is what a package manager shows about the package.
func rpmDescription() string {
	return pkgSummary + ".\n" +
		"Keeps a Dr.COM ePortal session alive, checking the portal every few seconds and\n" +
		"signing back in as soon as the session is gone. It never signs the session out,\n" +
		"and touches nothing else.\n" +
		"\n" +
		"The service runs as the " + pkgName + " system user, with its settings and its log\n" +
		"in /var/lib/" + pkgName + ". An account has to be written down once before the\n" +
		"service stays up; see /usr/share/doc/" + pkgName + "/README.md.\n"
}

// The scriptlets, the same work the Debian maintainer scripts do in the
// spelling rpm expects: what to run after the files are in place, before they
// are taken away, and after that, where the argument is how many packages will
// still be installed.
const rpmPostIn = `# The user the service runs as comes from the file this package installs, which
# nothing reads on its own: the trigger systemd ships only covers its own users.
if [ -x /usr/bin/systemd-sysusers ]; then
	systemd-sysusers /usr/lib/sysusers.d/sdautologin.conf >/dev/null 2>&1 || :
fi
systemctl --system daemon-reload >/dev/null 2>&1 || :
systemctl --no-reload preset sdautologin.service >/dev/null 2>&1 || :
if [ ! -f /var/lib/sdautologin/config.json ]; then
	echo "sdautologin has no account yet, so the service will keep stopping."
	echo "See /usr/share/doc/sdautologin/README.md for how to give it one."
fi
`

const rpmPreUn = `# Zero means the package is going away, one that it is being replaced, in which
# case the running instance is left to the package that is taking over.
case "${1:-0}" in
0)
	systemctl --no-reload disable --now sdautologin.service >/dev/null 2>&1 || :
	;;
esac
`

const rpmPostUn = `systemctl daemon-reload >/dev/null 2>&1 || :
case "${1:-0}" in
0) ;;
*)
	# An upgrade: the instance that is still running has the file that was just
	# replaced underneath it, so it is started again.
	systemctl try-restart sdautologin.service >/dev/null 2>&1 || :
	;;
esac
`

// rpmPayload is the payload of a package: the files and the directories as a
// cpio archive inside a gzip stream. The entries keep the order of the file
// list, the order rpm matches them in.
func rpmPayload(files []rpmFile) ([]byte, error) {
	var cpio bytes.Buffer
	for i, f := range files {
		rpmCpioEntry(&cpio, f, i+1)
	}
	rpmCpioTrailer(&cpio)

	var out bytes.Buffer
	gz, err := gzip.NewWriterLevel(&out, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := gz.Write(cpio.Bytes()); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// rpmCpioEntry writes one entry of the newc cpio format: a header of thirteen
// fields in hexadecimal, the path, and the contents, with the header, the path
// and the contents each rounded up to four bytes.
func rpmCpioEntry(b *bytes.Buffer, f rpmFile, ino int) {
	name := f.path + "\x00"
	fields := []int{
		ino,
		f.modeBits(),
		0, // the owner and the group, which rpm takes from the file list instead
		0,
		1, // hard links: a plain file or a directory has one
		int(pkgEpoch.Unix()),
		len(f.data),
		0, 0, 0, 0, // the device numbers, which neither of them has
		len(name),
		0, // a checksum of the header, which readers ignore
	}
	b.WriteString("070701")
	for _, v := range fields {
		fmt.Fprintf(b, "%08x", v)
	}
	b.WriteString(name)
	rpmCpioPad(b)
	b.Write(f.data)
	rpmCpioPad(b)
}

// rpmCpioTrailer ends the archive, the marker an extractor reads until.
func rpmCpioTrailer(b *bytes.Buffer) {
	fmt.Fprintf(b, "070701%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x",
		0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, len("TRAILER!!!\x00"), 0)
	b.WriteString("TRAILER!!!\x00")
	rpmCpioPad(b)
}

// rpmCpioPad rounds the archive up to the next four byte boundary.
func rpmCpioPad(b *bytes.Buffer) {
	for b.Len()%4 != 0 {
		b.WriteByte(0)
	}
}

// rpmDirs splits the paths into the directories they live in and the index of
// the one each file is in. The format stores a file as a directory, its index
// and a base name, so a package with many files in one directory writes that
// directory once.
func rpmDirs(files []rpmFile) ([]string, []int32) {
	var dirs []string
	var indexes []int32
	seen := map[string]int32{}
	for _, f := range files {
		dir := f.path[:strings.LastIndex(f.path, "/")+1]
		idx, ok := seen[dir]
		if !ok {
			idx = int32(len(dirs))
			seen[dir] = idx
			dirs = append(dirs, dir)
		}
		indexes = append(indexes, idx)
	}
	return dirs, indexes
}

// rpmBaseNames is the file names without the directories in front of them.
func rpmBaseNames(files []rpmFile) []string {
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.path[strings.LastIndex(f.path, "/")+1:]
	}
	return names
}

// rpmFileSizes and the rest turn the files into the arrays the header wants,
// one entry per file.
func rpmFileSizes(files []rpmFile) []int32 {
	sizes := make([]int32, len(files))
	for i, f := range files {
		sizes[i] = int32(len(f.data))
	}
	return sizes
}

func rpmFileModes(files []rpmFile) []int16 {
	modes := make([]int16, len(files))
	for i, f := range files {
		modes[i] = int16(f.modeBits())
	}
	return modes
}

// rpmFileDigests is the digest of every file. A directory has none, which the
// format writes as an empty string.
func rpmFileDigests(files []rpmFile) []string {
	digests := make([]string, len(files))
	for i, f := range files {
		if f.dir {
			continue
		}
		digests[i] = sha256Hex(f.data)
	}
	return digests
}

func rpmInstalledSize(files []rpmFile) int64 {
	var total int64
	for _, f := range files {
		total += int64(len(f.data))
	}
	return total
}

// rpmInodes numbers the files. rpm uses the number to tell an upgrade that
// replaces a file from one that follows links to it.
func rpmInodes(n int) []int32 {
	inodes := make([]int32, n)
	for i := range inodes {
		inodes[i] = int32(i + 1)
	}
	return inodes
}

func rpmRepeated(n int, value int32) []int32 {
	values := make([]int32, n)
	for i := range values {
		values[i] = value
	}
	return values
}

func rpmRepeatedString(n int, value string) []string {
	values := make([]string, n)
	for i := range values {
		values[i] = value
	}
	return values
}

// rpmArch is the architecture name rpm gives a build. GOARCH spells all four of
// them differently.
func rpmArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	case "386":
		return "i686"
	case "arm":
		return "armv7hl"
	}
	return goarch
}

// rpmEntry is one tag, with its data already encoded the way the format wants
// it.
type rpmEntry struct {
	tag   int
	kind  int
	count int
	data  []byte
}

func rpmStringEntry(tag int, value string) rpmEntry {
	return rpmEntry{tag: tag, kind: rpmString, count: 1, data: append([]byte(value), 0)}
}

// rpmI18nEntry is a string that is supposed to be translated. The format keeps
// one of them per language of the index, and there is only one language here, so
// the data is a plain string.
func rpmI18nEntry(tag int, value string) rpmEntry {
	return rpmEntry{tag: tag, kind: rpmI18n, count: 1, data: append([]byte(value), 0)}
}

func rpmStringsEntry(tag int, values []string) rpmEntry {
	var data []byte
	for _, v := range values {
		data = append(data, v...)
		data = append(data, 0)
	}
	return rpmEntry{tag: tag, kind: rpmStrings, count: len(values), data: data}
}

func rpmInt32Entry(tag int, value int32) rpmEntry {
	return rpmInt32sEntry(tag, []int32{value})
}

func rpmInt32sEntry(tag int, values []int32) rpmEntry {
	data := make([]byte, 4*len(values))
	for i, v := range values {
		binary.BigEndian.PutUint32(data[4*i:], uint32(v))
	}
	return rpmEntry{tag: tag, kind: rpmInt32, count: len(values), data: data}
}

func rpmInt16sEntry(tag int, values []int16) rpmEntry {
	data := make([]byte, 2*len(values))
	for i, v := range values {
		binary.BigEndian.PutUint16(data[2*i:], uint16(v))
	}
	return rpmEntry{tag: tag, kind: rpmInt16, count: len(values), data: data}
}

func rpmBinEntry(tag int, data []byte) rpmEntry {
	return rpmEntry{tag: tag, kind: rpmBin, count: len(data), data: data}
}

// rpmHeader lays the tags out the way a header is stored: the magic, the index,
// and then the data of every tag, each aligned to the width of its type and
// addressed by its offset in that data. A region entry at the front of the
// index holds a sixteen byte trailer with the entry count; its tag differs
// between the two headers, and rpm refuses a header with the wrong one.
func rpmHeader(regionTag int, entries []rpmEntry) []byte {
	sort.Slice(entries, func(i, j int) bool { return entries[i].tag < entries[j].tag })

	type placed struct {
		rpmEntry
		offset int
	}
	index := make([]placed, 0, len(entries)+1)
	var data bytes.Buffer
	for _, e := range entries {
		for data.Len()%rpmAlignment(e.kind) != 0 {
			data.WriteByte(0)
		}
		index = append(index, placed{rpmEntry: e, offset: data.Len()})
		data.Write(e.data)
	}

	for data.Len()%4 != 0 {
		data.WriteByte(0)
	}
	count := len(index) + 1
	trailer := rpmInt32sEntry(regionTag, []int32{
		int32(regionTag), rpmBin, -16 * int32(count), 16,
	})
	region := placed{
		rpmEntry: rpmEntry{tag: regionTag, kind: rpmBin, count: 16},
		offset:   data.Len(),
	}
	data.Write(trailer.data)

	var b bytes.Buffer
	b.Write([]byte{0x8e, 0xad, 0xe8, 0x01, 0, 0, 0, 0})
	rpmWriteInt32(&b, int32(len(index)+1))
	rpmWriteInt32(&b, int32(data.Len()))
	for _, e := range append([]placed{region}, index...) {
		rpmWriteInt32(&b, int32(e.tag))
		rpmWriteInt32(&b, int32(e.kind))
		rpmWriteInt32(&b, int32(e.offset))
		rpmWriteInt32(&b, int32(e.count))
	}
	b.Write(data.Bytes())
	return b.Bytes()
}

// rpmAlignment is the width a tag of this type is aligned to inside the data of
// a header.
func rpmAlignment(kind int) int {
	switch kind {
	case rpmInt16:
		return 2
	case rpmInt32:
		return 4
	}
	return 1
}

func rpmWriteInt32(b *bytes.Buffer, value int32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(value))
	b.Write(buf[:])
}

// fixedString is a string written into a field of a fixed width and padded with
// zeroes, the way the lead carries a name.
func fixedString(s string, width int) []byte {
	out := make([]byte, width)
	copy(out, s)
	return out
}
