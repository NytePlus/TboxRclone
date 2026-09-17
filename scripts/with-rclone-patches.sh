#!/bin/sh
# Runtime volumes can remain read-only; preparation is an explicit setup step.
set -eu
project_dir=$(cd "$(dirname "$0")/.." && pwd)
sh "$project_dir/scripts/rclone-patches.sh" --check
exec "$@"
