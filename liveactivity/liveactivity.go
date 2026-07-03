// Package liveactivity pushes iOS Live Activity updates directly to Apple's APNs using an
// operator-held key, so the Bandwidth Monitor Lock Screen / Dynamic Island view keeps updating
// while the app is suspended.
//
// This is the "hold your own key" path: fully independent, no dependency on anyone else's
// infrastructure. It's off by default (nil unless APNS_KEY_FILE is configured) — most users don't
// need this because the app defaults to registering with a shared relay gateway instead (see the
// apnsgateway package), which needs no APNs key on this server at all. Keep this path for operators
// who'd rather not depend on a third party.
//
// The iOS app registers a per-activity push token via POST /api/liveactivity/register; a background
// loop then builds the Live Activity content-state from the collector and pushes it to APNs for each
// registered token.
package liveactivity

import (
	"log"
	"sync"
	"time"

	"bandwidth-monitor/apns"
	"bandwidth-monitor/collector"
	"bandwidth-monitor/contentstate"
	"bandwidth-monitor/poller"
)

const (
	// minStaleAfter is a floor on the stale-date cushion regardless of push interval, so a
	// fast-configured cadence doesn't leave an unreasonably short window either.
	minStaleAfter = 60 * time.Second
	// staleAfterMultiple sizes the cushion relative to the configured push interval, so a few
	// dropped pushes in a row (transient network blip, brief APNs error) don't prematurely mark the
	// activity stale ahead of the next successful push. At the default 10s interval this gives 150s,
	// more forgiving than a short fixed value.
	staleAfterMultiple = 15
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

// Manager owns the token registry and push loop.
type Manager struct {
	cfg    Config
	col    *collector.Collector
	apns   *apns.Client
	runner poller.Runner

	mu     sync.Mutex
	tokens map[string]registration
}

// New parses the .p8 key and returns a configured Manager. Returns an error if the key is missing or
// not a P-256 private key.
func New(cfg Config, col *collector.Collector) (*Manager, error) {
	client, err := apns.NewClient(apns.Config{
		KeyFile: cfg.KeyFile, KeyID: cfg.KeyID, TeamID: cfg.TeamID, BundleID: cfg.BundleID,
	})
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
		apns:   client,
		tokens: make(map[string]registration),
	}
	m.runner.Init()
	return m, nil
}

// maxTokens caps the registry so the (unauthenticated) register endpoint can't grow it without
// bound — the same class of leak as an unswept rate-limiter map. Dead tokens are already pruned
// on APNs 410/BadDeviceToken/Unregistered; this bounds the live set. Far above any realistic
// number of devices watching one household router, so hitting it means abuse, not use.
const maxTokens = 1000

// Register records (or refreshes) a push token and the interface + APNs environment it wants.
// Returns false when the registry is at capacity and the token is new.
func (m *Manager) Register(token, iface, environment string) bool {
	if token == "" {
		return false
	}
	if environment != "sandbox" && environment != "production" {
		environment = m.cfg.Env
	}
	m.mu.Lock()
	if _, existed := m.tokens[token]; !existed && len(m.tokens) >= maxTokens {
		m.mu.Unlock()
		log.Printf("liveactivity: rejected registration: at capacity (%d tokens)", maxTokens)
		return false
	}
	m.tokens[token] = registration{iface: iface, environment: environment, lastSeen: time.Now()}
	n := len(m.tokens)
	m.mu.Unlock()
	log.Printf("liveactivity: registered token for %q (%s); %d active", iface, environment, n)
	return true
}

// Run pushes to all registered tokens every Interval until Stop. Blocks; call via `go m.Run()`.
func (m *Manager) Run() { m.runner.Run(m.cfg.Interval, m.tick) }

// Stop halts the push loop.
func (m *Manager) Stop() { m.runner.Stop() }

// staleAfter is the stale-date cushion: a multiple of the configured push interval (with a floor),
// so one or two dropped pushes don't prematurely mark the activity stale.
func (m *Manager) staleAfter() time.Duration {
	if d := m.cfg.Interval * staleAfterMultiple; d > minStaleAfter {
		return d
	}
	return minStaleAfter
}

func (m *Manager) tick() {
	m.mu.Lock()
	toks := make(map[string]registration, len(m.tokens))
	for k, v := range m.tokens {
		toks[k] = v
	}
	m.mu.Unlock()

	for token, reg := range toks {
		state, ok := m.buildState(reg.iface)
		if !ok {
			continue
		}
		result, err := m.apns.Send(token, reg.environment, m.staleAfter(), state)
		if err != nil {
			log.Printf("liveactivity: send: %v", err)
			continue
		}
		if result.Dead() {
			m.mu.Lock()
			delete(m.tokens, token)
			m.mu.Unlock()
		}
	}
}

// buildState gathers the selected interface's current rate and recent history from the local
// collector and shapes it into a contentstate.State via the shared builder.
func (m *Manager) buildState(iface string) (contentstate.State, bool) {
	all := m.col.GetAll()
	hist := m.col.GetHistory()

	name := iface
	if name == "" || (hist[name] == nil && statFor(all, name) == nil) {
		name = pickInterface(all, hist)
	}
	if name == "" {
		return contentstate.State{}, false
	}

	var rx, tx float64
	if s := statFor(all, name); s != nil {
		rx, tx = s.RxRate, s.TxRate
	}

	points := make([]contentstate.Point, 0, len(hist[name]))
	for _, p := range hist[name] {
		points = append(points, contentstate.Point{T: p.Timestamp, Rx: p.RxRate, Tx: p.TxRate})
	}

	return contentstate.Build(name, rx, tx, points), true
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
