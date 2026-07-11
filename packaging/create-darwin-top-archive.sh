#!/bin/sh
set -eu

if [ "$#" -ne 3 ]; then
	echo "usage: $0 BINARY LICENSE OUTPUT.tar.gz" >&2
	exit 2
fi

binary=$1
license=$2
output=$3

[ -f "$binary" ] || {
	echo "binary not found: $binary" >&2
	exit 1
}
[ -f "$license" ] || {
	echo "license not found: $license" >&2
	exit 1
}

case "$output" in
	*.tar.gz) ;;
	*)
		echo "archive must end in .tar.gz: $output" >&2
		exit 1
		;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

install -m 0755 "$binary" "$tmp/bandwidth-top"
install -m 0644 "$license" "$tmp/LICENSE"
TZ=UTC touch -t 198001010000 "$tmp/bandwidth-top" "$tmp/LICENSE"

(
	cd "$tmp"
	COPYFILE_DISABLE=1 tar \
		--format ustar \
		--gid 0 \
		--gname wheel \
		--uid 0 \
		--uname root \
		-cf archive.tar \
		bandwidth-top LICENSE
)
gzip -n -9 -c "$tmp/archive.tar" > "$output"
