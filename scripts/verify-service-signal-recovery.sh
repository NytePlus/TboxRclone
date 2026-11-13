#!/bin/sh
# Exercise durable upload recovery after signaling the WebDAV service PID 1.
# This is protocol/process evidence only: Finder and host power-loss evidence are separate.
set -eu

[ "$#" -ge 2 ] && [ "$#" -le 3 ] || { echo 'usage: verify-service-signal-recovery.sh SIGTERM|SIGKILL FRESH_REMOTE_BASENAME [ABSOLUTE_EVIDENCE_JSON]' >&2; exit 2; }
signal=$1
base=$2
output=${3:-}
case "$signal" in SIGTERM|SIGKILL) ;; *) echo 'signal must be SIGTERM or SIGKILL' >&2; exit 2;; esac
case "$base" in ''|*/*|*\\*|.|..) echo 'remote directory must be one fresh basename' >&2; exit 2;; esac

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
if [ -n "$output" ]; then
  case "$output" in "$root"/evidence/*.json) ;; *) echo 'evidence output must be an absolute JSON path below evidence' >&2; exit 2;; esac
  [ ! -e "$output" ] || { echo "refusing to overwrite evidence: $output" >&2; exit 1; }
  [ -d "$(dirname -- "$output")" ] || { echo 'evidence output directory does not exist' >&2; exit 1; }
fi
origin=http://127.0.0.1:8687
remote="codex-api-lab/$base"
state_name="signal-$base"
state_container="/state/$state_name"
state_host="$root/.state/$state_name"
ownership_container="/state/owners-$state_name"
work=$(mktemp -d /private/tmp/tbox-signal-recovery.XXXXXX)
upload_pid=
live_snapshot="$work/live-service.json"

compose() {
  TBOX_LAB_REMOTE="sjtu:$remote" TBOX_STATE_DIR="$state_container" \
  TBOX_OWNERSHIP_DIR="$ownership_container" \
  TBOX_FAULT_HOSTS='pan.sjtu.edu.cn:443,s3pan.jcloud.sjtu.edu.cn:443,s3pan1.jcloud.sjtu.edu.cn:443,s3pan2.jcloud.sjtu.edu.cn:443,s3pan3.jcloud.sjtu.edu.cn:443,s3pan4.jcloud.sjtu.edu.cn:443' \
  docker compose -f "$root/docker-compose.yml" -f "$root/docker-compose.doh.yml" "$@"
}
restore() {
  if [ -n "$upload_pid" ]; then kill "$upload_pid" 2>/dev/null || true; wait "$upload_pid" 2>/dev/null || true; fi
  compose --profile faults down --remove-orphans >/dev/null 2>&1 || true
  python3 "$root/scripts/live-webdav-state.py" restore "$live_snapshot" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap restore EXIT HUP INT TERM

python3 "$root/scripts/live-webdav-state.py" snapshot "$live_snapshot"

[ ! -e "$state_host" ] || { echo "state directory already exists: $state_host" >&2; exit 1; }
mkdir -m 700 "$state_host"
dd if=/dev/zero of="$work/fixture.bin" bs=1048576 count=9 status=none
fixture_sha=$(shasum -a 256 "$work/fixture.bin" | awk '{print $1}')
fixture_size=$(wc -c < "$work/fixture.bin" | tr -d ' ')

# The normal and fault services share the cloud-space ownership registry, so
# only one managed service may run at a time. The EXIT trap restores the exact
# captured remote, state, ownership, spool, lab-policy and base URL.
docker compose -f "$root/docker-compose.yml" -f "$root/docker-compose.doh.yml" --profile live stop webdav >/dev/null
compose --profile faults up -d fault-proxy webdav-faults

attempt=0
while [ "$attempt" -lt 60 ]; do
  ready=$(curl -sS --max-time 2 -o /dev/null -w '%{http_code}' -X OPTIONS "$origin/" 2>/dev/null || true)
  [ "$ready" = 200 ] && break
  attempt=$((attempt + 1))
  sleep 1
done
[ "${ready:-}" = 200 ] || { echo 'fault WebDAV did not become ready' >&2; exit 1; }

rule_id="st003-$base-$signal"
rule=$(jq -cn --arg id "$rule_id" '[{id:($id+"-s3pan"),host:"s3pan.jcloud.sjtu.edu.cn:443",method:"PUT",path_prefix:"/",nth:2,action:"hold_response"}] + [range(1;5) | {id:($id+"-s3pan"+(tostring)),host:("s3pan"+(tostring)+".jcloud.sjtu.edu.cn:443"),method:"PUT",path_prefix:"/",nth:2,action:"hold_response"}]')
configured=$(compose exec -T fault-proxy curl -sS -o /dev/null -w '%{http_code}' -X PUT -H 'Content-Type: application/json' --data-binary "$rule" http://127.0.0.1:8788/rules)
[ "$configured" = 204 ] || { echo "fault rule rejected: $configured" >&2; exit 1; }

curl -sS --max-time 180 -o "$work/put.body" -w '%{http_code}' -X PUT --data-binary "@$work/fixture.bin" "$origin/recovery.bin" > "$work/put.status" &
upload_pid=$!

held_request=
attempt=0
while [ "$attempt" -lt 120 ]; do
  events=$(compose exec -T fault-proxy curl -sS http://127.0.0.1:8788/events)
  held_request=$(printf '%s' "$events" | jq -r --arg id "$rule_id-s3pan" '[.[] | select((.rule // "") | startswith($id)) | select(.phase == "held")][-1].request // empty')
  [ -n "$held_request" ] && break
  attempt=$((attempt + 1))
  sleep 1
done
[ -n "$held_request" ] || { echo 'data-part response was not held' >&2; exit 1; }

record=$(find "$state_host" -maxdepth 1 -type f -name '*.json' -print | head -n 1)
[ -n "$record" ] || { echo 'durable operation record not found' >&2; exit 1; }
before="$work/before.json"
cp "$record" "$before"
op=$(jq -r '.id' "$before")
spool="$state_host/$op.data"
[ -f "$spool" ] || { echo 'durable spool not found' >&2; exit 1; }
before_state=$(jq -r '.state' "$before")
before_upload_id=$(jq -r '.upload_id' "$before")
before_key=$(jq -r '.confirm_key' "$before")
before_spool_sha=$(shasum -a 256 "$spool" | awk '{print $1}')
[ "$before_state" = Uploading ] && [ -n "$before_upload_id" ] && [ -n "$before_key" ] && [ "$before_spool_sha" = "$fixture_sha" ] || {
  echo 'durable boundary was not established before signal' >&2; exit 1;
}

core_cid=$(compose ps -q webdav-faults)
[ -n "$core_cid" ] || { echo 'fault WebDAV container not found' >&2; exit 1; }
compose kill -s "$signal" webdav-faults >/dev/null
attempt=0
while [ "$attempt" -lt 60 ]; do
  running=$(docker inspect -f '{{.State.Running}}' "$core_cid")
  [ "$running" = false ] && break
  attempt=$((attempt + 1))
  sleep 1
done
[ "$(docker inspect -f '{{.State.Running}}' "$core_cid")" = false ] || { echo 'WebDAV service did not exit after signal' >&2; exit 1; }
core_exit=$(docker inspect -f '{{.State.ExitCode}}' "$core_cid")

compose exec -T fault-proxy curl -sS -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' --data-binary "{\"request\":$held_request,\"forward\":false}" http://127.0.0.1:8788/release > "$work/release.status"
wait "$upload_pid" 2>/dev/null || true
upload_pid=
after_signal="$work/after-signal.json"
cp "$record" "$after_signal"
after_signal_spool_sha=$(shasum -a 256 "$spool" | awk '{print $1}')

# Run the recovery utility as a separate process on the same persistent state.
compose run --rm --no-deps fault-client sh -c '
  op=$1
  state=$2
  lib=$(sed -n "s/^library_id *= *//p" /secrets/rclone.conf)
  space=$(sed -n "s/^space_id *= *//p" /secrets/rclone.conf)
  ownership=$3
  exec go run ./cmd/tbox-state --state-dir "$state" --ownership-dir "$ownership" --library-id "$lib" --space-id "$space" --token-file /secrets/access-token --user-token-file /secrets/user-token --resume "$op"
