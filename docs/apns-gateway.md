# APNS Gateway

`apnsgateway` is a standalone relay binary that keeps the iOS app's Live Activity (Lock Screen widget + Dynamic Island) updating while the app is suspended, without requiring any APNs configuration on individual bandwidth-monitor instances.

## Why a separate binary?

Only the Apple Developer team that owns the app's bundle ID can push to it via APNs — which means the key must live somewhere. Rather than requiring every self-hoster to obtain their own paid developer account and APNs auth key, `apnsgateway` is the single place that holds the key:

- The iOS app registers its push token directly with the gateway, naming its own server's URL.
- The gateway polls that server's **existing, unmodified** `/api/interfaces` and `/api/interfaces/history` endpoints, builds the content-state, and pushes to APNs.
- **Individual bandwidth-monitor instances need zero APNs configuration or new code.**

`apnsgateway` is deliberately a separate binary (`make build-gateway`), not a mode flag on the main server — the main server assumes router-level access (raw sockets, netlink, often root) that a relay meant to run on any ordinary host has no use for.

## Building

```bash
make build-gateway
# produces: ./apns-gateway
```

Cross-compile for Linux (common deployment target):

```bash
GOOS=linux GOARCH=amd64 go build -o apns-gateway ./apnsgateway
```

## Configuration

All configuration is via environment variables. Every variable except the TLS paths and APNs credentials has a sensible default.

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTEN` | `:8443` | Listen address. The gateway always serves HTTPS (iOS App Transport Security rejects plain HTTP). |
| `TLS_CERT_FILE` | *(required)* | Path to the TLS certificate (PEM). |
| `TLS_KEY_FILE` | *(required)* | Path to the TLS private key (PEM). |
| `APNS_KEY_FILE` | *(required)* | Path to the APNs auth key (`.p8`) from [developer.apple.com](https://developer.apple.com) (Keys → Apple Push Notification service). Keep it `chmod 0600`. |
| `APNS_KEY_ID` | *(required)* | 10-character key ID from the Apple Developer portal. |
| `APNS_TEAM_ID` | *(required)* | Apple Developer team ID. |
| `APNS_BUNDLE_ID` | *(required)* | App bundle ID (e.g. `com.example.BandwidthMonitor`). |
| `APNS_PUSH_INTERVAL` | `10s` | How often to push an update to each registered device. |
| `APNS_MAX_RESPONSE_BYTES` | `16777216` (16 MiB) | Maximum response size accepted from a polled server. The `/api/interfaces/history` endpoint can return several MB per interface at the default 24h/1Hz retention, so this must be large enough to cover a full history response. |

## Running

```bash
export TLS_CERT_FILE=/etc/apns-gateway/server.crt
export TLS_KEY_FILE=/etc/apns-gateway/server.key
export APNS_KEY_FILE=/etc/apns-gateway/AuthKey_XXXXXXXXXX.p8
export APNS_KEY_ID=XXXXXXXXXX
export APNS_TEAM_ID=YOURTEAMID
export APNS_BUNDLE_ID=com.example.BandwidthMonitor
./apns-gateway
```

The gateway logs a startup line confirming all settings and then starts accepting registrations.

Graceful shutdown is handled automatically on `SIGINT`/`SIGTERM` (drains active connections, up to 5 seconds).

## API

### `POST /api/liveactivity/register`

Registers (or refreshes) a device's Live Activity push token. The iOS app calls this every time its Live Activity token changes and periodically to keep the subscription alive.

**Request body (JSON):**

```json
{
  "token": "<hex push token>",
  "environment": "sandbox|production",
  "serverURL": "https://your-bandwidth-monitor.example.com:8080",
  "interface": "eth0"
}
```

| Field | Description |
|-------|-------------|
| `token` | The device's APNs push token for this Live Activity, as a hex string. |
| `environment` | `sandbox` for Xcode dev builds, `production` for App Store / TestFlight builds. |
| `serverURL` | The public HTTPS (or HTTP) URL of the bandwidth-monitor instance this device is monitoring. Must resolve to a public IP address — private/loopback/link-local addresses are rejected (SSRF mitigation). |
| `interface` | The interface name to monitor (e.g. `eth0`). If empty or not found, the gateway picks the first WAN interface. |

**Response:** `{"status":"ok"}` on success, or an HTTP error with a plain-text message.

### `GET /healthz`

Returns `200 ok`. Use this for load balancer health checks or uptime monitoring.

## Security

### SSRF mitigation

Accepting an operator-supplied `serverURL` is inherently an SSRF shape (the gateway makes outbound requests to the supplied target). The gateway defends against this without requiring authentication — no secret is needed from iPhone users or self-hosting operators:

- `validateServerURL` rejects any URL whose host resolves to a private, loopback, link-local (including the `169.254.169.254` cloud-metadata pattern), multicast, or unspecified address.
- `checkPublicHost` is called again **immediately before every fetch**, not just at registration, to narrow the DNS-rebinding window where a hostname resolves to a public IP at registration time but to a private one later.
- Redirects are not followed.

This costs the real use case nothing: a bandwidth-monitor server can never legitimately live at a private address, since the gateway reaches it over the public internet.

### Other hygiene

- Per-source IP rate limiting on registration (20 registrations per 5-minute window).
- Global subscription cap (5 000 active subscriptions).
- Response size cap (`APNS_MAX_RESPONSE_BYTES`) on fetched server responses.
- Per-fetch timeout (8 seconds).
- Subscriptions that the app stops refreshing expire after 1 hour (TTL).
- Dead tokens (APNs 410 / `BadDeviceToken` / `Unregistered`) are dropped immediately.

### Registration is unauthenticated by design

No secret is required to register. The threat model is that registering a `serverURL` is a potential SSRF vector, handled above, not that registrations need to be gated — the app never surfaces any setup step to the end user, and requiring a shared secret would make self-hosting needlessly complicated.

## Notes

- **TODO (api-versioning):** The gateway currently fetches full history from `/api/interfaces/history` and discards all but the last hour for a single interface. Adding optional `?iface=&since=` query parameters to that endpoint would let the gateway request only the slice it needs, avoiding unnecessarily large responses. This is additive and backward-compatible on its own. See the `fetchHistory` TODO comment in `apnsgateway/relay.go` for details.
