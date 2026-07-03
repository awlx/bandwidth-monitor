package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	"bandwidth-monitor/apns"
	"bandwidth-monitor/contentstate"
	"bandwidth-monitor/httputil"
	"bandwidth-monitor/poller"
)

const (
	maxSubscriptions   = 5000            // global cap so the relay can't grow unbounded
	maxConcurrentFetch = 20              // bounded concurrency per tick, so one slow/unreachable
	fetchTimeout       = 8 * time.Second // server doesn't delay pushes to everyone else
	// DefaultMaxResponseBytes caps a polled server's response absent APNS_MAX_RESPONSE_BYTES.
	// /api/interfaces/history alone can run several MB per interface at the default 24h/1Hz
	// retention, so this needs to comfortably clear one server's full history, not just a
	// single Live Activity payload.
	DefaultMaxResponseBytes = 16 << 20
	registrationTTL         = time.Hour // drop a subscription if the app hasn't re-registered
	// minStaleAfter/staleAfterMultiple mirror the local liveactivity package's cushion sizing.
	minStaleAfter      = 60 * time.Second
	staleAfterMultiple = 15
)

type subscription struct {
	serverURL   string
	iface       string
	environment string
	lastSeen    time.Time
}

// Relay accepts registrations naming the caller's own bandwidth-monitor server, polls that
// server's existing plain /api/interfaces and /api/interfaces/history endpoints itself, and pushes
// to APNs using the operator's own key. No APNs configuration or new server-side code is needed on
// the polled server at all — it already serves the two endpoints this relay reads.
//
// There is intentionally no authentication (an operator decision — the app never surfaces any
// setup step to the end user). Hygiene against abuse is done without credentials: per-source rate
// limiting on registration, a global subscription cap, response size caps, fetch timeouts, and TTL
// expiry of subscriptions the app stops refreshing.
type Relay struct {
	apnsClient       *apns.Client
	httpClient       *http.Client
	interval         time.Duration
	maxResponseBytes int64
	runner           poller.Runner
	limiter          *ipRateLimiter

	mu   sync.Mutex
	subs map[string]subscription // push token -> subscription
}

// NewRelay builds a Relay. maxResponseBytes <= 0 falls back to DefaultMaxResponseBytes.
func NewRelay(apnsClient *apns.Client, interval time.Duration, maxResponseBytes int64) *Relay {
	if maxResponseBytes <= 0 {
		maxResponseBytes = DefaultMaxResponseBytes
	}
	r := &Relay{
		apnsClient:       apnsClient,
		httpClient:       newFetchClient(fetchTimeout, checkIP),
		interval:         interval,
		maxResponseBytes: maxResponseBytes,
		limiter:          newIPRateLimiter(20, 5*time.Minute),
		subs:             make(map[string]subscription),
	}
	r.runner.Init()
	return r
}

// newFetchClient builds the HTTP client used to poll registered servers. ipCheck (nil = allow
// all, tests only) runs inside the dialer's Control hook, which observes the concrete IP of
// every connection attempt *after* the client's own DNS resolution — unlike a LookupIP
// pre-check, this can't be split from the connection by a DNS-rebinding attacker serving one
// answer to the check and another to the dial.
func newFetchClient(timeout time.Duration, ipCheck func(net.IP) error) *http.Client {
	dialer := &net.Dialer{
		Timeout: timeout,
		Control: func(network, address string, _ syscall.RawConn) error {
			if ipCheck == nil {
				return nil
			}
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("dial %q: %w", address, err)
			}
			// Strip any IPv6 zone (fe80::1%eth0) so ParseIP accepts the literal.
			if i := strings.IndexByte(host, '%'); i >= 0 {
				host = host[:i]
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("dial %q: not an IP literal", address)
			}
			if err := ipCheck(ip); err != nil {
				return fmt.Errorf("dial %s: %w", ip, err)
			}
			return nil
		},
	}
	client := &http.Client{
		Timeout:   timeout,
		Transport: httputil.WrapTransport(&http.Transport{DialContext: dialer.DialContext}),
	}
	// Never follow redirects: a registered URL that dials a public IP could otherwise redirect
	// the request chain somewhere else entirely (SSRF via redirect) — though even a followed
	// redirect would still hit the dial-time IP check above.
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client
}

