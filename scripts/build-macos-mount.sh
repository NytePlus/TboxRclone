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
if [ -n "${TBOX_GO:-}" ]; then
  go_binary=$TBOX_GO
elif command -v go >/dev/null 2>&1; then
  go_binary=$(command -v go)
else
  if [ "$(uname -m)" != arm64 ]; then
    echo 'Provide TBOX_GO pointing to Go 1.26.0 for this architecture' >&2
    exit 1
  fi
  archive="$build_cache/go1.26.0.darwin-arm64.tar.gz"
  expected=b1640525dfe68f066d56f200bef7bf4dce955a1a893bd061de6754c211431023
  go_binary="$build_cache/toolchain/go/bin/go"
  if [ ! -x "$go_binary" ]; then
    if [ ! -f "$archive" ]; then
      curl --fail --location --retry 2 --connect-timeout 20 \
        https://go.dev/dl/go1.26.0.darwin-arm64.tar.gz -o "$archive.partial"
      mv "$archive.partial" "$archive"
    fi
    actual=$(shasum -a 256 "$archive" | cut -d ' ' -f 1)
    if [ "$actual" != "$expected" ]; then
      echo 'Go archive checksum mismatch; archive retained for inspection' >&2
      exit 1
    fi
    mkdir -p "$build_cache/toolchain"
    tar -xzf "$archive" -C "$build_cache/toolchain"
  fi
fi
export GOTOOLCHAIN=local
export GOPATH="$build_cache/gopath"
export GOCACHE="$build_cache/gocache"
if [ "$("$go_binary" version | cut -d ' ' -f 3)" != go1.26.0 ]; then
  echo 'Go 1.26.0 is required; select it with TBOX_GO' >&2
  exit 1
fi
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
