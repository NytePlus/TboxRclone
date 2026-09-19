#!/bin/sh
set -eu

if [ "$#" -ne 1 ] || [ ! -d "$1" ]; then
  echo "usage: $0 FIXTURE_DIRECTORY" >&2
  exit 2
fi

fixture=$1
uid=$(id -u)
gid=$(id -g)

# Finder should never need privilege escalation for a disposable local fixture.
# This also repairs fixtures copied out of a root-owned Docker container.
chown -R "$uid:$gid" "$fixture"
chmod u+rwx "$fixture"
find "$fixture" -type f -exec chmod u+rw {} +

owner=$(stat -f '%Su:%Sg' "$fixture")
expected=$(id -un):$(id -gn)
if [ "$owner" != "$expected" ]; then
  echo "fixture ownership mismatch: got $owner, expected $expected" >&2
  exit 1
fi

printf 'fixture_ready=%s owner=%s\n' "$fixture" "$owner"
