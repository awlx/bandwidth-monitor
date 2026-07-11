# Installation

The Bandwidth Monitor daemon supports Linux. The standalone `bandwidth-top`
terminal viewer supports Linux and macOS. They are built and packaged
independently; neither package requires the other.

## Release packages

[GitHub Releases](https://github.com/awlx/bandwidth-monitor/releases) provide:

| Format | Platforms |
|---|---|
| `.deb` | Debian, Ubuntu, and Raspbian on amd64 or arm64 |
| `.rpm` | Fedora, RHEL, and compatible distributions on amd64 or arm64 |
| `.ipk` | OpenWrt 23.05 targets |
| `.apk` | OpenWrt snapshot targets |
| `.tar.gz` | Standalone macOS `bandwidth-top` on amd64 or arm64 |
| Nix flake | NixOS and Nix on Linux |

Linux package formats have a `bandwidth-monitor` daemon package and a separate
`bandwidth-top` package. macOS archives contain only `bandwidth-top` and the
license, with a matching `.sha256` checksum.

## Debian and Ubuntu

### APT repository

```bash
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://awlx.github.io/bandwidth-monitor/bandwidth-monitor.gpg.key \
  | sudo gpg --dearmor -o /etc/apt/keyrings/bandwidth-monitor.gpg
echo "deb [signed-by=/etc/apt/keyrings/bandwidth-monitor.gpg] https://awlx.github.io/bandwidth-monitor stable main" \
  | sudo tee /etc/apt/sources.list.d/bandwidth-monitor.list
sudo apt update
sudo apt install bandwidth-monitor
```

Install the terminal viewer alone or alongside the daemon:

```bash
sudo apt install bandwidth-top
sudo apt install bandwidth-monitor bandwidth-top
```

### Downloaded packages

```bash
sudo dpkg -i bandwidth-monitor_*.deb
sudoedit /etc/bandwidth-monitor/env
sudo systemctl restart bandwidth-monitor
```

For the CLI only:

```bash
sudo dpkg -i bandwidth-top_*.deb
```

## Fedora and RHEL

```bash
sudo rpm -i bandwidth-monitor-*.rpm
sudoedit /etc/bandwidth-monitor/env
sudo systemctl enable --now bandwidth-monitor
```

For the CLI only:

```bash
sudo rpm -i bandwidth-top-*.rpm
```

## OpenWrt

The daemon needs the conntrack netlink kernel module for its NAT view.

### Stable releases with opkg

```bash
opkg update
opkg install kmod-nf-conntrack-netlink
opkg install /tmp/bandwidth-monitor_*.ipk
vi /etc/bandwidth-monitor/env
/etc/init.d/bandwidth-monitor enable
/etc/init.d/bandwidth-monitor start
```

Install the CLI independently with:

```bash
opkg install /tmp/bandwidth-top_*.ipk
```

### Snapshots with apk

```bash
apk update
apk add kmod-nf-conntrack-netlink
apk add --allow-untrusted /tmp/bandwidth-monitor-*.apk
vi /etc/bandwidth-monitor/env
/etc/init.d/bandwidth-monitor enable
/etc/init.d/bandwidth-monitor start
```

Install the CLI independently with:

```bash
apk add --allow-untrusted /tmp/bandwidth-top-*.apk
```

For GeoIP on a device with limited storage, a GeoLite2 Country database can be
used instead of the larger City database:

```bash
scp GeoLite2-Country.mmdb GeoLite2-ASN.mmdb root@router:/etc/bandwidth-monitor/
```

Then set `GEO_CITY` and `GEO_ASN` to those paths in
`/etc/bandwidth-monitor/env`.

## Nix

Run without installing:

```bash
nix run github:awlx/bandwidth-monitor
nix run github:awlx/bandwidth-monitor#bandwidth-top
```

### NixOS module

Add the repository as an input and import its module:

```nix
{
  inputs.bandwidth-monitor.url = "github:awlx/bandwidth-monitor";

  outputs = { nixpkgs, bandwidth-monitor, ... }: {
    nixosConfigurations.router = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        bandwidth-monitor.nixosModules.default
        {
          services.bandwidth-monitor = {
            enable = true;
            listenAddress = ":8080";
            settings.INTERFACES = "eth0,ppp0";
            # environmentFile = "/etc/bandwidth-monitor/env";
          };
        }
      ];
    };
  };
}
```

The flake includes GeoIP databases. Set `geoipDir` to a directory managed by
`services.geoipupdate` if you prefer independently updated databases.

## macOS standalone CLI

Download the archive matching the Mac architecture:

- `bandwidth-top_<version>_darwin_arm64.tar.gz` for Apple silicon
- `bandwidth-top_<version>_darwin_amd64.tar.gz` for Intel

Verify, extract, and install it:

```bash
shasum -a 256 -c bandwidth-top_<version>_darwin_<arch>.tar.gz.sha256
tar -xzf bandwidth-top_<version>_darwin_<arch>.tar.gz
sudo install -m 0755 bandwidth-top /usr/local/bin/bandwidth-top
sudo bandwidth-top
```

macOS capture uses native `/dev/bpf*` devices and does not require CGo or
libpcap. Running as root is the default. Administrators can instead grant a
dedicated trusted group read/write access to the BPF devices; do not use
world-readable or world-writable device permissions. The CLI supports Ethernet
(including 802.1Q/802.1ad VLAN), null/loopback, and raw-IP BPF link framing;
other Darwin link types are rejected explicitly. The selected interface must be
active, non-loopback, and have an assigned IPv4 or IPv6 address.

The macOS archive does not include or imply support for the Linux-only
`bandwidth-monitor` daemon.

## Build from source

Go 1.25 or newer is required.

```bash
git clone https://github.com/awlx/bandwidth-monitor.git
cd bandwidth-monitor
make build
make geoip          # optional
sudo ./bandwidth-monitor
```

Build the terminal viewer separately:

```bash
make build-top
sudo ./bandwidth-top
```

The same `make build-top` command builds the native standalone CLI on macOS.

## Standalone systemd installation

`make install` builds the daemon, downloads GeoIP databases, installs under
`/opt/bandwidth-monitor`, creates a private configuration file when needed, and
enables the included systemd service:

```bash
make install
sudoedit /opt/bandwidth-monitor/.env
sudo systemctl restart bandwidth-monitor
```

For a manual installation:

```bash
go build -o bandwidth-monitor .
sudo mkdir -p /opt/bandwidth-monitor
sudo install -m 0755 bandwidth-monitor /opt/bandwidth-monitor/
sudo install -m 0600 env.example /opt/bandwidth-monitor/.env
sudo install -m 0644 bandwidth-monitor.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now bandwidth-monitor
```

The standalone service grants `CAP_NET_RAW` and `CAP_NET_ADMIN`, applies
systemd hardening, and reads `/opt/bandwidth-monitor/.env`.

## After installation

Open `http://<host>:8080`, then review [Configuration](configuration.md).

Useful service commands:

```bash
systemctl status bandwidth-monitor
journalctl -u bandwidth-monitor -f
```
