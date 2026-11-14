#!/usr/bin/env python3
"""Run native mounted-filesystem rmdir or move cases in a fresh ops-* session.

Only the empty fixture is deleted. Keep the nonempty fixture and local source.
Independent cloud verification and final-state review follow separately.
"""
import argparse
import json
from pathlib import Path
import subprocess
import time

ROOT = Path(__file__).resolve().parents[2]
MOUNT = Path('/private/tmp/tboxrclone-webdav-501')
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--case', choices=['rmdir','move','delete-prepare'], default='rmdir')
p.add_argument('session', type=Path)
p.add_argument('evidence', type=Path)
a = p.parse_args()
plan = json.loads((a.session/'fault-session.json').read_text())
env = plan['environment']
remote = env['TBOX_LAB_REMOTE']
if not remote.startswith('sjtu:codex-api-lab/ops-') or env.get('TBOX_LAB_DELETE') != 'true' or plan.get('transport') != 'direct':
    p.error('requires isolated direct ops-* session with deletion enabled')
live = json.loads(subprocess.check_output(['docker','inspect','tboxrclone-webdav-1'],timeout=15))[0]
actual = dict(x.split('=',1) for x in live['Config']['Env'])
if not live['State']['Running'] or remote not in live['Config']['Cmd'] or actual.get('RCLONE_CONFIG_SJTU_STATE_DIR') != env['TBOX_STATE_DIR']:
    p.error('live service differs from session')
subprocess.run(['python3',str(ROOT/'scripts/mount-webdav-macos.py'),'check'],check=True,timeout=30)
out = a.evidence.resolve()
if ROOT/'evidence' not in out.parents:
    p.error('output must be in repository evidence/')
fixture = {'move':'MoveCases','rmdir':'RmdirCases','delete-prepare':'DeleteCases'}[a.case]
if a.case == 'move' and env.get('TBOX_LAB_MOVE') != 'true':
    p.error('move must be enabled in isolated session')
target = MOUNT/fixture
if target.exists():
    p.error('fixture already exists; will not overwrite it')
out.mkdir(parents=True,exist_ok=False)
source = out/'source'/fixture/({'move':'right/tree/nested','rmdir':'populated','delete-prepare':''}[a.case])
source.mkdir(parents=True)
payload = b'nonempty directories must survive rmdir\n'
(source/'keep.txt').write_bytes(payload)
steps = []
def run(label, command):
    start = time.monotonic()
    result = subprocess.run(command,capture_output=True,text=True,timeout=120)
    steps.append({'action':label,'command':command,'exit_code':result.returncode,
                  'stdout':result.stdout,'stderr':result.stderr,'seconds':time.monotonic()-start})
    (out/'commands.json').write_text(json.dumps(steps,indent=2)+'\n')
    return result.returncode
(out/'run.json').write_text(json.dumps({'filename':fixture,'tree':True,'source_directory':str(out/'source'),'remote_root':remote.removeprefix('sjtu:')},indent=2)+'\n')
if a.case == 'delete-prepare':
    # Reference stays outside the expected final tree; Finder performs deletion.
    reference = out/'reference'
    reference.mkdir()
    (reference/'delete-me.txt').write_bytes(b'isolated Finder deletion fixture\n')
    for label, command in [('prepare directory',['mkdir',str(target)]),
                           ('prepare sentinel',['cp',str(source/'keep.txt'),str(target/'keep.txt')]),
                           ('prepare deletion fixture',['cp',str(reference/'delete-me.txt'),str(target/'delete-me.txt')])]:
        if run(label, command):
            raise SystemExit(label+' failed')
    report = {'prepared':True,'requires_native_finder_delete':str(target/'delete-me.txt')}
elif a.case == 'move':
    expected=out/'source'/fixture
    (expected/'left').mkdir()
    (expected/'right/file.txt').write_bytes(b'file move must preserve bytes\n')
    commands=[('prepare directories',['mkdir','-p',str(target/'left/tree/nested'),str(target/'right')]),
              ('prepare file',['cp',str(expected/'right/file.txt'),str(target/'left/file.txt')]),
              ('prepare nested file',['cp',str(source/'keep.txt'),str(target/'left/tree/nested/keep.txt')]),
              ('move file',['mv',str(target/'left/file.txt'),str(target/'right/file.txt')]),
              ('move directory',['mv',str(target/'left/tree'),str(target/'right/tree')])]
    for label,command in commands:
        if run(label,command):
            raise SystemExit(label+' failed; source, journal and logs retained')
    report={'file_source_absent':not (target/'left/file.txt').exists(),
            'tree_source_absent':not (target/'left/tree').exists(),
            'file_intact':(target/'right/file.txt').read_bytes()==(expected/'right/file.txt').read_bytes(),
            'nested_file_intact':(target/'right/tree/nested/keep.txt').read_bytes()==payload}
else:
    for label, command in [('prepare directories',['mkdir','-p',str(target/'empty'),str(target/'populated')]),
                           ('prepare sentinel',['cp',str(source/'keep.txt'),str(target/'populated/keep.txt')])]:
        if run(label,command):
            raise SystemExit('fixture preparation failed; evidence retained')
    rejected = run('nonempty rmdir',['rmdir',str(target/'populated')]) != 0
    intact = (target/'populated/keep.txt').read_bytes() == payload
    removed = run('empty rmdir',['rmdir',str(target/'empty')]) == 0
    report = {'nonempty_rejected':rejected,'sentinel_intact':intact,
              'empty_removed':removed and not (target/'empty').exists()}
passed=all(report.values())
report.update(remote_root=remote.removeprefix('sjtu:'),acceptance='REQUIRES_INDEPENDENT_CLOUD_AND_FINAL_STATE_REVIEW')
(out/'result.json').write_text(json.dumps(report,indent=2)+'\n')
print(json.dumps(report))
raise SystemExit(0 if passed else 1)
