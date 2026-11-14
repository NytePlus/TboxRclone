#!/usr/bin/env python3
"""Read upload receipts without copying credentials or signed upload URLs."""
import argparse
import hashlib
import json
from pathlib import Path

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('state', type=Path)
p.add_argument('remote_path', help='exact cloud path from the isolated test root')
p.add_argument('output', type=Path)
p.add_argument('--subtree', action='store_true', help='include records strictly inside the named directory')
a = p.parse_args()
if a.output.exists() or not a.remote_path.startswith('codex-api-lab/'):
    p.error('new evidence output and isolated remote path required')
records = []
for path in sorted(a.state.glob('*.json')):
    record = json.loads(path.read_text())
    if record.get('path') != a.remote_path and not (a.subtree and record.get('path','').startswith(a.remote_path.rstrip('/')+'/')):
        continue
    clean = {key: record.get(key) for key in ['id', 'state', 'path', 'size', 'sha256', 'part_size', 'parts']}
    # A digest is enough to compare session continuity across observations.
    clean['upload_id_sha256'] = hashlib.sha256(record.get('upload_id', '').encode()).hexdigest()
    spool = a.state / (record['id'] + '.data')
    if spool.exists():
        with spool.open('rb') as stream:
            clean['spool_sha256'] = hashlib.file_digest(stream, 'sha256').hexdigest()
        clean['spool_size'] = spool.stat().st_size
    records.append(clean)
a.output.write_text(json.dumps({'records': records}, indent=2) + '\n')
print(json.dumps([{'size': r['size'], 'state': r['state'], 'parts': len(r['parts'] or [])} for r in records]))
