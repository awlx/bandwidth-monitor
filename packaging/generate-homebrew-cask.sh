#!/bin/sh
set -eu

usage() {
	echo "usage: $0 RELEASE_TAG ASSET_DIRECTORY [OUTPUT]" >&2
	exit 2
}

[ "$#" -ge 2 ] && [ "$#" -le 3 ] || usage

tag=$1
asset_dir=$2
repo=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
output=${3:-"$repo/Casks/bandwidth-top.rb"}

case "$tag" in
	v[0-9]*.[0-9]*.[0-9]*) ;;
	*)
		echo "release tag must be a stable v-prefixed version: $tag" >&2
		exit 1
		;;
esac

version=${tag#v}
case "$version" in
	*[!0-9.]* | *.*.*.* | .* | *. | *..*)
		echo "release tag must contain exactly three numeric components: $tag" >&2
		exit 1
		;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

validate_asset() {
	goarch=$1
	machine=$2
	archive_name="bandwidth-top_${version}_darwin_${goarch}.tar.gz"
	archive="$asset_dir/$archive_name"
	checksum_file="${archive}.sha256"
	extract_dir="$tmp/$goarch"

	[ -f "$archive" ] || {
		echo "release archive not found: $archive" >&2
		exit 1
	}
	[ -f "$checksum_file" ] || {
		echo "release checksum not found: $checksum_file" >&2
		exit 1
	}

	line_count=$(awk 'END { print NR }' "$checksum_file")
	[ "$line_count" -eq 1 ] || {
		echo "checksum file must contain exactly one line: $checksum_file" >&2
		exit 1
	}
	expected=$(awk 'NR == 1 { print $1 }' "$checksum_file")
	listed_name=$(awk 'NR == 1 { print $2 }' "$checksum_file")
	printf '%s\n' "$expected" | grep -Eq '^[0-9a-f]{64}$' || {
		echo "invalid SHA-256 in $checksum_file" >&2
		exit 1
	}
	[ "$listed_name" = "$archive_name" ] || {
		echo "checksum references $listed_name instead of $archive_name" >&2
		exit 1
	}

	actual=$(shasum -a 256 "$archive" | awk '{ print $1 }')
	[ "$actual" = "$expected" ] || {
		echo "checksum mismatch for $archive_name" >&2
		exit 1
	}

	tar -tzf "$archive" | LC_ALL=C sort > "$tmp/$goarch.files"
	printf '%s\n' LICENSE bandwidth-top > "$tmp/expected.files"
	cmp "$tmp/expected.files" "$tmp/$goarch.files" >/dev/null || {
		echo "unexpected payload in $archive_name" >&2
		exit 1
	}

	mkdir "$extract_dir"
	tar -xzf "$archive" -C "$extract_dir" bandwidth-top LICENSE
	[ -f "$extract_dir/bandwidth-top" ] &&
		[ ! -L "$extract_dir/bandwidth-top" ] &&
		[ -x "$extract_dir/bandwidth-top" ] || {
		echo "bandwidth-top is not a regular executable in $archive_name" >&2
		exit 1
	}
	[ -f "$extract_dir/LICENSE" ] && [ ! -L "$extract_dir/LICENSE" ] || {
		echo "LICENSE is not a regular file in $archive_name" >&2
		exit 1
	}
	cmp "$repo/LICENSE" "$extract_dir/LICENSE" >/dev/null || {
		echo "release license does not match repository LICENSE in $archive_name" >&2
		exit 1
	}
	file -b "$extract_dir/bandwidth-top" | grep -F "Mach-O 64-bit executable $machine" >/dev/null || {
		echo "unexpected executable architecture in $archive_name" >&2
		exit 1
	}
	if [ "${REQUIRE_NOTARIZATION:-0}" = "1" ]; then
		/usr/bin/codesign --verify --strict --verbose=2 --check-notarization \
			-R=notarized "$extract_dir/bandwidth-top" || {
			echo "online notarization ticket was not verified for $archive_name" >&2
			exit 1
		}
	fi

	printf '%s\n' "$actual"
}

arm_sha=$(validate_asset arm64 arm64)
intel_sha=$(validate_asset amd64 x86_64)

mkdir -p "$(dirname "$output")"
cat > "$output" <<EOF
cask "bandwidth-top" do
  version "$version"

  on_arm do
    sha256 "$arm_sha"

    url "https://github.com/awlx/bandwidth-monitor/releases/download/v#{version}/bandwidth-top_#{version}_darwin_arm64.tar.gz"
  end
  on_intel do
    sha256 "$intel_sha"

    url "https://github.com/awlx/bandwidth-monitor/releases/download/v#{version}/bandwidth-top_#{version}_darwin_amd64.tar.gz"
  end

  name "bandwidth-top"
  desc "Interactive per-flow network traffic viewer"
  homepage "https://github.com/awlx/bandwidth-monitor"

  livecheck do
    url :url
    strategy :github_latest
  end

  depends_on macos: :monterey

  binary "bandwidth-top"

  caveats <<~EOS
    bandwidth-top captures packets through macOS /dev/bpf* devices. Run it
    with sudo, or ask an administrator to grant a dedicated trusted group
    read/write access to those devices. Never make BPF devices world-accessible.

    This cask installs only the standalone bandwidth-top CLI. It does not
    install or run the Linux-only bandwidth-monitor daemon.
  EOS
end
EOF

echo "generated $output for $tag"
