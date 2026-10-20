package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"sdautologin/internal/update"
)

// signingKeyFileEnv names the variable that says where keygen writes the
// private half, for a machine that keeps its keys somewhere of its own.
const signingKeyFileEnv = "SDAUTOLOGIN_SIGNING_KEY_FILE"

// keygenPath is where keygen writes the private half: the file named by
// signingKeyFileEnv, or signing.key in this user's configuration directory.
//
// It is outside the checkout on purpose. A key beside the sources is a key that
// is in every copy of the tree, in every editor swap file and in whatever a
// backup sweeps up, and anyone who can read the checkout could sign a release
// that every installed copy accepts. Nothing reads the key from a file: the
// release build is given its text in the environment, and the file exists to
// carry it to the secret store.
func keygenPath() (string, error) {
	if p := strings.TrimSpace(os.Getenv(signingKeyFileEnv)); p != "" {
		return p, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("no directory to keep the key in, name one with %s: %w", signingKeyFileEnv, err)
	}
	return filepath.Join(base, "sdautologin", "signing.key"), nil
}

// publicKeySource is the file the public half is written into, which is the
// only key the program will accept a release from.
var publicKeySource = filepath.Join("internal", "update", "publickey.go")

// taskKeygen makes the key the releases are signed with. The private half goes
// into a file outside the checkout, to be handed to the release build as a
// secret; the public half is written into the program.
//
// It refuses to run when a key is already there. Rotating is done by deleting
// the file and running this again, which is a deliberate act, and the refusal
// says so along with what the rotation costs: a build carries one key, so every
// copy installed before the rotation has to be updated by hand, since it cannot
// check a release the new key signed.
func taskKeygen(root string) error {
	keyPath, err := keygenPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(keyPath); err == nil {
		return fmt.Errorf("%s is already there.\n"+
			"To rotate the key, delete it and run this again, then put the new public half in %s\n"+
			"and the new private half in the repository secret %s.\n"+
			"A build carries one key, so a copy installed before the rotation cannot check a release the\n"+
			"new key signed: those copies are updated by hand once, and update themselves again after that.",
			keyPath, publicKeySource, update.SigningKeyEnv)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	// The directory is made here, so a key on a machine that has never run this
	// has somewhere to go; 0700 and 0600, since it is a signing key and on a
	// machine where the mode means nothing the file still should not be world
	// readable by default.
	if dir := filepath.Dir(keyPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("%s could not be made: %w", dir, err)
		}
	}
	if err := os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); err != nil {
		return err
	}
	if err := writePublicKey(root, pub); err != nil {
		// The private half is useless without the public half in the program, so
		// leaving a stray key file behind would invite signing with it.
		os.Remove(keyPath)
		return err
	}
	// The mode is only half of it, and on Windows it is no part of it: the file
	// is created 0600 either way, but a platform with no such bit reports 0666
	// for a file only its owner can read. What keeps the key there is the access
	// list of the per-user configuration directory it sits under, so saying
	// "0600" alone would name a protection this platform does not have.
	mode := "0600"
	if runtime.GOOS == "windows" {
		mode = "0600, which this platform does not enforce: the per-user configuration directory it sits under keeps it to this account"
	}
	fmt.Printf("  wrote %s (%s) and %s\n", keyPath, mode, publicKeySource)
	fmt.Printf("  keep that file outside the checkout: give its contents to the release build as the secret %s\n",
		update.SigningKeyEnv)
	fmt.Printf("  key id %s\n", update.KeyID(pub))
	return nil
}

// writePublicKey puts the key into the program, replacing the line that holds
// it so the rest of the file, and its comment, stay as they are.
func writePublicKey(root string, pub ed25519.PublicKey) error {
	path := filepath.Join(root, publicKeySource)
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	const decl = "const PublicKey = "
	lines := strings.Split(string(src), "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(line, decl) {
			lines[i] = fmt.Sprintf("%s%q", decl, base64.StdEncoding.EncodeToString(pub))
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("%s has no %q line to write the key into", publicKeySource, strings.TrimSpace(decl))
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

// taskSign signs the checksum list of the packages that were just built, so a
// reader can tell a release this project published from anything else that
// arrives over the network.
//
// It is the last step of a release: pack, verify, sign, publish. Signing writes
// the list again itself, so the signature always covers the packages that are
// really in the directory; and a list written again afterwards does not keep the
// signature, see writeChecksums. Nothing is packed between the two.
//
// The key comes from the environment, which is where the release build keeps its
// secrets: a key named on a command line is a key in a process list, and a key in
// a file is a key in every copy of the tree. The file keygen writes carries the
// same text to the secret store and is not read here.
func taskSign(root string) error {
	text := os.Getenv(update.SigningKeyEnv)
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("no signing key: set %s to the contents of the key file, or run keygen to make one", update.SigningKeyEnv)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil {
		return fmt.Errorf("the signing key is not base64: %w", err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return fmt.Errorf("the signing key is %d bytes, want %d", len(raw), ed25519.PrivateKeySize)
	}
	priv := ed25519.PrivateKey(raw)

	// The key that signs has to be the key the program carries, or the release
	// goes out signed with something nothing will accept, and nobody can tell
	// until a reader tries to update.
	carried, err := update.PublicKeyBytes()
	if err != nil {
		return fmt.Errorf("%s holds no key to check against: %w", publicKeySource, err)
	}
	if !carried.Equal(priv.Public()) {
		return fmt.Errorf("the key that would sign is %s, and %s carries %s",
			update.KeyID(priv.Public().(ed25519.PublicKey)), publicKeySource, update.KeyID(carried))
	}

	dir := filepath.Join(root, distDir, pkgDir)
	// The list is written here rather than trusted as it stands: it is what the
	// packages in the directory sum to right now, which is what is about to be
	// signed. A list a hand put together, or one left over from an earlier pack,
	// would otherwise be signed as whatever it happens to say.
	if err := writeChecksums(root); err != nil {
		return err
	}
	sumsPath := filepath.Join(dir, checksumsFile)
	data, err := os.ReadFile(sumsPath)
	if err != nil {
		return fmt.Errorf("%s: %w", describe(root, sumsPath), err)
	}
	// A list that cannot be read back is a list that would be signed as
	// whatever bytes happen to be there.
	if _, err := update.ParseChecksums(data); err != nil {
		return fmt.Errorf("%s: %w", describe(root, sumsPath), err)
	}

	text2 := update.Sign(priv, data)
	if err := update.VerifySignature(carried, data, text2); err != nil {
		return fmt.Errorf("the signature just made does not verify: %w", err)
	}
	outPath := sumsPath + update.SignatureSuffix
	if err := os.WriteFile(outPath, []byte(text2), 0o644); err != nil {
		return err
	}
	// What matters is the pair on the disk, not the bytes this step still holds:
	// they are read back and checked, so a signature that was written short is a
	// failure here rather than at the reader.
	if err := verifyChecksumsSignature(dir, carried); err != nil {
		return fmt.Errorf("the signature written to %s does not check out: %w", filepath.Base(outPath), err)
	}
	fmt.Printf("  -> %s (%d bytes, key %s)\n", describe(root, outPath), len(data), update.KeyID(carried))
	return nil
}
