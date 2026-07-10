package bandwidthtop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"bandwidth-monitor/geoip"
)

const (
	DefaultPublicURL = "https://ip.ffmuc.net/json"
	maxResponseBody  = 64 << 10
	defaultCacheSize = 4096
	defaultQueueSize = 256
	monitorProbeIP   = "192.0.2.1"
)

type Enrichment struct {
	Hostname string
	Country  string
	City     string
	Provider string
	ASN      uint
	Source   string
	Err      string
}

type Config struct {
	GeoDB                   *geoip.DB
	ServerURL               string
	PublicURL               string
	HTTPClient              *http.Client
	Concurrency             int
	CacheSize               int
	QueueSize               int
	DisablePublic           bool
	AllowHTTP               bool
	MonitorDiscovery        bool
	DisableMonitorDiscovery bool

	lookupIP func(context.Context, string) ([]net.IP, error)

	skipMonitorProbe      bool
	allowDiscoveryTestURL bool
}

type cacheEntry struct {
	value Enrichment
	done  bool
}

type Enricher struct {
	cfg             Config
	monitorClient   *http.Client
	publicClient    *http.Client
	userAgent       string
	lookupIP        func(context.Context, string) ([]net.IP, error)
	localDBs        *LocalDatabases
	sourceMu        sync.RWMutex
	monitorState    string
	startupWarnings []string

	mu        sync.RWMutex
	cache     map[string]cacheEntry
	order     []string
	jobs      chan string
	stopCh    chan struct{}
	workers   sync.WaitGroup
	closeOnce sync.Once
	closed    bool
	inFlight  int
	maxSeen   int
}

func NewEnricher(cfg Config) (*Enricher, error) {
	if cfg.PublicURL == "" {
		cfg.PublicURL = DefaultPublicURL
	}
	for i, raw := range []string{cfg.ServerURL, cfg.PublicURL} {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("invalid enrichment URL %q", raw)
		}
		if i == 1 && !publicURLAllowed(u, cfg.AllowHTTP) {
			return nil, fmt.Errorf("public enrichment URL must use HTTPS")
		}
		if i == 0 && u.Scheme != "https" && u.Scheme != "http" {
			return nil, fmt.Errorf("monitor URL must use HTTP or HTTPS")
		}
		if i == 0 && cfg.MonitorDiscovery && !cfg.allowDiscoveryTestURL &&
			!discoveryURLAllowed(raw) {
			return nil, errors.New("invalid gateway monitor discovery URL")
		}
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.CacheSize <= 0 {
		cfg.CacheSize = defaultCacheSize
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultQueueSize
	}
	lookupIP := cfg.lookupIP
	if lookupIP == nil {
		lookupIP = func(ctx context.Context, host string) ([]net.IP, error) {
			addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			ips := make([]net.IP, 0, len(addrs))
			for _, addr := range addrs {
				ips = append(ips, addr.IP)
			}
			return ips, nil
		}
	}

	userAgent := UserAgent()
	monitorClient := cloneHTTPClient(cfg.HTTPClient)
	if cfg.MonitorDiscovery {
		monitorTransport := cloneTransport(monitorClient.Transport)
		monitorTransport.Proxy = nil
		monitorClient.Transport = monitorTransport
		monitorClient.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	} else {
		previous := monitorClient.CheckRedirect
		monitorClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			req.Header.Set("User-Agent", userAgent)
			if previous != nil {
				return previous(req, via)
			}
			if len(via) >= 10 {
				return errors.New("too many monitor enrichment redirects")
			}
			return nil
		}
	}
	publicClient := cloneHTTPClient(cfg.HTTPClient)
	transport := cloneTransport(publicClient.Transport)
	transport.Proxy = nil
	transport.DialTLSContext = nil
	transport.DialContext = publicDialContext(cfg.AllowHTTP, lookupIP)
	publicClient.Transport = schemeRoundTripper{base: transport}
	publicClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		req.Header.Set("User-Agent", userAgent)
		if len(via) >= 5 {
			return errors.New("too many public enrichment redirects")
		}
		return validatePublicDestination(req.Context(), req.URL, cfg.AllowHTTP, lookupIP)
	}

	e := &Enricher{
		cfg:           cfg,
		monitorClient: monitorClient,
		publicClient:  publicClient,
		userAgent:     userAgent,
		lookupIP:      lookupIP,
		cache:         make(map[string]cacheEntry, cfg.CacheSize),
		order:         make([]string, 0, cfg.CacheSize),
		jobs:          make(chan string, cfg.QueueSize),
		stopCh:        make(chan struct{}),
		monitorState:  "not discovered",
	}
	if cfg.DisableMonitorDiscovery {
		e.monitorState = "disabled by flag"
	}
	if cfg.ServerURL != "" {
		e.monitorState = "configured"
		if cfg.MonitorDiscovery {
			e.monitorState = "discovered"
		}
		if !cfg.skipMonitorProbe {
			if _, err := e.fetch(monitorProbeIP, cfg.ServerURL, true); err != nil {
				e.cfg.ServerURL = ""
				e.monitorState = "unavailable"
				warning := "monitor enrichment disabled: startup readiness probe failed"
				if cfg.MonitorDiscovery {
					warning = "gateway monitor discovery unavailable: startup probe failed"
				}
				e.startupWarnings = append(e.startupWarnings, warning)
			}
		}
	}
	for i := 0; i < cfg.Concurrency; i++ {
		e.workers.Add(1)
		go e.worker()
	}
	return e, nil
}

