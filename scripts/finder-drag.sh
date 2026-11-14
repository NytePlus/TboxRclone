#!/bin/sh
set -eu

FINDER_DRAG_VERSION='2026-11-12.1'
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
if [ "${1:-}" = '--record-download' ]; then
  shift
  exec sh "$root/scripts/finder/record.sh" --download "$@"
fi
if [ "${1:-}" = '--record-overwrite' ]; then
  shift
  exec sh "$root/scripts/finder/record.sh" --allow-existing "$@"
fi
if [ "${1:-}" = '--record' ]; then
  shift
  exec sh "$root/scripts/finder/record.sh" "$@"
fi
if [ "${1:-}" = '--preflight' ]; then
  shift
  exec python3 "$root/scripts/finder/preflight.py" "$@"
fi
if [ "${1:-}" = '--open' ]; then
  [ "$#" -eq 3 ] || { echo "usage: $0 --open SOURCE_DIRECTORY TARGET_DIRECTORY" >&2; exit 2; }
  [ -d "$2" ] && [ -d "$3" ] || { echo 'Both directories must already exist' >&2; exit 2; }
  source_dir=$(CDPATH= cd -- "$2" && pwd -P)
  target_dir=$(CDPATH= cd -- "$3" && pwd -P)
  [ "$source_dir" != "$target_dir" ] || exit 2
  cache=${TBOX_SWIFT_MODULE_CACHE:-/private/tmp/tboxrclone-swift-module-cache}
  mkdir -p "$cache"
  python3 "$root/scripts/finder/run-bounded.py" --timeout 30 \
    swift -module-cache-path "$cache" "$root/scripts/finder/open.swift" "$source_dir" "$target_dir"
  echo "finder_windows_opened version=$FINDER_DRAG_VERSION"
  exit 0
fi
if [ "$#" -ne 3 ] && [ "$#" -ne 6 ]; then
  echo "usage: $0 SOURCE_TITLE TARGET_TITLE FILENAME [or START_X START_Y END_X END_Y]" >&2
  exit 2
fi

cache=${TBOX_SWIFT_MODULE_CACHE:-/private/tmp/tboxrclone-swift-module-cache}
mkdir -p "$cache"

swiftc -parse "$root/scripts/finder/drag.swift"
echo "finder_drag_wrapper_version=$FINDER_DRAG_VERSION"
exec swift -module-cache-path "$cache" "$root/scripts/finder/drag.swift" "$@"