' sh "$op" "$state_container" "$ownership_container" > "$work/resume.out"

after="$work/after.json"
cp "$record" "$after"
after_state=$(jq -r '.state' "$after")
after_upload_id=$(jq -r '.upload_id' "$after")
after_key=$(jq -r '.confirm_key' "$after")
after_spool_sha=$(shasum -a 256 "$spool" | awk '{print $1}')
same_upload_id=false
same_confirm_key=false
[ "$before_upload_id" = "$after_upload_id" ] && same_upload_id=true
[ "$before_key" = "$after_key" ] && same_confirm_key=true

compose run --rm --no-deps fault-client go run ./cmd/finder-inspect -space-file /secrets/space.json -token-file /secrets/user-token "$remote" > "$work/cloud.json"
cloud_sha=$(jq -r '.files["recovery.bin"].sha256 // empty' "$work/cloud.json")
cloud_size=$(jq -r '.files["recovery.bin"].size // -1' "$work/cloud.json")
events=$(compose exec -T fault-proxy curl -sS http://127.0.0.1:8788/events)
put_status=$(cat "$work/put.status")
release_status=$(cat "$work/release.status")
case "$signal:$core_exit" in SIGTERM:0|SIGTERM:143|SIGKILL:137) exit_ok=true;; *) exit_ok=false;; esac
[ "$after_state" = Committed ] && [ "$same_upload_id" = true ] && [ "$same_confirm_key" = true ] && \
  [ "$after_signal_spool_sha" = "$fixture_sha" ] && [ "$after_spool_sha" = "$fixture_sha" ] && \
  [ "$cloud_sha" = "$fixture_sha" ] && [ "$cloud_size" = "$fixture_size" ] && [ "$exit_ok" = true ] && pass=true || pass=false

