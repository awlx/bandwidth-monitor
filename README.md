# Bandwidth Monitor

A real-time network monitoring dashboard for Linux, written in Go.

Single-binary deployment with an embedded web UI, optional DNS stats (AdGuard Home, NextDNS, or Pi-hole), WiFi monitoring (UniFi or Omada), GeoIP enrichment, continuous latency monitoring, a macOS menu bar plugin, a Windows system-tray widget, and a GNOME/Linux top-bar indicator.

## Table of Contents

- [Screenshots](#screenshots)
- [Features](#features)
- [Quick Start](#quick-start)
- [bandwidth-top CLI](#bandwidth-top-cli)
- [Installation](#installation)
- [Configuration](#configuration)
- [macOS Menu Bar Plugin](#macos-menu-bar-plugin)
- [Windows System Tray Widget](#windows-system-tray-widget)
- [GNOME/Linux Indicator](#gnomelinux-indicator)
- [iOS App](#ios-app)
- [Architecture](#architecture)
- [API Endpoints](#api-endpoints)
- [Development and Validation](#development-and-validation)
- [External Services Transparency](#external-services-transparency)
- [Notes](#notes)
- [License](#license)

---

## Screenshots

<table>
  <tr>
    <th>Traffic (Light)</th>
    <th>NAT (Light)</th>
    <th>DNS (Light)</th>
    <th>WiFi (Light)</th>
    <th>Monitor (Light)</th>
    <th>Speed Test (Light)</th>
    <th>Debug (Light)</th>
  </tr>
  <tr>
    <td><img src="docs/traffic-light.png" width="300" alt="Traffic (light)" /></td>
    <td><img src="docs/nat-light.png" width="300" alt="NAT (light)" /></td>
    <td><img src="docs/dns-light.png" width="300" alt="DNS (light)" /></td>
    <td><img src="docs/wifi-light.png" width="300" alt="WiFi (light)" /></td>
    <td><img src="docs/monitor-light.png" width="300" alt="Monitor (light)" /></td>
    <td><img src="docs/speedtest-light.png" width="300" alt="Speed Test (light)" /></td>
    <td><img src="docs/debug-light.png" width="300" alt="Debug (light)" /></td>
  </tr>
  <tr>
    <th>Traffic (Dark)</th>
    <th>NAT (Dark)</th>
    <th>DNS (Dark)</th>
    <th>WiFi (Dark)</th>
    <th>Monitor (Dark)</th>
    <th>Speed Test (Dark)</th>
    <th>Debug (Dark)</th>
  </tr>
  <tr>
    <td><img src="docs/traffic-dark.png" width="300" alt="Traffic (dark)" /></td>
    <td><img src="docs/nat-dark.png" width="300" alt="NAT (dark)" /></td>
    <td><img src="docs/dns-dark.png" width="300" alt="DNS (dark)" /></td>
    <td><img src="docs/wifi-dark.png" width="300" alt="WiFi (dark)" /></td>
    <td><img src="docs/monitor-dark.png" width="300" alt="Monitor (dark)" /></td>
    <td><img src="docs/speedtest-dark.png" width="300" alt="Speed Test (dark)" /></td>
    <td><img src="docs/debug-dark.png" width="300" alt="Debug (dark)" /></td>
  </tr>
</table>

---

## Features

### Traffic Tab

- **Live interface stats** — queries the kernel via netlink (`RTM_GETLINK`) every second; shows RX/TX rates, totals, packets, errors, and drops per interface
- **Interface grouping** — auto-classifies interfaces using netlink `IFLA_INFO_KIND` as Physical, VLAN, PPP/WAN, VPN, or Loopback
- **VPN routing detection** — configurable sentinel files to show whether a VPN interface is actively routing traffic
- **Real-time line chart** — Chart.js with per-interface filtering and 1-hour sliding window
- **Per-interface sparklines** — mini inline charts on each interface card
- **Top talkers by bandwidth** — live transfer rates via packet capture with 5-second rate ring for responsive peak detection
- **Top talkers by volume** — rolling 24-hour totals with 1-minute bucket aggregation
- **Host detail modal** — click any IP to see per-host stats, 24h traffic volume chart with time labels, and active conntrack flows
- **Protocol breakdown** — TCP / UDP / ICMP / Other pie chart
- **IP version breakdown** — IPv4 vs IPv6 traffic split
- **GeoIP enrichment** — country flags, city names, ASN org names via MaxMind MMDB files (city-level precision with GeoLite2-City)
- **Reverse DNS** — resolves IPs to hostnames via a shared resolver with TTL-based cache expiry and bounded concurrency

### DNS Tab

- **AdGuard Home, NextDNS, or Pi-hole integration** — total queries, blocked count/percentage, average latency
- **Time-series charts** — queries and blocked requests over time
- **Top clients, domains, and blocked domains** — pie charts + ranked detail tables
- **Upstream DNS servers** — response counts and average latency

### WiFi Tab

- **UniFi or Omada controller integration** — polls AP and client data from the controller API (first configured wins)
- **AP cards** — per-AP status, clients, firmware, uptime, IP, MAC, live RX/TX rates
- **Clients per AP / per SSID** — pie charts and detail tables
- **Traffic per AP / per SSID** — cumulative bytes + live rates
- **Per-client traffic table** — hostname, IP, SSID, AP, signal strength (color badges), RX/TX totals, live rates
- **Search & sort** — filter clients by name/IP/MAC/SSID/AP; sort by traffic, rate, name, or signal

### NAT Tab

- **Conntrack via netlink** — uses [ti-mo/conntrack](https://github.com/ti-mo/conntrack) to query the kernel's connection tracking table directly via Netlink (no `/proc/net/nf_conntrack` needed)
- **Connection table overview** — total active connections, max table size, usage percentage (color-coded warnings at >50% and >80%)
- **IPv4 / IPv6 split** — separate counts and sub-tabs for browsing entries by IP version
- **Protocol breakdown** — TCP / UDP / ICMP / other pie chart
- **TCP state distribution** — ESTABLISHED, TIME_WAIT, SYN_SENT, CLOSE_WAIT, etc. with color-coded badges
- **NAT type detection** — classifies each flow as SNAT, DNAT, both, or none by comparing original vs reply tuples
- **Per-flow counters** — bytes and packets per connection (requires `net.netfilter.nf_conntrack_acct=1`)
- **Top sources & destinations** — ranked tables by connection count
- **Full entry table** — original and reply tuples with translated addresses highlighted, searchable and filterable by NAT type
- **macOS menu bar** — SwiftBar plugin shows connection count, table usage, IPv4/IPv6 split, and SNAT/DNAT counts

### Network Tab

- **Network topology** — discovers local clients and infrastructure and renders their relationships
- **Client inventory** — shows names, addresses, vendors, connection state, and traffic context when available

### Monitor Tab

- **Traffic world map** — live SVG map showing traffic flows with directional RX/TX lines (green for download, orange for upload), city-level coordinates when available, animated flow lines sized by rate, with zoom/pan controls
- **Active countries** — summarizes current remote traffic by country and links back to matching talkers
- **Latency monitor** — continuous ICMP + HTTPS probes against configurable targets (default: FFMUC anycast, Quad9, Digitalcourage) with rolling sparklines, RTT, jitter, and packet loss; dual-stack IPv4+IPv6; 15-minute history

### Speed Test Tab

- **Server-side speed test** — runs download/upload/ping tests from the router against [speed.ffmuc.net](https://speed.ffmuc.net) (OpenSpeedTest)
- **Live progress** — real-time gauges and progress bar streamed via SSE during the test
- **Ping & jitter** — measures latency with outlier-resistant median filtering
- **Parallel download/upload** — 6 concurrent HTTP streams for 10 seconds each for accurate throughput measurement
- **Test history** — stores the last 50 results in memory with timestamps
- **Configurable server** — change the target via `SPEEDTEST_SERVER` environment variable

### Debug Tab

- **Traceroute** — native Go ICMP traceroute with configurable probes per hop (default 20), using raw sockets with proper TTL manipulation and ICMP ID matching; shows per-hop IP, reverse DNS hostname (always fresh, bypasses cache), avg/min/max RTT, and packet loss percentage; supports IPv4 and IPv6; streams progress via SSE
- **DNS Check** — queries a domain (A, AAAA, MX, TXT, NS, CNAME, SOA, PTR) against 14 DNS servers in parallel: System Resolver, FFMUC Anycast01/02 (IPv4+IPv6), Cloudflare (IPv4+IPv6), Google (IPv4+IPv6), Quad9 (IPv4+IPv6), and OpenDNS (IPv4+IPv6); shows comparison matrix, RCode, latency, TTL, DNSSEC AD flag per server; highlights the fastest server and flags records unique to a single server
- **Resolver leak check** — automatically detects which public IPs your system resolver uses when talking to authoritative servers, via `o-o.myaddr.l.google.com` TXT and `dnscheck.tools` TXT (including IPv4-only and IPv6-only variants); shows the configured local resolver from `/etc/resolv.conf`, upstream egress IPs, EDNS Client Subnet info, and resolver org/geo from dnscheck.tools
- **Path MTU discovery** — probes a target from a selected interface and streams progress
- **Public IP check** — compares address-family and interface-specific egress
- **TCP connectivity check** — tests a host and port from a selected interface

### General

- **`bandwidth-top` CLI** — separate, lightweight ANSI terminal viewer with direct single-interface AF_PACKET capture and asynchronous peer enrichment
- **Server-Sent Events (SSE) live updates** — 1-second refresh with automatic reconnection
- **Dark/light/auto theme** — saved to localStorage
- **Fully embedded UI** — all HTML/CSS/JS baked into the binary via `go:embed`
- **macOS menu bar plugin** — SwiftBar/xbar script showing live stats
- **Windows system tray widget** — PowerShell script showing live stats in the notification area
- **GNOME/Linux indicator** — Python AppIndicator showing live stats in the top bar

---

## Quick Start

### Requirements

- **Linux** — uses netlink (`RTM_GETLINK`, `RTM_GETADDR`) for interface stats and addresses
- **nf_conntrack kernel module** — for the NAT tab (loaded automatically on most routers)
- **Go 1.25+** — to build


### Build & Run

```bash
# Build
make build

# Download GeoIP databases (optional, free)
make geoip

# Build a stripped binary - smaller binary size (optional).
make build_stripped

# Run (needs root or CAP_NET_RAW + CAP_NET_ADMIN for packet capture and netlink)
sudo ./bandwidth-monitor
```

Then open **http://localhost:8080**.

---

## bandwidth-top CLI

`bandwidth-top` is an ad-hoc live traffic viewer, similar in spirit to `iftop`.
It captures one local interface directly; it does not require a running
bandwidth-monitor server. Build it with `make build-top`, then run:

```bash
sudo ./bandwidth-top --interface eth0 --rows 20 --refresh 1s
sudo ./bandwidth-top --interface eth0 --local-network 192.0.2.0/24
./bandwidth-top --snapshot --no-public
```

When `--interface` is omitted, the lowest-metric active IPv4 or IPv6
default-route interface is selected. ECMP nexthops are evaluated independently;
their weights do not override route metric and deterministic interface
tie-breaking. Capture requires root or `CAP_NET_RAW`:

```bash
sudo setcap cap_net_raw+ep /usr/bin/bandwidth-top
```

Rates are displayed in bit/s (capture accounting uses bytes/s). Each ranked flow
uses two linked lines: `=>` for outbound traffic and `<=` for inbound traffic,
with an iftop-style bandwidth ruler, proportional graph-lane bars, and rolling
2s, 10s, and 40s rates. Remote IP, ASN, and provider have independent headed
columns on the primary line; the inbound continuation leaves those cells blank
and aligned. Narrow layouts shrink or drop whole optional columns rather than
merging metadata into the remote host. The footer summarizes TX, RX, and total
rates across all observed peers, including peers below the row limit.

Each flow is one local/remote endpoint pair, counted once with direction relative
to the actual local endpoint. Local-to-local and remote-to-remote packets are
excluded because they have no unambiguous peer direction. Live output uses
restrained direction colors; `--snapshot` is deterministic plain text.
LOCAL classification uses only the selected interface's assigned unicast
prefixes and netmasks; address categories such as RFC1918 or ULA are not local
by themselves. Repeatable `--local-network CIDR` flags replace all
interface-derived prefixes, which is useful for routed or bridged captures.

| Option | Default | Purpose |
|--------|---------|---------|
| `--interface` | default route | Capture interface |
| `--local-network` | interface prefixes | Repeatable local CIDR; supplied values replace interface prefixes |
| `--rows` | `20` | Maximum displayed peers |
| `--refresh` | `1s` | Refresh interval |
| `--snapshot` | off | Print one plain snapshot and exit |
| `--width` | terminal width | Output width; long values are truncated |
| `--asn-mmdb` | auto | GeoLite2 ASN MMDB |
| `--city-mmdb` | auto | GeoLite2 City/Country MMDB |
| `--server` | gateway discovery | Explicit bandwidth-monitor base URL; suppresses discovery |
| `--no-server-discovery` | off | Do not probe the selected default gateway |
| `--public-url` | `https://ip.ffmuc.net/json` | Public fallback API |
| `--no-public` | off | Disable public fallback |

MMDB files are discovered in the current directory,
`/usr/share/bandwidth-monitor`, and `/opt/bandwidth-monitor`. Enrichment fills
missing fields in order: local MMDB, ready monitor `/api/host`, then
ip.ffmuc.net. Lookups use a fixed worker pool and queue, validate every public
redirect and resolved destination, and use a bounded FIFO-evicted result cache.
At startup, the two optional MMDB readers and one monitor capability probe run
concurrently. An explicit `--server` is probed once. Without it, the CLI makes
one direct, no-proxy HTTP request to port 8080 on the selected default-route
gateway; it does not scan other hosts or ports, and `--no-server-discovery`
disables the request. The probe uses a fixed documentation IP, not a captured
peer. A failed source is disabled with one endpoint-free warning while remaining
sources continue; public fallback is never preflighted. Endpoint-free status
lines show the active fallback chain plus reasoned `local MMDB`, `monitor`, and
`public` states, so an absent local database does not imply enrichment is absent.
Every enrichment request identifies only the packaged CLI version as
`User-Agent: bandwidth-top/<version>`.

> **Privacy:** public fallback sends each observed globally routable peer IP to
> ip.ffmuc.net. Use `--no-public` to prevent this. Private, loopback,
> link-local, multicast, unspecified, reserved documentation, and other
> non-global addresses are never sent.
>
> Gateway discovery sends no observed peer IP. After a valid monitor response,
> normal enrichment requests send observed peer IPs only to that discovered
> gateway service. Use `--no-server-discovery` to prevent all gateway probing.

---

## Installation

### Pre-built Packages

Pre-built packages are available from [GitHub Releases](https://github.com/awlx/bandwidth-monitor/releases) for:

| Format | Architectures | Platform |
|--------|--------------|----------|
| `.deb` | amd64, arm64 | Debian, Ubuntu, Raspbian |
| `.rpm` | amd64, arm64 | Fedora, RHEL, AlmaLinux |
| `.ipk` | x86_64, aarch64, mips_24kc, mipsel_24kc | OpenWrt 23.05 (stable) |
| `.apk` | x86_64, aarch64 | OpenWrt snapshot (nightly) |
| Nix flake | any | NixOS / Nix on Linux |

Each format publishes independent `bandwidth-monitor` and `bandwidth-top`
packages. Neither depends on the other, so the CLI can be installed alone.

#### Debian / Ubuntu

**APT Repository (recommended):**

```bash
curl -fsSL https://awlx.github.io/bandwidth-monitor/bandwidth-monitor.gpg.key \
  | sudo gpg --dearmor -o /etc/apt/keyrings/bandwidth-monitor.gpg
echo "deb [signed-by=/etc/apt/keyrings/bandwidth-monitor.gpg] https://awlx.github.io/bandwidth-monitor stable main" \
  | sudo tee /etc/apt/sources.list.d/bandwidth-monitor.list
sudo apt update
sudo apt install bandwidth-monitor
```

Install only the terminal viewer, or both packages:

```bash
sudo apt install bandwidth-top
sudo apt install bandwidth-monitor bandwidth-top
```

**Manual install:**

```bash
sudo dpkg -i bandwidth-monitor_*.deb
sudo vi /etc/bandwidth-monitor/env
sudo systemctl restart bandwidth-monitor

# CLI only (no service or configuration is installed)
sudo dpkg -i bandwidth-top_*.deb
```

#### RHEL / Fedora

```bash
sudo rpm -i bandwidth-monitor-*.rpm
sudo vi /etc/bandwidth-monitor/env
sudo systemctl enable --now bandwidth-monitor

# CLI only, or install both RPM files together
sudo rpm -i bandwidth-top-*.rpm
sudo rpm -i bandwidth-monitor-*.rpm bandwidth-top-*.rpm
```

#### OpenWrt (stable, opkg)

```bash
opkg update && opkg install kmod-nf-conntrack-netlink
opkg install /tmp/bandwidth-monitor_*.ipk
vi /etc/bandwidth-monitor/env
/etc/init.d/bandwidth-monitor enable
/etc/init.d/bandwidth-monitor start

# CLI only (contains only /usr/bin/bandwidth-top)
opkg install /tmp/bandwidth-top_*.ipk
```

Optional GeoIP databases:
```bash
scp GeoLite2-City.mmdb GeoLite2-ASN.mmdb root@router:/etc/bandwidth-monitor/
/etc/init.d/bandwidth-monitor restart
```

#### OpenWrt (snapshot, apk)

```bash
apk update && apk add kmod-nf-conntrack-netlink
apk add --allow-untrusted /tmp/bandwidth-monitor-*.apk
vi /etc/bandwidth-monitor/env
/etc/init.d/bandwidth-monitor enable
/etc/init.d/bandwidth-monitor start

# CLI only
apk add --allow-untrusted /tmp/bandwidth-top-*.apk
```

#### NixOS / Nix Flake

The repo includes a Nix flake with a NixOS module. GeoIP databases are downloaded automatically during the build.

```nix
# In your flake.nix inputs:
inputs.bandwidth-monitor.url = "github:awlx/bandwidth-monitor";

# In your NixOS configuration:
{ inputs, ... }: {
  imports = [ inputs.bandwidth-monitor.nixosModules.default ];

  services.bandwidth-monitor = {
    enable = true;
    listenAddress = ":8080";
    settings = {
      ADGUARD_URL = "http://adguard.local";
      ADGUARD_USER = "admin";
      ADGUARD_PASS = "secret";
    };
    # Or use an environment file:
    # environmentFile = "/etc/bandwidth-monitor/env";

    # Use services.geoipupdate for fresh databases instead of bundled ones:
    # geoipDir = "/var/lib/GeoIP";
  };
}
```

To update the bundled GeoIP databases: `nix flake update`

Or run directly without installing:
```bash
nix run github:awlx/bandwidth-monitor

# Run only the independently packaged terminal viewer
nix run github:awlx/bandwidth-monitor#bandwidth-top
```

### System package layout and upgrades

The `bandwidth-monitor` package installs the daemon in `/usr/bin`, service
definitions in platform service directories, and configuration in
`/etc/bandwidth-monitor/env`. The independent `bandwidth-top` package installs
only `/usr/bin/bandwidth-top`; it has no service, configuration, lifecycle
scripts, state directory, dependency on the daemon package, or capability
mutation. Run it as root or grant `CAP_NET_RAW` externally. Installing both
packages creates no overlapping files.

On first daemon installation the configuration is
created with mode `0600` from the packaged example. It is not managed as a
Debian conffile: package upgrades are noninteractive and always preserve the
administrator-owned file, including any credentials it contains. Review the
packaged example at `/usr/share/bandwidth-monitor/env.example` after upgrades
for newly supported settings.

### Using the Makefile

```bash
# Build, download GeoIP DBs, install to /opt/bandwidth-monitor,
# set up systemd service, and start
make install

# Install only the CLI to /usr/local/bin (does not grant capabilities)
make install-top
```

This will:
1. Build the daemon binary
2. Download GeoIP databases if not present
3. Copy everything to `/opt/bandwidth-monitor/`
4. Create `.env` from `env.example` (if it doesn't exist)
5. Install and enable the systemd service

```bash
# Check status
systemctl status bandwidth-monitor

# View logs
journalctl -u bandwidth-monitor -f

# Uninstall everything
make uninstall
```

### Manual

```bash
go build -o bandwidth-monitor .
sudo mkdir -p /opt/bandwidth-monitor
sudo cp bandwidth-monitor /opt/bandwidth-monitor/
sudo cp env.example /opt/bandwidth-monitor/.env
sudo chmod 0600 /opt/bandwidth-monitor/.env
# Edit .env with your settings
sudo cp bandwidth-monitor.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now bandwidth-monitor
```

### Standalone systemd service

The root-level `bandwidth-monitor.service` is for the standalone `/opt`
installation. System packages ship a package-specific unit with `/usr` and
`/etc` paths. Both units run the binary with:
- `CAP_NET_RAW` and `CAP_NET_ADMIN` for packet capture and netlink access (no full root needed)
- `ProtectSystem=strict`, `ProtectHome=yes`, `PrivateTmp=yes` hardening
- Environment loaded from `/opt/bandwidth-monitor/.env`

---

## Configuration

Use the configuration path for your installation method:

| Installation | Configuration file |
|--------------|--------------------|
| Debian/RPM package | `/etc/bandwidth-monitor/env` |
| OpenWrt package | `/etc/bandwidth-monitor/env` |
| `make install` or manual `/opt` install | `/opt/bandwidth-monitor/.env` |

For a standalone install:

```bash
sudo cp env.example /opt/bandwidth-monitor/.env
sudo chmod 0600 /opt/bandwidth-monitor/.env
```

Keep configuration files readable only by the service administrator: they may
contain controller passwords, API keys, and TLS/APNs key paths. `env.example`
is the canonical list of documented settings.

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTEN` | `:8080` | Web listen address (e.g. `198.51.100.1:8080`) |
| `LISTEN_PROTOCOL` | `http` | Web server protocol: `http` or `https` |
| `TLS_CERT_FILE` | *(empty)* | TLS certificate path (required when `LISTEN_PROTOCOL=https`) |
| `TLS_KEY_FILE` | *(empty)* | TLS private key path (required when `LISTEN_PROTOCOL=https`) |
| `PROMISCUOUS` | `true` | Enable promiscuous mode for packet capture (`true`/`false`) |
| `DEBUG_HTTP_LOG` | `false` | Log every HTTP request (method, path+query, remote addr, status, response size in bytes, duration) to stdout. Opt-in and noisy — useful for debugging client request patterns (e.g. verifying `?since=`/`?iface=` params and the resulting response size) |
| `INTERFACES` | *(all)* | Comma-separated list of interfaces to monitor and display (e.g. `eth0,ppp0,wg0`). Controls both the web UI and packet capture. If not set, all interfaces are used. |
| `WAN_INTERFACE` | *(auto-detect)* | Explicit WAN interface override, useful when the uplink has a private address or auto-detection chooses incorrectly |
| `GEO_CITY` | `GeoLite2-City.mmdb` | Path to GeoLite2 City MMDB (includes country, city, coordinates for map). ~57 MB. For devices with limited flash (e.g. OpenWrt routers), use `GeoLite2-Country.mmdb` (~6 MB) instead — set `GEO_CITY=GeoLite2-Country.mmdb`. Country data still works, just without city-level map precision. |
| `GEO_ASN` | `GeoLite2-ASN.mmdb` | Path to GeoLite2 ASN MMDB (~11 MB) |

#### HTTPS/TLS Configuration

By default, the server runs on HTTP. To enable HTTPS:
Both certificate and key must be in PEM format (`.crt`, `.pem`, etc. all work as long as the content is valid PEM-encoded data).

**Example HTTPS setup:**
```bash
export LISTEN=:8443
export LISTEN_PROTOCOL=https
export TLS_CERT_FILE=/etc/bandwidth-monitor/server.crt
export TLS_KEY_FILE=/etc/bandwidth-monitor/server.key
./bandwidth-monitor
```

The server will start with TLS and log: `server: TLS enabled cert=... key=...`

If `LISTEN_PROTOCOL=https` but cert/key paths are missing or invalid, the server will fail at startup with a clear error message.

#### DNS (mutually exclusive — first configured wins)

| Variable | Default | Description |
|----------|---------|-------------|
| `ADGUARD_URL` | *(disabled)* | AdGuard Home base URL (e.g. `http://adguard.example.net`) |
| `ADGUARD_USER` | | AdGuard Home username |
| `ADGUARD_PASS` | | AdGuard Home password |
| `NEXTDNS_PROFILE` | *(disabled)* | NextDNS profile ID (e.g. `abc123`) |
| `NEXTDNS_API_KEY` | | NextDNS API key (from [my.nextdns.io/account](https://my.nextdns.io/account)) |
| `PIHOLE_URL` | *(disabled)* | Pi-hole base URL (e.g. `http://pi.hole`) |
| `PIHOLE_PASSWORD` | | Pi-hole password or app password |

### WiFi (mutually exclusive — first configured wins)

#### UniFi

| Variable | Default | Description |
|----------|---------|-------------|
| `UNIFI_URL` | *(disabled)* | UniFi controller URL (e.g. `https://unifi.example.net:8443`) |
| `UNIFI_USER` | | UniFi controller username |
| `UNIFI_PASS` | | UniFi controller password |
| `UNIFI_SITE` | `default` | UniFi site name |

The UniFi integration auto-detects both legacy controllers (port 8443) and UniFi OS devices (UDM/UDR/CloudKey Gen2+, port 443).

#### Omada

| Variable | Default | Description |
|----------|---------|-------------|
| `OMADA_URL` | *(disabled)* | TP-Link Omada controller URL (e.g. `https://omada.example.net`) |
| `OMADA_USER` | | Omada controller username |
| `OMADA_PASS` | | Omada controller password |
| `OMADA_SITE` | `Default` | Omada site name |

#### Network & VPN

| Variable | Default | Description |
|----------|---------|-------------|
| `LOCAL_NETS` | *(auto-detect)* | Comma-separated CIDRs for RX/TX direction detection (e.g. `192.0.2.0/24,2001:db8::/48`). Auto-discovered from local interfaces if not set. |
| `SPAN_DEVICE` | *(disabled)* | SPAN/mirror port interface for direction-aware RX/TX (requires `LOCAL_NETS`; e.g. `eth1`) |
| `VPN_STATUS_FILES` | *(none)* | Comma-separated `iface=path` pairs for VPN routing detection (e.g. `wg0=/run/wg0-active`) |

#### Speed Test

| Variable | Default | Description |
|----------|---------|-------------|
| `SPEEDTEST_SERVER` | `https://speed.ffmuc.net` | Target server URL for the speed test tab |

#### Latency

| Variable | Default | Description |
|----------|---------|-------------|
| `LATENCY_TARGETS` | `anycast01.ffmuc.net,anycast02.ffmuc.net,dns.quad9.net,dns3.digitalcourage.de` | Comma-separated hostnames/IPs to probe via ICMP and HTTPS |

### Tab Visibility

- **DNS tab** — shown when AdGuard Home, NextDNS, or Pi-hole is configured
- **WiFi tab** — shown when UniFi or Omada is configured
- **NAT tab** — shown automatically when `nf_conntrack` is loaded and the process has `CAP_NET_ADMIN`

### Conntrack (NAT) Configuration

The NAT tab works out of the box on any Linux system with `nf_conntrack` loaded — no configuration needed. To enable per-flow byte/packet counters:

```bash
# Enable conntrack accounting (required for per-flow bytes/packets)
sysctl -w net.netfilter.nf_conntrack_acct=1

# Make persistent
echo 'net.netfilter.nf_conntrack_acct=1' >> /etc/sysctl.conf
```

The binary needs `CAP_NET_ADMIN` (or root) for netlink access. The included systemd service already grants this via `AmbientCapabilities`.

### RX/TX Direction Detection

The top-talkers tables show per-host RX (download) and TX (upload) columns. Local network ranges are **auto-discovered** from interface addresses at startup — no configuration needed in most cases.

For SPAN/mirror port setups or if auto-discovery doesn't cover all your addresses (e.g. dynamic ISP prefixes), set `LOCAL_NETS` explicitly — similar to iftop's `-F`/`-G` flags:

```bash
LOCAL_NETS=192.0.2.0/24,2001:db8::/48
```

### SPAN / Mirror Port Mode

On a SPAN or mirror port, the kernel reports all mirrored traffic as RX on the interface, making the normal RX/TX split meaningless. Setting `SPAN_DEVICE` activates a raw-socket overlay that inspects IP headers and classifies direction using `LOCAL_NETS`:

- **src in LOCAL_NETS → remote** = upload (TX)
- **remote → dst in LOCAL_NETS** = download (RX)
- **both local** = counted as both (intra-LAN)

```bash
# In your .env
SPAN_DEVICE=eth1
LOCAL_NETS=192.0.2.0/24,2001:db8::/48
```

All other interfaces keep their normal netlink-based stats, VPN routing detection, interface grouping, etc. Only the SPAN device gets its RX/TX overridden. Requires root or `CAP_NET_RAW`.

### VPN Routing Detection (OpenWrt)

The `VPN_STATUS_FILES` variable tells bandwidth-monitor which sentinel files to check for active VPN routing. On OpenWrt, a hotplug script (`99-vpn-status`) is included that automatically creates and removes these sentinel files when WireGuard interfaces go up or down.

The script is installed automatically with the OpenWrt package to `/etc/hotplug.d/iface/99-vpn-status`. It reads `VPN_STATUS_FILES` from `/etc/bandwidth-monitor/env` — the same file the main service uses — so there is nothing extra to configure:

```bash
# In /etc/bandwidth-monitor/env
VPN_STATUS_FILES=wg0=/run/wg0-active,wg1=/run/wg1-active
```

When `wg0` comes up, the hotplug script writes a timestamp to `/run/wg0-active`. When it goes down, the file is removed. The dashboard shows a 🔒 icon on interfaces that are actively routing.

---

## macOS Menu Bar Plugin

A [SwiftBar](https://github.com/swiftbar/SwiftBar) / [xbar](https://xbarapp.com/) plugin is included at `swiftbar/bandwidth-monitor.5s.sh`. It shows live RX/TX rates, DNS stats, and WiFi client counts in the macOS menu bar.

**Dependencies:** `curl`, `jq` (install via `brew install jq`)

**Setup:**
1. Copy `swiftbar/bandwidth-monitor.5s.sh` to your SwiftBar plugin directory
2. Make it executable: `chmod +x bandwidth-monitor.5s.sh`
3. Edit the defaults at the top of the script, or set environment variables

**Configuration via environment variables:**

| Variable | Default | Description |
|----------|---------|-------------|
| `BW_SERVERS` | `http://localhost:8080` | Comma-separated list of servers to try in order (first reachable wins) |
| `BW_SERVER` | `http://localhost:8080` | Single server URL (used if `BW_SERVERS` is not set) |
| `BW_PORT` | `8080` | Port used when auto-detecting the server from the macOS default gateway |
| `BW_PREFER_IFACE` | *(auto)* | Default preferred interface for menu bar title (e.g. `ppp0`) |
| `BW_PREFER_IFACE_MAP` | *(none)* | Per-server interface override: `url=iface,url=iface` |
| `BW_SHOW_EXTERNAL_IP` | `true` | Show public IPs in menu bar by querying [`ip.ffmuc.net`](https://ip.ffmuc.net); set to `false` to disable |

**Multi-server example** (edit the defaults in the script):
```bash
SERVERS="http://198.51.100.1:8080, http://203.0.113.1:8080"
PREFER_IFACE_MAP="http://198.51.100.1:8080=eth0,http://203.0.113.1:8080=ppp0"
```

The plugin tries each server in order with a 1-second timeout. The preferred interface is resolved per-server from the map. Shows a 🔒 icon when VPN routing is active.

---

## Windows System Tray Widget

A PowerShell system-tray widget is included at `windows/bandwidth-monitor-tray.ps1`. It creates a notification area (system tray) icon that polls the bandwidth-monitor API every 5 seconds, showing live RX/TX rates in the tooltip and full details (interfaces, DNS, WiFi, NAT) in the right-click context menu.

**Dependencies:** PowerShell 5.1+ (built into Windows 10/11), .NET Framework (for `System.Windows.Forms`)

**Setup:**
1. Double-click `windows/bandwidth-monitor-tray.vbs` for a silent launch (no console window)
2. Or use `windows/bandwidth-monitor-tray.bat` to launch with a visible console (useful for debugging)
3. Or run directly in PowerShell: `powershell -ExecutionPolicy Bypass -File bandwidth-monitor-tray.ps1`
4. The icon auto-detects the server from the Windows default gateway, or set it explicitly

**Configuration via parameters or environment variables:**

| Parameter | Env Variable | Default | Description |
|-----------|-------------|---------|-------------|
| `-Server` | `BW_SERVER` | *(auto-detect from default gateway)* | Base URL of the bandwidth-monitor server |
| `-Port` | `BW_PORT` | `8080` | Port used when auto-detecting from the gateway |
| `-PreferIface` | `BW_PREFER_IFACE` | *(auto)* | Preferred interface name for the tooltip |
| `-RefreshSeconds` | — | `5` | Polling interval in seconds |
| `-ShowExternalIP` | `BW_SHOW_EXTERNAL_IP` | `true` | Show public IPs by querying [`anycast-v4.ffmuc.net`](https://anycast-v4.ffmuc.net) / [`anycast-v6.ffmuc.net`](https://anycast-v6.ffmuc.net); set to `false` to disable |

**Example:**
```powershell
.\bandwidth-monitor-tray.ps1 -Server http://198.51.100.1:8080 -PreferIface eth0
```

**Features:**
- **Tooltip** shows current download/upload rates for the primary interface (e.g. `eth0: down 12.3 Mb/s / up 4.5 Mb/s`)
- **Live icon** renders compact down/up rates with coloured arrows (green ↓, orange ↑) directly on the tray icon
- **DPI-aware** icon scales with Windows display scaling (125%, 150%, 200%)
- **Right-click menu** shows all interfaces, external IPs, DNS stats, WiFi clients, NAT table info
- **Double-click** opens the web dashboard in the default browser
- Shows `[VPN]` in the tooltip when VPN routing is active

**Auto-start on login:**

1. Copy the `windows` folder to your user directory (e.g. `%USERPROFILE%\bandwidth-monitor\`):
   ```powershell
   Copy-Item -Recurse .\windows "$env:USERPROFILE\bandwidth-monitor"
   ```
2. Create a startup shortcut (copy-paste this as-is into PowerShell):
   ```powershell
   $ws = New-Object -ComObject WScript.Shell
   $sc = $ws.CreateShortcut("$env:APPDATA\Microsoft\Windows\Start Menu\Programs\Startup\bandwidth-monitor-tray.lnk")
   $sc.TargetPath = "$env:USERPROFILE\bandwidth-monitor\bandwidth-monitor-tray.vbs"
   $sc.WindowStyle = 7  # minimized
   $sc.Save()
   ```
Or manually: press `Win+R`, type `shell:startup`, and copy `bandwidth-monitor-tray.bat` (or a shortcut to it) into that folder.

> **Tip:** Windows may hide new tray icons in the overflow area. Click the **^** arrow near the clock to find it, then drag the icon onto the taskbar to keep it visible.

---

## GNOME/Linux Indicator

A Python AppIndicator is included at `gnome/bandwidth-monitor-indicator.py`. It shows live RX/TX rates in the GNOME top bar (or any panel supporting AppIndicator) and full details in the dropdown menu.

Works on GNOME (with the [AppIndicator extension](https://extensions.gnome.org/extension/615/appindicator-support/)), KDE Plasma, XFCE, Budgie, Cinnamon, and MATE.

**Dependencies:**
```bash
# Debian/Ubuntu
sudo apt install python3-gi gir1.2-gtk-3.0 gir1.2-ayatanaappindicator3-0.1

# Fedora
sudo dnf install python3-gobject gtk3 libayatana-appindicator-gtk3

# Arch
sudo pacman -S python-gobject gtk3 libayatana-appindicator
```

On GNOME Shell 42+, install the [AppIndicator and KStatusNotifierItem Support](https://extensions.gnome.org/extension/615/appindicator-support/) extension.

**Setup:**
1. Run directly: `./gnome/bandwidth-monitor-indicator.py`
2. Or with options: `./gnome/bandwidth-monitor-indicator.py --server http://198.51.100.1:8080`
3. The indicator auto-detects the server from the Linux default gateway

**Configuration via CLI flags or environment variables:**

| Flag | Env Variable | Default | Description |
|------|-------------|---------|-------------|
| `--server` | `BW_SERVER` | *(auto-detect from default gateway)* | Base URL of the bandwidth-monitor server |
| `--port` | `BW_PORT` | `8080` | Port used when auto-detecting from the gateway |
| `--prefer-iface` | `BW_PREFER_IFACE` | *(auto)* | Preferred interface name for the panel label |
| `--refresh` | -- | `5` | Polling interval in seconds |
| `--show-external-ip` | `BW_SHOW_EXTERNAL_IP` | `true` | Show public IPs via [`anycast-v4.ffmuc.net`](https://anycast-v4.ffmuc.net) / [`anycast-v6.ffmuc.net`](https://anycast-v6.ffmuc.net) |

**Auto-start on login:**
```bash
# Copy the indicator to a system-wide location
sudo mkdir -p /usr/local/share/bandwidth-monitor
sudo cp gnome/bandwidth-monitor-indicator.py /usr/local/share/bandwidth-monitor/
sudo chmod +x /usr/local/share/bandwidth-monitor/bandwidth-monitor-indicator.py

# Install the .desktop file for autostart
cp gnome/bandwidth-monitor-indicator.desktop ~/.config/autostart/
```
Or for the current user only, symlink the script and edit the `Exec=` path in the `.desktop` file.

**Features:**
- **Panel label** shows live compact down/up rates with arrows (e.g. `\u21935M \u219112M`)
- **Dropdown menu** shows all interfaces, external IPs, DNS stats, WiFi clients, NAT info
- **Click "Open Dashboard"** to launch the web UI in the default browser
- Shows `[VPN]` in the panel label when VPN routing is active
- Uses standard `network-transmit-receive` icon from the system theme

---

## iOS App

An iOS client is available at [yeled/bandwidth-monitor-ios](https://github.com/yeled/bandwidth-monitor-ios). It displays live traffic graphs, per-interface sparklines, and — via ActivityKit — a **Lock Screen widget and Dynamic Island Live Activity** that keep updating in real time while the app is in the background.

### Live Activity push (Lock Screen / Dynamic Island)

The Live Activity is kept alive by server-sent APNs push notifications. There are two ways this can work:

**Default (no server configuration needed):** The iOS app registers its push token with a shared relay gateway (`apnsgateway`). The gateway polls your server's existing `/api/interfaces` and `/api/interfaces/history` endpoints and pushes updates to APNs using its own key — your server needs no APNs configuration at all.

**Self-hosted key (advanced):** If you'd rather not depend on the shared gateway, you can push directly from your own bandwidth-monitor instance by providing your own Apple Developer APNs key. Enable it with these environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `APNS_KEY_FILE` | *(disabled)* | Path to the APNs auth key (`.p8`) from [developer.apple.com](https://developer.apple.com) (Keys → Apple Push Notification service). Keep it `chmod 0600`. When set, enables `POST /api/liveactivity/register`. |
| `APNS_KEY_ID` | | 10-character key ID from the Apple Developer portal |
| `APNS_TEAM_ID` | | Apple Developer team ID |
| `APNS_BUNDLE_ID` | | App bundle ID (e.g. `com.example.BandwidthMonitor`) |
| `APNS_ENV` | `production` | APNs environment: `production` (App Store / TestFlight) or `sandbox` (Xcode dev builds) |
| `APNS_PUSH_INTERVAL` | `10s` | How often to push an update to each registered device |

The server needs outbound HTTPS to `api.push.apple.com:443`. The feature is entirely off unless `APNS_KEY_FILE` is set.

For running the shared relay gateway yourself, see [docs/apns-gateway.md](docs/apns-gateway.md).

---

## Architecture

```text
main.go                    Entry point, environment parsing, component wiring, routes
cmd/bandwidth-top/         Separate Linux live terminal capture binary
bandwidthtop/              CLI enrichment, formatting, and interface selection
collector/                 Netlink interface stats, rates, history, VPN routing
conntrack/                 Netlink conntrack/NAT reader
talkers/                   AF_PACKET capture, host accounting, rolling rate/volume data
resolver/                  Shared reverse-DNS resolver and bounded cache
latency/                   Continuous ICMP and HTTPS probes
speedtest/                 Server-side download, upload, and latency tests
debug/                     Traceroute, DNS, MTU, public-IP, and TCP diagnostics
handler/                   JSON API and SSE handlers
topology/                  Local network discovery and topology state
dns/, wifi/                Provider interfaces
adguard/, nextdns/, pihole/ DNS provider clients
unifi/, omada/             WiFi controller clients
geoip/                     MaxMind country, city, coordinate, and ASN lookups
apns/, liveactivity/       Optional direct iOS Live Activity push support
apnsgateway/               Standalone APNs relay binary
contentstate/              Shared Live Activity content-state builder
poller/                    Idempotent, waitable periodic-runner lifecycle
static/index.html          Dashboard shell
static/js/                 Modular frontend core, components, and tab renderers
webassets/                 Local asset discovery and content fingerprinting
packaging/                 System-package services, scripts, versioning, and tests
nfpm*.yaml                 Separate daemon and CLI Debian/RPM manifests
env.example                Canonical documented runtime configuration
Makefile                   Build, install, and GeoIP targets
```

---

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/interfaces` | GET | Current stats for all interfaces |
| `/api/interfaces/history` | GET | 24h time-series per interface. Optional params: `iface` (single interface), `since` (Unix ms — only newer points). Additive: servers predating them ignore them and return everything |
| `/api/talkers/bandwidth` | GET | Top 10 by current bandwidth |
| `/api/talkers/volume` | GET | Top 10 by 24h volume |
| `/api/talkers/country` | GET | Talker aggregation by country |
| `/api/talkers/asn` | GET | Talker aggregation by autonomous system |
| `/api/dns` | GET | DNS summary (AdGuard Home, NextDNS, or Pi-hole) |
| `/api/wifi` | GET | WiFi summary (UniFi or Omada) |
| `/api/latency` | GET | Latency monitoring status (ICMP + HTTPS probes) |
| `/api/conntrack` | GET | NAT / conntrack summary (connections, states, NAT types, entries) |
| `/api/host` | GET | Detail for one host; parameter: `ip` |
| `/api/host/dns` | GET | Recent DNS activity for one host; parameter: `ip` |
| `/api/speedtest/run` | POST | Start a speed test; streams progress as SSE (Server-Sent Events) |
| `/api/speedtest/results` | GET | Speed test history (last 50 results) and running status |
| `/api/speedtest/interfaces` | GET | Interfaces available for speed-test binding |
| `/api/debug/traceroute` | POST | ICMP traceroute with SSE progress; params: `target`, `count` (probes/hop), `maxttl` |
| `/api/debug/dns` | GET | DNS check against 14 servers + resolver leak test; params: `domain`, `type` |
| `/api/debug/mtu` | POST | Path MTU discovery with SSE progress |
| `/api/debug/publicip` | GET | Interface-bound public address check |
| `/api/debug/tcpcheck` | GET | Interface-bound TCP connectivity check |
| `/api/summary` | GET | Compact summary for menu bar clients |
| `/api/topology` | GET | Current discovered network topology |
| `/api/events` | GET | SSE stream — pushes interface rates, talkers, DNS/WiFi/latency summaries every second (Server-Sent Events, gzip-compressed when the client accepts it). Conntrack and topology are deliberately excluded — poll `/api/conntrack`/`/api/topology` directly instead |
| `/api/liveactivity/register` | POST | Register an iOS Live Activity push token (only available when `APNS_KEY_FILE` is set) |

---

## Development and Validation

The runtime targets Linux and uses Linux-only networking APIs. Run the complete
Go suite on Linux:

```bash
go test ./...
go test -race ./poller ./talkers ./bandwidthtop
go vet ./...
go build ./...
GOOS=linux CGO_ENABLED=0 go build ./cmd/bandwidth-top
go run ./cmd/bandwidth-top --help
```

Frontend and packaging checks:

```bash
node static/js/utils.test.js
for file in static/js/*.js static/js/components/*.js static/js/tabs/*.js; do
  node --check "$file"
done
./packaging/test-packaging.sh
```

Maintainer source-of-truth map:

- Add runtime settings to `main.go`, `env.example`, and `packaging/runtime-env.list`; the packaging test enforces parity.
- Register HTTP routes in `main.go` and update the API table above.
- Add local scripts/styles to `static/index.html`; `webassets` discovers and fingerprints them at startup.
- Keep standalone `/opt` service files separate from package-specific files under `packaging/`.
- JavaScript that writes dynamic HTML must use the contextual escaping and numeric helpers in `static/js/utils.js`.

---

## External Services Transparency

Every hardcoded external service that bandwidth-monitor or its components contact:

| Service | URL / IP | Component | Trigger | Data sent | Data received |
|---------|---------|-----------|---------|-----------|---------------|
| **FFMUC Speed Test** | [`speed.ffmuc.net`](https://speed.ffmuc.net) | Speed Test tab | User clicks "Start Test" | HTTP GET `/downloading`, POST `/upload` (random payload) | Download payload, upload ack |
| **FFMUC IP Check** | [`ip.ffmuc.net`](https://ip.ffmuc.net) | SwiftBar plugin | Every ~5 min (cached), **on by default** (`BW_SHOW_EXTERNAL_IP=false` to disable) | HTTPS GET (IPv4 + IPv6) | Router's public IPv4 and IPv6 address |
| **FFMUC peer enrichment** | [`ip.ffmuc.net/json`](https://ip.ffmuc.net/json) | `bandwidth-top` | Once per uncached globally routable peer when local/server ASN fields are missing; **on by default** (`--no-public` to disable) | Observed peer IP in URL query | Country, city, provider, ASN |
| **FFMUC IP Check** | [`anycast-v4.ffmuc.net`](https://anycast-v4.ffmuc.net), [`anycast-v6.ffmuc.net`](https://anycast-v6.ffmuc.net) | Windows tray widget | Every ~5 min (cached), **on by default** (`BW_SHOW_EXTERNAL_IP=false` to disable) | HTTPS GET (one per address family) | Router's public IPv4 and IPv6 address |
| **FFMUC IP Check** | [`anycast-v4.ffmuc.net`](https://anycast-v4.ffmuc.net), [`anycast-v6.ffmuc.net`](https://anycast-v6.ffmuc.net) | GNOME indicator | Every ~5 min (cached), **on by default** (`--show-external-ip false` to disable) | HTTPS GET (one per address family) | Router's public IPv4 and IPv6 address |
| **FFMUC Anycast01** | `5.1.66.255`, `2001:678:e68:f000::` | DNS Check | User clicks "Query" | DNS query for user-entered domain | DNS records |
| **FFMUC Anycast02** | `185.150.99.255`, `2001:678:ed0:f000::` | DNS Check | User clicks "Query" | DNS query for user-entered domain | DNS records |
| **Cloudflare DNS** | `1.1.1.1`, `2606:4700:4700::1111` | DNS Check | User clicks "Query" | DNS query for user-entered domain | DNS records |
| **Google DNS** | `8.8.8.8`, `2001:4860:4860::8888` | DNS Check | User clicks "Query" | DNS query for user-entered domain | DNS records |
| **Quad9 DNS** | `9.9.9.9`, `2620:fe::fe` | DNS Check | User clicks "Query" | DNS query for user-entered domain | DNS records |
| **OpenDNS** | `208.67.222.222`, `2620:119:35::35` | DNS Check | User clicks "Query" | DNS query for user-entered domain | DNS records |
| **Google Authoritative** | `o-o.myaddr.l.google.com` | Resolver leak check | Piggybacks on DNS Check | TXT query via system resolver | Resolver's public IP, ECS info |
| **dnscheck.tools** | `test.dnscheck.tools`, `test-ipv4.*`, `test-ipv6.*` | Resolver leak check | Piggybacks on DNS Check | TXT query via system resolver | Resolver IP, org, geo, protocol |
| **FFMUC Anycast01** | `anycast01.ffmuc.net` | Latency monitor | Every 2s on startup (**on by default**, configurable via `LATENCY_TARGETS`) | ICMP echo + HTTPS GET | RTT measurement |
| **FFMUC Anycast02** | `anycast02.ffmuc.net` | Latency monitor | Every 2s on startup (**on by default**, configurable via `LATENCY_TARGETS`) | ICMP echo + HTTPS GET | RTT measurement |
| **Quad9 DNS** | `dns.quad9.net` | Latency monitor | Every 2s on startup (**on by default**, configurable via `LATENCY_TARGETS`) | ICMP echo + HTTPS GET | RTT measurement |
| **Digitalcourage DNS** | `dns3.digitalcourage.de` | Latency monitor | Every 2s on startup (**on by default**, configurable via `LATENCY_TARGETS`) | ICMP echo + HTTPS GET | RTT measurement |

All JavaScript libraries (Chart.js, Luxon) and fonts (Inter, JetBrains Mono) are **bundled in the binary** — no CDN requests are made at runtime. The world map boundary data (Natural Earth 110m, public domain) is also bundled. See [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md) for their licenses.

User-configured services (AdGuard Home, NextDNS, Pi-hole, UniFi Controller, Omada Controller) are **not** listed here — they are optional and only contacted when explicitly configured via environment variables.

FFMUC services ([`speed.ffmuc.net`](https://speed.ffmuc.net), [`ip.ffmuc.net`](https://ip.ffmuc.net), Anycast DNS) are operated by [Freie Netze München e.V.](https://ffmuc.net/) — see their [privacy policy](https://ffmuc.net/privacy/).

**No telemetry, analytics, crash reporting, or update checks.** Network requests
are limited to the configured integrations and explicitly listed optional or
diagnostic services above.

---

## Notes

### Permissions

| Feature | Capability required |
|---------|-------------------|
| Interface stats, NAT tab | `CAP_NET_ADMIN` (or root) |
| Top talkers, SPAN mode, `bandwidth-top` | `CAP_NET_RAW` (or root) |
| Traceroute, Latency monitor (ICMP) | `CAP_NET_RAW` |
| DNS check, resolver leak test | No special permissions |

If running without root, grant both `CAP_NET_RAW` and `CAP_NET_ADMIN` for full functionality.

### Optional Features

- **GeoIP** — without MMDB files, country/ASN columns are simply hidden. Download the free GeoLite2 databases from [MaxMind](https://dev.maxmind.com/geoip/geolite2-free-geolocation-data) (requires free account) or run `make geoip`
- **DNS and WiFi tabs** — only appear when their respective integrations are configured
- **Speed test** — runs from the router, not the client — useful for testing WAN throughput independent of local WiFi
- **NAT per-flow counters** — require `nf_conntrack_acct=1` (see [Conntrack Configuration](#conntrack-nat-configuration))
- All assets are embedded in the binary — single-file deployment, no runtime dependencies

---

## License

This project is licensed under the [GNU Affero General Public License v3.0](LICENSE) (AGPL-3.0).

You are free to use, modify, and distribute this software under the terms of the AGPL-3.0. If you modify the program and make it available over a network, you must release your modifications under the same license.

Bundled third-party libraries (Chart.js, Luxon, Inter, JetBrains Mono) are distributed under their respective permissive licenses — see [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md) for details.
