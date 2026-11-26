package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
)

// releasePublicKey verifies SHA256SUMS.sig on every release. The matching
// private key exists only as the UPDATE_SIGNING_KEY repository secret and is
// used by the release workflow (tools/signsums). Forks can override it at
// build time with
// -ldflags "-X github.com/nikolap994/magnetar/internal/update.releasePublicKey=...".
var releasePublicKey = "s0KJuZ1zhefxw2ElTOYsCntOUtbp5vyoUd8fel1zI/Q="

func publicKey() ed25519.PublicKey {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(releasePublicKey))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil
	}
	return ed25519.PublicKey(b)
}

// Configured reports whether this build can verify and install updates.
func Configured() bool { return publicKey() != nil }
