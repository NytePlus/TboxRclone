#!/usr/bin/env python3
"""Compare fixture, reopened mount and independent cloud inventory; no UI verdict."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('source', type=Path)
parser.add_argument('mount', type=Path)
parser.add_argument('cloud', type=Path)
parser.add_argument('output', type=Path)
parser.add_argument('--tree', action='append', default=[], help='also verify every fixture file and directory under this relative tree')
parser.add_argument('--cloud-subtree', default='', help='mount-relative directory inventoried as the cloud root')
parser.add_argument('--mount-subtree', default='', help='mount-relative target directory corresponding to the source/cloud root')
parser.add_argument('--cloud-name-map', type=Path, help='explicit logical-to-raw relative cloud filename mapping JSON')
parser.add_argument('files', nargs='*')
args = parser.parse_args()
mount = args.mount.resolve()
table = subprocess.check_output(['/sbin/mount'], text=True)
expected = f'http://127.0.0.1:8686/ on {mount} (webdav,'
if not any(line.startswith(expected) for line in table.splitlines()):
    parser.error('expected loopback WebDAV mount is not active')
if args.output.exists():
    parser.error('refusing to overwrite evidence')
cloud = json.loads(args.cloud.read_text())
target_root = mount
if args.mount_subtree:
    rel = Path(args.mount_subtree)
    if args.cloud_subtree or rel.is_absolute() or '..' in rel.parts or not cloud['root'].endswith('/' + rel.as_posix()):
        parser.error('mount subtree must match the cloud root and cannot combine with cloud subtree')
    target_root = mount / rel
subtree = args.cloud_subtree.strip('/')
if subtree and ( '..' in Path(subtree).parts or not cloud['root'].endswith('/' + subtree)):
    parser.error('cloud subtree must match the independently inventoried root')
name_map = json.loads(args.cloud_name_map.read_text()) if args.cloud_name_map else {}
if not isinstance(name_map, dict) or len(set(name_map.values())) != len(name_map):
    parser.error('cloud name mapping must be one-to-one')
if any(raw not in cloud['files'] for raw in name_map.values()):
    parser.error('mapped raw cloud filename is absent from inventory')

def cloud_name(name):
    relative = Path(name).relative_to(subtree).as_posix() if subtree else name
    return name_map.get(relative, relative)

def identity(path):
    h = hashlib.sha256()
    size = 0
    with path.open('rb') as stream:
        while block := stream.read(1024 * 1024):
            size += len(block)
            h.update(block)
    return {'size': size, 'sha256': h.hexdigest()}

results = []
if args.tree and cloud.get('selected_file'):
    parser.error('tree verification requires a directory inventory, not a targeted file read')
names = list(args.files)
directories = []
for tree in args.tree:
    rel = Path(tree)
    if rel.is_absolute() or '..' in rel.parts or not (args.source / rel).is_dir():
        parser.error('tree must name a directory inside the fixture')
    entries = [args.source / rel, *(args.source / rel).rglob('*')]
    for entry in entries:
        if entry.is_symlink():
            parser.error('fixture trees must not contain symlinks')
        name = entry.relative_to(args.source).as_posix()
        if entry.is_dir():
            directories.append({'directory': name,
                                'matched': (target_root / name).is_dir() and
                                (name == subtree or cloud_name(name) in cloud['directories'])})
        elif entry.is_file():
            names.append(name)
if not names and not directories:
    parser.error('specify files or --tree')
for name in sorted(set(names)):
    rel = Path(name)
    if rel.is_absolute() or '..' in rel.parts:
        parser.error('file paths must stay inside fixture and mount')
    source = identity(args.source / rel)
    mounted = identity(target_root / rel)
    raw_name = cloud_name(rel.as_posix())
    remote = cloud['files'].get(raw_name)
    results.append({'file': name, 'source': source, 'mounted': mounted,
                    'raw_cloud_name': raw_name, 'cloud': remote, 'matched': source == mounted == remote})
report = {'method': 'source + verified mount reopen + independent cloud full reads',
          'cloud_method': cloud.get('method'),
          'ui_verdict': 'REQUIRES_SEPARATE_REVIEW', 'files': results, 'directories': directories}
args.output.write_text(json.dumps(report, indent=2) + '\n')
print(json.dumps(report, indent=2))
raise SystemExit(0 if all(item['matched'] for item in results + directories) else 1)
