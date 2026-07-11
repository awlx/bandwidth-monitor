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
cleanup() {
	security delete-keychain "$keychain" >/dev/null 2>&1 || true
	rm -rf "$tmp"
}
trap cleanup EXIT HUP INT TERM

printf '%s' "$MACOS_SIGNING_CERTIFICATE" |
	base64 --decode > "$tmp/signing-certificate.p12"
printf '%s' "$MACOS_NOTARY_KEY" |
	base64 --decode > "$tmp/AuthKey_${MACOS_NOTARY_KEY_ID}.p8"

security create-keychain -p "$keychain_password" "$keychain"
security unlock-keychain -p "$keychain_password" "$keychain"
security set-keychain-settings -lut 21600 "$keychain"
security import "$tmp/signing-certificate.p12" \
	-k "$keychain" \
	-P "$MACOS_SIGNING_CERTIFICATE_PASSWORD" \
	-T /usr/bin/codesign
security set-key-partition-list \
	-S apple-tool:,apple:,codesign: \
	-s \
	-k "$keychain_password" \
	"$keychain"

codesign \
	--force \
	--options runtime \
	--sign "$MACOS_SIGNING_IDENTITY" \
	--timestamp \
	--keychain "$keychain" \
	"$binary"
codesign --verify --strict --verbose=2 "$binary"

ditto -c -k --keepParent "$binary" "$tmp/notarization.zip"
xcrun notarytool submit "$tmp/notarization.zip" \
	--issuer "$MACOS_NOTARY_ISSUER_ID" \
	--key "$tmp/AuthKey_${MACOS_NOTARY_KEY_ID}.p8" \
	--key-id "$MACOS_NOTARY_KEY_ID" \
	--wait

spctl --status | grep -Fx "assessments enabled" >/dev/null || {
	echo "Gatekeeper assessments must be enabled to verify notarization" >&2
	exit 1
}
assessment=$(spctl --assess --type execute --verbose=2 "$binary" 2>&1) || {
	printf '%s\n' "$assessment" >&2
	exit 1
}
printf '%s\n' "$assessment" |
	grep -F "source=Notarized Developer ID" >/dev/null || {
	printf '%s\n' "$assessment" >&2
	echo "Apple notarization was not verified" >&2
	exit 1
}
