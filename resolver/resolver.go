// Package resolver provides a shared reverse-DNS resolver with TTL-based
// caching and bounded concurrency.  Both the conntrack and talkers packages
// (and anything else that needs IP-to-hostname lookups) should use a single
// shared instance so that cache entries are not duplicated and TTL expiry
// is handled consistently.
//
// PTR queries are performed via miekg/dns so that the real DNS TTL from the
// response is used for cache expiry rather than a hardcoded value.
package resolver

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"bandwidth-monitor/poller"

	mdns "github.com/miekg/dns"
)

// Default tuning knobs.
const (
	DefaultNegTTL     = 1 * time.Minute
	DefaultMinTTL     = 10 * time.Second // floor so we don't hammer the resolver
	DefaultMaxTTL     = 30 * time.Minute // cap for sanity
	DefaultTimeout    = 500 * time.Millisecond
	DefaultConcurrent = 16
)

// cacheEntry stores a resolved hostname with an expiry timestamp.
type cacheEntry struct {
	hostname string
	expires  time.Time
}

// Resolver performs reverse-DNS lookups with a TTL-based cache and bounded
// concurrency.
type Resolver struct {
	mu      sync.RWMutex
	cache   map[string]cacheEntry
	enabled bool
	sem     chan struct{} // limits concurrent lookups
	negTTL  time.Duration
	minTTL  time.Duration
	maxTTL  time.Duration
	timeout time.Duration
	server  string // DNS server address (host:port)
	poller.Runner
}

// New creates a Resolver that reads /etc/resolv.conf for the system resolver.
// Falls back to 127.0.0.1:53 if the config cannot be read.
func New() *Resolver {
	server := "127.0.0.1:53"
	if config, err := mdns.ClientConfigFromFile("/etc/resolv.conf"); err == nil && len(config.Servers) > 0 {
		server = net.JoinHostPort(config.Servers[0], config.Port)
	} else {
		log.Printf("resolver: cannot read /etc/resolv.conf, falling back to %s", server)
	}

	r := &Resolver{
		cache:   make(map[string]cacheEntry, 256),
		enabled: true,
		sem:     make(chan struct{}, DefaultConcurrent),
		negTTL:  DefaultNegTTL,
		minTTL:  DefaultMinTTL,
		maxTTL:  DefaultMaxTTL,
		timeout: DefaultTimeout,
		server:  server,
	}
	r.Runner.Init()
	go r.pruneLoop()
	return r
}

// Stop terminates the cache pruning goroutine.
func (r *Resolver) Stop() { r.Runner.Stop() }

// SetEnabled controls whether new PTR lookups may start. Cached entries and
// the pruning goroutine are retained while lookups are disabled.
func (r *Resolver) SetEnabled(enabled bool) {
	r.mu.Lock()
	r.enabled = enabled
	r.mu.Unlock()
}

// Enabled reports whether new PTR lookups may start.
func (r *Resolver) Enabled() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.enabled
}

const pruneInterval = 5 * time.Minute

// pruneLoop periodically removes expired entries from the cache.
func (r *Resolver) pruneLoop() {
	r.Runner.Run(pruneInterval, func() {
		now := time.Now()
		r.mu.Lock()
		for ip, entry := range r.cache {
			if now.After(entry.expires) {
				delete(r.cache, ip)
			}
		}
		r.mu.Unlock()
	})
}

// LookupAddr performs a synchronous reverse-DNS lookup for ip, returning the
// resolved hostname or "" on failure.  Results are cached using the real DNS
// TTL from the response.
func (r *Resolver) LookupAddr(ip string) string {
	// Fast path: check cache under read lock.
	now := time.Now()
	r.mu.RLock()
	if !r.enabled {
		r.mu.RUnlock()
		return ""
	}
	entry, ok := r.cache[ip]
	r.mu.RUnlock()
	if ok && now.Before(entry.expires) {
		return entry.hostname
	}

	return r.resolve(ip)
}

// LookupAddrFresh always performs a fresh reverse-DNS lookup, bypassing the
// cache.  The result is still stored in the cache so subsequent LookupAddr
// calls benefit from it.
func (r *Resolver) LookupAddrFresh(ip string) string {
	if !r.Enabled() {
		return ""
	}
	return r.resolve(ip)
}