func (r *Relay) Run()  { r.runner.Run(r.interval, r.tick) }
func (r *Relay) Stop() { r.runner.Stop() }

func (r *Relay) staleAfter() time.Duration {
	if d := r.interval * staleAfterMultiple; d > minStaleAfter {
		return d
	}
	return minStaleAfter
}

// HandleRegister handles POST /api/liveactivity/register.
// Body: {"token":"...", "environment":"sandbox|production", "serverURL":"https://host:port", "interface":"eth0"}
func (r *Relay) HandleRegister(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	ip := clientIP(req)
	if !r.limiter.allow(ip) {
		log.Printf("apnsgateway: rejected registration from %s: rate limited", ip)
		http.Error(w, "too many registrations, slow down", http.StatusTooManyRequests)
		return
	}

	req.Body = http.MaxBytesReader(w, req.Body, 4<<10)
	var body struct {
		Token       string `json:"token"`
		Environment string `json:"environment"`
		ServerURL   string `json:"serverURL"`
		Interface   string `json:"interface"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		log.Printf("apnsgateway: rejected registration from %s: invalid JSON body: %v", ip, err)
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if body.Token == "" || len(body.Token) > 200 {
		log.Printf("apnsgateway: rejected registration from %s: invalid token (len=%d)", ip, len(body.Token))
		http.Error(w, "invalid token", http.StatusBadRequest)
		return
	}
	if body.Environment != "sandbox" && body.Environment != "production" {
		log.Printf("apnsgateway: rejected registration from %s: invalid environment %q", ip, body.Environment)
		http.Error(w, "environment must be sandbox or production", http.StatusBadRequest)
		return
	}
	normalizedURL, err := validateServerURL(body.ServerURL)
	if err != nil {
		log.Printf("apnsgateway: rejected registration from %s: invalid serverURL %q: %v", ip, body.ServerURL, err)
		http.Error(w, "invalid serverURL: "+err.Error(), http.StatusBadRequest)
		return
	}

	r.mu.Lock()
	_, existed := r.subs[body.Token]
	if !existed && len(r.subs) >= maxSubscriptions {
		r.mu.Unlock()
		log.Printf("apnsgateway: rejected registration from %s: relay at capacity (%d subscriptions)", ip, maxSubscriptions)
		http.Error(w, "relay at capacity, try again later", http.StatusServiceUnavailable)
		return
	}
	r.subs[body.Token] = subscription{
		serverURL: normalizedURL, iface: body.Interface, environment: body.Environment, lastSeen: time.Now(),
	}
	n := len(r.subs)
	r.mu.Unlock()

	if !existed {
		log.Printf("apnsgateway: registered %s (%s); %d active", normalizedURL, body.Environment, n)
	}
	httputil.WriteJSON(w, map[string]string{"status": "ok"})
}

// validateServerURL requires an http(s) URL whose host resolves only to public IP addresses.
//
// Registering serverURL is inherently a server-side-request-forgery shape (the relay makes outbound
// requests to an operator-supplied target), so this isn't just tidiness: a bandwidth-monitor server
// can never legitimately live at a private/loopback/link-local address anyway, since the gateway
// reaches it over the public internet, not the operator's LAN — so rejecting those targets costs
// the real use case nothing while closing off internal-network and cloud-metadata-endpoint probing.
// This registration-time resolve is for fast 400 feedback only; the authoritative check runs in
// the fetch client's dialer against the concrete IP of every connection attempt (newFetchClient),
// which DNS rebinding can't split from the connection.
func validateServerURL(raw string) (string, error) {
	if raw == "" || len(raw) > 2048 {
		return "", fmt.Errorf("empty or too long")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("scheme must be http or https")
	}
	if u.Hostname() == "" {
		return "", fmt.Errorf("missing host")
	}
	if err := checkPublicHost(u.Hostname()); err != nil {
		return "", err
	}
	return strings.TrimSuffix(raw, "/"), nil
}

// checkPublicHost resolves host and rejects it if any resolved address fails checkIP. This is
// the registration-time check, for fast feedback (400) on obviously invalid targets; the
// authoritative enforcement is the dial-time checkIP in newFetchClient, which a DNS-rebinding
// attacker can't bypass.
func checkPublicHost(host string) error {
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("resolve %q: %w", host, err)
	}
	for _, ip := range ips {
		if err := checkIP(ip); err != nil {
			return fmt.Errorf("%q: %w", host, err)
		}
	}
	return nil
}

// checkIP rejects private, loopback, link-local (which covers the 169.254.169.254
// cloud-metadata pattern), multicast, and unspecified addresses.
func checkIP(ip net.IP) error {
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return fmt.Errorf("non-public address %s", ip)
	}
	return nil
}

func (r *Relay) tick() {
	r.expireStale()

	r.mu.Lock()
	toks := make(map[string]subscription, len(r.subs))
	for k, v := range r.subs {
		toks[k] = v
	}
	r.mu.Unlock()

	sem := make(chan struct{}, maxConcurrentFetch)
	var wg sync.WaitGroup
	for token, sub := range toks {
		wg.Add(1)
		sem <- struct{}{}
		go func(token string, sub subscription) {
			defer wg.Done()
			defer func() { <-sem }()
			r.pushOne(token, sub)
		}(token, sub)
	}
	wg.Wait()
}

func (r *Relay) expireStale() {
	cutoff := time.Now().Add(-registrationTTL)
	r.mu.Lock()
	defer r.mu.Unlock()
	for token, sub := range r.subs {
		if sub.lastSeen.Before(cutoff) {
			delete(r.subs, token)
			log.Printf("apnsgateway: expired stale subscription for %s (no re-registration in %s)", sub.serverURL, registrationTTL)
		}
	}
}

func (r *Relay) pushOne(token string, sub subscription) {
	state, ok := r.fetchState(sub)
	if !ok {
		return
	}
	result, err := r.apnsClient.Send(token, sub.environment, r.staleAfter(), state)
	if err != nil {
		log.Printf("apnsgateway: send to %s: %v", sub.serverURL, err)
		return
	}
	if result.Dead() {
		r.mu.Lock()
		delete(r.subs, token)
		r.mu.Unlock()
	}
}

// fetchState polls the subscriber's own bandwidth-monitor server — the exact same plain
// /api/interfaces and /api/interfaces/history endpoints the iOS app already uses directly — and
// shapes the response into a contentstate.State via the shared builder.
func (r *Relay) fetchState(sub subscription) (contentstate.State, bool) {
	all, err := r.fetchInterfaces(sub.serverURL)
	if err != nil {
		log.Printf("apnsgateway: fetch %s/api/interfaces: %v", sub.serverURL, err)
		return contentstate.State{}, false
	}
	hist, err := r.fetchHistory(sub.serverURL)
	if err != nil {
		log.Printf("apnsgateway: fetch %s/api/interfaces/history: %v", sub.serverURL, err)
		return contentstate.State{}, false
	}

	name := sub.iface
	if name == "" || (hist[name] == nil && ifaceStat(all, name) == nil) {
		name = pickInterface(all, hist)
	}
	if name == "" {
		return contentstate.State{}, false
	}

	var rx, tx float64
	if s := ifaceStat(all, name); s != nil {
		rx, tx = s.RxRate, s.TxRate
	}
	return contentstate.Build(name, rx, tx, hist[name]), true
}

// remoteInterfaceStat mirrors the fields of collector.InterfaceStat this relay actually needs.
// Deliberately minimal and independently defined (not imported from the collector package) so this
// binary has zero router-specific dependencies and can run anywhere, unprivileged.
type remoteInterfaceStat struct {
	Name   string  `json:"name"`
	WAN    bool    `json:"wan"`
	RxRate float64 `json:"rx_rate"`
	TxRate float64 `json:"tx_rate"`
}

func (r *Relay) fetchInterfaces(serverURL string) ([]remoteInterfaceStat, error) {
	var out []remoteInterfaceStat
	if err := r.fetchJSON(serverURL+"/api/interfaces", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// TODO(api-versioning): this pulls every interface's full history (up to ~5.5MB per interface
// at the default 24h/1Hz retention — see PR #8) when contentstate.Build only ever uses the last
// contentstate.Window (1h), downsampled to contentstate.MaxPoints, for a single interface. Add
// optional ?iface=&since= params to /api/interfaces/history so this can request just that slice
// instead of relying on APNS_MAX_RESPONSE_BYTES to keep growing. This is additive/backward
// compatible on its own — the server currently has no API versioning scheme (flat /api/* routes,
// no /v1/ prefix; X-Bandwidth-Monitor is a build version, not a schema version) — so it likely
// doesn't need one just for this, but should ride along if a versioning scheme gets introduced
// for other reasons. Touches collector.go/handler.go on the main server as well as this file, so
// it needs a coordinated rebuild/redeploy of both binaries where they run on separate hosts.
func (r *Relay) fetchHistory(serverURL string) (map[string][]contentstate.Point, error) {
	var out map[string][]contentstate.Point
	if err := r.fetchJSON(serverURL+"/api/interfaces/history", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// fetchJSON GETs target and decodes it. SSRF enforcement happens inside the client's dialer
// (see newFetchClient): every connection attempt validates the concrete resolved IP, so a
// hostname repointed at a private address after registration (DNS rebinding) fails at dial
// time — there is deliberately no separate LookupIP pre-check here, because a check that
// resolves independently of the dial can be fed a different answer than the dial receives.
func (r *Relay) fetchJSON(target string, out any) error {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	start := time.Now()
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("after %s: %w", time.Since(start), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}

	// Read fully (bounded by the cap) rather than decoding straight off the body, so a
	// truncated/reset connection can be told apart from a response that's simply too big:
	// hitting the cap ends the read cleanly with no error, while a mid-transfer EOF surfaces
	// here as a read error with the partial byte count and elapsed time attached.
	body, err := io.ReadAll(io.LimitReader(resp.Body, r.maxResponseBytes+1))
	elapsed := time.Since(start)
	if err != nil {
		return fmt.Errorf("read body (got %d bytes, content-length=%d, after %s): %w", len(body), resp.ContentLength, elapsed, err)
	}
	if int64(len(body)) > r.maxResponseBytes {
		return fmt.Errorf("response exceeds %d byte cap (content-length=%d)", r.maxResponseBytes, resp.ContentLength)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode %d bytes (content-length=%d, after %s): %w", len(body), resp.ContentLength, elapsed, err)
	}
	return nil
}

func ifaceStat(all []remoteInterfaceStat, name string) *remoteInterfaceStat {
	for i := range all {
		if all[i].Name == name {
			return &all[i]
		}
	}
	return nil
}

func pickInterface(all []remoteInterfaceStat, hist map[string][]contentstate.Point) string {
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

func clientIP(req *http.Request) string {
	if host, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
		return host
	}
	return req.RemoteAddr
}

// ipRateLimiter is a minimal fixed-window-per-source limiter — no external dependency, just enough
// to stop a single source from hammering the (intentionally unauthenticated) register endpoint.
// A periodic sweep drops sources whose events have all aged out, so the map stays bounded by the
// number of currently-active sources rather than every source ever seen — without it, a long-lived
// public relay leaks an entry per churned client IP forever.
type ipRateLimiter struct {
	max    int
	window time.Duration

	mu        sync.Mutex
	events    map[string][]time.Time
	lastSweep time.Time
}

func newIPRateLimiter(max int, window time.Duration) *ipRateLimiter {
	return &ipRateLimiter{max: max, window: window, events: make(map[string][]time.Time), lastSweep: time.Now()}
}

func (l *ipRateLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-l.window)

	// At most once per window, drop sources with no events left in the window. Amortized cost is
	// negligible; keyed off registration traffic itself, so an idle relay holds only idle memory.
	if now.Sub(l.lastSweep) > l.window {
		for k, ts := range l.events {
			i := 0
			for i < len(ts) && ts[i].Before(cutoff) {
				i++
			}
			if i == len(ts) {
				delete(l.events, k)
			} else if i > 0 {
				l.events[k] = ts[i:]
			}
		}
		l.lastSweep = now
	}

	times := l.events[ip]
	i := 0
	for i < len(times) && times[i].Before(cutoff) {
		i++
	}
	times = times[i:]
	if len(times) >= l.max {
		l.events[ip] = times
		return false
	}
	l.events[ip] = append(times, now)
	return true
}
