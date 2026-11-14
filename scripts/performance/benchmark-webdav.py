#!/usr/bin/env python3
"""Repeatable live WebDAV performance tests, without Finder or a mounted drive.

Starts a separate Compose service on loopback8688, retains every journal and
source, and stops only that service afterward. Never marks system acceptance.
"""
import argparse
import hashlib
import http.client
import json
import os
from pathlib import Path
import re
import shutil
import statistics
import subprocess
import time

ROOT = Path(__file__).resolve().parents[2]
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('source', type=Path)
p.add_argument('output', type=Path)
p.add_argument('--run-id', required=True)
p.add_argument('--repeat', type=int, default=2)
p.add_argument('--mode', choices=['length','chunked','both'], default='both')
a = p.parse_args()
if not re.fullmatch(r'perf-code-[a-z0-9-]{3,60}', a.run_id):
    p.error('run id must be fresh perf-code-*')
source = a.source.resolve()
out = a.output.resolve()
if not source.is_file() or not 0 < source.stat().st_size <= 2 << 30 or not 1 <= a.repeat <= 5:
    p.error('requires regular1 byte..2GiB source and1..5 repetitions')
if ROOT/'evidence' not in out.parents or out.exists() or (ROOT/'.state'/a.run_id).exists():
    p.error('fresh repository evidence and state required')
modes = ['length','chunked'] if a.mode == 'both' else [a.mode]
size = source.stat().st_size
if shutil.disk_usage(ROOT).free < size*len(modes)*a.repeat+(1 << 30):
    p.error('insufficient space to retain experiment spools plus1GiB headroom')
out.mkdir(parents=True)
env = {**os.environ, 'TBOX_PERF_RUN_ID':a.run_id}
compose = ['docker','compose','-f','docker-compose.yml','-f','docker-compose.doh.yml','-f','docker-compose.performance.yml']
remote = 'codex-api-lab/'+a.run_id
with source.open('rb') as stream:
    sha = hashlib.file_digest(stream,'sha256').hexdigest()
rows = []
report = {'scope':'code-driven performance experiment; not Finder acceptance', 'source':str(source),
          'size':size,'sha256':sha,'remote_root':remote,'cases':rows,'passed':False}
tracked = ['internal/sjtu/journal/spool.go','backend/sjtu/object.go',
           'internal/sjtu/transfer/multipart.go','internal/sjtu/recovery/recovery.go',
           'scripts/performance/benchmark-webdav.py','docker-compose.performance.yml']
(out/'code-identity.json').write_text(json.dumps({
    'git_head':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),
    'files':{name:hashlib.sha256((ROOT/name).read_bytes()).hexdigest() for name in tracked}},indent=2)+'\n')
def save():
    (out/'result.json').write_text(json.dumps(report,indent=2)+'\n')
def command(name, args, timeout=180):
    with (out/(name+'.log')).open('x') as log:
        subprocess.run(args,cwd=ROOT,env=env,stdout=log,stderr=subprocess.STDOUT,check=True,timeout=timeout)

# Refuse to replace an experiment that another process may still be using.
running = subprocess.check_output(compose+['ps','--status','running','-q','performance-webdav'],cwd=ROOT,env=env,text=True).strip()
if running:
    raise SystemExit('performance service already running; inspect it before retrying')
save()
try:
    command('start',compose+['--profile','performance','up','-d','--wait','--wait-timeout','180','performance-webdav'],240)
    for repetition in range(a.repeat):
        for mode in modes:
            name = f'{mode}-{repetition+1}.bin'
            connection = http.client.HTTPConnection('127.0.0.1',8688,timeout=180)
            connection.blocksize = 1 << 20
            begin = time.monotonic()
            try:
                with source.open('rb') as stream:
                    headers = {'Content-Length':str(size)} if mode == 'length' else {}
                    connection.request('PUT','/'+name,body=stream,headers=headers,encode_chunked=mode=='chunked')
                    sent = time.monotonic()
                    response = connection.getresponse()
                    response.read()
                    status = response.status
            finally:
                connection.close()
            elapsed = time.monotonic()-begin
            row = {'mode':mode,'repetition':repetition+1,'filename':name,'status':status,
                   'send_seconds':sent-begin,'total_seconds':elapsed,'bytes_per_second':size/elapsed,'passed':False}
            rows.append(row); save()
            if status not in (201,204):
                raise RuntimeError(f'upload rejected: {status}')
            with (out/(name+'.cloud.json')).open('x') as stdout, (out/(name+'.cloud.stderr.log')).open('x') as stderr:
                subprocess.run(compose+['run','--rm','-v',str(ROOT/'.secrets')+':/secrets:ro','go-tests','go','run','./cmd/finder-inspect','--file',name,remote],cwd=ROOT,env=env,stdout=stdout,stderr=stderr,check=True,timeout=180)
            cloud = json.loads((out/(name+'.cloud.json')).read_text())
            records = [json.loads(path.read_text()) for path in (ROOT/'.state'/a.run_id).glob('*.json')]
            matches = [r for r in records if r.get('path') == remote+'/'+name]
            row['cloud_matches'] = cloud['files'].get(name) == {'size':size,'sha256':sha}
            row['committed'] = len(matches)==1 and matches[0]['state']=='Committed' and matches[0]['sha256']==sha
            row['passed'] = row['cloud_matches'] and row['committed']
            save()
            if not row['passed']:
                raise RuntimeError('content or journal verification failed')
    with source.open('rb') as stream:
        report['source_unchanged'] = hashlib.file_digest(stream,'sha256').hexdigest()==sha
    report['median_seconds'] = {m:statistics.median(r['total_seconds'] for r in rows if r['mode']==m) for m in modes}
    report['passed'] = all(r['passed'] for r in rows) and report['source_unchanged']
except Exception as exc:
    report['error'] = str(exc)
    raise
finally:
    # Keep timing-only logs; never persist credential-bearing request URLs.
    try:
        logs = subprocess.run(compose+['logs','--no-color','performance-webdav'],cwd=ROOT,env=env,capture_output=True,text=True,timeout=30)
        if logs.returncode:
            report['diagnostic_error'] = 'container log collection failed'
        (out/'upload-timings.log').write_text('\n'.join(line for line in logs.stdout.splitlines() if 'upload timing:' in line)+'\n')
    except (subprocess.SubprocessError, OSError) as exc:
        report['diagnostic_error'] = type(exc).__name__
    finally:
        # A failed diagnostic must not skip stopping our separate service.
        try:
            command('stop',compose+['stop','performance-webdav'],60)
        except (subprocess.SubprocessError, OSError) as exc:
            report['cleanup_error'] = type(exc).__name__
            report['passed'] = False
            raise
        finally:
            save()
print(json.dumps(report,indent=2))
raise SystemExit(0 if report['passed'] else 1)