type publicSchemeContextKey struct{}

type schemeRoundTripper struct {
	base http.RoundTripper
}

func (s schemeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := context.WithValue(req.Context(), publicSchemeContextKey{}, req.URL.Scheme)
	return s.base.RoundTrip(req.Clone(ctx))
}

func publicDialContext(allowHTTP bool, lookupIP func(context.Context, string) ([]net.IP, error)) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("invalid public dial address: %w", err)
		}
		scheme, _ := ctx.Value(publicSchemeContextKey{}).(string)
		ips, err := resolveAndValidate(ctx, host, scheme == "http", allowHTTP, lookupIP)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

func cloneHTTPClient(base *http.Client) *http.Client {
	if base == nil {
		return &http.Client{Timeout: 2 * time.Second}
	}
	clone := *base
	if clone.Timeout <= 0 {
		clone.Timeout = 2 * time.Second
	}
	return &clone
}

func cloneTransport(base http.RoundTripper) *http.Transport {
	if transport, ok := base.(*http.Transport); ok {
		return transport.Clone()
	}
	return http.DefaultTransport.(*http.Transport).Clone()
}

func publicURLAllowed(u *url.URL, allowHTTP bool) bool {
	return u != nil && (u.Scheme == "https" ||
		(allowHTTP && u.Scheme == "http" && isLoopbackHost(u.Hostname())))
}

func isLoopbackHost(host string) bool {
	ip := net.ParseIP(host)
	return strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
}

func validatePublicDestination(ctx context.Context, u *url.URL, allowHTTP bool, lookup func(context.Context, string) ([]net.IP, error)) error {
	if !publicURLAllowed(u, allowHTTP) {
		return errors.New("public enrichment destination must use HTTPS")
	}
	_, err := resolveAndValidate(ctx, u.Hostname(), u.Scheme == "http", allowHTTP, lookup)
	return err
}

func resolveAndValidate(ctx context.Context, host string, isHTTP, allowHTTP bool, lookup func(context.Context, string) ([]net.IP, error)) ([]net.IP, error) {
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		var err error
		ips, err = lookup(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve public enrichment host: %w", err)
		}
	}
	if len(ips) == 0 {
		return nil, errors.New("public enrichment host resolved to no addresses")
	}
	for _, ip := range ips {
		if isHTTP && allowHTTP && ip.IsLoopback() {
			continue
		}
		if !publicEligible(ip) {
			return nil, fmt.Errorf("public enrichment resolved to disallowed address %s", ip)
		}
	}
	return ips, nil
}

