// Package storeclient implements the sporemind-side plugin-store client.
//
// It polls the sporecloud store index (GET /store/index), installs encrypted
// store packages (Ed25519 verify → AES-256-GCM decrypt → sha256 →
// appmanager.install_local), and installs community plugins from
// commit-pinned GitHub tarballs (mirror-first, codeload fallback, sha256
// verified, native build from source, appmanager.register).
//
// Trust roots: the sporecloud official Ed25519 signing key is pinned in code
// for the dev channel and configured out-of-band for production; third-party
// publisher keys arrive in the index and only verify the in-package
// PACKAGE.sig (handled inside install_local).
package storeclient

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"strings"
)

// devOfficialPubKeyHex is the sporecloud dev-channel store signing public key
// (derived from sha256("sporecloud-dev-store-signing-key")); see
// sporecloud cmd/sporecloud/README.md "Development signing key". Pinned here
// so dev builds verify store signatures without any configuration.
const devOfficialPubKeyHex = "39fcd38d92614f13715cc02360d2711f61a4283a7b1267c67e9fb8cd06cca455"

// officialPubKey resolves the store signing public key for the configured
// channel. The production key is delivered out-of-band and set via
// storeclient.config_set.
func officialPubKey(channel, productionPubKeyHex string) (ed25519.PublicKey, error) {
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "", "dev":
		return parsePubKeyHex(devOfficialPubKeyHex)
	case "production":
		if strings.TrimSpace(productionPubKeyHex) == "" {
			return nil, fmt.Errorf("storeclient: channel is production but no production public key is configured")
		}
		return parsePubKeyHex(productionPubKeyHex)
	default:
		return nil, fmt.Errorf("storeclient: unknown channel %q (want dev|production)", channel)
	}
}

func parsePubKeyHex(s string) (ed25519.PublicKey, error) {
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("storeclient: decode public key hex: %w", err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("storeclient: public key must be %d bytes, got %d", ed25519.PublicKeySize, len(b))
	}
	return ed25519.PublicKey(b), nil
}
