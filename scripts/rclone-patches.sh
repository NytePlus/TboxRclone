#!/bin/sh
# Keep upstream provenance pinned; apply only the checked-in patch series.
set -eu
mode=${1:---check}
case "$mode" in --apply|--check) ;; *) echo 'usage: rclone-patches.sh [--apply|--check]' >&2; exit 2;; esac
project_dir=$(cd "$(dirname "$0")/.." && pwd)
upstream_dir="$project_dir/third_party/rclone"
patch_dir="$project_dir/patches/rclone"
base=$(cat "$patch_dir/base")
actual=$(git -C "$upstream_dir" rev-parse HEAD)
if [ "$actual" != "$base" ]; then
  echo 'rclone submodule revision differs from patch base; inspect before rebasing' >&2
  exit 1
fi
for patch_file in "$patch_dir"/*.patch; do
  if git -C "$upstream_dir" apply --reverse --check "$patch_file" 2>/dev/null; then
    continue
  fi
  if [ "$mode" = --check ]; then
    echo 'rclone patch missing or changed; run sh scripts/rclone-patches.sh --apply' >&2
    exit 1
  fi
  git -C "$upstream_dir" apply --check "$patch_file"
  git -C "$upstream_dir" apply "$patch_file"
done
