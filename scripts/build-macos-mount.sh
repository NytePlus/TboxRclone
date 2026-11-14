#!/bin/sh
# Build against the native SDK: Docker cross compilation cannot provide macOS CGO.
set -eu
project_dir=$(cd "$(dirname "$0")/.." && pwd)
if [ "$(uname -s)" != Darwin ]; then
  echo 'This build requires macOS and its native Xcode SDK' >&2
  exit 1
fi
sh "$project_dir/scripts/rclone-patches.sh" --check
xcrun --find clang >/dev/null
xcrun --show-sdk-path >/dev/null
build_cache=${TBOX_BUILD_CACHE:-"$project_dir/.state/native-build"}
mkdir -p "$build_cache"
go_binary=$(sh "$project_dir/scripts/select-macos-go.sh" "$build_cache")
export GOTOOLCHAIN=local
export GOPATH="$build_cache/gopath"
export GOCACHE="$build_cache/gocache"
# The cgofuse binding resolves the runtime library dynamically, but needs
# macFUSE headers at compile time. Keep these separate from driver installation.
fuse_commit=7a6cdd2b6e11071a706f4295aa931c65223ba953
fuse_archive="$build_cache/macfuse-library.tar.gz"
fuse_include="$build_cache/library-$fuse_commit/include"
if [ ! -f "$fuse_include/fuse.h" ]; then
  if [ ! -f "$fuse_archive" ]; then
    curl --fail --location --retry 2 --connect-timeout 20 \
      "https://codeload.github.com/macfuse/library/tar.gz/$fuse_commit" -o "$fuse_archive.partial"
    mv "$fuse_archive.partial" "$fuse_archive"
  fi
  actual=$(shasum -a 256 "$fuse_archive" | cut -d ' ' -f 1)
  if [ "$actual" != 3d6c946d3775a7915f2c974b75612dc509e9f094864b9c1d729afd85c73936c8 ]; then
    echo 'macFUSE headers checksum mismatch' >&2
    exit 1
  fi
  tar -xzf "$fuse_archive" -C "$build_cache"
fi
export CGO_CFLAGS="${CGO_CFLAGS:-} \"-I$fuse_include\""
cd "$project_dir"
mkdir -p bin
CGO_ENABLED=1 "$go_binary" build -trimpath -tags cmount -o bin/tboxrclone-macos-mount ./cmd/tboxrclone
bin/tboxrclone-macos-mount mount --help >/dev/null
echo 'Built bin/tboxrclone-macos-mount; runtime macFUSE availability is separate'
