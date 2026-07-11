#!/bin/sh
set -eu

if [ "$#" -ne 1 ]; then
	echo "usage: $0 RELEASE_TAG" >&2
	exit 2
fi

tag=$1
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

command -v gh >/dev/null 2>&1 || {
	echo "gh is required to download authenticated release assets" >&2
	exit 1
}
repo=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
repo=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

resolved=$(
	RELEASE_TAG=$tag gh release view "$tag" \
		--repo awlx/bandwidth-monitor \
		--json tagName,isDraft,isPrerelease \
		--jq 'select(.tagName == env.RELEASE_TAG and (.isDraft | not) and (.isPrerelease | not)) | .tagName'
)
[ "$resolved" = "$tag" ] || {
	echo "release must exist and be published as a stable release: $tag" >&2
	exit 1
}
immutable=$(
	gh api "repos/awlx/bandwidth-monitor/releases/tags/$tag" --jq .immutable
)
[ "$immutable" = "true" ] || {
	echo "release must be immutable before generating its cask: $tag" >&2
	exit 1
}

for goarch in arm64 amd64; do
	archive="bandwidth-top_${version}_darwin_${goarch}.tar.gz"
	gh release download "$tag" \
		--repo awlx/bandwidth-monitor \
		--pattern "$archive" \
		--dir "$tmp"
	gh release download "$tag" \
		--repo awlx/bandwidth-monitor \
		--pattern "${archive}.sha256" \
		--dir "$tmp"
done

REQUIRE_NOTARIZATION=1 "$repo/packaging/generate-homebrew-cask.sh" \
	"$tag" "$tmp" "$repo/Casks/bandwidth-top.rb"
