package adguard

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"bandwidth-monitor/dns"
	"bandwidth-monitor/httputil"
	"bandwidth-monitor/poller"
)

// Client polls ADGuard Home's REST API for DNS statistics.
type Client struct {
	baseURL    string
	user       string
	pass       string
	interval   time.Duration
	httpClient *http.Client

	mu    sync.RWMutex
	stats *Stats

	poller.Runner
}

// Stats holds the latest snapshot from AdGuard Home /control/stats.
type Stats struct {
	NumDNSQueries        int     `json:"num_dns_queries"`
	NumBlockedFiltering  int     `json:"num_blocked_filtering"`
	NumReplacedSafebrows int     `json:"num_replaced_safebrowsing"`
	NumReplacedParental  int     `json:"num_replaced_parental"`
	NumReplacedSafesrch  int     `json:"num_replaced_safesearch"`
	AvgProcessingTime    float64 `json:"avg_processing_time"`

	TopQueriedDomains []map[string]float64 `json:"top_queried_domains"`
	TopBlockedDomains []map[string]float64 `json:"top_blocked_domains"`
	TopClients        []map[string]float64 `json:"top_clients"`

	TopUpstreamsResponses []map[string]float64 `json:"top_upstreams_responses"`
	TopUpstreamsAvgTime   []map[string]float64 `json:"top_upstreams_avg_time"`

	// Time-series arrays (one entry per time unit)
	DNSQueries       []int `json:"dns_queries"`
	BlockedFiltering []int `json:"blocked_filtering"`

	TimeUnits string `json:"time_units"`
}

// New creates an AdGuard Home API client.
// baseURL should be like "http://adguard.example.local" (no trailing slash).
func New(baseURL, user, pass string, pollInterval time.Duration) *Client {
	c := &Client{
		baseURL:    baseURL,
		user:       user,
		pass:       pass,
		interval:   pollInterval,
		httpClient: &http.Client{Timeout: 10 * time.Second, Transport: httputil.WrapTransport(nil)},
	}
	c.Runner.Init()
	return c
}

// Run starts the polling loop. Call in a goroutine.
func (c *Client) Run() { c.Runner.Run(c.interval, c.poll) }

func (c *Client) poll() {
	url := c.baseURL + "/control/stats"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Printf("adguard: build request: %v", err)
		return
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Printf("adguard: fetch stats: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("adguard: unexpected status %d: %s", resp.StatusCode, string(body))
		return
	}

	var s Stats
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		log.Printf("adguard: decode stats: %v", err)
		return
	}

	c.mu.Lock()
	c.stats = &s
	c.mu.Unlock()
}

// GetSummary returns a frontend-friendly summary, or nil if no data yet.
func (c *Client) GetSummary() *dns.Summary {
	c.mu.RLock()
	s := c.stats
	c.mu.RUnlock()
	if s == nil {
		return nil
	}

	blockedTotal := s.NumBlockedFiltering + s.NumReplacedSafebrows + s.NumReplacedParental + s.NumReplacedSafesrch
	blockedPct := 0.0
	if s.NumDNSQueries > 0 {
		blockedPct = float64(blockedTotal) / float64(s.NumDNSQueries) * 100
	}

	sum := &dns.Summary{
		ProviderName:   "AdGuard Home",
		TotalQueries:   s.NumDNSQueries,
		BlockedTotal:   blockedTotal,
		BlockedPercent: blockedPct,
		AvgLatencyMs:   s.AvgProcessingTime * 1000,
		TopQueried:     parseDomainEntries(s.TopQueriedDomains, 10),
		TopBlocked:     parseDomainEntries(s.TopBlockedDomains, 10),
		TopClients:     parseClientEntries(s.TopClients, 10),
		Upstreams:      buildUpstreams(s.TopUpstreamsResponses, s.TopUpstreamsAvgTime),
		QueriesSeries:  s.DNSQueries,
		BlockedSeries:  s.BlockedFiltering,
		TimeUnits:      s.TimeUnits,
	}
	return sum
}

