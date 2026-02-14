# Bandwidth Monitor

A real-time network monitoring dashboard for Linux, written in Go.

Single-binary deployment with an embedded web UI, optional DNS stats (AdGuard Home, NextDNS, or Pi-hole), WiFi monitoring (UniFi or Omada), GeoIP enrichment, continuous latency monitoring, a macOS menu bar plugin, a Windows system-tray widget, and a GNOME/Linux top-bar indicator.

## Table of Contents

- [Screenshots](#screenshots)
- [Features](#features)
- [Quick Start](#quick-start)
- [Installation](#installation)
- [Configuration](#configuration)
- [macOS Menu Bar Plugin](#macos-menu-bar-plugin)
- [Windows System Tray Widget](#windows-system-tray-widget)
- [GNOME/Linux Indicator](#gnomelinux-indicator)
- [Architecture](#architecture)
- [API Endpoints](#api-endpoints)
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
- **Top talkers by bandwidth** — live transfer rates via packet capture (gopacket/libpcap)
- **Top talkers by volume** — rolling 24-hour totals with 1-minute bucket aggregation
- **Protocol breakdown** — TCP / UDP / ICMP / Other pie chart
- **IP version breakdown** — IPv4 vs IPv6 traffic split
- **GeoIP enrichment** — country flags, ASN org names via MaxMind MMDB files
- **Reverse DNS** — resolves IPs to hostnames via a shared resolver with TTL-based cache expiry and bounded concurrency
- **Traffic world map** — live SVG map showing traffic flows by country, sized by volume, with animated flow lines to active destinations
- **Latency monitor** — continuous ICMP + HTTPS probes against configurable targets (default: FFMUC anycast, Quad9, Digitalcourage) with rolling sparklines, RTT, jitter, and packet loss; dual-stack IPv4+IPv6; 15-minute history

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

### General

- **Server-Sent Events (SSE) live updates** — 1-second refresh with automatic reconnection
- **Dark/light/auto theme** — saved to localStorage
- **Fully embedded UI** — all HTML/CSS/JS baked into the binary via `go:embed`
- **macOS menu bar plugin** — SwiftBar/xbar script showing live stats
- **Windows system tray widget** — PowerShell script showing live stats in the notification area- **GNOME/Linux indicator** -- Python AppIndicator showing live stats in the top bar
---

## Quick Start

### Requirements

- **Linux** — uses netlink (`RTM_GETLINK`, `RTM_GETADDR`) for interface stats and addresses
- **libpcap-dev** — for packet capture (top talkers)
- **nf_conntrack kernel module** — for the NAT tab (loaded automatically on most routers)
- **Go 1.24+** — to build

```bash
# Debian/Ubuntu
sudo apt install libpcap-dev

# RHEL/Fedora
sudo dnf install libpcap-devel

# Arch
sudo pacman -S libpcap
```

### Build & Run

```bash
# Build
make build

# Download GeoIP databases (optional, free)
make geoip

# Run (needs root or CAP_NET_RAW + CAP_NET_ADMIN for packet capture and netlink)
sudo ./bandwidth-monitor
```

Then open **http://localhost:8080**.

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

#### Debian / Ubuntu

```bash
sudo dpkg -i bandwidth-monitor_*.deb
sudo vi /etc/bandwidth-monitor/env
sudo systemctl enable --now bandwidth-monitor
```

#### RHEL / Fedora

```bash
sudo rpm -i bandwidth-monitor-*.rpm
sudo vi /etc/bandwidth-monitor/env
sudo systemctl enable --now bandwidth-monitor
```

#### OpenWrt (stable, opkg)

```bash
opkg update && opkg install libpcap kmod-nf-conntrack-netlink
opkg install /tmp/bandwidth-monitor_*.ipk
vi /etc/bandwidth-monitor/env
/etc/init.d/bandwidth-monitor enable
/etc/init.d/bandwidth-monitor start
```

Optional GeoIP databases:
```bash
scp GeoLite2-Country.mmdb GeoLite2-ASN.mmdb root@router:/etc/bandwidth-monitor/
/etc/init.d/bandwidth-monitor restart
```

#### OpenWrt (snapshot, apk)

```bash
apk update && apk add libpcap kmod-nf-conntrack-netlink
apk add --allow-untrusted /tmp/bandwidth-monitor-*.apk
vi /etc/bandwidth-monitor/env
/etc/init.d/bandwidth-monitor enable
/etc/init.d/bandwidth-monitor start
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
```

### Using the Makefile

```bash
# Build, download GeoIP DBs, install to /opt/bandwidth-monitor,
# set up systemd service, and start
make install
```

This will:
1. Build the binary
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

### Systemd Service

The included `bandwidth-monitor.service` runs the binary with:
- `CAP_NET_RAW` and `CAP_NET_ADMIN` for packet capture and netlink access (no full root needed)
- `ProtectSystem=strict`, `ProtectHome=yes`, `PrivateTmp=yes` hardening
- Environment loaded from `/opt/bandwidth-monitor/.env`

---

## Configuration

All configuration is via environment variables. Copy the example file and edit:

```bash
cp env.example /opt/bandwidth-monitor/.env
chmod 0600 /opt/bandwidth-monitor/.env
```

### Environment Variables

#### Core

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTEN` | `:8080` | HTTP listen address (e.g. `198.51.100.1:8080`) |
| `DEVICE` | *(all)* | Network device for packet capture (e.g. `eth0`) |
| `PROMISCUOUS` | `true` | Enable promiscuous mode for packet capture (`true`/`false`) |
| `INTERFACES` | *(all)* | Comma-separated list of interfaces to display (e.g. `eth0,ppp0,wg0`). If not set, all interfaces are shown. |
| `GEO_COUNTRY` | `GeoLite2-Country.mmdb` | Path to GeoLite2 Country MMDB |
| `GEO_ASN` | `GeoLite2-ASN.mmdb` | Path to GeoLite2 ASN MMDB |

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

On a SPAN or mirror port, the kernel reports all mirrored traffic as RX on the interface, making the normal RX/TX split meaningless. Setting `SPAN_DEVICE` activates a pcap-based overlay that inspects IP headers and classifies direction using `LOCAL_NETS`:

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

## Architecture

```
main.go                   → entry point, env config, wires all components
collector/                → netlink-based interface stats (RTM_GETLINK/RTM_GETADDR), rates, 24h history, VPN routing
conntrack/                → netlink-based conntrack (NAT) table reader via ti-mo/conntrack
talkers/                  → pcap packet capture, per-IP tracking, 1-min bucket aggregation
resolver/                 → shared reverse-DNS resolver with TTL-based cache and bounded concurrency
latency/                  → continuous ICMP + HTTPS latency monitoring with rolling history
speedtest/                → HTTP-based speed test client (download/upload/ping against OpenSpeedTest servers)
debug/                    → traceroute (native ICMP), DNS checker (multi-server), resolver leak detection
handler/                  → HTTP REST API + SSE streaming handler
dns/                      → common DNS provider interface
adguard/                  → AdGuard Home API client (stats, top clients/domains)
nextdns/                  → NextDNS API client (stats, top clients/domains)
pihole/                   → Pi-hole v6 API client (stats, top clients/domains, upstreams)
wifi/                     → common WiFi provider interface
unifi/                    → UniFi controller API client (APs, SSIDs, clients, live rates)
omada/                    → TP-Link Omada controller API client (APs, SSIDs, clients, live rates)
geoip/                    → MaxMind MMDB GeoIP lookups (country, ASN)
static/
  index.html              → HTML shell with seven tabs (Traffic, NAT, DNS, WiFi, Monitor, Speed Test, Debug)
  app.js                  → all frontend JavaScript (charts, tables, SSE client)
  style.css               → full stylesheet (dark/light themes)
swiftbar/                 → macOS menu bar plugin
windows/                  → Windows system tray widget
gnome/                    → GNOME/Linux top-bar indicator
packaging/
  openwrt-Makefile        → OpenWrt package definition
  openwrt-files/
    bandwidth-monitor.init → procd init script for OpenWrt
    99-vpn-status         → OpenWrt hotplug script for VPN sentinel files
  postinstall.sh          → deb/rpm post-install script
  preremove.sh            → deb/rpm pre-remove script
nfpm.yaml                 → deb/rpm packaging config (nfpm)
.github/workflows/        → CI: builds deb, rpm, ipk, apk on push & tag
env.example               → example environment configuration
bandwidth-monitor.service → systemd unit file
flake.nix                 → Nix flake with package + NixOS module
Makefile                  → build, install, GeoIP download targets
```

---

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/interfaces` | GET | Current stats for all interfaces |
| `/api/interfaces/history` | GET | 24h time-series per interface |
| `/api/talkers/bandwidth` | GET | Top 10 by current bandwidth |
| `/api/talkers/volume` | GET | Top 10 by 24h volume |
| `/api/dns` | GET | DNS summary (AdGuard Home, NextDNS, or Pi-hole) |
| `/api/wifi` | GET | WiFi summary (UniFi or Omada) |
| `/api/latency` | GET | Latency monitoring status (ICMP + HTTPS probes) |
| `/api/conntrack` | GET | NAT / conntrack summary (connections, states, NAT types, entries) |
| `/api/speedtest/run` | POST | Start a speed test; streams progress as SSE (Server-Sent Events) |
| `/api/speedtest/results` | GET | Speed test history (last 50 results) and running status |
| `/api/debug/traceroute` | POST | ICMP traceroute with SSE progress; params: `target`, `count` (probes/hop), `maxttl` |
| `/api/debug/dns` | GET | DNS check against 14 servers + resolver leak test; params: `domain`, `type` |
| `/api/summary` | GET | Compact summary for menu bar clients |
| `/api/events` | GET | SSE stream — pushes all data every second (Server-Sent Events) |

---

## External Services Transparency

Every hardcoded external service that bandwidth-monitor or its components contact:

| Service | URL / IP | Component | Trigger | Data sent | Data received |
|---------|---------|-----------|---------|-----------|---------------|
| **FFMUC Speed Test** | [`speed.ffmuc.net`](https://speed.ffmuc.net) | Speed Test tab | User clicks "Start Test" | HTTP GET `/downloading`, POST `/upload` (random payload) | Download payload, upload ack |
| **FFMUC IP Check** | [`ip.ffmuc.net`](https://ip.ffmuc.net) | SwiftBar plugin | Every ~5 min (cached), **on by default** (`BW_SHOW_EXTERNAL_IP=false` to disable) | HTTPS GET (IPv4 + IPv6) | Router's public IPv4 and IPv6 address |
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

**No telemetry, no analytics, no crash reporting, no update checks.** The binary phones home to nothing.

---

## Notes

### Permissions

| Feature | Capability required |
|---------|-------------------|
| Interface stats, NAT tab | `CAP_NET_ADMIN` (or root) |
| Top talkers, SPAN mode | `CAP_NET_RAW` (or root) |
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
