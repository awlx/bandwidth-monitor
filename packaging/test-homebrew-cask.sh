#!/bin/sh
set -eu

repo=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
installed=false
tapped=false
tap_name=${HOMEBREW_TEST_TAP_NAME:-awlx-test/bandwidth-monitor}
timeout_helper="$repo/packaging/run-with-timeout.sh"
brew_query_timeout=${HOMEBREW_TEST_QUERY_TIMEOUT:-60}
brew_operation_timeout=${HOMEBREW_TEST_OPERATION_TIMEOUT:-600}
brew_cleanup_timeout=${HOMEBREW_TEST_CLEANUP_TIMEOUT:-120}

run_brew() {
	timeout=$1
	command_name=$2
	shift 2
	HOMEBREW_NO_AUTO_UPDATE=1 "$timeout_helper" --homebrew \
		"$timeout" "$command_name" brew "$@"
}

cleanup() {
	status=$?
	trap - EXIT
	if [ "$installed" = true ]; then
		run_brew "$brew_cleanup_timeout" "cleanup brew uninstall" \
			uninstall --cask "$tap_name/bandwidth-top" || true
	fi
	if [ "$tapped" = true ]; then
		run_brew "$brew_cleanup_timeout" "cleanup brew untap" \
			untap --force "$tap_name" || true
	fi
	rm -rf "$tmp"
	exit "$status"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

fail() {
	echo "Homebrew cask assertion failed: $*" >&2
	exit 1
}

mkdir -p "$tmp/assets" "$tmp/source"
cat > "$tmp/source/main.go" <<'EOF'
package main

import "fmt"

func main() {
	fmt.Println("bandwidth-top fixture")
}
EOF

for goarch in arm64 amd64; do
	binary="$tmp/bandwidth-top-$goarch"
	archive="$tmp/assets/bandwidth-top_1.2.3_darwin_${goarch}.tar.gz"
	CGO_ENABLED=0 GOOS=darwin GOARCH=$goarch \
		go build -trimpath -o "$binary" "$tmp/source/main.go"
	"$repo/packaging/create-darwin-top-archive.sh" \
		"$binary" "$repo/LICENSE" "$archive"
	(
		cd "$tmp/assets"
		shasum -a 256 "$(basename "$archive")" > "$(basename "$archive").sha256"
	)
done

"$repo/packaging/create-darwin-top-archive.sh" \
	"$tmp/bandwidth-top-arm64" "$repo/LICENSE" "$tmp/repeated.tar.gz"
cmp "$tmp/assets/bandwidth-top_1.2.3_darwin_arm64.tar.gz" \
	"$tmp/repeated.tar.gz" >/dev/null ||
	fail "Darwin archives are not reproducible"

cask="$tmp/Casks/bandwidth-top.rb"
"$repo/packaging/generate-homebrew-cask.sh" v1.2.3 "$tmp/assets" "$cask"
ruby -c "$cask" >/dev/null
cp "$cask" "$tmp/first-cask.rb"
"$repo/packaging/generate-homebrew-cask.sh" v1.2.3 "$tmp/assets" "$cask" >/dev/null
cmp "$tmp/first-cask.rb" "$cask" >/dev/null ||
	fail "repeated generation changed an identical release cask"

grep -F 'on_arm do' "$cask" >/dev/null || fail "cask has no on_arm block"
grep -F 'on_intel do' "$cask" >/dev/null || fail "cask has no on_intel block"
grep -F 'binary "bandwidth-top"' "$cask" >/dev/null ||
	fail "cask does not install the standalone binary"
grep -F 'depends_on macos: :monterey' "$cask" >/dev/null ||
	fail "cask does not declare the Go toolchain minimum macOS"
grep -F 'bandwidth-top_#{version}_darwin_arm64.tar.gz' "$cask" >/dev/null ||
	fail "cask does not select the arm64 release URL"
grep -F 'bandwidth-top_#{version}_darwin_amd64.tar.gz' "$cask" >/dev/null ||
	fail "cask does not select the amd64 release URL"

for goarch in arm64 amd64; do
	hash=$(shasum -a 256 "$tmp/assets/bandwidth-top_1.2.3_darwin_${goarch}.tar.gz" |
		awk '{ print $1 }')
	grep -F "sha256 \"$hash\"" "$cask" >/dev/null ||
		fail "cask does not contain the exact $goarch checksum"
done

if grep -Eq 'sha256 :no_check|version :latest|curl[[:space:]].*\|' "$cask"; then
	fail "cask contains an unverified download mechanism"
fi

if "$repo/packaging/generate-homebrew-cask.sh" v1.2 "$tmp/assets" "$tmp/invalid.rb" \
		>/dev/null 2>&1; then
	fail "generator accepted a malformed release tag"
fi
if MACOS_SIGNING_CERTIFICATE=partial \
		"$repo/packaging/sign-and-notarize-darwin.sh" "$tmp/bandwidth-top-arm64" \
		>/dev/null 2>&1; then
	fail "signing script accepted a partial credential configuration"
fi
if REQUIRE_APPLE_SIGNING=1 \
		"$repo/packaging/sign-and-notarize-darwin.sh" "$tmp/bandwidth-top-arm64" \
		>/dev/null 2>&1; then
	fail "signing script did not require credentials for a smoke run"
fi
if MACOS_NOTARY_ISSUER_ID=invalid \
	MACOS_NOTARY_KEY=invalid \
	MACOS_NOTARY_KEY_ID=invalid \
	MACOS_SIGNING_CERTIFICATE=invalid \
	MACOS_SIGNING_CERTIFICATE_PASSWORD=invalid \
	MACOS_SIGNING_IDENTITY=invalid \
	"$repo/packaging/sign-and-notarize-darwin.sh" "$tmp/bandwidth-top-arm64" \
	>/dev/null 2>&1; then
	fail "signing script accepted invalid credentials"
fi

if [ "${1:-}" = "--brew" ]; then
	command -v brew >/dev/null 2>&1 || fail "brew is required for --brew"
	run_brew "$brew_query_timeout" "brew style generated cask" style "$cask"

	installed_casks=$(run_brew "$brew_query_timeout" \
		"brew list installed casks" list --cask)
	if printf '%s\n' "$installed_casks" |
			grep -Fx bandwidth-top >/dev/null; then
		fail "bandwidth-top is already installed; refusing to replace it"
	fi

	case "$(uname -m)" in
		arm64) goarch=arm64 ;;
		x86_64) goarch=amd64 ;;
		*) fail "unsupported test architecture: $(uname -m)" ;;
	esac
	local_archive="$tmp/assets/bandwidth-top_1.2.3_darwin_${goarch}.tar.gz"
	escaped_archive=$(printf '%s\n' "$local_archive" | sed 's/[&|]/\\&/g')
	sed \
		-e "s|https://github.com/awlx/bandwidth-monitor/releases/download/v#{version}/bandwidth-top_#{version}_darwin_arm64.tar.gz|file://$escaped_archive|" \
		-e "s|https://github.com/awlx/bandwidth-monitor/releases/download/v#{version}/bandwidth-top_#{version}_darwin_amd64.tar.gz|file://$escaped_archive|" \
		"$cask" > "$tmp/bandwidth-top.rb"

	taps=$(run_brew "$brew_query_timeout" "brew list taps" tap)
	if printf '%s\n' "$taps" | grep -Fx "$tap_name" >/dev/null; then
		fail "$tap_name is already tapped; refusing to replace it"
	fi
	tapped=true
	run_brew "$brew_query_timeout" "brew create test tap" \
		tap-new --no-git "$tap_name"
	tap_path=$(run_brew "$brew_query_timeout" "brew locate test tap" \
		--repository "$tap_name")
	mkdir -p "$tap_path/Casks"
	install -m 0644 "$cask" "$tap_path/Casks/bandwidth-top.rb"
	run_brew "$brew_operation_timeout" "brew audit generated cask" \
		audit --cask --arch all "$tap_name/bandwidth-top"
	install -m 0644 "$tmp/bandwidth-top.rb" "$tap_path/Casks/bandwidth-top.rb"
	cache_path=$(run_brew "$brew_query_timeout" "brew locate cask cache" \
		--cache --cask "$tap_name/bandwidth-top")
	rm -f "$cache_path"

	installed=true
	run_brew "$brew_operation_timeout" "brew install local cask" \
		install --cask "$tap_name/bandwidth-top"
	brew_prefix=$(run_brew "$brew_query_timeout" "brew locate prefix" --prefix)
	[ -x "$brew_prefix/bin/bandwidth-top" ] ||
		fail "Homebrew did not link bandwidth-top"
	cmp "$tmp/bandwidth-top-$goarch" "$brew_prefix/bin/bandwidth-top" >/dev/null ||
		fail "installed cask binary does not match the selected archive"
	run_brew "$brew_cleanup_timeout" "brew uninstall local cask" \
		uninstall --cask "$tap_name/bandwidth-top"
	installed=false
	[ ! -e "$brew_prefix/bin/bandwidth-top" ] ||
		fail "Homebrew did not unlink bandwidth-top"
	run_brew "$brew_cleanup_timeout" "brew remove test tap" \
		untap --force "$tap_name"
	tapped=false
fi

echo "Homebrew cask assertions passed"
