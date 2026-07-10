#!/bin/sh
set -eu

if [ "$#" -ne 6 ]; then
	echo "usage: $0 <nfpm|openwrt> <ref-type> <ref-name> <run-number> <run-attempt> <sha>" >&2
	exit 2
fi

format=$1
ref_type=$2
ref_name=$3
run_number=$4
run_attempt=$5
sha=$6

case "$format" in
	nfpm|openwrt) ;;
	*) echo "unsupported package format: $format" >&2; exit 2 ;;
esac

if [ "$ref_type" = "tag" ]; then
	version=${ref_name#v}
	case "$version" in
		""|*[!0-9A-Za-z.+~_-]*)
			echo "invalid tag version: $ref_name" >&2
			exit 2
			;;
	esac
	printf '%s\n' "$version"
	exit
fi

case "$run_number$run_attempt" in
	""|*[!0-9]*) echo "run number and attempt must be numeric" >&2; exit 2 ;;
esac
case "$sha" in
	""|*[!0-9A-Fa-f]*) echo "sha must be hexadecimal" >&2; exit 2 ;;
esac

short_sha=$(printf '%.12s' "$sha")
case "$format" in
	nfpm)
		printf '0.0.0~git%s.%s.g%s\n' "$run_number" "$run_attempt" "$short_sha"
		;;
	openwrt)
		if [ "$run_attempt" -gt 999 ]; then
			echo "OpenWrt run attempt exceeds three-digit version field" >&2
			exit 2
		fi
		printf '0.0.0_git%s%03d\n' "$run_number" "$run_attempt"
		;;
esac
