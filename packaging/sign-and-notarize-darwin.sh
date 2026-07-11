#!/bin/sh
set -eu

if [ "$#" -ne 1 ]; then
	echo "usage: $0 MACH_O_BINARY" >&2
	exit 2
fi

binary=$1
[ -f "$binary" ] || {
	echo "binary not found: $binary" >&2
	exit 1
}

required_vars="
MACOS_SIGNING_CERTIFICATE
MACOS_SIGNING_CERTIFICATE_PASSWORD
MACOS_SIGNING_IDENTITY
MACOS_NOTARY_KEY
MACOS_NOTARY_KEY_ID
MACOS_NOTARY_ISSUER_ID
"
configured=0
missing=0
for name in $required_vars; do
	eval "value=\${$name:-}"
	if [ -n "$value" ]; then
		configured=$((configured + 1))
	else
		missing=$((missing + 1))
	fi
done

if [ "$configured" -eq 0 ]; then
	if [ "${REQUIRE_APPLE_SIGNING:-0}" = "1" ]; then
		echo "Apple signing credentials are required" >&2
		exit 1
	fi
	echo "Apple signing credentials are not configured; leaving $binary ad-hoc signed"
	exit 0
fi
[ "$missing" -eq 0 ] || {
	echo "Apple signing credentials are only partially configured" >&2
	exit 1
}

