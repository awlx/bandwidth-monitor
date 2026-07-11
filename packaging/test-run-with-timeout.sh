#!/bin/sh
set -eu

repo=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
helper="$repo/packaging/run-with-timeout.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail() {
	echo "timeout helper assertion failed: $*" >&2
	exit 1
}

output=$("$helper" 5 "successful fixture" sh -c 'printf success')
[ "$output" = success ] || fail "successful command output changed"
output=$("$helper" --homebrew 5 "Homebrew environment fixture" \
	sh -c 'printf %s "$HOMEBREW_NO_AUTO_UPDATE"')
[ "$output" = 1 ] || fail "Homebrew auto-update suppression was not preserved"

set +e
"$helper" 5 "failing fixture" sh -c 'exit 23'
status=$?
set -e
[ "$status" -eq 23 ] || fail "nonzero exit status was not preserved"

started=$(date +%s)
set +e
output=$(
	"$helper" 1 "timeout fixture" sh -c '
		sleep 30 &
		echo "$!" > "$1"
		wait
	' fixture "$tmp/child-pid" 2>&1
)
status=$?
set -e
elapsed=$(($(date +%s) - started))

[ "$status" -eq 124 ] || fail "timeout did not return status 124"
[ "$elapsed" -le 5 ] || fail "timeout took ${elapsed}s"
printf '%s\n' "$output" | grep -F 'Timed out after 1s: timeout fixture' >/dev/null ||
	fail "timeout diagnostic omitted the command name and duration"
printf '%s\n' "$output" | grep -F 'Process group state' >/dev/null ||
	fail "timeout diagnostic omitted process state"

child_pid=$(cat "$tmp/child-pid")
child_state=$(ps -o state= -p "$child_pid" 2>/dev/null | tr -d '[:space:]' || true)
case "$child_state" in
	'' | Z*) ;;
	*) fail "timeout left child process $child_pid running in state $child_state" ;;
esac

echo "timeout helper assertions passed"
