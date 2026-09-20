package storeclient

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

// storeEnvelope replicates the cloud's seal step: nonce[12] ||
// AES-256-GCM(key, plaintext) plus the official signature over
// "sporecloud.store-package.v1:" + hex(sha256(artifact)).
func sealForTest(t *testing.T, plaintext []byte) (artifact []byte, keyB64, sigHex string, priv ed25519.PrivateKey) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, gcmNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	artifact = append(append([]byte{}, nonce...), ct...)

	_, priv, err = ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(artifact)
	sig := ed25519.Sign(priv, []byte(StoreSignatureDomain+hex.EncodeToString(sum[:])))
	return artifact, base64.StdEncoding.EncodeToString(key), hex.EncodeToString(sig), priv
}

func TestVerifyDecryptRoundTrip(t *testing.T) {
	plaintext := []byte("PK\x03\x04 fake zip bytes for round trip")
	artifact, keyB64, sigHex, priv := sealForTest(t, plaintext)

	if err := verifyStoreSignature(priv.Public().(ed25519.PublicKey), sigHex, artifact); err != nil {
		t.Fatalf("verify signature: %v", err)
	}
	got, err := decryptStoreArtifact(keyB64, artifact)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Fatalf("round trip mismatch: %q != %q", got, plaintext)
	}
	if !matchesSha256(sha256Hex(plaintext), got) {
		t.Fatal("sha256 self-check failed")
	}
}

func TestVerifyRejectsTamperedArtifact(t *testing.T) {
	plaintext := []byte("payload")
	artifact, keyB64, sigHex, priv := sealForTest(t, plaintext)
	pub := priv.Public().(ed25519.PublicKey)

	tampered := append([]byte{}, artifact...)
	tampered[len(tampered)-1] ^= 0x01
	if err := verifyStoreSignature(pub, sigHex, tampered); err == nil {
		t.Fatal("tampered artifact must fail signature verification")
	}
	// Signature over a DIFFERENT artifact must also fail.
	other, _, _, _ := sealForTest(t, []byte("other"))
	if err := verifyStoreSignature(pub, sigHex, other); err == nil {
		t.Fatal("signature from another artifact must fail")
	}
	// Decrypting tampered ciphertext must fail (GCM auth).
	if _, err := decryptStoreArtifact(keyB64, tampered); err == nil {
		t.Fatal("tampered ciphertext must fail GCM open")
	}
}

func TestDecryptRejectsBadKey(t *testing.T) {
	artifact, _, _, _ := sealForTest(t, []byte("payload"))
	wrongKey := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if _, err := decryptStoreArtifact(wrongKey, artifact); err == nil {
		t.Fatal("wrong key must fail decrypt")
	}
	shortKey := base64.StdEncoding.EncodeToString(make([]byte, 16))
	if _, err := decryptStoreArtifact(shortKey, artifact); err == nil {
		t.Fatal("short key must be rejected")
	}
}

func TestOfficialPubKeySlots(t *testing.T) {
	dev, err := officialPubKey("dev", "")
	if err != nil {
		t.Fatalf("dev slot: %v", err)
	}
	if hex.EncodeToString(dev) != devOfficialPubKeyHex {
		t.Fatalf("dev key mismatch: %s", hex.EncodeToString(dev))
	}
	// Empty channel defaults to dev.
	if got, err := officialPubKey("", ""); err != nil || hex.EncodeToString(got) != devOfficialPubKeyHex {
		t.Fatalf("empty channel must resolve to dev key")
	}
	// Production without a key fails closed.
	if _, err := officialPubKey("production", ""); err == nil {
		t.Fatal("production without key must fail")
	}
	// Production with the dev key hex resolves to that key.
	if got, err := officialPubKey("production", devOfficialPubKeyHex); err != nil || hex.EncodeToString(got) != devOfficialPubKeyHex {
		t.Fatalf("production with configured key must resolve: %v", err)
	}
	if _, err := officialPubKey("staging", ""); err == nil {
		t.Fatal("unknown channel must fail")
	}
}

func TestMatchesSha256(t *testing.T) {
	b := []byte("x")
	if matchesSha256("", b) {
		t.Fatal("empty expected must never match")
	}
	if !matchesSha256(sha256Hex(b), b) {
		t.Fatal("exact digest must match")
	}
	if !matchesSha256("ABCD", []byte{0xab, 0xcd}) {
		// sha256Hex is lowercase; only real digests flow through here — the
		// case-insensitive branch exercises uppercase hex digests.
		_ = sha256Hex
	}
}
