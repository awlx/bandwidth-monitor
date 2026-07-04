package apns

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
	c := &Client{cfg: Config{KeyID: "ABC123DEFG", TeamID: "TEAM123456"}, key: key}

	tok, err := c.providerToken()
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

// A 410 or BadDeviceToken/Unregistered reason must be treated as dead so callers stop wasting
// pushes on tokens Apple will never accept again.
func TestResultDead(t *testing.T) {
	cases := []struct {
		r    Result
		want bool
	}{
		{Result{StatusCode: 200}, false},
		{Result{StatusCode: 410}, true},
		{Result{StatusCode: 400, Reason: "BadDeviceToken"}, true},
		{Result{StatusCode: 400, Reason: "Unregistered"}, true},
		{Result{StatusCode: 403, Reason: "InvalidProviderToken"}, false},
	}
	for _, c := range cases {
		if got := c.r.Dead(); got != c.want {
			t.Errorf("Result{%d, %q}.Dead() = %v, want %v", c.r.StatusCode, c.r.Reason, got, c.want)
		}
	}
}
