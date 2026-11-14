#!/usr/bin/env python3
"""Supplement Finder acceptance with bounded live WebDAV lock predicates.

Uses fresh fixtures only; retains all content and never sends DELETE or MOVE.
"""
import argparse
import hashlib
import http.client
import ipaddress
import json
from pathlib import Path
import subprocess
import time
from urllib.parse import urlsplit
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('output', type=Path, help='new evidence directory')
    parser.add_argument('--url', default='http://127.0.0.1:8686')
    parser.add_argument('--restart-session', type=Path,
                        help='existing fault session to restart with all pending state retained')
    args = parser.parse_args()
    origin = urlsplit(args.url)
    if (origin.scheme != 'http' or not ipaddress.ip_address(origin.hostname).is_loopback
            or origin.path not in ('', '/') or origin.username or origin.query or origin.fragment):
        parser.error('requires a loopback HTTP origin')
    args.output.mkdir(parents=True, exist_ok=False)
    prefix = '/lock-contract-' + uuid.uuid4().hex
    report = {'scope': 'protocol supplement, not Finder acceptance', 'fixture': prefix,
              'cases': [], 'requests': [], 'pass': False}

    def request(method, path, body=b'', headers=None):
        started = time.monotonic()
        connection = http.client.HTTPConnection(origin.hostname, origin.port or 80, timeout=60)
        try:
            connection.request(method, path, body=body, headers=headers or {})
            response = connection.getresponse()
            data = response.read(1024 * 1024 + 1)
            if len(data) > 1024 * 1024:
                raise RuntimeError('oversize response')
            # HTTPResponse.read(amt) can silently return fewer bytes than
            # Content-Length. Preserve this as a transport failure, not a
            # claim that a rejected write changed the accepted object.
            declared = response.getheader('Content-Length')
            complete = (method == 'HEAD' or declared is None or len(data) == int(declared))
            report['requests'].append({'method': method, 'path': path,
                'status': response.status, 'elapsed_seconds': time.monotonic() - started,
                'body_bytes': len(data), 'body_sha256': hashlib.sha256(data).hexdigest(),
                'declared_bytes': declared, 'transport_complete': complete})
            if method == 'GET':
                (args.output / f'response-{len(report["requests"]):03d}.bin').write_bytes(data)
            with (args.output / 'requests.jsonl').open('a') as stream:
                stream.write(json.dumps(report['requests'][-1]) + '\n')
            if not complete:
                raise RuntimeError(f'truncated {method} response: {len(data)} of {declared} bytes; content preservation unverified')
            return response.status, dict(response.getheaders()), data
        finally:
            connection.close()

    def require(condition, message):
        if not condition:
            raise RuntimeError(message)

    def lock(path, seconds=120, depth='0'):
        body = b'<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>'
        status, headers, _ = request('LOCK', path, body, {
            'Content-Type': 'application/xml', 'Depth': depth, 'Timeout': f'Second-{seconds}'})
        token = next((v for k, v in headers.items() if k.lower() == 'lock-token'), '')
        require(status == 200 and token.startswith('<') and token.endswith('>'), 'LOCK failed')
        return token

    def record(name, ok, **facts):
        report['cases'].append({'id': name, 'pass': ok, **facts})

    payload = b'TboxRclone lock contract baseline\n'
    updated = b'TboxRclone valid lock complete replacement\n'
    try:
        require(request('MKCOL', prefix)[0] == 201, 'fresh collection setup failed')
        file = prefix + '/file.txt'
        require(request('PUT', file, payload)[0] == 201, 'control PUT failed')
        require(request('GET', file)[2] == payload, 'control read differs')
        token = lock(file)
        for name, headers, allowed in [
                ('missing_token', {}, (423,)),
                ('wrong_token', {'If': '(<urn:uuid:invalid>)'}, (412,))]:
            status = request('PUT', file, b'must not replace', headers)[0]
            read_status, _, data = request('GET', file)
            record(name, status in allowed and read_status == 200 and data == payload,
                   http_status=status, original_preserved=data == payload)
        status = request('PUT', file, updated, {'If': '(' + token + ')'})[0]
        read_status, _, data = request('GET', file)
        record('valid_lock_token', status in (200, 201, 204) and read_status == 200 and data == updated,
               http_status=status, sha256=hashlib.sha256(data).hexdigest())
        require(request('UNLOCK', file, headers={'Lock-Token': token})[0] == 204, 'UNLOCK failed')
        token = lock(file, seconds=1)
        time.sleep(1.2)
        status = request('PUT', file, payload, {'If': '(' + token + ')'})[0]
        read_status, _, data = request('GET', file)
        record('expired_token', status == 412 and read_status == 200 and data == updated,
               http_status=status, accepted_content_preserved=data == updated)
        parent_token = lock(prefix + '/', depth='infinity')
        child = prefix + '/child.txt'
        status = request('PUT', child, payload)[0]
        absent = request('HEAD', child)[0] == 404
        accepted = request('PUT', child, payload, {'If': '(' + parent_token + ')'})[0]
        read_status, _, data = request('GET', child)
        record('parent_lock', status == 423 and absent and accepted in (200, 201, 204)
               and read_status == 200 and data == payload,
               rejected_http=status, absent_after_rejection=absent, valid_token_http=accepted)
        require(request('UNLOCK', prefix + '/', headers={'Lock-Token': parent_token})[0] == 204,
                'parent UNLOCK failed')
        if args.restart_session:
            root = Path(__file__).resolve().parents[2]
            plan = json.loads((args.restart_session / 'fault-session.json').read_text())['environment']
            require(plan.get('TBOX_LAB_DELETE') == 'false' and plan.get('TBOX_LAB_MOVE') == 'false',
                    'restart session must keep deletion and move disabled')
            container = 'tboxrclone-webdav-faults-1'
            def identity():
                raw = json.loads(subprocess.check_output(['docker', 'inspect', container], text=True))[0]
                require(raw['State']['Running'], 'service not running')
                require(plan['TBOX_LAB_REMOTE'] in raw['Config']['Cmd'], 'session remote mismatch')
                env = dict(v.split('=', 1) for v in raw['Config']['Env'])
                require(env.get('RCLONE_CONFIG_SJTU_STATE_DIR') == plan['TBOX_STATE_DIR'],
                        'session state directory mismatch')
                return {'container': raw['Id'], 'started_at': raw['State']['StartedAt']}
            before = identity()
            token = lock(file, seconds=3600)
            restart_started = time.monotonic()
            with (args.output / 'restart.log').open('x') as log:
                subprocess.run(['python3', str(root / 'scripts/finder-fault-session.py'),
                                'restart', str(args.restart_session), '--preserve-pending'],
                               stdout=log, stderr=subprocess.STDOUT, check=True, timeout=300)
            after = identity()
            elapsed = time.monotonic() - restart_started
            require(before != after and elapsed < 3600, 'restart was not proven before token expiry')
            status = request('PUT', file, payload, {'If': '(' + token + ')'})[0]
            read_status, _, data = request('GET', file)
            preserved = read_status == 200 and data == updated
            fresh = lock(file)
            accepted = request('PUT', file, updated, {'If': '(' + fresh + ')'})[0]
            final_status, _, final_data = request('GET', file)
            require(request('UNLOCK', file, headers={'Lock-Token': fresh})[0] == 204,
                    'fresh-token unlock failed')
            record('service_restart', status == 412 and preserved and accepted in (200, 201, 204)
                   and final_status == 200 and final_data == updated,
                   old_token_http=status, content_preserved=preserved, new_token_http=accepted,
                   before=before, after=after, elapsed_seconds=elapsed,
                   finder_verdict='REQUIRES_SEPARATE_REMOUNTED_FINDER_SAVE')
        report['pass'] = all(case['pass'] for case in report['cases'])
    except Exception as exc:
        report['error'] = str(exc)
    finally:
        (args.output / 'protocol.json').write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps({'pass': report['pass'], 'cases': report['cases'], 'error': report.get('error')}))
    return 0 if report['pass'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
