#!/bin/sh
# Execute through the real macOS terminal for ST-011; API checks are separate.
set -eu
[ "$#" -eq 3 ] || { echo 'usage: mounted-rmdir-case.sh TARGET empty|nonempty NEW_EVIDENCE_DIR' >&2; exit 2; }
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
target=$1
expected=$2
case "$expected" in empty|nonempty) ;; *) exit 2;; esac
mountpoint=${TBOX_WEBDAV_MOUNT:-/private/tmp/tboxrclone-webdav-$(id -u)}
case "$target" in "$mountpoint"/system-test-*/empty|"$mountpoint"/system-test-*/nonempty) ;; *) echo 'target must be an isolated system-test fixture' >&2; exit 2;; esac
mkdir "$3"
evidence=$(CDPATH= cd -- "$3" && pwd)
python3 "$root/scripts/mount-webdav-macos.py" check > "$evidence/mount.log"
[ -d "$target" ] && [ ! -L "$target" ] || exit 2
/usr/bin/find "$target" -print > "$evidence/before.txt"
count=$(wc -l < "$evidence/before.txt" | tr -d ' ')
if [ "$expected" = empty ]; then [ "$count" -eq 1 ] || exit 2; else [ "$count" -gt 1 ] || exit 2; fi
status=0
/bin/rmdir "$target" > "$evidence/rmdir.log" 2>&1 || status=$?
printf 'rmdir_exit=%s\nexpected=%s\ntarget=%s\n' "$status" "$expected" "$target" > "$evidence/status.txt"
cat "$evidence/status.txt" "$evidence/rmdir.log"
if [ "$expected" = empty ]; then
  [ "$status" -eq 0 ] && [ ! -e "$target" ]
else
  [ "$status" -ne 0 ] && [ -d "$target" ]
fi
echo 'mounted_operation_matches_expectation; independent cloud verification still required'
