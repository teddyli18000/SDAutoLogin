package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// SignatureSuffix is what a detached signature file is called. The releases
// carry the signature of SHA256SUMS under this name.
const SignatureSuffix = ".sig"

// SigningKeyEnv names the environment variable the release build reads the
// signing key from, so the key itself is never a file in the repository or an
// argument in a process list.
const SigningKeyEnv = "SDAUTOLOGIN_SIGNING_KEY"

// KeyID is the short name of a key, so a signature can say which key made it
// and a rotation can be told apart from a forgery.
func KeyID(pub ed25519.PublicKey) string {
	if len(pub) < 4 {
		return ""
	}
	return hex.EncodeToString(pub[:4])
}

// Sign returns the text of a signature file for data: the signature in base64,
// then the name of the key that made it.
//
// The signature covers the bytes of SHA256SUMS, and every archive is checked
// against a line of that file. Signing the list rather than each archive means
// one signature per release instead of sixteen, and nothing is installed that
// is not named and summed in the signed list.
func Sign(priv ed25519.PrivateKey, data []byte) string {
	sig := ed25519.Sign(priv, data)
	pub := priv.Public().(ed25519.PublicKey)
	return base64.StdEncoding.EncodeToString(sig) + " " + KeyID(pub) + "\n"
}

// ErrUnsigned is what a check answers when no key has been made yet. Nothing is
// downloaded in that state: an update that cannot be checked is one that would
// run whatever the server handed over.
var ErrUnsigned = errors.New("this build carries no release signing key")

// PublicKeyBytes decodes the key the releases are signed with.
func PublicKeyBytes() (ed25519.PublicKey, error) {
	return publicKeyFrom(PublicKey)
}

// publicKeyFrom is that decoding with the text named, so the state a build is in
// before a key has been made can be checked on any build.
func publicKeyFrom(text string) (ed25519.PublicKey, error) {
	if strings.TrimSpace(text) == "" {
		return nil, ErrUnsigned
	}
	return decodeKey(text)
}

// decodeKey reads a public key in the base64 the release build writes.
func decodeKey(text string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return nil, fmt.Errorf("the signing key in this build cannot be read: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("the signing key in this build is %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// VerifySignature checks the text of a signature file against data. The name of
// the key is checked as well as the signature, so a release signed with a key
// this build does not carry is refused with that said plainly rather than as a
// bad signature.
func VerifySignature(pub ed25519.PublicKey, data []byte, text string) error {
	fields := strings.Fields(text)
	if len(fields) != 2 {
		return errors.New("the signature is not in the form this program writes")
	}
	if fields[1] != KeyID(pub) {
		return fmt.Errorf("the release is signed with key %s, and this build carries %s", fields[1], KeyID(pub))
	}
	sig, err := base64.StdEncoding.DecodeString(fields[0])
	if err != nil {
		return fmt.Errorf("the signature could not be read: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("the signature is %d bytes, want %d", len(sig), ed25519.SignatureSize)
	}
	if !ed25519.Verify(pub, data, sig) {
		return errors.New("the signature does not match the file it came with")
	}
	return nil
}

// Checksums is a parsed SHA256SUMS: the digest of each file, by name.
type Checksums map[string]string

// ParseChecksums reads the output of the sha256sum tool: a digest, two spaces,
// and a name. The binary marker sha256sum writes for a file it read in binary
// mode is accepted and dropped.
func ParseChecksums(data []byte) (Checksums, error) {
	out := Checksums{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		// A digest, one space, then the name: two spaces for a file read as
		// text, or an asterisk for one read in binary mode.
		if len(line) < 66 || line[64] != ' ' || (line[65] != ' ' && line[65] != '*') {
			return nil, fmt.Errorf("the checksum line %q is not in the form the tool writes", line)
		}
		sum := line[:64]
		if _, err := hex.DecodeString(sum); err != nil {
			return nil, fmt.Errorf("the checksum %q is not hexadecimal", sum)
		}
		name := strings.TrimPrefix(line[66:], "*")
		out[name] = strings.ToLower(sum)
	}
	if len(out) == 0 {
		return nil, errors.New("no checksums were listed")
	}
	return out, nil
}

// Sum is the digest listed for a file, and whether it was listed at all. A file
// that is not in the signed list is not installed.
func (c Checksums) Sum(name string) (string, bool) {
	s, ok := c[name]
	return s, ok
}
