#!/bin/sh
# Print the validated Go binary used by every native macOS build script.
set -eu

go_version=1.26.0
go_arm64_sha256=b1640525dfe68f066d56f200bef7bf4dce955a1a893bd061de6754c211431023

[ "$#" -eq 1 ] || { echo 'usage: select-macos-go.sh BUILD_CACHE' >&2; exit 2; }
build_cache=$1
arch=$(uname -m)
mkdir -p "$build_cache"

if [ -n "${TBOX_GO:-}" ]; then
  go_binary=$TBOX_GO
elif command -v go >/dev/null 2>&1; then
  go_binary=$(command -v go)
elif [ "$arch" = arm64 ]; then
  archive="$build_cache/go${go_version}.darwin-arm64.tar.gz"
  go_binary="$build_cache/toolchain/go/bin/go"
  if [ ! -x "$go_binary" ]; then
    if [ ! -f "$archive" ]; then
      curl --fail --location --retry 2 --connect-timeout 20 \
        "https://go.dev/dl/go${go_version}.darwin-arm64.tar.gz" -o "$archive.partial"
      mv "$archive.partial" "$archive"
    fi
    actual=$(shasum -a 256 "$archive" | cut -d ' ' -f 1)
    if [ "$actual" != "$go_arm64_sha256" ]; then
      echo 'Go archive checksum mismatch; archive retained for inspection' >&2
      exit 1
    fi
    mkdir -p "$build_cache/toolchain"
    tar -xzf "$archive" -C "$build_cache/toolchain"
  fi
else
  echo "Install Go $go_version or set TBOX_GO to its binary on Intel macOS" >&2
  exit 1
fi

if [ "$("$go_binary" version | cut -d ' ' -f 3)" != "go$go_version" ]; then
  echo "Go $go_version is required; select it with TBOX_GO" >&2
  exit 1
fi
printf '%s\n' "$go_binary"
