#!/bin/sh
set -eu

repo=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail() {
	echo "packaging assertion failed: $*" >&2
	exit 1
}

assert_contains() {
	grep -F -- "$2" "$1" >/dev/null || fail "$1 does not contain $2"
}

assert_not_contains() {
	if grep -F -- "$2" "$1" >/dev/null; then
		fail "$1 unexpectedly contains $2"
	fi
}

assert_contains "$repo/bandwidth-monitor.openrc" "/opt/bandwidth-monitor/bandwidth-monitor"
assert_contains "$repo/packaging/bandwidth-monitor.openrc" 'command="/usr/bin/bandwidth-monitor"'
assert_contains "$repo/packaging/bandwidth-monitor.openrc" ': "${BANDWIDTH_MONITOR_CONFIG:=/etc/bandwidth-monitor/env}"'
assert_not_contains "$repo/packaging/bandwidth-monitor.openrc" "/opt/bandwidth-monitor"
assert_contains "$repo/packaging/bandwidth-monitor.service" "ExecStart=/usr/bin/bandwidth-monitor"
assert_contains "$repo/packaging/bandwidth-monitor.service" "EnvironmentFile=-/etc/bandwidth-monitor/env"
assert_not_contains "$repo/packaging/bandwidth-monitor.service" "/opt/bandwidth-monitor"

grep -Eo '(env|os\.Getenv)\("[A-Z0-9_]+"' "$repo/main.go" |
	sed 's/.*("//; s/"$//' | sort -u > "$tmp/app-env"
sort -u "$repo/packaging/runtime-env.list" > "$tmp/expected-env"
cmp "$tmp/expected-env" "$tmp/app-env" ||
	fail "runtime-env.list does not match variables consumed by main.go"

sed -n 's/^[[:space:]]*#[[:space:]]*\([A-Z][A-Z0-9_]*\)=.*/\1/p; s/^[[:space:]]*\([A-Z][A-Z0-9_]*\)=.*/\1/p' \
	"$repo/env.example" | sort -u > "$tmp/example-env"
grep -v '^GEO_COUNTRY$' "$tmp/expected-env" > "$tmp/documented-env"
cmp "$tmp/documented-env" "$tmp/example-env" ||
	fail "env.example does not document every supported runtime variable"

sed -n 's/^[[:space:]]*\([A-Z][A-Z0-9_]*\)="${.*/\1/p' \
	"$repo/packaging/openwrt-files/bandwidth-monitor.init" | sort -u > "$tmp/openwrt-env"
cmp "$tmp/expected-env" "$tmp/openwrt-env" ||
	fail "OpenWrt init does not forward every supported runtime variable"

assert_contains "$repo/nfpm.yaml" "dst: /usr/share/bandwidth-monitor/env.example"
assert_contains "$repo/nfpm.yaml" "dst: /usr/bin/bandwidth-top"
assert_contains "$repo/.github/workflows/release.yml" 'cp "$TOP_BINARY" "$PKG_DIR/usr/bin/bandwidth-top"'
assert_not_contains "$repo/nfpm.yaml" "dst: /etc/bandwidth-monitor/env"
assert_contains "$repo/nfpm.yaml" 'license: "AGPL-3.0-only"'
assert_contains "$repo/.github/workflows/release.yml" '-I "license:AGPL-3.0-only"'
assert_contains "$repo/packaging/postinstall.sh" 'if [ ! -e "$config_file" ]; then'
assert_contains "$repo/packaging/postinstall.sh" 'if [ -e "$config_file.dpkg-bak" ]; then'
assert_contains "$repo/packaging/postinstall.sh" 'mv "$config_file.dpkg-bak" "$config_file"'
assert_contains "$repo/packaging/postinstall.sh" 'install -m 0600 "$example_file" "$config_file"'
for script in preinstall.sh postinstall.sh postremove.sh; do
	assert_contains "$repo/packaging/$script" \
		"dpkg-maintscript-helper rm_conffile /etc/bandwidth-monitor/env"
done

older_nfpm=$("$repo/packaging/package-version.sh" nfpm branch main 100 1 0123456789abcdef)
newer_nfpm=$("$repo/packaging/package-version.sh" nfpm branch main 101 1 fedcba9876543210)
retry_nfpm=$("$repo/packaging/package-version.sh" nfpm branch main 101 2 fedcba9876543210)
older_openwrt=$("$repo/packaging/package-version.sh" openwrt branch main 100 1 0123456789abcdef)
newer_openwrt=$("$repo/packaging/package-version.sh" openwrt branch main 101 1 fedcba9876543210)
retry_openwrt=$("$repo/packaging/package-version.sh" openwrt branch main 101 2 fedcba9876543210)

