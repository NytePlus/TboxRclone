#!/usr/bin/env python3
"""Record one real Finder multipart interruption; never assign a UI verdict."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import uuid
import hashlib
import time

ROOT = Path(__file__).resolve().parents[2]
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('session', type=Path, help='existing fault session evidence directory')
p.add_argument('evidence', type=Path, help='new case evidence directory')
p.add_argument('source', type=Path, help='existing deterministic 9 MiB fixture')
p.add_argument('--part', type=int, choices=[1, 2, 3], required=True)
p.add_argument('--mode', choices=['cut-upload', 'signature-expired', 'session-expired', 'replay-part', 'service-term', 'service-kill'], default='cut-upload')
p.add_argument('--seconds', type=int, default=90)
a = p.parse_args()
started = time.monotonic()
started_utc = time.time()
plan = json.loads((a.session / 'fault-session.json').read_text())['environment']
if plan['TBOX_LAB_DELETE'] != 'false' or plan['TBOX_LAB_MOVE'] != 'false':
    p.error('this runner requires deletion and move disabled')
# Verify the plan still describes the actual running target before a UI write.
container = json.loads(subprocess.check_output(
    ['docker', 'inspect', 'tboxrclone-webdav-faults-1'], text=True))[0]
command = container['Config']['Cmd']
environment = dict(item.split('=', 1) for item in container['Config']['Env'] if '=' in item)
if (not container['State']['Running'] or plan['TBOX_LAB_REMOTE'] not in command
        or environment.get('RCLONE_CONFIG_SJTU_STATE_DIR') != plan['TBOX_STATE_DIR']
        or environment.get('RCLONE_SJTU_LAB_DELETE') != 'false'
        or environment.get('RCLONE_SJTU_LAB_MOVE') != 'false'):
    p.error('live service no longer matches the supplied safe fault session')
if a.source.stat().st_size != 9 * 1024 * 1024 or not 45 <= a.seconds <= 180:
    p.error('requires 9 MiB deterministic fixture and 45..180 recording seconds')
evidence = a.evidence.resolve()
if ROOT / 'evidence' not in evidence.parents:
    p.error('evidence must be inside repository evidence directory')
evidence.mkdir(parents=True, exist_ok=False)
source_dir = Path('/private/tmp') / ('tbox-part-fault-' + evidence.name)
source_dir.mkdir()
name = f'cut-part-{a.part}-{evidence.name}.bin'
shutil.copyfile(a.source, source_dir / name)
mount = Path('/private/tmp/tboxrclone-webdav-501')
run_id = uuid.uuid4().hex
rules = [{'id': f'cut-part-{a.part}-{run_id}-{i}', 'host': host, 'method': 'PUT',
          'path_prefix': '/', 'part_number': a.part, 'nth': 1,
          'action': 'cut_upload', 'bytes': 65536, 'min_content_length': 1048576}
         for i, host in enumerate(plan['TBOX_FAULT_HOSTS'].split(','))
         if host.startswith('s3pan')]
if a.mode != 'cut-upload':
    for rule in rules:
        rule.pop('bytes')
        if a.mode in ['service-term', 'service-kill']:
            rule.update(action='hold_response')
        elif a.mode == 'replay-part':
            rule.update(action='replay_part')
        else:
            rule.update(action='respond_status', status=403)
if a.mode == 'session-expired':
    rules.append({'id': f'expired-session-{run_id}', 'host': 'pan.sjtu.edu.cn:443',
                  'method': 'GET', 'path_prefix': '/api/v1/file/', 'action_key': 'upload',
                  'nth': 1, 'action': 'respond_status', 'status': 404,
                  'after_any': [r['id'] for r in rules]})
(evidence / 'rules.json').write_text(json.dumps(rules, indent=2) + '\n')
meta = {'source_directory': str(source_dir), 'filename': name, 'part': a.part, 'mode': a.mode,
        'remote_root': plan['TBOX_LAB_REMOTE'].removeprefix('sjtu:'),
        'ui_verdict': 'REQUIRES_SEPARATE_REVIEW'}
(evidence / 'run.json').write_text(json.dumps(meta, indent=2) + '\n')

def timing(phase, begin, utc, code=0):
    with (evidence / 'phase-timings.jsonl').open('a') as stream:
        stream.write(json.dumps({'phase': phase, 'started_unix_seconds': utc,
            'elapsed_seconds': time.monotonic() - begin, 'exit_code': code}) + '\n')

timing('setup', started, started_utc)

def run(args, **kwargs):
    begin, utc = time.monotonic(), time.time()
    code = None
    try:
        result = subprocess.run(args, cwd=ROOT, **kwargs)
        code = result.returncode
        result.check_returncode()
        return result
    finally:
        timing(Path(args[1]).name, begin, utc, code)

run(['python3', 'scripts/finder/fault-control.py', 'rules', str(evidence / 'rules.json')])
state = ROOT / '.state' / Path(plan['TBOX_STATE_DIR']).name
controller = None
if a.mode in ['service-term', 'service-kill']:
    controller = subprocess.Popen(['python3', 'scripts/finder/signal-upload.py', str(a.session),
        str(evidence), 'SIGTERM' if a.mode == 'service-term' else 'SIGKILL'], cwd=ROOT)
capture_start, capture_utc = time.monotonic(), time.time()
result = subprocess.Popen(['sh', 'scripts/finder-drag.sh', '--record', str(source_dir),
                         str(mount), name, str(mount), str(evidence / 'finder')],
                        cwd=ROOT, env={**os.environ, 'TBOX_FINDER_RECORD_SECONDS': str(a.seconds)})
# Persist state transitions while Finder runs; never persist signed URLs or keys.
previous = None
deadline = time.monotonic() + a.seconds + 100
with (evidence / 'journal-timeline.jsonl').open('x') as timeline:
    while True:
        records = []
        for path in sorted(state.glob('*.json')):
            record = json.loads(path.read_text())
            if record.get('path') != meta['remote_root'] + '/' + name:
                continue
            clean = {key: record.get(key) for key in ['id', 'state', 'size', 'sha256', 'parts']}
            clean['upload_id_sha256'] = hashlib.sha256(record.get('upload_id', '').encode()).hexdigest()
            records.append(clean)
        if records != previous:
            timeline.write(json.dumps({'unix_seconds': time.time(), 'records': records}) + '\n')
            timeline.flush()
            previous = records
        try:
            result.wait(timeout=0.5)
            break
        except subprocess.TimeoutExpired:
            if time.monotonic() >= deadline:
                raise SystemExit('Finder exceeded recording bound; inspect live process before restarting')
timing('finder-recording', capture_start, capture_utc, result.returncode)
run(['python3', 'scripts/finder/fault-control.py', 'events', str(evidence / 'events.json')])
run(['python3', 'scripts/finder/snapshot-journal.py', str(state),
     meta['remote_root'] + '/' + name, str(evidence / 'journal.json')])
if controller is not None and controller.wait(timeout=120) != 0:
    raise SystemExit('Service signal/recovery check failed; evidence retained')
if result.returncode != 3:
    raise SystemExit('Finder recording failed; evidence retained')
events = json.loads((evidence / 'events.json').read_text())
expected_phase = 'origin_transport_error' if a.mode == 'cut-upload' else 'synthetic_status'
if a.mode == 'replay-part':
    expected_phase = 'replay_same_etag'
if controller is not None:
    expected_phase = 'held'
hits = [e for e in events if e.get('rule') in {r['id'] for r in rules}
        and e['phase'] == expected_phase]
observed = bool(hits) and (a.mode != 'session-expired' or {e.get('status') for e in hits} == {403, 404})
(evidence / 'fault-check.json').write_text(json.dumps({'matching_transport_errors': hits,
    'expected_phase': expected_phase, 'fault_observed': observed}, indent=2) + '\n')
if not observed:
    raise SystemExit('No matching transport interruption observed; do not accept this run')
if controller is not None:
    # A service stop can invalidate/unmount webdavfs; never hash a local fallback.
    for action in ['unmount', 'mount', 'check']:
        with (evidence / ('remount-' + action + '.log')).open('x') as log:
            run(['python3', 'scripts/mount-webdav-macos.py', action], stdout=log, stderr=log)
run(['python3', 'scripts/finder/finish-part-evidence.py', str(evidence)])
print('Evidence saved; full indexed UI review still required.')
