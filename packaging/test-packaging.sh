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
assert_not_contains "$repo/nfpm.yaml" "dst: /usr/bin/bandwidth-top"
assert_contains "$repo/nfpm-top.yaml" "name: bandwidth-top"
assert_contains "$repo/nfpm-top.yaml" "dst: /usr/bin/bandwidth-top"
assert_not_contains "$repo/nfpm-top.yaml" "/etc/"
assert_not_contains "$repo/nfpm-top.yaml" "/var/"
assert_not_contains "$repo/nfpm-top.yaml" "scripts:"
assert_not_contains "$repo/nfpm-top.yaml" "depends:"
assert_contains "$repo/.github/workflows/release.yml" 'cp "$TOP_BINARY" "$TOP_PKG_DIR/usr/bin/bandwidth-top"'
assert_contains "$repo/.github/workflows/release.yml" 'bandwidth-top_${VERSION}_${ARCH}.ipk'
assert_contains "$repo/.github/workflows/release.yml" 'bandwidth-top-${VERSION}-r1_${ARCH}.apk'
assert_contains "$repo/.github/workflows/release.yml" 'bandwidth-monitor/version.Version=${PACKAGE_VERSION}'
assert_contains "$repo/.github/workflows/release.yml" 'bandwidth-monitor/version.Version=${VERSION}'
assert_contains "$repo/.github/workflows/release.yml" './packaging/create-darwin-top-archive.sh'
assert_contains "$repo/.github/workflows/release.yml" './packaging/sign-and-notarize-darwin.sh'
assert_contains "$repo/.github/workflows/update-homebrew-cask.yml" './packaging/update-homebrew-cask.sh'
assert_contains "$repo/.github/workflows/update-homebrew-cask.yml" './packaging/verify-homebrew-release.sh'
assert_contains "$repo/.github/workflows/update-homebrew-cask.yml" 'needs: validate-release'
assert_contains "$repo/.github/workflows/update-homebrew-cask.yml" 'Regenerate cask without executing release code'
assert_contains "$repo/.github/workflows/update-homebrew-cask.yml" 'workflow_run:'
assert_contains "$repo/.github/workflows/update-homebrew-cask.yml" '- Build & Release'
assert_contains "$repo/.github/workflows/update-homebrew-cask.yml" "conclusion == 'success'"
assert_contains "$repo/.github/workflows/update-homebrew-cask.yml" 'releases/tags/$RELEASE_TAG'
assert_contains "$repo/.github/workflows/update-homebrew-cask.yml" '--jq .immutable'
assert_contains "$repo/.github/workflows/update-homebrew-cask.yml" 'Casks/bandwidth-top.rb'
assert_contains "$repo/.github/workflows/update-homebrew-cask.yml" 'actions: read'
assert_contains "$repo/.github/workflows/update-homebrew-cask.yml" 'permission-administration: read'
assert_contains "$repo/.github/workflows/update-homebrew-cask.yml" 'permission-checks: read'
assert_contains "$repo/.github/workflows/update-homebrew-cask.yml" 'permission-contents: write'
assert_contains "$repo/.github/workflows/update-homebrew-cask.yml" 'permission-pull-requests: write'
assert_contains "$repo/.github/workflows/update-homebrew-cask.yml" 'permission-statuses: read'
assert_contains "$repo/.github/workflows/update-homebrew-cask.yml" 'Require GitHub App configuration'
assert_contains "$repo/.github/workflows/update-homebrew-cask.yml" 'branch: automation/update-bandwidth-top-cask'
assert_contains "$repo/.github/workflows/update-homebrew-cask.yml" '--auto --squash'
assert_contains "$repo/.github/workflows/update-homebrew-cask.yml" 'group: update-homebrew-cask'
assert_contains "$repo/.github/workflows/update-homebrew-cask.yml" 'homebrew-cask release-checks'
assert_not_contains "$repo/.github/workflows/update-homebrew-cask.yml" 'workflow_dispatch:'
assert_not_contains "$repo/.github/workflows/update-homebrew-cask.yml" 'pull_request_target:'
assert_not_contains "$repo/.github/workflows/update-homebrew-cask.yml" 'git push'
assert_not_contains "$repo/.github/workflows/update-homebrew-cask.yml" 'echo "$CASK_UPDATE_APP_PRIVATE_KEY"'
apple_smoke="$repo/.github/workflows/test-apple-signing.yml"
assert_contains "$apple_smoke" 'name: Test Apple signing'
assert_contains "$apple_smoke" 'workflow_dispatch:'
assert_contains "$apple_smoke" 'contents: read'
assert_contains "$apple_smoke" 'group: apple-signing-smoke'
assert_contains "$apple_smoke" 'runner: macos-15'
assert_contains "$apple_smoke" 'runner: macos-15-intel'
assert_contains "$apple_smoke" 'persist-credentials: false'
assert_contains "$apple_smoke" 'ref: ${{ github.sha }}'
assert_contains "$apple_smoke" 'test "$GITHUB_REF" = "$expected_ref"'
assert_contains "$apple_smoke" 'merge-base --is-ancestor'
assert_contains "$apple_smoke" 'signing-smoke-$SHORT_SHA'
assert_contains "$apple_smoke" 'REQUIRE_APPLE_SIGNING:'
assert_contains "$apple_smoke" './trusted/packaging/sign-and-notarize-darwin.sh'
assert_contains "$apple_smoke" './trusted/packaging/generate-homebrew-cask.sh'
assert_contains "$apple_smoke" 'cmp "$native_archive" "$RUNNER_TEMP/repeated.tar.gz"'
assert_contains "$apple_smoke" 'brew audit --cask --arch all'
assert_contains "$apple_smoke" 'brew install --cask'
assert_contains "$apple_smoke" 'source=Notarized Developer ID'
assert_contains "$apple_smoke" 'if: always()'
assert_not_contains "$apple_smoke" 'pull_request_target:'
assert_not_contains "$apple_smoke" 'actions/upload-artifact'
assert_not_contains "$apple_smoke" 'contents: write'
assert_not_contains "$apple_smoke" 'git push'
assert_not_contains "$apple_smoke" 'gh release'
assert_not_contains "$apple_smoke" 'echo "$MACOS_'
assert_not_contains "$repo/packaging/sign-and-notarize-darwin.sh" 'echo "$MACOS_'
assert_contains "$repo/packaging/sign-and-notarize-darwin.sh" 'openssl rand -hex 32'
assert_not_contains "$repo/packaging/sign-and-notarize-darwin.sh" '/dev/urandom'
assert_contains "$repo/packaging/sign-and-notarize-darwin.sh" 'REQUIRE_APPLE_SIGNING'
assert_contains "$repo/packaging/sign-and-notarize-darwin.sh" 'MACOS_SIGNING_WORK_DIR'
assert_contains "$repo/.github/workflows/release.yml" 'draft: true'
assert_contains "$repo/.github/workflows/release.yml" 'gh release edit "$GITHUB_REF_NAME" --draft=false'
assert_contains "$repo/.github/workflows/release.yml" 'release-checks:'
assert_contains "$repo/.github/workflows/release.yml" "if: always() && !startsWith(github.ref, 'refs/tags/v')"
assert_contains "$repo/.github/workflows/release.yml" 'DARWIN_TOP_RESULT: ${{ needs.darwin-top.result }}'
assert_contains "$repo/.github/workflows/release.yml" 'LINUX_PACKAGES_RESULT: ${{ needs.linux-packages.result }}'
assert_contains "$repo/.github/workflows/release.yml" 'OPENWRT_RESULT: ${{ needs.openwrt.result }}'
assert_contains "$repo/.github/workflows/homebrew-cask.yml" 'homebrew-cask:'
assert_contains "$repo/.github/workflows/homebrew-cask.yml" 'timeout-minutes: 30'
assert_contains "$repo/.github/workflows/homebrew-cask.yml" './packaging/test-run-with-timeout.sh'
assert_contains "$repo/.github/workflows/homebrew-cask.yml" \
	'./packaging/run-with-timeout.sh'
