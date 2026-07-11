#!/bin/sh
set -eu

if [ "$#" -ne 5 ]; then
	echo "usage: $0 REPOSITORY DEFAULT_BRANCH RUN_ID RUN_SHA RELEASE_TAG" >&2
	exit 2
fi

repository=$1
default_branch=$2
run_id=$3
run_sha=$4
tag=$5

case "$tag" in
	v[0-9]*.[0-9]*.[0-9]*) ;;
	*)
		echo "upstream run did not use a stable release tag: $tag" >&2
		exit 1
		;;
esac
version=${tag#v}
case "$version" in
	*[!0-9.]* | *.*.*.* | .* | *. | *..*)
		echo "upstream run used an invalid release tag: $tag" >&2
		exit 1
		;;
esac

release_jobs=$(
	gh api "repos/$repository/actions/runs/$run_id/jobs?per_page=100" \
		--jq '[.jobs[] | select(.name == "release" and .conclusion == "success")] | length'
)
[ "$release_jobs" = "1" ] || {
	echo "upstream workflow did not publish exactly one successful release job" >&2
	exit 1
}

tag_ref=$(gh api "repos/$repository/git/ref/tags/$tag")
tag_object_sha=$(printf '%s' "$tag_ref" | jq -r .object.sha)
tag_type=$(printf '%s' "$tag_ref" | jq -r .object.type)
tag_sha=$tag_object_sha
depth=0
while [ "$tag_type" = "tag" ]; do
	depth=$((depth + 1))
	[ "$depth" -le 5 ] || {
		echo "release tag nesting exceeds verification limit" >&2
		exit 1
	}
	tag_object=$(gh api "repos/$repository/git/tags/$tag_sha")
	tag_sha=$(printf '%s' "$tag_object" | jq -r .object.sha)
	tag_type=$(printf '%s' "$tag_object" | jq -r .object.type)
done
[ "$tag_type" = "commit" ] || {
	echo "release tag does not resolve to a commit" >&2
	exit 1
}
[ "$tag_sha" = "$run_sha" ] || {
	echo "release tag does not resolve to the completed workflow commit" >&2
	exit 1
}

compare_status=$(
	gh api "repos/$repository/compare/$tag_sha...$default_branch" --jq .status
)
case "$compare_status" in
	ahead | identical) ;;
	*)
		echo "release commit is not reachable from $default_branch" >&2
		exit 1
		;;
esac

[ "$(
	gh api "repos/$repository/releases/tags/$tag" --jq .immutable
)" = "true" ] || {
	echo "release immutability must be enabled before publishing" >&2
	exit 1
}

printf '%s\n' "$tag_object_sha"
