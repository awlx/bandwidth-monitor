// Package apns signs ES256 provider JWTs and sends Live Activity push updates to Apple's APNs.
// Shared by the local per-instance push path (liveactivity package) and the standalone relay
// gateway (apnsgateway), so both hold-your-own-key operators and gateway-relayed users get
// identical signing and error diagnostics.
//
// Dependency-free: crypto/ecdsa + crypto/x509 (stdlib) sign the JWT and parse the .p8 key; the
// stdlib http.Client auto-negotiates HTTP/2 over TLS, which APNs requires.
package apns

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

// Config holds the APNs auth-key identity. All fields are required for NewClient to succeed.
type Config struct {
	KeyFile  string // path to the APNs auth key (.p8)
	KeyID    string // the key's 10-char ID
	TeamID   string // Apple Developer team ID
	BundleID string // app bundle ID, e.g. com.evilforbeginners.BandwidthMonitor
}

// Client signs provider JWTs (cached ~50 min) and posts Live Activity updates to APNs.
type Client struct {
	cfg    Config
	key    *ecdsa.PrivateKey
	client *http.Client

	jwtMu     sync.Mutex
	jwtCache  string
	jwtIssued time.Time
}

// NewClient parses the .p8 key. Returns an error if the key is missing or not a P-256 private key.
func NewClient(cfg Config) (*Client, error) {
	key, err := loadKey(cfg.KeyFile)
	if err != nil {
		return nil, err
	}
	return &Client{
		cfg: cfg,
		key: key,
		// The stdlib client auto-negotiates HTTP/2 over TLS via ALPN (which APNs requires), so no
		// golang.org/x/net/http2 dependency is needed.
		client: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// Result describes the outcome of a push attempt.
type Result struct {
	StatusCode int
	Reason     string // APNs JSON "reason" field; empty on success
	ApnsID     string // apns-id response header, useful when reporting issues to Apple
}

// Dead reports whether APNs says this token will never accept another push (drop it).
func (r Result) Dead() bool {
	return r.StatusCode == http.StatusGone || r.Reason == "BadDeviceToken" || r.Reason == "Unregistered"
}

// Send posts a Live Activity "update" event with contentState to APNs for token. environment must be
// "sandbox" or "production". staleAfter sets how long the pushed state stays valid on-device before
// ActivityKit shows it as stale. Logs the outcome (including a hint on 403, the most common
// misconfiguration: provider-token/JWT auth rejected, not a device or environment problem).
func (c *Client) Send(token, environment string, staleAfter time.Duration, contentState any) (Result, error) {
	jwt, err := c.providerToken()
	if err != nil {
		return Result{}, fmt.Errorf("jwt: %w", err)
	}

	now := time.Now().Unix()
	payload := map[string]any{"aps": map[string]any{
		"timestamp":     now,
		"event":         "update",
		"content-state": contentState,
		"stale-date":    now + int64(staleAfter.Seconds()),
	}}
	body, err := json.Marshal(payload)
	if err != nil {
		return Result{}, fmt.Errorf("marshal payload: %w", err)
	}

	host := "https://api.push.apple.com"
	if environment == "sandbox" {
		host = "https://api.sandbox.push.apple.com"
	}
	req, err := http.NewRequest(http.MethodPost, host+"/3/device/"+token, bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("authorization", "bearer "+jwt)
	req.Header.Set("apns-topic", c.cfg.BundleID+".push-type.liveactivity")
	req.Header.Set("apns-push-type", "liveactivity")
	req.Header.Set("apns-priority", "10")

	resp, err := c.client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("send: %w", err)
	}
	defer resp.Body.Close()

	result := Result{StatusCode: resp.StatusCode, ApnsID: resp.Header.Get("apns-id")}
	if resp.StatusCode == http.StatusOK {
		return result, nil
	}
	var apnsBody struct {
		Reason string `json:"reason"`
	}
	if decErr := json.NewDecoder(resp.Body).Decode(&apnsBody); decErr != nil {
		log.Printf("apns: failed to decode error body (status %d, apns-id=%s): %v", result.StatusCode, result.ApnsID, decErr)
	}
	result.Reason = apnsBody.Reason

	if result.Dead() {
		log.Printf("apns: token dead (%d %s, apns-id=%s)", result.StatusCode, result.Reason, result.ApnsID)
		return result, nil
	}
	// 403 is a provider-token (JWT) problem, not device/env. Surface the likely culprits.
	if resp.StatusCode == http.StatusForbidden {
		log.Printf("apns: 403 %s (apns-id=%s) — verify APNS_KEY_ID (%s) matches the .p8, APNS_TEAM_ID (%s), and the server clock (now=%s)",
			result.Reason, result.ApnsID, c.cfg.KeyID, c.cfg.TeamID, time.Now().UTC().Format(time.RFC3339))
		return result, nil
	}
	log.Printf("apns: %d %s (apns-id=%s)", result.StatusCode, result.Reason, result.ApnsID)
	return result, nil
}

// providerToken returns a cached ES256 APNs provider JWT, regenerating it at most every ~50 min
// (Apple rejects tokens older than an hour).
func (c *Client) providerToken() (string, error) {
	c.jwtMu.Lock()
	defer c.jwtMu.Unlock()
	if c.jwtCache != "" && time.Since(c.jwtIssued) < 50*time.Minute {
		return c.jwtCache, nil
	}
	header := base64url([]byte(fmt.Sprintf(`{"alg":"ES256","kid":"%s"}`, c.cfg.KeyID)))
	claims := base64url([]byte(fmt.Sprintf(`{"iss":"%s","iat":%d}`, c.cfg.TeamID, time.Now().Unix())))
	signingInput := header + "." + claims

	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, c.key, digest[:])
	if err != nil {
		return "", err
	}
	// JOSE wants the raw R||S, each left-padded to 32 bytes for P-256.
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])

	jwt := signingInput + "." + base64url(sig)
	c.jwtCache, c.jwtIssued = jwt, time.Now()
	return jwt, nil
}

func base64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func loadKey(path string) (*ecdsa.PrivateKey, error) {
	if path == "" {
		return nil, fmt.Errorf("no key file configured")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block in %s", path)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is not ECDSA/P-256")
	}
	return key, nil
}
