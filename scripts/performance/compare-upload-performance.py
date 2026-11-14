#!/usr/bin/env python3
"""Compare verified WebDAV and direct API evidence in the performance workspace.

This is a performance regression check, not a Finder acceptance verdict.
Historical/missing context is reported as a limitation rather than hidden.
"""
import argparse
import json
import math
from pathlib import Path
import statistics

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('api', type=Path)
p.add_argument('webdav', type=Path)
p.add_argument('output', type=Path)
a = p.parse_args()
if a.output.exists():
    p.error('output must be fresh')
api, webdav = [json.loads(path.read_text()) for path in [a.api,a.webdav]]
def positive(value):
    return isinstance(value,(int,float)) and not isinstance(value,bool) and math.isfinite(value) and value > 0
if not api.get('pass') or api.get('state') != 'Committed' or not webdav.get('passed') or not webdav.get('source_unchanged'):
    p.error('both inputs must have successful content/commit checks')
if not positive(api.get('total_seconds')) or not positive(api.get('bytes')):
    p.error('API duration and bytes must be positive')
if api.get('bytes') != webdav.get('size') or api.get('sha256') != webdav.get('sha256') or not api.get('sha256'):
    p.error('same file size and SHA-256 required')
cases=webdav.get('cases',[])
if not cases or any(not r.get('passed') or not r.get('cloud_matches') or not r.get('committed') or not positive(r.get('total_seconds')) for r in cases):
    p.error('every sample must have independently verified content and completion')
rows=[]
for mode in sorted({r['mode'] for r in cases}):
    samples=[r['total_seconds'] for r in cases if r['mode']==mode]
    median=statistics.median(samples)
    ratio=api['total_seconds']/median
    rows.append({'mode':mode,'samples':len(samples),'median_seconds':median,
                 'api_seconds':api['total_seconds'],'throughput_ratio':ratio,'passed':ratio>=0.8})
result={'scope':'code-driven performance regression check; not Finder acceptance',
        'minimum_throughput_ratio':0.8,'api_evidence':str(a.api),'webdav_evidence':str(a.webdav),
        'size':api['bytes'],'sha256':api['sha256'],'cases':rows,'passed':all(r['passed'] for r in rows),
        'limitations':['The operator must establish comparable code, hardware and network conditions.',
                      'A single API sample and few WebDAV repetitions do not establish statistical significance.',
                      'Does not measure Finder/webdavfs input, desktop responsiveness, or UI completion.']}
a.output.write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps(result,indent=2))
raise SystemExit(0 if result['passed'] else 1)
