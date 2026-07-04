#!/usr/bin/env python3
"""
Bandwidth Monitor -- GNOME/Linux top-bar indicator.

Creates an AppIndicator (system tray / top bar) icon that polls the
bandwidth-monitor /api/summary endpoint every 5 seconds, showing live
RX/TX rates in the panel label and full details in the dropdown menu.

Works on GNOME (with AppIndicator/KStatusNotifier extension), KDE Plasma,
XFCE, Budgie, Cinnamon, MATE, and any desktop supporting libappindicator.

Dependencies:
  - Python 3.6+
  - PyGObject (gi): sudo apt install python3-gi gir1.2-gtk-3.0
  - AppIndicator:   sudo apt install gir1.2-ayatanaappindicator3-0.1
                    (or gir1.2-appindicator3-0.1 on older distros)
  - GNOME Shell:    install "AppIndicator and KStatusNotifierItem Support"
                    extension from extensions.gnome.org

Usage:
  ./bandwidth-monitor-indicator.py
  ./bandwidth-monitor-indicator.py --server http://198.51.100.1:8080
  ./bandwidth-monitor-indicator.py --prefer-iface eth0
  BW_SERVER=http://198.51.100.1:8080 ./bandwidth-monitor-indicator.py
"""

import argparse
import http.client
import json
import os
import signal
import ssl
import subprocess
import sys
import threading
import time
from urllib.parse import urlparse

import gi
gi.require_version("Gtk", "3.0")
try:
    gi.require_version("AyatanaAppIndicator3", "0.1")
    from gi.repository import AyatanaAppIndicator3 as AppIndicator3
except (ValueError, ImportError):
    try:
        gi.require_version("AppIndicator3", "0.1")
        from gi.repository import AppIndicator3
    except (ValueError, ImportError):
        print(
            "Error: neither AyatanaAppIndicator3 nor AppIndicator3 found.\n"
            "Install with: sudo apt install gir1.2-ayatanaappindicator3-0.1\n"
            "  (or gir1.2-appindicator3-0.1 on older distros)",
            file=sys.stderr,
        )
        sys.exit(1)

from gi.repository import Gtk, GLib


# -- Helpers ---------------------------------------------------------------

def auto_detect_server(port: int) -> str:
    """Detect the default gateway and build a server URL."""
    try:
        out = subprocess.check_output(
            ["ip", "route", "show", "default"], text=True, timeout=2
        )
        for line in out.splitlines():
            parts = line.split()
            if "via" in parts:
                gw = parts[parts.index("via") + 1]
                return f"http://{gw}:{port}"
    except Exception:
        pass
    return f"http://localhost:{port}"


def fmt_rate(bytes_per_sec: float) -> str:
    mbps = abs(bytes_per_sec) * 8 / 1e6
    if mbps >= 1:
        return f"{mbps:.1f} Mb/s"
    kbps = abs(bytes_per_sec) * 8 / 1e3
    return f"{kbps:.0f} Kb/s"


def fmt_rate_short(bytes_per_sec: float) -> str:
    """Compact rate for the panel label."""
    mbps = abs(bytes_per_sec) * 8 / 1e6
    if mbps >= 1000:
        return f"{mbps / 1000:.0f}G"
    if mbps >= 100:
        return f"{mbps:.0f}M"
    if mbps >= 1:
        return f"{mbps:.0f}M"
    kbps = abs(bytes_per_sec) * 8 / 1e3
    if kbps >= 1:
        return f"{kbps:.0f}K"
    return "0"


