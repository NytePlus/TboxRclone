"""Read-only preflight for a Finder copy through the local WebDAV service.

Does not mutate remote state or certify that a subsequent UI copy succeeds.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys

parser = argparse.ArgumentParser()
parser.add_argument('source', type=Path)
parser.add_argument('target', type=Path)
parser.add_argument('filename')
parser.add_argument('mount', type=Path)
parser.add_argument('--allow-existing', action='store_true', help='Explicit sequential overwrite test')
args = parser.parse_args()
errors = []
source, target, mount = (p.resolve() for p in (args.source, args.target, args.mount))
if Path(args.filename).name != args.filename or args.filename in ('', '.', '..'):
    parser.error('filename must be a single basename')
if not source.is_dir() or not target.is_dir():
    errors.append('Source and target directories must exist')
try:
    # Resolve directory entries through the mounted filesystem, not just stat
    # its mountpoint. Keep names out of the diagnostic output.
    with os.scandir(target) as entries:
        for _ in entries:
            pass
except OSError as exc:
    errors.append('Target directory listing failed: ' + str(exc))
item = source / args.filename
if item.is_symlink() or not (item.is_file() or item.is_dir()) or not os.access(item, os.R_OK):
    errors.append('Source must be a readable regular file or directory, not a symlink')
if item.is_dir():
    for child in item.rglob('*'):
        if child.is_symlink() or not (child.is_file() or child.is_dir()) or not os.access(child, os.R_OK):
            errors.append('Source tree contains an unsupported or unreadable entry')
            break
if (target / args.filename).exists() and not args.allow_existing:
    errors.append('Destination already exists; this is not a fresh-copy baseline')
mounts = subprocess.run(['/sbin/mount'], capture_output=True, text=True, check=True).stdout
if not any(f' on {mount} (webdav,' in line for line in mounts.splitlines()):
    errors.append('Expected mount is not mounted as WebDAV')
try:
    remote = '/' + (target / args.filename).relative_to(mount).as_posix()
except ValueError:
    errors.append('Target is outside the specified mount')
    remote = None
print(json.dumps({'status': 'BLOCKED' if errors else 'READY_FOR_RECORDED_UI_TEST',
                  'source': str(item), 'target': str(target / args.filename),
                  'remote_path': remote, 'errors': errors, 'warnings': [],
                  'fresh_acceptance_eligible': not args.allow_existing,
                  'sequential_overwrite_test': args.allow_existing,
                  'ui_success': 'NOT_TESTED'}, ensure_ascii=False, indent=2))
sys.exit(2 if errors else 0)
