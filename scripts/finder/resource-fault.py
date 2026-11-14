#!/usr/bin/env python3
"""Record a Finder save rejected by a scoped control-plane resource fault."""
import argparse
import configparser
import hashlib
import http.client
import json
import os
from pathlib import Path
import subprocess
import time

ROOT = Path(__file__).resolve().parents[2]
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('session', type=Path)
p.add_argument('evidence', type=Path)
fault = p.add_mutually_exclusive_group(required=True)
fault.add_argument('--status', type=int, choices=[401, 403, 429, 503])
fault.add_argument('--cache-full', action='store_true', help='requires live max_spool=1 and retained data exceeding that budget')
p.add_argument('--quota', action='store_true')
p.add_argument('--seconds', type=int, default=45)
a = p.parse_args()
if not 30 <= a.seconds <= 180 or a.quota and a.status != 403:
    p.error('requires 30..180 seconds and quota uses HTTP 403')
plan = json.loads((a.session / 'fault-session.json').read_text())['environment']
container = json.loads(subprocess.check_output(['docker', 'inspect', 'tboxrclone-webdav-faults-1'], text=True))[0]
env = dict(v.split('=', 1) for v in container['Config']['Env'])
if (not container['State']['Running'] or plan['TBOX_LAB_REMOTE'] not in container['Config']['Cmd']
        or env.get('RCLONE_CONFIG_SJTU_STATE_DIR') != plan['TBOX_STATE_DIR']
        or env.get('RCLONE_SJTU_LAB_DELETE') != 'false' or env.get('RCLONE_SJTU_LAB_MOVE') != 'false'):
    p.error('live service does not match the safe session')
state = ROOT / '.state' / Path(plan['TBOX_STATE_DIR']).name
retained_bytes = sum(path.stat().st_size for path in state.glob('*.data'))
if a.cache_full and (env.get('RCLONE_SJTU_MAX_SPOOL') != '1' or retained_bytes <= 1):
    p.error('cache-full requires a one-byte live spool budget already exhausted by retained recovery data')
evidence = a.evidence.resolve()
if ROOT / 'evidence' not in evidence.parents:
    p.error('evidence must be inside the repository')
evidence.mkdir(parents=True, exist_ok=False)
name = evidence.name
if not all(c.isalnum() or c == '-' for c in name):
    p.error('evidence basename must be alphanumeric or hyphens')
source = Path('/private/tmp') / ('tbox-source-' + name)
source.mkdir(exist_ok=False)
filename = 'resource-control.txt'
content = (f'TboxRclone retained resource fault fixture {name}\n' * 128).encode()
(source / filename).write_bytes(content)
identity = {'size': len(content), 'sha256': hashlib.sha256(content).hexdigest()}
mount = Path('/private/tmp/tboxrclone-webdav-501')
connection = http.client.HTTPConnection('127.0.0.1', 8686, timeout=60)
connection.request('MKCOL', '/' + name)
response = connection.getresponse()
response.read()
connection.close()
if response.status != 201:
    raise SystemExit('fresh fixture collection setup failed')
config = configparser.ConfigParser(interpolation=None)
config.read(ROOT / '.secrets' / Path(plan['TBOX_RCLONE_CONFIG']).name)
library, space = config['sjtu']['library_id'], config['sjtu']['space_id']
remote = plan['TBOX_LAB_REMOTE'].removeprefix('sjtu:') + '/' + name
target = '/api/v1/file/' + library + '/' + space + '/' + remote + '/' + filename
rules = [] if a.cache_full else [{'id': f'{name}-{i}', 'host': 'pan.sjtu.edu.cn:443', 'method': 'POST',
          'path_prefix': target, 'action_key': 'multipart', 'nth': 1,
          'action': 'respond_status', 'status': a.status,
          **({'code': 'QuotaLimitReached'} if a.quota else {})} for i in range(12)]
(evidence / 'rules.json').write_text(json.dumps(rules, indent=2) + '\n')
(evidence / 'clear.json').write_text('[]\n')
(evidence / 'run.json').write_text(json.dumps({'source_directory': str(source),
    'filename': filename, 'remote_root': remote, 'status': a.status, 'quota': a.quota,
    'cache_full': a.cache_full,
    'source_before': identity}, indent=2) + '\n')

def control(action, file):
    subprocess.run(['python3', str(ROOT / 'scripts/finder/fault-control.py'), action, str(file)], check=True)

started = time.monotonic()
try:
    control('rules', evidence / 'rules.json')
    record_env = {**os.environ, 'TBOX_FINDER_RECORD_SECONDS': str(a.seconds)}
    if a.cache_full or a.quota:
        record_env['TBOX_FINDER_STOP_ERROR_TEXT'] = '没有足够的可用空间'
    recording = subprocess.run(['sh', str(ROOT / 'scripts/finder-drag.sh'), '--record', str(source),
        str(mount / name), filename, str(mount), str(evidence / 'finder')],
        env=record_env, timeout=a.seconds + 100)
finally:
    control('events', evidence / 'events.json')
    control('rules', evidence / 'clear.json')
subprocess.run(['python3', str(ROOT / 'scripts/finder/snapshot-journal.py'), str(state),
                remote + '/' + filename, str(evidence / 'journal.json')], check=True)
after = (source / filename).read_bytes()
events = json.loads((evidence / 'events.json').read_text())
hits = [event for event in events if event.get('rule') in {rule['id'] for rule in rules}
        and event['phase'] == 'synthetic_status']
(evidence / 'facts.json').write_text(json.dumps({'source_preserved': after == content,
    'cache_full': a.cache_full, 'live_spool_limit': env.get('RCLONE_SJTU_MAX_SPOOL'),
    'retained_spool_bytes_before': retained_bytes,
    'injected_responses': hits, 'elapsed_seconds': time.monotonic() - started,
    'recording_exit': recording.returncode, 'ui_verdict': 'REQUIRES_SEPARATE_REVIEW'}, indent=2) + '\n')
if recording.returncode != 3 or (not a.cache_full and not hits) or after != content:
    raise SystemExit('resource fault evidence incomplete')
print('Fault observed and source retained; inspect Finder error and independent cloud state.')