if [ -n "${MACOS_SIGNING_WORK_DIR:-}" ]; then
	tmp=$MACOS_SIGNING_WORK_DIR
	case "$tmp" in
		/*) ;;
		*)
			echo "MACOS_SIGNING_WORK_DIR must be an absolute path" >&2
			exit 1
			;;
	esac
	if [ -e "$tmp" ] || [ -L "$tmp" ]; then
		echo "MACOS_SIGNING_WORK_DIR already exists" >&2
		exit 1
	fi
	mkdir -m 0700 "$tmp"
else
	tmp=$(mktemp -d)
fi
keychain="$tmp/release-signing.keychain-db"
keychain_password=$(openssl rand -hex 32)
original_keychains="$tmp/original-keychains"
original_default_keychain="$tmp/original-default-keychain"
keychain_created=false
search_list_changed=false
default_changed=false

parse_keychain_paths() {
	input=$1
	output=$2
	: > "$output"
	while IFS= read -r line || [ -n "$line" ]; do
		[ -n "$line" ] || continue
		case "$line" in
			*\"*\")
				path=${line#*\"}
				case "$path" in
					*\") path=${path%\"*} ;;
					*)
						echo "could not parse macOS keychain path" >&2
						return 1
						;;
				esac
				;;
			*)
				echo "could not parse macOS keychain path" >&2
				return 1
				;;
		esac
		printf '%s\n' "$path" >> "$output"
	done < "$input"
}

set_search_list() {
	prepend=$1
	paths=$2
	set --
	[ -z "$prepend" ] || set -- "$prepend"
	while IFS= read -r path || [ -n "$path" ]; do
		[ -n "$path" ] || continue
		set -- "$@" "$path"
	done < "$paths"
	/usr/bin/security list-keychains -d user -s "$@"
}

restore_default_keychain() {
	if [ -s "$original_default_keychain" ]; then
		IFS= read -r path < "$original_default_keychain"
		/usr/bin/security default-keychain -d user -s "$path"
	else
		/usr/bin/security default-keychain -d user -s
	fi
}

cleanup() {
	status=$?
	trap - EXIT
	trap '' HUP INT TERM
	set +e
	cleanup_failed=0

	if [ "$default_changed" = true ] &&
		! restore_default_keychain; then
		echo "failed to restore the original default keychain" >&2
		cleanup_failed=1
	fi
	if [ "$search_list_changed" = true ] &&
		! set_search_list "" "$original_keychains"; then
		echo "failed to restore the original keychain search list" >&2
		cleanup_failed=1
	fi
	if [ "$keychain_created" = true ] &&
		! /usr/bin/security delete-keychain "$keychain" >/dev/null 2>&1; then
		echo "failed to delete the ephemeral signing keychain" >&2
		cleanup_failed=1
	fi
	if ! rm -rf "$tmp"; then
		echo "failed to remove temporary Apple signing files" >&2
		cleanup_failed=1
	fi

	if [ "$status" -eq 0 ] && [ "$cleanup_failed" -ne 0 ]; then
		status=1
	fi
	exit "$status"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

raw_keychains="$tmp/current-keychains"
/usr/bin/security list-keychains -d user > "$raw_keychains" || {
	echo "could not read the user keychain search list" >&2
	exit 1
}
parse_keychain_paths "$raw_keychains" "$original_keychains"

raw_default_keychain="$tmp/current-default-keychain"
/usr/bin/security default-keychain -d user > "$raw_default_keychain" || {
	echo "could not read the user default keychain" >&2
	exit 1
}
parse_keychain_paths "$raw_default_keychain" "$original_default_keychain"
[ "$(wc -l < "$original_default_keychain")" -le 1 ] || {
	echo "macOS returned multiple default keychains" >&2
	exit 1
}

printf '%s' "$MACOS_SIGNING_CERTIFICATE" |
	base64 --decode > "$tmp/signing-certificate.p12"
printf '%s' "$MACOS_NOTARY_KEY" |
	base64 --decode > "$tmp/AuthKey_${MACOS_NOTARY_KEY_ID}.p8"

keychain_created=true
/usr/bin/security create-keychain -p "$keychain_password" "$keychain"
/usr/bin/security unlock-keychain -p "$keychain_password" "$keychain"
/usr/bin/security set-keychain-settings -lut 21600 "$keychain"

search_list_changed=true
set_search_list "$keychain" "$original_keychains" || {
	echo "could not configure the user keychain search list" >&2
	exit 1
}
default_changed=true
/usr/bin/security default-keychain -d user -s "$keychain" || {
	echo "could not configure the ephemeral default keychain" >&2
	exit 1
}

/usr/bin/security import "$tmp/signing-certificate.p12" \
	-k "$keychain" \
	-P "$MACOS_SIGNING_CERTIFICATE_PASSWORD" \
	-T /usr/bin/codesign \
	-T /usr/bin/security

identity_output="$tmp/codesigning-identities"
/usr/bin/security find-identity -v -p codesigning "$keychain" > "$identity_output" || {
	echo "could not inspect the ephemeral signing keychain" >&2
	exit 1
}
EXPECTED_SIGNING_IDENTITY=$MACOS_SIGNING_IDENTITY awk '
	/^[[:space:]]*[0-9]+\)/ {
		identity = $0
		sub(/^[[:space:]]*[0-9]+\)[[:space:]]+/, "", identity)

		fingerprint = identity
		sub(/[[:space:]].*$/, "", fingerprint)
		if (fingerprint == ENVIRON["EXPECTED_SIGNING_IDENTITY"])
			found = 1

		first_quote = index(identity, "\"")
		if (first_quote > 0) {
			name = substr(identity, first_quote + 1)
			sub(/"[[:space:]]*$/, "", name)
			if (name == ENVIRON["EXPECTED_SIGNING_IDENTITY"])
				found = 1
		}
	}
	END { exit(found ? 0 : 1) }
' "$identity_output" || {
	echo "configured macOS signing identity was not found in the ephemeral keychain" >&2
	exit 1
}

/usr/bin/security set-key-partition-list \
	-S apple-tool:,apple:,codesign: \
	-s \
	-k "$keychain_password" \
	"$keychain"

/usr/bin/codesign \
	--force \
	--options runtime \
	--sign "$MACOS_SIGNING_IDENTITY" \
	--timestamp \
	--keychain "$keychain" \
	"$binary"
/usr/bin/codesign --verify --strict --verbose=2 "$binary"

ditto -c -k --keepParent "$binary" "$tmp/notarization.zip"
notary_result="$tmp/notary-result.plist"
/usr/bin/xcrun notarytool submit "$tmp/notarization.zip" \
	--issuer "$MACOS_NOTARY_ISSUER_ID" \
	--key "$tmp/AuthKey_${MACOS_NOTARY_KEY_ID}.p8" \
	--key-id "$MACOS_NOTARY_KEY_ID" \
	--wait \
	--output-format plist > "$notary_result"
notary_status=$(
	/usr/bin/plutil -extract status raw -o - "$notary_result"
) || {
	echo "could not read the Apple notarization status" >&2
	exit 1
}
[ "$notary_status" = "Accepted" ] || {
	/usr/bin/plutil -p "$notary_result" >&2
	echo "Apple notarization was not accepted" >&2
	exit 1
}

/usr/bin/codesign --verify --strict --verbose=2 --check-notarization \
	-R=notarized "$binary"
