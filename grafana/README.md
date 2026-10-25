# Grafana dashboard

`bandwidth-monitor.json` visualizes every metric exported on `/metrics`:
WAN overview, per-interface traffic, top talkers and clients, countries and
ASNs, latency, DNS, WiFi, conntrack/NAT, speed tests, and daemon health.

## Requirements

- Prometheus scraping each Bandwidth Monitor instance at `/metrics`
  (see [API reference](../docs/api.md#prometheus-metrics)).
- Grafana 10 or newer with a Prometheus data source.

## Import

1. In Grafana, open **Dashboards → New → Import**.
2. Upload `bandwidth-monitor.json` (or paste its contents).
3. Pick your Prometheus data source and import.

Or through the HTTP API:

```bash
jq '{dashboard: ., overwrite: true}' grafana/bandwidth-monitor.json |
  curl -fsS -H "Authorization: Bearer $GRAFANA_TOKEN" \
    -H 'Content-Type: application/json' \
    -X POST "$GRAFANA_URL/api/dashboards/db" -d @-
```

## Variables

| Variable | Purpose |
|---|---|
| `datasource` | Prometheus data source |
| `job` | Scrape job(s) exposing `bandwidth_monitor_build_info` |
| `instance` | One or more Bandwidth Monitor instances |
| `interface` | Interfaces for the per-interface panels |

Panels for subsystems that are not configured (for example DNS or WiFi
without a provider, or speed tests that have not run since the last restart)
show no data.