assert_contains "$repo/.github/workflows/homebrew-cask.yml" \
	'HOMEBREW_NO_AUTO_UPDATE=1 "$timeout_helper" --homebrew'
assert_contains "$repo/.github/workflows/homebrew-cask.yml" \
	'"brew audit published cask"'
assert_contains "$repo/.github/workflows/homebrew-cask.yml" \
	'"brew install published cask"'
assert_contains "$repo/.github/workflows/homebrew-cask.yml" \
	'"cleanup published cask uninstall"'
assert_contains "$repo/packaging/test-homebrew-cask.sh" \
	'HOMEBREW_NO_AUTO_UPDATE=1 "$timeout_helper" --homebrew'
assert_contains "$repo/packaging/test-homebrew-cask.sh" \
	'"brew audit generated cask"'
assert_contains "$repo/packaging/test-homebrew-cask.sh" \
	'"brew install local cask"'
assert_contains "$repo/packaging/test-homebrew-cask.sh" \
	'"cleanup brew uninstall"'
assert_contains "$repo/packaging/run-with-timeout.sh" \
	'Process.spawn(*command, pgroup: true)'
assert_contains "$repo/packaging/run-with-timeout.sh" \
	'Process.kill(signal, -pid)'
assert_contains "$repo/packaging/run-with-timeout.sh" \
	'Homebrew lock and cache diagnostics'