// ptrName converts an IP address (v4 or v6) to its reverse-DNS PTR name.
func ptrName(ip string) (string, error) {
	addr := net.ParseIP(ip)
	if addr == nil {
		return "", fmt.Errorf("invalid IP: %s", ip)
	}

	arpa, err := mdns.ReverseAddr(ip)
	if err != nil {
		return "", err
	}
	return arpa, nil
}

// resolve does the actual PTR lookup via miekg/dns and caches with the real TTL.
func (r *Resolver) resolve(ip string) string {
	now := time.Now()
	r.mu.RLock()
	enabled := r.enabled
	r.mu.RUnlock()
	if !enabled {
		return ""
	}

	ptr, err := ptrName(ip)
	if err != nil {
		// Invalid IP — cache as negative.
		r.mu.Lock()
		r.cache[ip] = cacheEntry{hostname: "", expires: now.Add(r.negTTL)}
		r.mu.Unlock()
		return ""
	}

	msg := new(mdns.Msg)
	msg.SetQuestion(ptr, mdns.TypePTR)
	msg.RecursionDesired = true

	client := &mdns.Client{Timeout: r.timeout}
	resp, _, err := client.Exchange(msg, r.server)

	if err != nil || resp == nil || resp.Rcode != mdns.RcodeSuccess || len(resp.Answer) == 0 {
		// Negative result — cache with negTTL.
		r.mu.Lock()
		r.cache[ip] = cacheEntry{hostname: "", expires: now.Add(r.negTTL)}
		r.mu.Unlock()
		return ""
	}

	// Extract hostname and TTL from the first PTR record.
	var hostname string
	var ttl uint32
	for _, rr := range resp.Answer {
		if p, ok := rr.(*mdns.PTR); ok {
			hostname = strings.TrimSuffix(p.Ptr, ".")
			ttl = rr.Header().Ttl
			break
		}
	}

	if hostname == "" {
		r.mu.Lock()
		r.cache[ip] = cacheEntry{hostname: "", expires: now.Add(r.negTTL)}
		r.mu.Unlock()
		return ""
	}

	// Clamp the TTL to [minTTL, maxTTL].
	cacheTTL := time.Duration(ttl) * time.Second
	if cacheTTL < r.minTTL {
		cacheTTL = r.minTTL
	}
	if cacheTTL > r.maxTTL {
		cacheTTL = r.maxTTL
	}

	r.mu.Lock()
	r.cache[ip] = cacheEntry{hostname: hostname, expires: now.Add(cacheTTL)}
	r.mu.Unlock()

	return hostname
}

// LookupAddrAsync returns the cached hostname for ip immediately.  If there
// is no cached result (or it has expired), it returns the raw IP and kicks off
// a background lookup so the next call will have the resolved name.
//
// This is the preferred method for hot paths (e.g. per-second SSE payloads)
// where blocking on DNS is undesirable.
func (r *Resolver) LookupAddrAsync(ip string) string {
	now := time.Now()
	r.mu.RLock()
	if !r.enabled {
		r.mu.RUnlock()
		return ip
	}
	entry, ok := r.cache[ip]
	r.mu.RUnlock()
	if ok && now.Before(entry.expires) {
		if entry.hostname != "" {
			return entry.hostname
		}
		return ip // negative cache hit — don't re-trigger
	}

	// Store the IP as a placeholder so concurrent calls don't all trigger
	// lookups for the same address.
	r.mu.Lock()
	// Double-check after write lock.
	if !r.enabled {
		r.mu.Unlock()
		return ip
	}
	if entry, ok := r.cache[ip]; ok && now.Before(entry.expires) {
		r.mu.Unlock()
		if entry.hostname != "" {
			return entry.hostname
		}
		return ip
	}
	r.cache[ip] = cacheEntry{hostname: "", expires: now.Add(r.negTTL)}
	r.mu.Unlock()

	// Fire-and-forget with bounded concurrency.
	select {
	case r.sem <- struct{}{}:
		go func() {
			defer func() { <-r.sem }()
			r.resolve(ip)
		}()
	default:
		// Semaphore full — skip, will retry on next call.
	}

	return ip
}
