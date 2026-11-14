#!/usr/bin/env python3
"""Signal only the active test service after a held data part; retain all spools."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time

ROOT = Path(__file__).resolve().parents[2]
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('session', type=Path)
p.add_argument('evidence', type=Path)
p.add_argument('signal', choices=['SIGTERM', 'SIGKILL'])
a = p.parse_args()
plan = json.loads((a.session / 'fault-session.json').read_text())['environment']
meta = json.loads((a.evidence / 'run.json').read_text())
rules = {r['id'] for r in json.loads((a.evidence / 'rules.json').read_text())}
state = ROOT / '.state' / Path(plan['TBOX_STATE_DIR']).name
compose = ['docker', 'compose', '-f', str(ROOT / 'docker-compose.yml'), '-f', str(ROOT / 'docker-compose.doh.yml')]
env = {**os.environ, **plan}
container = 'tboxrclone-webdav-faults-1'
report = {'signal': a.signal, 'ui_verdict': 'REQUIRES_SEPARATE_REVIEW'}

def command(cmd, **kwargs):
    return subprocess.run(cmd, check=True, cwd=ROOT, text=True, **kwargs)

def save_report():
    (a.evidence / 'signal.json').write_text(json.dumps(report, indent=2) + '\n')

def snapshot(label, record):
    spool = state / (record['id'] + '.data')
    with spool.open('rb') as stream:
        sha = hashlib.file_digest(stream, 'sha256').hexdigest()
    clean = {k: record.get(k) for k in ['id', 'state', 'size', 'sha256', 'parts']}
    clean.update(unix_seconds=time.time(), spool_bytes=spool.stat().st_size, spool_sha256=sha)
    for key in ['upload_id', 'confirm_key']:
        clean[key + '_sha256'] = hashlib.sha256(record.get(key, '').encode()).hexdigest()
    report[label] = clean
    save_report()
    assert clean['spool_bytes'] == 9437184 and sha == record['sha256'], 'durable spool mismatch'

stopped = False
try:
    deadline = time.monotonic() + 200
    while time.monotonic() < deadline:
        raw = command(compose + ['exec', '-T', 'fault-proxy', 'curl', '--fail', '--silent',
            '--max-time', '5', 'http://127.0.0.1:8788/events'], capture_output=True)
        events = json.loads(raw.stdout)
        held = [e for e in events if e.get('rule') in rules and e['phase'] == 'held']
        if held:
            request = held[-1]['request']
            if any(e['request'] == request and e['phase'] in ['forwarded', 'dropped_after_release', 'hold_timeout', 'client_response_interrupted'] for e in events):
                raise RuntimeError('data response no longer held')
            break
        time.sleep(0.5)
    else:
        raise RuntimeError('no held data response before deadline')
    candidates = [(path, json.loads(path.read_text())) for path in state.glob('*.json')]
    candidates = [(path, r) for path, r in candidates if r.get('path') == meta['remote_root'] + '/' + meta['filename'] and r.get('size') == 9437184 and r.get('state') == 'Uploading']
    assert len(candidates) == 1, 'expected one durable uploading data operation'
    path, before = candidates[0]
    source = Path(meta['source_directory']) / meta['filename']
    with source.open('rb') as stream:
        assert hashlib.file_digest(stream, 'sha256').hexdigest() == before['sha256']
    live = json.loads(command(['docker', 'inspect', container], capture_output=True).stdout)[0]
    values = dict(x.split('=', 1) for x in live['Config']['Env'] if '=' in x)
    assert live['State']['Running'] and plan['TBOX_LAB_REMOTE'] in live['Config']['Cmd']
    assert values['RCLONE_CONFIG_SJTU_STATE_DIR'] == plan['TBOX_STATE_DIR']
    assert values['RCLONE_SJTU_LAB_DELETE'] == values['RCLONE_SJTU_LAB_MOVE'] == 'false'
    snapshot('before_signal', before)
    report['held_request'] = request
    report['signal_at'] = time.time()
    save_report()
    command(['docker', 'kill', '--signal', a.signal, container], capture_output=True)
    stopped = True
    report['exit_code'] = int(command(['docker', 'wait', container], capture_output=True, timeout=30).stdout.strip())
    assert report['exit_code'] in ([0, 143] if a.signal == 'SIGTERM' else [137])
    snapshot('after_signal', json.loads(path.read_text()))
    command(['python3', 'scripts/finder/fault-control.py', 'release', str(request), '--drop'])
    # Reuse the existing recovery CLI in a separate process with identical scope.
    with (a.evidence / 'resume.log').open('x') as log:
        command(compose + ['run', '--rm', '--no-deps', 'fault-client', 'sh', '-c', '''
lib=$(sed -n "s/^library_id *= *//p" /secrets/rclone.conf)
space=$(sed -n "s/^space_id *= *//p" /secrets/rclone.conf)
exec go run ./cmd/tbox-state --state-dir "$1" --ownership-dir "$2" --library-id "$lib" --space-id "$space" --token-file /secrets/access-token --user-token-file /secrets/user-token --resume "$3"
''', 'sh', plan['TBOX_STATE_DIR'], plan['TBOX_OWNERSHIP_DIR'], before['id']], env=env, stdout=log, stderr=log, timeout=90)
    after = json.loads(path.read_text())
    snapshot('after_resume', after)
    assert after['state'] == 'Committed'
    assert all(before[k] == after[k] for k in ['id', 'upload_id', 'confirm_key', 'sha256'])
    report['recovery_verified'] = True
except Exception as exc:
    report['error'] = str(exc)
    raise
finally:
    try:
        if stopped:
            command(['docker', 'start', container], capture_output=True)
            report['service_restarted_at'] = time.time()
    finally:
        save_report()
