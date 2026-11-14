#!/usr/bin/env python3
"""Observe one Finder upload and browse a different directory during its parts.

This records facts and resource samples; it never assigns system-test PASS.
"""
import argparse
import atexit
import datetime
import http.client
import json
import math
from pathlib import Path
import subprocess
import time

ROOT = Path(__file__).resolve().parents[2]
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('session', type=Path)
p.add_argument('evidence', type=Path)
p.add_argument('filename')
p.add_argument('bytes', type=int)
p.add_argument('--files-spec', type=Path, help='optional distinct relative paths and sizes for a directory upload')
p.add_argument('--browse-spec', type=Path, required=True, help='native browse fixture specification')
p.add_argument('--metadata-path', default='/baseline-recovery.bin')
p.add_argument('--seconds', type=int, default=180)
a = p.parse_args()
if Path(a.filename).name != a.filename or a.bytes <= 0 or not 10 <= a.seconds <= 600:
    p.error('regular basename, positive size and 10..600 seconds required')
out = a.evidence.resolve()
if ROOT / 'evidence' not in out.parents:
    p.error('evidence must be inside repository')
out.mkdir(exist_ok=False)
plan = json.loads((a.session / 'fault-session.json').read_text())
env = plan['environment']
container = 'tboxrclone-webdav-1' if plan.get('transport') == 'direct' else 'tboxrclone-webdav-faults-1'
info = json.loads(subprocess.check_output(['docker', 'inspect', container]))[0]
actual = dict(s.split('=', 1) for s in info['Config']['Env'])
if not info['State']['Running'] or env['TBOX_LAB_REMOTE'] not in info['Config']['Cmd'] or actual.get('RCLONE_CONFIG_SJTU_STATE_DIR') != env['TBOX_STATE_DIR']:
    p.error('running service differs from session')
state = ROOT / '.state' / Path(env['TBOX_STATE_DIR']).name
remote_root = env['TBOX_LAB_REMOTE'].removeprefix('sjtu:')
expected = {remote_root + '/' + a.filename: a.bytes}
if a.files_spec:
    entries = json.loads(a.files_spec.read_text())['files']
    if not entries or len(entries) > 32:
        p.error('files spec requires 1..32 distinct files')
    expected = {}
    for entry in entries:
        name, size = entry['path'], entry['size']
        relative = Path(name)
        if relative.is_absolute() or '..' in relative.parts or not name.startswith(a.filename + '/') or type(size) is not int or size < 0:
            p.error('each file must be inside the copied directory and have a nonnegative size')
        path = remote_root + '/' + name
        if path in expected:
            p.error('duplicate path in files spec')
        expected[path] = size
    if sum(expected.values()) != a.bytes:
        p.error('file sizes must sum to the requested total bytes')
(out/'expected-files.json').write_text(json.dumps(expected,indent=2)+'\n')
spec = json.loads(a.browse_spec.read_text())
if not a.metadata_path.startswith('/') or a.metadata_path.startswith('//'):
    p.error('metadata path must be origin-relative')
(out / 'browse-spec.json').write_text(json.dumps(spec, indent=2)+'\n')
cache = '/private/tmp/tboxrclone-swift-module-cache'
browse = None
started = time.monotonic()
last = None
samples = []
committed = False
# Separate native watcher samples at 0.5 s, even while metadata/stats calls block.
# Any matching blocking dialog interrupts observation, but is never a PASS.
dialog_file = out / 'blocking-dialog.json'
dialog_watch = subprocess.Popen(['swift', '-module-cache-path', cache,
    str(ROOT/'scripts/finder/inspect-dialog.swift'), '--watch', a.filename,
    a.filename, str(dialog_file), str(out/'stop-signal'), str(a.seconds)],
    stdout=(out/'dialog-watch.log').open('x'), stderr=subprocess.STDOUT)
blocking_dialog = False
def stop_helpers():
    for child in (dialog_watch, browse):
        if child is not None and child.poll() is None:
            child.terminate()
            try:
                child.wait(timeout=5)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait(timeout=5)
