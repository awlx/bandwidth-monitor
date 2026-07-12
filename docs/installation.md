# Installation

The Bandwidth Monitor daemon supports Linux. The standalone `bandwidth-top`
terminal viewer supports Linux and macOS. They are built and packaged
independently; neither package requires the other.

## Release packages

[GitHub Releases](https://github.com/awlx/bandwidth-monitor/releases) and the
project package repositories provide:

| Format | Platforms |
|---|---|
| `.deb` | Debian, Ubuntu, and Raspbian on amd64 or arm64 |
| `.rpm` | Fedora, RHEL, and compatible distributions on amd64 or arm64 |
| `.pkg.tar.zst` | Arch Linux and Arch Linux ARM on x86_64 or aarch64 |
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

## Arch Linux

Add the signed repository and refresh package databases:

```bash
curl -fsSL https://awlx.github.io/bandwidth-monitor/install-arch-repo.sh | sudo sh
```

The setup command does not install or upgrade any packages. Install the daemon,
the terminal viewer, or both explicitly:

```bash
sudo pacman -Syu bandwidth-monitor
sudo pacman -Syu bandwidth-top
sudo pacman -Syu bandwidth-monitor bandwidth-top
```

Enable the daemon after configuring it:

```bash
sudoedit /etc/bandwidth-monitor/env
sudo systemctl enable --now bandwidth-monitor
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

### Homebrew cask

The repository is also a custom Homebrew tap. Install the standalone CLI with:

```bash
brew tap awlx/bandwidth-monitor https://github.com/awlx/bandwidth-monitor
brew install --cask awlx/bandwidth-monitor/bandwidth-top
```

Update or remove it with:

```bash
brew upgrade --cask awlx/bandwidth-monitor/bandwidth-top
brew uninstall --cask awlx/bandwidth-monitor/bandwidth-top
```

The cask becomes available after the first release containing the macOS
archives and its generated cask update are published. It installs only the
`bandwidth-top` binary; it does not install or run the Linux-only daemon.

### Release archive

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

### Maintaining the cask

Every successful tagged **Build & Release** run automatically starts the
**Update Homebrew cask** workflow from the trusted default branch. It first
confirms that the upstream run completed the release job and that the tag still
resolves to the released commit. It then downloads the four expected Darwin
assets, verifies both checksum sidecars, archive payloads, architectures, and
online notarization tickets, runs Homebrew checks, and creates or updates
`automation/update-bandwidth-top-cask`. Required checks merge that pull request
automatically. No per-release command, approval, or merge is required.

Retries are idempotent: the workflow is serialized, uses one stable update
branch, and produces no commit or duplicate pull request when the cask already
matches the release. It never writes directly to the default branch or mutates
the release tag.

Complete this one-time repository setup before creating the first
cask-capable release:

1. Enable repository auto-merge.
2. Enable immutable releases so published tags and assets cannot change.
3. Protect `main`, require the `homebrew-cask` and `release-checks` status
   checks, and do not require human approval for the automated cask PR.
4. Install a repository-scoped GitHub App with Administration (read), Checks
   (read), Commit statuses (read), Contents (write), and Pull requests (write).
   Store its client ID as the `CASK_UPDATE_APP_CLIENT_ID` repository variable
   and its private key as the `CASK_UPDATE_APP_PRIVATE_KEY` repository secret.
5. Configure the Apple signing and notarization secrets listed below.

The GitHub App is necessary because pull requests created with the repository
`GITHUB_TOKEN` do not trigger the required checks. The workflow requests only
the listed permissions from the installation token and fails explicitly when
the App, auto-merge, branch protection, or required checks are missing. No
personal access token is used.

Secret and variable names can be checked during setup, but their presence does
not prove that the stored credentials are valid. Workflow code never reads
secret values back from GitHub or prints them. At runtime, GitHub validates the
App credentials when issuing the installation token, while `security`,
`codesign`, and `notarytool` validate the Apple credentials. Invalid values
stop the workflow before a release or cask pull request can be published.

For the exact install command above to work without bypassing macOS quarantine,
configure these Actions secrets before creating the release tag:

- `MACOS_SIGNING_CERTIFICATE`: base64-encoded Developer ID Application
  certificate in PKCS#12 format
- `MACOS_SIGNING_CERTIFICATE_PASSWORD`
- `MACOS_SIGNING_IDENTITY`
- `MACOS_NOTARY_KEY`: base64-encoded App Store Connect API private key
- `MACOS_NOTARY_KEY_ID`
- `MACOS_NOTARY_ISSUER_ID`

After the smoke workflow reaches the default branch and before creating the
first tagged macOS release, validate all six credentials once with:

```bash
gh workflow run test-apple-signing.yml \
  --repo awlx/bandwidth-monitor \
  --ref main \
  -f ref=main
```

The workflow is named **Test Apple signing**. GitHub only permits manual
dispatch after the workflow file exists on the default branch, so this command
must run after the workflow is merged. The `--ref main` option selects the
trusted workflow definition; the `ref=main` input selects the exact source ref
to build. The selected branch, tag, or commit must already be in `main` history;
unmerged pull request code is rejected before credentials are exposed.

The manual smoke runs natively on Apple silicon and Intel macOS runners. It
builds an explicitly test-only version, signs and notarizes it, verifies
the signature and forces an online lookup of its notarization ticket, creates
deterministic local release-like archives and checksums, and performs cask
generation, audit, install, and uninstall checks. It repeats signature and
online ticket verification for the installed native binary. It creates no tag,
release, artifact, branch, or pull request and pushes nothing. Credential files
and the ephemeral keychain are deleted even when a job fails. This is a
one-time credential validation, not a recurring release task; successful tagged
releases continue to update the cask automatically.

The tag build imports the certificate into an ephemeral keychain, signs each
native binary with the hardened runtime and a secure timestamp, submits it to
Apple's notary service, and deletes the keychain before the job ends. If none
of these secrets are configured, existing Darwin archive production remains
unchanged, but the cask updater rejects that unsigned release. A partial secret
configuration fails before publishing the affected archive.

Release builds create a draft, upload every asset, and only then publish it.
With release immutability enabled, GitHub locks the tag and assets at that
point. If immutability is not enabled, the release remains available but the
automatic cask update fails safely without opening or merging a pull request.

Standalone executables and tar archives cannot carry a stapled notarization
ticket. The signing workflow waits for the notary service to return `Accepted`,
then `codesign --check-notarization` forces an independent online lookup of the
ticket while `-R=notarized` requires that ticket to exist and the raw
executable's signature is verified. The automatic cask updater performs the
same online signature and ticket verification for both architecture binaries
before opening its pull request. It does not use `spctl`'s app-bundle
assessment for these standalone CLI executables.

Never create the cask before both immutable architecture archives exist: the
generator requires exact SHA-256 checksums and rejects missing, unexpected, or
incorrectly built assets.

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
