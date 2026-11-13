#!/bin/sh
# Verify live multipart recovery branches without deleting cloud data.
# Protocol/external-event evidence supplements, but never replaces, Finder UI.
set -eu

[ "$#" -ge 2 ] && [ "$#" -le 3 ] || { echo 'usage: verify-multipart-recovery.sh valid-session|signature-expired|session-expired FRESH_REMOTE_BASENAME [ABSOLUTE_EVIDENCE_JSON]' >&2; exit 2; }
mode=$1
base=$2
output=${3:-}
case "$mode" in valid-session|signature-expired|session-expired) ;; *) echo 'invalid recovery mode' >&2; exit 2;; esac
case "$base" in ''|*/*|*\\*|.|..) echo 'remote directory must be one fresh basename' >&2; exit 2;; esac

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
if [ -n "$output" ]; then
  case "$output" in "$root"/evidence/*.json) ;; *) echo 'evidence output must be an absolute JSON path below evidence' >&2; exit 2;; esac
  [ ! -e "$output" ] || { echo "refusing to overwrite evidence: $output" >&2; exit 1; }
  [ -d "$(dirname -- "$output")" ] || { echo 'evidence output directory does not exist' >&2; exit 1; }
fi

origin=http://127.0.0.1:8687
remote="codex-api-lab/$base"
state_name="multipart-$base"
state_container="/state/$state_name"
state_host="$root/.state/$state_name"
ownership_container="/state/owners-$state_name"
work=$(mktemp -d /private/tmp/tbox-multipart-recovery.XXXXXX)
live_snapshot="$work/live-service.json"

compose() {
  TBOX_LAB_REMOTE="sjtu:$remote" TBOX_STATE_DIR="$state_container" TBOX_OWNERSHIP_DIR="$ownership_container" \
  TBOX_FAULT_HOSTS='pan.sjtu.edu.cn:443,s3pan.jcloud.sjtu.edu.cn:443,s3pan1.jcloud.sjtu.edu.cn:443,s3pan2.jcloud.sjtu.edu.cn:443,s3pan3.jcloud.sjtu.edu.cn:443,s3pan4.jcloud.sjtu.edu.cn:443' \
  docker compose -f "$root/docker-compose.yml" -f "$root/docker-compose.doh.yml" "$@"
}
restore() {
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

case "$mode" in
  valid-session)
    rules=$(jq -cn '[{id:"st007-part-loss-s3pan",host:"s3pan.jcloud.sjtu.edu.cn:443",method:"PUT",path_prefix:"/",nth:2,action:"drop_response"}] + [range(1;5) | {id:("st007-part-loss-s3pan"+(tostring)),host:("s3pan"+(tostring)+".jcloud.sjtu.edu.cn:443"),method:"PUT",path_prefix:"/",nth:2,action:"drop_response"}] + [
      {id:"st007-renew",host:"pan.sjtu.edu.cn:443",method:"POST",path_prefix:"/api/v1/file/",action_key:"renew",nth:1,action:"observe"}
    ]')
    ;;
  signature-expired)
    rules=$(jq -cn '[{id:"st007-signature-expired-s3pan",host:"s3pan.jcloud.sjtu.edu.cn:443",method:"PUT",path_prefix:"/",nth:2,action:"respond_status",status:403}] + [range(1;5) | {id:("st007-signature-expired-s3pan"+(tostring)),host:("s3pan"+(tostring)+".jcloud.sjtu.edu.cn:443"),method:"PUT",path_prefix:"/",nth:2,action:"respond_status",status:403}] + [
      {id:"st007-renew",host:"pan.sjtu.edu.cn:443",method:"POST",path_prefix:"/api/v1/file/",action_key:"renew",nth:1,action:"observe"}
    ]')
    ;;
  session-expired)
    rules=$(jq -cn '[{id:"st007-part-loss-s3pan",host:"s3pan.jcloud.sjtu.edu.cn:443",method:"PUT",path_prefix:"/",nth:2,action:"drop_response"}] + [range(1;5) | {id:("st007-part-loss-s3pan"+(tostring)),host:("s3pan"+(tostring)+".jcloud.sjtu.edu.cn:443"),method:"PUT",path_prefix:"/",nth:2,action:"drop_response"}] + [
      {id:"st007-session-expired",host:"pan.sjtu.edu.cn:443",method:"GET",path_prefix:"/api/v1/file/",action_key:"upload",nth:1,action:"respond_status",status:404},
      {id:"st007-reinitialize",host:"pan.sjtu.edu.cn:443",method:"POST",path_prefix:"/api/v1/file/",action_key:"multipart",nth:2,action:"observe"}
    ]')
    ;;
esac
configured=$(compose exec -T fault-proxy curl -sS -o /dev/null -w '%{http_code}' -X PUT -H 'Content-Type: application/json' --data-binary "$rules" http://127.0.0.1:8788/rules)
[ "$configured" = 204 ] || { echo "fault rules rejected: $configured" >&2; exit 1; }

put_status=$(curl -sS --max-time 180 -o "$work/put.body" -w '%{http_code}' -X PUT --data-binary "@$work/fixture.bin" "$origin/recovery.bin" || true)
record=$(find "$state_host" -maxdepth 1 -type f -name '*.json' -print | head -n 1)
if [ -z "$record" ]; then
  echo "durable operation record not found; WebDAV PUT returned $put_status" >&2
  sed -n '1,20p' "$work/put.body" >&2
  compose logs --tail=80 webdav-faults fault-proxy >&2 || true
  compose exec -T fault-proxy curl -sS http://127.0.0.1:8788/events | jq '[.[] | {sequence,phase,rule,host,method,status}]' >&2 || true
  exit 1
fi
op=$(jq -r '.id' "$record")
spool="$state_host/$op.data"
[ -f "$spool" ] || { echo 'durable spool not found' >&2; exit 1; }
state=$(jq -r '.state' "$record")
spool_sha=$(shasum -a 256 "$spool" | awk '{print $1}')

compose run --rm --no-deps fault-client go run ./cmd/finder-inspect -space-file /secrets/space.json -token-file /secrets/user-token "$remote" > "$work/cloud.json"
cloud_sha=$(jq -r '.files["recovery.bin"].sha256 // empty' "$work/cloud.json")
cloud_size=$(jq -r '.files["recovery.bin"].size // -1' "$work/cloud.json")
events=$(compose exec -T fault-proxy curl -sS http://127.0.0.1:8788/events)
data_puts=$(printf '%s' "$events" | jq '[.[] | select(.phase == "received" and (.host | test("^s3pan[1-4]?\\.jcloud\\.sjtu\\.edu\\.cn:443$")) and .method == "PUT")] | length')
renew_events=$(printf '%s' "$events" | jq '[.[] | select(.rule == "st007-renew" and .phase == "received")] | length')
reinitialize_events=$(printf '%s' "$events" | jq '[.[] | select(.rule == "st007-reinitialize" and .phase == "received")] | length')
fault_events=$(printf '%s' "$events" | jq '[.[] | select((.rule // "") != "" and .rule != "st007-renew" and .rule != "st007-reinitialize")] | length')

pass=false
if [ "$put_status" = 201 ] && [ "$state" = Committed ] && [ "$spool_sha" = "$fixture_sha" ] && [ "$cloud_sha" = "$fixture_sha" ] && [ "$cloud_size" = "$fixture_size" ]; then
  case "$mode" in
    valid-session) [ "$data_puts" -ge 3 ] && [ "$data_puts" -lt 6 ] && [ "$renew_events" -le 1 ] && [ "$fault_events" -ge 1 ] && pass=true;;
    signature-expired) [ "$data_puts" -ge 4 ] && [ "$data_puts" -lt 6 ] && [ "$renew_events" -eq 1 ] && [ "$fault_events" -ge 1 ] && pass=true;;
    session-expired) [ "$data_puts" -eq 6 ] && [ "$reinitialize_events" -eq 1 ] && [ "$fault_events" -ge 2 ] && pass=true;;
  esac
fi

jq -n --arg at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg mode "$mode" --arg remote "$remote" \
  --arg scope 'real cloud protocol/external-event supplement; not Finder UI' --arg put_status "$put_status" --arg state "$state" \
  --arg fixture_sha256 "$fixture_sha" --arg spool_sha256 "$spool_sha" --arg cloud_sha256 "$cloud_sha" \
  --argjson bytes "$fixture_size" --argjson cloud_bytes "$cloud_size" --argjson data_puts "$data_puts" \
  --argjson renew_events "$renew_events" --argjson reinitialize_events "$reinitialize_events" \
  --argjson events "$events" --argjson pass "$pass" \
  '{at:$at,mode:$mode,scope:$scope,remote:$remote,put_status:$put_status,journal_state:$state,bytes:$bytes,fixture_sha256:$fixture_sha256,spool_sha256:$spool_sha256,independent_cloud_read:{bytes:$cloud_bytes,sha256:$cloud_sha256},data_plane_put_count:$data_puts,renew_observed:$renew_events,reinitialize_observed:$reinitialize_events,fault_events:[$events[] | select((.rule // "") != "")],pass:$pass}' > "$work/report.json"
if [ -n "$output" ]; then install -m 644 "$work/report.json" "$output"; fi
cat "$work/report.json"
[ "$pass" = true ]
