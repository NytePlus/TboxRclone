#!/usr/bin/env python3
"""Probe bounded loopback reads during outbound faults; no cloud mutations.

The existing fault service must serve an isolated lab root. This is protocol
evidence, not Finder acceptance. Rule files and response bodies are retained.
"""
import argparse
import hashlib
import http.client
import json
from pathlib import Path
import subprocess
import time

ROOT = Path(__file__).resolve().parents[2]


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('path', help='existing loopback WebDAV fixture path')
    p.add_argument('sha256', help='independently established complete content hash')
    p.add_argument('output', type=Path, help='new evidence directory')
    p.add_argument('--mode', choices=['status', 'hold', 'cut', 'ignore-range'], default='status')
    p.add_argument('--metadata', action='store_true', help='inject a metadata permission denial instead of a data-plane fault')
    p.add_argument('--status', type=int, choices=[403, 416, 503], default=503)
    p.add_argument('--range', dest='byte_range', help='inclusive START-END for the fault read')
    a = p.parse_args()
    span = None
    if a.byte_range:
        try:
            start, end = (int(x) for x in a.byte_range.split('-'))
            if not 0 <= start <= end:
                raise ValueError()
            span = (start, end)
        except ValueError:
            p.error('range must be inclusive nonnegative START-END')
    if a.metadata and (a.mode != 'status' or a.status != 403 or span):
        p.error('metadata probe requires status403 and no range')
    if a.mode == 'ignore-range' and span is None:
        p.error('ignore-range requires --range')
    if not a.path.startswith('/') or any(x in a.path for x in ('..', '?', '#', '%')):
        p.error('requires a plain absolute WebDAV fixture path')
    if len(a.sha256) != 64 or any(c not in '0123456789abcdef' for c in a.sha256):
        p.error('requires SHA-256 hex')
    a.output.mkdir(parents=True, exist_ok=False)
    rules = []
    hosts = ['s3pan.jcloud.sjtu.edu.cn'] + [f's3pan{i}.jcloud.sjtu.edu.cn' for i in range(1, 5)]
    if a.metadata:
        hosts = ['pan.sjtu.edu.cn']
    for host in hosts:
        for _ in range(12):
            rule = {'id': f'read-{a.mode}-{len(rules)}', 'host': host + ':443',
                    'method': 'GET', 'path_prefix': '/api/v1/directory/' if a.metadata else '/', 'nth': 1,
                    'action': {'status': 'respond_status', 'hold': 'hold_response', 'cut': 'cut_download', 'ignore-range': 'ignore_range'}[a.mode]}
            if a.mode == 'status':
                rule['status'] = a.status
            elif a.mode == 'cut':
                rule['bytes'] = 1
            rules.append(rule)
    (a.output / 'rules.json').write_text(json.dumps(rules, indent=2) + '\n')
    (a.output / 'clear.json').write_text('[]\n')

    def control(action, value, *extra):
        subprocess.run(['python3', str(ROOT / 'scripts/finder/fault-control.py'),
                        action, str(value), *extra], check=True, timeout=30)

    def read(name, ranged=False):
        connection = http.client.HTTPConnection('127.0.0.1', 8686, timeout=60)
        started = time.monotonic()
        result = {'transport_complete': False}
        body = b''
        try:
            headers = {'Range': f'bytes={span[0]}-{span[1]}'} if ranged else {}
            if ranged and baseline.get('etag'):
                headers['If-Match'] = baseline['etag']
            connection.request('GET', a.path, headers=headers)
            response = connection.getresponse()
            declared = response.getheader('Content-Length')
            result.update(status=response.status, declared_bytes=declared,
                          requested_range=headers.get('Range'),
                          content_range=response.getheader('Content-Range'),
                          etag=response.getheader('ETag'))
            body = response.read(8 * 1024 * 1024 + 1)
            if len(body) > 8 * 1024 * 1024:
                raise RuntimeError('response exceeds fixture limit')
            if declared is not None and len(body) != int(declared):
                raise RuntimeError(f'truncated body: {len(body)} of {declared} bytes')
            result['transport_complete'] = True
        except http.client.IncompleteRead as exc:
            body = exc.partial
            result['error'] = str(exc)
        except Exception as exc:
            result['error'] = str(exc)
        finally:
            connection.close()
            result.update(bytes=len(body), sha256=hashlib.sha256(body).hexdigest(),
                          elapsed_seconds=time.monotonic() - started)
            expected_hash = a.sha256
            if ranged:
                expected = (a.output / 'control.bin').read_bytes()[span[0]:span[1] + 1]
                expected_hash = hashlib.sha256(expected).hexdigest()
            result['matches_expected'] = result['transport_complete'] and result.get('status') == (206 if ranged else 200) and result['sha256'] == expected_hash
            if ranged:
                result['matches_expected'] = result['matches_expected'] and result.get('content_range') == f'bytes {span[0]}-{span[1]}/{baseline["bytes"]}' and result.get('etag') == baseline.get('etag')
            (a.output / (name + '.bin')).write_bytes(body)
            (a.output / (name + '.json')).write_text(json.dumps(result, indent=2) + '\n')
            print(name, json.dumps(result), flush=True)
        return result

    baseline = read('control')
    if not baseline['matches_expected']:
        raise SystemExit('baseline does not match independently established bytes')
    if span and span[1] >= baseline['bytes']:
        raise SystemExit('fault range must be valid for the independently verified fixture')
    if span and not read('range-control', ranged=True)['matches_expected']:
        raise SystemExit('baseline range does not match verified fixture bytes')
    try:
        control('rules', a.output / 'rules.json')
        fault = read('fault', ranged=span is not None)
    finally:
        control('events', a.output / 'events-before-clear.json')
        events = json.loads((a.output / 'events-before-clear.json').read_text())
        active = set()
        own = {rule['id'] for rule in rules}
        for event in events:
            if event.get('rule') not in own:
                continue
            if event['phase'] == 'held':
                active.add(event['request'])
            elif event['phase'] in ('forwarded', 'dropped_after_release', 'hold_timeout', 'client_response_interrupted'):
                active.discard(event['request'])
        for request in active:
            control('release', request, '--drop')
        control('rules', a.output / 'clear.json')
        control('events', a.output / 'events.json')
    recovery = read('recovery')
    injected = any(e.get('rule') in own and e['phase'] in
                   ('synthetic_status', 'held', 'client_response_interrupted', 'range_ignored') for e in events)
    detected = not fault['transport_complete'] or fault.get('status') not in (200, 206)
    report = {'scope': 'protocol only; not Finder acceptance', 'injection_observed': injected,
              'failure_detectable': detected, 'complete_retry_matches': fault['matches_expected'],
              'recovery_matches': recovery['matches_expected'],
              'metadata_status_correct': not a.metadata or fault.get('status') == 403,
              'pass': injected and (detected or fault['matches_expected']) and recovery['matches_expected'] and (not a.metadata or fault.get('status') == 403)}
    (a.output / 'result.json').write_text(json.dumps(report, indent=2) + '\n')
    return 0 if report['pass'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