class _PersistentConn:
    """A single persistent HTTP(S) connection with automatic reconnect.

    Uses http.client with keep-alive.  When the server closes the idle
    connection (or it breaks for any reason), the next request transparently
    reconnects.  Only ONE socket is ever open per _PersistentConn instance.
    """

    _UA = "bandwidth-monitor-indicator/1.0"

    def __init__(self, url: str):
        p = urlparse(url)
        self._scheme = p.scheme
        self._host = p.hostname
        self._port = p.port or (443 if p.scheme == "https" else 80)
        self._ssl = ssl.create_default_context() if p.scheme == "https" else None
        self._conn: http.client.HTTPConnection | None = None

    def _connect(self):
        """Open a fresh connection, closing any old one first."""
        if self._conn is not None:
            try:
                self._conn.close()
            except Exception:
                pass
        if self._ssl:
            self._conn = http.client.HTTPSConnection(
                self._host, self._port, timeout=3, context=self._ssl
            )
        else:
            self._conn = http.client.HTTPConnection(
                self._host, self._port, timeout=3
            )

    def request(self, path: str = "/") -> bytes | None:
        """GET path and return the response body, or None on error.

        Retries exactly once on connection failure (stale keep-alive).
        """
        for attempt in range(2):
            try:
                if self._conn is None:
                    self._connect()
                self._conn.request("GET", path, headers={
                    "User-Agent": self._UA,
                    "Connection": "keep-alive",
                    "Host": self._host,
                })
                resp = self._conn.getresponse()
                data = resp.read()
                if resp.status == 200:
                    return data
                return None
            except Exception:
                # First attempt failed -- reconnect and retry
                self._connect()
        return None


# One persistent connection per endpoint (max 3 sockets total)
_conns: dict[str, _PersistentConn] = {}


def _get_conn(url: str) -> _PersistentConn:
    p = urlparse(url)
    key = f"{p.scheme}://{p.netloc}"
    if key not in _conns:
        _conns[key] = _PersistentConn(url)
    return _conns[key]


def fetch_summary(server: str) -> dict | None:
    data = _get_conn(server).request("/api/summary")
    if data is None:
        return None
    try:
        return json.loads(data)
    except Exception:
        return None


def fetch_external_ips() -> tuple:
    """Fetch public IPv4 and IPv6 via FFMUC anycast endpoints."""
    ip4, ip6 = "", ""
    data = _get_conn("https://anycast-v4.ffmuc.net").request("/")
    if data:
        ip4 = data.decode().strip()
    data = _get_conn("https://anycast-v6.ffmuc.net").request("/")
    if data:
        ip6 = data.decode().strip()
    return ip4, ip6


# -- Indicator -------------------------------------------------------------

