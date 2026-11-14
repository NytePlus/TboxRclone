#!/bin/sh
# Run the native WebDAV service in the foreground; stop it with Ctrl-C.
set -eu

project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
if [ "$(uname -s)" != Darwin ]; then
  echo 'This launcher requires macOS' >&2
  exit 1
fi
if [ "$#" -ne 1 ]; then
  echo 'usage: scripts/run-macos-webdav.sh sjtu:<remote-path>' >&2
  exit 2
fi
case "$1" in
  sjtu:*) remote=$1 ;;
  *) echo 'remote must start with sjtu:' >&2; exit 2 ;;
esac

case "$(uname -m)" in
  arm64) arch=arm64 ;;
  x86_64) arch=amd64 ;;
  *) echo 'unsupported macOS architecture' >&2; exit 1 ;;
esac
binary=${TBOX_BINARY:-"$project_dir/bin/tboxrclone-darwin-$arch"}
config=${TBOX_RCLONE_CONFIG:-"$project_dir/.secrets/rclone-macos.conf"}
state_dir=${TBOX_STATE_DIR:-"$project_dir/.state/sjtu"}
owners_dir=${TBOX_OWNERSHIP_DIR:-"$project_dir/.state/owners"}
cache_dir=${TBOX_VFS_CACHE_DIR:-"$project_dir/.state/vfs"}
listen_addr=${TBOX_WEBDAV_ADDR:-127.0.0.1:8686}

case "$listen_addr" in
  127.0.0.1:*|'[::1]':*) ;;
  *) echo 'TBOX_WEBDAV_ADDR must use a loopback address' >&2; exit 2 ;;
esac

[ -x "$binary" ] || { echo "missing binary: $binary; run scripts/build-macos-webdav.sh" >&2; exit 1; }
[ -f "$config" ] || { echo "missing config: $config" >&2; exit 1; }
mkdir -p "$state_dir" "$owners_dir" "$cache_dir"
chmod 0700 "$state_dir" "$owners_dir" "$cache_dir"

export RCLONE_CONFIG_SJTU_STATE_DIR="$state_dir"
export RCLONE_SJTU_OWNERSHIP_DIR="$owners_dir"
exec "$binary" serve webdav "$remote" \
  --config "$config" \
  --addr "$listen_addr" \
  --baseurl "${TBOX_WEBDAV_BASE_URL:-}" \
  --vfs-cache-mode off \
  --dir-cache-time "${TBOX_DIR_CACHE_TIME:-5s}" \
  --no-recursive-delete \
  --cache-dir "$cache_dir"
