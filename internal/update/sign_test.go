package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
)

func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestASignatureVerifiesAgainstTheFileItCameWith(t *testing.T) {
	pub, priv := testKey(t)
	sums := []byte("18361a1e42d1c5cac53b2123a3f3901a576a38f46b332848c0e077d975728435  SDAutoLogin-1.2.0-windows-amd64.zip\n")
	text := Sign(priv, sums)
	if err := VerifySignature(pub, sums, text); err != nil {
		t.Fatalf("VerifySignature: %v", err)
	}
}

// Everything the signature is supposed to stop: a file that was changed, a
// signature that was changed, and a signature made with another key.
func TestASignatureRefusesWhatItIsThereFor(t *testing.T) {
	pub, priv := testKey(t)
	sums := []byte("aaaa  file.zip\n")
	text := Sign(priv, sums)

	if err := VerifySignature(pub, []byte("bbbb  file.zip\n"), text); err == nil {
		t.Error("a changed file was accepted")
	}

	other, _ := testKey(t)
	if err := VerifySignature(other, sums, text); err == nil {
		t.Error("a signature from another key was accepted")
	} else if !strings.Contains(err.Error(), KeyID(other)) || !strings.Contains(err.Error(), KeyID(pub)) {
		t.Errorf("the refusal does not name both keys: %v", err)
	}

	// A signature with one byte of it changed is not a signature.
	raw := strings.Fields(text)[0]
	flipped := []byte(raw)
	if flipped[0] == 'A' {
		flipped[0] = 'B'
	} else {
		flipped[0] = 'A'
	}
	bad := string(flipped) + " " + strings.Fields(text)[1]
	if err := VerifySignature(pub, sums, bad); err == nil {
		t.Error("a changed signature was accepted")
	}
	// An empty or truncated file is refused rather than read as no signature.
	for _, s := range []string{"", "\n", "onlyonefield", "AAAA", "!!!! " + KeyID(pub)} {
		if err := VerifySignature(pub, sums, s); err == nil {
			t.Errorf("the signature text %q was accepted", s)
		}
	}
}

// A build with no key made yet cannot check anything, and says so rather than
// treating every release as unsigned-and-therefore-fine.
func TestABuildWithNoKeyRefusesToCheck(t *testing.T) {
	// Driven through the text and not the constant, so this holds whichever state
	// the build is in and does not stop covering the case once a key is made.
	for _, empty := range []string{"", "   ", "\n"} {
		if _, err := publicKeyFrom(empty); err != ErrUnsigned {
			t.Errorf("publicKeyFrom(%q) error = %v, want ErrUnsigned", empty, err)
		}
	}
}

func TestAKeyThatIsNotOneIsRefused(t *testing.T) {
	// The constant is read by the program, so an unreadable one is reported
	// rather than trusted. This drives the decoder directly, since the constant
	// is a constant.
	if _, err := decodeKey("not base64!!"); err == nil {
		t.Error("a key that is not base64 was accepted")
	}
	if _, err := decodeKey("AAAA"); err == nil {
		t.Error("a key of the wrong length was accepted")
	}
}

// The checksum list is what the archives are checked against, so a line that
// cannot be read is a failure rather than a line that is skipped: skipping it
// would leave that archive unchecked.
func TestTheChecksumListIsReadOrRefused(t *testing.T) {
	good := "18361a1e42d1c5cac53b2123a3f3901a576a38f46b332848c0e077d975728435  a.zip\n" +
		"11e799927b37c98f239bbd55689b924ba79b7fb81c8657d2fbf88d43b0f863c8 *b.zip\n"
	c, err := ParseChecksums([]byte(good))
	if err != nil {
		t.Fatalf("ParseChecksums: %v", err)
	}
	if len(c) != 2 {
		t.Fatalf("read %d checksums, want 2", len(c))
	}
	// The asterisk sha256sum writes for a file read in binary mode is not part
	// of the name.
	if _, ok := c.Sum("b.zip"); !ok {
		t.Error("the name of a binary-mode line kept its marker")
	}
	if s, ok := c.Sum("a.zip"); !ok || s != "18361a1e42d1c5cac53b2123a3f3901a576a38f46b332848c0e077d975728435" {
		t.Errorf("Sum(a.zip) = %q (ok=%v)", s, ok)
	}
	for _, bad := range []string{
		"",
		"just-a-name.zip\n",
		"18361a1e42d1c5cac53b2123a3f3901a576a38f46b332848c0e077d975728435 a.zip\n", // one space
		"zzzz1a1e42d1c5cac53b2123a3f3901a576a38f46b332848c0e077d975728435  a.zip\n",
	} {
		if _, err := ParseChecksums([]byte(bad)); err == nil {
			t.Errorf("the list %q was accepted", bad)
		}
	}
}
