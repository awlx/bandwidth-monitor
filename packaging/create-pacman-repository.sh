#!/bin/sh
set -eu

if [ "$#" -ne 3 ]; then
	echo "usage: $0 <artifact-directory> <repository-directory> <gpg-key-id>" >&2
	exit 2
fi

artifact_dir=$(CDPATH= cd -- "$1" && pwd)
mkdir -p "$2"
repository_dir=$(CDPATH= cd -- "$2" && pwd)
gpg_key_id=$3

for arch in x86_64 aarch64; do
	arch_dir=$repository_dir/$arch
	rm -rf "$arch_dir"
	mkdir -p "$arch_dir"

	for package_name in bandwidth-monitor bandwidth-top; do
		matches=$(find "$artifact_dir" -type f \
			-name "${package_name}-*-1-${arch}.pkg.tar.zst" -print)
		count=$(printf '%s\n' "$matches" | awk 'NF { count++ } END { print count + 0 }')
		if [ "$count" -ne 1 ]; then
			echo "expected one ${package_name} package for ${arch}, found ${count}" >&2
			exit 1
		fi
		cp "$matches" "$arch_dir/"
	done

	for package in "$arch_dir"/*.pkg.tar.zst; do
		gpg --batch --yes --local-user "$gpg_key_id" --detach-sign \
			--output "$package.sig" "$package"
	done
done

docker run --rm \
	--platform linux/amd64 \
	-v "$repository_dir:/repo" \
	archlinux:base-devel \
	/bin/sh -eu -c '
		for arch in x86_64 aarch64; do
			set -- /repo/$arch/*.pkg.tar.zst
			repo-add /repo/$arch/bandwidth-monitor.db.tar.gz "$@"
		done
	'

for arch in x86_64 aarch64; do
	arch_dir=$repository_dir/$arch

	rm -f "$arch_dir/bandwidth-monitor.db" "$arch_dir/bandwidth-monitor.files"
	cp "$arch_dir/bandwidth-monitor.db.tar.gz" "$arch_dir/bandwidth-monitor.db"
	cp "$arch_dir/bandwidth-monitor.files.tar.gz" "$arch_dir/bandwidth-monitor.files"

	gpg --batch --yes --local-user "$gpg_key_id" --detach-sign \
		--output "$arch_dir/bandwidth-monitor.db.sig" \
		"$arch_dir/bandwidth-monitor.db"
done
