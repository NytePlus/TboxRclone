#!/bin/sh
# A bounded capture is evidence, never an automatic UI PASS.
set -eu
# Include completion of short cloud uploads, not only the drag gesture.
record_seconds=${TBOX_FINDER_RECORD_SECONDS:-45}
case "$record_seconds" in ''|*[!0-9]*) echo 'record duration must be seconds' >&2; exit 2;; esac
[ "$record_seconds" -ge 15 ] && [ "$record_seconds" -le 180 ] || { echo 'record duration must be 15..180 seconds' >&2; exit 2; }
preflight_mode=''
if [ "${1:-}" = '--allow-existing' ] || [ "${1:-}" = '--download' ]; then
  preflight_mode=$1
  shift
fi
[ "$#" -eq 5 ] || { echo 'usage: finder-drag.sh --record SOURCE TARGET FILE MOUNT NEW_EVIDENCE_DIR' >&2; exit 2; }
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
mkdir "$5"
evidence=$(CDPATH= cd -- "$5" && pwd)
if ! "$root/scripts/finder-drag.sh" --preflight "$1" "$2" "$3" "$4" ${preflight_mode:+"$preflight_mode"} > "$evidence/preflight.json"; then
  cat "$evidence/preflight.json"
  exit 2
fi
cache=${TBOX_SWIFT_MODULE_CACHE:-/private/tmp/tboxrclone-swift-module-cache}
mkdir -p "$cache"
sh "$root/scripts/finder-session-guard.sh" "$((record_seconds + 90))" > "$evidence/awake.log" 2>&1 &
awake_pid=$!
monitor_pid=''
trap 'kill "$awake_pid" 2>/dev/null || true; if [ -n "$monitor_pid" ]; then kill "$monitor_pid" 2>/dev/null || true; fi' EXIT
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
if [ "$preflight_mode" = --allow-existing ]; then event SEQUENTIAL_OVERWRITE_TEST; fi
# Window loading can exceed the bounded recording. Finish it first; the Swift
# drag re-raises and validates both windows after capture starts.
event OPEN_WINDOWS_BEGIN
if ! TBOX_FINDER_SELECT_FILENAME="$3" "$root/scripts/finder-drag.sh" --open "$1" "$2" > "$evidence/open.log" 2>&1; then
  exit 1
fi
event OPEN_WINDOWS_FINISHED
# Check before starting a bounded recording so a layout failure does not waste
# the entire capture interval. The drag helper repeats this check before input.
event ENDPOINT_CHECK_BEGIN
if ! TBOX_FINDER_CHECK_ONLY=1 "$root/scripts/finder-drag.sh" "$(basename "$1")" "$(basename "$2")" "$3" > "$evidence/endpoint-check.log" 2>&1; then
  event REARRANGE_WINDOWS_BEGIN
  if ! TBOX_FINDER_SELECT_FILENAME="$3" "$root/scripts/finder-drag.sh" --open "$1" "$2" > "$evidence/rearrange.log" 2>&1; then exit 1; fi
  if ! TBOX_FINDER_CHECK_ONLY=1 "$root/scripts/finder-drag.sh" "$(basename "$1")" "$(basename "$2")" "$3" > "$evidence/endpoint-recheck.log" 2>&1; then exit 1; fi
fi
event ENDPOINT_CHECK_FINISHED
expected_error=${TBOX_FINDER_STOP_ERROR_TEXT:-}
if [ -n "$expected_error" ]; then
  swift -module-cache-path "$cache" "$root/scripts/finder/inspect-dialog.swift" "$3" > "$evidence/dialog-before-drag.json"
  python3 - "$evidence/dialog-before-drag.json" "$expected_error" <<'PY_DIALOG'
import json, sys
dialogs = json.load(open(sys.argv[1]))['dialogs']
if any(sys.argv[2] in text for dialog in dialogs for text in dialog['texts']):
    raise SystemExit('matching error already present before drag; dismiss/review it before a new run')
