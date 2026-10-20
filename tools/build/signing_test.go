// The tests of the signing half of the task runner: where the private key is
// written, and that nothing reads one out of the working tree. They make no
// release, so they need no key of the project's own.

package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestSigningDoesNotReadAKeyFromTheTree covers the fallback that used to be
// here: a key file beside the sources was read when the environment held none,
// which made reading the checkout enough to sign a release every installed copy
// accepts. The file is there in this test and must be ignored.
func TestSigningDoesNotReadAKeyFromTheTree(t *testing.T) {
	root := t.TempDir()
	// A file keygen of an earlier version would have left here, with a key that
	// looks entirely usable.
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(priv) + "\n"
	if err := os.WriteFile(filepath.Join(root, "signing.key"), []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SDAUTOLOGIN_SIGNING_KEY", "")

	err = taskSign(root)
	if err == nil {
		t.Fatal("a signing key in the working tree was used")
	}
	if !strings.Contains(err.Error(), "SDAUTOLOGIN_SIGNING_KEY") {
		t.Errorf("the refusal does not say where the key has to come from: %v", err)
	}
}

// TestSigningRefusesAKeyThatIsNotOne covers the environment variable holding
// something other than a key: better said here than as a signature nothing can
// read later.
func TestSigningRefusesAKeyThatIsNotOne(t *testing.T) {
	root := t.TempDir()
	for _, c := range []struct{ name, key string }{
		{"not base64", "not a key at all!"},
		{"too short", base64.StdEncoding.EncodeToString([]byte("short"))},
		{"a public key", base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize))},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("SDAUTOLOGIN_SIGNING_KEY", c.key)
			if err := taskSign(root); err == nil {
				t.Errorf("%s was accepted as a signing key", c.name)
			}
		})
	}
}

// TestKeygenWritesTheKeyOutsideTheTree covers where the private half goes: not
// the top of the checkout, but the file the environment names, or the per-user
// configuration directory. The tree it writes the public half into is a stand-in
// made here, so the test never touches the key the program carries.
func TestKeygenWritesTheKeyOutsideTheTree(t *testing.T) {
	// The checkout this is about is the one the tool was started in. A cross
	// compiled binary run from a Windows directory has none above it, and then
	// there is no checkout for the private half to land in; the path itself is
	// checked either way, and the stand-in tree below covers where it does land.
	if got, err := keygenPath(); err != nil {
		t.Errorf("no key path could be worked out: %v", err)
	} else if repo, err := repoRoot(); err == nil {
		if strings.HasPrefix(got, repo+string(filepath.Separator)) {
			t.Errorf("keygen writes the private half into the checkout: %s", got)
		}
	} else if runtime.GOOS != "windows" {
		t.Fatalf("finding the repository: %v", err)
	}

	root := t.TempDir()
	// The stand-in for internal/update/publickey.go, with the one line keygen
	// rewrites.
	source := filepath.Join(root, publicKeySource)
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	const decl = "const PublicKey = "
	if err := os.WriteFile(source, []byte("package update\n\n"+decl+"\"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "nested", "signing.key")
	t.Setenv(signingKeyFileEnv, keyPath)

	if err := taskKeygen(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "signing.key")); !os.IsNotExist(err) {
		t.Errorf("a key was written into the tree: %v", err)
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("the key was not written where the environment said: %v", err)
	}
	// The mode is what keeps the key to its owner where a mode means something,
	// and the file is written 0600 for that. Windows has no such bit and reports
	// 0666 for a file only its owner can read: what keeps the key there is the
	// access list of the per-user configuration directory the path is under
	// (%AppData%, which only this account and the administrators of the machine
	// can write through), so the mode is the part of this the platform decides.
	if runtime.GOOS != "windows" {
		if mode, err := os.Stat(keyPath); err != nil {
			t.Fatal(err)
		} else if perm := mode.Mode().Perm(); perm != 0o600 {
			t.Errorf("the key is %o, want 600", perm)
		}
	}

	// The public half that went into the program has to be the one this private
	// half belongs to, or the release would be signed with a key nothing carries.
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(key)))
	if err != nil {
		t.Fatal(err)
	}
	priv := ed25519.PrivateKey(raw)
	want := base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))
	written, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	line := ""
	for _, l := range strings.Split(string(written), "\n") {
		if strings.HasPrefix(l, decl) {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("%s did not get the public half written into it", publicKeySource)
	}
	if got, err := strconv.Unquote(strings.TrimSpace(strings.TrimPrefix(line, decl))); err != nil {
		t.Fatalf("%s holds %q: %v", publicKeySource, line, err)
	} else if got != want {
		t.Errorf("%s carries the key %q, want %q", publicKeySource, got, want)
	}
}

// TestKeygenRefusesToOverwriteAKey covers the deliberate act a rotation is: with
// a key already there, nothing is made or overwritten.
func TestKeygenRefusesToOverwriteAKey(t *testing.T) {
	root := t.TempDir()
	keyPath := filepath.Join(t.TempDir(), "signing.key")
	if err := os.WriteFile(keyPath, []byte("an existing key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(signingKeyFileEnv, keyPath)

	err := taskKeygen(root)
	if err == nil {
		t.Fatal("keygen overwrote a key that was already there")
	}
	if !strings.Contains(err.Error(), "already there") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	if data, readErr := os.ReadFile(keyPath); readErr != nil {
		t.Fatal(readErr)
	} else if string(data) != "an existing key\n" {
		t.Error("the key that was there was changed")
	}
}
