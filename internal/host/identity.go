package host

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// HostKey holds the Ed25519 keypair used to sign requests to the server.
type HostKey struct {
	Private ed25519.PrivateKey
	Public  ed25519.PublicKey
}

func keyPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".docsgpt", "host.key")
}

// LoadOrCreateKey returns the persisted key, generating + persisting one on
// first use. The private key is stored alongside the public key, base64-
// encoded, one per line: ``priv\npub\n``. File mode is 0600.
func LoadOrCreateKey() (*HostKey, error) {
	data, err := os.ReadFile(keyPath())
	if err == nil {
		return parseKey(string(data))
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read host.key: %w", err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("ed25519 generate: %w", err)
	}
	k := &HostKey{Private: priv, Public: pub}
	if err := persistKey(k); err != nil {
		return nil, err
	}
	return k, nil
}

func parseKey(content string) (*HostKey, error) {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) < 2 {
		return nil, fmt.Errorf("host.key: malformed (expected 2 lines)")
	}
	priv, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[0]))
	if err != nil {
		return nil, fmt.Errorf("host.key: bad private base64: %w", err)
	}
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil {
		return nil, fmt.Errorf("host.key: bad public base64: %w", err)
	}
	if len(priv) != ed25519.PrivateKeySize || len(pub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("host.key: unexpected key sizes")
	}
	return &HostKey{
		Private: ed25519.PrivateKey(priv),
		Public:  ed25519.PublicKey(pub),
	}, nil
}

func persistKey(k *HostKey) error {
	dir := filepath.Dir(keyPath())
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("mkdir host key: %w", err)
	}
	body := fmt.Sprintf(
		"%s\n%s\n",
		base64.StdEncoding.EncodeToString(k.Private),
		base64.StdEncoding.EncodeToString(k.Public),
	)
	if err := os.WriteFile(keyPath(), []byte(body), 0600); err != nil {
		return fmt.Errorf("write host.key: %w", err)
	}
	return nil
}

// PublicKeyB64 returns the base64-encoded public key for pairing payloads.
func (k *HostKey) PublicKeyB64() string {
	return base64.StdEncoding.EncodeToString(k.Public)
}

// Fingerprint returns the hex SHA-256 of the public key bytes. The server
// stores the same hex digest in devices.machine_pubkey_fingerprint.
func (k *HostKey) Fingerprint() string {
	digest := sha256.Sum256(k.Public)
	return hex.EncodeToString(digest[:])
}

// CanonicalPayload builds the string that gets signed / verified:
// "METHOD PATH TIMESTAMP SHA256_HEX(BODY)". The body hash binds the request
// body into the signature so a captured signature can't be replayed with a
// tampered body inside the timestamp window. For a request with no body
// (GET) this is the SHA-256 of the empty string.
//
// KEEP IN SYNC with the backend verifier
// (application/api/devices/auth.py ``_canonical_payload``). The hex encoding
// and single-space separators must match byte-for-byte.
func CanonicalPayload(method, path, ts string, body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("%s %s %s %s", method, path, ts, hex.EncodeToString(sum[:]))
}

// SignRequest signs the canonical payload (including a hash of ``body``) and
// returns (base64-signature, unix timestamp string). Pass nil/empty ``body``
// for requests without one (e.g. GET).
func (k *HostKey) SignRequest(method, path string, body []byte) (string, string) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	payload := CanonicalPayload(method, path, ts, body)
	sig := ed25519.Sign(k.Private, []byte(payload))
	return base64.StdEncoding.EncodeToString(sig), ts
}
