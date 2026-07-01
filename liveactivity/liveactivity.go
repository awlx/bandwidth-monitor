// Package liveactivity pushes iOS Live Activity updates to Apple's APNs so the Bandwidth Monitor
// Lock Screen / Dynamic Island view keeps updating while the app is suspended.
//
// The iOS app registers a per-activity push token via POST /api/liveactivity/register; a background
// loop then builds the Live Activity content-state from the collector and pushes it to APNs for each
// registered token. Dependency-free beyond the stdlib and golang.org/x/net/http2 (already required):
// crypto/ecdsa + crypto/x509 sign the ES256 provider JWT and parse the .p8 key.
package liveactivity

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
	"math"
	"net/http"
	"os"
	"sync"
	"time"

	"bandwidth-monitor/collector"
	"bandwidth-monitor/poller"
)

const (
	historyWindow = time.Hour
	maxPoints     = 38
	staleAfter    = 120 * time.Second
)

// Config holds APNs settings. The feature is enabled only when KeyFile, KeyID, TeamID, and BundleID
// are all set (wired in main.go).
type Config struct {
	KeyFile  string        // path to the APNs auth key (.p8)
	KeyID    string        // the key's 10-char ID
	TeamID   string        // Apple Developer team ID
	BundleID string        // app bundle ID, e.g. com.evilforbeginners.BandwidthMonitor
	Env      string        // default APNs environment: "production" (default) or "sandbox"
	Interval time.Duration // push cadence (default 10s)
}

type registration struct {
	iface       string
	environment string
	lastSeen    time.Time
}

// Manager owns the token registry, signing key, and push loop.
type Manager struct {
	cfg    Config
	col    *collector.Collector
	key    *ecdsa.PrivateKey
	client *http.Client
	runner poller.Runner

	mu     sync.Mutex
	tokens map[string]registration

	jwtMu     sync.Mutex
	jwtCache  string
	jwtIssued time.Time
}

// New parses the .p8 key and returns a configured Manager. Returns an error if the key is missing or
// not a P-256 private key.
func New(cfg Config, col *collector.Collector) (*Manager, error) {
	key, err := loadKey(cfg.KeyFile)
	if err != nil {
		return nil, err
	}
	if cfg.Env != "sandbox" {
		cfg.Env = "production"
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 10 * time.Second
	}
	m := &Manager{
		cfg:    cfg,
		col:    col,
		key:    key,
		tokens: make(map[string]registration),
		// The stdlib client auto-negotiates HTTP/2 over TLS via ALPN (which APNs requires), so no
		// golang.org/x/net/http2 dependency is needed.
		client: &http.Client{Timeout: 10 * time.Second},
	}
	m.runner.Init()
	return m, nil
}

// Register records (or refreshes) a push token and the interface + APNs environment it wants.
func (m *Manager) Register(token, iface, environment string) {
	if token == "" {
		return
	}
	if environment != "sandbox" && environment != "production" {
		environment = m.cfg.Env
	}
	m.mu.Lock()
	m.tokens[token] = registration{iface: iface, environment: environment, lastSeen: time.Now()}
	n := len(m.tokens)
	m.mu.Unlock()
	log.Printf("liveactivity: registered token for %q (%s); %d active", iface, environment, n)
}

// Run pushes to all registered tokens every Interval until Stop. Blocks; call via `go m.Run()`.
func (m *Manager) Run() { m.runner.Run(m.cfg.Interval, m.tick) }

// Stop halts the push loop.
func (m *Manager) Stop() { m.runner.Stop() }

func (m *Manager) tick() {
	m.mu.Lock()
	toks := make(map[string]registration, len(m.tokens))
	for k, v := range m.tokens {
		toks[k] = v
	}
	m.mu.Unlock()

	for token, reg := range toks {
		if state := m.buildState(reg.iface); state != nil {
			m.send(token, reg.environment, state)
		}
	}
}

// --- content-state (keys MUST match the iOS BandwidthActivityAttributes.ContentState) ------------

type point struct {
	T  int64   `json:"t"`
	Rx float64 `json:"rx"`
	Tx float64 `json:"tx"`
}

type contentState struct {
	InterfaceName string  `json:"interfaceName"`
	RxRate        float64 `json:"rxRate"`
	TxRate        float64 `json:"txRate"`
	Points        []point `json:"points"`
	UpdatedAt     float64 `json:"updatedAt"`
}

// buildState mirrors the iOS liveState(): the selected interface's last hour, downsampled keeping
// peaks, with a synthetic "now" sample carrying the live rate appended so the marked latest point
// reflects the current rate.
func (m *Manager) buildState(iface string) *contentState {
	all := m.col.GetAll()
	hist := m.col.GetHistory()

	name := iface
	if name == "" || (hist[name] == nil && statFor(all, name) == nil) {
		name = pickInterface(all, hist)
	}
	if name == "" {
		return nil
	}

	var rx, tx float64
	if s := statFor(all, name); s != nil {
		rx, tx = s.RxRate, s.TxRate
	}

	cutoff := time.Now().Add(-historyWindow).UnixMilli()
	var windowed []collector.HistoryPoint
	for _, p := range hist[name] {
		if p.Timestamp >= cutoff {
			windowed = append(windowed, p)
		}
	}
	windowed = downsamplePeaks(windowed, maxPoints)

	pts := make([]point, 0, len(windowed)+1)
	for _, p := range windowed {
		pts = append(pts, point{T: p.Timestamp, Rx: p.RxRate, Tx: p.TxRate})
	}
	pts = append(pts, point{T: time.Now().UnixMilli(), Rx: rx, Tx: tx})

	return &contentState{
		InterfaceName: name,
		RxRate:        rx,
		TxRate:        tx,
		Points:        pts,
		UpdatedAt:     float64(time.Now().UnixMilli()) / 1000,
	}
}

