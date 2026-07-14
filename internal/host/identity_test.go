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
	sig, ts := k.SignRequest("GET", "/api/devices/poll", nil)
	if sig == "" || ts == "" {
		t.Fatal("expected non-empty signature and timestamp")
	}
	rawSig, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		t.Fatalf("signature is not base64: %v", err)
	}
	payload := CanonicalPayload("GET", "/api/devices/poll", ts, nil)
	if !ed25519.Verify(pub, []byte(payload), rawSig) {
		t.Fatal("ed25519.Verify rejected signature")
	}
}

func TestSignRequestRoundTripWithBody(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	k := &HostKey{Private: priv, Public: pub}
	body := []byte(`{"decision":"accept","reason":""}`)
	sig, ts := k.SignRequest("POST", "/api/devices/sessions/s/invocations/i/ack", body)
	rawSig, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		t.Fatalf("signature is not base64: %v", err)
	}
	good := CanonicalPayload("POST", "/api/devices/sessions/s/invocations/i/ack", ts, body)
	if !ed25519.Verify(pub, []byte(good), rawSig) {
		t.Fatal("ed25519.Verify rejected signature over the signed body")
	}
	// A tampered body must NOT verify against the original signature.
	tampered := CanonicalPayload("POST", "/api/devices/sessions/s/invocations/i/ack", ts, []byte(`{"decision":"deny"}`))
	if ed25519.Verify(pub, []byte(tampered), rawSig) {
		t.Fatal("signature unexpectedly verified against a tampered body")
	}
}

// TestCanonicalPayloadExactString pins the canonical signed string for a
// known (method, path, ts, body). The backend's _canonical_payload in
// application/api/devices/auth.py MUST produce this identical string —
// see the Python test test_canonical_payload_matches_cli.
func TestCanonicalPayloadExactString(t *testing.T) {
	// sha256("") = e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
	gotEmpty := CanonicalPayload("GET", "/api/devices/poll", "1700000000", nil)
	wantEmpty := "GET /api/devices/poll 1700000000 " +
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if gotEmpty != wantEmpty {
		t.Fatalf("empty-body canonical mismatch:\n got=%q\nwant=%q", gotEmpty, wantEmpty)
	}

	// sha256("hello") = 2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824
	gotBody := CanonicalPayload("POST", "/api/devices/x", "1700000000", []byte("hello"))
	wantBody := "POST /api/devices/x 1700000000 " +
		"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if gotBody != wantBody {
		t.Fatalf("body canonical mismatch:\n got=%q\nwant=%q", gotBody, wantBody)
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