class BWIndicator:
    EXT_IP_INTERVAL = 300  # seconds

    def __init__(
        self,
        server: str,
        prefer_iface: str,
        refresh: int,
        show_ext_ip: bool,
        auto_detect: bool = False,
        port: int = 8080,
    ):
        self.server = server
        self.prefer_iface = prefer_iface
        self.refresh = refresh
        self.show_ext_ip = show_ext_ip
        # Only re-detect the server on failure if it was auto-detected from
        # the default gateway in the first place -- an explicit --server/
        # BW_SERVER should never be silently overridden.
        self.auto_detect = auto_detect
        self.port = port
        self.ext_ip4 = ""
        self.ext_ip6 = ""
        self.ext_ip_last = 0.0

        self.indicator = AppIndicator3.Indicator.new(
            "bandwidth-monitor",
            "network-transmit-receive",
            AppIndicator3.IndicatorCategory.SYSTEM_SERVICES,
        )
        self.indicator.set_status(AppIndicator3.IndicatorStatus.ACTIVE)
        self.indicator.set_label(" -- ", "")
        self.indicator.set_title("Bandwidth Monitor")

        self.menu = Gtk.Menu()
        self._build_menu()
        self.indicator.set_menu(self.menu)

        # Start polling
        GLib.timeout_add_seconds(self.refresh, self._tick)
        # Fire immediately
        GLib.idle_add(self._tick)

    def _build_menu(self):
        # WAN header
        self.wan_item = Gtk.MenuItem(label="WAN: --")
        self.wan_item.set_sensitive(False)
        self.menu.append(self.wan_item)

        # External IPs
        self.ext_ip4_item = Gtk.MenuItem(label="")
        self.ext_ip4_item.set_sensitive(False)
        self.ext_ip4_item.set_visible(False)
        self.menu.append(self.ext_ip4_item)

        self.ext_ip6_item = Gtk.MenuItem(label="")
        self.ext_ip6_item.set_sensitive(False)
        self.ext_ip6_item.set_visible(False)
        self.menu.append(self.ext_ip6_item)

        self.menu.append(Gtk.SeparatorMenuItem())

        # Traffic header
        traffic_hdr = Gtk.MenuItem(label="Traffic")
        traffic_hdr.set_sensitive(False)
        self.menu.append(traffic_hdr)

        # Interface items (up to 10)
        self.iface_items = []
        for _ in range(10):
            item = Gtk.MenuItem(label="")
            item.set_sensitive(False)
            item.set_visible(False)
            self.menu.append(item)
            self.iface_items.append(item)

        # DNS section
        self.sep_dns = Gtk.SeparatorMenuItem()
        self.sep_dns.set_visible(False)
        self.menu.append(self.sep_dns)
        self.dns_header = Gtk.MenuItem(label="DNS")
        self.dns_header.set_sensitive(False)
        self.dns_header.set_visible(False)
        self.menu.append(self.dns_header)
        self.dns_items = []
        for _ in range(3):
            item = Gtk.MenuItem(label="")
            item.set_sensitive(False)
            item.set_visible(False)
            self.menu.append(item)
            self.dns_items.append(item)

        # WiFi section
        self.sep_wifi = Gtk.SeparatorMenuItem()
        self.sep_wifi.set_visible(False)
        self.menu.append(self.sep_wifi)
        self.wifi_header = Gtk.MenuItem(label="WiFi")
        self.wifi_header.set_sensitive(False)
        self.wifi_header.set_visible(False)
        self.menu.append(self.wifi_header)
        self.wifi_items = []
        for _ in range(2):
            item = Gtk.MenuItem(label="")
            item.set_sensitive(False)
            item.set_visible(False)
            self.menu.append(item)
            self.wifi_items.append(item)

        # NAT section
        self.sep_nat = Gtk.SeparatorMenuItem()
        self.sep_nat.set_visible(False)
        self.menu.append(self.sep_nat)
        self.nat_header = Gtk.MenuItem(label="NAT - Conntrack")
        self.nat_header.set_sensitive(False)
        self.nat_header.set_visible(False)
        self.menu.append(self.nat_header)
        self.nat_items = []
        for _ in range(3):
            item = Gtk.MenuItem(label="")
            item.set_sensitive(False)
            item.set_visible(False)
            self.menu.append(item)
            self.nat_items.append(item)

        # Footer
        self.menu.append(Gtk.SeparatorMenuItem())

        open_dash = Gtk.MenuItem(label="Open Dashboard")
        open_dash.connect("activate", self._open_dashboard)
        self.menu.append(open_dash)

        self.server_item = Gtk.MenuItem(label=f"Server: {self.server}")
        self.server_item.set_sensitive(False)
        self.menu.append(self.server_item)

        self.menu.append(Gtk.SeparatorMenuItem())

        quit_item = Gtk.MenuItem(label="Quit")
        quit_item.connect("activate", lambda _: Gtk.main_quit())
        self.menu.append(quit_item)

        self.menu.show_all()
        # Hide optional items after show_all
        self.ext_ip4_item.set_visible(False)
        self.ext_ip6_item.set_visible(False)
        for item in self.iface_items:
            item.set_visible(False)
        self.sep_dns.set_visible(False)
        self.dns_header.set_visible(False)
        for item in self.dns_items:
            item.set_visible(False)
        self.sep_wifi.set_visible(False)
        self.wifi_header.set_visible(False)
        for item in self.wifi_items:
            item.set_visible(False)
        self.sep_nat.set_visible(False)
        self.nat_header.set_visible(False)
        for item in self.nat_items:
            item.set_visible(False)

    def _open_dashboard(self, _widget):
        try:
            subprocess.Popen(["xdg-open", self.server])
        except Exception:
            pass

    def _update_external_ips(self):
        if not self.show_ext_ip:
            return
        now = time.time()
        if now - self.ext_ip_last < self.EXT_IP_INTERVAL:
            return
        self.ext_ip_last = now
        # Run in background to avoid blocking GTK main loop
        def _fetch():
            ip4, ip6 = fetch_external_ips()
            GLib.idle_add(self._set_ext_ips, ip4, ip6)
        threading.Thread(target=_fetch, daemon=True).start()

    def _set_ext_ips(self, ip4, ip6):
        self.ext_ip4 = ip4
        self.ext_ip6 = ip6
        if ip4:
            self.ext_ip4_item.set_label(f"  IPv4: {ip4}")
            self.ext_ip4_item.set_visible(True)
        else:
            self.ext_ip4_item.set_visible(False)
        if ip6:
            self.ext_ip6_item.set_label(f"  IPv6: {ip6}")
            self.ext_ip6_item.set_visible(True)
        else:
            self.ext_ip6_item.set_visible(False)
        return False  # remove from idle

    def _redetect_server(self) -> bool:
        """Re-run gateway auto-detection and switch servers if it changed.

        The gateway can change after the indicator has started (DHCP lease
        renewal, router reboot, switching networks, VPN connect/disconnect),
        which otherwise leaves the indicator stuck polling a dead address
        until it's manually restarted. Returns True if the server changed.
        """
        if not self.auto_detect:
            return False
        new_server = auto_detect_server(self.port)
        if new_server == self.server:
            return False
        print(
            f"bandwidth-monitor indicator: gateway changed, "
            f"switching server from {self.server} to {new_server}"
        )
        self.server = new_server
        self.server_item.set_label(f"Server: {self.server}")
        return True

    def _tick(self):
        data = fetch_summary(self.server)

        if not data and self._redetect_server():
            data = fetch_summary(self.server)

        if not data:
            self.indicator.set_label(" -- ", "")
            self.indicator.set_icon_full("network-error", "unreachable")
            self.wan_item.set_label("Server unreachable")
            self._hide_sections()
            return True

        if data.get("app") != "bandwidth-monitor":
            self.indicator.set_label(" ?? ", "")
            self.wan_item.set_label("Not a bandwidth-monitor instance")
            self._hide_sections()
            return True

        ifaces = data.get("interfaces", [])
        active = sorted(
            [i for i in ifaces if i.get("state") in ("up", "unknown")],
            key=lambda i: -(i.get("rx_rate", 0) + i.get("tx_rate", 0)),
        )
        down = [i for i in ifaces if i.get("state") not in ("up", "unknown")]

        # Pick primary interface
        pri = None
        if self.prefer_iface:
            pri = next((i for i in active if i["name"] == self.prefer_iface), None)
        if not pri:
            pri = next((i for i in active if i.get("wan")), None)
        if not pri and active:
            pri = active[0]
        if not pri:
            pri = {"name": "?", "rx_rate": 0, "tx_rate": 0}

        # Panel label with live rates
        vpn_tag = " [VPN]" if data.get("vpn") else ""
        rx_short = fmt_rate_short(pri.get("rx_rate", 0))
        tx_short = fmt_rate_short(pri.get("tx_rate", 0))
        self.indicator.set_label(
            f" \u2193{rx_short} \u2191{tx_short}{vpn_tag} ", ""
        )
        self.indicator.set_icon_full(
            "network-transmit-receive", "Bandwidth Monitor"
        )

        # WAN line
        if self.prefer_iface and any(
            i["name"] == self.prefer_iface for i in active
        ):
            self.wan_item.set_label(f"WAN: {self.prefer_iface} (preferred)")
        elif pri.get("wan"):
            self.wan_item.set_label(f"WAN: {pri['name']}")
        else:
            self.wan_item.set_label(f"WAN: {pri['name']} (highest rate)")

        # External IPs
        self._update_external_ips()

        # Interfaces
        all_ifaces = active + down
        for idx, item in enumerate(self.iface_items):
            if idx < len(all_ifaces):
                iface = all_ifaces[idx]
                if iface.get("state") in ("up", "unknown"):
                    rx = fmt_rate(iface.get("rx_rate", 0))
                    tx = fmt_rate(iface.get("tx_rate", 0))
                    item.set_label(f"  {iface['name']}: \u2193{rx}  \u2191{tx}")
                else:
                    item.set_label(f"  {iface['name']}: down")
                item.set_visible(True)
            else:
                item.set_visible(False)

        # DNS
        dns = data.get("dns")
        if dns:
            self.sep_dns.set_visible(True)
            prov = dns.get("provider_name", "DNS")
            self.dns_header.set_label(f"DNS - {prov}")
            self.dns_header.set_visible(True)
            self.dns_items[0].set_label(f"  Queries:  {dns.get('total_queries', 0)}")
            self.dns_items[0].set_visible(True)
            block_pct = round(dns.get("block_pct", 0), 1)
            self.dns_items[1].set_label(
                f"  Blocked:  {dns.get('blocked', 0)} ({block_pct}%)"
            )
            self.dns_items[1].set_visible(True)
            lat = round(dns.get("latency_ms", 0), 1)
            self.dns_items[2].set_label(f"  Latency:  {lat} ms")
            self.dns_items[2].set_visible(True)
        else:
            self.sep_dns.set_visible(False)
            self.dns_header.set_visible(False)
            for item in self.dns_items:
                item.set_visible(False)

        # WiFi
        wifi = data.get("wifi")
        if wifi:
            self.sep_wifi.set_visible(True)
            prov = wifi.get("provider_name", "WiFi")
            self.wifi_header.set_label(f"WiFi - {prov}")
            self.wifi_header.set_visible(True)
            self.wifi_items[0].set_label(f"  APs:      {wifi.get('aps', 0)}")
            self.wifi_items[0].set_visible(True)
            self.wifi_items[1].set_label(f"  Clients:  {wifi.get('clients', 0)}")
            self.wifi_items[1].set_visible(True)
        else:
            self.sep_wifi.set_visible(False)
            self.wifi_header.set_visible(False)
            for item in self.wifi_items:
                item.set_visible(False)

        # NAT
        nat = data.get("nat")
        if nat:
            self.sep_nat.set_visible(True)
            self.nat_header.set_visible(True)
            usage = round(nat.get("usage_pct", 0), 1)
            self.nat_items[0].set_label(
                f"  Connections: {nat.get('total', 0)}/{nat.get('max', 0)} ({usage}%)"
            )
            self.nat_items[0].set_visible(True)
            self.nat_items[1].set_label(
                f"  IPv4: {nat.get('ipv4', 0)}  IPv6: {nat.get('ipv6', 0)}"
            )
            self.nat_items[1].set_visible(True)
            self.nat_items[2].set_label(
                f"  SNAT: {nat.get('snat', 0)}  DNAT: {nat.get('dnat', 0)}"
            )
            self.nat_items[2].set_visible(True)
        else:
            self.sep_nat.set_visible(False)
            self.nat_header.set_visible(False)
            for item in self.nat_items:
                item.set_visible(False)

        return True  # keep timer alive

    def _hide_sections(self):
        for item in self.iface_items:
            item.set_visible(False)
        self.ext_ip4_item.set_visible(False)
        self.ext_ip6_item.set_visible(False)
        self.sep_dns.set_visible(False)
        self.dns_header.set_visible(False)
        for item in self.dns_items:
            item.set_visible(False)
        self.sep_wifi.set_visible(False)
        self.wifi_header.set_visible(False)
        for item in self.wifi_items:
            item.set_visible(False)
        self.sep_nat.set_visible(False)
        self.nat_header.set_visible(False)
        for item in self.nat_items:
            item.set_visible(False)


