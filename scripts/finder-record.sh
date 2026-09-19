#!/bin/sh
# A bounded capture is evidence, never an automatic UI PASS.
set -eu
preflight_mode=''
if [ "${1:-}" = '--reproduce-pending' ]; then
  preflight_mode='--reproduce-pending'
  shift
fi
[ "$#" -eq 5 ] || { echo 'usage: finder-drag.sh --record SOURCE TARGET FILE MOUNT NEW_EVIDENCE_DIR' >&2; exit 2; }
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
mkdir "$5"
evidence=$(CDPATH= cd -- "$5" && pwd)
if ! "$root/scripts/finder-drag.sh" --preflight "$1" "$2" "$3" "$4" ${preflight_mode:+"$preflight_mode"} > "$evidence/preflight.json"; then
  cat "$evidence/preflight.json"
  exit 2
fi
cache=${TBOX_SWIFT_MODULE_CACHE:-/private/tmp/tboxrclone-swift-module-cache}
mkdir -p "$cache"
displays=$(swift -module-cache-path "$cache" -e 'import CoreGraphics; var n: UInt32 = 0; guard CGGetActiveDisplayList(0,nil,&n) == .success else { exit(1) }; print(n)')
case "$displays" in ''|*[!0-9]*|0) echo 'Cannot enumerate active displays' >&2; exit 2;; esac
event() {
  python3 - "$evidence/timeline.jsonl" "$1" <<'PY_EVENT'
import datetime, json, sys, time
with open(sys.argv[1], 'a') as f:
    f.write(json.dumps({'event':sys.argv[2], 'unix_seconds':time.time(),
                        'utc':datetime.datetime.now(datetime.timezone.utc).isoformat()}) + '\n')
PY_EVENT
}
if [ -n "$preflight_mode" ]; then event FAILURE_REPRODUCTION_NOT_ACCEPTANCE; fi
# Window loading can exceed the bounded recording. Finish it first; the Swift
# drag re-raises and validates both windows after capture starts.
event OPEN_WINDOWS_BEGIN
if ! "$root/scripts/finder-drag.sh" --open "$1" "$2" > "$evidence/open.log" 2>&1; then
  exit 1
fi
event OPEN_WINDOWS_FINISHED
event CAPTURE_LAUNCH_BEGIN
recorders=''
i=1
while [ "$i" -le "$displays" ]; do
  /usr/sbin/screencapture -v -D "$i" -k -V 15 "$evidence/display-$i.mov" > "$evidence/display-$i.log" 2>&1 &
  recorders="$recorders $!"
  i=$((i + 1))
done
# Let capture initialize before opening windows; do not interrupt video writers.
sleep 4
capture_alive=true
for pid in $recorders; do kill -0 "$pid" 2>/dev/null || capture_alive=false; done
drag_status=NOT_RUN
if [ "$capture_alive" = true ]; then
    event DRAG_BEGIN
    if "$root/scripts/finder-drag.sh" "$(basename "$1")" "$(basename "$2")" "$3" > "$evidence/drag.log" 2>&1; then
      drag_status=EVENTS_SENT
      event DRAG_EVENTS_SENT
    else
      drag_status=SCRIPT_FAILED
      event DRAG_SCRIPT_FAILED
    fi
fi
event WAIT_FOR_CAPTURE_COMPLETION
capture_status=SAVED
for pid in $recorders; do wait "$pid" || capture_status=FAILED; done
i=1
while [ "$i" -le "$displays" ]; do
  [ -s "$evidence/display-$i.mov" ] || capture_status=FAILED
  i=$((i + 1))
done
event CAPTURE_WAIT_FINISHED
printf 'drag=%s\ncapture=%s\nacceptance=INCOMPLETE_REQUIRES_FULL_VIDEO_REVIEW_AND_CONTENT_VERIFICATION\n' "$drag_status" "$capture_status" > "$evidence/status.txt"
cat "$evidence/status.txt"
echo 'If copy remains active at recording end, coverage is incomplete; do not mark PASS.'
if [ "$capture_status" != SAVED ] || [ "$drag_status" != EVENTS_SENT ]; then
  exit 1
fi
# A saved recording and delivered mouse events do not establish acceptance.
# Exit 3 explicitly distinguishes evidence awaiting review from a passing test.
exit 3