func statFor(all []collector.InterfaceStat, name string) *collector.InterfaceStat {
	for i := range all {
		if all[i].Name == name {
			return &all[i]
		}
	}
	return nil
}

func pickInterface(all []collector.InterfaceStat, hist map[string][]collector.HistoryPoint) string {
	for i := range all {
		if all[i].WAN {
			return all[i].Name
		}
	}
	if len(all) > 0 {
		return all[0].Name
	}
	for k := range hist {
		return k
	}
	return ""
}

// downsamplePeaks reduces pts to ~maxPoints, keeping the highest-rate sample in each bucket so brief
// spikes survive (matches the iOS downsampledPreservingPeaks).
func downsamplePeaks(pts []collector.HistoryPoint, maxPoints int) []collector.HistoryPoint {
	if maxPoints <= 0 || len(pts) <= maxPoints {
		return pts
	}
	bucket := float64(len(pts)) / float64(maxPoints)
	out := make([]collector.HistoryPoint, 0, maxPoints)
	start := 0
	for i := 0; i < maxPoints; i++ {
		end := len(pts)
		if i < maxPoints-1 {
			end = int(math.Round(float64(i+1) * bucket))
		}
		if start >= end {
			continue
		}
		peak := pts[start]
		for _, p := range pts[start:end] {
			if math.Max(p.RxRate, p.TxRate) > math.Max(peak.RxRate, peak.TxRate) {
				peak = p
			}
		}
		out = append(out, peak)
		start = end
	}
	return out
}

// --- APNs ---------------------------------------------------------------------------------------

func (m *Manager) send(token, environment string, state *contentState) {
	jwt, err := m.providerToken()
	if err != nil {
		log.Printf("liveactivity: jwt: %v", err)
		return
	}

	now := time.Now().Unix()
	payload := map[string]any{"aps": map[string]any{
		"timestamp":     now,
		"event":         "update",
		"content-state": state,
		"stale-date":    now + int64(staleAfter.Seconds()),
	}}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}

	host := "https://api.push.apple.com"
	if environment == "sandbox" {
		host = "https://api.sandbox.push.apple.com"
	}
	req, err := http.NewRequest(http.MethodPost, host+"/3/device/"+token, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("authorization", "bearer "+jwt)
	req.Header.Set("apns-topic", m.cfg.BundleID+".push-type.liveactivity")
	req.Header.Set("apns-push-type", "liveactivity")
	req.Header.Set("apns-priority", "10")

	resp, err := m.client.Do(req)
	if err != nil {
		log.Printf("liveactivity: send: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return
	}
	var apns struct {
		Reason string `json:"reason"`
	}
	json.NewDecoder(resp.Body).Decode(&apns)
	// Drop tokens Apple says are dead so we stop wasting pushes on them.
	if resp.StatusCode == http.StatusGone || apns.Reason == "BadDeviceToken" || apns.Reason == "Unregistered" {
		m.mu.Lock()
		delete(m.tokens, token)
		m.mu.Unlock()
		log.Printf("liveactivity: dropped token (%d %s)", resp.StatusCode, apns.Reason)
		return
	}
	log.Printf("liveactivity: APNs %d %s", resp.StatusCode, apns.Reason)
}

// providerToken returns a cached ES256 APNs provider JWT, regenerating it at most every ~50 min
// (Apple rejects tokens older than an hour).
func (m *Manager) providerToken() (string, error) {
	m.jwtMu.Lock()
	defer m.jwtMu.Unlock()
	if m.jwtCache != "" && time.Since(m.jwtIssued) < 50*time.Minute {
		return m.jwtCache, nil
	}
	header := base64url([]byte(fmt.Sprintf(`{"alg":"ES256","kid":"%s"}`, m.cfg.KeyID)))
	claims := base64url([]byte(fmt.Sprintf(`{"iss":"%s","iat":%d}`, m.cfg.TeamID, time.Now().Unix())))
	signingInput := header + "." + claims

	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, m.key, digest[:])
	if err != nil {
		return "", err
	}
	// JOSE wants the raw R||S, each left-padded to 32 bytes for P-256.
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])

	jwt := signingInput + "." + base64url(sig)
	m.jwtCache, m.jwtIssued = jwt, time.Now()
	return jwt, nil
}

func base64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func loadKey(path string) (*ecdsa.PrivateKey, error) {
	if path == "" {
		return nil, fmt.Errorf("APNS_KEY_FILE not set")
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
