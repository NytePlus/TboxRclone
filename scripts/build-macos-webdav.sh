#!/bin/sh
# Build the native macOS WebDAV service without Docker or macFUSE.
set -eu

project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
if [ "$(uname -s)" != Darwin ]; then
  echo 'This build requires macOS' >&2
  exit 1
fi

sh "$project_dir/scripts/rclone-patches.sh" --check
arch=$(uname -m)
case "$arch" in
  arm64|x86_64) ;;
  *) echo "unsupported macOS architecture: $arch" >&2; exit 1 ;;
esac
[ "$arch" != x86_64 ] || output_arch=amd64
output_arch=${output_arch:-arm64}

build_cache=${TBOX_BUILD_CACHE:-"$project_dir/.state/native-build"}
mkdir -p "$build_cache"
go_binary=$(sh "$project_dir/scripts/select-macos-go.sh" "$build_cache")

export GOTOOLCHAIN=local
export GOPATH="$build_cache/gopath"
export GOCACHE="$build_cache/gocache"
cd "$project_dir"
mkdir -p bin
output="bin/tboxrclone-darwin-$output_arch"
state_output="bin/tbox-state-darwin-$output_arch"
CGO_ENABLED=0 GOOS=darwin GOARCH="$output_arch" "$go_binary" build -trimpath -o "$output" ./cmd/tboxrclone
CGO_ENABLED=0 GOOS=darwin GOARCH="$output_arch" "$go_binary" build -trimpath -o "$state_output" ./cmd/tbox-state
"$output" version
shasum -a 256 "$output" "$state_output"
echo "Built $output and $state_output"
