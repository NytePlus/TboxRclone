#!/usr/bin/env python3
"""Verify rejected fixture names are absent from a live mount and full cloud inventory."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('mount', type=Path)
p.add_argument('fixture')
p.add_argument('cloud', type=Path)
p.add_argument('output', type=Path)
p.add_argument('--absent', action='append', required=True)
p.add_argument('--present', action='append', required=True)
a = p.parse_args()
mount = a.mount.resolve()
if a.output.exists():
    p.error('refusing to overwrite evidence')
for name in [a.fixture, *a.absent, *a.present]:
    if not name or Path(name).is_absolute() or '..' in Path(name).parts:
        p.error('all fixture paths must remain relative')
if set(a.absent) & set(a.present):
    p.error('present and absent names must be disjoint')
if not any(line.startswith(f'http://127.0.0.1:8686/ on {mount} (webdav,')
           for line in subprocess.check_output(['/sbin/mount'], text=True).splitlines()):
    p.error('verified loopback WebDAV mount required')
cloud = json.loads(a.cloud.read_text())
if cloud.get('selected_file') or not cloud['root'].endswith('/' + a.fixture):
    p.error('requires a full independent cloud inventory rooted at the fixture')
directory = mount / a.fixture
# Enumeration must succeed: an inaccessible directory is not an empty directory.
listed = sorted(entry.name for entry in directory.iterdir())
remote = set(cloud['files']) | set(cloud['directories'])
absent = []
for name in a.absent:
    mounted_absent = not (directory / name).exists()
    cloud_absent = not any(item == name or item.startswith(name + '/') for item in remote)
    absent.append({'name': name, 'mount_absent': mounted_absent,
                   'cloud_absent': cloud_absent, 'pass': mounted_absent and cloud_absent})
present = []
for name in a.present:
    digest = hashlib.sha256()
    size = 0
    with (directory / name).open('rb') as stream:
        while block := stream.read(1024 * 1024):
            digest.update(block)
            size += len(block)
    identity = {'size': size, 'sha256': digest.hexdigest()}
    present.append({'name': name, 'mounted': identity, 'cloud': cloud['files'].get(name),
                    'pass': identity == cloud['files'].get(name)})
report = {'scope': 'mount and independent cloud fact check; UI reviewed separately',
          'mounted_listing': listed, 'cloud_listing': sorted(remote),
          'absent': absent, 'present': present,
          'pass': all(row['pass'] for row in absent + present)}
a.output.write_text(json.dumps(report, indent=2) + '\n')
print(json.dumps(report, indent=2))
raise SystemExit(0 if report['pass'] else 1)
