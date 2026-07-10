# Configuration

Bandwidth Monitor is configured with environment variables. Use
[`env.example`](../env.example) as the canonical list of supported settings.

| Installation | File |
|---|---|
| Debian, RPM, or OpenWrt package | `/etc/bandwidth-monitor/env` |
| Standalone `/opt` installation | `/opt/bandwidth-monitor/.env` |
| NixOS module | `services.bandwidth-monitor.settings` or `environmentFile` |

Keep the file owned by the service administrator and mode `0600`; provider
passwords, API keys, and private key paths may be present.

## Core settings

```bash
LISTEN=:8080
INTERFACES=eth0,ppp0,wg0
WAN_INTERFACE=ppp0
```

- `LISTEN` controls the web bind address.
- `INTERFACES` limits both displayed interfaces and packet capture. Without it,
  all non-loopback interfaces are used.
- `WAN_INTERFACE` overrides automatic WAN detection.
- `PROMISCUOUS=false` disables promiscuous packet capture if it is not needed.

## HTTPS

Both certificate and key must contain PEM data:

```bash
LISTEN=:8443
LISTEN_PROTOCOL=https
TLS_CERT_FILE=/etc/bandwidth-monitor/server.crt
TLS_KEY_FILE=/etc/bandwidth-monitor/server.key
```

The daemon exits at startup when HTTPS is selected and either file is missing
or invalid.

## GeoIP

GeoIP is optional. Without databases, location and ASN information is omitted.

```bash
GEO_CITY=/var/lib/GeoIP/GeoLite2-City.mmdb
GEO_ASN=/var/lib/GeoIP/GeoLite2-ASN.mmdb
```

`GEO_CITY` may point to a smaller GeoLite2 Country database when city
coordinates are not needed.

## DNS provider

Configure one provider. If several are present, the first configured provider
is used.

### AdGuard Home

```bash
ADGUARD_URL=http://adguard.example.local
ADGUARD_USER=monitor
ADGUARD_PASS=change-me
```

### NextDNS

```bash
NEXTDNS_PROFILE=abc123
NEXTDNS_API_KEY=change-me
```

### Pi-hole

```bash
PIHOLE_URL=http://pi.hole
PIHOLE_PASSWORD=change-me
```

## WiFi provider

Configure either UniFi or Omada.

### UniFi

```bash
UNIFI_URL=https://unifi.example.local:8443
UNIFI_USER=monitor
UNIFI_PASS=change-me
UNIFI_SITE=default
```

Legacy controllers and UniFi OS devices are detected automatically.

### Omada

```bash
OMADA_URL=https://omada.example.local
OMADA_USER=monitor
OMADA_PASS=change-me
OMADA_SITE=Default
```

## Conntrack accounting

The NAT view needs `nf_conntrack` and `CAP_NET_ADMIN`. Per-flow byte and packet
counters additionally need conntrack accounting:

```bash
sudo sysctl -w net.netfilter.nf_conntrack_acct=1
echo 'net.netfilter.nf_conntrack_acct=1' \
  | sudo tee /etc/sysctl.d/90-bandwidth-monitor.conf
```

## Routed and mirrored traffic

Local networks are normally discovered from interface addresses. Override them
when routed prefixes are missing or when capturing a SPAN/mirror port:

```bash
LOCAL_NETS=192.0.2.0/24,2001:db8::/48
SPAN_DEVICE=eth1
```

On a mirror port, `SPAN_DEVICE` classifies direction from IP headers and
`LOCAL_NETS` because the kernel reports all mirrored packets as received.
This mode requires `CAP_NET_RAW`.

## VPN status on OpenWrt

The OpenWrt package installs a hotplug helper that creates status files when
configured WireGuard interfaces change state:

```bash
VPN_STATUS_FILES=wg0=/run/wg0-active,wg1=/run/wg1-active
```

The dashboard marks an interface when its corresponding file exists.

## Diagnostics

Override the built-in speed test endpoint or latency targets when desired:

```bash
SPEEDTEST_SERVER=https://speed.ffmuc.net
LATENCY_TARGETS=anycast01.ffmuc.net,anycast02.ffmuc.net,dns.quad9.net
```

Latency targets are probed with ICMP and HTTPS. See
[external services](integrations.md#external-services) before exposing the
daemon to a network with strict egress requirements.

## iOS Live Activity

Most installations need no APNs configuration. For operators using their own
Apple credentials, the relevant variables are documented in
[`env.example`](../env.example); the separate relay is documented in
[APNS Gateway](apns-gateway.md).
