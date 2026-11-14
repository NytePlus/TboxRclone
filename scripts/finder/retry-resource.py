#!/usr/bin/env python3
"""Retry a retained Finder fixture after fault cleanup and dialog review."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[2]
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('evidence', type=Path)
p.add_argument('--seconds', type=int, default=90)
p.add_argument('--output-name', default='recovery', help='fresh child directory for repeated verification after a fix')
p.add_argument('--record-only', action='store_true', help='record a retry expected to remain blocked; never claims content verification')
p.add_argument('--expected-error-text', help='stop recording after this exact native error text and fixture filename are observed')
a = p.parse_args()
if not 45 <= a.seconds <= 180:
    p.error('recording must be 45..180 seconds')
evidence = a.evidence.resolve()
meta = json.loads((evidence / 'run.json').read_text())
source = Path(meta['source_directory'])
data = (source / meta['filename']).read_bytes()
if {'size': len(data), 'sha256': hashlib.sha256(data).hexdigest()} != meta['source_before']:
    p.error('source changed since fault injection')
remote = meta['remote_root']
if not remote.startswith('codex-api-lab/'):
    p.error('isolated remote root required')
container = json.loads(subprocess.check_output(['docker', 'inspect', 'tboxrclone-webdav-faults-1'], text=True))[0]
env = dict(v.split('=', 1) for v in container['Config']['Env'])
if (not container['State']['Running'] or 'sjtu:' + remote.rsplit('/', 1)[0] not in container['Config']['Cmd']
        or env.get('RCLONE_SJTU_LAB_DELETE') != 'false' or env.get('RCLONE_SJTU_LAB_MOVE') != 'false'):
    p.error('live service no longer matches the safe fixture root')
mount = Path('/private/tmp/tboxrclone-webdav-501')
subtree = evidence.name
if not remote.endswith('/' + subtree):
    p.error('fixture remote does not match its evidence identity')
if Path(a.output_name).name != a.output_name or a.output_name in ('', '.', '..'):
    p.error('--output-name must be a child directory name')
out = evidence / a.output_name
out.mkdir(exist_ok=False)
meta['mount_subtree'] = subtree
(out / 'run.json').write_text(json.dumps(meta, indent=2) + '\n')
result = subprocess.run(['sh', str(ROOT / 'scripts/finder-drag.sh'), '--record',
    str(source), str(mount / subtree), meta['filename'], str(mount), str(out / 'finder')],
    env={**os.environ, 'TBOX_FINDER_RECORD_SECONDS': str(a.seconds),
         **({'TBOX_FINDER_STOP_ERROR_TEXT': a.expected_error_text} if a.expected_error_text else {})},
    timeout=a.seconds + 100)
if result.returncode != 3:
    raise SystemExit('retry recording failed; evidence retained')
if a.record_only:
    print('Retry recorded; inspect UI and independently verify retained state. No content success claimed.')
    raise SystemExit(0)
subprocess.run(['python3', str(ROOT / 'scripts/finder/finish-part-evidence.py'), str(out)], check=True)
print('Recovery content verified; UI completion requires separate review.')
