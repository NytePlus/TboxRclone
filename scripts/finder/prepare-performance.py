#!/usr/bin/env python3
"""Create a fresh, small WebDAV fixture for native performance browsing (setup only)."""
import argparse
import hashlib
import http.client
import json
from pathlib import Path
import time
import subprocess

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('output', type=Path)
a = p.parse_args()
info = json.loads(subprocess.check_output(['docker', 'inspect', 'tboxrclone-webdav-1']))[0]
if not info['State']['Running'] or not any(str(v).startswith('sjtu:codex-api-lab/perf-') for v in info['Config']['Cmd']):
    p.error('requires a running direct isolated perf-* laboratory service')
a.output.mkdir(exist_ok=False)
requests = []
def request(method, path, payload=None):
    c = http.client.HTTPConnection('127.0.0.1', 8686, timeout=60)
    started = time.monotonic()
    try:
        c.request(method, path, body=payload)
        r = c.getresponse(); r.read()
        requests.append({'method':method,'path':path,'status':r.status,'seconds':time.monotonic()-started})
        (a.output/'setup-requests.json').write_text(json.dumps(requests,indent=2)+'\n')
        return r.status
    finally:
        c.close()
if request('PROPFIND','/browse/') != 404:
    raise SystemExit('refusing to reuse existing or inaccessible fixture')
if request('MKCOL','/browse/') != 201:
    raise SystemExit('fixture directory creation failed')
files = {}
for i in range(6):
    name = f'item-{i:02}.txt'
    data = f'performance browsing fixture {i}\n'.encode()
    if request('PUT','/browse/'+name,data) not in (200,201,204):
        raise SystemExit('fixture upload failed')
    files[name] = {'size':len(data),'sha256':hashlib.sha256(data).hexdigest()}
(a.output/'files.json').write_text(json.dumps(files,indent=2)+'\n')
spec = {'timeout_seconds':15,'cases':[{'path':'/private/tmp/tboxrclone-webdav-501/browse','expected_count':6,'expected_last':'item-05.txt'}]}
(a.output/'browse-spec.json').write_text(json.dumps(spec,indent=2)+'\n')
print('performance fixture prepared; native browsing not yet tested')