atexit.register(stop_helpers)
with (out / 'timeline.jsonl').open('x') as log:
    while time.monotonic() - started < a.seconds:
        if dialog_file.exists():
            blocking_dialog = True
            break
        watcher_status = dialog_watch.poll()
        if watcher_status == 0 and dialog_file.exists():
            blocking_dialog = True
            break
        if watcher_status not in (None, 3):
            raise RuntimeError('native dialog watcher failed; inspect dialog-watch.log')
        records = []
        for f in state.glob('*.json'):
            r = json.loads(f.read_text())
            if r.get('path') in expected and r.get('size') == expected[r['path']]:
                records.append({k: r.get(k) for k in ['id', 'path', 'state', 'size', 'sha256']})
        now = datetime.datetime.now(datetime.timezone.utc).isoformat()
        if records != last:
            log.write(json.dumps({'at': now, 'records': records})+'\n'); log.flush(); last = records
        active = any(r['state'] == 'Uploading' for r in records)
        committed = set(expected) <= {r['path'] for r in records if r['state'] == 'Committed'}
        if active and browse is None:
            begin = time.monotonic()
            subprocess.run(['swift', '-module-cache-path', cache, str(ROOT/'scripts/finder/browse.swift'), 'prepare', str(out/'browse-spec.json'), str(out/'window.json')], check=True, stdout=(out/'browse-prepare.log').open('x'), timeout=20)
            window = json.loads((out/'window.json').read_text())['window_id']
            browse = subprocess.Popen(['swift', '-module-cache-path', cache, str(ROOT/'scripts/finder/browse.swift'), 'run', str(out/'browse-spec.json'), str(window), str(out/'browse-result.json')], stdout=(out/'browse.log').open('x'), stderr=subprocess.STDOUT)
            log.write(json.dumps({'at': now, 'event': 'browse_launched_after_upload_detected', 'preparation_seconds': time.monotonic()-begin})+'\n'); log.flush()
        sample = {'at': now, 'uploading': active}
        c = http.client.HTTPConnection('127.0.0.1', 8686, timeout=5)
        begin = time.monotonic()
        try:
            c.request('HEAD', a.metadata_path)
            response = c.getresponse(); response.read()
            sample.update(metadata_seconds=time.monotonic()-begin, metadata_status=response.status)
        except Exception as e:
            sample.update(metadata_seconds=time.monotonic()-begin, metadata_error=type(e).__name__)
        finally:
            c.close()
        stats = subprocess.run(['docker','stats','--no-stream','--format','{{json .}}',container], capture_output=True, text=True, timeout=10)
        if stats.returncode == 0:
            raw = json.loads(stats.stdout)
            sample['resources'] = {k:raw[k] for k in ['CPUPerc','MemUsage','MemPerc','NetIO','BlockIO','PIDs']}
        samples.append(sample)
        (out/'samples.json').write_text(json.dumps(samples,indent=2)+'\n')
        if committed and (browse is None or browse.poll() is not None): break
        time.sleep(.2)
if dialog_watch.poll() is None:
    dialog_watch.terminate()
    dialog_watch.wait(timeout=5)
if browse is not None:
    try: browse.wait(timeout=20)
    except subprocess.TimeoutExpired:
        browse.terminate(); browse.wait(timeout=5)
latencies = sorted(s['metadata_seconds'] for s in samples)
summary = {'blocking_dialog_detected':blocking_dialog,'committed':committed,'browse_started_while_uploading':browse is not None,'browse_exit_code':browse.returncode if browse else None,'samples':len(samples),'metadata_p95_seconds':latencies[math.ceil(len(latencies)*.95)-1] if latencies else None,'metadata_all_200':all(s.get('metadata_status')==200 for s in samples),'elapsed_seconds':time.monotonic()-started,'acceptance':'REQUIRES_NATIVE_REVIEW_AND_INDEPENDENT_CONTENT_CHECK'}
(out/'summary.json').write_text(json.dumps(summary,indent=2)+'\n')
print(json.dumps(summary))
