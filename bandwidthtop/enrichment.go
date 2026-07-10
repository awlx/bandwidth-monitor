package bandwidthtop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
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
	GeoDB         *geoip.DB
	ServerURL     string
	PublicURL     string
	HTTPClient    *http.Client
	Concurrency   int
	DisablePublic bool
	AllowHTTP     bool
}

type cacheEntry struct {
	value Enrichment
	done  bool
}

type Enricher struct {
	cfg      Config
	mu       sync.RWMutex
	cache    map[string]cacheEntry
	sem      chan struct{}
	inFlight int
	maxSeen  int
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
		isPublic := i == 1
		if isPublic && !publicURLAllowed(u, cfg.AllowHTTP) {
			return nil, fmt.Errorf("enrichment URL must use HTTPS")
		}
		if !isPublic && u.Scheme != "https" && u.Scheme != "http" {
			return nil, fmt.Errorf("monitor URL must use HTTP or HTTPS")
		}
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 2 * time.Second}
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	return &Enricher{cfg: cfg, cache: make(map[string]cacheEntry), sem: make(chan struct{}, cfg.Concurrency)}, nil
}

func publicURLAllowed(u *url.URL, allowHTTP bool) bool {
	return u.Scheme == "https" ||
		(allowHTTP && u.Scheme == "http" && isLoopbackHost(u.Hostname()))
}

func isLoopbackHost(host string) bool {
	ip := net.ParseIP(host)
	return strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
}

// Lookup returns cached data immediately and starts one bounded background
// lookup when remote fields are still missing.
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
	if !publicEligible(parsed) || (value.ASN != 0 && value.Provider != "") {
		e.mu.Lock()
		e.cache[ip] = cacheEntry{value: value, done: true}
		e.mu.Unlock()
		return value
	}

	e.mu.Lock()
	if entry, ok = e.cache[ip]; !ok {
		e.cache[ip] = cacheEntry{value: value}
		go e.resolve(ip)
	}
	e.mu.Unlock()
	return value
}

var nonPublicRanges = func() []*net.IPNet {
	cidrs := []string{
		"0.0.0.0/8", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
		"2001:db8::/32", "2001:10::/28", "ff00::/8",
	}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, network, _ := net.ParseCIDR(cidr)
		out = append(out, network)
	}
	return out
}()

func publicEligible(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	for _, network := range nonPublicRanges {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func (e *Enricher) local(ip string) Enrichment {
	var out Enrichment
	if e.cfg.GeoDB == nil {
		return out
	}
	if r := e.cfg.GeoDB.Lookup(ip); r != nil {
		out.Country, out.City, out.ASN, out.Provider = first(r.CountryName, r.Country), r.City, r.ASN, r.ASOrg
		if out.Country != "" || out.ASN != 0 || out.Provider != "" {
			out.Source = "local"
		}
	}
	return out
}

func (e *Enricher) resolve(ip string) {
	e.sem <- struct{}{}
	e.mu.Lock()
	e.inFlight++
	if e.inFlight > e.maxSeen {
		e.maxSeen = e.inFlight
	}
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.inFlight--
		e.mu.Unlock()
		<-e.sem
	}()

	e.mu.RLock()
	out := e.cache[ip].value
	e.mu.RUnlock()
	var errs []string
	if e.cfg.ServerURL != "" && (out.ASN == 0 || out.Provider == "") {
		v, err := e.fetch(ip, e.cfg.ServerURL, true)
		if err != nil {
			errs = append(errs, "monitor: "+err.Error())
		} else {
			merge(&out, v, "monitor")
		}
	}
	if !e.cfg.DisablePublic && (out.ASN == 0 || out.Provider == "") {
		v, err := e.fetch(ip, e.cfg.PublicURL, false)
		if err != nil {
			errs = append(errs, "public: "+err.Error())
		} else {
			merge(&out, v, "public")
		}
	}
	if len(errs) > 0 {
		out.Err = strings.Join(errs, "; ")
	}
	e.mu.Lock()
	e.cache[ip] = cacheEntry{value: out, done: true}
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
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	respHTTP, err := e.cfg.HTTPClient.Do(req)
	if err != nil {
		return Enrichment{}, err
	}
	defer respHTTP.Body.Close()
	if !monitor && !publicURLAllowed(respHTTP.Request.URL, e.cfg.AllowHTTP) {
		return Enrichment{}, errors.New("public enrichment redirected away from HTTPS")
	}
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
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// Wait blocks until all currently scheduled lookups complete.
func (e *Enricher) Wait() {
	for {
		e.mu.RLock()
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
