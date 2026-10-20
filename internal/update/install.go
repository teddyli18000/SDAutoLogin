package update

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// progName is the name the archives carry, as the release build writes it.
	progName = "SDAutoLogin"
	// archiveTimeout is how long a release archive may take. It is megabytes
	// over a campus network, so it is minutes rather than seconds.
	archiveTimeout = 5 * time.Minute
	// maxArchiveBytes bounds an archive before it is read into memory. The
	// Windows one is about 3 MiB; the bound is loose enough to hold a larger
	// build and tight enough that a wrong address does not fill the disk.
	maxArchiveBytes = 128 << 20
	// leftoverSuffix is what the copy being replaced is renamed to. It is
	// removed on the next start, since a program cannot delete the file it is
	// running from.
	leftoverSuffix = ".old"
)

// archiveIn is the plain archive of one platform inside a signed checksum list,
// with the version its name carries.
//
// The name of the file to install is taken from the list and not built from the
// release tag: the list is covered by the signature and the tag is not. It is
// this that keeps the protection against going backwards explicit rather than a
// property of the way ArchiveName happens to spell a name: if the archive of a
// platform stops being named after its version, the lookup here fails loudly
// instead of finding an older file and installing it.
func archiveIn(list Checksums, goos, goarch string) (string, Version, error) {
	prefix := progName + "-"
	suffix := "-" + goos + "-" + goarch + ".zip"
	var (
		name  string
		ver   Version
		found bool
	)
	for candidate := range list {
		if !strings.HasPrefix(candidate, prefix) || !strings.HasSuffix(candidate, suffix) {
			continue
		}
		v, err := ParseVersion(strings.TrimSuffix(strings.TrimPrefix(candidate, prefix), suffix))
		if err != nil {
			return "", Version{}, fmt.Errorf("the signed list names %s, whose version cannot be read: %w", candidate, err)
		}
		if found {
			return "", Version{}, fmt.Errorf("the signed list names two archives for %s, %s and %s", goos+"/"+goarch, name, candidate)
		}
		name, ver, found = candidate, v, true
	}
	if !found {
		return "", Version{}, fmt.Errorf("the signed list names no archive for %s", goos+"/"+goarch)
	}
	return name, ver, nil
}

// ArchiveName is the name of the plain archive of one platform's build, and
// whether this program publishes one at all. It answers whether a platform is
// one the program ships an archive for; the name of the file an update installs
// is read out of the signed checksum list by archiveIn, never from here.
//
// Only Windows does. Everywhere else the program arrives inside a package the
// package manager owns, and replacing that file by hand would leave the
// manager's database describing a file that is no longer there. The check still
// runs on those platforms; what it finds is reported, and the reader is sent to
// the package manager.
func ArchiveName(version, goos, goarch string) (string, bool) {
	if goos != "windows" {
		return "", false
	}
	switch goarch {
	case "amd64", "arm64", "386":
	default:
		return "", false
	}
	return fmt.Sprintf("%s-%s-windows-%s.zip", progName, version, goarch), true
}

// Target is where a release would go: the platform of the build being replaced,
// and the program file itself.
type Target struct {
	GOOS, GOARCH string
	ExePath      string
}

// Native is the target of the running build.
func Native(exePath string) Target {
	return Target{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, ExePath: exePath}
}

// Install is whether this copy of the program is in a position to replace
// itself, and what stands in the way when it is not.
type Install struct {
	Possible bool
	// Reason says why not, in words a reader can act on.
	Reason string
}

// SelfInstall decides whether this copy may replace itself.
//
// The rule is not "which platform is this": it is who owns the file. A program
// that can write the directory it runs from, and that no package manager put
// there, is one that can put a new version of itself in place. The service unit
// on Linux is the clearest case of the opposite: it runs as its own user under
// ProtectSystem=strict, so the file it was started from cannot be written at
// all.
func SelfInstall(t Target) Install {
	if _, ok := ArchiveName("0", t.GOOS, t.GOARCH); !ok {
		return Install{Reason: "the program is installed as a package on this platform, so the package manager is what updates it"}
	}
	dir := filepath.Dir(t.ExePath)
	if name, ok := packageManagerMarker(dir); ok {
		return Install{Reason: "this copy was installed by " + name + ", which is what updates it"}
	}
	// Permission bits do not say whether a directory can be written: a read-only
	// mount, an ACL and a read-only filesystem all look writable to a stat. The
	// only answer that holds is to make a file there and remove it again.
	probe, err := os.CreateTemp(dir, ".sdautologin-write-probe")
	if err != nil {
		return Install{Reason: fmt.Sprintf("%s cannot be written to, so the program cannot replace itself there", dir)}
	}
	name := probe.Name()
	probe.Close()
	os.Remove(name)
	return Install{Possible: true}
}

// packageManagerMarker reports a package manager that owns the directory the
// program runs from. It is checked because the directory being writable is not
// enough: Scoop keeps its applications in a directory the user owns, and a
// program that replaced its own file there would leave Scoop's record of what
// it installed describing something else.
func packageManagerMarker(dir string) (string, bool) {
	// Scoop writes install.json beside the version it unpacked.
	if _, err := os.Stat(filepath.Join(dir, "install.json")); err == nil {
		return "Scoop", true
	}
	lower := strings.ToLower(filepath.ToSlash(dir))
	for _, marker := range []struct{ path, name string }{
		{"/scoop/apps/", "Scoop"},
		{"/windowsapps/", "winget"},
	} {
		if strings.Contains(lower, marker.path) {
			return marker.name, true
		}
	}
	return "", false
}

