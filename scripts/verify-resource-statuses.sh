#!/bin/sh
# Inject sanitized cloud control-plane statuses and verify their WebDAV mapping.
# The fault stack must already be running with docker-compose.doh.yml.
set -eu

[ "$#" -eq 1 ] || { echo 'usage: verify-resource-statuses.sh FRESH_WEBDAV_DIRECTORY' >&2; exit 2; }
base=$1
case "$base" in
  ''|*/*|*\\*|.|..) echo 'fixture directory must be one fresh basename' >&2; exit 2 ;;
esac

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
origin=${TBOX_FAULT_WEBDAV_ORIGIN:-http://127.0.0.1:8687}
[ "$origin" = http://127.0.0.1:8687 ] || { echo 'only the loopback fault WebDAV origin is allowed' >&2; exit 2; }
work=$(mktemp -d /private/tmp/tbox-resource-statuses.XXXXXX)
trap 'rm -rf "$work"' EXIT HUP INT TERM

compose() {
  docker compose -f "$root/docker-compose.yml" -f "$root/docker-compose.doh.yml" "$@"
}
control() {
  compose exec -T fault-proxy curl -sS "$@" http://127.0.0.1:8788/rules
}

health=$(curl -sS --max-time 5 -o /dev/null -w '%{http_code}' -X OPTIONS "$origin/")
[ "$health" = 200 ] || { echo "fault WebDAV is not healthy: $health" >&2; exit 1; }
created=$(curl -sS --max-time 30 -o /dev/null -w '%{http_code}' -X MKCOL "$origin/$base")
[ "$created" = 201 ] || { echo "fixture directory is not fresh: MKCOL returned $created" >&2; exit 1; }

run_stamp=$(date -u +%Y%m%d%H%M%S)
for status in 403 429 503 401; do
  case "$status" in 401) expected=403;; *) expected=$status;; esac
	id="st015-$run_stamp-$status"
	attempt=1
	hit=false
	while [ "$attempt" -le 3 ]; do
	  before=$(compose exec -T fault-proxy curl -sS http://127.0.0.1:8788/events | jq '[.[].sequence] | max // 0')
	  rule=$(jq -cn --arg id "$id" --argjson status "$status" '[{id:$id,host:"pan.sjtu.edu.cn:443",method:"POST",path_prefix:"/api/v1/file/",nth:1,action:"respond_status",status:$status}]')
	  configured=$(control -o /dev/null -w '%{http_code}' -X PUT -H 'Content-Type: application/json' --data-binary "$rule")
	  [ "$configured" = 204 ] || { echo "rule $id was rejected: $configured" >&2; exit 1; }
	  path="$base/status-$status-attempt-$attempt.bin"
	  if ! metrics=$(curl -sS --max-time 60 -o "$work/$status.body" -w '%{http_code} %{time_total}' -X PUT --data-binary "status-$status-fixture" "$origin/$path"); then
	    metrics='000 60'
	    : > "$work/$status.body"
	  fi
	  events=$(compose exec -T fault-proxy curl -sS http://127.0.0.1:8788/events)
	  if [ "$(printf '%s' "$events" | jq --arg id "$id" --argjson before "$before" '[.[] | select(.sequence > $before and .rule == $id and .phase == "synthetic_status")] | length')" -eq 1 ]; then
	    hit=true
	    break
	  fi
	  attempt=$((attempt + 1))
	done
	actual=${metrics%% *}
	seconds=${metrics#* }
	head=$(curl -sS --max-time 30 -o /dev/null -w '%{http_code}' -I "$origin/$path")
  body=$(tr -d '\r\n' < "$work/$status.body" | cut -c1-80)
	[ "$hit" = true ] && [ "$actual" = "$expected" ] && [ "$head" = 404 ] && pass=true || pass=false
  jq -cn --arg id "$id" --argjson injected "$status" --argjson expected "$expected" \
    --argjson actual "$actual" --argjson head "$head" --argjson seconds "$seconds" \
	  --arg body "$body" --argjson injection_observed "$hit" --argjson attempts "$attempt" --argjson pass "$pass" \
	  '{id:$id,injected_status:$injected,injection_observed:$injection_observed,attempts:$attempts,expected_webdav_status:$expected,actual_webdav_status:$actual,elapsed_seconds:$seconds,final_head_status:$head,response_body:$body,pass:$pass}' \
    > "$work/$status.json"
done

events=$(compose exec -T fault-proxy curl -sS http://127.0.0.1:8788/events)
jq -s --arg at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg base "$base" --arg run_prefix "st015-$run_stamp-" --argjson events "$events" '
  {
    at:$at,
    base:$base,
    method:"live loopback WebDAV writes with one-shot HTTPS fault-proxy responses before the cloud origin",
    cases:.,
	  synthetic_events:[$events[] | select(.phase == "synthetic_status" and (.rule | startswith($run_prefix)))],
	  pass:all(.[]; .pass)
	}' "$work/401.json" "$work/403.json" "$work/429.json" "$work/503.json" > "$work/report.json"
cat "$work/report.json"
jq -e '.pass == true' "$work/report.json" >/dev/null