[ "$older_nfpm" != "$newer_nfpm" ] || fail "nfpm snapshots are not unique"
[ "$newer_nfpm" != "$retry_nfpm" ] || fail "nfpm retries are not unique"
[ "$older_openwrt" != "$newer_openwrt" ] || fail "OpenWrt snapshots are not unique"
[ "$newer_openwrt" != "$retry_openwrt" ] || fail "OpenWrt retries are not unique"
[ "$("$repo/packaging/package-version.sh" nfpm tag v1.2.3 101 1 fedcba)" = "1.2.3" ] ||
	fail "tag version changed"

if command -v dpkg >/dev/null 2>&1; then
	dpkg --compare-versions "$older_nfpm" lt "$newer_nfpm" ||
		fail "nfpm snapshots are not ordered chronologically"
	dpkg --compare-versions "$newer_nfpm" lt "$retry_nfpm" ||
		fail "nfpm retries are not ordered"
fi

older_openwrt_number=${older_openwrt##*_git}
newer_openwrt_number=${newer_openwrt##*_git}
retry_openwrt_number=${retry_openwrt##*_git}
[ "$older_openwrt_number" -lt "$newer_openwrt_number" ] ||
	fail "OpenWrt snapshots are not ordered by run"
[ "$newer_openwrt_number" -lt "$retry_openwrt_number" ] ||
	fail "OpenWrt retries are not ordered"

if command -v nfpm >/dev/null 2>&1; then
	mkdir -p "$tmp/build/packaging"
	cp "$repo/nfpm.yaml" "$repo/env.example" "$tmp/build/"
	cp "$repo/packaging/bandwidth-monitor.service" \
		"$repo/packaging/bandwidth-monitor.openrc" \
		"$repo/packaging/preinstall.sh" \
		"$repo/packaging/postinstall.sh" \
		"$repo/packaging/preremove.sh" \
		"$repo/packaging/postremove.sh" \
		"$tmp/build/packaging/"
	printf '#!/bin/sh\nexit 0\n' > "$tmp/build/bandwidth-monitor"
	printf '#!/bin/sh\nexit 0\n' > "$tmp/build/bandwidth-top"
	chmod 0755 "$tmp/build/bandwidth-monitor"
	chmod 0755 "$tmp/build/bandwidth-top"
	(
		cd "$tmp/build"
		VERSION=$newer_nfpm GOARCH=amd64 nfpm package -p deb -f nfpm.yaml -t "$tmp/package.deb" >/dev/null
		VERSION=$newer_nfpm GOARCH=amd64 nfpm package -p rpm -f nfpm.yaml -t "$tmp/package.rpm" >/dev/null
	)
	[ -s "$tmp/package.deb" ] || fail "nfpm did not build a Debian package"
	[ -s "$tmp/package.rpm" ] || fail "nfpm did not build an RPM package"

	if command -v dpkg-deb >/dev/null 2>&1; then
		dpkg-deb --info "$tmp/package.deb" > "$tmp/deb-info"
		dpkg-deb --contents "$tmp/package.deb" > "$tmp/deb-contents"
		assert_contains "$tmp/deb-info" "Version: $newer_nfpm"
		assert_contains "$tmp/deb-contents" "./usr/bin/bandwidth-monitor"
		assert_contains "$tmp/deb-contents" "./usr/bin/bandwidth-top"
		assert_contains "$tmp/deb-contents" "./etc/init.d/bandwidth-monitor"
		assert_contains "$tmp/deb-contents" "./usr/share/bandwidth-monitor/env.example"
		assert_not_contains "$tmp/deb-contents" "./etc/bandwidth-monitor/env"
		dpkg-deb --control "$tmp/package.deb" "$tmp/control"
		[ ! -e "$tmp/control/conffiles" ] || fail "Debian package still declares conffiles"
		assert_contains "$tmp/control/preinst" \
			"dpkg-maintscript-helper rm_conffile /etc/bandwidth-monitor/env"
		assert_contains "$tmp/control/postinst" 'mv "$config_file.dpkg-bak" "$config_file"'
		assert_contains "$tmp/control/postrm" \
			"dpkg-maintscript-helper rm_conffile /etc/bandwidth-monitor/env"
	fi
fi

echo "packaging assertions passed"