// Fetch downloads an asset and returns it once its digest matches. The digest
// comes from the signed checksum list, so a file that does not match is not
// something the release published.
func Fetch(ctx context.Context, url, wantSum string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, archiveTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered http %d", filepath.Base(url), resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxArchiveBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxArchiveBytes {
		return nil, fmt.Errorf("%s is larger than this program will read", filepath.Base(url))
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != wantSum {
		return nil, fmt.Errorf("%s does not match the digest the release published", filepath.Base(url))
	}
	return data, nil
}

// UnpackProgram reads the program out of the archive of a Windows build.
func UnpackProgram(archive []byte) ([]byte, error) {
	return unpackProgram(archive, maxArchiveBytes)
}

// unpackProgram is that read with the bound named, so the two sides of it can be
// tried without writing a hundred megabytes into an archive. The bound the
// program itself reads under is maxArchiveBytes, which is a constant.
func unpackProgram(archive []byte, limit int64) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("the archive could not be read: %w", err)
	}
	want := progName + ".exe"
	for _, f := range zr.File {
		// The archive holds one directory of files; the program is the one
		// named for it, wherever it sits inside.
		if !strings.EqualFold(filepath.Base(f.Name), want) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		// One byte past the bound, so a program that is exactly at it is read
		// whole and one larger than it is refused rather than cut short into a
		// file that would be put in place as a program that does not start.
		program, err := io.ReadAll(io.LimitReader(rc, limit+1))
		if err != nil {
			return nil, err
		}
		if int64(len(program)) > limit {
			return nil, fmt.Errorf("the %s in the archive is larger than this program will read", want)
		}
		return program, nil
	}
	return nil, fmt.Errorf("the archive holds no %s", want)
}

// Replace puts a new program in place of the one at exePath.
//
// On Windows a running program cannot be overwritten or deleted, but it can be
// renamed, so the copy that is running moves aside and the new one takes its
// name. Nothing is removed here: the copy that moved aside is deleted on the
// next start, by which time it is not the file being executed.
//
// Every step is checked, and a step that fails puts back what the step before
// it moved. A half-replaced program would be one that does not start.
func Replace(exePath string, newProgram []byte) error {
	dir := filepath.Dir(exePath)
	// The new file goes in the same directory as the old one, so the rename that
	// follows is within one filesystem and cannot fail for being across two.
	staged, err := os.CreateTemp(dir, filepath.Base(exePath)+".new")
	if err != nil {
		return err
	}
	stagedPath := staged.Name()
	// The mode of the copy being replaced, so the new one is executable in the
	// same way. os.CreateTemp writes 0600, which on a system where the mode means
	// something is a program nobody can run, so a copy whose mode cannot be read
	// back still gets the mode a program is expected to have.
	mode := os.FileMode(0o755)
	if info, err := os.Stat(exePath); err == nil {
		mode = info.Mode().Perm()
	}
	written, err := staged.Write(newProgram)
	if err != nil {
		staged.Close()
		os.Remove(stagedPath)
		return err
	}
	if written != len(newProgram) {
		staged.Close()
		os.Remove(stagedPath)
		return fmt.Errorf("only %d of %d bytes of the new program could be written", written, len(newProgram))
	}
	// The bytes have to be on the disk before the rename below replaces the
	// program: a rename that lands first leaves a program that does not start.
	if err := staged.Sync(); err != nil {
		staged.Close()
		os.Remove(stagedPath)
		return err
	}
	if err := staged.Close(); err != nil {
		os.Remove(stagedPath)
		return err
	}
	if err := os.Chmod(stagedPath, mode); err != nil {
		// The mode is what makes a program runnable on the systems where it
		// means something. Where it does not, a filesystem that will not take it
		// is not a reason to leave the reader without the release.
		if runtime.GOOS != "windows" {
			os.Remove(stagedPath)
			return fmt.Errorf("the new program could not be given the mode of the one it replaces: %w", err)
		}
	}

	leftover := exePath + leftoverSuffix
	os.Remove(leftover)
	if err := os.Rename(exePath, leftover); err != nil {
		os.Remove(stagedPath)
		return fmt.Errorf("the program in use could not be moved aside: %w", err)
	}
	if err := os.Rename(stagedPath, exePath); err != nil {
		// Put back what was moved, or there is no program left at all.
		if back := os.Rename(leftover, exePath); back != nil {
			return fmt.Errorf("the new program could not be put in place (%v), and the one that was there could not be put back (%v): it is at %s", err, back, leftover)
		}
		os.Remove(stagedPath)
		return fmt.Errorf("the new program could not be put in place: %w", err)
	}
	return nil
}

// RemoveLeftover deletes the copy a replacement moved aside, which is the
// program that was running when it was replaced.
//
// It belongs at the start of the program, before anything else runs: the file it
// names is not the one being executed then, and that is what makes it deletable
// on Windows, where a running program cannot be removed. Nothing in this package
// calls it, and the call site is in the program rather than here for the same
// reason the file is: at the moment it is written the program is still running.
// Without that call a copy keeps the previous release beside it until the next
// update, which removes it as well.
func RemoveLeftover(exePath string) {
	if exePath == "" {
		return
	}
	os.Remove(exePath + leftoverSuffix)
}
