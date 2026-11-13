#!/bin/sh
# Drive a bounded real-Finder directory browse, capture only that Finder window,
# and emit machine-readable AX status-bar evidence. Computer Use should inspect
# the final Finder state once; it need not perform every repeated navigation.
set -eu

[ "$#" -eq 2 ] || { echo 'usage: finder-browse.sh SPEC.json NEW_EVIDENCE_DIRECTORY' >&2; exit 2; }
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
spec=$(CDPATH= cd -- "$(dirname -- "$1")" && pwd)/$(basename "$1")
[ -f "$spec" ] || { echo 'specification does not exist' >&2; exit 2; }
mkdir "$2"
evidence=$(CDPATH= cd -- "$2" && pwd)
cache=${TBOX_SWIFT_MODULE_CACHE:-/private/tmp/tboxrclone-swift-module-cache}
mkdir -p "$cache"

swiftc -parse "$root/scripts/finder/browse.swift"
swiftc -parse "$root/scripts/finder/record-window.swift"
swift -module-cache-path "$cache" "$root/scripts/finder/browse.swift" prepare "$spec" "$evidence/window.json" > "$evidence/prepare.log" 2>&1
window_id=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["window_id"])' "$evidence/window.json")
title=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["title"])' "$evidence/window.json")
timeout=$(python3 -c 'import json,sys; print(int(json.load(open(sys.argv[1]))["timeout_seconds"]))' "$spec")
recording_timeout=$((timeout + 5))
stop_file="$evidence/stop-recording"

swift -module-cache-path "$cache" "$root/scripts/finder/record-window.swift" \
  "$window_id" "$title" "$evidence/finder.mp4" "$stop_file" "$recording_timeout" > "$evidence/record.log" 2>&1 &
recorder=$!
cleanup() {
  : > "$stop_file"
  wait "$recorder" 2>/dev/null || true
}
trap cleanup EXIT HUP INT TERM

i=0
while ! grep -q RECORDING_STARTED "$evidence/record.log" 2>/dev/null; do
  kill -0 "$recorder" 2>/dev/null || { cat "$evidence/record.log" >&2; exit 2; }
  i=$((i + 1))
  [ "$i" -lt 50 ] || { echo 'recording did not start within 5 seconds' >&2; exit 2; }
  sleep 0.1
done

status=0
swift -module-cache-path "$cache" "$root/scripts/finder/browse.swift" run "$spec" "$window_id" "$evidence/result.json" > "$evidence/run.log" 2>&1 || status=$?
: > "$stop_file"
wait "$recorder" || status=2
trap - EXIT HUP INT TERM
[ -s "$evidence/finder.mp4" ] || status=2
cat "$evidence/run.log"
cat "$evidence/record.log"
exit "$status"
