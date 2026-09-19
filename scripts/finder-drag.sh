#!/bin/sh
set -eu

FINDER_DRAG_VERSION='2026-09-20.2'
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
if [ "${1:-}" = '--record-repro' ]; then
  shift
  exec sh "$root/scripts/finder-record.sh" --reproduce-pending "$@"
fi
if [ "${1:-}" = '--record' ]; then
  shift
  exec sh "$root/scripts/finder-record.sh" "$@"
fi
if [ "${1:-}" = '--preflight' ]; then
  shift
  exec python3 "$root/scripts/finder-preflight.py" "$@"
fi
if [ "${1:-}" = '--open' ]; then
  [ "$#" -eq 3 ] || { echo "usage: $0 --open SOURCE_DIRECTORY TARGET_DIRECTORY" >&2; exit 2; }
  [ -d "$2" ] && [ -d "$3" ] || { echo 'Both directories must already exist' >&2; exit 2; }
  source_dir=$(CDPATH= cd -- "$2" && pwd -P)
  target_dir=$(CDPATH= cd -- "$3" && pwd -P)
  [ "$source_dir" != "$target_dir" ] || exit 2
  /usr/bin/osascript - "$source_dir" "$target_dir" <<'APPLESCRIPT'
on run paths
  tell application "Finder"
    activate
    set screenBounds to bounds of window of desktop
    set halfWidth to ((item 3 of screenBounds) - (item 1 of screenBounds)) div 2
    repeat with i from 1 to 2
      set folderRef to (POSIX file (item i of paths)) as alias
      set w to missing value
      repeat with candidate in Finder windows
        try
          if (target of candidate as alias) is folderRef then
            set w to contents of candidate
            exit repeat
          end if
        end try
      end repeat
      if w is missing value then set w to make new Finder window to folderRef
      set current view of w to list view
      set leftEdge to (item 1 of screenBounds) + (i - 1) * halfWidth
      set bounds of w to {leftEdge, 60, leftEdge + halfWidth, (item 4 of screenBounds) - 60}
    end repeat
  end tell
end run
APPLESCRIPT
  echo "finder_windows_opened version=$FINDER_DRAG_VERSION"
  exit 0
fi
if [ "$#" -ne 3 ] && [ "$#" -ne 6 ]; then
  echo "usage: $0 SOURCE_TITLE TARGET_TITLE FILENAME [or START_X START_Y END_X END_Y]" >&2
  exit 2
fi

cache=${TBOX_SWIFT_MODULE_CACHE:-/private/tmp/tboxrclone-swift-module-cache}
mkdir -p "$cache"

swiftc -parse "$root/scripts/finder-drag.swift"
echo "finder_drag_wrapper_version=$FINDER_DRAG_VERSION"
exec swift -module-cache-path "$cache" "$root/scripts/finder-drag.swift" "$@"
