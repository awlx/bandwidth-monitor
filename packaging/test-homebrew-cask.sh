#!/bin/sh
set -eu

repo=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
installed=false
tapped=false
tap_name=awlx-test/bandwidth-monitor
trap '
	if [ "$installed" = true ]; then
		HOMEBREW_NO_AUTO_UPDATE=1 brew uninstall --cask "$tap_name/bandwidth-top" >/dev/null 2>&1 || true
	fi
	if [ "$tapped" = true ]; then
		HOMEBREW_NO_AUTO_UPDATE=1 brew untap --force "$tap_name" >/dev/null 2>&1 || true
	fi
	rm -rf "$tmp"
' EXIT HUP INT TERM

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
	HOMEBREW_NO_AUTO_UPDATE=1 brew style "$cask"

	if HOMEBREW_NO_AUTO_UPDATE=1 brew list --cask bandwidth-top >/dev/null 2>&1; then
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

	if HOMEBREW_NO_AUTO_UPDATE=1 brew tap | grep -Fx "$tap_name" >/dev/null; then
		fail "$tap_name is already tapped; refusing to replace it"
	fi
	HOMEBREW_NO_AUTO_UPDATE=1 brew tap-new --no-git "$tap_name"
	tapped=true
	tap_path=$(brew --repository "$tap_name")
	mkdir -p "$tap_path/Casks"
	install -m 0644 "$cask" "$tap_path/Casks/bandwidth-top.rb"
	HOMEBREW_NO_AUTO_UPDATE=1 brew audit --cask --arch all \
		"$tap_name/bandwidth-top"
	install -m 0644 "$tmp/bandwidth-top.rb" "$tap_path/Casks/bandwidth-top.rb"
	rm -f "$(brew --cache --cask "$tap_name/bandwidth-top")"

	HOMEBREW_NO_AUTO_UPDATE=1 brew install --cask "$tap_name/bandwidth-top"
	installed=true
	[ -x "$(brew --prefix)/bin/bandwidth-top" ] ||
		fail "Homebrew did not link bandwidth-top"
	cmp "$tmp/bandwidth-top-$goarch" "$(brew --prefix)/bin/bandwidth-top" >/dev/null ||
		fail "installed cask binary does not match the selected archive"
	HOMEBREW_NO_AUTO_UPDATE=1 brew uninstall --cask "$tap_name/bandwidth-top"
	installed=false
	[ ! -e "$(brew --prefix)/bin/bandwidth-top" ] ||
		fail "Homebrew did not unlink bandwidth-top"
	HOMEBREW_NO_AUTO_UPDATE=1 brew untap --force "$tap_name"
	tapped=false
fi

echo "Homebrew cask assertions passed"
