#!/usr/bin/env python3
"""Read-only deletion receipt check; never sends DELETE or recreates a file.

Run after finish-upload.py and verify-absence.py. Their independent cloud,
mounted sentinel, and final Finder evidence remain separate requirements.
"""
import argparse
import json
from pathlib import Path
import subprocess
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('session', type=Path)
p.add_argument('relative_path')
p.add_argument('output', type=Path)
a = p.parse_args()
rel = Path(a.relative_path)
if rel.is_absolute() or '..' in rel.parts or len(rel.parts) < 2:
    p.error('requires a file inside an isolated fixture directory')
if a.output.exists():
    p.error('output must be fresh')
plan = json.loads((a.session/'fault-session.json').read_text())
env = plan['environment']
remote = env['TBOX_LAB_REMOTE']
if not remote.startswith('sjtu:codex-api-lab/ops-') or plan['transport'] != 'direct':
    p.error('requires direct isolated ops session')
live = json.loads(subprocess.check_output(['docker','inspect','tboxrclone-webdav-1'], timeout=15))[0]
actual = dict(item.split('=',1) for item in live['Config']['Env'])
if not live['State']['Running'] or remote not in live['Config']['Cmd'] or actual.get('RCLONE_CONFIG_SJTU_STATE_DIR') != env['TBOX_STATE_DIR']:
    p.error('live service differs from session')
state = ROOT/'.state'/Path(env['TBOX_STATE_DIR']).name
path = remote.removeprefix('sjtu:')+'/'+rel.as_posix()
records, pending = [], []
for file in sorted(state.glob('*.json')):
    record = json.loads(file.read_text())
    if record.get('path') != path:
        continue
    safe = {key:record.get(key) for key in ['id','kind','path','state','recycled_item_id','old_size']}
    records.append(safe)
    if record.get('state') not in ['Committed','Rejected','Aborted']:
        pending.append(safe['id'])
deletes = [record for record in records if record['kind'] == 'delete']
req = urllib.request.Request('http://127.0.0.1:8686/'+urllib.parse.quote(rel.as_posix()), method='HEAD')
try:
    response = urllib.request.urlopen(req, timeout=30)
except urllib.error.HTTPError as exc:
    response = exc
with response:
    status = response.status
checks = {
    'one_delete_receipt':len(deletes) == 1,
    'delete_committed':len(deletes) == 1 and deletes[0]['state'] == 'Committed',
    'recycle_identifier_recorded':len(deletes) == 1 and bool(deletes[0]['recycled_item_id']),
    'no_pending_exact_path':not pending,
    'webdav_reports_absent_not_busy':status == 404,
}
report = dict(path=path, records=records, checks=checks, head_status=status,
    passed=all(checks.values()),
    limits='Recycle ID is traceability evidence, not a restore test. HEAD and terminal journal state do not test a subsequent write. Independent cloud/mount absence and final UI review required separately.')
a.output.write_text(json.dumps(report, indent=2)+'\n')
print(json.dumps(report, indent=2))
raise SystemExit(0 if report['passed'] else 1)
