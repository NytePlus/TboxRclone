#!/usr/bin/env python3
"""Snapshot pending journal/payload bytes, or verify them after a service restart."""
import argparse
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('session', type=Path)
p.add_argument('output', type=Path)
p.add_argument('--baseline', type=Path)
a = p.parse_args()
if a.output.exists():
    p.error('output must be fresh')
plan = json.loads((a.session / 'fault-session.json').read_text())
state = ROOT / '.state' / Path(plan['environment']['TBOX_STATE_DIR']).name

def identity(path):
    h = hashlib.sha256()
    with path.open('rb') as stream:
        while block := stream.read(1024 * 1024):
            h.update(block)
    return {'bytes': path.stat().st_size, 'sha256': h.hexdigest()}

if a.baseline:
    baseline = json.loads(a.baseline.read_text())
    if baseline['state_directory'] != str(state):
        p.error('baseline belongs to a different state directory')
    checks = []
    for name, expected in baseline['files'].items():
        if Path(name).name != name:
            p.error('invalid baseline filename')
        path = state / name
        actual = identity(path) if path.is_file() else None
        checks.append({'file': name, 'matched': actual == expected})
    result = {'checks': checks, 'pass': all(row['matched'] for row in checks)}
else:
    files = {}
    records = []
    for path in sorted(state.glob('*.json')):
        record = json.loads(path.read_text())
        if record.get('state') in ['Committed', 'Rejected', 'Aborted']:
            continue
        records.append({'id': record['id'], 'state': record['state']})
        for candidate in state.glob(path.stem + '.*'):
            if candidate.is_file():
                files[candidate.name] = identity(candidate)
    result = {'state_directory': str(state), 'records': records, 'files': files}
a.output.write_text(json.dumps(result, indent=2) + '\n')
print(json.dumps({'records': result.get('records'), 'pass': result.get('pass')}))
if result.get('pass') is False:
    raise SystemExit('pending state changed; inspect before continuing')