var nonPublicPrefixes = func() []netip.Prefix {
	cidrs := []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8",
		"169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24",
		"192.31.196.0/24", "192.52.193.0/24", "192.88.99.0/24",
		"192.168.0.0/16", "192.175.48.0/24", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
		"::/128", "::1/128", "64:ff9b::/96", "64:ff9b:1::/48",
		"100::/64", "100:0:0:1::/64", "2001::/23", "2001:db8::/32",
		"2002::/16", "2620:4f:8000::/48", "3fff::/20", "5f00::/16",
		"fc00::/7", "fe80::/10", "ff00::/8",
	}
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, cidr := range cidrs {
		out = append(out, netip.MustParsePrefix(cidr))
	}
	return out
}()

func publicEligible(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return addr.IsGlobalUnicast()
}

func (e *Enricher) local(ip string) Enrichment {
	var out Enrichment
	if e.cfg.GeoDB != nil {
		if result := e.cfg.GeoDB.Lookup(ip); result != nil {
			mergeLocalResult(&out, result, true, true)
		}
	}
	e.sourceMu.RLock()
	defer e.sourceMu.RUnlock()
	dbs := e.localDBs
	if dbs != nil {
		if dbs.city != nil {
			mergeLocalResult(&out, dbs.city.Lookup(ip), true, false)
		}
		if dbs.asn != nil {
			mergeLocalResult(&out, dbs.asn.Lookup(ip), false, true)
		}
	}
	if out.Country != "" || out.City != "" || out.ASN != 0 || out.Provider != "" {
		out.Source = "local"
	}
	return out
}

func mergeLocalResult(out *Enrichment, result *geoip.Result, geography, network bool) {
	if result == nil {
		return
	}
	if geography {
		out.Country = first(result.CountryName, result.Country)
		out.City = result.City
	}
	if network {
		out.ASN = result.ASN
		out.Provider = result.ASOrg
	}
}

func (e *Enricher) setLocalDatabases(dbs *LocalDatabases) {
	e.sourceMu.Lock()
	e.localDBs = dbs
	if dbs != nil {
		e.startupWarnings = append(e.startupWarnings, dbs.warnings...)
	}
	e.sourceMu.Unlock()
}

// StartupWarnings returns one sanitized, endpoint-free diagnostic per failed
// startup source check.
func (e *Enricher) StartupWarnings() []string {
	e.sourceMu.RLock()
	defer e.sourceMu.RUnlock()
	return append([]string(nil), e.startupWarnings...)
}

func (e *Enricher) SourceSummary() string {
	e.sourceMu.RLock()
	defer e.sourceMu.RUnlock()
	chain, details := e.sourceStatus()
	return chain + "; " + strings.Join(details, " | ")
}

// SourceStatusLines returns endpoint-free status lines without splitting a
// source label across columns. Narrow displays wrap whole source states.
func (e *Enricher) SourceStatusLines(width int) []string {
	e.sourceMu.RLock()
	defer e.sourceMu.RUnlock()
	chain, details := e.sourceStatus()
	lines := []string{Truncate(chain, width)}
	current := ""
	for _, detail := range details {
		candidate := detail
		if current != "" {
			candidate = current + " | " + detail
		}
		if current != "" && displayWidth(candidate) > width {
			lines = append(lines, Truncate(current, width))
			current = detail
		} else {
			current = candidate
		}
	}
	if current != "" {
		lines = append(lines, Truncate(current, width))
	}
	return lines
}

func (e *Enricher) sourceStatus() (string, []string) {
	localState, localReady := e.localDBs.status()
	monitorReady := e.cfg.ServerURL != ""
	publicReady := !e.cfg.DisablePublic
	chain := make([]string, 0, 3)
	if localReady {
		chain = append(chain, "local MMDB")
	}
	if monitorReady {
		chain = append(chain, "monitor")
	}
	if publicReady {
		chain = append(chain, "public")
	}
	chainSummary := "enrichment: none"
	if len(chain) == 1 && chain[0] == "public" {
		chainSummary = "enrichment: public fallback"
	} else if len(chain) > 0 {
		chainSummary = "enrichment: " + strings.Join(chain, " -> ")
	}
	publicState := "enabled"
	if !publicReady {
		publicState = "disabled by flag"
	}
	return chainSummary, []string{
		"local MMDB: " + localState,
		"monitor: " + e.monitorState,
		"public: " + publicState,
	}
}

