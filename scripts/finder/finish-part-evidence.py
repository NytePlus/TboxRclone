#!/usr/bin/env python3
"""Generate review sheets and verify one uploaded file; never assign UI PASS."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import json
from pathlib import Path
import subprocess
import time

ROOT = Path(__file__).resolve().parents[2]
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('evidence', type=Path)
p.add_argument('--output', type=Path, help='fresh output directory for a repeat benchmark')
p.add_argument('--content-only', action='store_true', help='recheck content without decoding an already reviewed or consolidated movie')
a = p.parse_args()
evidence = a.evidence.resolve()
meta = json.loads((evidence / 'run.json').read_text())
# Use a fresh output directory so a failed/repeated check cannot reuse old success.
out = a.output.resolve() if a.output else evidence / 'verification'
out.mkdir(exist_ok=False)
started = time.monotonic()

def run(phase, command, output=None):
    start = time.monotonic()
    utc = time.time()
    code = None
    try:
        if output:
            with output.open('x') as stream:
                code = subprocess.run(command, cwd=ROOT, stdout=stream).returncode
        else:
            code = subprocess.run(command, cwd=ROOT).returncode
        if code:
            raise RuntimeError(f'{phase} failed: exit {code}')
    finally:
        (out / (phase + '.timing.json')).write_text(json.dumps({
            'phase': phase, 'started_unix_seconds': utc,
            'elapsed_seconds': time.monotonic() - start, 'exit_code': code}, indent=2) + '\n')

def content():
    run('cloud', ['docker', 'compose', '-f', 'docker-compose.yml', '-f',
        'docker-compose.doh.yml', 'run', '--rm', '-v', str(ROOT / '.secrets') + ':/secrets:ro',
        'go-tests', 'go', 'run', './cmd/finder-inspect',
        *([meta['remote_root']+'/'+meta['filename']] if meta.get('tree') else
          ['--file',meta['filename'],meta['remote_root']])], out / 'cloud.json')
    run('reopen', ['python3', 'scripts/finder/verify-copy.py', meta['source_directory'],
        '/private/tmp/tboxrclone-webdav-501', str(out / 'cloud.json'),
        str(out / 'reopen.json'),
        *(['--cloud-name-map', str(evidence / meta['cloud_name_map'])] if meta.get('cloud_name_map') else []),
        *(['--mount-subtree', meta['mount_subtree']] if meta.get('mount_subtree') else []),
        *(['--tree',meta['filename'],'--cloud-subtree',meta['filename']] if meta.get('tree') else [meta['filename']])])

def sheets():
    run('review-generation', ['swift', '-module-cache-path',
        '/private/tmp/tboxrclone-swift-module-cache', 'scripts/finder/review-recording.swift',
        str(evidence / 'finder/display-1.mov'), str(out / 'review')])

with ThreadPoolExecutor(max_workers=2) as executor:
    jobs = [executor.submit(content)]
    if not a.content_only:
        jobs.append(executor.submit(sheets))
    errors = []
    for job in jobs:
        try:
            job.result()
        except Exception as exc:
            errors.append(str(exc))
(out / 'result.json').write_text(json.dumps({'elapsed_seconds': time.monotonic() - started,
    'errors': errors, 'ui_verdict': 'REQUIRES_SEPARATE_REVIEW'}, indent=2) + '\n')
if errors:
    raise SystemExit('; '.join(errors))
print('Content verified. Review the ten-second samples and the mandatory final task state; record the UI verdict separately.')
