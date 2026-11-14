#!/usr/bin/env python3
"""Live WebDAV Overwrite matrix in a fresh isolated ops fixture.

Protocol actions are explicit ST-013-F coverage; Finder Replace is separate.
Retains local expected contents and a request ledger for independent verification.
"""
import argparse
import json
from pathlib import Path
import subprocess
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
BASE = 'http://127.0.0.1:8686/'
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('session', type=Path)
p.add_argument('evidence', type=Path)
a = p.parse_args()
plan = json.loads((a.session/'fault-session.json').read_text())
env = plan['environment']
remote = env['TBOX_LAB_REMOTE']
if not remote.startswith('sjtu:codex-api-lab/ops-') or plan['transport'] != 'direct' or any(env.get(k) != 'true' for k in ['TBOX_LAB_DELETE', 'TBOX_LAB_MOVE', 'TBOX_LAB_OVERWRITE']):
    p.error('requires isolated direct ops session with file operations enabled')
live = json.loads(subprocess.check_output(['docker', 'inspect', 'tboxrclone-webdav-1'], timeout=15))[0]
actual = dict(x.split('=', 1) for x in live['Config']['Env'])
if not live['State']['Running'] or remote not in live['Config']['Cmd'] or actual.get('RCLONE_CONFIG_SJTU_STATE_DIR') != env['TBOX_STATE_DIR']:
    p.error('live service differs from session')
out = a.evidence.resolve()
if ROOT/'evidence' not in out.parents:
    p.error('output must be inside evidence/')
out.mkdir(parents=True, exist_ok=False)
fixture = 'OverwriteCases-' + str(time.time_ns())
expected = out/'source'/fixture
expected.mkdir(parents=True)
ledger, cases = [], []

def request(method, name, data=None, headers=None):
    start = time.monotonic()
    req = urllib.request.Request(BASE+fixture+'/'+name, data=data, headers=headers or {}, method=method)
    try:
        response = urllib.request.urlopen(req, timeout=90)
    except urllib.error.HTTPError as exc:
        response = exc
    with response:
        status, body = response.status, response.read()
    ledger.append(dict(method=method, path=fixture+'/'+name, headers=headers or {}, status=status,
                       seconds=time.monotonic()-start))
    (out/'requests.json').write_text(json.dumps(ledger, indent=2)+'\n')
    return status, body

def directory(name):
    status, _ = request('MKCOL', name)
    if status != 201:
        raise RuntimeError(f'MKCOL {name}: {status}')
    (expected/name).mkdir(parents=True, exist_ok=True)

def put(name, content):
    status, _ = request('PUT', name, content)
    if status not in (201, 204):
        raise RuntimeError(f'PUT {name}: {status}')
    (expected/name).write_bytes(content)

def operation(method, source, target, overwrite):
    headers = {'Destination': BASE+fixture+'/'+target}
    if overwrite is not None:
        headers['Overwrite'] = overwrite
    return request(method, source, headers=headers)[0]

(out/'run.json').write_text(json.dumps(dict(filename=fixture, tree=True,
    source_directory=str(out/'source'), remote_root=remote.removeprefix('sjtu:')), indent=2)+'\n')
# UUID-like fresh name plus absence check prevents touching pre-existing data.
if request('PROPFIND', '', headers={'Depth': '0'})[0] != 404:
    raise SystemExit('fixture is not proven absent')
directory('')
for method in ['COPY', 'MOVE']:
    for label, overwrite in [('default', None), ('T', 'T'), ('F', 'F')]:
        branch = method.lower()+'-'+label
        directory(branch)
        source, target = branch+'/source.txt', branch+'/target.txt'
        old, new = b'original target must survive rejection\n', b'complete replacement content\n'
        put(source, new)
        put(target, old)
        status = operation(method, source, target, overwrite)
        wanted = 412 if overwrite == 'F' else 204
        accepted = status == wanted
        if overwrite != 'F':
            (expected/target).write_bytes(new)
            if method == 'MOVE':
                (expected/source).unlink()
        target_status, body = request('GET', target)
        source_status, source_body = request('GET', source)
        intact = target_status == 200 and body == (old if overwrite == 'F' else new)
        source_ok = (source_status == 404 if method == 'MOVE' and overwrite != 'F'
                     else source_status == 200 and source_body == new)
        cases.append(dict(id='overwrite_'+label, method=method, status=status,
                          expected_status=wanted, target_matches=intact, source_matches=source_ok,
                          passed=accepted and intact and source_ok))
        (out/'protocol-results.json').write_text(json.dumps(cases, indent=2)+'\n')
    for direction in ['file-to-directory', 'directory-to-file']:
        branch = method.lower()+'-'+direction
        directory(branch)
        file, tree = branch+'/file.txt', branch+'/tree'
        put(file, b'file conflict sentinel\n')
        directory(tree)
        put(tree+'/keep.txt', b'directory conflict sentinel\n')
        source, target = (file, tree) if direction == 'file-to-directory' else (tree, file)
        status = operation(method, source, target, 'T')
        file_status, file_body = request('GET', file)
        tree_status, tree_body = request('GET', tree+'/keep.txt')
        intact = file_status == tree_status == 200 and file_body == (expected/file).read_bytes() and tree_body == (expected/tree/'keep.txt').read_bytes()
        cases.append(dict(id='file_directory_conflict', method=method, direction=direction,
                          status=status, sentinels_intact=intact, passed=400 <= status < 500 and intact))
        (out/'protocol-results.json').write_text(json.dumps(cases, indent=2)+'\n')
print(json.dumps(cases, indent=2))
raise SystemExit(0 if all(case['passed'] for case in cases) else 1)
