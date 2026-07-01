package liveactivity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
)

// The provider JWT must be a well-formed ES256 token whose signature verifies against the key.
func TestProviderTokenIsValidES256(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{cfg: Config{KeyID: "ABC123DEFG", TeamID: "TEAM123456"}, key: key}

	tok, err := m.providerToken()
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("want 3 JWT parts, got %d", len(parts))
	}

	hdr, _ := base64.RawURLEncoding.DecodeString(parts[0])
	var h map[string]string
	if err := json.Unmarshal(hdr, &h); err != nil {
		t.Fatalf("header not JSON: %v", err)
	}
	if h["alg"] != "ES256" || h["kid"] != "ABC123DEFG" {
		t.Errorf("bad header: %s", hdr)
	}

	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	if len(sig) != 64 {
		t.Fatalf("ES256 signature must be 64 bytes (R||S), got %d", len(sig))
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(&key.PublicKey, digest[:], r, s) {
		t.Error("signature does not verify against the public key")
	}
}

// The content-state JSON keys must match the iOS BandwidthActivityAttributes.ContentState exactly,
// or ActivityKit will reject the push.
func TestContentStateJSONKeys(t *testing.T) {
	cs := contentState{
		InterfaceName: "eth0",
		RxRate:        1,
		TxRate:        2,
		Points:        []point{{T: 123, Rx: 4, Tx: 5}},
		UpdatedAt:     6.5,
	}
	b, err := json.Marshal(cs)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, key := range []string{
		`"interfaceName"`, `"rxRate"`, `"txRate"`, `"points"`, `"updatedAt"`, `"t"`, `"rx"`, `"tx"`,
	} {
		if !strings.Contains(got, key) {
			t.Errorf("content-state JSON missing key %s: %s", key, got)
		}
	}
}
