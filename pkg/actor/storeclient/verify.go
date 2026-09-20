package storeclient

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// StoreSignatureDomain is the Ed25519 message domain separator shared with
// sporecloud: the signed message is the domain + lowercase hex sha256 of the
// encrypted artifact file.
const StoreSignatureDomain = "sporecloud.store-package.v1:"

// nonceSize matches the cloud's artifact layout nonce[12] || ciphertext.
const gcmNonceSize = 12

// verifyStoreSignature checks the official Ed25519 signature over the
// encrypted artifact bytes. sigHex is the X-Signature header value.
func verifyStoreSignature(pub ed25519.PublicKey, sigHex string, artifact []byte) error {
	sig, err := hex.DecodeString(trimSpace(sigHex))
	if err != nil {
		return fmt.Errorf("decode X-Signature: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("X-Signature length %d, want %d", len(sig), ed25519.SignatureSize)
	}
	sum := sha256.Sum256(artifact)
	msg := StoreSignatureDomain + hex.EncodeToString(sum[:])
	if !ed25519.Verify(pub, []byte(msg), sig) {
		return fmt.Errorf("store signature verification failed")
	}
	return nil
}

// decryptStoreArtifact opens the AES-256-GCM envelope
// nonce[12] || AES-256-GCM(key, zip) and returns the plaintext zip.
func decryptStoreArtifact(keyB64 string, artifact []byte) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(trimSpace(keyB64))
	if err != nil {
		return nil, fmt.Errorf("decode X-Content-Key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("content key length %d bytes, want 32 (AES-256)", len(key))
	}
	if len(artifact) <= gcmNonceSize {
		return nil, fmt.Errorf("artifact too short (%d bytes) for nonce||ciphertext", len(artifact))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	plaintext, err := gcm.Open(nil, artifact[:gcmNonceSize], artifact[gcmNonceSize:], nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt payload (wrong key or tampered ciphertext): %w", err)
	}
	return plaintext, nil
}

// sha256Hex returns the lowercase hex sha256 of b.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// matchesSha256 reports whether b matches the expected hex digest
// (case-insensitive; empty expected never matches).
func matchesSha256(expectedHex string, b []byte) bool {
	e := trimSpace(expectedHex)
	if e == "" {
		return false
	}
	got := sha256Hex(b)
	return len(got) == len(e) && bytes.EqualFold([]byte(got), []byte(e))
}

func trimSpace(s string) string { return strings.TrimSpace(s) }