# -- Main ------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(
        description="Bandwidth Monitor GNOME/Linux indicator"
    )
    parser.add_argument(
        "--server",
        default=os.environ.get("BW_SERVER", ""),
        help="Server URL (default: auto-detect from default gateway)",
    )
    parser.add_argument(
        "--port",
        type=int,
        default=int(os.environ.get("BW_PORT", "8080")),
        help="Port for auto-detection (default: 8080)",
    )
    parser.add_argument(
        "--prefer-iface",
        default=os.environ.get("BW_PREFER_IFACE", ""),
        help="Preferred interface name for the panel label",
    )
    parser.add_argument(
        "--refresh",
        type=int,
        default=5,
        help="Polling interval in seconds (default: 5)",
    )
    parser.add_argument(
        "--show-external-ip",
        default=os.environ.get("BW_SHOW_EXTERNAL_IP", "true"),
        help="Query FFMUC anycast for public IPs (default: true)",
    )
    args = parser.parse_args()

    auto_detect = not args.server
    server = args.server or auto_detect_server(args.port)
    show_ext = args.show_external_ip.lower() == "true"

    print(f"bandwidth-monitor indicator: polling {server} every {args.refresh}s")
    if show_ext:
        print("External IP lookup enabled (anycast-v4/v6.ffmuc.net, cached ~5 min)")

    # Allow clean Ctrl+C
    signal.signal(signal.SIGINT, signal.SIG_DFL)

    BWIndicator(
        server,
        args.prefer_iface,
        args.refresh,
        show_ext,
        auto_detect=auto_detect,
        port=args.port,
    )
    Gtk.main()


if __name__ == "__main__":
    main()
