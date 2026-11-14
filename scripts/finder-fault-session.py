#!/usr/bin/env python3
"""Switch the single Finder service to outbound fault injection; preserve state."""
import argparse
import datetime
import json
import os
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parent.parent
COMPOSE = ['docker', 'compose', '-f', str(ROOT / 'docker-compose.yml'),
           '-f', str(ROOT / 'docker-compose.doh.yml')]
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('action', choices=['start', 'restart', 'restore'])
p.add_argument('evidence', type=Path)
p.add_argument('--run-id')
p.add_argument('--preserve-pending', action='store_true', help='explicitly restart while retaining unfinished journal and spool files')
p.add_argument('--max-spool', choices=['1', '4G'], help='restart with a one-byte fault budget or restore the normal 4G budget; never removes retained data')
p.add_argument('--transport', choices=['direct', 'faults'], help='restart the same isolated session with direct cloud access or outbound fault injection')
p.add_argument('--file-operations', action='store_true', help='enable delete/move only for a fresh isolated ops-* acceptance session')
a = p.parse_args()
if a.file_operations and (a.action != 'start' or not (a.run_id or '').startswith('ops-')):
    p.error('--file-operations requires start and a fresh ops-* run-id')
if a.max_spool and a.action != 'restart':
    p.error('--max-spool requires restart')
if a.transport and a.action not in ('start', 'restart'):
    p.error('--transport requires start or restart')
evidence = a.evidence.resolve()
if ROOT / 'evidence' not in evidence.parents:
    p.error('evidence must be below the repository evidence directory')
plan_path = evidence / 'fault-session.json'
snapshot = evidence / 'original-service.json'

def run(command, **kwargs):
    subprocess.run(command, check=True, **kwargs)

def unmount():
    run(['python3', str(ROOT / 'scripts/mount-webdav-macos.py'), 'unmount'])

if a.action == 'start':
    if not a.run_id or not re.fullmatch(r'[a-z0-9][a-z0-9-]{3,70}', a.run_id):
        p.error('start requires a unique lowercase --run-id')
    if plan_path.exists() or snapshot.exists():
        p.error('session already exists; restore it before starting a new one')
    state = 'finder-fault-' + a.run_id
    if (ROOT / '.state' / state).exists():
        p.error('state directory already exists')
    evidence.mkdir(parents=True, exist_ok=True)
    run(['python3', str(ROOT / 'scripts/live-webdav-state.py'), 'snapshot', str(snapshot)])
    original = json.loads(snapshot.read_text())
    if not original.get('present'):
        p.error('a running live service is required for restorable configuration')
    env = dict(original['compose_environment'])
    env.update(TBOX_LAB_REMOTE='sjtu:codex-api-lab/' + a.run_id,
               TBOX_STATE_DIR='/state/' + state,
               TBOX_OWNERSHIP_DIR='/state/owners-' + state,
               TBOX_FAULT_WEBDAV_PORT='8686', TBOX_LAB_OVERWRITE='true',
               TBOX_LAB_DELETE='false', TBOX_LAB_MOVE='false',
               TBOX_FAULT_HOSTS=','.join(['pan.sjtu.edu.cn:443', 's3pan.jcloud.sjtu.edu.cn:443'] +
                   [f's3pan{i}.jcloud.sjtu.edu.cn:443' for i in range(1, 5)]))
    if a.file_operations:
        env.update(TBOX_LAB_DELETE='true', TBOX_LAB_MOVE='true')
    # Save recovery instructions before touching any live service. Failure keeps
    # the plan and all test state; restore is an explicit idempotent operation.
    plan_path.write_text(json.dumps({'environment': env, 'transport': a.transport or 'faults'}, indent=2) + '\n')
    unmount()
    run(COMPOSE + ['stop', 'webdav'])
    if a.transport == 'direct':
        run(COMPOSE + ['--profile', 'live', 'up', '-d', '--wait', '--wait-timeout', '180',
                       'webdav'], env={**os.environ, **env})
    else:
        run(COMPOSE + ['--profile', 'faults', 'up', '-d', '--wait', '--wait-timeout', '180',
                       'fault-proxy', 'webdav-faults'], env={**os.environ, **env})
elif a.action == 'restart':
    plan = json.loads(plan_path.read_text())
    env = plan['environment']
    state_dir = ROOT / '.state' / Path(env['TBOX_STATE_DIR']).name
    records = [json.loads(path.read_text()) for path in state_dir.glob('*.json')]
    pending = [r for r in records if r.get('state') not in ['Committed', 'Rejected', 'Aborted']]
    if pending and not a.preserve_pending:
        p.error('pending operations present; inspect them and use --preserve-pending explicitly')
    if a.max_spool:
        stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')
        (evidence / ('fault-session-before-budget-' + stamp + '.json')).write_bytes(plan_path.read_bytes())
        env['TBOX_MAX_SPOOL'] = a.max_spool
        plan_path.write_text(json.dumps(plan, indent=2) + '\n')
    if a.transport:
        stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')
        (evidence / ('fault-session-before-transport-' + stamp + '.json')).write_bytes(plan_path.read_bytes())
        plan['transport'] = a.transport
        plan_path.write_text(json.dumps(plan, indent=2) + '\n')
    print(json.dumps({'pending_retained': [{'id': r.get('id'), 'state': r.get('state')} for r in pending]}), flush=True)
    unmount()
    # Rebuild both processes. The restarted proxy issues a new ephemeral CA;
    # its client must restart as well to load that certificate.
    run(COMPOSE + ['stop', 'webdav', 'webdav-faults', 'fault-proxy'], env={**os.environ, **env})
    if plan.get('transport', 'faults') == 'direct':
        run(COMPOSE + ['--profile', 'live', 'up', '-d', '--wait', '--wait-timeout', '180',
                       'webdav'], env={**os.environ, **env})
    else:
        run(COMPOSE + ['--profile', 'faults', 'up', '-d', '--wait', '--wait-timeout', '180',
                       'fault-proxy', 'webdav-faults'], env={**os.environ, **env})
else:
    plan = json.loads(plan_path.read_text())
    unmount()
    run(COMPOSE + ['stop', 'webdav', 'webdav-faults', 'fault-proxy'],
        env={**os.environ, **plan['environment']})
    run(['python3', str(ROOT / 'scripts/live-webdav-state.py'), 'restore', str(snapshot)])
    run(COMPOSE + ['--profile', 'live', 'up', '-d', '--wait', '--wait-timeout', '180', 'webdav'],
        env={**os.environ, **json.loads(snapshot.read_text())['compose_environment']})
run(['python3', str(ROOT / 'scripts/mount-webdav-macos.py'), 'mount'])
run(['python3', str(ROOT / 'scripts/mount-webdav-macos.py'), 'check'])
print('fault_session_' + a.action + '_complete')
