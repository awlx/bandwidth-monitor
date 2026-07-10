# Integrations

Bandwidth Monitor includes lightweight clients for desktop panels and supports
an independently developed iOS app. All clients consume the daemon's HTTP API.

## macOS menu bar

`swiftbar/bandwidth-monitor.5s.sh` works with
[SwiftBar](https://github.com/swiftbar/SwiftBar) and
[xbar](https://xbarapp.com/). It requires `curl` and `jq`.

Copy the script to the application's plugin directory, make it executable, and
set a server when gateway discovery is not appropriate:

```bash
chmod +x bandwidth-monitor.5s.sh
BW_SERVER=http://198.51.100.1:8080 ./bandwidth-monitor.5s.sh
```

`BW_SERVERS` accepts a comma-separated failover list. `BW_PREFER_IFACE` selects
the interface shown in the title, and `BW_SHOW_EXTERNAL_IP=false` disables
public-address lookup.

## Windows system tray

Run `windows/bandwidth-monitor-tray.vbs` for a silent launch, or use the `.bat`
file for a visible console. PowerShell can also start it directly:

```powershell
.\bandwidth-monitor-tray.ps1 -Server http://198.51.100.1:8080 -PreferIface eth0
```

The widget uses PowerShell 5.1+ and Windows Forms. It discovers a monitor from
the default gateway when `-Server`/`BW_SERVER` is unset. Set
`BW_SHOW_EXTERNAL_IP=false` to disable public-address lookup.

## GNOME and AppIndicator panels

The Python indicator works on GNOME with AppIndicator support and on other
panels that implement the same protocol.

Debian and Ubuntu dependencies:

```bash
sudo apt install python3-gi gir1.2-gtk-3.0 \
  gir1.2-ayatanaappindicator3-0.1
```

Run it directly:

```bash
./gnome/bandwidth-monitor-indicator.py \
  --server http://198.51.100.1:8080
```

The included `gnome/bandwidth-monitor-indicator.desktop` can be copied to
`~/.config/autostart/`. Use `--show-external-ip false` or
`BW_SHOW_EXTERNAL_IP=false` to disable public-address lookup.

## iOS

[bandwidth-monitor-ios](https://github.com/yeled/bandwidth-monitor-ios) shows
live traffic and supports Lock Screen and Dynamic Island Live Activities.

The app can use a relay gateway for APNs updates without adding Apple
credentials to the monitored daemon. Operators who prefer to self-host that
component can build `apns-gateway` and follow
[APNS Gateway](apns-gateway.md). Direct APNs push from the daemon is also
available through the APNs variables in [`env.example`](../env.example).

## External services

Bandwidth Monitor has no telemetry, analytics, crash reporting, or update
checks. The following built-in features make outbound requests:

| Service | Component and trigger | Data sent | Disable or replace |
|---|---|---|---|
| `speed.ffmuc.net` | Speed test started by a user | Random download/upload test data | Set `SPEEDTEST_SERVER` |
| Configured latency targets | Continuous latency monitor | ICMP echo and HTTPS requests | Set `LATENCY_TARGETS` |
| Public DNS resolvers and resolver-test domains | DNS diagnostic started by a user | User-entered domain and diagnostic DNS queries | Do not run the diagnostic |
| `ip.ffmuc.net` | SwiftBar public-address display | Request revealing the monitor's public address | `BW_SHOW_EXTERNAL_IP=false` |
| `anycast-v4.ffmuc.net`, `anycast-v6.ffmuc.net` | Windows and GNOME public-address display | Request revealing the monitor's public address | `BW_SHOW_EXTERNAL_IP=false` |
| `ip.ffmuc.net/json` | `bandwidth-top` fallback enrichment | **Each observed globally routable peer IP needing fallback data** | Run with `--no-public` |

The default latency targets are listed in [`env.example`](../env.example).
DNS diagnostics query FFMUC, Cloudflare, Google, Quad9, OpenDNS,
`o-o.myaddr.l.google.com`, and `dnscheck.tools`.

`bandwidth-top` first uses local MMDB data and a ready discovered or configured
Bandwidth Monitor server. Public fallback is only used for missing data, but
when used it discloses the observed peer address. Private, loopback,
link-local, multicast, unspecified, and other non-global addresses are not
sent. `--no-public` disables this fallback, while `--no-server-discovery`
disables the one-time default-gateway monitor probe.

User-configured DNS and WiFi providers are contacted only when their
environment variables are set. FFMUC services are operated by
[Freie Netze München e.V.](https://ffmuc.net/); see its
[privacy policy](https://ffmuc.net/privacy/).

Frontend libraries, fonts, and map data are bundled locally and do not require
CDN requests. Their licenses are listed in
[`THIRD_PARTY_LICENSES.md`](../THIRD_PARTY_LICENSES.md).
