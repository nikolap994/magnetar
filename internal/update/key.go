package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
)

// releasePublicKey verifies SHA256SUMS.sig on every release. It is set at
// build time from the UPDATE_PUBLIC_KEY repository variable
// (-ldflags "-X github.com/nikolap994/magnetar/internal/update.releasePublicKey=...").
// The matching private key exists only as the UPDATE_SIGNING_KEY repository
// secret and is used by the release workflow (tools/signsums). Builds without
// a key never install updates; they only report that one is available.
var releasePublicKey = ""

func publicKey() ed25519.PublicKey {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(releasePublicKey))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil
	}
	return ed25519.PublicKey(b)
}

// Configured reports whether this build can verify and install updates.
func Configured() bool { return publicKey() != nil }
