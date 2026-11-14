#!/usr/bin/env python3
"""Check stable Finder list fixtures against mounted and independent cloud sets."""
import argparse
import json
from pathlib import Path
import subprocess

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('mount', type=Path)
p.add_argument('fixture')
p.add_argument('cloud', type=Path)
p.add_argument('output', type=Path)
a = p.parse_args()
mount = a.mount.resolve()
if not any(line.startswith(f'http://127.0.0.1:8686/ on {mount} (webdav,')
           for line in subprocess.check_output(['/sbin/mount'], text=True).splitlines()):
    p.error('expected WebDAV mount is not active')
if Path(a.fixture).is_absolute() or '..' in Path(a.fixture).parts:
    p.error('fixture must be relative to the mount')
if a.output.exists():
    p.error('refusing to replace evidence')
cloud = json.loads(a.cloud.read_text())
results = []
for count in (0, 1, 50, 51, 1000):
    parent = f'list-{count:04d}'
    expected = [f'dir-{n:04d}' for n in range(count)]
    remote = sorted(name[len(parent)+1:] for name in cloud['directories']
                    if name.startswith(parent+'/'))
    mounted = sorted(entry.name for entry in (mount/a.fixture/parent).iterdir()
                     if entry.name not in ('.DS_Store', '._.DS_Store'))
    results.append({'case': f'list_{count}', 'expected': expected,
                    'mounted': mounted, 'cloud': remote,
                    'pass': mounted == remote == expected})
a.output.write_text(json.dumps({'cases': results,
    'pass': all(row['pass'] for row in results)}, indent=2)+'\n')
print(json.dumps({row['case']: row['pass'] for row in results}))
raise SystemExit(0 if all(row['pass'] for row in results) else 1)
