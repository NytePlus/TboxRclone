#!/usr/bin/env python3
"""Generate bounded outbound slow-network rules; does not arm the proxy."""
import argparse
import json
from pathlib import Path

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('session', type=Path)
p.add_argument('output', type=Path)
p.add_argument('--mode', choices=['high-rtt', 'rate-limit'], required=True)
p.add_argument('--delay-ms', type=int, default=250)
p.add_argument('--bytes-per-second', type=int, default=1<<20)
a = p.parse_args()
plan = json.loads((a.session/'fault-session.json').read_text())
if not plan['environment']['TBOX_LAB_REMOTE'].startswith('sjtu:codex-api-lab/perf-'):
    p.error('requires an isolated perf-* session')
if a.output.exists() or not 1 <= a.delay_ms <= 10000 or not 1024 <= a.bytes_per_second <= 1<<30:
    p.error('fresh output and bounded delay/rate required')
rules = []
for host in plan['environment']['TBOX_FAULT_HOSTS'].split(','):
    methods = ['GET','HEAD','PUT','POST','PROPFIND'] if a.mode=='high-rtt' else ['PUT']
    if a.mode=='rate-limit' and not host.startswith('s3pan'):
        continue
    for method in methods:
        rule = {'id':f'slow-{len(rules)}','host':host,'method':method,'path_prefix':'/',
                'nth':1,'repeat':True,'action':'delay_request' if a.mode=='high-rtt' else 'throttle_upload'}
        if a.mode=='high-rtt':
            rule['delay_ms']=a.delay_ms
        else:
            rule.update(bytes_per_second=a.bytes_per_second,min_content_length=65536,action_key='partNumber')
        rules.append(rule)
a.output.write_text(json.dumps(rules,indent=2)+'\n')
print(json.dumps({'rules':len(rules),'mode':a.mode,'scope':'per matched request; delay is additional request latency, rate is per upload stream, not aggregate bandwidth'}))
