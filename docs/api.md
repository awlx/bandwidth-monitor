# API reference

Bandwidth Monitor serves JSON endpoints under `/api`. The browser dashboard
and companion clients use the same API.

| Endpoint | Method | Description |
|---|---|---|
| `/api/interfaces` | GET | Current interface statistics |
| `/api/interfaces/history` | GET | Interface history; optional `iface` and Unix-millisecond `since` parameters |
| `/api/talkers/bandwidth` | GET | Top talkers by current bandwidth |
| `/api/talkers/volume` | GET | Top talkers by rolling volume |
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
