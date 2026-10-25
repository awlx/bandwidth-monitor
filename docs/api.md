# API reference

Bandwidth Monitor serves JSON endpoints under `/api`. The browser dashboard
and companion clients use the same API.

| Endpoint | Method | Description |
|---|---|---|
| `/api/interfaces` | GET | Current interface statistics |
| `/api/interfaces/history` | GET | Interface history; optional `iface` and Unix-millisecond `since` parameters |
| `/api/talkers/bandwidth` | GET | Top talkers by current bandwidth |
| `/api/talkers/volume` | GET | Top talkers by rolling volume |
| `/api/clients/bandwidth` | GET | Top local clients by current bandwidth |
| `/api/clients/volume` | GET | Top local clients by rolling 24-hour volume |
| `/api/talkers/country` | GET | Talkers grouped by country |
| `/api/talkers/asn` | GET | Talkers grouped by autonomous system |
| `/api/host?ip=<address>` | GET | Traffic, flow, and enrichment details for one host |
| `/api/host/dns?ip=<address>` | GET | Recent DNS activity for one host |
| `/api/dns` | GET | Configured DNS provider summary |
| `/api/wifi` | GET | Configured WiFi controller summary |
| `/api/conntrack` | GET | Conntrack and NAT summary |
| `/api/topology` | GET | Discovered local network topology |
| `/api/latency` | GET | Latency monitor status |
| `/api/summary` | GET | Compact summary for companion clients |
| `/api/events` | GET | Server-Sent Events stream for live dashboard data |
| `/api/speedtest/run` | POST | Start a speed test and stream progress |
| `/api/speedtest/results` | GET | Speed test history and running status |
| `/api/speedtest/interfaces` | GET | Interfaces available for speed-test binding |
| `/api/debug/traceroute` | POST | Run traceroute and stream progress |
| `/api/debug/dns` | GET | DNS comparison and resolver diagnostics |
| `/api/debug/mtu` | POST | Run path MTU discovery |
| `/api/debug/publicip` | GET | Check interface-bound public addresses |
| `/api/debug/tcpcheck` | GET | Run an interface-bound TCP connectivity check |
| `/api/liveactivity/register` | POST | Register an iOS Live Activity token when direct APNs is enabled |

`/api/events` streams interface rates, talkers, DNS, WiFi, latency, and related
live summaries. Conntrack and topology are polled through their dedicated
endpoints.

## Prometheus metrics

`GET /metrics` returns all of the above in the Prometheus text exposition
format (`text/plain; version=0.0.4`). It is enabled by default, served on the
main `LISTEN` address, and unauthenticated like `/api`. Disable it with
`METRICS_ENABLED=false`; cap per-entity lists with `METRICS_TOP_N`
(see [configuration](configuration.md#prometheus-metrics)).

Example scrape job:

```yaml
scrape_configs:
  - job_name: bandwidth-monitor
    scrape_interval: 15s
    static_configs:
      - targets: ["192.0.2.10:8080"]
```

All metrics use the `bandwidth_monitor_` prefix and base units (bytes,
bits per second for link and speed-test rates, seconds, and 0-1 ratios).
Subsystems that are not configured simply emit no series.

| Area | Metrics | Labels |
|---|---|---|
| Daemon | `build_info`, `start_time_seconds`, `scrape_duration_seconds`, `go_goroutines`, `go_heap_alloc_bytes`, `go_heap_inuse_bytes`, `go_sys_bytes`, `go_gc_cycles_total` | `version`, `commit`, `goversion` |
| Interfaces | `interface_info`, `interface_up`, `interface_speed_bits_per_second`, `interface_vpn_routing` | `interface`, `type`, `wan` (+ `oper_state`) |
| Interface traffic | `interface_bytes_total`, `interface_packets_total`, `interface_errors_total`, `interface_dropped_total` (counters); `interface_rate_bytes_per_second`, `interface_rate_packets_per_second` | `interface`, `type`, `wan`, `direction` |
| Talkers | `top_talker_rate_bytes_per_second`, `top_talker_volume_bytes`, `top_client_rate_bytes_per_second`, `top_client_volume_bytes`, `talkers_unique_ips` | `ip`, `hostname`, `country`, `asn`, `as_org`, `direction` |
| Breakdown (24h) | `protocol_volume_bytes`, `ip_version_volume_bytes`, `country_volume_bytes`, `country_remote_ips`, `asn_volume_bytes`, `asn_remote_ips` | `protocol`, `ip_version`, `country`, `country_name`, `asn`, `as_org` |
| DNS | `dns_up`, `dns_queries`, `dns_blocked_queries`, `dns_blocked_ratio`, `dns_latency_seconds`, `dns_top_domain_queries`, `dns_top_blocked_domain_queries`, `dns_top_client_queries`, `dns_upstream_responses`, `dns_upstream_latency_seconds` | `provider`, `domain`, `ip`, `hostname`, `upstream` |
| WiFi | `wifi_up`, `wifi_access_points`, `wifi_clients`, `wifi_wired_clients`, `wifi_switches`, `wifi_ap_up`, `wifi_ap_clients`, `wifi_ap_uptime_seconds`, `wifi_ap_bytes_total`, `wifi_ap_rate_bytes_per_second`, `wifi_ssid_clients`, `wifi_ssid_rate_bytes_per_second`, `wifi_clients_by_signal`, `wifi_top_client_signal_dbm`, `wifi_top_client_rate_bytes_per_second` | `provider`, `ap`, `mac`, `model`, `ssid`, `radio`, `quality`, `hostname`, `direction` |
| Conntrack | `conntrack_entries`, `conntrack_entries_limit`, `conntrack_entries_by_family`, `conntrack_entries_by_protocol`, `conntrack_entries_by_state`, `conntrack_entries_by_nat_type`, `conntrack_top_lan_client_connections`, `conntrack_top_lan_client_bytes`, `conntrack_top_remote_connections`, `conntrack_top_remote_bytes` | `family`, `protocol`, `state`, `nat_type`, `ip`, `hostname` |
| Latency | `latency_target_up`, `latency_rtt_seconds`, `latency_rtt_avg_seconds`, `latency_rtt_min_seconds`, `latency_rtt_max_seconds`, `latency_rtt_p95_seconds`, `latency_rtt_p99_seconds`, `latency_jitter_seconds`, `latency_loss_ratio` | `target`, `probe` (`icmp`, `https`) |
| Speed test | `speedtest_running`, `speedtest_results`, `speedtest_download_bits_per_second`, `speedtest_upload_bits_per_second`, `speedtest_download_single_stream_bits_per_second`, `speedtest_upload_single_stream_bits_per_second`, `speedtest_ping_seconds`, `speedtest_jitter_seconds`, `speedtest_last_run_timestamp_seconds` | `interface` |
| Topology | `topology_nodes`, `topology_links` | `type` |

Rolling 24-hour volumes are gauges because entries age out of the window; use
`rate()` only on the `_total` counters. Speed-test results are kept in memory,
so their series disappear after a restart until the next test.
