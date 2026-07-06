package pihole

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"bandwidth-monitor/dns"
	"bandwidth-monitor/httputil"
	"bandwidth-monitor/poller"
)

// Client polls a Pi-hole v6 instance for DNS statistics.
type Client struct {
	baseURL    string
	password   string
	interval   time.Duration
	httpClient *http.Client

	mu    sync.RWMutex
	stats *snapshot

	// session management
	sid       string
	sidExpiry time.Time

	poller.Runner
}

// snapshot holds the combined data from multiple Pi-hole API calls.
type snapshot struct {
	summary *summaryResp
	topQ    *topDomainsResp
	topB    *topDomainsResp
	topC    *topClientsResp
	upstr   *upstreamsResp
	history *historyResp
}

// ── Pi-hole v6 API response types ──

type authReq struct {
	Password string `json:"password"`
}

type authResp struct {
	Session struct {
		Valid    bool    `json:"valid"`
		SID      string  `json:"sid"`
		Validity float64 `json:"validity"`
	} `json:"session"`
}

type summaryResp struct {
	Queries struct {
		Total          int     `json:"total"`
		Blocked        int     `json:"blocked"`
		PercentBlocked float64 `json:"percent_blocked"`
		Forwarded      int     `json:"forwarded"`
		Cached         int     `json:"cached"`
	} `json:"queries"`
	Took float64 `json:"took"`
}

type topDomainsResp struct {
	Domains []struct {
		Domain string `json:"domain"`
		Count  int    `json:"count"`
	} `json:"domains"`
	Took float64 `json:"took"`
}

type topClientsResp struct {
	Clients []struct {
		IP    string `json:"ip"`
		Name  string `json:"name"`
		Count int    `json:"count"`
	} `json:"clients"`
	Took float64 `json:"took"`
}

type upstreamsResp struct {
	Upstreams []struct {
		IP         string `json:"ip"`
		Name       string `json:"name"`
		Port       int    `json:"port"`
		Count      int    `json:"count"`
		Statistics struct {
			Response float64 `json:"response"`
		} `json:"statistics"`
	} `json:"upstreams"`
	Took float64 `json:"took"`
}

type historyResp struct {
	History []struct {
		Timestamp float64 `json:"timestamp"`
		Total     int     `json:"total"`
		Blocked   int     `json:"blocked"`
	} `json:"history"`
	Took float64 `json:"took"`
}

// New creates a Pi-hole v6 API client.
// baseURL should be like "http://pi.hole" or "https://192.168.1.2" (no trailing slash).
func New(baseURL, password string, pollInterval time.Duration) *Client {
	c := &Client{
		baseURL:    baseURL,
		password:   password,
		interval:   pollInterval,
		httpClient: httputil.NewInsecureClient(15 * time.Second),
	}
	c.Runner.Init()
	return c
}

// Run starts the polling loop. Call in a goroutine.
func (c *Client) Run() { c.Runner.Run(c.interval, c.poll) }

// authenticate obtains (or reuses) a session ID from the Pi-hole API.
func (c *Client) authenticate() (string, error) {
	c.mu.RLock()
	if c.sid != "" && time.Now().Before(c.sidExpiry.Add(-30*time.Second)) {
		sid := c.sid
		c.mu.RUnlock()
		return sid, nil
	}
	c.mu.RUnlock()

	body, _ := json.Marshal(authReq{Password: c.password})
	resp, err := c.httpClient.Post(c.baseURL+"/api/auth", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("auth request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("auth failed (HTTP %d): %s", resp.StatusCode, string(b))
	}

	var ar authResp
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return "", fmt.Errorf("auth decode: %w", err)
	}
	if !ar.Session.Valid {
		return "", fmt.Errorf("auth: session not valid (wrong password?)")
	}

	c.mu.Lock()
	c.sid = ar.Session.SID
	c.sidExpiry = time.Now().Add(time.Duration(ar.Session.Validity) * time.Second)
	sid := c.sid
	c.mu.Unlock()
	return sid, nil
}

func (c *Client) apiGet(path string, target interface{}) error {
	sid, err := c.authenticate()
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/api%s", c.baseURL, path)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-FTL-SID", sid)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", path, err)
	}
	defer resp.Body.Close()

	// If we get 401, invalidate the SID and retry once
	if resp.StatusCode == http.StatusUnauthorized {
		c.mu.Lock()
		c.sid = ""
		c.mu.Unlock()
		sid, err = c.authenticate()
		if err != nil {
			return fmt.Errorf("re-auth: %w", err)
		}
		req2, _ := http.NewRequest("GET", url, nil)
		req2.Header.Set("X-FTL-SID", sid)
		resp2, err := c.httpClient.Do(req2)
		if err != nil {
			return fmt.Errorf("retry %s: %w", path, err)
		}
		defer resp2.Body.Close()
		if resp2.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp2.Body)
			return fmt.Errorf("%s returned %d: %s", path, resp2.StatusCode, string(b))
		}
		return json.NewDecoder(resp2.Body).Decode(target)
	}

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s returned %d: %s", path, resp.StatusCode, string(b))
	}

	return json.NewDecoder(resp.Body).Decode(target)
}