// Available returns true if the client has successfully fetched data at least once.
func (c *Client) Available() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.stats != nil
}

// queryLogResponse mirrors the subset of AdGuard Home's /control/querylog
// response we care about.
type queryLogResponse struct {
	Data []struct {
		Time     string `json:"time"`
		Client   string `json:"client"`
		Reason   string `json:"reason"`
		Status   string `json:"status"`
		ElapsedM string `json:"elapsedMs"`
		Question struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"question"`
	} `json:"data"`
}

// QueryLog returns the most recent DNS queries made by clientIP, newest
// first. It implements dns.ClientQueryLogger. AdGuard Home's "search" query
// param matches either a domain name or a client IP, so it's constrained
// here to an exact client match to avoid pulling in unrelated domain hits.
func (c *Client) QueryLog(clientIP string, limit int) ([]dns.QueryLogEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	url := fmt.Sprintf("%s/control/querylog?search=%s&limit=%d", c.baseURL, clientIP, limit)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("adguard: querylog status %d: %s", resp.StatusCode, string(body))
	}

	var qlr queryLogResponse
	if err := json.NewDecoder(resp.Body).Decode(&qlr); err != nil {
		return nil, err
	}

	out := make([]dns.QueryLogEntry, 0, len(qlr.Data))
	for _, e := range qlr.Data {
		// The search param can also match on domain name, so double-check
		// the client IP to keep results scoped to this one client.
		if e.Client != clientIP {
			continue
		}
		elapsed := 0.0
		fmt.Sscanf(e.ElapsedM, "%f", &elapsed)
		out = append(out, dns.QueryLogEntry{
			Time:      e.Time,
			Domain:    e.Question.Name,
			Type:      e.Question.Type,
			Blocked:   strings.HasPrefix(e.Reason, "Filtered"),
			Reason:    e.Reason,
			ElapsedMs: elapsed,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func parseDomainEntries(raw []map[string]float64, limit int) []dns.DomainStat {
	type kv struct {
		k string
		v int
	}
	var list []kv
	for _, m := range raw {
		for k, v := range m {
			list = append(list, kv{k, int(v)})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].v > list[j].v })
	if len(list) > limit {
		list = list[:limit]
	}
	out := make([]dns.DomainStat, len(list))
	for i, e := range list {
		out[i] = dns.DomainStat{Domain: e.k, Count: e.v}
	}
	return out
}

func parseClientEntries(raw []map[string]float64, limit int) []dns.ClientStat {
	type kv struct {
		k string
		v int
	}
	var list []kv
	for _, m := range raw {
		for k, v := range m {
			list = append(list, kv{k, int(v)})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].v > list[j].v })
	if len(list) > limit {
		list = list[:limit]
	}
	out := make([]dns.ClientStat, len(list))
	for i, e := range list {
		out[i] = dns.ClientStat{IP: e.k, Count: e.v}
	}
	return out
}

// buildUpstreams merges response counts and average latencies per upstream.
func buildUpstreams(respEntries, avgEntries []map[string]float64) []dns.UpstreamStat {
	respMap := make(map[string]int)
	for _, m := range respEntries {
		for k, v := range m {
			respMap[k] = int(v)
		}
	}
	avgMap := make(map[string]float64)
	for _, m := range avgEntries {
		for k, v := range m {
			avgMap[k] = v * 1000 // seconds → ms
		}
	}
	// Collect all keys
	seen := make(map[string]bool)
	var keys []string
	for k := range respMap {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for k := range avgMap {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return respMap[keys[i]] > respMap[keys[j]] })
	out := make([]dns.UpstreamStat, len(keys))
	for i, k := range keys {
		out[i] = dns.UpstreamStat{Address: k, Responses: respMap[k], AvgMs: avgMap[k]}
	}
	return out
}

// String returns a debug string.
func (c *Client) String() string {
	return fmt.Sprintf("AdGuard[%s]", c.baseURL)
}