assert_not_contains "$repo/packaging/run-with-timeout.sh" 'env |'
assert_contains "$repo/packaging/generate-homebrew-cask.sh" 'on_arm do'
assert_contains "$repo/packaging/generate-homebrew-cask.sh" 'on_intel do'
assert_contains "$repo/packaging/generate-homebrew-cask.sh" 'binary "bandwidth-top"'
assert_not_contains "$repo/packaging/generate-homebrew-cask.sh" 'sha256 :no_check'
assert_not_contains "$repo/packaging/generate-homebrew-cask.sh" 'version :latest'
assert_contains "$repo/packaging/update-homebrew-cask.sh" 'REQUIRE_NOTARIZATION=1'
assert_contains "$repo/packaging/update-homebrew-cask.sh" '--jq .immutable'
assert_contains "$repo/packaging/verify-homebrew-release.sh" 'release_jobs='
assert_contains "$repo/packaging/verify-homebrew-release.sh" 'git/ref/tags/$tag'
assert_contains "$repo/packaging/verify-homebrew-release.sh" 'compare/$tag_sha...$default_branch'
assert_contains "$repo/packaging/verify-homebrew-release.sh" '--jq .immutable'
assert_contains "$repo/Makefile" 'bandwidth-monitor/version.Version=$(BUILD_VERSION)'
assert_contains "$repo/flake.nix" 'bandwidth-monitor/version.Version=${bandwidth-top.version}'
assert_contains "$repo/packaging/openwrt-Makefile" "define Package/bandwidth-top"
assert_contains "$repo/packaging/openwrt-Makefile" '$(GO_PKG_BUILD_BIN_DIR)/bandwidth-monitor $(1)/usr/bin/bandwidth-monitor'
assert_contains "$repo/packaging/openwrt-Makefile" '$(GO_PKG_BUILD_BIN_DIR)/bandwidth-top $(1)/usr/bin/bandwidth-top'
assert_contains "$repo/packaging/openwrt-Makefile" '$(eval $(call BuildPackage,bandwidth-top))'
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
	cp "$repo/nfpm.yaml" "$repo/nfpm-top.yaml" "$repo/env.example" "$tmp/build/"
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
		VERSION=$newer_nfpm GOARCH=amd64 nfpm package -p deb -f nfpm-top.yaml -t "$tmp/top-package.deb" >/dev/null
		VERSION=$newer_nfpm GOARCH=amd64 nfpm package -p rpm -f nfpm-top.yaml -t "$tmp/top-package.rpm" >/dev/null
	)
	[ -s "$tmp/package.deb" ] || fail "nfpm did not build a Debian package"
	[ -s "$tmp/package.rpm" ] || fail "nfpm did not build an RPM package"
	[ -s "$tmp/top-package.deb" ] || fail "nfpm did not build a bandwidth-top Debian package"
	[ -s "$tmp/top-package.rpm" ] || fail "nfpm did not build a bandwidth-top RPM package"

	if command -v dpkg-deb >/dev/null 2>&1; then
		dpkg-deb --info "$tmp/package.deb" > "$tmp/deb-info"
		dpkg-deb --contents "$tmp/package.deb" > "$tmp/deb-contents"
		assert_contains "$tmp/deb-info" "Version: $newer_nfpm"
		assert_contains "$tmp/deb-contents" "./usr/bin/bandwidth-monitor"
		assert_not_contains "$tmp/deb-contents" "./usr/bin/bandwidth-top"
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

		dpkg-deb --contents "$tmp/top-package.deb" > "$tmp/top-deb-contents"
		assert_contains "$tmp/top-deb-contents" "./usr/bin/bandwidth-top"
		assert_not_contains "$tmp/top-deb-contents" "./usr/bin/bandwidth-monitor"
		assert_not_contains "$tmp/top-deb-contents" "./etc/"
		assert_not_contains "$tmp/top-deb-contents" "./lib/systemd/"
		dpkg-deb -x "$tmp/package.deb" "$tmp/monitor-root"
		dpkg-deb -x "$tmp/top-package.deb" "$tmp/top-root"
		(cd "$tmp/monitor-root" && find . \( -type f -o -type l \)) | sort > "$tmp/monitor-files"
		(cd "$tmp/top-root" && find . \( -type f -o -type l \)) | sort > "$tmp/top-files"
		[ "$(cat "$tmp/top-files")" = "./usr/bin/bandwidth-top" ] ||
			fail "bandwidth-top Debian package contains unexpected payload"
		comm -12 "$tmp/monitor-files" "$tmp/top-files" > "$tmp/overlap"
		[ ! -s "$tmp/overlap" ] || fail "Debian package payloads overlap"
	fi

	if command -v rpm >/dev/null 2>&1; then
		rpm -qlp "$tmp/package.rpm" | sort > "$tmp/monitor-rpm-files"
		rpm -qlp "$tmp/top-package.rpm" | sort > "$tmp/top-rpm-files"
		[ "$(cat "$tmp/top-rpm-files")" = "/usr/bin/bandwidth-top" ] ||
			fail "bandwidth-top RPM contains unexpected payload"
		comm -12 "$tmp/monitor-rpm-files" "$tmp/top-rpm-files" > "$tmp/rpm-overlap"
		[ ! -s "$tmp/rpm-overlap" ] || fail "RPM package payloads overlap"
	fi
fi

"$repo/packaging/test-run-with-timeout.sh"

echo "packaging assertions passed"
