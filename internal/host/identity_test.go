package host

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
)

func TestSignRequestRoundTrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	k := &HostKey{Private: priv, Public: pub}
	sig, ts := k.SignRequest("GET", "/api/devices/poll")
	if sig == "" || ts == "" {
		t.Fatal("expected non-empty signature and timestamp")
	}
	rawSig, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		t.Fatalf("signature is not base64: %v", err)
	}
	payload := "GET /api/devices/poll " + ts
	if !ed25519.Verify(pub, []byte(payload), rawSig) {
		t.Fatal("ed25519.Verify rejected signature")
	}
}

func TestFingerprintStable(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	k := &HostKey{Private: priv, Public: pub}
	fp := k.Fingerprint()
	if len(fp) != 64 {
		t.Fatalf("expected 64-char hex fingerprint, got %d", len(fp))
	}
	for _, r := range fp {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("non-hex char in fingerprint: %q", r)
		}
	}
	// Idempotent across calls.
	if k.Fingerprint() != fp {
		t.Fatal("fingerprint mismatch on subsequent call")
	}
}

func TestPublicKeyB64Decodes(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	k := &HostKey{Private: priv, Public: pub}
	decoded, err := base64.StdEncoding.DecodeString(k.PublicKeyB64())
	if err != nil {
		t.Fatalf("PublicKeyB64 is not base64: %v", err)
	}
	if len(decoded) != ed25519.PublicKeySize {
		t.Fatalf("expected %d-byte pubkey, got %d", ed25519.PublicKeySize, len(decoded))
	}
}