// Lookup returns cached data immediately and schedules at most one bounded
// background lookup. Completed entries are evicted FIFO when CacheSize is
// reached; in-flight entries are never evicted.
func (e *Enricher) Lookup(ip string) Enrichment {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return Enrichment{Err: "invalid IP"}
	}
	e.mu.RLock()
	entry, ok := e.cache[ip]
	e.mu.RUnlock()
	if ok {
		return entry.value
	}

	value := e.local(ip)
	canMonitor := e.cfg.ServerURL != ""
	canPublic := !e.cfg.DisablePublic && publicEligible(parsed)
	if !needsFallback(value) || (!canMonitor && !canPublic) {
		e.storeCompleted(ip, value)
		return value
	}
	if !e.enqueue(ip, value) {
		value.Err = "enrichment queue or cache full"
	}
	return value
}

func needsFallback(value Enrichment) bool {
	return value.Hostname == "" || value.Country == "" || value.City == "" ||
		value.Provider == "" || value.ASN == 0
}

func (e *Enricher) enqueue(ip string, value Enrichment) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return false
	}
	if _, ok := e.cache[ip]; ok {
		return true
	}
	if !e.makeCacheRoomLocked() {
		return false
	}
	e.cache[ip] = cacheEntry{value: value}
	e.order = append(e.order, ip)
	select {
	case e.jobs <- ip:
		return true
	default:
		delete(e.cache, ip)
		e.order = e.order[:len(e.order)-1]
		return false
	}
}

func (e *Enricher) storeCompleted(ip string, value Enrichment) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.cache[ip]; ok {
		return
	}
	if !e.makeCacheRoomLocked() {
		return
	}
	e.cache[ip] = cacheEntry{value: value, done: true}
	e.order = append(e.order, ip)
}

func (e *Enricher) makeCacheRoomLocked() bool {
	for len(e.cache) >= e.cfg.CacheSize {
		evicted := false
		for i, ip := range e.order {
			if entry, ok := e.cache[ip]; ok && entry.done {
				delete(e.cache, ip)
				e.order = append(e.order[:i], e.order[i+1:]...)
				evicted = true
				break
			}
		}
		if !evicted {
			return false
		}
	}
	return true
}

func (e *Enricher) worker() {
	defer e.workers.Done()
	for {
		select {
		case <-e.stopCh:
			return
		default:
		}
		select {
		case ip := <-e.jobs:
			select {
			case <-e.stopCh:
				return
			default:
			}
			e.mu.Lock()
			e.inFlight++
			if e.inFlight > e.maxSeen {
				e.maxSeen = e.inFlight
			}
			e.mu.Unlock()
			e.resolve(ip)
			e.mu.Lock()
			e.inFlight--
			e.mu.Unlock()
		case <-e.stopCh:
			return
		}
	}
}

func (e *Enricher) resolve(ip string) {
	e.mu.RLock()
	entry, ok := e.cache[ip]
	e.mu.RUnlock()
	if !ok {
		return
	}
	out := entry.value
	var errs []string
	if e.cfg.ServerURL != "" && needsFallback(out) {
		value, err := e.fetch(ip, e.cfg.ServerURL, true)
		if err != nil {
			errs = append(errs, "monitor: "+err.Error())
		} else {
			merge(&out, value, "monitor")
		}
	}
	if !e.cfg.DisablePublic && publicEligible(net.ParseIP(ip)) && needsFallback(out) {
		value, err := e.fetch(ip, e.cfg.PublicURL, false)
		if err != nil {
			errs = append(errs, "public: "+err.Error())
		} else {
			merge(&out, value, "public")
		}
	}
	if len(errs) > 0 {
		out.Err = strings.Join(errs, "; ")
	}
	e.mu.Lock()
	if _, ok := e.cache[ip]; ok {
		e.cache[ip] = cacheEntry{value: out, done: true}
	}
	e.mu.Unlock()
}

