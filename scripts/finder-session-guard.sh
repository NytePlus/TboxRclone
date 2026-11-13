#!/bin/sh
# Keep an already-unlocked Mac awake during a bounded Finder test session.
# This cannot and must not bypass the macOS lock screen.
set -eu

seconds=${1:-1800}
case "$seconds" in
  ''|*[!0-9]*) echo 'usage: finder-session-guard.sh [SECONDS: 1..7200]' >&2; exit 2 ;;
esac
if [ "$seconds" -lt 1 ] || [ "$seconds" -gt 7200 ]; then
  echo 'duration must be between 1 and 7200 seconds' >&2
  exit 2
fi

echo "finder_session_awake seconds=$seconds"
exec /usr/bin/caffeinate -diu -t "$seconds"
