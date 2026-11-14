#!/bin/sh
# Repeatable regression and evidence audit; never substitutes for Finder runs.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
[ "$#" -eq 1 ] || { echo 'usage: scripts/system-check.sh NEW_REPORT_DIRECTORY' >&2; exit 2; }
mkdir -p "$root/evidence/reports"
mkdir "$1"
report=$(CDPATH= cd -- "$1" && pwd)
case "$report" in "$root"/evidence/reports/*) ;; *) echo 'report directory must be below evidence/reports' >&2; exit 2;; esac
relative=${report#"$root"/}
compose() { docker compose -f docker-compose.yml -f docker-compose.doh.yml "$@"; }
result=0
run_check() {
  name=$1
  shift
  code=0
  "$@" > "$report/$name.log" 2>&1 || code=$?
  printf '%s\t%s\n' "$name" "$code" >> "$report/status.tsv"
  cat "$report/$name.log"
  if [ "$code" -ne 0 ]; then result=1; fi
}
run_check patch sh scripts/rclone-patches.sh --check
run_check patch-replay python3 scripts/check-rclone-patch-replay.py
run_check layout python3 scripts/quality/check_source_layout.py
run_check rclone-backend-contract compose run --rm go-tests go test ./backend/sjtu/test -run '^TestBackendContract$' -count=1
run_check race compose run --rm go-tests go test -race ./...
run_check vet compose run --rm go-tests go vet ./...
run_check evidence compose run --rm go-tests go run ./cmd/verify --output "$relative/acceptance"
printf 'Finder operations were not rerun. A nonzero evidence gate means full acceptance remains incomplete.\n'
exit "$result"
