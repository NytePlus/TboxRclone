#!/usr/bin/env python3
"""Mount the native loopback WebDAV service outside the project directory."""
import os
from pathlib import Path
import subprocess
import sys
from urllib.parse import urlsplit

repo = Path(__file__).resolve().parent.parent
mountpoint = Path(os.environ.get('TBOX_WEBDAV_MOUNT', f'/private/tmp/tboxrclone-webdav-{os.getuid()}')).resolve()
url = os.environ.get('TBOX_WEBDAV_URL', 'http://127.0.0.1:8686/')
parsed_url = urlsplit(url)
if parsed_url.scheme != 'http' or parsed_url.hostname not in ('127.0.0.1', '::1') or parsed_url.username or parsed_url.password or parsed_url.query or parsed_url.fragment:
    sys.exit('TBOX_WEBDAV_URL must be a plain loopback HTTP URL')
if not url.endswith('/'):
    url += '/'
action = sys.argv[1] if len(sys.argv) == 2 else ''
if action not in ('mount', 'check', 'unmount'):
    sys.exit('usage: python3 scripts/mount-webdav-macos.py mount|check|unmount')
# Keep the client mount outside the source and durable state trees.
if mountpoint == repo or repo in mountpoint.parents or mountpoint in repo.parents:
    sys.exit('refusing mountpoint overlapping the project directory')
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
        result = subprocess.run(['/sbin/umount', str(mountpoint)], capture_output=True, text=True)
        if result.returncode:
            # Finder can retain ordinary handles after a completed operation.
            # Use Disk Arbitration's normal unmount, never a forced detach.
            if 'Resource busy' not in result.stderr:
                sys.exit(result.stderr.strip() or 'unmount failed')
            subprocess.run(['/usr/sbin/diskutil', 'unmount', str(mountpoint)], check=True)
        if mounted():
            sys.exit('unmount returned success but WebDAV is still mounted')
    print(f'unmounted: {mountpoint}; local directory retained')
    sys.exit(0)
if not present:
    sys.exit(f'not mounted: {mountpoint}')
print(f'verified WebDAV mount: {mountpoint}')
