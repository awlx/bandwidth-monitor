#!/bin/sh
set -eu

repository_name=bandwidth-monitor
repository_url=https://awlx.github.io/bandwidth-monitor/arch/\$arch
key_url=https://awlx.github.io/bandwidth-monitor/bandwidth-monitor.gpg.key
expected_fingerprint=2F9C7923C454355A9CF1232C4B621C9FAAEE0D71

if [ "$(id -u)" -ne 0 ]; then
	echo "run this repository setup as root" >&2
	exit 1
fi

for command in awk curl gpg grep mktemp pacman pacman-key; do
	if ! command -v "$command" >/dev/null 2>&1; then
		echo "required command not found: $command" >&2
		exit 1
	fi
done

key_file=$(mktemp)
trap 'rm -f "$key_file"' EXIT HUP INT TERM
curl -fsSL "$key_url" -o "$key_file"

fingerprint=$(gpg --batch --show-keys --with-colons "$key_file" |
	awk -F: '$1 == "fpr" { print $10; exit }')
if [ "$fingerprint" != "$expected_fingerprint" ]; then
	echo "repository signing key fingerprint does not match" >&2
	exit 1
fi

pacman-key --add "$key_file"
pacman-key --lsign-key "$expected_fingerprint"

if grep -Eq '^[[:space:]]*\[bandwidth-monitor\][[:space:]]*$' /etc/pacman.conf; then
	echo "the bandwidth-monitor repository is already configured"
else
	cat >>/etc/pacman.conf <<EOF

[$repository_name]
SigLevel = Required DatabaseOptional
Server = $repository_url
EOF
	echo "added the bandwidth-monitor repository to /etc/pacman.conf"
fi

pacman -Sy

cat <<'EOF'

Repository metadata refreshed. No packages were installed.
Install either or both packages explicitly:

  sudo pacman -Syu bandwidth-monitor
  sudo pacman -Syu bandwidth-top
  sudo pacman -Syu bandwidth-monitor bandwidth-top
EOF
