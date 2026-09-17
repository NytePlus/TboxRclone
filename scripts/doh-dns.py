#!/usr/bin/env python3
"""Optional lab DNS relay: forward DNS wire messages over authenticated HTTPS.

No persistent cache, query logging, credentials, or host DNS configuration changes.
The IP endpoint has a publicly trusted IP certificate; TLS verification stays on.
"""
import socketserver
import struct
import threading
import urllib.request

ENDPOINT = 'https://8.8.8.8/dns-query'
slots = threading.BoundedSemaphore(32)
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def resolve(query):
    if len(query) < 12:
        return b''
    try:
        if not slots.acquire(blocking=False):
            raise RuntimeError('busy')
        try:
            request = urllib.request.Request(ENDPOINT, data=query, headers={
                'Content-Type': 'application/dns-message',
                'Accept': 'application/dns-message',
            })
            with opener.open(request, timeout=5) as response:
                result = response.read(65536)
            if not 12 <= len(result) <= 65535:
                raise ValueError('invalid DNS response')
            return query[:2] + result[2:]
        finally:
            slots.release()
    except Exception:
        # SERVFAIL with the transaction ID; omit possibly compressed sections.
        return query[:2] + struct.pack('!H', 0x8182) + b'\0' * 8


class UDP(socketserver.BaseRequestHandler):
    def handle(self):
        query, sock = self.request
        result = resolve(query)
        if result:
            sock.sendto(result, self.client_address)


class TCP(socketserver.StreamRequestHandler):
    def handle(self):
        self.connection.settimeout(10)
        header = self.rfile.read(2)
        if len(header) != 2:
            return
        size, = struct.unpack('!H', header)
        query = self.rfile.read(size)
        if len(query) != size:
            return
        result = resolve(query)
        self.wfile.write(struct.pack('!H', len(result)) + result)


if __name__ == '__main__':
    udp = socketserver.ThreadingUDPServer(('0.0.0.0', 53), UDP)
    tcp = socketserver.ThreadingTCPServer(('0.0.0.0', 53), TCP)
    udp.daemon_threads = tcp.daemon_threads = True
    threading.Thread(target=tcp.serve_forever, daemon=True).start()
    udp.serve_forever()
