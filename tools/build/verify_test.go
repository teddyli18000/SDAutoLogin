// The tests of the checks on what the packaging wrote that need no external
// tool: the pair a release carries, the checksum list and the signature over it.
// Every other check in verify.go runs a tool of the format it reads, which a
// machine running the tests does not have to have.

package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sdautologin/internal/update"
)

// signedList writes a checksum list and a signature over it with a key of the
// test's own making, and returns the directory and the key.
func signedList(t *testing.T) (dir string, pub ed25519.PublicKey) {
	t.Helper()

	dir = t.TempDir()
	line := sha256Hex([]byte("package\n")) + "  sdautologin_1.2.3-1_amd64.deb\n"
	sums := filepath.Join(dir, checksumsFile)
	if err := os.WriteFile(sums, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sums+update.SignatureSuffix, []byte(update.Sign(priv, []byte(line))), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, pub
}

// TestASignedListChecksOut is the state sign leaves behind: the list and the
// signature over it, made with the key the build carries.
func TestASignedListChecksOut(t *testing.T) {
	dir, pub := signedList(t)
	if err := verifyChecksumsSignature(dir, pub); err != nil {
		t.Errorf("a list signed with the key of this build was refused: %v", err)
	}
}

// TestAListWrittenAgainAfterSigningIsRefused covers the order the release runs
// in. A list that changed after it was signed is one nothing accepts, so a run
// that finds that pair says so here rather than publishing a release nobody can
// update from.
func TestAListWrittenAgainAfterSigningIsRefused(t *testing.T) {
	dir, pub := signedList(t)
	sums := filepath.Join(dir, checksumsFile)
	// A list that gained a line after it was signed: the shape a package packed
	// after the signing step leaves behind.
	changed := string(mustRead(t, sums)) + sha256Hex([]byte("late\n")) + "  late.deb\n"
	if err := os.WriteFile(sums, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	err := verifyChecksumsSignature(dir, pub)
	if err == nil {
		t.Fatal("a list written again after it was signed was accepted")
	}
	if !strings.Contains(err.Error(), "written again after it was signed") {
		t.Errorf("the refusal does not name what is wrong: %v", err)
	}
}

// TestASignatureFromAnotherKeyIsRefused covers a release signed by a key this
// build does not carry, which is the shape a rotation leaves behind.
func TestASignatureFromAnotherKeyIsRefused(t *testing.T) {
	dir, _ := signedList(t)
	_, other, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksumsSignature(dir, other.Public().(ed25519.PublicKey)); err == nil {
		t.Error("a signature made with another key was accepted")
	}
}

// TestASignatureWithNoKeyToCheckIt covers a build that carries no key of its
// own reading a signed release: the signature cannot be judged, and judging it
// by skipping the check is how whatever the server handed over gets installed.
func TestASignatureWithNoKeyToCheckIt(t *testing.T) {
	dir, _ := signedList(t)
	err := verifyChecksumsSignature(dir, nil)
	if err == nil {
		t.Fatal("a signed list was accepted by a build carrying no key")
	}
	if !strings.Contains(err.Error(), "carries no key") {
		t.Errorf("the refusal does not name what is wrong: %v", err)
	}
}

// TestAnUnsignedListIsRefused covers the quality gate itself: a set of packages
// nothing has signed is not a release this project puts out, so a run that finds
// one fails. The signature is a step of the release before this check for the
// same reason, and the two are one gate: sign, then check what was written.
func TestAnUnsignedListIsRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, checksumsFile), []byte("whatever\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		pub  ed25519.PublicKey
	}{
		{"a build carrying no key", nil},
		{"a build carrying a key", ed25519.PublicKey(make([]byte, ed25519.PublicKeySize))},
	} {
		err := verifyChecksumsSignature(dir, c.pub)
		if err == nil {
			t.Fatalf("%s: a list with no signature was accepted", c.name)
		}
		if !strings.Contains(err.Error(), "not signed") || !strings.Contains(err.Error(), "run sign") {
			t.Errorf("%s: the refusal does not say what is missing: %v", c.name, err)
		}
	}
}

// TestASignatureWithoutItsList covers a half-copied release: the signature is
// there and the file it signs is not, so there is nothing it could be checked
// against.
func TestASignatureWithoutItsList(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, checksumsFile+update.SignatureSuffix), []byte("signature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksumsSignature(dir, ed25519.PublicKey(make([]byte, ed25519.PublicKeySize))); err == nil {
		t.Error("a signature with no list beside it was accepted")
	}
}

// TestVerifyRefusesATreeNothingWasSignedIn covers the gate at the task level: a
// run over a directory that holds no signature fails, rather than walking the
// packages, finding none it can read back and saying so about the machine.
func TestVerifyRefusesATreeNothingWasSignedIn(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, distDir, pkgDir), 0o755); err != nil {
		t.Fatal(err)
	}
	err := taskVerify(root)
	if err == nil {
		t.Fatal("verify accepted a tree with no signed package set")
	}
	if !strings.Contains(err.Error(), "not signed") {
		t.Errorf("error = %q, and it does not say the packages are unsigned", err)
	}
}

// TestTheKeyTheBuildCarries covers the key read once at the top of verify: this
// build carries one, and the reading of it is not an error to be passed over.
func TestTheKeyTheBuildCarries(t *testing.T) {
	pub, err := buildKey()
	if err != nil {
		t.Fatalf("the build carries no key that can be read: %v", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		t.Errorf("the key is %d bytes, want %d", len(pub), ed25519.PublicKeySize)
	}
}

// TestASignatureThatCannotBeRead covers the read that is not a missing file: a
// directory where the signature belongs is a broken release, and the error is
// carried out rather than turned into a pass.
func TestASignatureThatCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, checksumsFile), []byte("whatever\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, checksumsFile+update.SignatureSuffix), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksumsSignature(dir, nil); err == nil {
		t.Fatal("a signature that could not be read was accepted")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
