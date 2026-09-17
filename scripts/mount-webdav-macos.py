#!/usr/bin/env python3
"""Mount the loopback service outside Docker bind mounts; verify before use."""
import os
from pathlib import Path
import subprocess
import sys

repo = Path(__file__).resolve().parent.parent
mountpoint = Path(os.environ.get('TBOX_WEBDAV_MOUNT', f'/private/tmp/tboxrclone-webdav-{os.getuid()}')).resolve()
url = 'http://127.0.0.1:8686/'
action = sys.argv[1] if len(sys.argv) == 2 else ''
if action not in ('mount', 'check', 'unmount'):
    sys.exit('usage: python3 scripts/mount-webdav-macos.py mount|check|unmount')
# /src and /state bind mounts must never contain a client mount of the service.
if mountpoint == repo or repo in mountpoint.parents or mountpoint in repo.parents:
    sys.exit('refusing mountpoint overlapping the Docker-shared project directory')
if sys.platform != 'darwin':
    sys.exit('this helper requires macOS')

def mounted():
    table = subprocess.check_output(['/sbin/mount'], text=True)
    expected = f'{url} on {mountpoint} (webdav,'
    if any(line.startswith(expected) for line in table.splitlines()):
        return True
    if any(f' on {mountpoint} (' in line for line in table.splitlines()):
        sys.exit('mountpoint is occupied by a different filesystem or URL')
    return False

present = mounted()
if action == 'mount' and not present:
    mountpoint.mkdir(mode=0o700, parents=True, exist_ok=True)
    if mountpoint.stat().st_uid != os.getuid() or any(mountpoint.iterdir()):
        sys.exit('mountpoint must be owned by the current user and empty')
    subprocess.run(['/sbin/mount_webdav', '-S', url, str(mountpoint)], check=True)
    present = mounted()
elif action == 'unmount':
    if present:
        subprocess.run(['/sbin/umount', str(mountpoint)], check=True)
    print(f'unmounted: {mountpoint}; local directory retained')
    sys.exit(0)
if not present:
    sys.exit(f'not mounted: {mountpoint}')
print(f'verified WebDAV mount: {mountpoint}')
