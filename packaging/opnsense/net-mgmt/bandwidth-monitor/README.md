# os-bandwidth-monitor (skeleton)

OPNsense plugin skeleton for running the bandwidth-monitor daemon as a managed
service. It follows the layout of the
[opnsense/plugins](https://github.com/opnsense/plugins) repository, so the
`net-mgmt/bandwidth-monitor` directory can be copied into a plugins checkout
and built with `make package`.

> **Status:** untested on a real OPNsense installation. The rc.d script it
> drives and the daemon itself have been tested on FreeBSD 14; the PHP, XML,
> and configd template files have only been syntax-checked and rendered
> outside OPNsense.

## What it does

- Adds **Services → Bandwidth Monitor** with settings for `LISTEN`,
  `INTERFACES`, `WAN_INTERFACE`, `LOCAL_NETS`, and `METRICS_ENABLED`.
- Generates `/etc/rc.conf.d/bandwidth_monitor` and
  `/usr/local/etc/bandwidth-monitor/env` from those settings through configd
  templates, and starts, stops, and restarts the service via
  `/usr/local/etc/rc.d/bandwidth_monitor`.
- Registers the service in the OPNsense service list and dashboard widget.

## What it does not do yet

- It does not install the daemon. `PLUGIN_DEPENDS` would need a FreeBSD port
  or package for `bandwidth-monitor`, which does not exist yet. Until then,
  install the release archive first (see
  [Installation](../../../../docs/installation.md#freebsd-and-opnsense)).
- It does not embed the dashboard in the OPNsense UI. Open the dashboard at
  the configured `LISTEN` address. The default `127.0.0.1:8080` is only
  reachable through an SSH tunnel; set a LAN address to reach it directly, and
  keep it off WAN.
- It exposes only a subset of the settings in `env.example`. Other variables
  can be added as model fields and template lines.

## Manual install for development

On a test OPNsense box, with the daemon archive already installed:

```sh
cp -R src/* /usr/local/
service configd restart
configctl template reload OPNsense/BandwidthMonitor
```

Then reload the web UI and open **Services → Bandwidth Monitor**.
