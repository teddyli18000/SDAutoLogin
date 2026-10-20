package update

// PublicKey is the ed25519 key the releases are signed with, in base64, written
// by "go run ./tools/build keygen".
//
// It is empty until a key is made, and nothing is downloaded while it is. An
// update that cannot be checked against a key this build carries is one that
// would run whatever the server handed over, which is the whole reason the
// signature is here.
//
// Changing this line is how the key is rotated: a release signed with a key
// that is not the one written here is refused, and the refusal names both keys.
const PublicKey = "Qyh5LHHJwL83IlHmu2fZBmdeMOdzqZoJnnwZng0GcM0="