jq -n --arg at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg signal "$signal" --arg remote "$remote" \
  --arg scope 'Docker service PID 1 and real cloud; protocol/process supplement only, not Finder or host crash/power loss' \
  --argjson core_exit "$core_exit" --argjson exit_ok "$exit_ok" --arg before_state "$before_state" \
  --arg after_signal_state "$(jq -r '.state' "$after_signal")" --arg after_state "$after_state" \
  --arg upload_id "$before_upload_id" --arg confirm_key_sha "$(printf '%s' "$before_key" | shasum -a 256 | awk '{print $1}')" \
  --arg fixture_sha256 "$fixture_sha" --arg after_signal_spool_sha256 "$after_signal_spool_sha" --arg after_spool_sha256 "$after_spool_sha" \
  --arg cloud_sha256 "$cloud_sha" --argjson bytes "$fixture_size" --argjson cloud_bytes "$cloud_size" \
  --arg put_status "$put_status" --arg release_status "$release_status" --argjson held_request "$held_request" \
  --argjson same_upload_id "$same_upload_id" --argjson same_confirm_key "$same_confirm_key" \
  --argjson acknowledged_before "$(jq '[.parts[] | select((.etag // "") != "") | .number]' "$before")" \
  --argjson acknowledged_after_signal "$(jq '[.parts[] | select((.etag // "") != "") | .number]' "$after_signal")" \
  --argjson events "$events" --argjson pass "$pass" \
  '{at:$at,signal:$signal,scope:$scope,remote:$remote,core_exit:$core_exit,exit_ok:$exit_ok,durable_before_signal:{state:$before_state,bytes:$bytes,sha256:$fixture_sha256,upload_id:$upload_id,confirm_key_sha256:$confirm_key_sha,acknowledged_parts:$acknowledged_before},after_signal:{state:$after_signal_state,spool_sha256:$after_signal_spool_sha256,acknowledged_parts:$acknowledged_after_signal},recovery:{state:$after_state,same_upload_id:$same_upload_id,same_confirm_key:$same_confirm_key,spool_sha256:$after_spool_sha256},independent_cloud_read:{bytes:$cloud_bytes,sha256:$cloud_sha256},held_request:$held_request,release_status:$release_status,interrupted_put_status:$put_status,proxy_events:[$events[] | select((.rule // "") != "")],pass:$pass}' > "$work/report.json"
if [ -n "$output" ]; then install -m 644 "$work/report.json" "$output"; fi
cat "$work/report.json"
[ "$pass" = true ]