func merge(dst *Enrichment, src Enrichment, source string) {
	used := false
	if dst.Hostname == "" && src.Hostname != "" {
		dst.Hostname, used = src.Hostname, true
	}
	if dst.Country == "" && src.Country != "" {
		dst.Country, used = src.Country, true
	}
	if dst.City == "" && src.City != "" {
		dst.City, used = src.City, true
	}
	if dst.Provider == "" && src.Provider != "" {
		dst.Provider, used = src.Provider, true
	}
	if dst.ASN == 0 && src.ASN != 0 {
		dst.ASN, used = src.ASN, true
	}
	if used {
		if dst.Source == "" {
			dst.Source = source
		} else if !strings.Contains(dst.Source, source) {
			dst.Source += "+" + source
		}
	}
}

type response struct {
	IP          string          `json:"ip"`
	Hostname    string          `json:"hostname"`
	Country     string          `json:"country"`
	CountryName string          `json:"country_name"`
	City        string          `json:"city"`
	Provider    string          `json:"provider"`
	ASOrg       string          `json:"as_org"`
	ASN         json.RawMessage `json:"asn"`
}

func (e *Enricher) fetch(ip, base string, monitor bool) (Enrichment, error) {
	u, _ := url.Parse(base)
	if monitor {
		u.Path = strings.TrimRight(u.Path, "/") + "/api/host"
	}
	q := u.Query()
	q.Set("ip", ip)
	u.RawQuery = q.Encode()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if !monitor {
		if err := validatePublicDestination(ctx, u, e.cfg.AllowHTTP, e.lookupIP); err != nil {
			return Enrichment{}, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Enrichment{}, err
	}
	req.Header.Set("User-Agent", e.userAgent)
	client := e.publicClient
	if monitor {
		client = e.monitorClient
	}
	respHTTP, err := client.Do(req)
	if err != nil {
		return Enrichment{}, err
	}
	defer respHTTP.Body.Close()
	if respHTTP.StatusCode != http.StatusOK {
		return Enrichment{}, fmt.Errorf("HTTP %d", respHTTP.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(respHTTP.Body, maxResponseBody+1))
	if err != nil {
		return Enrichment{}, err
	}
	if len(body) > maxResponseBody {
		return Enrichment{}, errors.New("response body too large")
	}
	var wire response
	if err := json.Unmarshal(body, &wire); err != nil {
		return Enrichment{}, fmt.Errorf("invalid JSON: %w", err)
	}
	echoed := net.ParseIP(wire.IP)
	if echoed == nil || !echoed.Equal(net.ParseIP(ip)) {
		return Enrichment{}, errors.New("response IP mismatch")
	}
	asn, err := parseASN(wire.ASN)
	if err != nil {
		return Enrichment{}, err
	}
	return Enrichment{
		Hostname: wire.Hostname,
		Country:  first(wire.CountryName, wire.Country),
		City:     wire.City,
		Provider: first(wire.ASOrg, wire.Provider),
		ASN:      asn,
	}, nil
}

func parseASN(raw json.RawMessage) (uint, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	var text string
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &text); err != nil {
			return 0, errors.New("invalid ASN")
		}
	} else {
		text = string(raw)
	}
	n, err := strconv.ParseUint(text, 10, 32)
	if err != nil {
		return 0, errors.New("invalid ASN")
	}
	return uint(n), nil
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// Wait blocks until all currently scheduled lookups complete.
func (e *Enricher) Wait() {
	for {
		e.mu.RLock()
		if e.closed {
			e.mu.RUnlock()
			return
		}
		pending := false
		for _, entry := range e.cache {
			if !entry.done {
				pending = true
				break
			}
		}
		e.mu.RUnlock()
		if !pending {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func (e *Enricher) Close() {
	e.closeOnce.Do(func() {
		e.mu.Lock()
		e.closed = true
		e.mu.Unlock()
		close(e.stopCh)
		e.workers.Wait()
		e.sourceMu.Lock()
		if e.localDBs != nil {
			e.localDBs.Close()
		}
		e.sourceMu.Unlock()
		e.mu.Lock()
		for ip, entry := range e.cache {
			if !entry.done {
				entry.done = true
				entry.value.Err = "enrichment stopped"
				e.cache[ip] = entry
			}
		}
		e.mu.Unlock()
	})
}