PY_DIALOG
fi
# Read faults must not interfere with revealing the fixture or endpoint checks.
if [ -n "${TBOX_FINDER_BEFORE_CAPTURE_RULES:-}" ]; then
  event ARM_FAULT_BEGIN
  python3 "$root/scripts/finder/fault-control.py" rules "$TBOX_FINDER_BEFORE_CAPTURE_RULES"
  event ARM_FAULT_FINISHED
fi
event CAPTURE_LAUNCH_BEGIN
recorders=''
i=1
while [ "$i" -le "$displays" ]; do
  swift -module-cache-path "$cache" "$root/scripts/finder/record-display.swift" "$i" "$evidence/display-$i.mov" "$record_seconds" "$evidence/expected-error.stop" > "$evidence/display-$i.log" 2>&1 &
  recorders="$recorders $!"
  i=$((i + 1))
done
# Let capture initialize before opening windows; do not interrupt video writers.
sleep 4
capture_alive=true
for pid in $recorders; do kill -0 "$pid" 2>/dev/null || capture_alive=false; done
i=1
while [ "$i" -le "$displays" ]; do
  if ! rg -q '^RECORDING_STARTED$' "$evidence/display-$i.log"; then capture_alive=false; fi
  i=$((i + 1))
done
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
if [ "$drag_status" = EVENTS_SENT ] && [ -n "$expected_error" ]; then
  event ERROR_WATCH_BEGIN
  swift -module-cache-path "$cache" "$root/scripts/finder/inspect-dialog.swift" --watch "$3" "$expected_error" "$evidence/expected-error.json" "$evidence/expected-error.stop" "$record_seconds" > "$evidence/error-watch.log" 2>&1 &
  monitor_pid=$!
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
if [ -n "$monitor_pid" ]; then
  kill "$monitor_pid" 2>/dev/null || true
  wait "$monitor_pid" 2>/dev/null || true
  monitor_pid=''
fi
# A nonempty movie may still have been stopped early by the recording service.
# Preserve it, but make the coverage gap explicit in machine-readable evidence.
python3 - "$evidence/timeline.jsonl" "$record_seconds" "$evidence/capture-coverage.json" <<'PY_COVERAGE'
import json, sys
from pathlib import Path
rows = [json.loads(line) for line in open(sys.argv[1])]
times = {row['event']: row['unix_seconds'] for row in rows}
elapsed = times['CAPTURE_WAIT_FINISHED'] - times['CAPTURE_LAUNCH_BEGIN']
requested = int(sys.argv[2])
directory = Path(sys.argv[3]).parent
logs = list(directory.glob('display-*.log'))
expected_stop = (directory / 'expected-error.json').exists() and bool(logs) and all('EXPECTED_ERROR_CAPTURE_FINISHED' in p.read_text() for p in logs)
ended_early = elapsed < requested - 2 and not expected_stop
with open(sys.argv[3], 'w') as output:
    json.dump({'requested_seconds': requested, 'elapsed_seconds': elapsed,
               'ended_early': ended_early,
               'stop_reason': 'expected_error_detected' if expected_stop else 'duration_or_interruption',
               'saved_wait_seconds': max(0, requested - elapsed) if expected_stop else 0,
               'ui_verdict': 'REQUIRES_SEPARATE_REVIEW'}, output, indent=2)
    output.write('\n')
if ended_early:
    print('Recording interrupted: skip video review; verify final task state and content')
PY_COVERAGE
printf 'drag=%s\ncapture=%s\nacceptance=REQUIRES_FINAL_STATE_AND_CONTENT_VERIFICATION\n' "$drag_status" "$capture_status" > "$evidence/status.txt"
cat "$evidence/status.txt"
echo 'Recording is supplementary. Verify final task state, journal and independent content before PASS.'
if [ "$drag_status" != EVENTS_SENT ]; then
  exit 1
fi
# A saved recording and delivered mouse events do not establish acceptance.
# Exit 3 explicitly distinguishes evidence awaiting review from a passing test.
exit 3
