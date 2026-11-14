#!/usr/bin/env python3
"""Control the outbound test proxy without exposing signed URLs or credentials."""
import argparse
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[2]
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('action', choices=['events', 'rules', 'release'])
p.add_argument('value', help='new event output, rule JSON file, or held request ID')
p.add_argument('--drop', action='store_true', help='drop a held response instead of forwarding it')
a = p.parse_args()
command = ['docker', 'compose', '-f', str(ROOT / 'docker-compose.yml'),
           '-f', str(ROOT / 'docker-compose.doh.yml'), 'exec', '-T', 'fault-proxy',
           'curl', '--fail', '--silent', '--show-error', '--max-time', '15']
body = None
if a.action == 'events':
    output = Path(a.value)
    if output.exists():
        p.error('refusing to overwrite evidence')
    command += ['http://127.0.0.1:8788/events']
elif a.action == 'rules':
    body = json.dumps(json.loads(Path(a.value).read_text()))
    command += ['-X', 'PUT', '-H', 'Content-Type: application/json',
                '--data-binary', '@-', 'http://127.0.0.1:8788/rules']
else:
    request = int(a.value)
    if request <= 0:
        p.error('request ID must be positive')
    body = json.dumps({'request': request, 'forward': not a.drop})
    command += ['-X', 'POST', '-H', 'Content-Type: application/json',
                '--data-binary', '@-', 'http://127.0.0.1:8788/release']
result = subprocess.run(command, input=body, text=True, capture_output=True, check=True)
if a.action == 'events':
    events = json.loads(result.stdout)
    output.write_text(json.dumps(events, indent=2) + '\n')
    held = set()
    for event in events:
        if event['phase'] == 'held':
            held.add(event['request'])
        elif event['phase'] in ['forwarded', 'dropped_after_release', 'hold_timeout', 'client_response_interrupted']:
            held.discard(event['request'])
    print(json.dumps({'events': len(events), 'currently_held': sorted(held)}))
else:
    print(a.action + '_accepted')
