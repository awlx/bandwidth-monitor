// Package dns defines the common interface for DNS providers (AdGuard Home, NextDNS, etc.).
package dns

// Provider is implemented by any DNS stats backend.
type Provider interface {
	GetSummary() *Summary
	Available() bool
	Stop()
}

// Summary is the common DNS stats format sent to the frontend.
// Both adguard and nextdns produce this same shape.
type Summary struct {
	ProviderName   string  `json:"provider_name"`
	TotalQueries   int     `json:"total_queries"`
	BlockedTotal   int     `json:"blocked_total"`
	BlockedPercent float64 `json:"blocked_pct"`
	AvgLatencyMs   float64 `json:"avg_latency_ms"`

	TopQueried []DomainStat `json:"top_queried"`
	TopBlocked []DomainStat `json:"top_blocked"`
	TopClients []ClientStat `json:"top_clients"`

	Upstreams []UpstreamStat `json:"upstreams"`

	QueriesSeries []int  `json:"queries_series"`
	BlockedSeries []int  `json:"blocked_series"`
	TimeUnits     string `json:"time_units"`
}

// DomainStat is a single domain + count entry.
type DomainStat struct {
	Domain string `json:"domain"`
	Count  int    `json:"count"`
}

// ClientStat is a single client IP + count entry.
type ClientStat struct {
	IP       string `json:"ip"`
	Hostname string `json:"hostname,omitempty"`
	Count    int    `json:"count"`
}

// UpstreamStat is a single upstream server entry.
type UpstreamStat struct {
	Address   string  `json:"address"`
	Responses int     `json:"responses"`
	AvgMs     float64 `json:"avg_ms"`
}

// QueryLogEntry is a single recent DNS query made by a client.
type QueryLogEntry struct {
	Time      string  `json:"time"`
	Domain    string  `json:"domain"`
	Type      string  `json:"type,omitempty"`
	Blocked   bool    `json:"blocked"`
	Reason    string  `json:"reason,omitempty"`
	ElapsedMs float64 `json:"elapsed_ms,omitempty"`
}

// ClientQueryLogger is implemented by providers that can return recent
// per-client query history (e.g. AdGuard Home's /control/querylog). It is
// intentionally separate from Provider so existing providers that can't
// support it (NextDNS, Pi-hole today) aren't forced to implement a stub.
type ClientQueryLogger interface {
	QueryLog(clientIP string, limit int) ([]QueryLogEntry, error)
}