func (c *Client) poll() {
	snap := &snapshot{}

	summary := &summaryResp{}
	if err := c.apiGet("/stats/summary", summary); err != nil {
		log.Printf("pihole: stats/summary: %v", err)
		return
	}
	snap.summary = summary

	topQ := &topDomainsResp{}
	if err := c.apiGet("/stats/top_domains?count=10", topQ); err != nil {
		log.Printf("pihole: top_domains: %v", err)
		return
	}
	snap.topQ = topQ

	topB := &topDomainsResp{}
	if err := c.apiGet("/stats/top_domains?blocked=true&count=10", topB); err != nil {
		log.Printf("pihole: top_domains(blocked): %v", err)
		return
	}
	snap.topB = topB

	topC := &topClientsResp{}
	if err := c.apiGet("/stats/top_clients?count=10", topC); err != nil {
		log.Printf("pihole: top_clients: %v", err)
		return
	}
	snap.topC = topC

	upstr := &upstreamsResp{}
	if err := c.apiGet("/stats/upstreams", upstr); err != nil {
		log.Printf("pihole: upstreams: %v", err)
		return
	}
	snap.upstr = upstr

	hist := &historyResp{}
	if err := c.apiGet("/history", hist); err != nil {
		log.Printf("pihole: history: %v", err)
		return
	}
	snap.history = hist

	c.mu.Lock()
	c.stats = snap
	c.mu.Unlock()
}

// GetSummary returns a frontend-friendly summary, or nil if no data yet.
func (c *Client) GetSummary() *dns.Summary {
	c.mu.RLock()
	snap := c.stats
	c.mu.RUnlock()
	if snap == nil {
		return nil
	}

	s := snap.summary

	sum := &dns.Summary{
		ProviderName:   "Pi-hole",
		TotalQueries:   s.Queries.Total,
		BlockedTotal:   s.Queries.Blocked,
		BlockedPercent: s.Queries.PercentBlocked,
		AvgLatencyMs:   0,
		TimeUnits:      "hours",
	}

	// Top queried domains
	if snap.topQ != nil {
		sum.TopQueried = make([]dns.DomainStat, len(snap.topQ.Domains))
		for i, d := range snap.topQ.Domains {
			sum.TopQueried[i] = dns.DomainStat{Domain: d.Domain, Count: d.Count}
		}
	}

	// Top blocked domains
	if snap.topB != nil {
		sum.TopBlocked = make([]dns.DomainStat, len(snap.topB.Domains))
		for i, d := range snap.topB.Domains {
			sum.TopBlocked[i] = dns.DomainStat{Domain: d.Domain, Count: d.Count}
		}
	}

	// Top clients
	if snap.topC != nil {
		sum.TopClients = make([]dns.ClientStat, len(snap.topC.Clients))
		for i, cl := range snap.topC.Clients {
			ip := cl.IP
			if cl.Name != "" {
				ip = cl.Name + " (" + cl.IP + ")"
			}
			sum.TopClients[i] = dns.ClientStat{IP: ip, Count: cl.Count}
		}
	}

	// Upstreams
	if snap.upstr != nil {
		upstreams := make([]dns.UpstreamStat, 0, len(snap.upstr.Upstreams))
		for _, u := range snap.upstr.Upstreams {
			addr := u.IP
			if u.Name != "" {
				addr = u.Name
			}
			if u.Port > 0 {
				addr = fmt.Sprintf("%s#%d", addr, u.Port)
			}
			upstreams = append(upstreams, dns.UpstreamStat{
				Address:   addr,
				Responses: u.Count,
				AvgMs:     u.Statistics.Response * 1000,
			})
		}
		sort.Slice(upstreams, func(i, j int) bool { return upstreams[i].Responses > upstreams[j].Responses })
		sum.Upstreams = upstreams
	}

	// History time series
	if snap.history != nil && len(snap.history.History) > 0 {
		sum.QueriesSeries = make([]int, len(snap.history.History))
		sum.BlockedSeries = make([]int, len(snap.history.History))
		for i, h := range snap.history.History {
			sum.QueriesSeries[i] = h.Total
			sum.BlockedSeries[i] = h.Blocked
		}
	}

	return sum
}

// Available returns true if the client has successfully fetched data at least once.
func (c *Client) Available() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.stats != nil
}

// String returns a debug string.
func (c *Client) String() string {
	return fmt.Sprintf("Pi-hole[%s]", c.baseURL)
}
