"""Read-only preflight for a fresh Finder copy through the local WebDAV guard.

Does not delete pending receipts or certify that a subsequent UI copy succeeds.
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
parser.add_argument('--state-dir', type=Path, default=Path(__file__).resolve().parents[1] / '.state/webdav-guard')
parser.add_argument('--reproduce-pending', action='store_true', help='Allow an unresolved receipt only for recorded failure reproduction, never fresh acceptance')
args = parser.parse_args()
warnings = []
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
if not item.is_file() or not os.access(item, os.R_OK):
    errors.append('Source file is not readable')
if (target / args.filename).exists():
    errors.append('Destination already exists; this is not a fresh-copy baseline')
mounts = subprocess.run(['/sbin/mount'], capture_output=True, text=True, check=True).stdout
if not any(f' on {mount} (webdav,' in line for line in mounts.splitlines()):
    errors.append('Expected mount is not mounted as WebDAV')
try:
    remote = '/' + (target / args.filename).relative_to(mount).as_posix()
except ValueError:
    errors.append('Target is outside the specified mount')
    remote = None
if not args.state_dir.is_dir():
    errors.append('Guard receipt directory is unavailable; cannot check pending state')
elif remote is not None:
    receipt = args.state_dir / (remote.encode().hex() + '.json')
    if receipt.exists():
        # Receipt existence is significant even when lock counters are zero.
        message = 'Unresolved guard receipt: ' + str(receipt)
        (warnings if args.reproduce_pending else errors).append(message)
print(json.dumps({'status': 'BLOCKED' if errors else ('READY_FOR_FAILURE_REPRODUCTION' if args.reproduce_pending else 'READY_FOR_RECORDED_UI_TEST'),
                  'source': str(item), 'target': str(target / args.filename),
                  'remote_path': remote, 'errors': errors, 'warnings': warnings,
                  'fresh_acceptance_eligible': not args.reproduce_pending,
                  'ui_success': 'NOT_TESTED'}, ensure_ascii=False, indent=2))
sys.exit(2 if errors else 0)
