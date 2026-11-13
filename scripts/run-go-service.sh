#!/bin/sh
# Replace the build wrapper with the service so SIGTERM reaches its handler.
set -eu
service=$1
shift
case "$service" in
  faultproxy|tboxrclone) ;;
  *) echo 'unsupported service' >&2; exit 2 ;;
esac
go build -o "/tmp/tbox-service-$service" "./cmd/$service"
exec "/tmp/tbox-service-$service" "$@"
